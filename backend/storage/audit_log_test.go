package storage

import (
	"bytes"
	"database/sql"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// PAC-23: the activity log belongs to the person it is about. Every entry
// names its subject, a read returns only the caller's own entries, and the
// entries recorded before migration 026 (no subject) are kept and returned to
// nobody.

// captureLog redirects the standard logger for the duration of the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &buf
}

func countAuditRows(t *testing.T, db *sql.DB, where string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE `+where, args...).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return n
}

// insertUnattributed stands in for a row recorded before migration 026. The
// constraint refuses such a row today, so it is dropped and re-added NOT VALID
// around the insert, in one transaction: exactly the state migration 026
// leaves a database with history in.
func insertUnattributed(t *testing.T, db *sql.DB, action, details string, at time.Time) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, stmt := range []string{
		`ALTER TABLE audit_log DROP CONSTRAINT audit_log_user_id_present`,
		`INSERT INTO audit_log (action, details, created_at) VALUES ($1, $2, $3)`,
		`ALTER TABLE audit_log ADD CONSTRAINT audit_log_user_id_present CHECK (user_id IS NOT NULL) NOT VALID`,
	} {
		var args []any
		if strings.HasPrefix(stmt, "INSERT") {
			args = []any{action, details, at}
		}
		if _, err := tx.Exec(stmt, args...); err != nil {
			t.Fatalf("insert unattributed row (%s): %v", stmt, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestWriteAuditLog_StoresSubject(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "audit-subject@example.com")

	WriteAuditLog(db, owner, "focus_created", `{"created":2}`)

	entries, err := ListAuditLog(db, owner, 0)
	if err != nil {
		t.Fatalf("ListAuditLog: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Action != "focus_created" || entries[0].Details != `{"created":2}` {
		t.Errorf("entry = %+v", entries[0])
	}
	if n := countAuditRows(t, db, `user_id = $1`, owner); n != 1 {
		t.Errorf("rows stored against the subject = %d, want 1", n)
	}
}

// AC-7: an absent subject (the zero UUID is what auth.UserIDFromContext
// returns when the context carries none) stores nothing and is logged.
func TestWriteAuditLog_RejectsNilSubject(t *testing.T) {
	db := openTestDB(t)
	logs := captureLog(t)

	WriteAuditLog(db, uuid.Nil, "meeting_created", `{"event_id":"e1"}`)

	if n := countAuditRows(t, db, `TRUE`); n != 0 {
		t.Errorf("rows stored = %d, want 0", n)
	}
	if !strings.Contains(logs.String(), "meeting_created") || !strings.Contains(logs.String(), "no subject") {
		t.Errorf("server log = %q, want a line naming the action and the missing subject", logs.String())
	}
}

// AC-7: a failed insert is reported in the server log instead of being
// discarded, and the caller is not failed (WriteAuditLog returns nothing).
func TestWriteAuditLog_ReportsInsertFailure(t *testing.T) {
	db := openTestDB(t)
	logs := captureLog(t)
	stranger := uuid.New() // no such user: the foreign key refuses the row

	WriteAuditLog(db, stranger, "nlp_parsed", `{"text":"x"}`)

	if n := countAuditRows(t, db, `TRUE`); n != 0 {
		t.Errorf("rows stored = %d, want 0", n)
	}
	if !strings.Contains(logs.String(), "nlp_parsed") || !strings.Contains(logs.String(), stranger.String()) {
		t.Errorf("server log = %q, want a line naming the action and the subject", logs.String())
	}
}

// AC-1 / AC-4: each user reads their own entries and only those.
func TestListAuditLog_ScopesByUser(t *testing.T) {
	db := openTestDB(t)
	a := mustUser(t, db, "audit-a@example.com")
	b := mustUser(t, db, "audit-b@example.com")

	WriteAuditLog(db, a, "meeting_scheduled", `{"title":"A's meeting"}`)
	WriteAuditLog(db, a, "focus_created", `{}`)
	WriteAuditLog(db, b, "meeting_scheduled", `{"title":"1:1 re: PIP"}`)

	for _, tc := range []struct {
		user uuid.UUID
		want int
		not  string
	}{{a, 2, "PIP"}, {b, 1, "A's meeting"}} {
		entries, err := ListAuditLog(db, tc.user, AuditMaxLimit)
		if err != nil {
			t.Fatalf("ListAuditLog: %v", err)
		}
		if len(entries) != tc.want {
			t.Errorf("user %s: got %d entries, want %d", tc.user, len(entries), tc.want)
		}
		for _, e := range entries {
			if strings.Contains(e.Details, tc.not) {
				t.Errorf("user %s received another user's entry %+v", tc.user, e)
			}
		}
	}
}

// AC-2: no activity is an empty, non-nil slice.
func TestListAuditLog_EmptyIsEmptySlice(t *testing.T) {
	db := openTestDB(t)
	a := mustUser(t, db, "audit-empty@example.com")
	other := mustUser(t, db, "audit-busy@example.com")
	WriteAuditLog(db, other, "focus_created", `{}`)

	entries, err := ListAuditLog(db, a, 0)
	if err != nil {
		t.Fatalf("ListAuditLog: %v", err)
	}
	if entries == nil || len(entries) != 0 {
		t.Errorf("entries = %#v, want an empty non-nil slice", entries)
	}
}

// AC-8: a row with no subject is returned for no user, including the zero UUID.
func TestListAuditLog_ExcludesNullSubjectRows(t *testing.T) {
	db := openTestDB(t)
	a := mustUser(t, db, "audit-null@example.com")
	insertUnattributed(t, db, "nlp_parsed", `{"text":"call with the recruiter"}`, time.Now())

	for _, u := range []uuid.UUID{a, uuid.Nil} {
		entries, err := ListAuditLog(db, u, AuditMaxLimit)
		if err != nil {
			t.Fatalf("ListAuditLog: %v", err)
		}
		if len(entries) != 0 {
			t.Errorf("user %s: got %d entries, want 0 (%+v)", u, len(entries), entries)
		}
	}
}

// AC-10 / AC-11: absent or non-positive -> 50; above the maximum -> 500, not 100.
func TestListAuditLog_DefaultAndClamping(t *testing.T) {
	db := openTestDB(t)
	a := mustUser(t, db, "audit-limits@example.com")
	if _, err := db.Exec(`INSERT INTO audit_log (user_id, action, details, created_at)
		SELECT $1, 'focus_created', '{}', NOW() - make_interval(secs => g) FROM generate_series(1, 520) g`, a); err != nil {
		t.Fatalf("seed rows: %v", err)
	}
	for _, tc := range []struct{ limit, want int }{
		{0, 50}, {-5, 50}, {1, 1}, {10, 10}, {500, 500}, {501, 500}, {10000, 500},
	} {
		entries, err := ListAuditLog(db, a, tc.limit)
		if err != nil {
			t.Fatalf("ListAuditLog(%d): %v", tc.limit, err)
		}
		if len(entries) != tc.want {
			t.Errorf("limit %d: got %d entries, want %d", tc.limit, len(entries), tc.want)
		}
	}
}

func TestListAuditLog_NewestFirst(t *testing.T) {
	db := openTestDB(t)
	a := mustUser(t, db, "audit-order@example.com")
	base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	for i, action := range []string{"focus_created", "meeting_scheduled", "focus_cleared"} {
		if _, err := db.Exec(`INSERT INTO audit_log (user_id, action, details, created_at) VALUES ($1, $2, '', $3)`,
			a, action, base.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := ListAuditLog(db, a, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, e := range entries {
		got = append(got, e.Action)
	}
	if strings.Join(got, ",") != "focus_cleared,meeting_scheduled,focus_created" {
		t.Errorf("order = %v", got)
	}
}

// §3.2: the database refuses a new row without a subject, and a subject that
// is not a user (the zero UUID included), whoever writes it.
func TestAuditLog_DatabaseRefusesUnattributedNewRows(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`INSERT INTO audit_log (action, details, created_at) VALUES ('x', '', NOW())`); err == nil {
		t.Error("insert with no user_id: want a check-constraint error")
	}
	if _, err := db.Exec(`INSERT INTO audit_log (user_id, action, details, created_at) VALUES ($1, 'x', '', NOW())`, uuid.Nil); err == nil {
		t.Error("insert with the zero UUID: want a foreign-key error")
	}
}

// Migration 026, both directions, on a database holding pre-migration rows.
// AC-9: the old rows survive the up migration unaltered and are told apart by
// the absence of a subject; the down migration keeps every row and loses only
// the attribution (spec §7); up again re-applies cleanly.
func TestMigration026_AuditLogUserID_UpDownUp(t *testing.T) {
	db, to := migrationTestDB(t)
	to(25)
	owner := seedMigrationUser(t, db, "m26-owner@example.com", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO audit_log (action, details, created_at) VALUES ('meeting_scheduled', '{"title":"old"}', $1)`, at); err != nil {
		t.Fatalf("seed pre-migration row: %v", err)
	}

	to(26)
	var action, details string
	var created time.Time
	var subject sql.NullString
	if err := db.QueryRow(`SELECT action, details, created_at, user_id::text FROM audit_log`).Scan(&action, &details, &created, &subject); err != nil {
		t.Fatalf("read pre-migration row after up: %v", err)
	}
	if action != "meeting_scheduled" || details != `{"title":"old"}` || !created.Equal(at) || subject.Valid {
		t.Errorf("pre-migration row after up = %q %q %v subject=%v", action, details, created, subject)
	}
	if entries, err := ListAuditLog(db, owner, AuditMaxLimit); err != nil || len(entries) != 0 {
		t.Errorf("owner reads %d pre-migration entries (err %v), want 0", len(entries), err)
	}
	if _, err := db.Exec(`INSERT INTO audit_log (action, details, created_at) VALUES ('x', '', NOW())`); err == nil {
		t.Error("after up: a new row without a subject was accepted")
	}
	WriteAuditLog(db, owner, "focus_created", `{}`)
	if n := countAuditRows(t, db, `user_id = $1`, owner); n != 1 {
		t.Errorf("after up: attributed rows = %d, want 1", n)
	}

	to(25)
	if n := countAuditRows(t, db, `TRUE`); n != 2 {
		t.Errorf("after down: rows = %d, want 2 (no row is lost)", n)
	}
	var hasColumn bool
	if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'audit_log' AND column_name = 'user_id')`).Scan(&hasColumn); err != nil {
		t.Fatal(err)
	}
	if hasColumn {
		t.Error("after down: audit_log.user_id still exists")
	}

	to(26)
	if n := countAuditRows(t, db, `user_id IS NULL`); n != 2 {
		t.Errorf("after up again: unattributed rows = %d, want 2 (down loses the attribution)", n)
	}
}
