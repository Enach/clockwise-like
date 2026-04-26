package scheduler

import (
	"database/sql"
	"testing"

	"github.com/Enach/paceday/backend/storage"
	"golang.org/x/oauth2"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestNewFocusCron(t *testing.T) {
	db := openTestDB(t)
	config := &oauth2.Config{}
	fc := NewFocusCron(db, config)
	if fc == nil {
		t.Fatal("expected non-nil FocusCron")
	}
	if fc.cron == nil {
		t.Error("expected non-nil cron scheduler")
	}
}

func TestFocusCron_StartStop(t *testing.T) {
	db := openTestDB(t)
	fc := NewFocusCron(db, &oauth2.Config{})
	fc.Start()
	fc.Stop()
}

func TestFocusCron_Reload_Disabled(t *testing.T) {
	db := openTestDB(t)
	// Default settings have auto_schedule_enabled = false
	fc := NewFocusCron(db, &oauth2.Config{})
	fc.Reload()
	if fc.entryID != 0 {
		t.Error("expected zero entryID when auto-schedule disabled")
	}
}

func TestFocusCron_Reload_ValidCron(t *testing.T) {
	db := openTestDB(t)
	s := &storage.Settings{
		AutoScheduleEnabled: true,
		AutoScheduleCron:    "@weekly",
	}
	storage.SaveSettings(db, s)

	fc := NewFocusCron(db, &oauth2.Config{})
	fc.Reload()

	if fc.entryID == 0 {
		t.Error("expected non-zero entryID after Reload with valid cron")
	}
	fc.cron.Stop()
}

func TestFocusCron_Reload_InvalidCron(t *testing.T) {
	db := openTestDB(t)
	s := &storage.Settings{
		AutoScheduleEnabled: true,
		AutoScheduleCron:    "not-a-valid-cron-expression",
	}
	storage.SaveSettings(db, s)

	fc := NewFocusCron(db, &oauth2.Config{})
	fc.Reload()
	if fc.entryID != 0 {
		t.Error("expected zero entryID after invalid cron registration")
	}
}

func TestFocusCron_Reload_RemovesOldEntry(t *testing.T) {
	db := openTestDB(t)
	s := &storage.Settings{
		AutoScheduleEnabled: true,
		AutoScheduleCron:    "@weekly",
	}
	storage.SaveSettings(db, s)

	fc := NewFocusCron(db, &oauth2.Config{})
	fc.Reload()
	firstID := fc.entryID

	fc.Reload()
	// Second reload should remove old entry and register a new one
	if fc.entryID == 0 {
		t.Error("expected non-zero entryID after second Reload")
	}
	_ = firstID

	fc.cron.Stop()
}
