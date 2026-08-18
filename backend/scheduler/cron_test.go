package scheduler

import (
	"database/sql"
	"testing"

	"github.com/Enach/paceday/backend/internal/testdb"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn, cleanup, err := testdb.Create()
	if err != nil {
		t.Fatalf("create test database: %v", err)
	}
	db, err := storage.Open(dsn)
	if err != nil {
		_ = cleanup()
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		if err := cleanup(); err != nil {
			t.Errorf("cleanup test database: %v", err)
		}
	})
	return db
}

// seedUserWithSchedule inserts a user + a settings row keyed by user_id with
// the given cron expression. Returns the user's UUID.
func seedUserWithSchedule(t *testing.T, db *sql.DB, email, cronExpr string, enabled bool) uuid.UUID {
	t.Helper()
	uid := uuid.New()
	if _, err := db.Exec(`INSERT INTO users (id, email, name, created_at) VALUES ($1, $2, $2, NOW())`, uid, email); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	// Upsert settings — defaults are inserted automatically by insertDefaultSettings;
	// here we INSERT a per-user row directly. Use minimal required columns.
	if _, err := db.Exec(`INSERT INTO settings (user_id, work_start, work_end, timezone, auto_schedule_enabled, auto_schedule_cron, updated_at)
		VALUES ($1, '09:00', '17:00', 'UTC', $2, $3, NOW())`, uid, enabled, cronExpr); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	return uid
}

func TestNewFocusCron(t *testing.T) {
	db := openTestDB(t)
	fc := NewFocusCron(db, &oauth2.Config{})
	if fc == nil {
		t.Fatal("expected non-nil FocusCron")
		return
	}
	if fc.cron == nil {
		t.Error("expected non-nil cron scheduler")
	}
	if fc.entryIDs == nil {
		t.Error("expected non-nil entryIDs map")
	}
}

func TestFocusCron_StartStop(t *testing.T) {
	db := openTestDB(t)
	fc := NewFocusCron(db, &oauth2.Config{})
	fc.Start()
	fc.Stop()
}

func TestFocusCron_Reload_NoUsers(t *testing.T) {
	db := openTestDB(t)
	fc := NewFocusCron(db, &oauth2.Config{})
	fc.Reload()
	if len(fc.entryIDs) != 0 {
		t.Errorf("expected zero entries when no users have auto-schedule, got %d", len(fc.entryIDs))
	}
}

func TestFocusCron_Reload_PerUserEntries(t *testing.T) {
	db := openTestDB(t)
	alice := seedUserWithSchedule(t, db, "alice@example.com", "@weekly", true)
	bob := seedUserWithSchedule(t, db, "bob@example.com", "@daily", true)
	// carol has auto-schedule disabled — should not be registered
	_ = seedUserWithSchedule(t, db, "carol@example.com", "@hourly", false)

	fc := NewFocusCron(db, &oauth2.Config{})
	fc.Reload()
	defer fc.cron.Stop()

	if len(fc.entryIDs) != 2 {
		t.Fatalf("expected 2 entries (alice + bob), got %d", len(fc.entryIDs))
	}
	if _, ok := fc.entryIDs[alice]; !ok {
		t.Error("alice's cron entry missing")
	}
	if _, ok := fc.entryIDs[bob]; !ok {
		t.Error("bob's cron entry missing")
	}
}

func TestFocusCron_Reload_InvalidCronSkipped(t *testing.T) {
	db := openTestDB(t)
	good := seedUserWithSchedule(t, db, "good@example.com", "@weekly", true)
	_ = seedUserWithSchedule(t, db, "bad@example.com", "not-a-valid-cron", true)

	fc := NewFocusCron(db, &oauth2.Config{})
	fc.Reload()
	defer fc.cron.Stop()

	if len(fc.entryIDs) != 1 {
		t.Fatalf("expected 1 entry (good user only), got %d", len(fc.entryIDs))
	}
	if _, ok := fc.entryIDs[good]; !ok {
		t.Error("good user's entry should be present")
	}
}

func TestFocusCron_Reload_ReplacesOldEntries(t *testing.T) {
	db := openTestDB(t)
	alice := seedUserWithSchedule(t, db, "alice@example.com", "@weekly", true)

	fc := NewFocusCron(db, &oauth2.Config{})
	fc.Reload()
	firstID, ok := fc.entryIDs[alice]
	if !ok {
		t.Fatal("alice's entry missing on first Reload")
	}

	// Reload again — same user, same cron — entry should be removed and recreated.
	fc.Reload()
	defer fc.cron.Stop()
	secondID, ok := fc.entryIDs[alice]
	if !ok {
		t.Fatal("alice's entry missing on second Reload")
	}
	if firstID == secondID {
		t.Error("expected a fresh cron.EntryID after Reload (old entries should be removed first)")
	}
}
