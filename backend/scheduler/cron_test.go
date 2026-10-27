package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/internal/testdb"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// PAC-49 spec v3.1 §4.3 and §5 "Buildable now". "Tick at HH:MM" means the
// loop is handed that instant and ticks once; nothing here waits for a clock.

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

var paris = mustLoad("Europe/Paris")

func mustLoad(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

func seedUser(t *testing.T, db *sql.DB, email string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := db.Exec(`INSERT INTO users (id, email, name, created_at) VALUES ($1, $2, $2, NOW())`, id, email); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := storage.GetOrCreateSettingsByUser(db, id); err != nil {
		t.Fatalf("create settings: %v", err)
	}
	return id
}

// enable turns auto-schedule on for userID through the settings write, at the
// given instant, with the given schedule and timezone.
func enable(t *testing.T, db *sql.DB, userID uuid.UUID, at time.Time, cronExpr, tz string) {
	t.Helper()
	s, err := storage.GetOrCreateSettingsByUser(db, userID)
	if err != nil {
		t.Fatal(err)
	}
	s.AutoScheduleEnabled, s.AutoScheduleCron, s.Timezone = true, cronExpr, tz
	if err := storage.SaveSettingsForUserAt(db, userID, s, at); err != nil {
		t.Fatalf("enable: %v", err)
	}
}

func setWatermark(t *testing.T, db *sql.DB, userID uuid.UUID, w time.Time) {
	t.Helper()
	if _, err := db.Exec(`UPDATE settings SET auto_schedule_last_run = $2 WHERE user_id = $1`, userID, w); err != nil {
		t.Fatal(err)
	}
}

func watermark(t *testing.T, db *sql.DB, userID uuid.UUID) *time.Time {
	t.Helper()
	w, err := storage.GetAutoScheduleLastRun(db, userID)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

type call struct {
	user uuid.UUID
	week time.Time
}

// recorder is a RunFunc that records each call and returns an empty result.
type recorder struct {
	mu    sync.Mutex
	calls []call
	// block, when set, is called inside the run before it returns.
	block func(n int, user uuid.UUID)
}

func (r *recorder) run(_ context.Context, user uuid.UUID, week time.Time) (*engine.FocusRunResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, call{user, week})
	n := len(r.calls)
	block := r.block
	r.mu.Unlock()
	if block != nil {
		block(n, user)
	}
	return &engine.FocusRunResult{}, nil
}

func (r *recorder) runsFor(user uuid.UUID) []call {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []call
	for _, c := range r.calls {
		if c.user == user {
			out = append(out, c)
		}
	}
	return out
}

type logSink struct {
	mu    sync.Mutex
	lines []string
}

func (l *logSink) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logSink) mentioning(s string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, line := range l.lines {
		if strings.Contains(line, s) {
			n++
		}
	}
	return n
}

func newTestCron(db *sql.DB, run RunFunc) (*FocusCron, *logSink) {
	fc := NewFocusCron(db, &oauth2.Config{})
	logs := &logSink{}
	fc.run = run
	fc.logf = logs.logf
	return fc, logs
}

// at builds a Paris wall-clock instant on Wednesday 1 July 2026 (summer).
func at(hh, mm int) time.Time { return time.Date(2026, 7, 1, hh, mm, 0, 0, paris) }

func TestNewFocusCron_Defaults(t *testing.T) {
	fc := NewFocusCron(nil, &oauth2.Config{})
	if fc.interval != time.Minute {
		t.Errorf("interval = %v, want one minute (§4.3 item 1)", fc.interval)
	}
	if fc.runTimeout != 10*time.Minute {
		t.Errorf("run timeout = %v, want ten minutes (§4.3 item 7)", fc.runTimeout)
	}
	if fc.run == nil || fc.now == nil || fc.logf == nil {
		t.Error("run, clock and log must all be set")
	}
}

// Start ticks on its own and Stop ends the loop; a tick with no candidates
// runs nobody. (Replaces the old Start/Stop and "no users" Reload tests.)
func TestFocusCron_StartTicksAndStopEnds(t *testing.T) {
	db := openTestDB(t)
	a := seedUser(t, db, "pac49-start@example.com")
	enable(t, db, a, at(6, 30), "0 7 * * *", "Europe/Paris")
	rec := &recorder{}
	fc, _ := newTestCron(db, rec.run)
	fc.interval = 10 * time.Millisecond
	fc.now = func() time.Time { return at(7, 0) }
	fc.Start()
	deadline := time.Now().Add(5 * time.Second)
	for len(rec.runsFor(a)) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	fc.Stop()
	if n := len(rec.runsFor(a)); n != 1 {
		t.Errorf("runs after many ticks at the same instant = %d, want 1", n)
	}
	fc.Stop() // a second Stop is harmless
}

// AC-3: before A's first slot after enabling, nobody is run.
func TestFocusCron_AC3_NothingBeforeTheFirstSlot(t *testing.T) {
	db := openTestDB(t)
	a := seedUser(t, db, "pac49-ac3-a@example.com")
	b := seedUser(t, db, "pac49-ac3-b@example.com")
	enable(t, db, a, at(6, 30), "0 7 * * 1-5", "Europe/Paris")
	rec := &recorder{}
	fc, _ := newTestCron(db, rec.run)
	for _, tk := range []time.Time{at(6, 31), at(6, 58)} {
		fc.tick(tk)
	}
	if n := len(rec.runsFor(a)) + len(rec.runsFor(b)); n != 0 {
		t.Errorf("runs before 07:00 = %d, want 0", n)
	}
}

// AC-4: A runs exactly once, at the 07:00 tick, for the week containing that
// instant; the watermark becomes the tick instant; B never runs.
func TestFocusCron_AC4_RunsOnceAtTheSlot(t *testing.T) {
	db := openTestDB(t)
	a := seedUser(t, db, "pac49-ac4-a@example.com")
	b := seedUser(t, db, "pac49-ac4-b@example.com")
	enable(t, db, a, at(6, 30), "0 7 * * 1-5", "Europe/Paris")
	rec := &recorder{}
	fc, _ := newTestCron(db, rec.run)
	for _, tk := range []time.Time{at(6, 31), at(6, 58), at(7, 0), at(7, 1), at(7, 7)} {
		fc.tick(tk)
	}
	runs := rec.runsFor(a)
	if len(runs) != 1 {
		t.Fatalf("A ran %d times, want 1", len(runs))
	}
	if !runs[0].week.Equal(at(7, 0)) || runs[0].week.Location().String() != "Europe/Paris" {
		t.Errorf("target = %v, want the 07:00 tick instant in the user's zone", runs[0].week)
	}
	if w := watermark(t, db, a); w == nil || !w.Equal(at(7, 0)) {
		t.Errorf("watermark = %v, want %v", w, at(7, 0))
	}
	if n := len(rec.runsFor(b)); n != 0 {
		t.Errorf("B ran %d times, want 0", n)
	}
}

// AC-4a: the user's zone decides the hour, never the process's.
func TestFocusCron_AC4a_UsersTimezoneNotServers(t *testing.T) {
	t.Run("summer", func(t *testing.T) {
		db := openTestDB(t)
		a := seedUser(t, db, "pac49-ac4a-s@example.com")
		enable(t, db, a, time.Date(2026, 7, 1, 4, 30, 0, 0, time.UTC), "0 7 * * 1-5", "Europe/Paris")
		rec := &recorder{}
		fc, _ := newTestCron(db, rec.run)
		fc.tick(time.Date(2026, 7, 1, 5, 0, 0, 0, time.UTC))
		if n := len(rec.runsFor(a)); n != 1 {
			t.Errorf("runs at 05:00 UTC (07:00 Paris, CEST) = %d, want 1", n)
		}
	})
	t.Run("winter", func(t *testing.T) {
		db := openTestDB(t)
		a := seedUser(t, db, "pac49-ac4a-w@example.com")
		enable(t, db, a, time.Date(2026, 1, 7, 4, 30, 0, 0, time.UTC), "0 7 * * 1-5", "Europe/Paris")
		rec := &recorder{}
		fc, _ := newTestCron(db, rec.run)
		fc.tick(time.Date(2026, 1, 7, 5, 0, 0, 0, time.UTC))
		if n := len(rec.runsFor(a)); n != 0 {
			t.Errorf("runs at 05:00 UTC (06:00 Paris, CET) = %d, want 0", n)
		}
		fc.tick(time.Date(2026, 1, 7, 6, 0, 0, 0, time.UTC))
		if n := len(rec.runsFor(a)); n != 1 {
			t.Errorf("runs at 06:00 UTC (07:00 Paris, CET) = %d, want 1", n)
		}
	})
}

// AC-4b: a tick that begins while the previous one runs is dropped; A and B
// each run once, B by the 07:00 tick after A's run returns.
func TestFocusCron_AC4b_OverlappingTicksAreDropped(t *testing.T) {
	db := openTestDB(t)
	a := seedUser(t, db, "pac49-ac4b-a@example.com")
	b := seedUser(t, db, "pac49-ac4b-b@example.com")
	enable(t, db, a, at(6, 30), "0 7 * * *", "Europe/Paris")
	enable(t, db, b, at(6, 30), "0 7 * * *", "Europe/Paris")

	started := make(chan uuid.UUID, 1)
	release := make(chan struct{})
	rec := &recorder{block: func(n int, user uuid.UUID) {
		if n == 1 { // the first run blocks, standing in for three minutes
			started <- user
			<-release
		}
	}}
	fc, _ := newTestCron(db, rec.run)

	first := make(chan bool)
	go func() { first <- fc.tick(at(7, 0)) }()
	firstUser := <-started
	if fc.tick(at(7, 1)) || fc.tick(at(7, 2)) {
		t.Error("a tick that began during the 07:00 tick was not dropped")
	}
	close(release)
	if !<-first {
		t.Error("the 07:00 tick reported dropped")
	}

	if len(rec.runsFor(a)) != 1 || len(rec.runsFor(b)) != 1 {
		t.Fatalf("runs A=%d B=%d, want 1 each", len(rec.runsFor(a)), len(rec.runsFor(b)))
	}
	rec.mu.Lock()
	order := []uuid.UUID{rec.calls[0].user, rec.calls[1].user}
	rec.mu.Unlock()
	if order[0] != firstUser {
		t.Errorf("the second user ran before the blocked run returned: %v", order)
	}
	for _, c := range []uuid.UUID{a, b} {
		if w := watermark(t, db, c); w == nil || !w.Equal(at(7, 0)) {
			t.Errorf("watermark = %v, want the 07:00 tick", w)
		}
	}
}

// AC-4c: missed slots are caught up once at the first tick after a restart.
func TestFocusCron_AC4c_MissedSlotsCaughtUpOnce(t *testing.T) {
	monday7 := time.Date(2026, 7, 6, 7, 0, 0, 0, paris)
	day := func(d, hh, mm int) time.Time { return time.Date(2026, 7, d, hh, mm, 0, 0, paris) }

	t.Run("one slot missed", func(t *testing.T) {
		db := openTestDB(t)
		a := seedUser(t, db, "pac49-ac4c-1@example.com")
		enable(t, db, a, monday7.Add(-time.Hour), "0 7 * * 1-5", "Europe/Paris")
		setWatermark(t, db, a, monday7)
		rec := &recorder{}
		fc, _ := newTestCron(db, rec.run)
		for _, tk := range []time.Time{day(7, 9, 30), day(7, 9, 31), day(7, 9, 45), day(8, 6, 59)} {
			fc.tick(tk)
		}
		if runs := rec.runsFor(a); len(runs) != 1 || !runs[0].week.Equal(day(7, 9, 30)) {
			t.Fatalf("runs = %v, want exactly one at Tuesday 09:30", runs)
		}
		if w := watermark(t, db, a); w == nil || !w.Equal(day(7, 9, 30)) {
			t.Errorf("watermark = %v, want Tuesday 09:30", w)
		}
		fc.tick(day(8, 7, 0))
		if n := len(rec.runsFor(a)); n != 2 {
			t.Errorf("runs after Wednesday 07:00 = %d, want 2", n)
		}
	})
	t.Run("three slots missed", func(t *testing.T) {
		db := openTestDB(t)
		a := seedUser(t, db, "pac49-ac4c-3@example.com")
		enable(t, db, a, monday7.Add(-time.Hour), "0 7 * * 1-5", "Europe/Paris")
		setWatermark(t, db, a, monday7)
		rec := &recorder{}
		fc, _ := newTestCron(db, rec.run)
		for _, tk := range []time.Time{day(9, 9, 30), day(9, 9, 31), day(9, 9, 45)} {
			fc.tick(tk)
		}
		if n := len(rec.runsFor(a)); n != 1 {
			t.Errorf("runs = %d, want 1", n)
		}
	})
}

// AC-4d: a stored schedule the five-field parser rejects, or a timezone that
// cannot be loaded, is skipped with one warning naming the user, no audit
// entry, and the other users still run. No "already warned" state is kept.
func TestFocusCron_AC4d_UnusableStoredValuesSkipped(t *testing.T) {
	for _, tc := range []struct {
		name, column, value string
	}{
		{"descriptor schedule", "auto_schedule_cron", "@daily"},
		{"unloadable timezone", "timezone", "Mars/Olympus"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			a := seedUser(t, db, "pac49-ac4d-a@example.com")
			b := seedUser(t, db, "pac49-ac4d-b@example.com")
			enable(t, db, a, at(6, 30), "0 7 * * *", "Europe/Paris")
			enable(t, db, b, at(6, 30), "0 7 * * *", "Europe/Paris")
			if _, err := db.Exec(`UPDATE settings SET `+tc.column+` = $2 WHERE user_id = $1`, a, tc.value); err != nil {
				t.Fatal(err)
			}
			rec := &recorder{}
			fc, logs := newTestCron(db, rec.run)
			fc.tick(at(7, 0))
			if n := len(rec.runsFor(a)); n != 0 {
				t.Errorf("A ran %d times, want 0", n)
			}
			if n := len(rec.runsFor(b)); n != 1 {
				t.Errorf("B ran %d times, want 1", n)
			}
			if n := logs.mentioning(a.String()); n != 1 {
				t.Errorf("warnings naming A = %d, want 1: %v", n, logs.lines)
			}
			var audits int
			if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE user_id = $1`, a).Scan(&audits); err != nil || audits != 0 {
				t.Errorf("audit entries for A = %d (err %v), want 0", audits, err)
			}
			fc.tick(at(7, 1))
			if n := logs.mentioning(a.String()); n != 2 {
				t.Errorf("warnings naming A after a second tick = %d, want 2 (no warned-once state)", n)
			}
		})
	}
}

// AC-4e: a run is abandoned after the per-user bound; its watermark was
// already advanced and the next user runs on the same tick.
func TestFocusCron_AC4e_HungRunIsAbandoned(t *testing.T) {
	db := openTestDB(t)
	a := seedUser(t, db, "pac49-ac4e-a@example.com")
	b := seedUser(t, db, "pac49-ac4e-b@example.com")
	enable(t, db, a, at(6, 30), "0 7 * * *", "Europe/Paris")
	enable(t, db, b, at(6, 30), "0 7 * * *", "Europe/Paris")

	forever := make(chan struct{})
	t.Cleanup(func() { close(forever) })
	hungCh := make(chan uuid.UUID, 1)
	rec := &recorder{block: func(n int, user uuid.UUID) {
		if n == 1 {
			hungCh <- user
			<-forever // ignores its context: blocks until the test ends
		}
	}}
	fc, logs := newTestCron(db, rec.run)
	fc.runTimeout = 50 * time.Millisecond

	done := make(chan struct{})
	go func() { fc.tick(at(7, 0)); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the tick did not abandon the hung run")
	}
	if len(rec.runsFor(a)) != 1 || len(rec.runsFor(b)) != 1 {
		t.Errorf("runs A=%d B=%d, want 1 each", len(rec.runsFor(a)), len(rec.runsFor(b)))
	}
	hung := <-hungCh
	if w := watermark(t, db, hung); w == nil || !w.Equal(at(7, 0)) {
		t.Errorf("hung user's watermark = %v, want the tick", w)
	}
	if logs.mentioning(hung.String()) == 0 {
		t.Error("the abandoned run was not logged")
	}
}

// One user's failure never stops the loop, and a failed run still advances
// its watermark, so it is retried at the next slot rather than every minute.
func TestFocusCron_FailedRunAdvancesAndOthersRun(t *testing.T) {
	db := openTestDB(t)
	a := seedUser(t, db, "pac49-fail-a@example.com")
	b := seedUser(t, db, "pac49-fail-b@example.com")
	enable(t, db, a, at(6, 30), "0 7 * * *", "Europe/Paris")
	enable(t, db, b, at(6, 30), "0 7 * * *", "Europe/Paris")
	var mu sync.Mutex
	ran := map[uuid.UUID]int{}
	fc, _ := newTestCron(db, func(_ context.Context, u uuid.UUID, _ time.Time) (*engine.FocusRunResult, error) {
		mu.Lock()
		ran[u]++
		mu.Unlock()
		if u == a {
			return nil, fmt.Errorf("not authenticated")
		}
		panic("a panicking run must not take the loop down")
	})
	fc.tick(at(7, 0))
	fc.tick(at(7, 1))
	if ran[a] != 1 || ran[b] != 1 {
		t.Errorf("runs = %v, want one each", ran)
	}
	for _, u := range []uuid.UUID{a, b} {
		if w := watermark(t, db, u); w == nil || !w.Equal(at(7, 0)) {
			t.Errorf("watermark = %v, want the 07:00 tick", w)
		}
	}
}

// §4.1 last rule: a null watermark on an enabled user is set to the tick and
// the user is not run on that tick.
func TestFocusCron_NullWatermarkIsTreatedAsNow(t *testing.T) {
	db := openTestDB(t)
	a := seedUser(t, db, "pac49-null@example.com")
	enable(t, db, a, at(6, 30), "0 7 * * *", "Europe/Paris")
	if _, err := db.Exec(`UPDATE settings SET auto_schedule_last_run = NULL WHERE user_id = $1`, a); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	fc, _ := newTestCron(db, rec.run)
	fc.tick(at(7, 0))
	if n := len(rec.runsFor(a)); n != 0 {
		t.Errorf("runs = %d, want 0", n)
	}
	if w := watermark(t, db, a); w == nil || !w.Equal(at(7, 0)) {
		t.Errorf("watermark = %v, want the tick", w)
	}
	fc.tick(time.Date(2026, 7, 2, 7, 0, 0, 0, paris))
	if n := len(rec.runsFor(a)); n != 1 {
		t.Errorf("runs at the next slot = %d, want 1", n)
	}
}

// AC-7c through the loop: an edit while enabled resets the watermark, so the
// next run is the new schedule's next slot, never the next tick.
func TestFocusCron_AC7c_EditMovesTheNextRun(t *testing.T) {
	db := openTestDB(t)
	a := seedUser(t, db, "pac49-ac7c-loop@example.com")
	yesterday7 := time.Date(2026, 6, 30, 7, 0, 0, 0, paris)
	enable(t, db, a, yesterday7.Add(-time.Hour), "0 7 * * 1-5", "Europe/Paris")
	setWatermark(t, db, a, yesterday7)

	s, _ := storage.GetOrCreateSettingsByUser(db, a)
	s.AutoScheduleCron = "0 5 * * 1-5"
	if err := storage.SaveSettingsForUserAt(db, a, s, at(9, 0)); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	fc, _ := newTestCron(db, rec.run)
	fc.tick(at(9, 1))
	fc.tick(at(9, 30))
	if n := len(rec.runsFor(a)); n != 0 {
		t.Fatalf("runs after the edit, before 05:00 tomorrow = %d, want 0", n)
	}
	fc.tick(time.Date(2026, 7, 2, 5, 0, 0, 0, paris))
	if n := len(rec.runsFor(a)); n != 1 {
		t.Errorf("runs at 05:00 tomorrow = %d, want 1", n)
	}

	// A timezone six hours west: the watermark resets at 09:00 Paris (03:00
	// New York) and nothing runs before the next 05:00 in the new zone, which
	// is two hours later the same day.
	ny := mustLoad("America/New_York")
	s, _ = storage.GetOrCreateSettingsByUser(db, a)
	s.Timezone = "America/New_York"
	editAt := time.Date(2026, 7, 2, 9, 0, 0, 0, paris)
	if err := storage.SaveSettingsForUserAt(db, a, s, editAt); err != nil {
		t.Fatal(err)
	}
	for tk := editAt.Add(time.Minute); tk.Before(time.Date(2026, 7, 2, 5, 0, 0, 0, ny)); tk = tk.Add(15 * time.Minute) {
		fc.tick(tk)
	}
	if n := len(rec.runsFor(a)); n != 1 {
		t.Fatalf("runs before 05:00 New York = %d, want still 1", n)
	}
	fc.tick(time.Date(2026, 7, 2, 5, 0, 0, 0, ny))
	if n := len(rec.runsFor(a)); n != 2 {
		t.Errorf("runs at 05:00 New York = %d, want 2", n)
	}
}

// AC-7e (loop half): a save built from a document read before the tick
// advanced the watermark does not cause a second run.
func TestFocusCron_AC7e_StaleSaveCausesNoSecondRun(t *testing.T) {
	db := openTestDB(t)
	a := seedUser(t, db, "pac49-ac7e-loop@example.com")
	enable(t, db, a, at(6, 30), "0 7 * * *", "Europe/Paris")
	stale, _ := storage.GetOrCreateSettingsByUser(db, a)
	rec := &recorder{}
	fc, _ := newTestCron(db, rec.run)
	fc.tick(at(7, 0))
	stale.FocusLabel = "changed"
	if err := storage.SaveSettingsForUserAt(db, a, stale, at(7, 0).Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	fc.tick(at(7, 1))
	if n := len(rec.runsFor(a)); n != 1 {
		t.Errorf("runs = %d, want 1", n)
	}
}

// OQ-6: daylight-saving edges, pinned to Europe/Paris 2026. A slot inside the
// spring-forward gap runs once that day, at the wall-clock instant one hour
// later (03:30 CEST); a slot inside the repeated fall-back hour runs once.
// The library's own stepping would skip the spring day and run twice in the
// autumn; nextSlot evaluates in wall-clock time to avoid both.
func TestNextSlot_DaylightSaving(t *testing.T) {
	sched, err := engine.ParseAutoScheduleCron("30 2 * * *")
	if err != nil {
		t.Fatal(err)
	}
	spring := nextSlot(sched, time.Date(2026, 3, 28, 2, 30, 0, 0, paris), paris)
	if want := time.Date(2026, 3, 29, 1, 30, 0, 0, time.UTC); !spring.Equal(want) {
		t.Errorf("spring-forward slot = %v, want %v (03:30 CEST on the 29th)", spring, want)
	}
	firstFall := nextSlot(sched, time.Date(2026, 10, 24, 2, 30, 0, 0, paris), paris)
	if firstFall.Year() != 2026 || firstFall.Month() != 10 || firstFall.In(paris).Day() != 25 {
		t.Fatalf("fall-back slot = %v, want on the 25th", firstFall)
	}
	if again := nextSlot(sched, firstFall, paris); again.In(paris).Day() != 26 {
		t.Errorf("slot after the fall-back run = %v, want the 26th (never twice for one slot)", again)
	}
}

func TestFocusCron_DaylightSavingOneRunPerDay(t *testing.T) {
	for _, tc := range []struct {
		name       string
		start, end time.Time
	}{
		{"spring forward", time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC), time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)},
		{"fall back", time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC), time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			a := seedUser(t, db, "pac49-dst@example.com")
			enable(t, db, a, tc.start, "30 2 * * *", "Europe/Paris")
			rec := &recorder{}
			fc, _ := newTestCron(db, rec.run)
			for tk := tc.start; tk.Before(tc.end); tk = tk.Add(15 * time.Minute) {
				fc.tick(tk)
			}
			perDay := map[string]int{}
			for _, c := range rec.runsFor(a) {
				perDay[c.week.In(paris).Format("2006-01-02")]++
			}
			if len(perDay) != 3 {
				t.Errorf("days with a run = %v, want 3", perDay)
			}
			for d, n := range perDay {
				if n != 1 {
					t.Errorf("%s: %d runs, want 1", d, n)
				}
			}
		})
	}
}

// fakeCalendar is a Google Calendar stand-in keyed by the bearer credential:
// each credential sees and writes only its own events.
type fakeCalendar struct {
	mu      sync.Mutex
	events  map[string][]map[string]any // credential -> events
	creator map[string]string           // event id -> credential that created it
	deletes int
	seq     int
}

func newFakeCalendar() *fakeCalendar {
	return &fakeCalendar{events: map[string][]map[string]any{}, creator: map[string]string{}}
}

func (f *fakeCalendar) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cred := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/events"):
		items := f.events[cred]
		if items == nil {
			items = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/events"):
		body, _ := io.ReadAll(r.Body)
		ev := map[string]any{}
		_ = json.Unmarshal(body, &ev)
		f.seq++
		id := fmt.Sprintf("ev%d", f.seq)
		ev["id"] = id
		f.events[cred] = append(f.events[cred], ev)
		f.creator[id] = cred
		_ = json.NewEncoder(w).Encode(ev)
	case r.Method == http.MethodDelete:
		f.deletes++
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
	}
}

// AC-6b and AC-14 against the real per-user run: each user's events are
// created with that user's own credential, and a scheduled run never clears
// an existing block.
func TestFocusCron_AC6b_AC14_OwnTokenAndNeverClears(t *testing.T) {
	db := openTestDB(t)
	stand := newFakeCalendar()
	srv := httptest.NewServer(stand)
	t.Cleanup(srv.Close)
	calendar.Configure(config.CalendarEndpoint{BaseURL: srv.URL + config.CalendarAPIRoot, Guard: true})
	t.Cleanup(func() { calendar.Configure(config.ProductionCalendarEndpoint()) })

	// A week in the future, so every day is plannable.
	tick := time.Date(2030, 1, 7, 7, 0, 0, 0, time.UTC) // Monday
	users := map[string]uuid.UUID{}
	for _, name := range []string{"a", "b"} {
		u := seedUser(t, db, "pac49-ac6b-"+name+"@example.com")
		users[name] = u
		enable(t, db, u, tick.Add(-time.Hour), "0 7 * * *", "UTC")
		if err := auth.UpsertUserToken(db, u, &oauth2.Token{AccessToken: "cred-" + name, Expiry: time.Now().Add(24 * time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}

	// AC-14: A already has a block on Tuesday, in storage and on the calendar.
	kStart := time.Date(2030, 1, 8, 9, 0, 0, 0, time.UTC)
	kEnd := kStart.Add(time.Hour)
	if err := storage.SaveFocusBlock(db, users["a"], "kept", "2030-01-08", kStart, kEnd); err != nil {
		t.Fatal(err)
	}
	stand.events["cred-a"] = []map[string]any{{
		"id": "kept", "summary": "Focus Time",
		"start": map[string]any{"dateTime": kStart.Format(time.RFC3339)},
		"end":   map[string]any{"dateTime": kEnd.Format(time.RFC3339)},
	}}

	fc := NewFocusCron(db, &oauth2.Config{})
	logs := &logSink{}
	fc.logf = logs.logf
	fc.tick(tick)

	for name, u := range users {
		blocks, err := storage.ListFocusBlocksForUserWeek(db, u, time.Date(2030, 1, 7, 0, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		created := 0
		for _, bl := range blocks {
			if bl.GoogleEventID == "kept" {
				continue
			}
			created++
			if got := stand.creator[bl.GoogleEventID]; got != "cred-"+name {
				t.Errorf("user %s's block %s was created with credential %q", name, bl.GoogleEventID, got)
			}
		}
		if created == 0 {
			t.Errorf("user %s: no blocks created; log: %v", name, logs.lines)
		}
	}

	blocks, _ := storage.ListFocusBlocksForUserWeek(db, users["a"], time.Date(2030, 1, 7, 0, 0, 0, 0, time.UTC))
	kept := false
	for _, bl := range blocks {
		kept = kept || bl.GoogleEventID == "kept"
	}
	if !kept {
		t.Error("the existing block is gone from storage")
	}
	if stand.deletes != 0 {
		t.Errorf("calendar deletes = %d, want 0", stand.deletes)
	}
	var cleared int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'focus_cleared'`).Scan(&cleared); err != nil || cleared != 0 {
		t.Errorf("focus_cleared entries = %d (err %v), want 0", cleared, err)
	}
}

// PAC-49 acceptance "saving a preference re-reads the schedule set" (v2 §2.9):
// one long-lived loop, no restart and no reload call — enabling, disabling and
// re-enabling through the settings write each take effect at the next tick.
func TestFocusCron_SavesTakeEffectWithoutRestart(t *testing.T) {
	db := openTestDB(t)
	a := seedUser(t, db, "pac49-live@example.com")
	rec := &recorder{}
	fc, _ := newTestCron(db, rec.run)
	day := func(d, hh, mm int) time.Time { return time.Date(2026, 7, d, hh, mm, 0, 0, paris) }

	fc.tick(day(1, 7, 0))
	if n := len(rec.runsFor(a)); n != 0 {
		t.Fatalf("runs before enabling = %d, want 0", n)
	}

	enable(t, db, a, day(1, 8, 0), "0 9 * * *", "Europe/Paris")
	fc.tick(day(1, 9, 0))
	if n := len(rec.runsFor(a)); n != 1 {
		t.Fatalf("runs at the first slot after enabling = %d, want 1", n)
	}

	s, _ := storage.GetOrCreateSettingsByUser(db, a)
	s.AutoScheduleEnabled = false
	if err := storage.SaveSettingsForUserAt(db, a, s, day(1, 10, 0)); err != nil {
		t.Fatal(err)
	}
	fc.tick(day(2, 9, 0))
	if n := len(rec.runsFor(a)); n != 1 {
		t.Fatalf("runs after disabling = %d, want still 1", n)
	}

	enable(t, db, a, day(2, 10, 0), "0 9 * * *", "Europe/Paris")
	fc.tick(day(2, 10, 1))
	if n := len(rec.runsFor(a)); n != 1 {
		t.Fatalf("re-enabling ran stale history: runs = %d, want still 1", n)
	}
	fc.tick(day(3, 9, 0))
	if n := len(rec.runsFor(a)); n != 2 {
		t.Errorf("runs at the next slot after re-enabling = %d, want 2", n)
	}
}
