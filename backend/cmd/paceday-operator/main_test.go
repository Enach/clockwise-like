package main

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/internal/testdb"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

func operatorDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn, cleanup, err := testdb.Create()
	if err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(dsn)
	if err != nil {
		_ = cleanup()
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); _ = cleanup() })
	return db
}

// PAC-71 operator command (spec §3 "Operator procedure", AC-44, AC-49): the
// three subcommands, their input checks and their reports.
func TestPAC71_OperatorCommand(t *testing.T) {
	db := operatorDB(t)
	ctx := context.Background()
	var a uuid.UUID
	if err := db.QueryRow(`INSERT INTO users (email, name) VALUES ('henry@acme.test', 'H') RETURNING id`).Scan(&a); err != nil {
		t.Fatal(err)
	}
	run1 := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := run(ctx, db, args, &out)
		return out.String(), err
	}
	for _, bad := range [][]string{
		{},
		{"nope"},
		{"free-address", "-account", "x", "-operator", "o", "-reason", "r"},
		{"free-address", "-account", a.String()},
		{"reassign", "-account", a.String(), "-identity", "bad", "-evidence", "mailbox", "-operator", "o", "-reason", "r"},
		{"reassign", "-account", a.String(), "-identity", "google:1", "-evidence", "none", "-operator", "o", "-reason", "r"},
		{"reassign", "-account", "x"},
		{"advance-all", "-operator", "o"},
		{"advance-all", "-bogus"},
	} {
		if _, err := run1(bad...); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	out, err := run1("reassign", "-account", a.String(), "-identity", "google:123", "-evidence", "authoritative", "-operator", "ops", "-reason", "verified", "-remove", "slack, zoom")
	if err != nil || !strings.Contains(out, "bound to google:123 (was none)") {
		t.Fatalf("reassign: %q %v", out, err)
	}
	out, err = run1("free-address", "-account", a.String(), "-operator", "ops", "-reason", "recycled", "-squatter")
	if err != nil || !strings.Contains(out, storage.FreedAddress(a)) {
		t.Fatalf("free-address: %q %v", out, err)
	}
	out, err = run1("advance-all", "-operator", "ops", "-reason", "OQ-6")
	if err != nil || !strings.Contains(out, "1 accounts") {
		t.Fatalf("advance-all: %q %v", out, err)
	}
	var gen int64
	_ = db.QueryRow(`SELECT session_generation FROM users WHERE id = $1`, a).Scan(&gen)
	if gen != 3 {
		t.Fatalf("gen = %d, want 3 (reassign, free, advance)", gen)
	}
	var audits int
	_ = db.QueryRow(`SELECT count(*) FROM operator_audit`).Scan(&audits)
	if audits != 3 {
		t.Fatalf("audits = %d", audits)
	}
}
