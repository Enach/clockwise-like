package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/internal/lifecycle"
	"github.com/Enach/paceday/backend/internal/testdb"
	"github.com/Enach/paceday/backend/storage"
	"github.com/robfig/cron/v3"
)

// fakeTrigger is a periodic trigger the test fires by hand (AC-35).
type fakeTrigger struct {
	spec string
	fire func()
}

func (f *fakeTrigger) AddFunc(spec string, cmd func()) (cron.EntryID, error) {
	f.spec, f.fire = spec, cmd
	return 1, nil
}

// AC-35 (PAC-40 D-13, contract §5 "Schedule"): a start-up run with a
// trigger that never fires erases the overdue link once; one tick (the
// clock 24 h later) is exactly one more run; once shutdown has begun no run
// starts, and a run in progress is a unit the drain awaits.
func TestPAC40B_ErasureRunsAtStartupThenOnEveryTick(t *testing.T) {
	dsn, cleanup, err := testdb.Create()
	if err != nil {
		t.Fatalf("PostgreSQL unavailable: %v", err)
	}
	db, err := storage.Open(dsn)
	if err != nil {
		_ = cleanup()
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); _ = cleanup() })
	g := lifecycle.NewGroup()
	defer lifecycle.SetDefault(g)()

	owner, err := storage.UpsertUser(db, "pac40b-main@example.com", "O", "", "google", "pac40b-main")
	if err != nil {
		t.Fatal(err)
	}
	newDeleted := func(slug string, end time.Time) {
		l, err := storage.CreateSchedulingLink(db, &storage.SchedulingLink{OwnerUserID: owner.ID, Slug: slug, Title: slug,
			DurationOptions: []int{30}, DaysOfWeek: []int{1}, WindowStart: "09:00", WindowEnd: "17:00", UsageType: "reusable", Active: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time, notes)
			VALUES ($1, 'Booker', 'b@booker.test', $2, $3, '')`, l.ID, end.Add(-time.Hour), end); err != nil {
			t.Fatal(err)
		}
		if ok, err := storage.DeleteSchedulingLink(db, l.ID); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	start := time.Date(2026, 6, 1, 3, 0, 0, 0, time.UTC)
	newDeleted("ac35-overdue", start.Add(-100*24*time.Hour))

	var mu sync.Mutex
	clock := start
	var lines []string
	job := &engine.BookerErasure{DB: db,
		Now: func() time.Time { mu.Lock(); defer mu.Unlock(); return clock },
		Logf: func(f string, a ...any) {
			mu.Lock()
			defer mu.Unlock()
			lines = append(lines, fmt.Sprintf(f, a...))
		}}
	runs := func() int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, l := range lines {
			if strings.HasPrefix(l, "booker-erasure: run ") {
				n++
			}
		}
		return n
	}
	erased := func() int {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM bookings WHERE booker_erased_at IS NOT NULL`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	trig := &fakeTrigger{}
	startup, err := scheduleBookerErasure(trig, job)
	if err != nil {
		t.Fatal(err)
	}
	if trig.spec != "@every 24h" || trig.fire == nil {
		t.Fatalf("registered %q", trig.spec)
	}
	startup()
	if runs() != 1 || erased() != 1 {
		t.Fatalf("after start-up with a silent trigger: %d runs, %d erased; want 1 and 1 (lines %v)", runs(), erased(), lines)
	}

	// One tick, clock + 24 h, with a link that became due meanwhile.
	newDeleted("ac35-next", start.Add(-90*24*time.Hour+12*time.Hour))
	mu.Lock()
	clock = start.Add(24 * time.Hour)
	mu.Unlock()
	trig.fire()
	if runs() != 2 || erased() != 2 {
		t.Fatalf("after one tick: %d runs, %d erased; want 2 and 2", runs(), erased())
	}

	// A run in progress is a tracked unit: hold the next due link's row so
	// the run waits inside its transaction, then begin shutdown.
	newDeleted("ac35-held", start.Add(-100*24*time.Hour))
	holder, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := holder.Exec(`SELECT 1 FROM scheduling_links WHERE slug = 'ac35-held' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { trig.fire(); close(done) }()
	deadline := time.Now().Add(10 * time.Second)
	for len(g.Pending()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if p := g.Pending(); len(p) != 1 || p[0].String() != "cron booker-erasure" {
		t.Fatalf("pending %v; want the erasure run as a unit", p)
	}
	g.BeginShutdown()
	trig.fire() // after shutdown began: no run starts
	startup()
	_ = holder.Rollback()
	waitCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if !g.Wait(waitCtx) {
		t.Fatal("the drain did not see the run end")
	}
	<-done
	if runs() != 3 || erased() != 3 {
		t.Fatalf("after shutdown: %d runs, %d erased; want exactly the one in progress (3 runs, 3 erased)", runs(), erased())
	}
}
