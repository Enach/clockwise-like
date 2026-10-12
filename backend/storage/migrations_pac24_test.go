package storage

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/testdb"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
)

// PAC-24, migration 024: every user gets their own settings row. Passive
// preferences are copied from the legacy shared row (id = 1); anything that
// acts on a calendar by itself, credentials, providers and recap settings are
// not (spec PAC-24, "Decisions — 2026-10-12").

// migrationTestDB returns a fresh database at migration version 23 and a
// function that moves it to another version.
func migrationTestDB(t *testing.T) (*sql.DB, func(version uint)) {
	t.Helper()
	dsn, cleanup, err := testdb.Create()
	if err != nil {
		t.Fatalf("create test database: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		_ = cleanup()
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		if err := cleanup(); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	src, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "postgres", driver)
	if err != nil {
		t.Fatal(err)
	}
	to := func(version uint) {
		t.Helper()
		if err := m.Migrate(version); err != nil && err != migrate.ErrNoChange {
			t.Fatalf("migrate to %d: %v", version, err)
		}
	}
	to(23)
	return db, to
}

func seedMigrationUser(t *testing.T, db *sql.DB, email string, created time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := db.Exec(`INSERT INTO users (id, email, name, created_at) VALUES ($1, $2, $2, $3)`, id, email, created); err != nil {
		t.Fatalf("seed user %s: %v", email, err)
	}
	return id
}

// seedSharedRow writes the legacy shared row exactly as insertDefaultSettings
// did (explicit id 1, bypassing the sequence), owned by ownerID or by nobody.
func seedSharedRow(t *testing.T, db *sql.DB, ownerID *uuid.UUID) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO settings (id, user_id, work_start, timezone, focus_label,
			working_hours, llm_provider, llm_api_key, aws_profile, auto_schedule_enabled, auto_schedule_cron,
			auto_decline_outside_working_hours, compression_enabled, calendar_provider, webcal_url,
			conferencing_provider, zoom_tokens, recap_enabled)
		VALUES (1, $1, '07:00', 'Europe/Paris', 'Deep Work',
			'{"mode":"by_day","default":{"enabled":true,"start":"07:00","end":"15:00"},"days":{"monday":{"enabled":true,"start":"07:00","end":"15:00"}}}',
			'openai', 'owner-secret', 'owner-profile', true, '0 9 * * 1',
			true, true, 'outlook', 'https://cal.example/secret',
			'zoom', '{"access_token":"z"}', true)`, ownerID); err != nil {
		t.Fatalf("seed shared row: %v", err)
	}
}

type migratedRow struct {
	id                                          int64
	workStart, timezone, focusLabel, workingDay string
	llmProvider, llmKey, awsProfile, cron       string
	autoSchedule, autoDecline, compression      bool
	calendarProvider, webcal, conferencing      string
	zoom                                        string
	recap                                       bool
}

func readMigratedRow(t *testing.T, db *sql.DB, user uuid.UUID) migratedRow {
	t.Helper()
	var r migratedRow
	err := db.QueryRow(`SELECT id, work_start, timezone, focus_label, COALESCE(working_hours->'days'->'monday'->>'end',''),
			llm_provider, llm_api_key, aws_profile, auto_schedule_cron,
			auto_schedule_enabled, auto_decline_outside_working_hours, compression_enabled,
			calendar_provider, webcal_url, conferencing_provider, zoom_tokens, recap_enabled
		FROM settings WHERE user_id = $1`, user).Scan(&r.id, &r.workStart, &r.timezone, &r.focusLabel, &r.workingDay,
		&r.llmProvider, &r.llmKey, &r.awsProfile, &r.cron,
		&r.autoSchedule, &r.autoDecline, &r.compression,
		&r.calendarProvider, &r.webcal, &r.conferencing, &r.zoom, &r.recap)
	if err != nil {
		t.Fatalf("read settings for %s: %v", user, err)
	}
	return r
}

func TestMigration024_OwnerKeepsEverythingOthersGetPreferencesOnly(t *testing.T) {
	db, to := migrationTestDB(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	owner := seedMigrationUser(t, db, "m24-owner@example.com", base)
	ownRow := seedMigrationUser(t, db, "m24-own-row@example.com", base.Add(time.Hour))
	fresh := seedMigrationUser(t, db, "m24-fresh@example.com", base.Add(2*time.Hour))
	seedSharedRow(t, db, nil) // never attributed: migrations ran before anyone signed up
	if _, err := db.Exec(`INSERT INTO settings (id, user_id, work_start) VALUES (50, $1, '10:00')`, ownRow); err != nil {
		t.Fatalf("seed own row: %v", err)
	}

	to(24)

	o := readMigratedRow(t, db, owner)
	if o.id != 1 || o.workStart != "07:00" || o.llmKey != "owner-secret" || !o.autoSchedule || o.calendarProvider != "outlook" || o.zoom == "" || !o.recap {
		t.Errorf("the earliest user should own the shared row with every value: %+v", o)
	}
	if r := readMigratedRow(t, db, ownRow); r.workStart != "10:00" || r.timezone != "UTC" {
		t.Errorf("a user's existing row must not change: %+v", r)
	}

	f := readMigratedRow(t, db, fresh)
	if f.workStart != "07:00" || f.timezone != "Europe/Paris" || f.focusLabel != "Deep Work" || f.workingDay != "15:00" {
		t.Errorf("passive preferences not copied: %+v", f)
	}
	if f.llmKey != "" || f.llmProvider == "openai" || f.awsProfile != "" {
		t.Errorf("provider, key or profile copied: %+v", f)
	}
	if f.autoSchedule || f.autoDecline || f.compression || f.cron == "0 9 * * 1" {
		t.Errorf("something that acts on a calendar was copied: %+v", f)
	}
	if f.calendarProvider != "google" || f.webcal != "" || f.conferencing != "meet" || f.zoom != "" || f.recap {
		t.Errorf("calendar identity, conferencing or recap copied: %+v", f)
	}

	var orphans int
	_ = db.QueryRow(`SELECT COUNT(*) FROM settings WHERE user_id IS NULL`).Scan(&orphans)
	if orphans != 0 {
		t.Errorf("%d settings rows belong to nobody", orphans)
	}
	if _, err := db.Exec(`INSERT INTO settings (work_start) VALUES ('09:00')`); err == nil {
		t.Error("a settings row without a user was accepted after 024")
	}
	// The sequence moved past the legacy explicit id 1.
	later := seedMigrationUser(t, db, "m24-later@example.com", base.Add(3*time.Hour))
	if _, err := db.Exec(`INSERT INTO settings (user_id) VALUES ($1)`, later); err != nil {
		t.Errorf("a new user's row collided after 024: %v", err)
	}
}

func TestMigration024_OrphanRowDeletedWhenEarliestUserHasARow(t *testing.T) {
	db, to := migrationTestDB(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	earliest := seedMigrationUser(t, db, "m24b-earliest@example.com", base)
	other := seedMigrationUser(t, db, "m24b-other@example.com", base.Add(time.Hour))
	seedSharedRow(t, db, nil)
	if _, err := db.Exec(`INSERT INTO settings (id, user_id, work_start) VALUES (50, $1, '10:00')`, earliest); err != nil {
		t.Fatalf("seed earliest's row: %v", err)
	}

	to(24)

	if r := readMigratedRow(t, db, earliest); r.workStart != "10:00" || r.llmKey != "" {
		t.Errorf("the earliest user's own row must not change: %+v", r)
	}
	if r := readMigratedRow(t, db, other); r.workStart != "07:00" || r.llmKey != "" {
		t.Errorf("other user should get the shared row's preferences only: %+v", r)
	}
	var shared int
	_ = db.QueryRow(`SELECT COUNT(*) FROM settings WHERE id = 1`).Scan(&shared)
	if shared != 0 {
		t.Error("the unattributable shared row should be deleted")
	}
}

func TestMigration024_NoSharedRowGivesDefaults(t *testing.T) {
	db, to := migrationTestDB(t)
	user := seedMigrationUser(t, db, "m24c-user@example.com", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	to(24)

	if r := readMigratedRow(t, db, user); r.workStart != "09:00" || r.timezone != "UTC" || r.llmKey != "" {
		t.Errorf("without a shared row a user gets the defaults: %+v", r)
	}
}

func TestMigration024_DownKeepsPerUserRows(t *testing.T) {
	db, to := migrationTestDB(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	owner := seedMigrationUser(t, db, "m24d-owner@example.com", base)
	seedMigrationUser(t, db, "m24d-other@example.com", base.Add(time.Hour))
	seedSharedRow(t, db, &owner)

	to(24)
	var before int
	_ = db.QueryRow(`SELECT COUNT(*) FROM settings`).Scan(&before)
	to(23)

	var after int
	_ = db.QueryRow(`SELECT COUNT(*) FROM settings`).Scan(&after)
	if after != before || after != 2 {
		t.Errorf("down migration changed the rows: %d before, %d after, want 2", before, after)
	}
	if _, err := db.Exec(`INSERT INTO settings (work_start) VALUES ('09:00')`); err != nil {
		t.Errorf("down migration should allow a row without a user again: %v", err)
	}
	if r := readMigratedRow(t, db, owner); r.id != 1 || r.llmKey != "owner-secret" {
		t.Errorf("the anchor row must still be id 1 with its values: %+v", r)
	}
}

// Values the contract's Settings schema does not admit become what the code
// already treated them as (contract revision 3, challenge finding 2).
func TestMigration024_NormalisesLegacyValues(t *testing.T) {
	db, to := migrationTestDB(t)
	user := seedMigrationUser(t, db, "m24e-user@example.com", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	// recap_send_to is not seeded: a CHECK constraint already limits it.
	if _, err := db.Exec(`INSERT INTO settings (id, user_id, calendar_provider, conferencing_provider,
			buffer_min_meeting_minutes, work_start, lunch_end, working_hours, lunch_breaks)
		VALUES (60, $1, '', 'webex', -5, '99:99', '',
			'{"mode":"BY_DAY","default":{"enabled":true,"start":"99:99","end":"17:00"},"days":{"Monday":{"enabled":true,"start":"07:00","end":"11:00"},"tuesday":{"enabled":true,"start":"10:00","end":"16:00"},"wednesday":{"enabled":true,"start":"99:99","end":"x"}}}',
			'{"Friday":{"enabled":false,"start":"","end":""},"monday":{"enabled":true,"start":"12:30","end":"13:00"}}')`, user); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	to(24)

	var calendar, conferencing, recapTo, workStart, lunchEnd, mode string
	var bufferMin int
	var days, lunch, defaultStart string
	if err := db.QueryRow(`SELECT calendar_provider, conferencing_provider, recap_send_to, buffer_min_meeting_minutes,
			work_start, lunch_end, working_hours->>'mode', working_hours->'days', lunch_breaks,
			working_hours->'default'->>'start'
		FROM settings WHERE user_id = $1`, user).Scan(&calendar, &conferencing, &recapTo, &bufferMin,
		&workStart, &lunchEnd, &mode, &days, &lunch, &defaultStart); err != nil {
		t.Fatal(err)
	}
	if calendar != "google" || conferencing != "meet" || recapTo != "dm" || bufferMin != 0 {
		t.Errorf("enums/negatives not normalised: %q %q %q %d", calendar, conferencing, recapTo, bufferMin)
	}
	if workStart != "09:00" || lunchEnd != "" {
		t.Errorf("clocks: work_start %q (want the default), lunch_end %q (empty is a real state, kept)", workStart, lunchEnd)
	}
	if mode != "by_day" {
		t.Errorf("mode = %q, want by_day", mode)
	}
	if strings.Contains(days, "Monday") || !strings.Contains(days, "tuesday") {
		t.Errorf("working days = %s, want only the lowercase key kept", days)
	}
	if strings.Contains(days, "wednesday") {
		t.Errorf("a day whose times are not clocks was kept: %s", days)
	}
	if defaultStart != "09:00" {
		t.Errorf("default start = %q, want the product default", defaultStart)
	}
	if strings.Contains(lunch, "Friday") || !strings.Contains(lunch, "monday") {
		t.Errorf("lunch breaks = %s, want only the lowercase key kept", lunch)
	}
}
