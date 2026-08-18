package testdb

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"sync/atomic"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

var sequence atomic.Uint64

const fallbackDSN = "postgres://test:test@127.0.0.1:15432/testdb?sslmode=disable"

// Create creates an isolated database on the PostgreSQL instance configured for tests.
func Create() (string, func() error, error) {
	dsn := os.Getenv("PACEDAY_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = fallbackDSN
	}
	base, err := url.Parse(dsn)
	if err != nil {
		return "", nil, fmt.Errorf("parse test database URL: %w", err)
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		return "", nil, fmt.Errorf("open test database admin connection: %w", err)
	}
	if err := admin.Ping(); err != nil {
		admin.Close()
		return "", nil, fmt.Errorf("connect to test database at %s: %w", base.Host, err)
	}

	name := fmt.Sprintf("paceday_test_%d_%d", os.Getpid(), sequence.Add(1)+uint64(time.Now().UnixNano()))
	quotedName := `"` + name + `"`
	if _, err := admin.Exec("CREATE DATABASE " + quotedName); err != nil {
		admin.Close()
		return "", nil, fmt.Errorf("create isolated test database %s: %w", name, err)
	}
	target := *base
	target.Path = "/" + name
	target.RawPath = ""
	cleanup := func() error {
		defer admin.Close()
		_, err := admin.Exec("DROP DATABASE IF EXISTS " + quotedName + " WITH (FORCE)")
		return err
	}
	return target.String(), cleanup, nil
}
