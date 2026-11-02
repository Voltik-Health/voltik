package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/domain"
)

// AccountLoader reads a live account with its roles.
type AccountLoader interface {
	LoadAccount(ctx context.Context, userID uuid.UUID) (domain.Account, error)
}

type accountKey struct{}

// WithAccount stores the caller's account in the context.
func WithAccount(ctx context.Context, a domain.Account) context.Context {
	return context.WithValue(ctx, accountKey{}, a)
}

// AccountFrom returns the account the middleware stored for this request.
func AccountFrom(ctx context.Context) (domain.Account, bool) {
	a, ok := ctx.Value(accountKey{}).(domain.Account)
	return a, ok
}

// IsPublic reports whether the endpoint works without a session token:
// GET /v1/health and the sign-in endpoints, as in the contract (security: []).
func IsPublic(r *http.Request) bool {
	return r.URL.Path == "/v1/health" || strings.HasPrefix(r.URL.Path, "/v1/auth/")
}

// Middleware lets a request through only with a valid session token of a live,
// active account, and puts that account (with its current roles) in the
// request context. The account is read from the database on every request, so
// deleting or suspending it takes effect at once.
func Middleware(tokens *Tokens, accounts AccountLoader, writeError func(http.ResponseWriter, *http.Request, error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if IsPublic(r) || r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}
			token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || strings.TrimSpace(token) == "" {
				writeError(w, r, domain.ErrUnauthenticated)
				return
			}
			userID, err := tokens.Parse(strings.TrimSpace(token))
			if err != nil {
				writeError(w, r, err)
				return
			}
			account, err := accounts.LoadAccount(r.Context(), userID)
			if errors.Is(err, domain.ErrNotFound) {
				writeError(w, r, domain.ErrUnauthenticated) // deleted since the token was issued
				return
			}
			if err != nil {
				writeError(w, r, err)
				return
			}
			if account.AccountStatus != domain.AccountActive {
				writeError(w, r, domain.ErrAccountDisabled)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithAccount(r.Context(), account)))
		})
	}
}
