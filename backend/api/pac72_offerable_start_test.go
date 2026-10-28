package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// PAC-72: POST /api/book/{slug} accepts only a start GET /api/book/{slug}/slots
// would offer at that moment, else 409 with nothing written. These tests drive
// the real handlers and the real booking engine against the test database and
// a recording Google Calendar stand-in (the PAC-43 seam), with a fixed clock.

const pac72Refusal = `{"error":"slot is no longer available"}`

// Fixed instants. 2030-01-07 is a Monday.
var (
	pac72Mon = time.Date(2030, 1, 7, 0, 0, 0, 0, time.UTC)
	pac72Tue = pac72Mon.AddDate(0, 0, 1)
	pac72Sat = pac72Mon.AddDate(0, 0, 5)
)

func pac72At(day time.Time, hhmm string) time.Time {
	var h, m int
	_, _ = fmt.Sscanf(hhmm, "%d:%d", &h, &m)
	return time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, time.UTC)
}

// pac72Calendar stands in for Google Calendar: it answers events.list from the
// events seeded for the presented credential (filtered to the requested range,
// as Google does), records every events.insert, and fails every request of a
// credential marked failing.
type pac72Calendar struct {
	mu      sync.Mutex
	events  map[string][]pac72Event
	failing map[string]bool
	inserts map[string]int
}

type pac72Event struct {
	start, end  time.Time
	transparent bool
}

func newPAC72Calendar(t *testing.T) *pac72Calendar {
	t.Helper()
	c := &pac72Calendar{events: map[string][]pac72Event{}, failing: map[string]bool{}, inserts: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		c.mu.Lock()
		fail := c.failing[bearer]
		isInsert := r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/events")
		if isInsert {
			c.inserts[bearer]++
		}
		evs := append([]pac72Event(nil), c.events[bearer]...)
		c.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":503,"message":"backend error"}}`))
			return
		}
		switch {
		case isInsert:
			var ev map[string]any
			_ = json.NewDecoder(r.Body).Decode(&ev)
			ev["id"], ev["kind"] = "pac72-created", "calendar#event"
			_ = json.NewEncoder(w).Encode(ev)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/events"):
			minT, _ := time.Parse(time.RFC3339, r.URL.Query().Get("timeMin"))
			maxT, _ := time.Parse(time.RFC3339, r.URL.Query().Get("timeMax"))
			items := []any{}
			for i, ev := range evs {
				if !ev.start.Before(maxT) || !ev.end.After(minT) {
					continue
				}
				item := map[string]any{"id": fmt.Sprintf("ev-%d", i), "status": "confirmed", "summary": "Busy",
					"start": map[string]string{"dateTime": ev.start.Format(time.RFC3339)},
					"end":   map[string]string{"dateTime": ev.end.Format(time.RFC3339)}}
				if ev.transparent {
					item["transparency"] = "transparent"
				}
				items = append(items, item)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"kind": "calendar#events", "items": items})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	calendar.Configure(config.CalendarEndpoint{BaseURL: srv.URL + config.CalendarAPIRoot, Guard: true})
	t.Cleanup(func() { calendar.Configure(config.ProductionCalendarEndpoint()) })
	return c
}

func (c *pac72Calendar) addEvent(bearer string, start, end time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events[bearer] = append(c.events[bearer], pac72Event{start: start, end: end})
}

func (c *pac72Calendar) setFailing(bearer string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failing[bearer] = true
}

func (c *pac72Calendar) totalInserts() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, v := range c.inserts {
		n += v
	}
	return n
}

func (c *pac72Calendar) insertsFor(bearer string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inserts[bearer]
}

// pac72Flow is the real booking engine, counting ConfirmBooking calls: a call
// is what writes the booking row, the hosts' calendar events and the email, so
// zero calls proves no email was attempted either.
type pac72Flow struct {
	*engine.BookingEngine
	confirms atomic.Int64
}

func (f *pac72Flow) ConfirmBooking(ctx context.Context, link *storage.SchedulingLink, name, email string, start, end time.Time, notes string) (*storage.Booking, error) {
	f.confirms.Add(1)
	return f.BookingEngine.ConfirmBooking(ctx, link, name, email, start, end, notes)
}

type pac72Bench struct {
	t    *testing.T
	db   *sql.DB
	cal  *pac72Calendar
	flow *pac72Flow
	h    *bookingHandlers
	r    *chi.Mux
}

func newPAC72Bench(t *testing.T, now time.Time) *pac72Bench {
	t.Helper()
	resetBookingLimiters(t)
	// These tests make many requests from one peer; the rate limits are
	// NEW-booking-rate-limit's subject and are tested there.
	for _, l := range []*fixedWindowLimiter{bookingCreateLimiter, bookingSlotsLimiter} {
		l.mu.Lock()
		l.limit = 1 << 30
		l.mu.Unlock()
	}
	db := openTestDB(t)
	b := &pac72Bench{t: t, db: db, cal: newPAC72Calendar(t)}
	b.flow = &pac72Flow{BookingEngine: &engine.BookingEngine{DB: db, OAuthConfig: &oauth2.Config{ClientID: "pac72"}}}
	b.h = newBookingHandlersWithEngine(db, b.flow)
	b.h.now = func() time.Time { return now }
	r := chi.NewRouter()
	r.Get("/api/book/{slug}", b.h.getLinkInfo)
	r.Get("/api/book/{slug}/slots", b.h.getSlots)
	r.Post("/api/book/{slug}", b.h.createBooking)
	b.r = r
	return b
}

// host creates a user whose Google credential is the stand-in's bearer
// "pac72-<email>" (returned), so its calendar is read and written there.
func (b *pac72Bench) host(email string) (uuid.UUID, string) {
	b.t.Helper()
	id := createTestUser(b.t, email)
	bearer := "pac72-" + email
	if err := auth.UpsertUserToken(b.db, id, &oauth2.Token{AccessToken: bearer, RefreshToken: "never-used", Expiry: time.Now().Add(24 * time.Hour)}); err != nil {
		b.t.Fatalf("seed token: %v", err)
	}
	return id, bearer
}

// link creates a link owned by owner, Monday to Friday 09:00-17:00, durations
// 30 and 60, no notice and no buffers unless mod changes them, with hosts as
// its accepted hosts.
func (b *pac72Bench) link(slug string, owner uuid.UUID, mod func(*storage.SchedulingLink), hosts ...uuid.UUID) *storage.SchedulingLink {
	b.t.Helper()
	l := &storage.SchedulingLink{
		OwnerUserID: owner, Slug: slug, Title: "PAC-72 " + slug, DurationOptions: []int{30, 60},
		DaysOfWeek: []int{1, 2, 3, 4, 5}, WindowStart: "09:00", WindowEnd: "17:00", UsageType: "reusable", Active: true,
	}
	if mod != nil {
		mod(l)
	}
	link, err := storage.CreateSchedulingLink(b.db, l)
	if err != nil {
		b.t.Fatalf("create link: %v", err)
	}
	for _, h := range hosts {
		if _, err := storage.AddLinkHost(b.db, link.ID, h, "accepted"); err != nil {
			b.t.Fatalf("add host: %v", err)
		}
	}
	return link
}

func (b *pac72Bench) booking(link *storage.SchedulingLink, start, end time.Time, status string) {
	b.t.Helper()
	bk, err := storage.CreateBooking(b.db, &storage.Booking{LinkID: link.ID, BookerName: "Seeded", BookerEmail: "seeded@example.com", StartTime: start, EndTime: end})
	if err != nil {
		b.t.Fatalf("create booking: %v", err)
	}
	if _, err := b.db.Exec(`UPDATE bookings SET status = $1 WHERE id = $2`, status, bk.ID); err != nil {
		b.t.Fatalf("set status: %v", err)
	}
}

// post books with an arbitrary JSON body and returns the status and the
// trimmed body.
func (b *pac72Bench) post(slug, body string) (int, string) {
	b.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/book/"+slug, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	b.r.ServeHTTP(rec, req)
	return rec.Code, strings.TrimSpace(rec.Body.String())
}

// book books start for duration minutes, by duration only.
func (b *pac72Bench) book(slug string, start time.Time, duration int) (int, string) {
	b.t.Helper()
	return b.post(slug, fmt.Sprintf(`{"name":"Visitor","email":"pac72-visitor@example.com","start":%q,"duration":%d}`, start.Format(time.RFC3339Nano), duration))
}

type pac72SlotsAnswer struct {
	code  int
	body  string
	slots []engine.AvailableSlot
	dates []string
}

func (b *pac72Bench) slots(slug, date string, duration int) pac72SlotsAnswer {
	b.t.Helper()
	target := fmt.Sprintf("/api/book/%s/slots?duration=%d", slug, duration)
	if date != "" {
		target += "&date=" + date
	}
	rec := httptest.NewRecorder()
	b.r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	out := pac72SlotsAnswer{code: rec.Code, body: strings.TrimSpace(rec.Body.String())}
	if rec.Code == http.StatusOK {
		var got struct {
			Slots []engine.AvailableSlot `json:"slots"`
			Dates []string               `json:"available_dates"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			b.t.Fatalf("decode slots: %v (%s)", err, rec.Body.String())
		}
		if got.Slots == nil || got.Dates == nil {
			b.t.Fatalf("slots answer lacks a key or has null: %s", rec.Body.String())
		}
		out.slots, out.dates = got.Slots, got.Dates
	}
	return out
}

func (a pac72SlotsAnswer) offers(start time.Time) bool {
	for _, s := range a.slots {
		if s.Start.Equal(start) {
			return true
		}
	}
	return false
}

func (a pac72SlotsAnswer) starts() []string {
	out := []string{}
	for _, s := range a.slots {
		out = append(out, s.Start.UTC().Format(time.RFC3339)+"/"+s.End.UTC().Format(time.RFC3339))
	}
	return out
}

type pac72Written struct {
	rows     int
	inserts  int
	confirms int64
}

func (b *pac72Bench) written() pac72Written {
	b.t.Helper()
	var n int
	if err := b.db.QueryRow(`SELECT count(*) FROM bookings`).Scan(&n); err != nil {
		b.t.Fatal(err)
	}
	return pac72Written{rows: n, inserts: b.cal.totalInserts(), confirms: b.flow.confirms.Load()}
}

// refused asserts a 409 with the single refusal body and nothing written:
// no booking row on any link, no calendar insert on any host's calendar, and
// no ConfirmBooking call (hence no email).
func (b *pac72Bench) refused(what string, code int, body string, before pac72Written) {
	b.t.Helper()
	if code != http.StatusConflict || body != pac72Refusal {
		b.t.Errorf("%s: answer = %d %s, want 409 %s", what, code, body, pac72Refusal)
	}
	if after := b.written(); after != before {
		b.t.Errorf("%s: something was written: before %+v, after %+v", what, before, after)
	}
}

// AC-1: an offered slot books as today; the stored interval is the slot's,
// and the host's calendar receives one event.
func TestPAC72_OfferedSlotBooks(t *testing.T) {
	b := newPAC72Bench(t, pac72At(pac72Mon, "06:00"))
	host, bearer := b.host("pac72-ac1@example.com")
	link := b.link("pac72-ac1", host, nil, host)
	start := pac72At(pac72Tue, "10:00")

	if got := b.slots(link.Slug, "2030-01-08", 30); got.code != http.StatusOK || !got.offers(start) {
		t.Fatalf("listing = %d %v, want 10:00 offered", got.code, got.starts())
	}
	code, body := b.book(link.Slug, start, 30)
	if code != http.StatusCreated {
		t.Fatalf("booking = %d %s, want 201", code, body)
	}
	var conf bookingConfirmationDTO
	if err := json.Unmarshal([]byte(body), &conf); err != nil {
		t.Fatal(err)
	}
	if conf.Start != "2030-01-08T10:00:00Z" || conf.End != "2030-01-08T10:30:00Z" || conf.DurationMinutes != 30 || conf.LinkSlug != link.Slug || len(conf.Hosts) != 1 {
		t.Errorf("confirmation = %+v", conf)
	}
	rows, err := storage.GetBookingsByLink(b.db, link.ID)
	if err != nil || len(rows) != 1 || !rows[0].StartTime.Equal(start) || !rows[0].EndTime.Equal(start.Add(30*time.Minute)) {
		t.Fatalf("booking rows = %v (%v), want one 10:00-10:30", rows, err)
	}
	if n := b.cal.insertsFor(bearer); n != 1 {
		t.Errorf("calendar inserts for the host = %d, want 1", n)
	}
}

// AC-2, AC-2b, AC-3, AC-4, AC-4b, AC-5, AC-6: a busy host refuses the slot,
// one test per busy source.
func TestPAC72_HostBusyRefuses(t *testing.T) {
	start := pac72At(pac72Tue, "10:00")
	cases := []struct {
		name   string
		setup  func(b *pac72Bench, owner uuid.UUID, bearer string, link *storage.SchedulingLink)
		mod    func(*storage.SchedulingLink)
		coBusy bool
	}{
		{name: "AC-2 calendar event", setup: func(b *pac72Bench, _ uuid.UUID, bearer string, _ *storage.SchedulingLink) {
			b.cal.addEvent(bearer, pac72At(pac72Tue, "10:15"), pac72At(pac72Tue, "10:45"))
		}},
		{name: "AC-2b buffer after an event ending at the start", mod: func(l *storage.SchedulingLink) { l.BufferAfter = 15 },
			setup: func(b *pac72Bench, _ uuid.UUID, bearer string, _ *storage.SchedulingLink) {
				b.cal.addEvent(bearer, pac72At(pac72Tue, "09:30"), pac72At(pac72Tue, "10:00"))
			}},
		{name: "AC-3 focus block", setup: func(b *pac72Bench, owner uuid.UUID, _ string, _ *storage.SchedulingLink) {
			if err := storage.SaveFocusBlock(b.db, owner, "pac72-focus", "2030-01-08", pac72At(pac72Tue, "10:00"), pac72At(pac72Tue, "11:00")); err != nil {
				b.t.Fatal(err)
			}
		}},
		{name: "AC-4 confirmed booking on another link the host co-hosts", setup: func(b *pac72Bench, owner uuid.UUID, _ string, _ *storage.SchedulingLink) {
			stranger := createTestUser(b.t, "pac72-ac4-other-owner@example.com")
			other := b.link("pac72-ac4-other", stranger, nil, stranger, owner)
			b.booking(other, pac72At(pac72Tue, "09:45"), pac72At(pac72Tue, "10:15"), "confirmed")
		}},
		{name: "AC-4b pending booking on another link the host hosts", setup: func(b *pac72Bench, owner uuid.UUID, _ string, _ *storage.SchedulingLink) {
			other := b.link("pac72-ac4b-other", owner, nil, owner)
			b.booking(other, pac72At(pac72Tue, "10:00"), pac72At(pac72Tue, "10:30"), "pending")
		}},
		{name: "AC-5 only the co-host is busy", coBusy: true},
		{name: "AC-6 confirmed booking on this link", setup: func(b *pac72Bench, _ uuid.UUID, _ string, link *storage.SchedulingLink) {
			b.booking(link, pac72At(pac72Tue, "10:00"), pac72At(pac72Tue, "10:30"), "confirmed")
		}},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newPAC72Bench(t, pac72At(pac72Mon, "06:00"))
			owner, bearer := b.host(fmt.Sprintf("pac72-busy-%d@example.com", i))
			hosts := []uuid.UUID{owner}
			var coBearer string
			if tc.coBusy {
				var co uuid.UUID
				co, coBearer = b.host(fmt.Sprintf("pac72-busy-co-%d@example.com", i))
				hosts = append(hosts, co)
			}
			link := b.link(fmt.Sprintf("pac72-busy-%d", i), owner, tc.mod, hosts...)
			if tc.setup != nil {
				tc.setup(b, owner, bearer, link)
			}
			if tc.coBusy {
				b.cal.addEvent(coBearer, pac72At(pac72Tue, "10:00"), pac72At(pac72Tue, "10:30"))
			}

			if got := b.slots(link.Slug, "2030-01-08", 30); got.code != http.StatusOK || got.offers(start) {
				t.Errorf("listing = %d %v, want 10:00 not offered", got.code, got.starts())
			}
			before := b.written()
			code, body := b.book(link.Slug, start, 30)
			b.refused(tc.name, code, body, before)
		})
	}
}

// AC-7, AC-8, AC-9, AC-10, AC-11, AC-23, AC-21a: a start outside the link's
// configuration is refused, each case separately; AC-13: every refusal has
// the same body.
func TestPAC72_OutsideTheLinkRefuses(t *testing.T) {
	now := pac72At(pac72Mon, "06:00")
	d59 := pac72Mon.AddDate(0, 0, 59) // Thursday 2030-03-07
	d60 := pac72Mon.AddDate(0, 0, 60) // Friday 2030-03-08
	bodies := map[string]bool{}
	cases := []struct {
		name     string
		start    time.Time
		raw      string
		duration int
		mod      func(*storage.SchedulingLink)
		hostless bool
	}{
		{name: "AC-7 Saturday inside the hours", start: pac72At(pac72Sat, "10:00"), duration: 30},
		{name: "AC-8 before the window opens", start: pac72At(pac72Tue, "08:45"), duration: 30},
		{name: "AC-8 ends after the window closes", start: pac72At(pac72Tue, "16:45"), duration: 30},
		{name: "AC-9 off the 15-minute grid", start: pac72At(pac72Tue, "09:05"), duration: 30},
		{name: "AC-10 beyond the horizon (D+60)", start: pac72At(d60, "10:00"), duration: 30},
		{name: "AC-11 inside the minimum notice", start: pac72At(pac72Mon, "06:45"), duration: 30,
			mod: func(l *storage.SchedulingLink) { l.WindowStart = "06:00"; l.MinNoticeMinutes = 60 }},
		{name: "AC-21a start with seconds", raw: "2030-01-08T10:00:30Z", duration: 30},
		{name: "AC-21a start with a fraction", raw: "2030-01-08T10:00:00.5Z", duration: 30},
		{name: "AC-23 link with no accepted host", start: pac72At(pac72Tue, "10:00"), duration: 30, hostless: true},
		{name: "in the past", start: pac72At(pac72Mon.AddDate(0, 0, -7), "10:00"), duration: 30},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newPAC72Bench(t, now)
			owner, _ := b.host(fmt.Sprintf("pac72-outside-%d@example.com", i))
			hosts := []uuid.UUID{owner}
			if tc.hostless {
				hosts = nil
			}
			link := b.link(fmt.Sprintf("pac72-outside-%d", i), owner, tc.mod, hosts...)
			raw := tc.raw
			if raw == "" {
				raw = tc.start.Format(time.RFC3339)
			}
			before := b.written()
			code, body := b.post(link.Slug, fmt.Sprintf(`{"name":"V","email":"v@example.com","start":%q,"duration":%d}`, raw, tc.duration))
			b.refused(tc.name, code, body, before)
			bodies[body] = true
		})
	}
	if len(bodies) != 1 {
		t.Errorf("AC-13: refusal bodies differ between causes: %v", bodies)
	}

	// The accepting halves of AC-10 and AC-11.
	t.Run("AC-10 D+59 is inside the horizon", func(t *testing.T) {
		b := newPAC72Bench(t, now)
		owner, _ := b.host("pac72-d59@example.com")
		link := b.link("pac72-d59", owner, nil, owner)
		if code, body := b.book(link.Slug, pac72At(d59, "10:00"), 30); code != http.StatusCreated {
			t.Errorf("D+59 booking = %d %s, want 201", code, body)
		}
	})
	t.Run("AC-11 the first start after the notice books", func(t *testing.T) {
		b := newPAC72Bench(t, pac72At(pac72Tue, "10:00"))
		owner, _ := b.host("pac72-notice@example.com")
		link := b.link("pac72-notice", owner, func(l *storage.SchedulingLink) { l.MinNoticeMinutes = 60 }, owner)
		before := b.written()
		code, body := b.book(link.Slug, pac72At(pac72Tue, "10:45"), 30)
		b.refused("10:45 with 60 minutes notice at 10:00", code, body, before)
		if code, body := b.book(link.Slug, pac72At(pac72Tue, "11:00"), 30); code != http.StatusCreated {
			t.Errorf("11:00 = %d %s, want 201", code, body)
		}
	})
}

// AC-12, AC-21b: a length that is not exactly an offered duration is 400 and
// nothing is written; AC-21c: another offset for the same instant books the
// UTC slot.
func TestPAC72_LengthAndOffset(t *testing.T) {
	b := newPAC72Bench(t, pac72At(pac72Mon, "06:00"))
	owner, _ := b.host("pac72-length@example.com")
	link := b.link("pac72-length", owner, nil, owner)
	for _, tc := range []struct {
		name, body, want string
	}{
		{"AC-12 duration 45", `{"name":"V","email":"v@example.com","start":"2030-01-08T10:00:00Z","duration":45}`, `{"error":"invalid duration"}`},
		{"AC-12 end making 45 minutes", `{"name":"V","email":"v@example.com","start":"2030-01-08T10:00:00Z","end":"2030-01-08T10:45:00Z"}`, `{"error":"invalid duration"}`},
		{"AC-21b end with seconds and a duration", `{"name":"V","email":"v@example.com","start":"2030-01-08T10:00:00Z","end":"2030-01-08T10:30:30Z","duration":30}`, `{"error":"duration does not match end time"}`},
		{"AC-21b end with seconds alone", `{"name":"V","email":"v@example.com","start":"2030-01-08T10:00:00Z","end":"2030-01-08T10:30:30Z"}`, `{"error":"invalid duration"}`},
		{"AC-21b end with a fraction alone", `{"name":"V","email":"v@example.com","start":"2030-01-08T10:00:00Z","end":"2030-01-08T10:30:00.25Z"}`, `{"error":"invalid duration"}`},
	} {
		before := b.written()
		code, body := b.post(link.Slug, tc.body)
		if code != http.StatusBadRequest || body != tc.want {
			t.Errorf("%s: answer = %d %s, want 400 %s", tc.name, code, body, tc.want)
		}
		if after := b.written(); after != before {
			t.Errorf("%s: something was written", tc.name)
		}
	}

	code, body := b.post(link.Slug, `{"name":"V","email":"v@example.com","start":"2030-01-08T11:00:00+01:00","end":"2030-01-08T11:30:00+01:00"}`)
	if code != http.StatusCreated {
		t.Fatalf("AC-21c: answer = %d %s, want 201", code, body)
	}
	rows, err := storage.GetBookingsByLink(b.db, link.ID)
	if err != nil || len(rows) != 1 || !rows[0].StartTime.Equal(pac72At(pac72Tue, "10:00")) || !rows[0].EndTime.Equal(pac72At(pac72Tue, "10:30")) {
		t.Fatalf("AC-21c: rows = %v (%v), want one 10:00-10:30 UTC", rows, err)
	}
	var conf bookingConfirmationDTO
	_ = json.Unmarshal([]byte(body), &conf)
	if conf.Start != "2030-01-08T10:00:00Z" || conf.End != "2030-01-08T10:30:00Z" {
		t.Errorf("AC-21c: confirmation %s..%s, want the UTC slot", conf.Start, conf.End)
	}
}

// AC-15, AC-16, AC-23: link days, the horizon and a hostless link in the
// listing.
func TestPAC72_ListingDaysAndHorizon(t *testing.T) {
	b := newPAC72Bench(t, pac72At(pac72Mon, "06:00"))
	owner, _ := b.host("pac72-listing@example.com")
	link := b.link("pac72-listing", owner, nil, owner)

	all := b.slots(link.Slug, "", 30)
	if all.code != http.StatusOK || len(all.dates) == 0 {
		t.Fatalf("date-less listing = %d %s", all.code, all.body)
	}
	last := pac72Mon.AddDate(0, 0, 59).Format(time.DateOnly)
	if !sort.StringsAreSorted(all.dates) || all.dates[0] != "2030-01-07" || all.dates[len(all.dates)-1] != last {
		t.Errorf("AC-16: dates run %s..%s, want 2030-01-07..%s ascending", all.dates[0], all.dates[len(all.dates)-1], last)
	}
	for _, d := range all.dates {
		day, _ := time.Parse(time.DateOnly, d)
		if wd := day.Weekday(); wd == time.Saturday || wd == time.Sunday {
			t.Errorf("AC-15: date-less listing offers %s, a %s", d, wd)
		}
	}
	// 60 days from a Monday: 8 full weeks and Monday-Thursday = 44 link days.
	if len(all.dates) != 44 {
		t.Errorf("date-less listing has %d dates, want 44", len(all.dates))
	}

	for _, tc := range []struct {
		name, date string
		want       int
	}{
		{"AC-15 explicit Saturday", "2030-01-12", 0},
		{"AC-16 D+59", last, 31},
		{"AC-16 D+60", pac72Mon.AddDate(0, 0, 60).Format(time.DateOnly), 0},
		{"AC-16 a year ahead", "2031-01-07", 0},
		{"explicit date before today", "2030-01-01", 0},
	} {
		got := b.slots(link.Slug, tc.date, 30)
		if got.code != http.StatusOK || len(got.slots) != tc.want || len(got.dates) != 0 {
			t.Errorf("%s: %d, %d slots, %d dates; want 200 with %d slots", tc.name, got.code, len(got.slots), len(got.dates), tc.want)
		}
	}

	hostless := b.link("pac72-listing-hostless", owner, nil)
	if got := b.slots(hostless.Slug, "", 30); got.code != http.StatusOK || len(got.dates) != 0 {
		t.Errorf("AC-23: hostless date-less listing = %d %v, want no date", got.code, got.dates)
	}
	if got := b.slots(hostless.Slug, "2030-01-08", 30); got.code != http.StatusOK || len(got.slots) != 0 {
		t.Errorf("AC-23: hostless explicit listing = %d %v, want no slot", got.code, got.starts())
	}
}

// AC-17a-c: busy time that cannot be gathered from storage is a server error
// for the booking (nothing written) and the explicit-date listing, and drops
// the day from the date-less listing; never a 409. AC-17d: an unreadable
// external calendar keeps today's tolerance.
func TestPAC72_UnreadableBusyTime(t *testing.T) {
	boom := errors.New("injected storage failure")
	target := pac72Tue
	start := pac72At(target, "10:00")
	isTarget := func(t time.Time) bool { return t.UTC().Format(time.DateOnly) == target.Format(time.DateOnly) }
	for _, tc := range []struct {
		name  string
		reads func(host uuid.UUID) engine.BookingReads
	}{
		{"AC-17a accepted hosts", func(uuid.UUID) engine.BookingReads {
			return engine.BookingReads{AcceptedHosts: func(db *sql.DB, linkID uuid.UUID) ([]*storage.LinkHost, error) {
				return nil, boom
			}}
		}},
		{"AC-17b focus blocks of host A", func(host uuid.UUID) engine.BookingReads {
			return engine.BookingReads{FocusBlocks: func(db *sql.DB, user uuid.UUID, weekStart time.Time) ([]storage.FocusBlock, error) {
				if user == host && isTarget(weekStart) {
					return nil, boom
				}
				return storage.ListFocusBlocksForUserWeek(db, user, weekStart)
			}}
		}},
		{"AC-17c bookings on host A's links", func(host uuid.UUID) engine.BookingReads {
			return engine.BookingReads{HostBookings: func(db *sql.DB, user uuid.UUID, s, e time.Time) ([]time.Time, error) {
				if user == host && isTarget(s) {
					return nil, boom
				}
				return storage.GetBusyBookingsForUser(db, user, s, e)
			}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newPAC72Bench(t, pac72At(pac72Mon, "06:00"))
			slug := "pac72-17-" + strings.ToLower(strings.Fields(tc.name)[0])
			owner, _ := b.host(slug + "@example.com")
			coHost, _ := b.host(slug + "-b@example.com")
			link := b.link(slug, owner, nil, owner, coHost)
			b.flow.Reads = tc.reads(owner)

			before := b.written()
			code, body := b.book(link.Slug, start, 30)
			if code != http.StatusInternalServerError || body != `{"error":"booking conflict check failed"}` {
				t.Errorf("booking = %d %s, want 500 booking conflict check failed", code, body)
			}
			if after := b.written(); after != before {
				t.Errorf("something was written: %+v -> %+v", before, after)
			}
			if got := b.slots(link.Slug, "2030-01-08", 30); got.code != http.StatusInternalServerError || got.body != `{"error":"internal error"}` {
				t.Errorf("explicit-date listing = %d %s, want 500 internal error", got.code, got.body)
			}
			all := b.slots(link.Slug, "", 30)
			if all.code != http.StatusOK {
				t.Fatalf("date-less listing = %d %s", all.code, all.body)
			}
			for _, d := range all.dates {
				if d == "2030-01-08" {
					t.Errorf("date-less listing offers the day whose busy time could not be read")
				}
			}
			if strings.HasSuffix(tc.name, "hosts") && len(all.dates) != 0 {
				t.Errorf("date-less listing = %v, want no date when no host list can be read", all.dates)
			}
			if !strings.HasSuffix(tc.name, "hosts") && len(all.dates) != 43 {
				t.Errorf("date-less listing has %d dates, want the other 43 link days", len(all.dates))
			}
		})
	}

	t.Run("AC-17d external calendar", func(t *testing.T) {
		b := newPAC72Bench(t, pac72At(pac72Mon, "06:00"))
		owner, bearer := b.host("pac72-17d@example.com")
		unconnected := createTestUser(t, "pac72-17d-unconnected@example.com")
		link := b.link("pac72-17d", owner, nil, owner, unconnected)
		b.cal.setFailing(bearer)
		if got := b.slots(link.Slug, "2030-01-08", 30); got.code != http.StatusOK || !got.offers(start) {
			t.Errorf("listing = %d %v, want 10:00 offered", got.code, got.starts())
		}
		if code, body := b.book(link.Slug, start, 30); code != http.StatusCreated {
			t.Errorf("booking = %d %s, want 201", code, body)
		}
	})
}

// AC-18: the 409 check comes after 429, 404, 410 and 400.
func TestPAC72_EarlierRefusalsKeepTheirStatus(t *testing.T) {
	b := newPAC72Bench(t, pac72At(pac72Mon, "06:00"))
	owner, _ := b.host("pac72-ac18@example.com")
	link := b.link("pac72-ac18", owner, nil, owner)
	used := b.link("pac72-ac18-used", owner, func(l *storage.SchedulingLink) { l.UsageType = "single_use" }, owner)
	b.booking(used, pac72At(pac72Mon.AddDate(0, 0, -7), "10:00"), pac72At(pac72Mon.AddDate(0, 0, -7), "10:30"), "confirmed")
	saturday := pac72At(pac72Sat, "10:00").Format(time.RFC3339)
	valid := fmt.Sprintf(`{"name":"V","email":"v@example.com","start":%q,"duration":30}`, saturday)

	before := b.written()
	for _, tc := range []struct {
		name, slug, body string
		want             int
	}{
		{"404 unknown slug", "pac72-no-such-link", valid, http.StatusNotFound},
		{"410 exhausted link", used.Slug, valid, http.StatusGone},
		{"400 malformed body", link.Slug, `{"name":"","email":"","start":"` + saturday + `"}`, http.StatusBadRequest},
		{"400 unoffered duration", link.Slug, strings.Replace(valid, `"duration":30`, `"duration":45`, 1), http.StatusBadRequest},
	} {
		if code, body := b.post(tc.slug, tc.body); code != tc.want {
			t.Errorf("%s: %d %s, want %d", tc.name, code, body, tc.want)
		}
	}
	bookingCreateLimiter.mu.Lock()
	bookingCreateLimiter.limit = 0
	bookingCreateLimiter.mu.Unlock()
	if code, body := b.post(link.Slug, valid); code != http.StatusTooManyRequests {
		t.Errorf("429: %d %s, want 429", code, body)
	}
	if after := b.written(); after != before {
		t.Errorf("something was written: %+v -> %+v", before, after)
	}
}

// AC-14: for a fixed clock and calendar state, every slot the listing returns
// books, and every 15-minute-aligned start it does not return (plus one date
// past the horizon and one off-grid start per day) is refused, with nothing
// written.
func TestPAC72_ListingAndBookingAreOneRule(t *testing.T) {
	now := pac72At(pac72Mon, "09:20")
	b := newPAC72Bench(t, now)
	hostA, bearerA := b.host("pac72-ac14-a@example.com")
	hostB, _ := b.host("pac72-ac14-b@example.com")
	link := b.link("pac72-ac14", hostA, func(l *storage.SchedulingLink) {
		l.DaysOfWeek = []int{1, 3} // Monday, Wednesday
		l.WindowStart, l.WindowEnd = "09:00", "11:00"
		l.BufferAfter = 15
		l.MinNoticeMinutes = 30
	}, hostA, hostB)
	wed := pac72Mon.AddDate(0, 0, 2)
	b.cal.addEvent(bearerA, pac72At(wed, "09:30"), pac72At(wed, "10:00")) // + 15 min buffer
	b.cal.addEvent(bearerA, pac72At(pac72Mon.AddDate(0, 0, 7), "10:15"), pac72At(pac72Mon.AddDate(0, 0, 7), "10:30"))
	if err := storage.SaveFocusBlock(b.db, hostB, "pac72-ac14-focus", pac72Mon.AddDate(0, 0, 9).Format(time.DateOnly),
		pac72At(pac72Mon.AddDate(0, 0, 9), "09:00"), pac72At(pac72Mon.AddDate(0, 0, 9), "09:45")); err != nil {
		t.Fatal(err)
	}
	stranger := createTestUser(t, "pac72-ac14-stranger@example.com")
	other := b.link("pac72-ac14-other", stranger, nil, stranger, hostB)
	b.booking(other, pac72At(pac72Mon.AddDate(0, 0, 14), "10:00"), pac72At(pac72Mon.AddDate(0, 0, 14), "11:00"), "confirmed")

	offeredTotal, refusedTotal := 0, 0
	for i := 0; i <= 60; i++ {
		day := pac72Mon.AddDate(0, 0, i)
		date := day.Format(time.DateOnly)
		for _, dur := range []int{30, 60} {
			list := b.slots(link.Slug, date, dur)
			if list.code != http.StatusOK {
				t.Fatalf("%s/%d: listing %d %s", date, dur, list.code, list.body)
			}
			offered := map[time.Time]bool{}
			for _, s := range list.slots {
				offered[s.Start] = true
				if !s.End.Equal(s.Start.Add(time.Duration(dur) * time.Minute)) {
					t.Errorf("%s: slot %v..%v is not %d minutes", date, s.Start, s.End, dur)
				}
				// Book it in a fresh copy of the state: the booking is removed
				// again so it cannot affect the next start.
				code, body := b.book(link.Slug, s.Start, dur)
				if code != http.StatusCreated {
					t.Errorf("%s %d min: offered %s refused %d %s", date, dur, s.Start.Format("15:04"), code, body)
					continue
				}
				var conf bookingConfirmationDTO
				_ = json.Unmarshal([]byte(body), &conf)
				if _, err := b.db.Exec(`DELETE FROM bookings WHERE id = $1`, conf.ID); err != nil {
					t.Fatal(err)
				}
				offeredTotal++
			}
			if i == 60 && len(list.slots) != 0 {
				t.Errorf("D+60 lists %d slots", len(list.slots))
			}
			before := b.written()
			for m := 0; m < 24*60; m += 15 {
				s := day.Add(time.Duration(m) * time.Minute)
				if offered[s] {
					continue
				}
				code, body := b.book(link.Slug, s, dur)
				if code != http.StatusConflict || body != pac72Refusal {
					t.Errorf("%s %d min: unoffered %s answered %d %s, want 409", date, dur, s.Format("15:04"), code, body)
				}
				refusedTotal++
			}
			code, body := b.book(link.Slug, day.Add(9*time.Hour+7*time.Minute), dur)
			if code != http.StatusConflict || body != pac72Refusal {
				t.Errorf("%s %d min: off-grid 09:07 answered %d %s, want 409", date, dur, code, body)
			}
			if after := b.written(); after != before {
				t.Fatalf("%s %d min: a refused start wrote something: %+v -> %+v", date, dur, before, after)
			}
		}
	}
	// The calendar state removed some slots and the notice others; both
	// halves of the equivalence were exercised.
	if offeredTotal < 100 || refusedTotal < 10000 {
		t.Fatalf("offered %d, refused %d: the scenario exercised too little", offeredTotal, refusedTotal)
	}
	for _, d := range []struct {
		day   time.Time
		start string
	}{{pac72Mon, "09:30"}, {pac72Mon, "09:45"}, {wed, "09:45"}, {wed, "10:00"}, {pac72Mon.AddDate(0, 0, 7), "10:15"}, {pac72Mon.AddDate(0, 0, 9), "09:30"}, {pac72Mon.AddDate(0, 0, 14), "10:00"}} {
		if l := b.slots(link.Slug, d.day.Format(time.DateOnly), 30); l.offers(pac72At(d.day, d.start)) {
			t.Errorf("%s %s is offered, but the state makes it busy (or inside the notice)", d.day.Format(time.DateOnly), d.start)
		}
	}
	if code, body := b.book(link.Slug, pac72At(pac72Mon, "10:00"), 30); code != http.StatusCreated {
		t.Errorf("today 10:00 (the first start after the 30-minute notice) = %d %s, want 201", code, body)
	}
}

// AC-22: with the process in a zone other than UTC, the date-less listing, the
// explicit-date listing and the booking answer exactly as in UTC.
func TestPAC72_ProcessZoneDoesNotMatter(t *testing.T) {
	now := pac72At(pac72Mon, "20:00") // Tuesday 06:00 at UTC+10
	run := func(tag string) string {
		b := newPAC72Bench(t, now)
		owner, _ := b.host("pac72-zone-" + tag + "@example.com")
		link := b.link("pac72-zone-"+tag, owner, nil, owner)
		var out []string
		all := b.slots(link.Slug, "", 30)
		out = append(out, fmt.Sprintf("dates %d %v", all.code, all.dates))
		for _, date := range []string{"2030-01-07", "2030-01-08", "2030-01-12"} {
			l := b.slots(link.Slug, date, 30)
			out = append(out, fmt.Sprintf("%s %d %v", date, l.code, l.starts()))
		}
		for _, s := range []string{"2030-01-07T20:30:00Z", "2030-01-07T23:00:00Z", "2030-01-08T09:00:00Z", "2030-01-08T09:00:00+10:00", "2030-01-12T10:00:00Z"} {
			code, _ := b.post(link.Slug, fmt.Sprintf(`{"name":"V","email":"v@example.com","start":%q,"duration":30}`, s))
			out = append(out, fmt.Sprintf("%s %d", s, code))
		}
		return strings.Join(out, "\n")
	}
	inUTC := run("utc")
	prev := time.Local
	time.Local = time.FixedZone("UTC+10", 10*60*60)
	defer func() { time.Local = prev }()
	inPlus10 := run("plus10")
	if inUTC != inPlus10 {
		t.Fatalf("answers differ by process zone.\nUTC:\n%s\nUTC+10:\n%s", inUTC, inPlus10)
	}
	if !strings.Contains(inUTC, "2030-01-08T09:00:00Z 201") || !strings.Contains(inUTC, "2030-01-12T10:00:00Z 409") || !strings.Contains(inUTC, "2030-01-07T23:00:00Z 409") {
		t.Fatalf("unexpected UTC answers:\n%s", inUTC)
	}
}
