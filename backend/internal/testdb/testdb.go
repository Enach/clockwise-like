package testdb

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Enach/paceday/backend/internal/secretbox/secretboxtest"
	_ "github.com/jackc/pgx/v5/stdlib"
)

var sequence atomic.Uint64

const fallbackDSN = "postgres://test:test@127.0.0.1:15432/testdb?sslmode=disable"

// Create creates an isolated database on the PostgreSQL instance configured for tests.
func Create() (string, func() error, error) {
	// PAC-84: every database test may write an inventory secret, which needs
	// a keyring; a random one is installed once per test process.
	secretboxtest.EnsureProcess()
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

// WaitNoSessions waits until no client session is connected to dsn's
// database (autovacuum workers are not sessions). A
// pool a test closed can linger server-side for a moment, and PAC-116's
// migrate-when-stopped procedure counts every other session on the database
// as "serving", so a test that closes its pool and then applies pending
// migrations waits here first (PAC-116 OQ-3). The check runs from the admin
// database, so it is not itself a session on dsn's database.
func WaitNoSessions(dsn string, timeout time.Duration) error {
	target, err := url.Parse(dsn)
	if err != nil {
		return err
	}
	name := strings.TrimPrefix(target.Path, "/")
	admin, err := sql.Open("pgx", AdminDSN())
	if err != nil {
		return err
	}
	defer admin.Close()
	deadline := time.Now().Add(timeout)
	for {
		var n int
		if err := admin.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE datname = $1 AND backend_type = 'client backend'`, name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%d session(s) still connected to %s after %s", n, name, timeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// AdminDSN is the DSN of the database the test databases are created from:
// a session there is not a session on any test database.
func AdminDSN() string {
	if dsn := os.Getenv("PACEDAY_TEST_DATABASE_URL"); dsn != "" {
		return dsn
	}
	return fallbackDSN
}

// WaitPIDGone waits until the server process pid has ended, for a test that
// closed one connection of its own and must not have it counted as another
// session by PAC-116's procedure.
func WaitPIDGone(pid int, timeout time.Duration) error {
	admin, err := sql.Open("pgx", AdminDSN())
	if err != nil {
		return err
	}
	defer admin.Close()
	deadline := time.Now().Add(timeout)
	for {
		var n int
		if err := admin.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE pid = $1`, pid).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("server process %d still running after %s", pid, timeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
