package storage

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Open opens the backend's pool (application_name "paceday-backend",
// PAC-84 contract 4.3) after applying pending migrations through the
// migrate-when-stopped procedure (PAC-116 contract 3).
func Open(dsn string) (*sql.DB, error) { return OpenAs(dsn, AppBackend) }

// OpenAs is Open with the connections named appName in pg_stat_activity and
// appName as the procedure's caller. The procedure runs before the pool is
// opened, so the pool's own sessions are never "other sessions" (PAC-116
// contract 3 step 2).
func OpenAs(dsn, appName string) (*sql.DB, error) {
	if _, err := MigrateWhenStopped(context.Background(), dsn, MigrateOptions{Caller: appName}); err != nil {
		return nil, fmt.Errorf("run migrations: %w", err)
	}
	return OpenNoMigrate(dsn, appName)
}

// migrationCeiling is the highest version MigrateWhenStopped applies; 0 (always,
// outside tests) means every embedded migration.
var migrationCeiling atomic.Uint32

// SetMigrationCeilingForTest makes every Open/MigrateWhenStopped in this process stop at
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

// SchemaVersion reads schema_migrations; present is false on a database no
// migration ever ran on.
func SchemaVersion(ctx context.Context, db DBTXQuery) (version uint, dirty, present bool, err error) {
	return readVersion(ctx, db)
}
