package api

import (
	"database/sql"
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/internal/testdb"
	"github.com/Enach/paceday/backend/storage"
)

var sharedTestDB *sql.DB

// sharedTestDSN is sharedTestDB's DSN (PAC-81: tests open a second pool on
// the same database with a cap of 1).
var sharedTestDSN string

func TestMain(m *testing.M) {
	dsn, cleanup, err := testdb.Create()
	if err != nil {
		_, _ = os.Stderr.WriteString("create test database: " + err.Error() + "\n")
		os.Exit(1)
	}
	db, err := storage.Open(dsn)
	if err != nil {
		_ = cleanup()
		_, _ = os.Stderr.WriteString("open test database: " + err.Error() + "\n")
		os.Exit(1)
	}
	sharedTestDB = db
	sharedTestDSN = dsn
	code := m.Run()
	db.Close()
	if err := cleanup(); err != nil && code == 0 {
		_, _ = os.Stderr.WriteString("cleanup test database: " + err.Error() + "\n")
		code = 1
	}
	os.Exit(code)
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return sharedTestDB
}

// pinnedSlugSuffixes pins the first n draws of a generated slug's random
// suffix to pinned and makes every later draw unique.
//
// A generated slug carries 32 bits of entropy since the 2026-11-22 security
// audit (engine.GenerateSlug): without it a default slug was guessable from
// the owner's first name, and GET /api/book/{slug} answers with every host's
// address. A test that needs two creates to pick the same slug — the collision
// PAC-126 is about — pins the draws it must collide on; the later unique draws
// let the loser's retry find a slug of its own.
func pinnedSlugSuffixes(tb engine.TB, pinned string, n int) {
	tb.Helper()
	var draws atomic.Int64
	engine.InstallSlugSuffix(tb, func() (string, error) {
		if d := draws.Add(1); d > int64(n) {
			return fmt.Sprintf("c0ffee%02d", d), nil
		}
		return pinned, nil
	})
}
