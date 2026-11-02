package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/domain"
)

type fakeLoader map[uuid.UUID]domain.Account

func (f fakeLoader) LoadAccount(_ context.Context, id uuid.UUID) (domain.Account, error) {
	a, ok := f[id]
	if !ok {
		return domain.Account{}, domain.ErrNotFound
	}
	return a, nil
}

func TestMiddleware(t *testing.T) {
	now := time.Now()
	tokens := fixedTokens("local", now)
	active := domain.Account{User: domain.User{ID: uuid.New(), AccountStatus: domain.AccountActive}, Roles: []domain.Role{domain.RolePatient}}
	suspended := domain.Account{User: domain.User{ID: uuid.New(), AccountStatus: domain.AccountSuspended}}
	deletedID := uuid.New() // not returned by LoadAccount, like a deleted account
	loader := fakeLoader{active.ID: active, suspended.ID: suspended}

	issue := func(id uuid.UUID) string {
		token, _, err := tokens.Issue(id)
		if err != nil {
			t.Fatal(err)
		}
		return "Bearer " + token
	}

	tests := []struct {
		name, method, path, auth string
		wantErr                  error // nil: the request reaches the handler
		wantAccount              bool
	}{
		{"health is public", "GET", "/v1/health", "", nil, false},
		{"sign-in is public", "POST", "/v1/auth/google", "", nil, false},
		{"CORS preflight", "OPTIONS", "/v1/me", "", nil, false},
		{"no header", "GET", "/v1/me", "", domain.ErrUnauthenticated, false},
		{"not bearer", "GET", "/v1/me", "Basic abc", domain.ErrUnauthenticated, false},
		{"bad token", "GET", "/v1/me", "Bearer abc", domain.ErrUnauthenticated, false},
		{"deleted account", "GET", "/v1/me", issue(deletedID), domain.ErrUnauthenticated, false},
		{"suspended account", "GET", "/v1/me", issue(suspended.ID), domain.ErrAccountDisabled, false},
		{"active account", "GET", "/v1/me/patient", issue(active.ID), nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotErr error
			var reached, gotAccount bool
			mw := Middleware(tokens, loader, func(w http.ResponseWriter, r *http.Request, err error) { gotErr = err })
			h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				a, ok := AccountFrom(r.Context())
				gotAccount = ok && a.ID == active.ID
			}))
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)

			if tt.wantErr != nil {
				if reached || !errors.Is(gotErr, tt.wantErr) {
					t.Fatalf("reached=%v err=%v, want %v", reached, gotErr, tt.wantErr)
				}
				return
			}
			if !reached || gotErr != nil || gotAccount != tt.wantAccount {
				t.Fatalf("reached=%v err=%v account=%v", reached, gotErr, gotAccount)
			}
		})
	}
}
