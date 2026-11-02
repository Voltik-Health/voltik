package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/domain"
)

type fakeVerifier struct {
	id  domain.Identity
	err error
}

func (v fakeVerifier) Verify(context.Context, string) (domain.Identity, error) { return v.id, v.err }

type fakeTokens struct{}

func (fakeTokens) Issue(id uuid.UUID) (string, time.Time, error) {
	return "token-" + id.String(), time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC), nil
}

func googleIdentity() domain.Identity {
	return domain.Identity{Provider: "google", Subject: "sub-1", Email: ptr("manuel@example.com"), FullName: "Manuel Sousa"}
}

func newSignIn(store *fakeStore, id domain.Identity) *SignIn {
	return NewSignIn(store, fakeTokens{}, map[string]Verifier{
		"google": fakeVerifier{id: id},
		"apple":  fakeVerifier{id: domain.Identity{Provider: "apple", Subject: "apple-1"}},
	})
}

func TestSignInCreatesTheAccountOnTheFirstSignIn(t *testing.T) {
	store := &fakeStore{}
	session, err := newSignIn(store, googleIdentity()).SignIn(context.Background(), "google", "t", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(store.created) != 1 || store.created[0].FullName != "Manuel Sousa" || *store.created[0].Email != "manuel@example.com" {
		t.Fatalf("created = %+v", store.created)
	}
	if session.Token != "token-"+session.Account.ID.String() || len(session.Account.Roles) != 0 {
		t.Fatalf("session = %+v", session)
	}
}

func TestSignInName(t *testing.T) {
	tests := []struct {
		name, provider, hint, want string
	}{
		{"from the provider", "google", "ignored", "Manuel Sousa"},
		{"from the app when the token has none (Apple)", "apple", "  Inês Sousa ", "Inês Sousa"},
		{"default", "apple", "", domain.DefaultFullName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{}
			if _, err := newSignIn(store, googleIdentity()).SignIn(context.Background(), tt.provider, "t", tt.hint); err != nil {
				t.Fatal(err)
			}
			if got := store.created[0].FullName; got != tt.want {
				t.Fatalf("full name = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSignInExistingAccount(t *testing.T) {
	deletedAt := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		account    domain.Account
		wantErr    error
		wantSignIn bool
	}{
		{"active", domain.Account{User: domain.User{ID: uuid.New(), AccountStatus: domain.AccountActive}}, nil, true},
		{"suspended", domain.Account{User: domain.User{ID: uuid.New(), AccountStatus: domain.AccountSuspended}}, domain.ErrAccountDisabled, false},
		{"waiting for erasure", domain.Account{User: domain.User{ID: uuid.New(), AccountStatus: domain.AccountDeactivated, DeletedAt: &deletedAt}}, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{accounts: map[string]domain.Account{"google/sub-1": tt.account}}
			session, err := newSignIn(store, googleIdentity()).SignIn(context.Background(), "google", "t", "")

			var pending *domain.PendingDeletionError
			switch {
			case tt.account.DeletedAt != nil:
				if !errors.As(err, &pending) || !pending.EraseAt.Equal(deletedAt.Add(30*24*time.Hour)) {
					t.Fatalf("err = %v, want erasure on %v", err, deletedAt.Add(30*24*time.Hour))
				}
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
			default:
				if err != nil || session.Account.ID != tt.account.ID {
					t.Fatalf("session = %+v, %v", session, err)
				}
			}
			if (store.signIns == 1) != tt.wantSignIn || len(store.created) != 0 {
				t.Fatalf("sign-ins recorded = %d, accounts created = %d", store.signIns, len(store.created))
			}
		})
	}
}

func TestSignInProviderUnavailable(t *testing.T) {
	store := &fakeStore{inactiveProviders: map[string]bool{"apple": true}}
	s := newSignIn(store, googleIdentity())
	for _, provider := range []string{"apple", "microsoft"} { // inactive in the database; not configured
		if _, err := s.SignIn(context.Background(), provider, "t", ""); !errors.Is(err, domain.ErrProviderUnavailable) {
			t.Fatalf("%s: err = %v", provider, err)
		}
	}
}

func TestSignInErrors(t *testing.T) {
	invalid := NewSignIn(&fakeStore{}, fakeTokens{}, map[string]Verifier{"google": fakeVerifier{err: domain.ErrInvalidProviderToken}})
	if _, err := invalid.SignIn(context.Background(), "google", "t", ""); !errors.Is(err, domain.ErrInvalidProviderToken) {
		t.Fatalf("invalid token: err = %v", err)
	}

	emailInUse := &fakeStore{createErr: domain.ErrEmailInUse}
	if _, err := newSignIn(emailInUse, googleIdentity()).SignIn(context.Background(), "google", "t", ""); !errors.Is(err, domain.ErrEmailInUse) {
		t.Fatalf("email in use: err = %v", err)
	}
}

func TestSignInRetriesWhenAnotherRequestCreatedTheIdentity(t *testing.T) {
	other := domain.Account{User: domain.User{ID: uuid.New(), AccountStatus: domain.AccountActive}}
	store := &fakeStore{racer: &other}
	session, err := newSignIn(store, googleIdentity()).SignIn(context.Background(), "google", "t", "")
	if err != nil {
		t.Fatal(err)
	}
	if session.Account.ID != other.ID || len(store.created) != 0 || store.signIns != 1 {
		t.Fatalf("session = %+v, created = %d, sign-ins = %d", session, len(store.created), store.signIns)
	}
}
