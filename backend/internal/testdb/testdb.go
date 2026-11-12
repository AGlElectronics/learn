// Package testdb prepares the disposable database behind TEST_DATABASE_URL.
//
// go test stops a package after 10 minutes. Building the schema from scratch
// before every test used that up on the CI runner: the timeout fired in
// whichever test was still applying migrations. The schema is built once per
// process. Each test then empties the tables and puts the seeded demo
// organization back, so a test does not see rows left by another and does
// not depend on which test ran first.
package testdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/AGlElectronics/learn/backend/internal/db"
	"github.com/AGlElectronics/learn/backend/migrations"
)

// demoOrg is the organization the initial migration seeds. Emptying the
// tables removes it; tests look the organization up by this slug.
const demoOrg = `
INSERT INTO organizations (id, slug, display_name)
VALUES ('00000000-0000-0000-0000-000000000010', 'demo-org', 'Demo Organization')`

var (
	mu       sync.Mutex
	migrated bool
)

// Open migrates TEST_DATABASE_URL once, empties the tables, and returns a
// pool. The test is skipped when the variable is unset. The pool is closed
// when the test ends.
func Open(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := prepare(ctx, dsn); err != nil {
		t.Fatalf("prepare test database: %v", err)
	}
	// Background pool setup keeps this context. It must not be the one
	// cancelled above, or a cancelled connect can outlive the helper.
	pool, err := db.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func prepare(ctx context.Context, dsn string) error {
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open test db: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()
	// One connection so a migration cannot wait on a lock held by another
	// connection from this same pool.
	sqlDB.SetMaxOpenConns(1)
	// A lock wait should fail the test, not burn the package timeout.
	if _, err := sqlDB.ExecContext(ctx, `SET lock_timeout = '30s'`); err != nil {
		return fmt.Errorf("set lock_timeout: %w", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !migrated {
		if err := migrate(ctx, sqlDB); err != nil {
			return err
		}
		migrated = true
	}
	return empty(ctx, sqlDB)
}

func migrate(ctx context.Context, sqlDB *sql.DB) error {
	if _, err := sqlDB.ExecContext(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		return fmt.Errorf("reset schema: %w", err)
	}
	goose.SetBaseFS(migrations.FS)
	goose.SetTableName("goose_db_version")
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("goose dialect: %w", err)
	}
	if err := goose.UpContext(ctx, sqlDB, "."); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}

func empty(ctx context.Context, sqlDB *sql.DB) error {
	var stmt string
	err := sqlDB.QueryRowContext(ctx, `
		SELECT COALESCE(
			'TRUNCATE TABLE ' || string_agg(format('%I.%I', schemaname, tablename), ', ')
				|| ' RESTART IDENTITY CASCADE',
			'')
		  FROM pg_tables
		 WHERE schemaname = 'public' AND tablename <> 'goose_db_version'`).Scan(&stmt)
	if err != nil {
		return fmt.Errorf("list tables: %w", err)
	}
	if stmt == "" {
		return errors.New("refusing to reset: public has no tables")
	}
	if _, err := sqlDB.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("empty tables: %w", err)
	}
	if _, err := sqlDB.ExecContext(ctx, demoOrg); err != nil {
		return fmt.Errorf("restore demo organization: %w", err)
	}
	return nil
}
