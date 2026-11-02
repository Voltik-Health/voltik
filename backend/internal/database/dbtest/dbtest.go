// Package dbtest gives the integration tests a PostgreSQL database migrated
// to the latest version, used as voltik_api like the real API.
//
// Set both variables to run them (see backend/README.md); without them the
// integration tests are skipped:
//
//	VOLTIK_TEST_DATABASE_URL      voltik_migrations on a database whose name ends in _test
//	VOLTIK_TEST_API_DATABASE_URL  voltik_api on the same database
//
// The tests delete every row they create. Never point them at staging or production.
package dbtest

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // pgx5:// URLs
	_ "github.com/golang-migrate/migrate/v4/source/file"     // file:// migrations
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/voltik/voltik/backend/internal/database"
)

const (
	OwnerURLEnv = "VOLTIK_TEST_DATABASE_URL"
	APIURLEnv   = "VOLTIK_TEST_API_DATABASE_URL"
)

// lockKey serialises the test packages that share the database.
const lockKey int64 = 78_0099

// Tables filled by the migrations: kept between tests.
var referenceTables = []string{
	"schema_migrations", "identity_providers", "genders", "diabetes_types", "therapy_types",
	"subscription_plans", "device_types", "meal_types", "insulin_types", "medical_specialties", "legal_documents",
}

var (
	owner  *pgxpool.Pool
	apiURL string
)

// Main runs a package's tests. With a test database configured, it first
// migrates it and takes a lock held for the whole run, so packages that share
// the database never run at the same time.
func Main(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ownerURL, api := os.Getenv(OwnerURLEnv), os.Getenv(APIURLEnv)
	if ownerURL == "" || api == "" {
		return m.Run()
	}
	fail := func(err error) int {
		fmt.Fprintln(os.Stderr, "dbtest:", err)
		return 1
	}
	if err := checkTestDatabase(ownerURL, api); err != nil {
		return fail(err)
	}
	ctx := context.Background()
	lock, err := pgx.Connect(ctx, ownerURL)
	if err != nil {
		return fail(err)
	}
	defer lock.Close(ctx) // releases the lock
	if _, err := lock.Exec(ctx, `SELECT pg_advisory_lock($1)`, lockKey); err != nil {
		return fail(err)
	}
	if err := migrateUp(ownerURL); err != nil {
		return fail(err)
	}
	if owner, err = database.Open(ctx, ownerURL); err != nil {
		return fail(err)
	}
	defer owner.Close()
	apiURL = api
	return m.Run()
}

// checkTestDatabase refuses any database whose name does not end in _test.
func checkTestDatabase(ownerURL, apiURL string) error {
	name := func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			return ""
		}
		return strings.TrimPrefix(u.Path, "/")
	}
	if n := name(ownerURL); !strings.HasSuffix(n, "_test") || n != name(apiURL) {
		return fmt.Errorf("%s and %s must name the same database, ending in _test", OwnerURLEnv, APIURLEnv)
	}
	return nil
}

func migrateUp(ownerURL string) error {
	_, file, _, _ := runtime.Caller(0) // backend/internal/database/dbtest/dbtest.go
	dir := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "database", "migrations")
	dbURL := "pgx5://" + strings.SplitN(ownerURL, "://", 2)[1]
	m, err := migrate.New("file://"+filepath.ToSlash(dir), dbURL)
	if err != nil {
		return err
	}
	defer m.Close()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

// API empties the database and returns a pool connected as voltik_api.
// It skips the test when no test database is configured.
func API(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if owner == nil {
		t.Skipf("integration test: set %s and %s to run it", OwnerURLEnv, APIURLEnv)
	}
	ctx := context.Background()
	var tables string
	err := owner.QueryRow(ctx, `
		SELECT string_agg(format('%I', tablename), ', ')
		  FROM pg_tables WHERE schemaname = 'public' AND tablename <> ALL ($1)`, referenceTables).Scan(&tables)
	if err != nil {
		t.Fatal(err)
	}
	Exec(t, `TRUNCATE `+tables+` RESTART IDENTITY CASCADE`)
	Exec(t, `DELETE FROM legal_documents WHERE version <> '1.0'`) // versions added by tests

	pool, err := database.Open(ctx, apiURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// Exec runs SQL as the owner (voltik_migrations), to prepare what the API cannot write.
func Exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := owner.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("dbtest.Exec: %v", err)
	}
}

// Count runs a SELECT count(*) ... as the owner and returns the number.
func Count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := owner.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("dbtest.Count: %v", err)
	}
	return n
}
