// Package database opens the PostgreSQL connection pool and runs transactions.
package database

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Open creates the pool. It does not wait for the database: while it is down,
// GET /v1/health answers 503 and requests fail with service_unavailable.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	// Every instant is UTC, and CURRENT_DATE is the UTC date (as on the servers)
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	return pgxpool.NewWithConfig(ctx, cfg)
}

// WithTx runs fn inside a transaction: committed if fn returns nil, rolled back otherwise.
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// IsUniqueViolation reports whether err broke the unique constraint or index with this name.
func IsUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

// IsForeignKeyViolation reports whether err is a foreign key violation (e.g. ON DELETE RESTRICT).
func IsForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// IsUnavailable reports whether err means the database could not be reached.
func IsUnavailable(err error) bool {
	var connErr *pgconn.ConnectError
	var netErr net.Error
	return errors.As(err, &connErr) || errors.As(err, &netErr)
}
