package storage

import (
	"database/sql"
	"testing"

	"github.com/google/uuid"
)

// PAC-24: settings belong to one user. Each test creates its own users.

func settingsTestUser(t *testing.T, db *sql.DB, email string) uuid.UUID {
	t.Helper()
	u, err := UpsertUser(db, email, email, "", "google", email)
	if err != nil {
		t.Fatalf("upsert %s: %v", email, err)
	}
	return u.ID
}

func TestGetOrCreateSettingsByUser_CreatesDefaults(t *testing.T) {
	db := openTestDB(t)
	user := settingsTestUser(t, db, "settings-defaults@example.com")
	s, err := GetOrCreateSettingsByUser(db, user)
	if err != nil {
		t.Fatalf("GetOrCreateSettingsByUser: %v", err)
	}
	if s.WorkStart != "09:00" || s.WorkEnd != "18:00" || s.Timezone != "UTC" {
		t.Errorf("work day = %s-%s %s, want 09:00-18:00 UTC", s.WorkStart, s.WorkEnd, s.Timezone)
	}
	if s.FocusMinBlockMinutes != 25 || s.FocusLabel != "Focus Time" || !s.ProtectLunch {
		t.Errorf("focus/lunch defaults wrong: %+v", s)
	}
	if s.RecapSendTime != "08:00" {
		t.Errorf("RecapSendTime = %q, want 08:00 (HH:MM, not Postgres's HH:MM:SS)", s.RecapSendTime)
	}
	if s.LLMAPIKeySet {
		t.Error("a new user has no provider key")
	}
}

func TestGetOrCreateSettingsByUser_Idempotent(t *testing.T) {
	db := openTestDB(t)
	user := settingsTestUser(t, db, "settings-idempotent@example.com")
	s1, err := GetOrCreateSettingsByUser(db, user)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := GetOrCreateSettingsByUser(db, user)
	if err != nil {
		t.Fatal(err)
	}
	if s1.ID != s2.ID {
		t.Errorf("second read created a new row: %d then %d", s1.ID, s2.ID)
	}
}

func TestGetOrCreateSettingsByUser_RejectsNilUser(t *testing.T) {
	db := openTestDB(t)
	if _, err := GetOrCreateSettingsByUser(db, uuid.Nil); err == nil {
		t.Error("nil user: want an error, never a shared row")
	}
	if err := SaveSettingsForUser(db, uuid.Nil, &Settings{}); err == nil {
		t.Error("save for nil user: want an error")
	}
}

func TestSaveSettingsForUser_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	user := settingsTestUser(t, db, "settings-save@example.com")
	s, err := GetOrCreateSettingsByUser(db, user)
	if err != nil {
		t.Fatal(err)
	}
	s.WorkStart, s.WorkEnd, s.Timezone = "08:00", "17:00", "America/New_York"
	s.FocusMinBlockMinutes = 30
	s.LLMProvider, s.LLMAPIKeys.OpenAI = "openai", "sk-test"
	s.CompressionEnabled = true
	s.WorkingHours = WorkingHoursSchedule{Mode: "by_day", Days: map[string]DaySchedule{"monday": {Enabled: true, Start: "08:00", End: "12:00"}}}
	s.LunchBreaks = LunchBreakSchedule{"friday": {Enabled: false}}
	if err := SaveSettingsForUser(db, user, s); err != nil {
		t.Fatalf("SaveSettingsForUser: %v", err)
	}

	got, err := GetOrCreateSettingsByUser(db, user)
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkStart != "08:00" || got.WorkEnd != "17:00" || got.Timezone != "America/New_York" || got.FocusMinBlockMinutes != 30 {
		t.Errorf("saved values not read back: %+v", got)
	}
	if got.LLMAPIKeys.OpenAI != "sk-test" || !got.LLMAPIKeySet || !got.CompressionEnabled {
		t.Errorf("key/compression not read back: key set=%v compression=%v", got.LLMAPIKeySet, got.CompressionEnabled)
	}
	if got.WorkingHours.Mode != "by_day" || got.WorkingHours.Days["monday"].End != "12:00" {
		t.Errorf("working hours not on the user's row: %+v", got.WorkingHours)
	}
	if _, ok := got.LunchBreaks["friday"]; !ok {
		t.Errorf("lunch breaks not on the user's row: %+v", got.LunchBreaks)
	}
}

func TestSaveSettingsForUser_LeavesOtherUsersUntouched(t *testing.T) {
	db := openTestDB(t)
	alice := settingsTestUser(t, db, "settings-alice@example.com")
	bob := settingsTestUser(t, db, "settings-bob@example.com")
	before, err := GetOrCreateSettingsByUser(db, bob)
	if err != nil {
		t.Fatal(err)
	}

	s, _ := GetOrCreateSettingsByUser(db, alice)
	s.WorkStart, s.Timezone, s.LLMAPIKeys.OpenAI = "06:00", "Asia/Tokyo", "alice-key"
	s.WorkingHours = WorkingHoursSchedule{Mode: "by_day", Days: map[string]DaySchedule{"tuesday": {Enabled: true, Start: "06:00", End: "10:00"}}}
	if err := SaveSettingsForUser(db, alice, s); err != nil {
		t.Fatal(err)
	}

	after, err := GetOrCreateSettingsByUser(db, bob)
	if err != nil {
		t.Fatal(err)
	}
	if after.WorkStart != before.WorkStart || after.Timezone != before.Timezone || after.LLMAPIKeysSet.OpenAI ||
		after.WorkingHours.Mode != before.WorkingHours.Mode || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("bob's settings changed when alice saved hers:\nbefore %+v\nafter  %+v", before, after)
	}
}

func TestSaveRecapSettingsForUser_TouchesOnlyRecapColumns(t *testing.T) {
	db := openTestDB(t)
	user := settingsTestUser(t, db, "settings-recap@example.com")
	s, _ := GetOrCreateSettingsByUser(db, user)
	s.WorkStart, s.LLMAPIKeys.OpenAI = "07:00", "kept-key"
	if err := SaveSettingsForUser(db, user, s); err != nil {
		t.Fatal(err)
	}

	stale, _ := GetOrCreateSettingsByUser(db, user)
	stale.WorkStart, stale.LLMAPIKeys.OpenAI = "11:00", "" // must not be written
	stale.RecapEnabled, stale.RecapSendTime, stale.RecapSendTo = true, "07:30", "channel"
	if err := SaveRecapSettingsForUser(db, user, stale); err != nil {
		t.Fatalf("SaveRecapSettingsForUser: %v", err)
	}

	got, _ := GetOrCreateSettingsByUser(db, user)
	if !got.RecapEnabled || got.RecapSendTime != "07:30" || got.RecapSendTo != "channel" {
		t.Errorf("recap not saved: %+v", got)
	}
	if got.WorkStart != "07:00" || got.LLMAPIKeys.OpenAI != "kept-key" {
		t.Errorf("recap save rewrote other columns: workStart=%q key set=%v", got.WorkStart, got.LLMAPIKeySet)
	}
}

func TestZoomTokens_ArePerUser(t *testing.T) {
	db := openTestDB(t)
	alice := settingsTestUser(t, db, "zoom-alice@example.com")
	bob := settingsTestUser(t, db, "zoom-bob@example.com")

	if err := SaveZoomTokens(db, alice, "a-access", "a-refresh"); err != nil {
		t.Fatalf("save alice: %v", err)
	}
	if err := SaveZoomTokens(db, bob, `b"access`, "b-refresh"); err != nil {
		t.Fatalf("save bob: %v", err)
	}
	a, _ := GetOrCreateSettingsByUser(db, alice)
	if a.ConferencingProvider != "zoom" || a.ZoomTokens != `{"access_token":"a-access","refresh_token":"a-refresh"}` {
		t.Errorf("alice's zoom = %q / %q", a.ConferencingProvider, a.ZoomTokens)
	}

	if err := ClearZoomTokens(db, bob); err != nil {
		t.Fatalf("clear bob: %v", err)
	}
	a, _ = GetOrCreateSettingsByUser(db, alice)
	b, _ := GetOrCreateSettingsByUser(db, bob)
	if a.ZoomTokens == "" || a.ConferencingProvider != "zoom" {
		t.Error("clearing bob's zoom cleared alice's")
	}
	if b.ZoomTokens != "" || b.ConferencingProvider != "meet" {
		t.Errorf("bob's zoom after clear = %q / %q", b.ConferencingProvider, b.ZoomTokens)
	}
}
