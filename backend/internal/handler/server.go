// Package handler is the HTTP layer: it implements the server interface
// generated from api/openapi.yaml, turns requests into service calls and
// service results (or errors) into the contract's responses.
package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/voltik/voltik/backend/internal/api"
	"github.com/voltik/voltik/backend/internal/auth"
	"github.com/voltik/voltik/backend/internal/domain"
)

type HealthChecker interface {
	Ping(ctx context.Context) error
}

type SignInService interface {
	SignIn(ctx context.Context, provider, token, fullName string) (domain.Session, error)
}

type AccountService interface {
	Update(ctx context.Context, a domain.Account, change func(*domain.User)) (domain.Account, error)
	Delete(ctx context.Context, a domain.Account, meta domain.RequestMeta) error
}

type PatientService interface {
	Get(ctx context.Context, a domain.Account) (domain.Patient, error)
	Create(ctx context.Context, a domain.Account, p domain.Patient, consents []domain.ConsentRequest, meta domain.RequestMeta) (domain.Patient, error)
	Update(ctx context.Context, a domain.Account, change func(*domain.Patient)) (domain.Patient, error)
}

type ConsentService interface {
	List(ctx context.Context, a domain.Account) ([]domain.Consent, error)
	Accept(ctx context.Context, a domain.Account, req domain.ConsentRequest, meta domain.RequestMeta) (domain.Consent, bool, error)
	Revoke(ctx context.Context, a domain.Account, docType string, meta domain.RequestMeta) error
}

// Deps is everything the HTTP layer needs.
type Deps struct {
	Log         *slog.Logger
	Environment string
	Version     string
	CORSOrigins map[string]bool
	Tokens      *auth.Tokens
	Accounts    auth.AccountLoader
	Health      HealthChecker
	SignIn      SignInService
	Account     AccountService
	Patients    PatientService
	Consents    ConsentService
}

// Server implements api.StrictServerInterface.
type Server struct {
	log         *slog.Logger
	environment string
	version     string
	health      HealthChecker
	signIn      SignInService
	account     AccountService
	patients    PatientService
	consents    ConsentService
}

var _ api.StrictServerInterface = (*Server)(nil)

// NewRouter builds the whole HTTP handler: the generated routes behind the
// middleware chain (request id → logging → panic recovery → CORS → body limit
// and caller metadata → authentication).
func NewRouter(d Deps) http.Handler {
	s := &Server{
		log:         d.Log,
		environment: d.Environment,
		version:     d.Version,
		health:      d.Health,
		signIn:      d.SignIn,
		account:     d.Account,
		patients:    d.Patients,
		consents:    d.Consents,
	}
	badRequestHandler := func(w http.ResponseWriter, r *http.Request, err error) {
		s.writeError(w, r, &badRequest{message: "The request is not valid: " + err.Error()})
	}
	strict := api.NewStrictHandlerWithOptions(s, nil, api.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  badRequestHandler,
		ResponseErrorHandlerFunc: s.writeError,
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.writeError(w, r, domain.ErrNotFound) // unknown route, in the contract's error format
	})
	api.HandlerWithOptions(strict, api.StdHTTPServerOptions{BaseRouter: mux, ErrorHandlerFunc: badRequestHandler})

	var h http.Handler = mux
	h = auth.Middleware(d.Tokens, d.Accounts, s.writeError)(h)
	h = withRequestMeta(h)
	h = withCORS(d.CORSOrigins, h)
	h = s.withRecovery(h)
	h = withLogging(d.Log, h)
	return withRequestID(h)
}

// account returns the caller, put in the context by the authentication middleware.
func account(ctx context.Context) (domain.Account, error) {
	a, ok := auth.AccountFrom(ctx)
	if !ok {
		return domain.Account{}, domain.ErrUnauthenticated
	}
	return a, nil
}

func (s *Server) GetHealth(ctx context.Context, _ api.GetHealthRequestObject) (api.GetHealthResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := s.health.Ping(ctx); err != nil {
		s.log.Warn("health check: database not reachable", "error", err)
		return api.GetHealth503JSONResponse{
			Code:    "service_unavailable",
			Message: "The database is not reachable.",
			Details: []api.ErrorDetail{},
		}, nil
	}
	return api.GetHealth200JSONResponse{
		Status:      "ok",
		Environment: api.HealthEnvironment(s.environment),
		Version:     s.version,
	}, nil
}
