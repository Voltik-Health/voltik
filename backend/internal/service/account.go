package service

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/voltik/voltik/backend/internal/domain"
)

type AccountStore interface {
	UpdateUser(ctx context.Context, userID uuid.UUID, apply func(*domain.User) error) (domain.User, error)
	HasActiveFollowUps(ctx context.Context, userID uuid.UUID) (bool, error)
	RequestDeletion(ctx context.Context, userID uuid.UUID, patientID *uuid.UUID, meta domain.RequestMeta) error
}

// Accounts changes and deletes the signed-in user's account.
type Accounts struct {
	store AccountStore
}

func NewAccounts(store AccountStore) *Accounts {
	return &Accounts{store: store}
}

// Update applies change (name and phone) and saves it if the result is valid.
func (s *Accounts) Update(ctx context.Context, a domain.Account, change func(*domain.User)) (domain.Account, error) {
	u, err := s.store.UpdateUser(ctx, a.ID, func(u *domain.User) error {
		change(u)
		u.FullName = strings.TrimSpace(u.FullName)
		u.Phone = trimmed(u.Phone)
		return u.ValidateProfile()
	})
	if err != nil {
		return domain.Account{}, err
	}
	a.User = u
	return a, nil
}

// Delete starts the GDPR erasure of the account (SRS UC016). A clinician who
// still follows patients cannot: the administration ends the follow-ups first.
func (s *Accounts) Delete(ctx context.Context, a domain.Account, meta domain.RequestMeta) error {
	if a.HasRole(domain.RoleHealthProfessional) {
		busy, err := s.store.HasActiveFollowUps(ctx, a.ID)
		if err != nil {
			return err
		}
		if busy {
			return domain.ErrProfessionalHasPatients
		}
	}
	return s.store.RequestDeletion(ctx, a.ID, a.PatientID, meta)
}

// trimmed removes the spaces around an optional text. A blank text stays
// blank, so validation rejects it instead of silently clearing the field.
func trimmed(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return s
	}
	return &t
}
