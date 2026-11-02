package storage

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
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
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	cfg.RuntimeParams["application_name"] = appName
	db := stdlib.OpenDB(*cfg)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	if err := runMigrations(db); err != nil {
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
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// ApplyPool sets the pool caps on db (PAC-81 AC-A9; values decided by
// config.DecideDBPool).
func ApplyPool(db *sql.DB, maxOpen, maxIdle int, maxLifetime, maxIdleTime time.Duration) {
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(maxLifetime)
	db.SetConnMaxIdleTime(maxIdleTime)
}
