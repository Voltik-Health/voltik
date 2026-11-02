package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/voltik/voltik/backend/internal/database"
	"github.com/voltik/voltik/backend/internal/domain"
)

// accountColumns reads a user, their roles and their patient profile.
// Expects the aliases u (users) and p (live patients, LEFT JOIN).
const accountColumns = `
	u.id, u.email, u.full_name, u.phone, u.profile_photo_url, u.account_status, u.created_at, u.deleted_at,
	p.id,
	EXISTS (SELECT 1 FROM caregiver_links c
	          JOIN patients cp ON cp.id = c.patient_id AND cp.deleted_at IS NULL
	         WHERE c.caregiver_user_id = u.id AND c.status = 'active'),
	EXISTS (SELECT 1 FROM health_professionals h WHERE h.user_id = u.id AND h.deleted_at IS NULL),
	EXISTS (SELECT 1 FROM organization_managers m
	          JOIN organizations o ON o.id = m.organization_id AND o.deleted_at IS NULL
	         WHERE m.user_id = u.id),
	u.is_platform_admin`

func scanAccount(row pgx.Row) (domain.Account, error) {
	var a domain.Account
	var caregiver, professional, manager, admin bool
	err := row.Scan(&a.ID, &a.Email, &a.FullName, &a.Phone, &a.ProfilePhotoURL, &a.AccountStatus, &a.CreatedAt, &a.DeletedAt,
		&a.PatientID, &caregiver, &professional, &manager, &admin)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Account{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Account{}, err
	}
	a.Roles = []domain.Role{}
	for _, r := range []struct {
		has  bool
		role domain.Role
	}{
		{a.PatientID != nil, domain.RolePatient},
		{caregiver, domain.RoleCaregiver},
		{professional, domain.RoleHealthProfessional},
		{manager, domain.RoleCompanyManager},
		{admin, domain.RoleAdmin},
	} {
		if r.has {
			a.Roles = append(a.Roles, r.role)
		}
	}
	return a, nil
}

// LoadAccount reads a live account (not deleted) with its roles. It runs on
// every authenticated request, so a deleted account stops working at once.
func (s *Store) LoadAccount(ctx context.Context, userID uuid.UUID) (domain.Account, error) {
	return scanAccount(s.pool.QueryRow(ctx, `
		SELECT `+accountColumns+`
		  FROM users u
		  LEFT JOIN patients p ON p.user_id = u.id AND p.deleted_at IS NULL
		 WHERE u.id = $1 AND u.deleted_at IS NULL`, userID))
}

// FindAccountByIdentity finds the account of a provider identity, including
// an account waiting for erasure (its DeletedAt is set).
func (s *Store) FindAccountByIdentity(ctx context.Context, provider, subject string) (domain.Account, error) {
	return scanAccount(s.pool.QueryRow(ctx, `
		SELECT `+accountColumns+`
		  FROM external_identities e
		  JOIN identity_providers ip ON ip.id = e.provider_id
		  JOIN users u ON u.id = e.user_id
		  LEFT JOIN patients p ON p.user_id = u.id AND p.deleted_at IS NULL
		 WHERE ip.code = $1 AND e.provider_subject = $2`, provider, subject))
}

// ProviderActive reports whether identity_providers lists the provider as active.
func (s *Store) ProviderActive(ctx context.Context, provider string) (bool, error) {
	var active bool
	err := s.pool.QueryRow(ctx, `SELECT is_active FROM identity_providers WHERE code = $1`, provider).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return active, err
}

// RecordSignIn stores the time of the sign-in and the latest verified email of the identity.
func (s *Store) RecordSignIn(ctx context.Context, id domain.Identity) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE external_identities e
		   SET last_login_at = now(), provider_email = COALESCE($3, e.provider_email)
		  FROM identity_providers ip
		 WHERE ip.id = e.provider_id AND ip.code = $1 AND e.provider_subject = $2`,
		id.Provider, id.Subject, id.Email)
	return err
}

// CreateAccount creates the user and their first identity in one transaction (SRS UC002).
func (s *Store) CreateAccount(ctx context.Context, u domain.User, id domain.Identity) (domain.Account, error) {
	a := domain.Account{User: u, Roles: []domain.Role{}}
	a.AccountStatus = domain.AccountActive
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			INSERT INTO users (email, full_name, profile_photo_url) VALUES ($1, $2, $3)
			RETURNING id, created_at`,
			u.Email, u.FullName, u.ProfilePhotoURL).Scan(&a.ID, &a.CreatedAt)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO external_identities (user_id, provider_id, provider_subject, provider_email, last_login_at)
			SELECT $1, ip.id, $3, $4, now() FROM identity_providers ip WHERE ip.code = $2`,
			a.ID, id.Provider, id.Subject, id.Email)
		if err == nil && tag.RowsAffected() != 1 {
			err = fmt.Errorf("unknown identity provider %q", id.Provider)
		}
		return err
	})
	switch {
	case database.IsUniqueViolation(err, "uq_users_email_lower"):
		return domain.Account{}, domain.ErrEmailInUse
	case database.IsUniqueViolation(err, "uq_ext_id_provider_subject"):
		return domain.Account{}, domain.ErrIdentityTaken
	case err != nil:
		return domain.Account{}, err
	}
	return a, nil
}

// UpdateUser locks the live user, lets apply change the editable fields
// (full name and phone) and saves them.
func (s *Store) UpdateUser(ctx context.Context, userID uuid.UUID, apply func(*domain.User) error) (domain.User, error) {
	var u domain.User
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			SELECT id, email, full_name, phone, profile_photo_url, account_status, created_at, deleted_at
			  FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, userID).
			Scan(&u.ID, &u.Email, &u.FullName, &u.Phone, &u.ProfilePhotoURL, &u.AccountStatus, &u.CreatedAt, &u.DeletedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := apply(&u); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE users SET full_name = $2, phone = $3 WHERE id = $1`, u.ID, u.FullName, u.Phone)
		return err
	})
	return u, err
}
