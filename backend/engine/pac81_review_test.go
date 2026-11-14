package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/budget"
	"github.com/Enach/paceday/backend/internal/lifecycle"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// Review of #247, blocking 1: a booking committed after the drain has begun
// still gets every host event (creates are not gated by the drain deadline;
// only the drain's hard cancellation cuts them), and the drain is clean.
func TestPAC81_BookingCommittedDuringDrainCreatesHostEvents(t *testing.T) {
	pac81Scale(t)
	db := openTestDB(t)
	g := newGoogleStub(t)
	swapSendMail(t, func(string, smtp.Auth, string, []string, []byte) error { return nil })
	var hosts []uuid.UUID
	for i := 0; i < 3; i++ {
		id, tok := pac81User(t, db, fmt.Sprintf("drain-%d@pac81.test", i))
		g.set(tok, stubBehaviour{createDelay: 50 * time.Millisecond})
		hosts = append(hosts, id)
	}
	link := pac81Link(t, db, hosts)

	// Scaled like production: a create's limit plus the margin (200 + 100 ms)
	// is longer than the whole drain (280 ms), as 25 + 10 s is against 30 s.
	grp := lifecycle.Default()
	busy := grp.Track(lifecycle.Unit{Kind: "request", ID: "POST /api/book/x"}) // keeps the drain open
	result := make(chan bool, 1)
	go func() {
		result <- grp.Shutdown(&http.Server{}, lifecycle.Phases{Drain: 280 * time.Millisecond, Grace: 100 * time.Millisecond}, nil, nil)
	}()
	for grp.DrainDeadline().IsZero() {
		time.Sleep(time.Millisecond)
	}
	start := nextWeekday(time.Now()).Add(15 * time.Hour)
	if _, err := (&BookingEngine{DB: db}).ConfirmBooking(context.Background(), link, "B", "b@pac81.test", start, start.Add(30*time.Minute), "", ""); err != nil {
		t.Fatal(err)
	}
	busy()
	if !<-result {
		t.Fatal("drain not clean")
	}
	if total, maxPer := g.createCount(); total != 3 || maxPer != 1 {
		t.Fatalf("host events %d (max per host %d), want 3 committed during the drain", total, maxPer)
	}
}

// Review of #247, blocking 1: a host whose event was not created is logged
// by id with the booking id (never silently dropped).
func TestPAC81_BookingSkippedHostIsLogged(t *testing.T) {
	pac81Scale(t)
	db := openTestDB(t)
	g := newGoogleStub(t)
	swapSendMail(t, func(string, smtp.Auth, string, []string, []byte) error { return nil })
	ok, okTok := pac81User(t, db, "skip-ok@pac81.test")
	bad, badTok := pac81User(t, db, "skip-bad@pac81.test")
	g.set(okTok, stubBehaviour{})
	// Reads succeed (the D-6 check passes); the create fails.
	g.set(badTok, stubBehaviour{createDelay: time.Hour})
	link := pac81Link(t, db, []uuid.UUID{ok, bad})
	logged := captureStdLog(t)
	start := nextWeekday(time.Now()).Add(16 * time.Hour)
	booking, err := (&BookingEngine{DB: db}).ConfirmBooking(context.Background(), link, "B", "b@pac81.test", start, start.Add(30*time.Minute), "", "")
	if err != nil {
		t.Fatal(err)
	}
	lifecycle.Default().Wait(context.Background())
	out := logged()
	want := fmt.Sprintf("ERROR: booking %s: not every host event was created (hosts done: [%s]; hosts not done: [%s]", booking.ID, ok, bad)
	if !strings.Contains(out, want) {
		t.Fatalf("log %q lacks %q", out, want)
	}
}

// Review of #247, blocking 3: the pool is saturated just before the insert:
// the wait for the insert's connection ends at the budget with the 503, and
// nothing is written; never a hang past the budget.
func TestPAC81_BookingPoolSaturatedBeforeInsertIs503(t *testing.T) {
	pac81Scale(t)
	db := openTestDB(t)
	g := newGoogleStub(t)
	host, tok := pac81User(t, db, "sat@pac81.test")
	g.set(tok, stubBehaviour{})
	link := pac81Link(t, db, []uuid.UUID{host})
	db.SetMaxOpenConns(1)
	var held atomic.Pointer[heldConn]
	beforeBookingConn = func() {
		c, err := db.Conn(context.Background())
		if err == nil {
			held.Store(&heldConn{c: c})
		}
	}
	defer func() {
		beforeBookingConn = nil
		if h := held.Load(); h != nil {
			_ = h.c.Close()
		}
	}()
	start := nextWeekday(time.Now()).Add(14 * time.Hour)
	var engErr error
	done := make(chan struct{})
	h := budget.Middleware(400*time.Millisecond, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		_, engErr = (&BookingEngine{DB: db}).ConfirmBooking(r.Context(), link, "B", "b@pac81.test", start, start.Add(30*time.Minute), "", "")
	}))
	rec := httptest.NewRecorder()
	began := time.Now()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/book/x", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", rec.Code)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the handler is still waiting for a connection after the budget")
	}
	if took := time.Since(began); took > 2*time.Second {
		t.Fatalf("took %s", took)
	}
	if !errorsIsExpired(engErr) {
		t.Fatalf("engine error %v, want the budget's", engErr)
	}
	held.Load().c.Close()
	db.SetMaxOpenConns(0)
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM bookings WHERE link_id = $1`, link.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("%d bookings written", n)
	}
}

type heldConn struct{ c interface{ Close() error } }

func errorsIsExpired(err error) bool { return errors.Is(err, budget.ErrExpired) }

// Review of #247, blocking 2: with one recap user, the cron run's context
// ends as soon as RunAll returns; the user's send still succeeds.
func TestPAC81_RecapOnlyUserIsSent(t *testing.T) {
	pac81Scale(t)
	db := openTestDB(t)
	var posts atomic.Int32
	slack := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond) // answered after RunAll returned
		if strings.HasSuffix(r.URL.Path, "/chat.postMessage") {
			posts.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "message": map[string]string{"ts": "1.2"}})
	}))
	defer slack.Close()
	prev := slackAPIBase
	slackAPIBase = slack.URL
	defer func() { slackAPIBase = prev }()

	u, err := storage.UpsertUser(db, "recap-only@pac81.test", "Only", "", "google", "recap-only")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 2, 8, 30, 0, 0, time.UTC)
	s, _ := storage.GetOrCreateSettingsByUser(db, u.ID)
	s.RecapEnabled, s.RecapSendTime, s.RecapSendTo, s.RecapChannelID = true, "08:30", "channel", "C1"
	s.Timezone = "UTC"
	if err := storage.SaveRecapSettingsForUser(db, u.ID, s); err != nil {
		t.Fatal(err)
	}
	if err := storage.SaveSettingsForUser(db, u.ID, s); err != nil {
		t.Fatal(err)
	}
	if err := storage.UpsertWorkspaceConnection(db, &storage.WorkspaceConnection{UserID: u.ID, Provider: "slack", AccessToken: "a", BotToken: "xoxb", WorkspaceID: "W", Scopes: []string{}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	(&DailyRecapService{DB: db, Clock: FixedClock{T: now}}).RunAll(ctx)
	cancel() // the cron closure's context ends here
	lifecycle.Default().Wait(context.Background())
	if posts.Load() != 1 {
		t.Fatalf("%d Slack posts, want 1", posts.Load())
	}
	var status string
	_ = db.QueryRow(`SELECT status FROM recap_sends WHERE user_id = $1`, u.ID).Scan(&status)
	if status != "sent" {
		t.Fatalf("recap status %q, want sent", status)
	}
}

// captureStdLog sends the standard logger to a buffer until the test ends and
// returns a reader of what was logged so far. Writes and reads share a mutex:
// a goroutine the test does not wait for (an oauth2 token refresh, say) may
// still log while the test reads (PAC-110 item 3).
func captureStdLog(t *testing.T) func() string {
	t.Helper()
	var (
		mu  sync.Mutex
		buf bytes.Buffer
	)
	prev := log.Writer()
	log.SetOutput(writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return buf.Write(p)
	}))
	t.Cleanup(func() { log.SetOutput(prev) })
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}
}
