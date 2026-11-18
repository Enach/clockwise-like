package engine

// PAC-136 review fixes: the in-process gate in front of the habit run's
// lock (B1), the guarded adoption of an existing event (M1), and the recap's
// gate instant and deadline report (L2).

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/lifecycle"
	"github.com/Enach/paceday/backend/internal/lifecycle/lifecycletest"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	googlecalendar "google.golang.org/api/calendar/v3"
	"google.golang.org/api/googleapi"
)

// B1, in-process: while a user's run is held, a second request for that
// user waits in the gate holding no connection (no Postgres waiter), and a
// third is coalesced into it (returns at once). One event per (habit, day)
// results.
func TestPAC136_SameUserRunsCoalesceInProcess(t *testing.T) {
	db := openTestDB(t)
	user, h := b5Habit(t, db, "pac136-coalesce@pac136.test")
	cal := newPAC136Cal()
	eng := &HabitsEngine{DB: db, Clock: FixedClock{T: b5Monday}, calOps: cal}
	entered := make(chan struct{})
	hold := make(chan struct{})
	var once sync.Once
	InstallHabitRunLocked(t, func(uuid.UUID) {
		first := false
		once.Do(func() { first = true })
		if first {
			close(entered)
			<-hold
		}
	})
	errs := make(chan error, 2)
	go func() { errs <- eng.ReoptimizeAll(context.Background(), user) }()
	<-entered
	go func() { errs <- eng.ReoptimizeAll(context.Background(), user) }()
	g := habitGateFor(db)
	for deadline := time.Now().Add(5 * time.Second); ; {
		g.mu.Lock()
		pending := 0
		if turn := g.users[user]; turn != nil {
			pending = turn.pending
		}
		g.mu.Unlock()
		if pending == 2 {
			break
		}
		if time.Now().After(deadline) {
			close(hold)
			t.Fatalf("the second request never queued (pending %d)", pending)
		}
		time.Sleep(10 * time.Millisecond)
	}
	third := make(chan error, 1)
	go func() { third <- eng.ReoptimizeAll(context.Background(), user) }()
	select {
	case err := <-third:
		if err != nil {
			close(hold)
			t.Fatalf("coalesced request = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		close(hold)
		t.Fatal("a third request waited instead of coalescing into the queued run")
	}
	var waiting int
	if err := db.QueryRow(`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND classid = $1 AND NOT granted AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`,
		uint32(storage.HabitLockClass)).Scan(&waiting); err != nil || waiting != 0 {
		close(hold)
		t.Fatalf("%d Postgres waiters, %v; want none (the queued run holds no connection)", waiting, err)
	}
	close(hold)
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("run: %v", err)
		}
	}
	pac136AssertOneEventPerMonday(t, cal, b5Occurrences(t, db, h.ID))
	g.mu.Lock()
	left := len(g.users)
	g.mu.Unlock()
	if left != 0 || g.inUse() != 0 {
		t.Fatalf("gate left %d users, %d slots held; want none", left, g.inUse())
	}
}

// B1: on a pool of two connections, three concurrent runs (for three users,
// and for one user) all finish and the pool drains. Each run waits a moment
// after taking its lock for the others to arrive, so without the
// process-wide slots two runs held both connections in their locks and each
// waited for a connection to read with until its deadline.
func TestPAC136_RunsCannotExhaustThePool(t *testing.T) {
	for _, mode := range []string{"three users", "one user"} {
		t.Run(mode, func(t *testing.T) {
			db := openTestDB(t)
			var users []uuid.UUID
			for i := 0; i < 3; i++ {
				if mode == "one user" && i > 0 {
					users = append(users, users[0])
					continue
				}
				u, _ := b5Habit(t, db, fmt.Sprintf("pac136-pool-%d@pac136.test", i))
				users = append(users, u)
			}
			db.SetMaxOpenConns(2)
			var mu sync.Mutex
			arrivals := 0
			allIn := make(chan struct{})
			InstallHabitRunLocked(t, func(uuid.UUID) {
				mu.Lock()
				arrivals++
				if arrivals == 3 {
					close(allIn)
				}
				mu.Unlock()
				select {
				case <-allIn:
				case <-time.After(300 * time.Millisecond):
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cal := newPAC136Cal()
			eng := &HabitsEngine{DB: db, Clock: FixedClock{T: b5Monday}, calOps: cal}
			errs := make(chan error, 3)
			for _, u := range users {
				u := u
				go func() { errs <- eng.ReoptimizeAll(ctx, u) }()
			}
			for i := 0; i < 3; i++ {
				select {
				case err := <-errs:
					if err != nil {
						t.Fatalf("run: %v", err)
					}
				case <-time.After(30 * time.Second):
					t.Fatal("a run never finished")
				}
			}
			for deadline := time.Now().Add(5 * time.Second); db.Stats().InUse != 0; {
				if time.Now().After(deadline) {
					t.Fatalf("%d pool connections still in use, want none", db.Stats().InUse)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if g := habitGateFor(db); g.inUse() != 0 {
				t.Fatalf("%d run slots held, want none", g.inUse())
			}
		})
	}
}

// B1: K is a quarter of the pool, between 1 and 4.
func TestPAC136_HabitRunSlots(t *testing.T) {
	for maxOpen, want := range map[int]int{0: 4, 1: 1, 2: 1, 3: 1, 4: 1, 8: 2, 12: 3, 20: 4, 100: 4} {
		if got := habitRunSlots(maxOpen); got != want {
			t.Errorf("habitRunSlots(%d) = %d, want %d", maxOpen, got, want)
		}
	}
}

// M1/N1: when Google refuses to restore the deleted (habit, day) event as
// gone or for a permission reason (404, 410, 403 forbidden), the habit is
// created under a generated id and that id is stored.
func TestPAC136_UnrestorableEventFallsBackToGeneratedID(t *testing.T) {
	cases := map[string]*googleapi.Error{
		"404":                          {Code: http.StatusNotFound},
		"410":                          {Code: http.StatusGone},
		"403 forbidden":                pac136Err(http.StatusForbidden, "forbidden"),
		"403 forbiddenForNonOrganizer": pac136Err(http.StatusForbidden, "forbiddenForNonOrganizer"),
	}
	for name, refusal := range cases {
		t.Run(name, func(t *testing.T) {
			db := openTestDB(t)
			user, h := b5Habit(t, db, "pac136-norestore@pac136.test")
			cal := newPAC136Cal()
			eng := &HabitsEngine{DB: db, Clock: FixedClock{T: b5Monday}, calOps: cal}
			if err := eng.ReoptimizeAll(context.Background(), user); err != nil {
				t.Fatal(err)
			}
			cal.mu.Lock()
			cal.restoreErr = refusal
			cal.live["meeting"] = b5Event("meeting", time.Date(2026, 11, 2, 7, 0, 0, 0, time.UTC), true)
			cal.mu.Unlock()
			if err := eng.ReoptimizeAll(context.Background(), user); err != nil {
				t.Fatalf("second run = %v, want the fallback to succeed", err)
			}
			var habitEvents []*googlecalendar.Event
			for _, ev := range cal.onDay("2026-11-02") {
				if ev.Id != "meeting" {
					habitEvents = append(habitEvents, ev)
				}
			}
			if len(habitEvents) != 1 || !strings.HasPrefix(habitEvents[0].Id, "random") || habitEvents[0].Start.DateTime != "2026-11-02T07:30:00Z" {
				t.Fatalf("habit events %+v, want one at 07:30 under a generated id", habitEvents)
			}
			if got := b5Occurrences(t, db, h.ID)["2026-11-02"]; got == nil || got.Status != "scheduled" || got.CalendarEventID != habitEvents[0].Id {
				t.Fatalf("Monday row %+v, want the generated id stored", got)
			}
		})
	}
}

func pac136Err(code int, reason string) *googleapi.Error {
	return &googleapi.Error{Code: code, Errors: []googleapi.ErrorItem{{Reason: reason}}}
}

// N1: a live event of ours under the (habit, day) id (a crash left it with
// no row) whose adopt update fails, for any reason (a rate-limit or quota
// 403, a permission 403, a 5xx), is the run's error, with no second event
// under a generated id (one was created, and ours stayed on the calendar for
// good). The next run adopts it.
func TestPAC136_LiveAdoptFailureIsAnError(t *testing.T) {
	failures := map[string]*googleapi.Error{
		"rateLimitExceeded":     pac136Err(http.StatusForbidden, "rateLimitExceeded"),
		"userRateLimitExceeded": pac136Err(http.StatusForbidden, "userRateLimitExceeded"),
		"quotaExceeded":         pac136Err(http.StatusForbidden, "quotaExceeded"),
		"forbidden":             pac136Err(http.StatusForbidden, "forbidden"),
		"500":                   {Code: http.StatusInternalServerError},
	}
	for name, failure := range failures {
		t.Run(name, func(t *testing.T) {
			db := openTestDB(t)
			user, h := b5Habit(t, db, "pac136-transient@pac136.test")
			cal := newPAC136Cal()
			monday := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
			left := b5Event(habitEventID(h.ID, monday), time.Date(2026, 11, 2, 7, 0, 0, 0, time.UTC), false)
			left.ExtendedProperties = &googlecalendar.EventExtendedProperties{Private: map[string]string{habitEventMarker: h.ID.String()}}
			cal.live[left.Id] = left
			cal.updateErr = failure
			err := (&HabitsEngine{DB: db, Clock: FixedClock{T: b5Monday}, calOps: cal}).ReoptimizeAll(context.Background(), user)
			if err == nil || !strings.Contains(err.Error(), "adopt existing event") {
				t.Fatalf("ReoptimizeAll = %v, want the failed adopt", err)
			}
			evs := cal.onDay("2026-11-02")
			if len(evs) != 1 || evs[0].Id != left.Id {
				t.Fatalf("Monday events %+v, want only the one left behind", evs)
			}
			if got := b5Occurrences(t, db, h.ID)["2026-11-02"]; got != nil && got.Status == "scheduled" {
				t.Fatalf("Monday row %+v, want none scheduled", got)
			}

			cal.mu.Lock()
			cal.updateErr = nil
			cal.mu.Unlock()
			if err := (&HabitsEngine{DB: db, Clock: FixedClock{T: b5Monday}, calOps: cal}).ReoptimizeAll(context.Background(), user); err != nil {
				t.Fatalf("retry = %v", err)
			}
			pac136AssertOneEventPerMonday(t, cal, b5Occurrences(t, db, h.ID))
		})
	}
}

// N1: a cancelled event whose restore hits a rate-limit 403 is an error too
// (only a permission reason falls back).
func TestPAC136_RateLimitedRestoreIsAnError(t *testing.T) {
	db := openTestDB(t)
	user, h := b5Habit(t, db, "pac136-ratelimited-restore@pac136.test")
	cal := newPAC136Cal()
	eng := &HabitsEngine{DB: db, Clock: FixedClock{T: b5Monday}, calOps: cal}
	if err := eng.ReoptimizeAll(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	cal.mu.Lock()
	cal.restoreErr = pac136Err(http.StatusForbidden, "rateLimitExceeded")
	cal.live["meeting"] = b5Event("meeting", time.Date(2026, 11, 2, 7, 0, 0, 0, time.UTC), true)
	cal.mu.Unlock()
	if err := eng.ReoptimizeAll(context.Background(), user); err == nil || !strings.Contains(err.Error(), "adopt existing event") {
		t.Fatalf("second run = %v, want the failed restore", err)
	}
	for _, ev := range cal.onDay("2026-11-02") {
		if strings.HasPrefix(ev.Id, "random") {
			t.Fatalf("event %s created under a generated id on a rate limit", ev.Id)
		}
	}
	if got := b5Occurrences(t, db, h.ID)["2026-11-02"]; got == nil || got.Status != "displaced" {
		t.Fatalf("Monday row %+v, want displaced (retried next run)", got)
	}
}

// N2: a request that arrives while a run leaves the gate, once the queued
// run can take the token, is not folded into that run (it may already have
// read); it queues behind it.
func TestPAC136_RequestDuringLeaveIsNotFolded(t *testing.T) {
	db := openTestDB(t)
	user, _ := b5Habit(t, db, "pac136-leave@pac136.test")
	cal := newPAC136Cal()
	eng := &HabitsEngine{DB: db, Clock: FixedClock{T: b5Monday}, calOps: cal}
	entered := make(chan struct{})
	hold := make(chan struct{})
	var once sync.Once
	InstallHabitRunLocked(t, func(uuid.UUID) {
		first := false
		once.Do(func() { first = true })
		if first {
			close(entered)
			<-hold
		}
	})
	leaving := make(chan struct{})
	release := make(chan struct{})
	var leaveOnce sync.Once
	InstallHabitGateLeaving(t, func(uuid.UUID) {
		first := false
		leaveOnce.Do(func() { first = true })
		if first {
			close(leaving)
			<-release
		}
	})
	errs := make(chan error, 2)
	go func() { errs <- eng.ReoptimizeAll(context.Background(), user) }()
	<-entered
	go func() { errs <- eng.ReoptimizeAll(context.Background(), user) }()
	g := habitGateFor(db)
	for deadline := time.Now().Add(5 * time.Second); ; {
		g.mu.Lock()
		pending := 0
		if turn := g.users[user]; turn != nil {
			pending = turn.pending
		}
		g.mu.Unlock()
		if pending == 2 {
			break
		}
		if time.Now().After(deadline) {
			close(hold)
			t.Fatalf("the second request never queued (pending %d)", pending)
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(hold)
	<-leaving // the first run is between its two leaving steps
	third := make(chan error, 1)
	go func() { third <- eng.ReoptimizeAll(context.Background(), user) }()
	select {
	case err := <-third:
		close(release)
		t.Fatalf("a request during the leave was folded (= %v); want it queued", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("run: %v", err)
		}
	}
	if err := <-third; err != nil {
		t.Fatalf("third run: %v", err)
	}
}

// M1: an event under the (habit, day) id whose marker names another habit
// is never overwritten; the habit gets a generated id.
func TestPAC136_ForeignEventUnderTheIDIsNotOverwritten(t *testing.T) {
	db := openTestDB(t)
	user, h := b5Habit(t, db, "pac136-foreign@pac136.test")
	cal := newPAC136Cal()
	monday := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	foreign := b5Event(habitEventID(h.ID, monday), time.Date(2026, 11, 2, 15, 0, 0, 0, time.UTC), false)
	foreign.Summary = "not this habit"
	other := uuid.New().String()
	foreign.ExtendedProperties = &googlecalendar.EventExtendedProperties{Private: map[string]string{habitEventMarker: other}}
	cal.live[foreign.Id] = foreign
	if err := (&HabitsEngine{DB: db, Clock: FixedClock{T: b5Monday}, calOps: cal}).ReoptimizeAll(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	kept, err := cal.getEvent(context.Background(), foreign.Id)
	if err != nil || kept.Summary != "not this habit" || kept.ExtendedProperties.Private[habitEventMarker] != other {
		t.Fatalf("foreign event now %+v, %v; want it untouched", kept, err)
	}
	if got := b5Occurrences(t, db, h.ID)["2026-11-02"]; got == nil || got.Status != "scheduled" || !strings.HasPrefix(got.CalendarEventID, "random") {
		t.Fatalf("Monday row %+v, want a generated id", got)
	}
}

// pac136SteppingClock answers first on its first call, then later.
type pac136SteppingClock struct {
	mu           sync.Mutex
	calls        int
	first, later time.Time
}

func (c *pac136SteppingClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.calls == 1 {
		return c.first
	}
	return c.later
}

// L2: a user RunAll found due is sent to even when their send starts after
// the minute has turned (sends are staggered); it was re-gated on the clock
// and skipped.
func TestPAC136_RecapLateStartIsNotRegated(t *testing.T) {
	rec := lifecycletest.Record(t)
	svc, _, posts := pac122Recap(t, true)
	svc.Clock = &pac136SteppingClock{
		first: time.Date(2026, 11, 2, 8, 30, 59, 0, time.UTC),
		later: time.Date(2026, 11, 2, 8, 31, 0, 0, time.UTC),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	svc.RunAll(ctx)
	lifecycle.Default().Wait(context.Background())
	if posts() != 1 || len(rec.All()) != 0 {
		t.Fatalf("%d posts, reports %+v; want the recap sent", posts(), rec.All())
	}
}

// L2: the due users whose send the run's deadline ended before it started
// are reported, by count and ids, not dropped silently.
func TestPAC136_RecapUsersCutByTheDeadlineAreReported(t *testing.T) {
	rec := lifecycletest.Record(t)
	svc, first, _ := pac122Recap(t, true)
	u, err := storage.UpsertUser(svc.DB, "pac136-recap-2@pac136.test", "R2", "", "google", "pac136-recap-2")
	if err != nil {
		t.Fatal(err)
	}
	s, err := storage.GetOrCreateSettingsByUser(svc.DB, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.RecapEnabled, s.RecapSendTime, s.RecapSendTo, s.RecapChannelID, s.Timezone = true, "08:30", "channel", "C1", "UTC"
	if err := storage.SaveSettingsForUser(svc.DB, u.ID, s); err != nil {
		t.Fatal(err)
	}
	if err := storage.SaveRecapSettingsForUser(svc.DB, u.ID, s); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	svc.RunAll(ctx)
	lifecycle.Default().Wait(context.Background())
	got := rec.Matching("daily-recap", "run deadline ended before 1 due users started")
	if len(got) != 1 || got[0].Unit.ID != "run" {
		t.Fatalf("reports %+v, want one for the user not started", rec.All())
	}
	msg := got[0].Err.Error()
	inFirst, inSecond := strings.Contains(msg, first.String()), strings.Contains(msg, u.ID.String())
	if inFirst == inSecond || strings.Contains(msg, "@") {
		t.Fatalf("report %q, want exactly one user's id and no address", msg)
	}
}
