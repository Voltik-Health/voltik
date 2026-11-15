// Package repository is the only layer that talks to PostgreSQL. Every query
// is plain SQL run with pgx as the voltik_api role.
//
// Soft-deleted rows (deleted_at IS NOT NULL) are invisible: every query
// filters them, and the integration tests check it. The one exception is
// FindAccountByIdentity, which must see accounts waiting for erasure to
// answer account_pending_deletion.
package repository

import (
	"context"
	"net/netip"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/voltik/voltik/backend/internal/domain"
)

// Store runs the queries of every service.
type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Ping checks that the database answers.
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// audit is one row of data_access_audit_log. It is always written in the
// transaction of the operation it records (SRS UC021).
type audit struct {
	actor     *uuid.UUID
	patient   *uuid.UUID
	operation string
	meta      domain.RequestMeta
}

const maxUserAgentLength = 255

func insertAudit(ctx context.Context, tx pgx.Tx, a audit) error {
	ip := a.meta.IP
	if !ip.IsValid() {
		ip = netip.IPv4Unspecified() // the column is NOT NULL
	}
	var userAgent *string
	if ua := strings.TrimSpace(a.meta.UserAgent); ua != "" {
		if utf8.RuneCountInString(ua) > maxUserAgentLength {
			ua = string([]rune(ua)[:maxUserAgentLength])
		}
		userAgent = &ua
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO data_access_audit_log (actor_user_id, target_patient_id, operation, ip_address, user_agent)
		VALUES ($1, $2, $3, $4, $5)`,
		a.actor, a.patient, a.operation, ip, userAgent)
	return err
}

// ipOrNil returns the IP for a nullable INET column, or nil when it is unknown.
func ipOrNil(ip netip.Addr) any {
	if !ip.IsValid() {
		return nil
	}
	return ip
}
