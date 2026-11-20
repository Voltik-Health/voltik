package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/voltik/voltik/backend/internal/database"
	"github.com/voltik/voltik/backend/internal/domain"
)

// HasActiveFollowUps reports whether the user is a clinician who still follows patients.
func (s *Store) HasActiveFollowUps(ctx context.Context, userID uuid.UUID) (bool, error) {
	var has bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
		    SELECT 1 FROM health_professionals h
		      JOIN patient_professional_assignments a ON a.professional_id = h.id
		     WHERE h.user_id = $1 AND h.deleted_at IS NULL AND a.status = 'active')`, userID).Scan(&has)
	return has, err
}

// RequestDeletion starts the GDPR erasure of an account (SRS UC016), in one
// transaction: the account and the patient profile are deactivated, push
// notifications stop, paid subscriptions stop renewing, and the request is
// written to the audit log. The retention job erases it 30 days later.
func (s *Store) RequestDeletion(ctx context.Context, userID uuid.UUID, patientID *uuid.UUID, meta domain.RequestMeta) error {
	return database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE users SET account_status = 'deactivated', deleted_at = now()
			 WHERE id = $1 AND deleted_at IS NULL`, userID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		for _, stmt := range []string{
			`UPDATE patients SET deleted_at = now() WHERE user_id = $1 AND deleted_at IS NULL`,
			`UPDATE notification_tokens SET is_active = FALSE WHERE user_id = $1 AND is_active`,
			`UPDATE b2c_subscriptions SET auto_renew = FALSE WHERE payer_user_id = $1 AND auto_renew`,
		} {
			if _, err := tx.Exec(ctx, stmt, userID); err != nil {
				return err
			}
		}
		return insertAudit(ctx, tx, audit{actor: &userID, patient: patientID, operation: domain.AuditAccountDeletionRequest, meta: meta})
	})
}

// retentionLockKey is the PostgreSQL advisory lock that lets a single API
// instance run the retention job at a time.
const retentionLockKey int64 = 78_0006

// TryRetentionLock takes the retention lock if no other instance holds it.
// The lock lives on one connection, kept until unlock is called.
func (s *Store) TryRetentionLock(ctx context.Context) (unlock func(), ok bool, err error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, retentionLockKey).Scan(&ok); err != nil || !ok {
		conn.Release()
		return nil, false, err
	}
	return func() {
		// A fresh context: the lock must be released even when ctx is cancelled
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, retentionLockKey)
		conn.Release()
	}, true, nil
}

// ExpiredAccounts lists up to limit accounts deleted before cutoff, oldest
// first, leaving out the ids in skip. Clinicians are never listed: their
// follow-ups and consultations keep them (ON DELETE RESTRICT).
func (s *Store) ExpiredAccounts(ctx context.Context, cutoff time.Time, limit int, skip []uuid.UUID) ([]uuid.UUID, error) {
	if skip == nil {
		skip = []uuid.UUID{}
	}
	rows, err := s.pool.Query(ctx, `
		SELECT u.id FROM users u
		 WHERE u.deleted_at < $1
		   AND NOT EXISTS (SELECT 1 FROM health_professionals h WHERE h.user_id = u.id)
		   AND u.id <> ALL ($3)
		 ORDER BY u.deleted_at, u.id
		 LIMIT $2`, cutoff, limit, skip)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// CountRetainedClinicians counts deleted accounts the job keeps because they are clinicians.
func (s *Store) CountRetainedClinicians(ctx context.Context, cutoff time.Time) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM users u
		 WHERE u.deleted_at < $1
		   AND EXISTS (SELECT 1 FROM health_professionals h WHERE h.user_id = u.id)`, cutoff).Scan(&n)
	return n, err
}

// EraseAccount deletes the user for good (SRS UC018). The foreign keys take
// everything that belongs to the account with it; the audit log keeps its rows
// with the user and the patient set to NULL. A live account is never touched.
func (s *Store) EraseAccount(ctx context.Context, userID uuid.UUID, cutoff time.Time) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id = $1 AND deleted_at < $2`, userID, cutoff)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return err
}
