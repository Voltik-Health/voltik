// Package service holds the business rules. It knows nothing about HTTP or
// SQL: it talks to the repository through the small interfaces it declares.
package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/domain"
)

// Verifier checks a token from one identity provider (see package auth).
type Verifier interface {
	Verify(ctx context.Context, token string) (domain.Identity, error)
}

// TokenIssuer creates Voltik session tokens.
type TokenIssuer interface {
	Issue(userID uuid.UUID) (token string, expiresAt time.Time, err error)
}

type SignInStore interface {
	ProviderActive(ctx context.Context, provider string) (bool, error)
	FindAccountByIdentity(ctx context.Context, provider, subject string) (domain.Account, error)
	RecordSignIn(ctx context.Context, id domain.Identity) error
	CreateAccount(ctx context.Context, u domain.User, id domain.Identity) (domain.Account, error)
}

// SignIn exchanges an identity provider's token for a Voltik session (SRS UC002).
type SignIn struct {
	store     SignInStore
	tokens    TokenIssuer
	verifiers map[string]Verifier // only the providers configured on this server
}

func NewSignIn(store SignInStore, tokens TokenIssuer, verifiers map[string]Verifier) *SignIn {
	return &SignIn{store: store, tokens: tokens, verifiers: verifiers}
}

// SignIn checks the provider's token, creates the account on the first
// sign-in and returns a session. fullName is only used to create the account
// (Apple shares the name outside the token, and only once).
func (s *SignIn) SignIn(ctx context.Context, provider, token, fullName string) (domain.Session, error) {
	verifier, ok := s.verifiers[provider]
	if !ok {
		return domain.Session{}, domain.ErrProviderUnavailable
	}
	active, err := s.store.ProviderActive(ctx, provider)
	if err != nil {
		return domain.Session{}, err
	}
	if !active {
		return domain.Session{}, domain.ErrProviderUnavailable
	}
	id, err := verifier.Verify(ctx, token)
	if err != nil {
		return domain.Session{}, err
	}

	// Two attempts: if another request creates the same identity at the same
	// time, the second attempt finds it.
	for attempt := 1; ; attempt++ {
		account, err := s.store.FindAccountByIdentity(ctx, provider, id.Subject)
		switch {
		case err == nil:
			return s.existingAccount(ctx, account, id)
		case !errors.Is(err, domain.ErrNotFound):
			return domain.Session{}, err
		}
		account, err = s.store.CreateAccount(ctx, newUser(id, fullName), id)
		if errors.Is(err, domain.ErrIdentityTaken) && attempt == 1 {
			continue
		}
		if err != nil {
			return domain.Session{}, err
		}
		return s.session(account)
	}
}

func (s *SignIn) existingAccount(ctx context.Context, a domain.Account, id domain.Identity) (domain.Session, error) {
	if a.DeletedAt != nil {
		return domain.Session{}, &domain.PendingDeletionError{EraseAt: domain.EraseAt(*a.DeletedAt)}
	}
	if a.AccountStatus != domain.AccountActive {
		return domain.Session{}, domain.ErrAccountDisabled
	}
	if err := s.store.RecordSignIn(ctx, id); err != nil {
		return domain.Session{}, err
	}
	return s.session(a)
}

func (s *SignIn) session(a domain.Account) (domain.Session, error) {
	token, expiresAt, err := s.tokens.Issue(a.ID)
	if err != nil {
		return domain.Session{}, err
	}
	return domain.Session{Token: token, ExpiresAt: expiresAt, Account: a}, nil
}

// newUser is the user created on the first sign-in.
func newUser(id domain.Identity, fullName string) domain.User {
	name := id.FullName
	if name == "" {
		name = strings.TrimSpace(fullName)
	}
	if name == "" {
		name = domain.DefaultFullName
	}
	if r := []rune(name); len(r) > 150 {
		name = string(r[:150])
	}
	return domain.User{Email: id.Email, FullName: name, ProfilePhotoURL: id.PhotoURL}
}
