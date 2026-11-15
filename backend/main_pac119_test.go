package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Enach/paceday/backend/internal/lifecycle"
	"github.com/Enach/paceday/backend/internal/testdb"
	"github.com/Enach/paceday/backend/storage"
)

// AC-12 (schedule; PAC-119 contract md §3.3): the link-create key cleanup is
// registered @every 1h and has a start-up run; both are the same cronJob unit,
// so no run starts once shutdown has begun. The start-up run with a trigger
// that never fires clears an expired key's digest and outcomes; one tick is
// one more run. The log holds the count only.
func TestPAC119_LinkCreateKeyCleanupRunsAtStartupThenHourly(t *testing.T) {
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

	owner, err := storage.UpsertUser(db, "pac119-main@example.com", "O", "", "google", "pac119-main")
	if err != nil {
		t.Fatal(err)
	}
	keyed := func(key, slug string) {
		t.Helper()
		l := &storage.SchedulingLink{OwnerUserID: owner.ID, Slug: slug, Title: slug, DurationOptions: []int{30},
			DaysOfWeek: []int{1}, WindowStart: "09:00", WindowEnd: "17:00"}
		pre := *l
		a := &storage.LinkCreateAttempt{Key: key, Link: &pre, CoHostEmails: []string{"nobody@pac119.test"}}
		if _, err := storage.ResolveLinkCreateKeyCtx(context.Background(), db, owner.ID, a); err != nil {
			t.Fatal(err)
		}
		if _, _, err := storage.CreateSchedulingLinkWithCoHostsKeyedCtx(context.Background(), db, l, a.CoHostEmails, key, a.Digest); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE scheduling_link_create_keys SET created_at = now() - interval '25 hours' WHERE idempotency_key = $1`, key); err != nil {
			t.Fatal(err)
		}
	}
	retained := func() int {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM scheduling_link_create_keys WHERE request_sha256 IS NOT NULL OR co_host_changes IS NOT NULL`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	var mu sync.Mutex
	var lines []string
	var reported []error
	job := linkCreateKeyCleanup(db, func(f string, a ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, fmt.Sprintf(f, a...))
	}, func(err error) { mu.Lock(); defer mu.Unlock(); reported = append(reported, err) })

	keyed("pac119-main-key-0001", "pac119-main-a")
	trig := &fakeTrigger{}
	startup, err := scheduleLinkCreateKeyCleanup(trig, job)
	if err != nil {
		t.Fatal(err)
	}
	if trig.spec != "@every 1h" || trig.fire == nil {
		t.Fatalf("registered %q; want @every 1h", trig.spec)
	}
	startup()
	if n := retained(); n != 0 {
		t.Fatalf("after the start-up run %d rows keep data; want 0 (lines %v)", n, lines)
	}
	keyed("pac119-main-key-0002", "pac119-main-b")
	trig.fire()
	if n := retained(); n != 0 {
		t.Fatalf("after one tick %d rows keep data", n)
	}
	mu.Lock()
	got := strings.Join(lines, "|")
	mu.Unlock()
	if got != "link-create-key-cleanup: cleared 1|link-create-key-cleanup: cleared 1" {
		t.Fatalf("log lines %q", got)
	}
	if strings.Contains(got, "pac119-main-key") || strings.Contains(got, "nobody@") {
		t.Error("the log holds a key or an address")
	}

	// Once shutdown has begun, no run starts.
	keyed("pac119-main-key-0003", "pac119-main-c")
	g.BeginShutdown()
	trig.fire()
	if n := retained(); n != 1 {
		t.Fatalf("a run started after shutdown began (%d rows keep data)", n)
	}

	// A failed run is logged and reported, and writes nothing.
	db.Close()
	linkCreateKeyCleanup(db, func(f string, a ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, fmt.Sprintf(f, a...))
	}, func(err error) { mu.Lock(); defer mu.Unlock(); reported = append(reported, err) })(context.Background())
	mu.Lock()
	defer mu.Unlock()
	if len(reported) != 1 || !strings.HasPrefix(lines[len(lines)-1], "link-create-key-cleanup: run failed: ") {
		t.Fatalf("failure: reported %v, last line %q", reported, lines[len(lines)-1])
	}
	if errors.Is(reported[0], context.Canceled) {
		t.Fatal("unexpected cancellation")
	}
}
