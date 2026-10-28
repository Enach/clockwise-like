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
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/internal/budget"
	"github.com/Enach/paceday/backend/internal/lifecycle"
	"github.com/Enach/paceday/backend/internal/outbound"
	"github.com/Enach/paceday/backend/internal/peritem"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	googlecalendar "google.golang.org/api/calendar/v3"
)

// PAC-81 Part A engine tests. Bounds are scaled down: provider 150 ms,
// identity 50 ms (item limit 200 ms), stop margin 100 ms, 4 workers.

func pac81Scale(t *testing.T) {
	t.Helper()
	l := outbound.Default
	l.Provider, l.Identity = 150*time.Millisecond, 50*time.Millisecond
	t.Cleanup(outbound.SetForTest(l))
	t.Cleanup(peritem.SetForTest(peritem.Settings{Workers: 4, Margin: 100 * time.Millisecond}))
	g := lifecycle.NewGroup()
	t.Cleanup(lifecycle.SetDefault(g))
}

// behaviour of the Google stub for one user's token.
type stubBehaviour struct {
	delay  time.Duration
	status int // non-zero: answer this error status
}

// googleStub is a Google Calendar API stand-in keyed by bearer token.
type googleStub struct {
	mu       sync.Mutex
	by       map[string]stubBehaviour
	lists    []time.Time // when each list call started
	creates  map[string]int
	deletes  int
	createAt []time.Time
}

func newGoogleStub(t *testing.T) *googleStub {
	t.Helper()
	g := &googleStub{by: map[string]stubBehaviour{}, creates: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(srv.Close)
	calendar.Configure(config.CalendarEndpoint{BaseURL: srv.URL + config.CalendarAPIRoot, Guard: true})
	t.Cleanup(func() { calendar.Configure(config.ProductionCalendarEndpoint()) })
	return g
}

func (g *googleStub) set(token string, b stubBehaviour) {
	g.mu.Lock()
	g.by[token] = b
	g.mu.Unlock()
}

func (g *googleStub) serve(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	g.mu.Lock()
	b := g.by[tok]
	g.mu.Unlock()
	var ev googlecalendar.Event
	if r.Method == http.MethodPost {
		_ = json.NewDecoder(r.Body).Decode(&ev)
	}
	g.mu.Lock()
	switch r.Method {
	case http.MethodGet:
		g.lists = append(g.lists, time.Now())
	case http.MethodPost:
		g.createAt = append(g.createAt, time.Now())
	}
	g.mu.Unlock()
	select {
	case <-time.After(b.delay):
	case <-r.Context().Done():
		return
	}
	if b.status != 0 {
		w.WriteHeader(b.status)
		_, _ = w.Write([]byte(`{"error":{"code":500,"message":"stub failure"}}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		_, _ = w.Write([]byte(`{"items":[]}`))
	case http.MethodPost:
		key := tok + "|" + ev.Start.DateTime
		g.mu.Lock()
		g.creates[key]++
		n := len(g.creates)
		g.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{"id": fmt.Sprintf("ev-%d", n)})
	case http.MethodDelete:
		g.mu.Lock()
		g.deletes++
		g.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}
}

func (g *googleStub) createCount() (total int, maxPerKey int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, n := range g.creates {
		total += n
		if n > maxPerKey {
			maxPerKey = n
		}
	}
	return
}

// pac81User creates a user with a valid (not expiring) Google token named
// after its id.
func pac81User(t *testing.T, db storage.DBTX, email string) (uuid.UUID, string) {
	t.Helper()
	u, err := storage.UpsertUser(db, email, "User "+email, "", "google", "sub-"+email)
	if err != nil {
		t.Fatal(err)
	}
	tok := "tok-" + u.ID.String()
	if err := auth.UpsertUserToken(db, u.ID, &oauth2.Token{AccessToken: tok, RefreshToken: "r", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	return u.ID, tok
}

func pac81Link(t *testing.T, db storage.DBTX, hosts []uuid.UUID) *storage.SchedulingLink {
	t.Helper()
	link, err := storage.CreateSchedulingLink(db, &storage.SchedulingLink{
		OwnerUserID: hosts[0], Slug: "pac81-" + uuid.NewString()[:8], Title: "Intro",
		DurationOptions: []int{30}, DaysOfWeek: []int{1, 2, 3, 4, 5}, WindowStart: "09:00", WindowEnd: "17:00",
		UsageType: "reusable", Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hosts {
		if _, err := storage.AddLinkHost(db, link.ID, h, "accepted"); err != nil {
			t.Fatal(err)
		}
	}
	return link
}

func nextWeekday(from time.Time) time.Time {
	d := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC).Add(48 * time.Hour)
	for d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
		d = d.Add(24 * time.Hour)
	}
	return d
}

// --- AC-A18: collective slot listing -----------------------------------------

func TestPAC81_Slots3HostsComplete(t *testing.T) {
	pac81Scale(t)
	db := openTestDB(t)
	g := newGoogleStub(t)
	var hosts []uuid.UUID
	for i := 0; i < 3; i++ {
		id, tok := pac81User(t, db, fmt.Sprintf("slots-%d@pac81.test", i))
		g.set(tok, stubBehaviour{delay: 140 * time.Millisecond})
		hosts = append(hosts, id)
	}
	link := pac81Link(t, db, hosts)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	slots, err := (&BookingEngine{DB: db}).CollectiveSlots(ctx, link, nextWeekday(time.Now()), 30)
	if err != nil || len(slots) == 0 {
		t.Fatalf("slots %d err %v", len(slots), err)
	}
	if len(g.lists) != 3 {
		t.Fatalf("%d list calls, want 3", len(g.lists))
	}
}

// One host failing with a provider error: today's 200 without that host;
// one host timing out on its own limit: treated as free (200).
func TestPAC81_HostProviderErrorAndOwnLimitTreatedFree(t *testing.T) {
	pac81Scale(t)
	db := openTestDB(t)
	g := newGoogleStub(t)
	ok, okTok := pac81User(t, db, "ok@pac81.test")
	bad, badTok := pac81User(t, db, "bad@pac81.test")
	slow, slowTok := pac81User(t, db, "slow@pac81.test")
	g.set(okTok, stubBehaviour{})
	g.set(badTok, stubBehaviour{status: http.StatusInternalServerError})
	g.set(slowTok, stubBehaviour{delay: time.Hour}) // fails on its own 150 ms limit
	link := pac81Link(t, db, []uuid.UUID{ok, bad, slow})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	slots, err := (&BookingEngine{DB: db}).CollectiveSlots(ctx, link, nextWeekday(time.Now()), 30)
	if err != nil || len(slots) == 0 {
		t.Fatalf("slots %d err %v", len(slots), err)
	}
}

// AC-A18 rev 2: a read whose deadline the budget shortened, and that hits
// it, is a budget cut (503), not "free".
func TestPAC81_ShortenedDeadlineIsBudgetCut(t *testing.T) {
	pac81Scale(t)
	db := openTestDB(t)
	g := newGoogleStub(t)
	h, tok := pac81User(t, db, "short@pac81.test")
	g.set(tok, stubBehaviour{delay: time.Hour})
	link := pac81Link(t, db, []uuid.UUID{h})
	// remaining (250 ms) - margin (100 ms) = 150 ms < item limit 200 ms.
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if _, err := (&BookingEngine{DB: db}).CollectiveSlots(ctx, link, nextWeekday(time.Now()), 30); !errors.Is(err, budget.ErrExpired) {
		t.Fatalf("got %v, want a budget cut", err)
	}
}

// --- AC-A18: team availability -------------------------------------------------

func pac81Team(t *testing.T, db storage.DBTX, g *googleStub, n int, delay time.Duration) uuid.UUID {
	t.Helper()
	owner, tok := pac81User(t, db, fmt.Sprintf("owner-%s@pac81.test", uuid.NewString()[:6]))
	g.set(tok, stubBehaviour{delay: delay})
	team, err := storage.CreateTeam(db, "T", owner)
	if err != nil {
		t.Fatal(err)
	}
	members, _ := storage.ListTeamMembers(db, team.ID)
	for len(members) < n {
		id, tok := pac81User(t, db, fmt.Sprintf("m-%s@pac81.test", uuid.NewString()[:6]))
		g.set(tok, stubBehaviour{delay: delay})
		if err := storage.AddTeamMember(db, team.ID, id, "member"); err != nil {
			t.Fatal(err)
		}
		members, _ = storage.ListTeamMembers(db, team.ID)
	}
	return team.ID
}

func TestPAC81_TeamAvailability5MembersTwoWaves(t *testing.T) {
	pac81Scale(t)
	db := openTestDB(t)
	g := newGoogleStub(t)
	team := pac81Team(t, db, g, 5, 140*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	slots, err := (&TeamAvailabilityEngine{DB: db}).FindSlots(ctx, team, nextWeekday(time.Now()), 30)
	if err != nil || len(slots) == 0 {
		t.Fatalf("slots %d err %v", len(slots), err)
	}
	if len(g.lists) != 5 {
		t.Fatalf("%d list calls", len(g.lists))
	}
	if took := time.Since(start); took < 270*time.Millisecond {
		t.Fatalf("took %s: not two waves of 4 workers", took)
	}
}

func TestPAC81_TeamAvailability20MembersCut(t *testing.T) {
	pac81Scale(t)
	db := openTestDB(t)
	g := newGoogleStub(t)
	team := pac81Team(t, db, g, 20, 140*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	deadline, _ := ctx.Deadline()
	_, err := (&TeamAvailabilityEngine{DB: db}).FindSlots(ctx, team, nextWeekday(time.Now()), 30)
	if !errors.Is(err, budget.ErrExpired) {
		t.Fatalf("got %v, want a budget cut", err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.lists) >= 20 {
		t.Fatal("every member was read")
	}
	for _, at := range g.lists {
		if !at.Before(deadline.Add(-100 * time.Millisecond)) {
			t.Fatalf("a call started %s before the end, inside the margin", deadline.Sub(at))
		}
	}
}

// --- AC-A17: personal-calendar sync ------------------------------------------

func pac81Personal(t *testing.T, db storage.DBTX, g *googleStub, n int) (uuid.UUID, int64) {
	t.Helper()
	user, tok := pac81User(t, db, fmt.Sprintf("personal-%s@pac81.test", uuid.NewString()[:6]))
	g.set(tok, stubBehaviour{delay: 140 * time.Millisecond})
	if _, err := storage.GetOrCreateSettingsByUser(db, user); err != nil {
		t.Fatal(err)
	}
	day := nextWeekday(time.Now())
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\n")
	for i := 0; i < n; i++ {
		s := day.Add(9*time.Hour + time.Duration(i)*10*time.Minute)
		fmt.Fprintf(&b, "BEGIN:VEVENT\r\nUID:ev-%d\r\nSUMMARY:x\r\nDTSTART:%s\r\nDTEND:%s\r\nEND:VEVENT\r\n",
			i, s.Format("20060102T150405Z"), s.Add(5*time.Minute).Format("20060102T150405Z"))
	}
	b.WriteString("END:VCALENDAR\r\n")
	ics := b.String()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(ics)) }))
	t.Cleanup(srv.Close)
	id, err := storage.InsertPersonalCalendar(db, &storage.PersonalCalendar{UserID: user, Provider: "webcal", Name: "Home", URL: srv.URL, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return user, id
}

func TestPAC81_PersonalSync6EventsTwoWaves(t *testing.T) {
	pac81Scale(t)
	db := openTestDB(t)
	g := newGoogleStub(t)
	user, cal := pac81Personal(t, db, g, 6)
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), auth.UserIDKey, user), 3*time.Second)
	defer cancel()
	start := time.Now()
	if err := (&PersonalBlocker{DB: db}).Sync(ctx, cal); err != nil {
		t.Fatal(err)
	}
	total, maxPer := g.createCount()
	if total != 6 || maxPer != 1 {
		t.Fatalf("creates %d (max per event %d)", total, maxPer)
	}
	if took := time.Since(start); took < 270*time.Millisecond {
		t.Fatalf("took %s: not two waves", took)
	}
	if bl, _ := storage.ListPersonalBlockers(db, cal); len(bl) != 6 {
		t.Fatalf("%d blockers recorded", len(bl))
	}
}

func TestPAC81_PersonalSync40EventsCutThenRerun(t *testing.T) {
	pac81Scale(t)
	db := openTestDB(t)
	g := newGoogleStub(t)
	user, cal := pac81Personal(t, db, g, 40)
	base := context.WithValue(context.Background(), auth.UserIDKey, user)
	ctx, cancel := context.WithTimeout(base, 700*time.Millisecond)
	err := (&PersonalBlocker{DB: db}).Sync(ctx, cal)
	cancel()
	if !errors.Is(err, budget.ErrExpired) {
		t.Fatalf("first run: %v, want a budget cut", err)
	}
	first, _ := g.createCount()
	if first == 0 || first >= 40 {
		t.Fatalf("first run created %d", first)
	}
	if bl, _ := storage.ListPersonalBlockers(db, cal); len(bl) != first {
		t.Fatalf("recorded %d of %d answered creates", len(bl), first)
	}
	ctx, cancel = context.WithTimeout(base, 10*time.Second)
	defer cancel()
	if err := (&PersonalBlocker{DB: db}).Sync(ctx, cal); err != nil {
		t.Fatalf("re-run: %v", err)
	}
	total, maxPer := g.createCount()
	if total != 40 || maxPer != 1 {
		t.Fatalf("across both runs: %d creates, max %d per event", total, maxPer)
	}
}

// An ICS feed over the size limit fails the sync; nothing is created.
func TestPAC81_PersonalSyncICSOverSizeCreatesNothing(t *testing.T) {
	pac81Scale(t)
	l := outbound.Current()
	l.ICSMaxBytes = 200
	defer outbound.SetForTest(l)()
	db := openTestDB(t)
	g := newGoogleStub(t)
	user, cal := pac81Personal(t, db, g, 6)
	ctx := context.WithValue(context.Background(), auth.UserIDKey, user)
	err := (&PersonalBlocker{DB: db}).Sync(ctx, cal)
	if err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("got %v", err)
	}
	if total, _ := g.createCount(); total != 0 {
		t.Fatalf("%d blockers from a partial feed", total)
	}
}

// --- AC-A19 / AC-A8: booking completion ----------------------------------------

func swapSendMail(t *testing.T, f func(string, smtp.Auth, string, []string, []byte) error) {
	t.Helper()
	prev := sendMail
	sendMail = f
	t.Cleanup(func() { sendMail = prev })
	t.Setenv("SMTP_HOST", "smtp.pac81.test")
	t.Setenv("SMTP_FROM", "noreply@pac81.test")
}

// (a) The budget ends after commit with one host done: the booking is
// returned; the remaining host events and the email complete afterwards,
// each exactly once.
func TestPAC81_BookingBudgetAfterCommitCompletesInBackground(t *testing.T) {
	pac81Scale(t)
	l := outbound.Current()
	l.Provider = 2 * time.Second
	defer outbound.SetForTest(l)()
	db := openTestDB(t)
	g := newGoogleStub(t)
	var mails sync.Map
	var mailCount int
	var mailMu sync.Mutex
	swapSendMail(t, func(_ string, _ smtp.Auth, _ string, to []string, _ []byte) error {
		mailMu.Lock()
		mailCount++
		mailMu.Unlock()
		mails.Store(to[0], true)
		return nil
	})
	fast, fastTok := pac81User(t, db, "fast@pac81.test")
	slow1, s1 := pac81User(t, db, "slow1@pac81.test")
	slow2, s2 := pac81User(t, db, "slow2@pac81.test")
	g.set(fastTok, stubBehaviour{})
	g.set(s1, stubBehaviour{delay: 600 * time.Millisecond})
	g.set(s2, stubBehaviour{delay: 600 * time.Millisecond})
	link := pac81Link(t, db, []uuid.UUID{fast, slow1, slow2})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := nextWeekday(time.Now()).Add(10 * time.Hour)
	booking, err := (&BookingEngine{DB: db}).ConfirmBooking(ctx, link, "Booker", "booker@pac81.test", start, start.Add(30*time.Minute), "")
	if err != nil || booking == nil {
		t.Fatalf("booking %v err %v", booking, err)
	}
	if total, _ := g.createCount(); total != 1 {
		t.Fatalf("%d host events done when the budget ended, want 1", total)
	}
	if !lifecycle.Default().Wait(context.Background()) {
		t.Fatal("unit never finished")
	}
	total, maxPer := g.createCount()
	if total != 3 || maxPer != 1 {
		t.Fatalf("host events %d (max per host %d)", total, maxPer)
	}
	if mailCount != 1 {
		t.Fatalf("%d emails", mailCount)
	}
	var recorded int
	_ = db.QueryRow(`SELECT COUNT(*) FROM booking_events WHERE booking_id = $1`, booking.ID).Scan(&recorded)
	if recorded != 3 {
		t.Fatalf("%d host events recorded", recorded)
	}
}

// (b) SIGTERM after commit with stubs slower than the drain: the unit is
// abandoned and logged with the booking id and the hosts done and not done.
func TestPAC81_BookingCompletionAbandonedAtDrainIsLogged(t *testing.T) {
	pac81Scale(t)
	l := outbound.Current()
	l.Provider = 5 * time.Second
	defer outbound.SetForTest(l)()
	db := openTestDB(t)
	g := newGoogleStub(t)
	swapSendMail(t, func(string, smtp.Auth, string, []string, []byte) error { return nil })
	fast, fastTok := pac81User(t, db, "drain-fast@pac81.test")
	slow, slowTok := pac81User(t, db, "drain-slow@pac81.test")
	g.set(fastTok, stubBehaviour{})
	g.set(slowTok, stubBehaviour{delay: time.Hour})
	link := pac81Link(t, db, []uuid.UUID{fast, slow})

	var buf bytes.Buffer
	var bufMu sync.Mutex
	defer lifecycle.SetLogf(func(f string, a ...any) {
		bufMu.Lock()
		defer bufMu.Unlock()
		fmt.Fprintf(&buf, f+"\n", a...)
	})()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := nextWeekday(time.Now()).Add(11 * time.Hour)
	booking, err := (&BookingEngine{DB: db}).ConfirmBooking(ctx, link, "B", "b@pac81.test", start, start.Add(30*time.Minute), "")
	if err != nil {
		t.Fatal(err)
	}
	ph := lifecycle.Phases{Notice: 10 * time.Millisecond, Drain: 200 * time.Millisecond, Grace: 50 * time.Millisecond}
	if lifecycle.Default().Shutdown(&http.Server{}, ph, nil, nil) {
		t.Fatal("clean shutdown with a unit abandoned")
	}
	bufMu.Lock()
	out := buf.String()
	bufMu.Unlock()
	want := fmt.Sprintf("abandoned booking-completion %s (hosts done: [%s]; hosts not done: [%s]", booking.ID, fast, slow)
	if !strings.Contains(out, want) {
		t.Fatalf("log %q lacks %q", out, want)
	}
	if strings.Contains(out, "@") {
		t.Fatalf("an email address was logged: %q", out)
	}
}

// AC-A8: a failing booking confirmation email is logged at error level with
// the booking id (no address) and not retried.
func TestPAC81_BookingEmailFailureLogged(t *testing.T) {
	pac81Scale(t)
	db := openTestDB(t)
	newGoogleStub(t)
	var calls int
	swapSendMail(t, func(string, smtp.Auth, string, []string, []byte) error {
		calls++
		return errors.New("smtp rcpt: 550 <booker@pac81.test> rejected")
	})
	host, _ := pac81User(t, db, "mailhost@pac81.test")
	link := pac81Link(t, db, []uuid.UUID{host})
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)
	start := nextWeekday(time.Now()).Add(12 * time.Hour)
	booking, err := (&BookingEngine{DB: db}).ConfirmBooking(context.Background(), link, "B", "booker@pac81.test", start, start.Add(30*time.Minute), "")
	if err != nil {
		t.Fatal(err)
	}
	lifecycle.Default().Wait(context.Background())
	out := buf.String()
	if !strings.Contains(out, "ERROR: booking confirmation email failed for booking "+booking.ID.String()) {
		t.Fatalf("log %q", out)
	}
	if strings.Contains(out, "booker@pac81.test") {
		t.Fatal("the address was logged")
	}
	if calls != 1 {
		t.Fatalf("%d sends: a failure must not be retried", calls)
	}
}

// AC-A19: the budget gone before the insert: no row, budget error.
func TestPAC81_BookingBudgetBeforeInsertWritesNothing(t *testing.T) {
	pac81Scale(t)
	db := openTestDB(t)
	host, _ := pac81User(t, db, "nohost@pac81.test")
	link := pac81Link(t, db, []uuid.UUID{host})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := nextWeekday(time.Now()).Add(13 * time.Hour)
	_, err := (&BookingEngine{DB: db}).ConfirmBooking(ctx, link, "B", "b@pac81.test", start, start.Add(30*time.Minute), "")
	if err == nil {
		t.Fatal("booked with the budget gone")
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM bookings WHERE link_id = $1`, link.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("%d bookings written", n)
	}
}
