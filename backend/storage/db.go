package storage

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Open opens the backend's pool (application_name "paceday-backend",
// PAC-84 contract 4.3) and applies the migrations.
func Open(dsn string) (*sql.DB, error) { return OpenAs(dsn, AppBackend) }

// OpenAs is Open with the connections named appName in pg_stat_activity
// (the operator CLI names itself after its subcommand).
func OpenAs(dsn, appName string) (*sql.DB, error) {
	db, err := OpenNoMigrate(dsn, appName)
	if err != nil {
		return nil, err
	}
	if err := runMigrations(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}
	return db, nil
}

// runMigrations applies the embedded migrations on one dedicated
// connection and releases it afterwards. (postgres.WithInstance keeps a pooled
// connection for the life of the process, which a pool cap of 1 cannot
// afford: PAC-81 AC-A10.)
func runMigrations(db *sql.DB) error {
	ctx := context.Background()
	src, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migration connection: %w", err)
	}
	driver, err := postgres.WithConnection(ctx, conn, &postgres.Config{})
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("create migration driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "postgres", driver)
	if err != nil {
		_ = driver.Close()
		return fmt.Errorf("create migrator: %w", err)
	}
	// Close releases the connection (the driver has no pool of its own).
	defer m.Close()
	if ceiling := migrationCeiling.Load(); ceiling != 0 {
		// Tests only (SetMigrationCeilingForTest): migrate up to the
		// ceiling, never down from above it.
		cur, _, verr := m.Version()
		if verr != nil && !errors.Is(verr, migrate.ErrNilVersion) {
			return fmt.Errorf("migrate up: %w", verr)
		}
		if errors.Is(verr, migrate.ErrNilVersion) || cur < uint(ceiling) {
			if err := m.Migrate(uint(ceiling)); err != nil && err != migrate.ErrNoChange {
				return fmt.Errorf("migrate up: %w", err)
			}
		}
		return nil
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// migrationCeiling is the highest version runMigrations applies; 0 (always,
// outside tests) means every embedded migration.
var migrationCeiling atomic.Uint32

// SetMigrationCeilingForTest makes every Open/Migrate in this process stop at
// version v (it never migrates down) until the returned restore is called.
// Tests only: the PAC-84 downgrade tests hold a database at the encryption
// migration (034) although a later one (PAC-71's 035) is embedded, because
// downgrade-schema runs only from 034 (owner decision 2026-11-12, PAC-84
// contract "Stage-4 amendments").
func SetMigrationCeilingForTest(v uint) (restore func()) {
	prev := migrationCeiling.Swap(uint32(v))
	return func() { migrationCeiling.Store(prev) }
}

// ApplyPool sets the pool caps on db (PAC-81 AC-A9; values decided by
// config.DecideDBPool).
func ApplyPool(db *sql.DB, maxOpen, maxIdle int, maxLifetime, maxIdleTime time.Duration) {
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(maxLifetime)
	db.SetConnMaxIdleTime(maxIdleTime)
}

// OpenNoMigrate opens and pings the database, named appName, without
// applying migrations (PAC-84 review #3: the operator CLI must not re-apply
// this work's migration on a database downgraded below it).
func OpenNoMigrate(dsn, appName string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	cfg.RuntimeParams["application_name"] = appName
	db := stdlib.OpenDB(*cfg)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return db, nil
}

// Migrate applies the embedded migrations (what Open does after opening).
func Migrate(db *sql.DB) error {
	if err := runMigrations(db); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}

// SchemaVersion reads schema_migrations; present is false on a database no
// migration ever ran on.
func SchemaVersion(ctx context.Context, db *sql.DB) (version uint, dirty, present bool, err error) {
	var exists bool
	if err = db.QueryRowContext(ctx, `SELECT to_regclass('public.schema_migrations') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		return 0, false, false, err
	}
	var v int64
	err = db.QueryRowContext(ctx, `SELECT version, dirty FROM schema_migrations LIMIT 1`).Scan(&v, &dirty)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, false, nil
	}
	if err != nil {
		return 0, false, false, err
	}
	return uint(v), dirty, true, nil
}
