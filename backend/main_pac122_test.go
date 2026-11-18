package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/internal/lifecycle"
	"github.com/Enach/paceday/backend/internal/lifecycle/lifecycletest"
	"github.com/Enach/paceday/backend/internal/secretbox"
	"github.com/Enach/paceday/backend/internal/testdb"
	"github.com/Enach/paceday/backend/storage"
	"github.com/getsentry/sentry-go"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
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

// PAC-122 review: a panicking keyring-refresher tick is recovered, reported
// and released; the refresher keeps running.
func TestPAC122_RefresherTickPanicRecovered(t *testing.T) {
	rec := lifecycletest.Record(t)
	captureLog(t)
	g := lifecycle.NewGroup()
	defer lifecycle.SetDefault(g)()
	r := &encryptionRuntime{}
	ok := r.tickOnce(context.Background(), func(context.Context) bool {
		var keys []int
		return keys[len(keys)] > 0
	})
	if !ok {
		t.Fatal("a panicking tick stopped the refresher")
	}
	if !g.Wait(context.Background()) {
		t.Fatal("the tick was not released")
	}
	if got := rec.Matching("encryption", "index out of range"); len(got) != 1 || got[0].Unit.ID != "keyring-refresher" {
		t.Fatalf("reports %+v", rec.All())
	}
}

// PAC-122 review blocker 1: the weekly detection, run with the real
// ManagerEngine into a storage failure on a detected member, reports and
// logs the failure without the member's (or anyone's) address, and Sentry
// receives none either.
func TestPAC122_ManagerDetectionRealEngineNoAddress(t *testing.T) {
	logs := captureLog(t)
	tr := &stubTransport{}
	if err := sentry.Init(sentry.ClientOptions{Dsn: "https://k@sentry.invalid/1", Transport: tr}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sentry.Init(sentry.ClientOptions{}) })
	t.Cleanup(lifecycle.SetReporter(nil))
	installReporters()

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
	mgr, err := storage.UpsertUser(db, "boss@pac122.test", "Boss", "", "google", "sub-boss")
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.UpsertUserToken(db, mgr.ID, &oauth2.Token{AccessToken: "tok", RefreshToken: "r", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	// Two weekly 1:1s with one report: a detected member.
	now := time.Now().UTC()
	ev := func(id string, at time.Time) map[string]any {
		return map[string]any{
			"id": id, "recurringEventId": "series-1",
			"start": map[string]string{"dateTime": at.Format(time.RFC3339)},
			"end":   map[string]string{"dateTime": at.Add(30 * time.Minute).Format(time.RFC3339)},
			"attendees": []map[string]any{
				{"email": "boss@pac122.test", "self": true},
				{"email": "alex.report@pac122.test", "displayName": "Alex"},
			},
		}
	}
	google := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{ev("e1", now.Add(-14*24*time.Hour)), ev("e2", now.Add(-7*24*time.Hour))}})
	}))
	defer google.Close()
	calendar.Configure(config.CalendarEndpoint{BaseURL: google.URL + config.CalendarAPIRoot, Guard: true})
	t.Cleanup(func() { calendar.Configure(config.ProductionCalendarEndpoint()) })
	// The member lookup fails.
	if _, err := db.Exec(`ALTER TABLE manager_team_members RENAME TO manager_team_members_pac122`); err != nil {
		t.Fatal(err)
	}

	rec := lifecycletest.Record(t)
	run := managerDetectionRun(func() ([]uuid.UUID, error) { return []uuid.UUID{mgr.ID}, nil },
		&engine.ManagerEngine{DB: db, OAuthConfig: &oauth2.Config{}})
	if err := run(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := rec.Matching("cron", "detected member #1")
	if len(got) != 1 {
		t.Fatalf("the engine did not reach the member step: reports %+v; log %s", rec.All(), logs.String())
	}
	if strings.Contains(got[0].Err.Error(), "@") {
		t.Fatalf("report carries an address: %v", got[0].Err)
	}
	if strings.Contains(logs.String(), "alex.report") {
		t.Fatalf("log carries the member's address: %s", logs.String())
	}
	// What Sentry would receive: the reporter installed by main.
	restore := lifecycle.SetReporter(reportUnitFailure)
	lifecycle.Report(lifecycle.Unit{Kind: "cron", ID: "manager-detection"}, fmt.Errorf("wrapped: %w", errors.New("member alex.report@pac122.test")))
	restore()
	sentry.Flush(time.Second)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	for _, e := range tr.events {
		b, _ := json.Marshal(e)
		if strings.Contains(string(b), "alex.report") {
			t.Fatalf("Sentry event carries the address: %s", b)
		}
	}
}
