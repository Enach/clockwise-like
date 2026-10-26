package storage

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
)

// PAC-49 (spec v3.1 §4.1, §4.2): who the focus scheduler considers, and the
// per-user watermark (auto_schedule_last_run) that decides when it next runs.

func seedAutoScheduleUser(t *testing.T, db *sql.DB, email string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := db.Exec(`INSERT INTO users (id, email, name, created_at) VALUES ($1, $2, $2, NOW())`, id, email); err != nil {
		t.Fatalf("seed user %s: %v", email, err)
	}
	return id
}

func mustLastRun(t *testing.T, db *sql.DB, userID uuid.UUID) *time.Time {
	t.Helper()
	w, err := GetAutoScheduleLastRun(db, userID)
	if err != nil {
		t.Fatalf("GetAutoScheduleLastRun: %v", err)
	}
	return w
}

func assertLastRun(t *testing.T, db *sql.DB, userID uuid.UUID, want time.Time, what string) {
	t.Helper()
	got := mustLastRun(t, db, userID)
	if got == nil || !got.Equal(want) {
		t.Errorf("%s: watermark = %v, want %v", what, got, want)
	}
}

// saveAt reads userID's settings, applies change and saves at the given instant,
// exactly as the PATCH handler does (read, merge, write).
func saveAt(t *testing.T, db *sql.DB, userID uuid.UUID, at time.Time, change func(*Settings)) {
	t.Helper()
	s, err := GetOrCreateSettingsByUser(db, userID)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	change(s)
	if err := SaveSettingsForUserAt(db, userID, s, at); err != nil {
		t.Fatalf("save settings: %v", err)
	}
}

// AC-1: users who never saved settings are not candidates; their defaults are
// off, the stored '0 8 * * *', and no watermark.
func TestAutoSchedule_AC1_NeverSavedUsersAreNotCandidates(t *testing.T) {
	db := openTestDB(t)
	a := seedAutoScheduleUser(t, db, "pac49-ac1-a@example.com")
	b := seedAutoScheduleUser(t, db, "pac49-ac1-b@example.com")

	got, err := ListAutoScheduleCandidates(db)
	if err != nil {
		t.Fatalf("ListAutoScheduleCandidates: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("candidates = %v, want none", got)
	}
	for _, u := range []uuid.UUID{a, b} {
		s, err := GetOrCreateSettingsByUser(db, u)
		if err != nil {
			t.Fatal(err)
		}
		if s.AutoScheduleEnabled || s.AutoScheduleCron != "0 8 * * *" {
			t.Errorf("defaults = enabled %v cron %q, want false and '0 8 * * *'", s.AutoScheduleEnabled, s.AutoScheduleCron)
		}
		if w := mustLastRun(t, db, u); w != nil {
			t.Errorf("watermark = %v, want null", w)
		}
	}
	// A row created by the first read is still not a candidate.
	if got, _ := ListAutoScheduleCandidates(db); len(got) != 0 {
		t.Errorf("after first reads, candidates = %v, want none", got)
	}
}

// AC-2: the candidate set is exactly the users whose own row has the flag on,
// each with their own schedule, timezone and watermark.
func TestAutoSchedule_AC2_CandidatesAreExactlyTheOptedIn(t *testing.T) {
	db := openTestDB(t)
	a := seedAutoScheduleUser(t, db, "pac49-ac2-a@example.com")
	b := seedAutoScheduleUser(t, db, "pac49-ac2-b@example.com")
	enabledAt := time.Date(2026, 7, 1, 4, 30, 0, 0, time.UTC)
	saveAt(t, db, a, enabledAt, func(s *Settings) {
		s.AutoScheduleEnabled = true
		s.AutoScheduleCron = "0 7 * * 1-5"
		s.Timezone = "Europe/Paris"
	})
	saveAt(t, db, b, enabledAt, func(s *Settings) { s.FocusLabel = "B's label" })

	got, err := ListAutoScheduleCandidates(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].UserID != a {
		t.Fatalf("candidates = %+v, want exactly [A]", got)
	}
	c := got[0]
	if c.CronExpr != "0 7 * * 1-5" || c.Timezone != "Europe/Paris" || c.LastRun == nil || !c.LastRun.Equal(enabledAt) {
		t.Errorf("candidate = %+v", c)
	}
}

// AC-7b: turning the flag on sets the watermark to the instant of the save;
// saving it on again, or not mentioning it, leaves the watermark alone.
func TestAutoSchedule_AC7b_EnableSetsWatermarkOnlyOnTheTransition(t *testing.T) {
	db := openTestDB(t)
	a := seedAutoScheduleUser(t, db, "pac49-ac7b@example.com")
	T := time.Date(2026, 7, 1, 4, 30, 0, 0, time.UTC)
	saveAt(t, db, a, T, func(s *Settings) { s.AutoScheduleEnabled = true })
	assertLastRun(t, db, a, T, "after enabling")

	saveAt(t, db, a, T.Add(time.Hour), func(s *Settings) { s.AutoScheduleEnabled = true })
	assertLastRun(t, db, a, T, "after saving enabled again")
	saveAt(t, db, a, T.Add(2*time.Hour), func(*Settings) {})
	assertLastRun(t, db, a, T, "after a save that omits the flag")
}

// AC-7c: editing the schedule or the timezone while enabled resets the
// watermark to the instant of the save.
func TestAutoSchedule_AC7c_EditWhileEnabledResetsWatermark(t *testing.T) {
	db := openTestDB(t)
	a := seedAutoScheduleUser(t, db, "pac49-ac7c@example.com")
	W := time.Date(2026, 6, 30, 5, 0, 0, 0, time.UTC)
	saveAt(t, db, a, W, func(s *Settings) {
		s.AutoScheduleEnabled, s.AutoScheduleCron, s.Timezone = true, "0 7 * * 1-5", "Europe/Paris"
	})

	cronEdit := time.Date(2026, 7, 1, 7, 0, 0, 0, time.UTC)
	saveAt(t, db, a, cronEdit, func(s *Settings) { s.AutoScheduleCron = "0 5 * * 1-5" })
	assertLastRun(t, db, a, cronEdit, "after editing the schedule")

	tzEdit := cronEdit.Add(time.Hour)
	saveAt(t, db, a, tzEdit, func(s *Settings) { s.Timezone = "America/New_York" })
	assertLastRun(t, db, a, tzEdit, "after editing the timezone")
}

// AC-7d: a save that changes neither the flag, the schedule nor the timezone
// leaves the watermark alone. Disabling also leaves it (it becomes history),
// and editing while disabled does not touch it; re-enabling resets it.
func TestAutoSchedule_AC7d_UnrelatedSaveAndDisableLeaveWatermark(t *testing.T) {
	db := openTestDB(t)
	a := seedAutoScheduleUser(t, db, "pac49-ac7d@example.com")
	W := time.Date(2026, 7, 1, 5, 0, 0, 0, time.UTC)
	saveAt(t, db, a, W, func(s *Settings) { s.AutoScheduleEnabled = true })

	saveAt(t, db, a, W.Add(time.Hour), func(s *Settings) { s.FocusLabel = "Heads down" })
	assertLastRun(t, db, a, W, "after an unrelated save")

	saveAt(t, db, a, W.Add(2*time.Hour), func(s *Settings) { s.AutoScheduleEnabled = false })
	assertLastRun(t, db, a, W, "after disabling")
	saveAt(t, db, a, W.Add(3*time.Hour), func(s *Settings) { s.AutoScheduleCron = "0 6 * * *"; s.Timezone = "Asia/Tokyo" })
	assertLastRun(t, db, a, W, "after editing while disabled")
	if got, _ := ListAutoScheduleCandidates(db); len(got) != 0 {
		t.Errorf("disabled user is a candidate: %+v", got)
	}

	reenable := W.Add(4 * time.Hour)
	saveAt(t, db, a, reenable, func(s *Settings) { s.AutoScheduleEnabled = true })
	assertLastRun(t, db, a, reenable, "after re-enabling")
}

// AC-7e (storage half): a save built from a document read before the
// scheduler advanced the watermark does not rewind it.
func TestAutoSchedule_AC7e_SaveNeverCarriesWatermark(t *testing.T) {
	db := openTestDB(t)
	a := seedAutoScheduleUser(t, db, "pac49-ac7e@example.com")
	W0 := time.Date(2026, 7, 1, 4, 30, 0, 0, time.UTC)
	saveAt(t, db, a, W0, func(s *Settings) { s.AutoScheduleEnabled = true })

	stale, err := GetOrCreateSettingsByUser(db, a) // the document read at T0
	if err != nil {
		t.Fatal(err)
	}
	W1 := time.Date(2026, 7, 1, 5, 0, 0, 0, time.UTC)
	if ok, err := AdvanceAutoScheduleWatermark(db, a, &W0, W1); err != nil || !ok {
		t.Fatalf("advance: ok=%v err=%v", ok, err)
	}
	stale.FocusLabel = "changed"
	if err := SaveSettingsForUserAt(db, a, stale, W1.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	assertLastRun(t, db, a, W1, "after a save built from a pre-advance read")
}

// The scheduler's advance is a compare-and-set on the watermark it observed:
// it fails, changing nothing, when a save moved the watermark or turned the
// flag off in between.
func TestAutoSchedule_AdvanceIsCompareAndSet(t *testing.T) {
	db := openTestDB(t)
	a := seedAutoScheduleUser(t, db, "pac49-cas@example.com")
	W0 := time.Date(2026, 7, 1, 4, 30, 0, 0, time.UTC)
	saveAt(t, db, a, W0, func(s *Settings) { s.AutoScheduleEnabled = true })

	stale := W0.Add(-time.Hour)
	if ok, err := AdvanceAutoScheduleWatermark(db, a, &stale, W0.Add(time.Hour)); err != nil || ok {
		t.Errorf("advance from a stale observation: ok=%v err=%v, want false", ok, err)
	}
	assertLastRun(t, db, a, W0, "after a refused advance")

	if ok, err := AdvanceAutoScheduleWatermark(db, a, nil, W0.Add(time.Hour)); err != nil || ok {
		t.Errorf("advance observing null when it is set: ok=%v err=%v, want false", ok, err)
	}

	saveAt(t, db, a, W0.Add(time.Minute), func(s *Settings) { s.AutoScheduleEnabled = false })
	if ok, err := AdvanceAutoScheduleWatermark(db, a, &W0, W0.Add(time.Hour)); err != nil || ok {
		t.Errorf("advance for a disabled user: ok=%v err=%v, want false", ok, err)
	}

	// A null watermark on an enabled row (only a direct edit makes one) is
	// advanced from null.
	if _, err := db.Exec(`UPDATE settings SET auto_schedule_enabled = TRUE, auto_schedule_last_run = NULL WHERE user_id = $1`, a); err != nil {
		t.Fatal(err)
	}
	to := W0.Add(2 * time.Hour)
	if ok, err := AdvanceAutoScheduleWatermark(db, a, nil, to); err != nil || !ok {
		t.Errorf("advance from null: ok=%v err=%v, want true", ok, err)
	}
	assertLastRun(t, db, a, to, "after advancing from null")

	if _, err := AdvanceAutoScheduleWatermark(db, uuid.Nil, nil, to); err == nil {
		t.Error("advance with no user id: want an error")
	}
	if _, err := GetAutoScheduleLastRun(db, uuid.Nil); err == nil {
		t.Error("read with no user id: want an error")
	}
}

// A first save that creates the row (no prior read) applies the same rule:
// enabled → watermark is the save instant, disabled → null.
func TestAutoSchedule_InsertPathSetsWatermarkOnlyWhenEnabled(t *testing.T) {
	db := openTestDB(t)
	on := seedAutoScheduleUser(t, db, "pac49-insert-on@example.com")
	off := seedAutoScheduleUser(t, db, "pac49-insert-off@example.com")
	T := time.Date(2026, 7, 1, 4, 30, 0, 0, time.UTC)
	if err := SaveSettingsForUserAt(db, on, &Settings{WorkStart: "09:00", WorkEnd: "17:00", Timezone: "UTC", AutoScheduleEnabled: true, AutoScheduleCron: "0 8 * * *"}, T); err != nil {
		t.Fatal(err)
	}
	if err := SaveSettingsForUserAt(db, off, &Settings{WorkStart: "09:00", WorkEnd: "17:00", Timezone: "UTC", AutoScheduleCron: "0 8 * * *"}, T); err != nil {
		t.Fatal(err)
	}
	assertLastRun(t, db, on, T, "enabled on insert")
	if w := mustLastRun(t, db, off); w != nil {
		t.Errorf("disabled on insert: watermark = %v, want null", w)
	}
}

// AC-9: migration 027 up, down, up — on an empty database and on one holding
// per-user settings rows — succeeds each time and leaves every watermark null.
func TestMigration027_AutoScheduleLastRun_UpDownUp(t *testing.T) {
	hasColumn := func(t *testing.T, db *sql.DB) bool {
		t.Helper()
		var ok bool
		if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.columns
			WHERE table_name = 'settings' AND column_name = 'auto_schedule_last_run')`).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	nonNull := func(t *testing.T, db *sql.DB) int {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM settings WHERE auto_schedule_last_run IS NOT NULL`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	t.Run("empty database", func(t *testing.T) {
		db, to := migrationTestDB(t)
		to(26)
		to(27)
		if !hasColumn(t, db) {
			t.Fatal("after up: column missing")
		}
		to(26)
		if hasColumn(t, db) {
			t.Fatal("after down: column still present")
		}
		to(27)
		if !hasColumn(t, db) {
			t.Fatal("after up again: column missing")
		}
	})

	t.Run("per-user rows", func(t *testing.T) {
		db, to := migrationTestDB(t)
		to(26)
		base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		for i, email := range []string{"m27-a@example.com", "m27-b@example.com"} {
			u := seedMigrationUser(t, db, email, base.Add(time.Duration(i)*time.Hour))
			if _, err := db.Exec(`INSERT INTO settings (user_id, auto_schedule_enabled) VALUES ($1, $2)`, u, i == 0); err != nil {
				t.Fatalf("seed settings: %v", err)
			}
		}
		to(27)
		if n := nonNull(t, db); n != 0 {
			t.Errorf("after up: %d non-null watermarks, want 0", n)
		}
		if _, err := db.Exec(`UPDATE settings SET auto_schedule_last_run = NOW()`); err != nil {
			t.Fatal(err)
		}
		to(26)
		var rows int
		if err := db.QueryRow(`SELECT COUNT(*) FROM settings`).Scan(&rows); err != nil || rows != 2 {
			t.Errorf("after down: %d rows (err %v), want 2", rows, err)
		}
		to(27)
		if n := nonNull(t, db); n != 0 {
			t.Errorf("after up again: %d non-null watermarks, want 0 (the down migration loses them)", n)
		}
	})
}
