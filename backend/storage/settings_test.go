package storage

import (
	"database/sql"
	"testing"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestGetSettings_Defaults(t *testing.T) {
	db := openTestDB(t)
	s, err := GetSettings(db)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if s.WorkStart != "09:00" {
		t.Errorf("WorkStart = %q, want 09:00", s.WorkStart)
	}
	if s.WorkEnd != "18:00" {
		t.Errorf("WorkEnd = %q, want 18:00", s.WorkEnd)
	}
	if s.Timezone != "UTC" {
		t.Errorf("Timezone = %q, want UTC", s.Timezone)
	}
	if s.FocusMinBlockMinutes != 25 {
		t.Errorf("FocusMinBlockMinutes = %d, want 25", s.FocusMinBlockMinutes)
	}
	if s.FocusLabel != "Focus Time" {
		t.Errorf("FocusLabel = %q, want Focus Time", s.FocusLabel)
	}
	if !s.ProtectLunch {
		t.Error("ProtectLunch should be true by default")
	}
}

func TestGetSettings_Idempotent(t *testing.T) {
	db := openTestDB(t)
	s1, err := GetSettings(db)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := GetSettings(db)
	if err != nil {
		t.Fatal(err)
	}
	if s1.ID != s2.ID {
		t.Errorf("second GetSettings created a new row")
	}
}

func TestSaveSettings(t *testing.T) {
	db := openTestDB(t)

	s := &Settings{
		WorkStart:               "08:00",
		WorkEnd:                 "17:00",
		Timezone:                "America/New_York",
		FocusMinBlockMinutes:    30,
		FocusMaxBlockMinutes:    90,
		FocusDailyTargetMinutes: 180,
		FocusLabel:              "Deep Work",
		FocusColor:              "#FF0000",
		LunchStart:              "12:00",
		LunchEnd:                "13:00",
		ProtectLunch:            false,
		BufferBeforeMinutes:     10,
		BufferAfterMinutes:      10,
		CompressionEnabled:      true,
		AutoScheduleEnabled:     true,
		AutoScheduleCron:        "0 9 * * 1",
		LLMProvider:             "openai",
		LLMModel:                "gpt-4o",
		LLMAPIKey:               "sk-test",
		LLMBaseURL:              "",
	}

	if err := SaveSettings(db, s); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	got, err := GetSettings(db)
	if err != nil {
		t.Fatalf("GetSettings after save: %v", err)
	}

	if got.WorkStart != "08:00" {
		t.Errorf("WorkStart = %q, want 08:00", got.WorkStart)
	}
	if got.WorkEnd != "17:00" {
		t.Errorf("WorkEnd = %q, want 17:00", got.WorkEnd)
	}
	if got.Timezone != "America/New_York" {
		t.Errorf("Timezone = %q, want America/New_York", got.Timezone)
	}
	if got.FocusMinBlockMinutes != 30 {
		t.Errorf("FocusMinBlockMinutes = %d, want 30", got.FocusMinBlockMinutes)
	}
	if got.LLMProvider != "openai" {
		t.Errorf("LLMProvider = %q, want openai", got.LLMProvider)
	}
	if !got.CompressionEnabled {
		t.Error("CompressionEnabled should be true")
	}
	if got.ProtectLunch {
		t.Error("ProtectLunch should be false")
	}
}

func TestSaveSettings_Update(t *testing.T) {
	db := openTestDB(t)

	_, err := GetSettings(db)
	if err != nil {
		t.Fatal(err)
	}

	s := &Settings{WorkStart: "07:00", WorkEnd: "16:00", Timezone: "UTC"}
	if err := SaveSettings(db, s); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	if err := SaveSettings(db, s); err != nil {
		t.Fatalf("SaveSettings update: %v", err)
	}

	got, err := GetSettings(db)
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkStart != "07:00" {
		t.Errorf("WorkStart = %q, want 07:00", got.WorkStart)
	}
}

func TestBoolToInt(t *testing.T) {
	if boolToInt(true) != 1 {
		t.Error("boolToInt(true) should be 1")
	}
	if boolToInt(false) != 0 {
		t.Error("boolToInt(false) should be 0")
	}
}
