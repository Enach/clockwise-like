package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/internal/lifecycle"
	"github.com/Enach/paceday/backend/internal/lifecycle/lifecycletest"
	"github.com/Enach/paceday/backend/internal/secretbox"
	"github.com/Enach/paceday/backend/internal/testdb"
	"github.com/Enach/paceday/backend/storage"
	"github.com/getsentry/sentry-go"
	"github.com/google/uuid"
)

// PAC-122 phase 2, batch O: background failures reach error monitoring.

// SF-097: a panicking cron job is recovered, reported and released; it
// does not take the process down (this test binary would die without it).
func TestPAC122_CronJobPanicRecoveredAndReported(t *testing.T) {
	rec := lifecycletest.Record(t)
	captureLog(t)
	g := lifecycle.NewGroup()
	defer lifecycle.SetDefault(g)()
	done := make(chan struct{})
	go func() {
		defer close(done)
		cronJob("auto-decline", func(context.Context) {
			var users []string
			_ = users[len(users)] // an index-out-of-range panic
		})()
	}()
	<-done
	if !g.Wait(context.Background()) {
		t.Fatal("the panicking run was not released")
	}
	got := rec.Matching("cron", "index out of range")
	if len(got) != 1 || got[0].Unit.ID != "auto-decline" {
		t.Fatalf("reports %+v", rec.All())
	}
}

// SF-097: the same for the error-returning wrapper.
func TestPAC122_CronJobErrPanicRecovered(t *testing.T) {
	rec := lifecycletest.Record(t)
	captureLog(t)
	defer lifecycle.SetDefault(lifecycle.NewGroup())()
	done := make(chan struct{})
	go func() {
		defer close(done)
		cronJobErr("manager-detection", func(context.Context) error { panic("boom") })()
	}()
	<-done
	if got := rec.Matching("cron", "boom"); len(got) != 1 || got[0].Unit.ID != "manager-detection" {
		t.Fatalf("reports %+v", rec.All())
	}
}

// SF-098: a cron job's returned error is logged and reported; nil is not.
func TestPAC122_CronJobErrReportsTheError(t *testing.T) {
	rec := lifecycletest.Record(t)
	logs := captureLog(t)
	defer lifecycle.SetDefault(lifecycle.NewGroup())()
	cronJobErr("auto-decline", func(context.Context) error {
		return errors.Join(errors.New("auto-decline for user a: x"), errors.New("auto-decline for user b: y"))
	})()
	cronJobErr("auto-decline", func(context.Context) error { return nil })()
	all := rec.All()
	if len(all) != 1 || all[0].Unit.String() != "cron auto-decline" ||
		!strings.Contains(all[0].Err.Error(), "user a") || !strings.Contains(all[0].Err.Error(), "user b") {
		t.Fatalf("reports %+v", all)
	}
	if !strings.Contains(logs.String(), "cron auto-decline failed") {
		t.Fatalf("log %q", logs.String())
	}
}

type fakeDetector struct{ fail map[uuid.UUID]bool }

func (f fakeDetector) DetectTeam(_ context.Context, id uuid.UUID) (*engine.DetectResult, error) {
	if f.fail[id] {
		return nil, errors.New("calendar not connected")
	}
	return &engine.DetectResult{}, nil
}

// SF-098: the weekly manager detection reports each manager's failure;
// a failed listing is the run's error.
func TestPAC122_ManagerDetectionReportsEachFailure(t *testing.T) {
	rec := lifecycletest.Record(t)
	captureLog(t)
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	run := managerDetectionRun(func() ([]uuid.UUID, error) { return []uuid.UUID{a, b, c}, nil },
		fakeDetector{fail: map[uuid.UUID]bool{a: true, c: true}})
	if err := run(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{a, c} {
		if got := rec.Matching("cron", "detect "+id.String()+": calendar not connected"); len(got) != 1 || got[0].Unit.ID != "manager-detection" {
			t.Errorf("manager %s: reports %+v", id, rec.All())
		}
	}
	if n := len(rec.All()); n != 2 {
		t.Errorf("%d reports, want 2", n)
	}
	listErr := managerDetectionRun(func() ([]uuid.UUID, error) { return nil, errors.New("db down") }, fakeDetector{})(context.Background())
	if listErr == nil || !strings.Contains(listErr.Error(), "db down") {
		t.Fatalf("list failure = %v", listErr)
	}
}

// §6: main's reporter sends the failure to Sentry tagged with the unit's
// kind and id.
func TestPAC122_ReporterTagsUnitKindAndID(t *testing.T) {
	tr := &stubTransport{}
	if err := sentry.Init(sentry.ClientOptions{Dsn: "https://k@sentry.invalid/1", Transport: tr}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sentry.Init(sentry.ClientOptions{}) })
	t.Cleanup(lifecycle.SetReporter(nil))
	installReporters()
	lifecycle.Report(lifecycle.Unit{Kind: "habit-reoptimize", ID: "u-1"}, errors.New("list habits: db down"))
	sentry.Flush(time.Second)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.events) != 1 {
		t.Fatalf("%d events", len(tr.events))
	}
	e := tr.events[0]
	if e.Tags["unit.kind"] != "habit-reoptimize" || e.Tags["unit.id"] != "u-1" {
		t.Fatalf("tags %v", e.Tags)
	}
	if len(e.Exception) == 0 || !strings.Contains(e.Exception[len(e.Exception)-1].Value, "db down") {
		t.Fatalf("exception %+v", e.Exception)
	}
}

// SF-099: a failed automatic conversion is reported, not only logged.
func TestPAC122_ConversionFailureReported(t *testing.T) {
	captureLog(t)
	dsn, cleanup, err := testdb.Create()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cleanup() })
	db, err := storage.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	prev := secretbox.Current()
	t.Cleanup(func() { secretbox.Install(prev); secretbox.SetSealGate(nil); secretbox.SetReporter(nil) })
	cfg, _ := pac84Keys(t, "pac122")
	enc, err := startEncryption(context.Background(), db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	// A pass that cannot read a secret column fails.
	if _, err := db.Exec(`ALTER TABLE settings RENAME COLUMN webcal_url TO webcal_url_pac122`); err != nil {
		t.Fatal(err)
	}
	rec := lifecycletest.Record(t)
	enc.convert(context.Background())
	if got := rec.Matching("encryption", "conversion stopped"); len(got) != 1 || got[0].Unit.ID != "conversion" {
		t.Fatalf("reports %+v", rec.All())
	}
}
