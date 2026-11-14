package api

// PAC-82 (docs/specs/PAC-82.md, contracts/features/PAC-82.md): concurrent
// public bookings are decided again at the save, and an optional
// Idempotency-Key makes a retry safe. These tests run the real handler, the
// real engine and real PostgreSQL; requests are held at seams (a barrier after
// every pre-save check, afterBookingLinkRead, afterBookingReplayLookup and the
// storage seams) and released together, never ordered by sleeping alone.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// pac82Flow offers every grid slot of the link (no busy time: the save's own
// re-check is what is under test) and books through the real engine. When a
// barrier is armed, ConfirmBooking waits at it: every pre-save check has then
// passed, which is where the contract's afterBookingCommit releases requests.
type pac82Flow struct {
	eng *engine.BookingEngine
	mu  sync.Mutex
	bar *pac82Barrier
}

type pac82Barrier struct {
	n       int
	arrived atomic.Int32
	all     chan struct{}
	release chan struct{}
}

func (f *pac82Flow) arm(n int) *pac82Barrier {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bar = &pac82Barrier{n: n, all: make(chan struct{}), release: make(chan struct{})}
	return f.bar
}

func (f *pac82Flow) CollectiveSlots(_ context.Context, link *storage.SchedulingLink, date time.Time, d int) ([]engine.AvailableSlot, error) {
	return engine.GridSlots(link, date, d), nil
}

func (f *pac82Flow) CollectiveSlotsDays(_ context.Context, link *storage.SchedulingLink, days []time.Time, d int) ([][]engine.AvailableSlot, error) {
	out := make([][]engine.AvailableSlot, len(days))
	for i, day := range days {
		out[i] = engine.GridSlots(link, day, d)
	}
	return out, nil
}

func (f *pac82Flow) ConfirmBooking(ctx context.Context, link *storage.SchedulingLink, name, email string, start, end time.Time, notes, key string) (*storage.Booking, error) {
	f.mu.Lock()
	b := f.bar
	f.mu.Unlock()
	if b != nil {
		if int(b.arrived.Add(1)) == b.n {
			close(b.all)
		}
		<-b.release
	}
	return f.eng.ConfirmBooking(ctx, link, name, email, start, end, notes, key)
}

type pac82Env struct {
	*pac101Env
	flow *pac82Flow
	sfx  string
}

func newPAC82Env(t *testing.T) *pac82Env {
	t.Helper()
	e := newPAC101Env(t)
	flow := &pac82Flow{eng: &engine.BookingEngine{DB: e.db, OAuthConfig: &oauth2.Config{}}}
	bh := newBookingHandlersWithEngine(e.db, flow)
	e.r.Post("/api/book/{slug}/pac82", bh.createBooking)
	return &pac82Env{pac101Env: e, flow: flow, sfx: uuid.NewString()[:8]}
}

// day is the test's booking day (a weekday at least 72 h ahead), at h:m UTC.
func pac82At(h, m int) time.Time {
	d := futureBookingStart()
	return time.Date(d.Year(), d.Month(), d.Day(), h, m, 0, 0, time.UTC)
}

type pac82Req struct {
	name, email, notes string
	start              time.Time
	minutes            int
	key                string
	keys               []string // several field lines
}

func (e *pac82Env) post(slug string, q pac82Req) *httptest.ResponseRecorder {
	if q.name == "" {
		q.name = "Booker"
	}
	if q.minutes == 0 {
		q.minutes = 30
	}
	body, _ := json.Marshal(map[string]any{
		"name": q.name, "email": q.email, "notes": q.notes,
		"start": q.start.Format(time.RFC3339), "end": q.start.Add(time.Duration(q.minutes) * time.Minute).Format(time.RFC3339),
		"duration": q.minutes,
	})
	return e.raw(slug, string(body), q)
}

func (e *pac82Env) raw(slug, body string, q pac82Req) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/book/"+slug+"/pac82", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if q.key != "" {
		req.Header.Set("Idempotency-Key", q.key)
	}
	for _, k := range q.keys {
		req.Header.Add("Idempotency-Key", k)
	}
	n := e.addr.Add(1)
	req.RemoteAddr = fmt.Sprintf("10.82.%d.%d:4000", n/250, n%250+1)
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w
}

func (e *pac82Env) async(slug string, q pac82Req) <-chan *httptest.ResponseRecorder {
	out := make(chan *httptest.ResponseRecorder, 1)
	go func() { out <- e.post(slug, q) }()
	return out
}

func (e *pac82Env) link(owner uuid.UUID, usage string, maxUses *int, durations []int, specific bool) *storage.SchedulingLink {
	e.t.Helper()
	refusal := storage.RefusalGeneric
	if specific {
		refusal = storage.RefusalSpecific
	}
	l, err := storage.CreateSchedulingLink(e.db, &storage.SchedulingLink{
		OwnerUserID: owner, Slug: "pac82-" + uuid.NewString()[:10], Title: "PAC-82", DurationOptions: durations,
		DaysOfWeek: []int{1, 2, 3, 4, 5}, WindowStart: "09:00", WindowEnd: "17:00", UsageType: usage, MaxUses: maxUses,
		Active: true, RefusalMessage: refusal,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := storage.AddLinkHost(e.db, l.ID, owner, "accepted"); err != nil {
		e.t.Fatal(err)
	}
	return l
}

func (e *pac82Env) host(label string) uuid.UUID {
	return e.user("pac82-" + label + "-" + e.sfx + "@example.com")
}

func (e *pac82Env) settle() {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e.group.Wait(ctx)
}

func pac82Key(label string) string { return "pac82-" + label + "-" + uuid.NewString() }

type pac82Body struct {
	ID              string `json:"id"`
	Start           string `json:"start"`
	End             string `json:"end"`
	DurationMinutes int    `json:"duration_minutes"`
	BookerName      string `json:"booker_name"`
	BookerEmail     string `json:"booker_email"`
	Notes           string `json:"notes"`
	Error           string `json:"error"`
	Reason          string `json:"reason"`
}

func pac82Decode(t *testing.T, w *httptest.ResponseRecorder) pac82Body {
	t.Helper()
	var b pac82Body
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode %d %q: %v", w.Code, w.Body.String(), err)
	}
	return b
}

const pac82Reused = `{"error":"idempotency key already used"}`

func pac82IsReused(w *httptest.ResponseRecorder) bool {
	return w.Code == http.StatusConflict && strings.TrimSpace(w.Body.String()) == pac82Reused
}

// release sends the requests, waits until all of them are at the barrier
// (every pre-save check passed), then releases them together.
func (e *pac82Env) release(slugs []string, reqs []pac82Req) []*httptest.ResponseRecorder {
	e.t.Helper()
	bar := e.flow.arm(len(reqs))
	defer func() { e.flow.mu.Lock(); e.flow.bar = nil; e.flow.mu.Unlock() }()
	chans := make([]<-chan *httptest.ResponseRecorder, len(reqs))
	for i := range reqs {
		chans[i] = e.async(slugs[i], reqs[i])
	}
	select {
	case <-bar.all:
	case <-time.After(15 * time.Second):
		e.t.Fatalf("only %d of %d requests passed their pre-save checks", bar.arrived.Load(), len(reqs))
	}
	close(bar.release)
	out := make([]*httptest.ResponseRecorder, len(reqs))
	for i, ch := range chans {
		select {
		case out[i] = <-ch:
		case <-time.After(30 * time.Second):
			e.t.Fatalf("request %d did not answer", i)
		}
	}
	return out
}

func pac82Count(ws []*httptest.ResponseRecorder) map[int]int {
	m := map[int]int{}
	for _, w := range ws {
		m[w.Code]++
	}
	return m
}

// AC-1: two requests for one slot released together past their pre-save
// checks: one 201, one 409 (taken, or the generic body), one row, one
// completion unit (one host event).
func TestPAC82_ConcurrentSameSlotOneWins(t *testing.T) {
	for _, specific := range []bool{true, false} {
		t.Run(fmt.Sprintf("specific=%v", specific), func(t *testing.T) {
			e := newPAC82Env(t)
			owner := e.host(fmt.Sprintf("ac1-%v", specific))
			l := e.link(owner, "reusable", nil, []int{30}, specific)
			s := pac82At(10, 0)
			ws := e.release([]string{l.Slug, l.Slug}, []pac82Req{{email: "a@ac1.test", start: s}, {email: "b@ac1.test", start: s}})
			e.settle()
			var loser *httptest.ResponseRecorder
			for _, w := range ws {
				if w.Code != http.StatusCreated {
					loser = w
				}
			}
			if c := pac82Count(ws); c[http.StatusCreated] != 1 || c[http.StatusConflict] != 1 {
				t.Fatalf("statuses %v, want one 201 and one 409", c)
			}
			want := `{"error":"slot is no longer available"}`
			if specific {
				want = `{"error":"slot is already booked","reason":"taken"}`
			}
			if got := strings.TrimSpace(loser.Body.String()); got != want {
				t.Errorf("loser body %s, want %s", got, want)
			}
			if n := e.bookingCount(l.ID); n != 1 {
				t.Errorf("%d rows, want 1", n)
			}
			if c := e.creates.Load(); c != 1 {
				t.Errorf("%d host events, want 1 (one completion unit)", c)
			}
		})
	}
}

// AC-2: two links sharing host H, overlapping (not equal) slots, one request
// each, released together: one 201, one 409 host_busy, one row. H is owner
// of one and accepted co-host of the other; and, separately, owner of both.
func TestPAC82_ConcurrentCrossLinkOneWins(t *testing.T) {
	for _, shape := range []string{"co-host", "owner of both"} {
		t.Run(shape, func(t *testing.T) {
			e := newPAC82Env(t)
			h := e.host("ac2-h-" + strings.ReplaceAll(shape, " ", ""))
			l1 := e.link(h, "reusable", nil, []int{30}, true)
			owner2 := h
			if shape == "co-host" {
				owner2 = e.host("ac2-o")
			}
			l2 := e.link(owner2, "reusable", nil, []int{30}, true)
			if owner2 != h {
				if _, err := storage.AddLinkHost(e.db, l2.ID, h, "accepted"); err != nil {
					t.Fatal(err)
				}
			}
			ws := e.release([]string{l1.Slug, l2.Slug}, []pac82Req{{email: "a@ac2.test", start: pac82At(10, 0)}, {email: "b@ac2.test", start: pac82At(10, 15)}})
			if c := pac82Count(ws); c[http.StatusCreated] != 1 || c[http.StatusConflict] != 1 {
				t.Fatalf("statuses %v", c)
			}
			for _, w := range ws {
				if w.Code == http.StatusConflict && strings.TrimSpace(w.Body.String()) != `{"error":"a host is busy at that time","reason":"host_busy"}` {
					t.Errorf("loser body %s, want host_busy", w.Body.String())
				}
			}
			if n := e.bookingCount(l1.ID) + e.bookingCount(l2.ID); n != 1 {
				t.Errorf("%d rows, want 1", n)
			}
			e.settle()
		})
	}
}

// AC-3: twenty requests for one slot: one 201, nineteen 409, no 500.
func TestPAC82_TwentyConcurrentRequestsOneWins(t *testing.T) {
	e := newPAC82Env(t)
	l := e.link(e.host("ac3"), "reusable", nil, []int{30}, true)
	var slugs []string
	var reqs []pac82Req
	for i := 0; i < 20; i++ {
		slugs = append(slugs, l.Slug)
		reqs = append(reqs, pac82Req{email: fmt.Sprintf("r%d@ac3.test", i), start: pac82At(11, 0)})
	}
	ws := e.release(slugs, reqs)
	e.settle()
	if c := pac82Count(ws); c[http.StatusCreated] != 1 || c[http.StatusConflict] != 19 {
		t.Fatalf("statuses %v, want 1 x 201 and 19 x 409", c)
	}
	if n := e.bookingCount(l.ID); n != 1 {
		t.Fatalf("%d rows", n)
	}
}

// AC-7: use limits under concurrency, two non-overlapping requests released
// together.
func TestPAC82_ConcurrentUseLimit(t *testing.T) {
	three := 3
	for _, c := range []struct {
		name   string
		usage  string
		max    *int
		stored int
		want   map[int]int
	}{
		{"single use", "single_use", nil, 0, map[int]int{201: 1, 410: 1}},
		{"max 3 with 2 stored", "recurring", &three, 2, map[int]int{201: 1, 410: 1}},
		{"max 3 with 1 stored", "recurring", &three, 1, map[int]int{201: 2}},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newPAC82Env(t)
			l := e.link(e.host("ac7-"+strings.ReplaceAll(c.name, " ", "")), c.usage, c.max, []int{30}, false)
			for i := 0; i < c.stored; i++ {
				s := pac82At(9, 0).AddDate(0, 0, 1+i)
				if _, err := storage.CreateBooking(e.db, &storage.Booking{LinkID: l.ID, BookerName: "P", BookerEmail: "p@ac7.test", StartTime: s, EndTime: s.Add(30 * time.Minute)}); err != nil {
					t.Fatal(err)
				}
			}
			ws := e.release([]string{l.Slug, l.Slug}, []pac82Req{{email: "a@ac7.test", start: pac82At(10, 0)}, {email: "b@ac7.test", start: pac82At(14, 0)}})
			e.settle()
			if got := pac82Count(ws); fmt.Sprint(got) != fmt.Sprint(c.want) {
				t.Fatalf("statuses %v, want %v", got, c.want)
			}
			for _, w := range ws {
				if w.Code == http.StatusGone && strings.TrimSpace(w.Body.String()) != `{"error":"link is no longer accepting bookings"}` {
					t.Errorf("410 body %s", w.Body.String())
				}
			}
			if n, want := e.bookingCount(l.ID), c.stored+c.want[201]; n != want {
				t.Fatalf("%d rows, want %d", n, want)
			}
		})
	}
}

// AC-9: a keyed booking replayed within 24 h: 201 with the same booking, no
// new row, no host event.
func TestPAC82_ReplayIsTheOriginal201(t *testing.T) {
	e := newPAC82Env(t)
	l := e.link(e.host("ac9"), "reusable", nil, []int{30}, true)
	q := pac82Req{name: " Ada ", email: " ada@ac9.test ", notes: "  hello ", start: pac82At(10, 0), key: pac82Key("ac9")}
	first := e.post(l.Slug, q)
	e.mustStatus(first, http.StatusCreated, "first")
	e.settle()
	rows, creates := e.bookingCount(l.ID), e.creates.Load()
	again := e.post(l.Slug, q)
	e.mustStatus(again, http.StatusCreated, "replay")
	a, b := pac82Decode(t, first), pac82Decode(t, again)
	if a != b || a.BookerName != "Ada" || a.Notes != "  hello " {
		t.Fatalf("replay %+v, original %+v", b, a)
	}
	e.settle()
	if e.bookingCount(l.ID) != rows || e.creates.Load() != creates {
		t.Fatalf("replay wrote: rows %d -> %d, events %d -> %d", rows, e.bookingCount(l.ID), creates, e.creates.Load())
	}
}

// AC-10 (extended by contract C-4): the replay is decided before the 410, the
// notice, the availability, the overlap and the duration list.
func TestPAC82_ReplayBeforeLinkStateChecks(t *testing.T) {
	cases := []struct {
		name   string
		usage  string
		mutate func(e *pac82Env, l *storage.SchedulingLink, owner uuid.UUID)
	}{
		{"single use, now used up", "single_use", func(*pac82Env, *storage.SchedulingLink, uuid.UUID) {}},
		{"start now inside the notice", "reusable", func(e *pac82Env, l *storage.SchedulingLink, _ uuid.UUID) {
			if _, err := e.db.Exec(`UPDATE scheduling_links SET min_notice_minutes = 100000 WHERE id = $1`, l.ID); err != nil {
				e.t.Fatal(err)
			}
		}},
		{"slot now busy for a host", "reusable", func(e *pac82Env, _ *storage.SchedulingLink, owner uuid.UUID) {
			other := e.link(owner, "reusable", nil, []int{30}, false)
			s := pac82At(10, 0)
			if _, err := storage.CreateBooking(e.db, &storage.Booking{LinkID: other.ID, BookerName: "X", BookerEmail: "x@ac10.test", StartTime: s, EndTime: s.Add(30 * time.Minute)}); err != nil {
				e.t.Fatal(err)
			}
		}},
		{"duration removed from the link", "reusable", func(e *pac82Env, l *storage.SchedulingLink, _ uuid.UUID) {
			if _, err := e.db.Exec(`UPDATE scheduling_links SET duration_options = '{60}' WHERE id = $1`, l.ID); err != nil {
				e.t.Fatal(err)
			}
		}},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newPAC82Env(t)
			owner := e.host(fmt.Sprintf("ac10-%d", i))
			l := e.link(owner, c.usage, nil, []int{30}, true)
			q := pac82Req{email: "k@ac10.test", start: pac82At(10, 0), key: pac82Key("ac10")}
			first := e.post(l.Slug, q)
			e.mustStatus(first, http.StatusCreated, "first")
			c.mutate(e, l, owner)
			again := e.post(l.Slug, q)
			e.mustStatus(again, http.StatusCreated, "replay")
			if pac82Decode(t, first).ID != pac82Decode(t, again).ID {
				t.Fatal("replay answered another booking")
			}
			e.settle()
		})
	}
}

// AC-11: one differing field, with each refusal setting: the key-reused 409,
// nothing written, none of the stored booking in the body.
func TestPAC82_SameKeyOtherDetailsIsKeyReused(t *testing.T) {
	for _, specific := range []bool{true, false} {
		t.Run(fmt.Sprintf("specific=%v", specific), func(t *testing.T) {
			e := newPAC82Env(t)
			l := e.link(e.host(fmt.Sprintf("ac11-%v", specific)), "reusable", nil, []int{30, 60}, specific)
			base := pac82Req{name: "Ada", email: "ada@ac11.test", notes: "n", start: pac82At(10, 0), key: pac82Key("ac11")}
			e.mustStatus(e.post(l.Slug, base), http.StatusCreated, "original")
			rows := e.bookingCount(l.ID)
			variants := map[string]pac82Req{}
			v := base
			v.name = "Bob"
			variants["name"] = v
			v = base
			v.email = "bob@ac11.test"
			variants["email"] = v
			v = base
			v.start = pac82At(13, 0)
			variants["start"] = v
			v = base
			v.minutes = 60
			variants["end"] = v
			v = base
			v.notes = "other"
			variants["notes"] = v
			for field, q := range variants {
				w := e.post(l.Slug, q)
				if !pac82IsReused(w) {
					t.Errorf("%s differs: %d %s, want the key-reused 409", field, w.Code, w.Body.String())
				}
			}
			if e.bookingCount(l.ID) != rows {
				t.Fatal("a key-reused request wrote a row")
			}
			e.settle()
		})
	}
}

// AC-12: a key 25 hours old, same or different details: the key-reused 409.
func TestPAC82_ExpiredKeyIsKeyReused(t *testing.T) {
	e := newPAC82Env(t)
	l := e.link(e.host("ac12"), "reusable", nil, []int{30}, true)
	q := pac82Req{email: "k@ac12.test", start: pac82At(10, 0), key: pac82Key("ac12")}
	first := e.post(l.Slug, q)
	e.mustStatus(first, http.StatusCreated, "first")
	if _, err := e.db.Exec(`UPDATE bookings SET created_at = now() - interval '25 hours' WHERE id = $1`, pac82Decode(t, first).ID); err != nil {
		t.Fatal(err)
	}
	other := q
	other.start = pac82At(15, 0)
	for _, r := range []pac82Req{q, other} {
		if w := e.post(l.Slug, r); !pac82IsReused(w) {
			t.Errorf("expired key: %d %s", w.Code, w.Body.String())
		}
	}
	if e.bookingCount(l.ID) != 1 {
		t.Fatal("an expired key wrote a row")
	}
	e.settle()
}

// AC-13: a key is scoped to its link.
func TestPAC82_KeyIsScopedToItsLink(t *testing.T) {
	e := newPAC82Env(t)
	l1 := e.link(e.host("ac13-1"), "reusable", nil, []int{30}, true)
	l2 := e.link(e.host("ac13-2"), "reusable", nil, []int{30}, true)
	q := pac82Req{email: "k@ac13.test", start: pac82At(10, 0), key: pac82Key("ac13")}
	a := e.post(l1.Slug, q)
	b := e.post(l2.Slug, q)
	e.mustStatus(a, http.StatusCreated, "L1")
	e.mustStatus(b, http.StatusCreated, "L2")
	if pac82Decode(t, a).ID == pac82Decode(t, b).ID {
		t.Fatal("L2 replayed L1's booking")
	}
	e.settle()
}

// pac82HoldOnce returns a seam function that holds the first caller only.
func pac82HoldOnce() (seam func(), reached, release chan struct{}) {
	reached, release = make(chan struct{}), make(chan struct{})
	var first atomic.Bool
	return func() {
		if first.CompareAndSwap(false, true) {
			close(reached)
			<-release
		}
	}, reached, release
}

// AC-14: A held after its link read; its retry A' completes; A then answers
// the replay of A' and writes nothing; one row, one completion unit.
func TestPAC82_OriginalHeldBeforeItsChecksReplaysTheRetry(t *testing.T) {
	e := newPAC82Env(t)
	l := e.link(e.host("ac14"), "reusable", nil, []int{30}, true)
	seam, reached, release := pac82HoldOnce()
	afterBookingLinkRead = seam
	defer func() { afterBookingLinkRead = nil }()
	q := pac82Req{email: "k@ac14.test", start: pac82At(10, 0), key: pac82Key("ac14")}
	a := e.async(l.Slug, q)
	<-reached
	retry := e.post(l.Slug, q)
	e.mustStatus(retry, http.StatusCreated, "retry")
	close(release)
	wa := <-a
	e.mustStatus(wa, http.StatusCreated, "original")
	if pac82Decode(t, wa).ID != pac82Decode(t, retry).ID {
		t.Fatal("original and retry answered different bookings")
	}
	e.settle()
	if e.bookingCount(l.ID) != 1 || e.creates.Load() != 1 {
		t.Fatalf("rows %d, host events %d; want 1 and 1", e.bookingCount(l.ID), e.creates.Load())
	}
}

// AC-29: A' passed its replay lookup (nothing yet); its original A commits;
// A' then meets the taken slot and looks for its key again: the replay, never
// a refusal of its own booking.
func TestPAC82_RetryRacingItsOriginalsCommitIsTheReplay(t *testing.T) {
	e := newPAC82Env(t)
	l := e.link(e.host("ac29"), "reusable", nil, []int{30}, true)
	seam, reached, release := pac82HoldOnce()
	afterBookingReplayLookup = seam
	defer func() { afterBookingReplayLookup = nil }()
	q := pac82Req{email: "k@ac29.test", start: pac82At(10, 0), key: pac82Key("ac29")}
	retry := e.async(l.Slug, q)
	<-reached
	orig := e.post(l.Slug, q)
	e.mustStatus(orig, http.StatusCreated, "original")
	close(release)
	wr := <-retry
	e.mustStatus(wr, http.StatusCreated, "retry")
	if pac82Decode(t, wr).ID != pac82Decode(t, orig).ID {
		t.Fatal("retry answered another booking")
	}
	if e.bookingCount(l.ID) != 1 {
		t.Fatal("two rows")
	}
	e.settle()
}

// AC-15 (end to end): A held inside its save after its keys; A' reaches its
// save and waits; A commits: A' answers 201 with A's id.
func TestPAC82_RetryWaitsForTheOriginalsSave(t *testing.T) {
	e := newPAC82Env(t)
	l := e.link(e.host("ac15"), "reusable", nil, []int{30}, true)
	reached, release := make(chan struct{}), make(chan struct{})
	var first atomic.Bool
	storage.AfterBookingLocks = func(*storage.Booking) {
		if first.CompareAndSwap(false, true) {
			close(reached)
			<-release
		}
	}
	defer func() { storage.AfterBookingLocks = nil }()
	q := pac82Req{email: "k@ac15.test", start: pac82At(10, 0), key: pac82Key("ac15")}
	a := e.async(l.Slug, q)
	<-reached
	a2 := e.async(l.Slug, q)
	pac101AwaitWaiter(t, e.db)
	close(release)
	wa, wa2 := <-a, <-a2
	e.mustStatus(wa, http.StatusCreated, "A")
	e.mustStatus(wa2, http.StatusCreated, "A'")
	if pac82Decode(t, wa).ID != pac82Decode(t, wa2).ID {
		t.Fatal("A' saved another booking")
	}
	e.settle()
	if e.bookingCount(l.ID) != 1 || e.creates.Load() != 1 {
		t.Fatalf("rows %d, host events %d; want 1 and 1", e.bookingCount(l.ID), e.creates.Load())
	}
}

// AC-16: the same key with different details released together: one 201, one
// key-reused 409, one row.
func TestPAC82_ConcurrentSameKeyDifferentDetails(t *testing.T) {
	e := newPAC82Env(t)
	l := e.link(e.host("ac16"), "reusable", nil, []int{30}, true)
	key := pac82Key("ac16")
	ws := e.release([]string{l.Slug, l.Slug}, []pac82Req{{email: "k@ac16.test", start: pac82At(9, 0), key: key}, {email: "k@ac16.test", start: pac82At(15, 0), key: key}})
	created, reused := 0, 0
	for _, w := range ws {
		switch {
		case w.Code == http.StatusCreated:
			created++
		case pac82IsReused(w):
			reused++
		default:
			t.Errorf("%d %s", w.Code, w.Body.String())
		}
	}
	if created != 1 || reused != 1 || e.bookingCount(l.ID) != 1 {
		t.Fatalf("created %d reused %d rows %d", created, reused, e.bookingCount(l.ID))
	}
	e.settle()
}

// AC-17: the key's format and its place in the order.
func TestPAC82_KeyFormatAndOrder(t *testing.T) {
	e := newPAC82Env(t)
	l := e.link(e.host("ac17"), "reusable", nil, []int{30}, true)
	bad := `{"error":"invalid Idempotency-Key header"}`
	good := pac82Req{email: "k@ac17.test", start: pac82At(10, 0)}
	for name, q := range map[string]pac82Req{
		"15 characters":  {key: strings.Repeat("a", 15)},
		"256 characters": {key: strings.Repeat("a", 256)},
		"a dot":          {key: "abcdefghijklmnop.q"},
		"quoted":         {key: `"8e03978e-40d5-43e8-bc93-6894a57f9324"`},
		"two lines":      {keys: []string{"aaaaaaaaaaaaaaaaaaaa", "aaaaaaaaaaaaaaaaaaaa"}},
	} {
		q.email, q.start = good.email, good.start
		w := e.post(l.Slug, q)
		if w.Code != http.StatusBadRequest || strings.TrimSpace(w.Body.String()) != bad {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if n := e.bookingCount(l.ID); n != 0 {
		t.Fatalf("%d rows after refused keys", n)
	}
	// After the 404: an unknown slug with a bad key is the 404.
	if w := e.post("pac82-no-such-"+e.sfx, pac82Req{email: "x@ac17.test", start: pac82At(10, 0), key: "bad"}); w.Code != http.StatusNotFound {
		t.Errorf("unknown slug, bad key: %d", w.Code)
	}
	// Before the body: a bad key with invalid JSON is the key's 400.
	if w := e.raw(l.Slug, "{not json", pac82Req{key: "bad"}); strings.TrimSpace(w.Body.String()) != bad {
		t.Errorf("bad key, bad body: %d %s", w.Code, w.Body.String())
	}
	// A body failing validation answers its own 400, with or without a key.
	for _, key := range []string{"", pac82Key("ac17v")} {
		w := e.raw(l.Slug, `{"email":"x@ac17.test","start":"`+pac82At(10, 0).Format(time.RFC3339)+`"}`, pac82Req{key: key})
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "name, email, and start are required") {
			t.Errorf("key %q, missing name: %d %s", key, w.Code, w.Body.String())
		}
	}
	// An empty key is no key.
	q := good
	q.keys = []string{""}
	e.mustStatus(e.post(l.Slug, q), http.StatusCreated, "empty key")
	var stored *string
	if err := e.db.QueryRow(`SELECT idempotency_key FROM bookings WHERE link_id = $1`, l.ID).Scan(&stored); err != nil || stored != nil {
		t.Errorf("empty key stored as %v (%v)", stored, err)
	}
	e.settle()
}

// AC-18 / contract C-4: without a key the 410 still precedes the body's 400;
// with a key, a body 400 that does not depend on the link precedes the 410,
// and "invalid duration" comes after it.
func TestPAC82_KeyedAndKeylessOrder(t *testing.T) {
	e := newPAC82Env(t)
	l := e.link(e.host("ac18"), "single_use", nil, []int{30}, true)
	if _, err := storage.CreateBooking(e.db, &storage.Booking{LinkID: l.ID, BookerName: "P", BookerEmail: "p@ac18.test", StartTime: pac82At(9, 0), EndTime: pac82At(9, 30)}); err != nil {
		t.Fatal(err)
	}
	if w := e.raw(l.Slug, "{not json", pac82Req{}); w.Code != http.StatusGone {
		t.Errorf("keyless, exhausted, bad body: %d, want 410", w.Code)
	}
	if w := e.raw(l.Slug, "{not json", pac82Req{key: pac82Key("ac18")}); w.Code != http.StatusBadRequest {
		t.Errorf("keyed, exhausted, bad body: %d, want 400", w.Code)
	}
	if w := e.post(l.Slug, pac82Req{email: "d@ac18.test", start: pac82At(10, 0), minutes: 45, key: pac82Key("ac18d")}); w.Code != http.StatusGone {
		t.Errorf("keyed, exhausted, duration not offered: %d, want 410", w.Code)
	}
	if w := e.post(l.Slug, pac82Req{email: "d@ac18.test", start: pac82At(10, 0), minutes: 45}); w.Code != http.StatusGone {
		t.Errorf("keyless, exhausted, duration not offered: %d, want 410", w.Code)
	}
	live := e.link(e.host("ac18b"), "reusable", nil, []int{30}, true)
	if w := e.post(live.Slug, pac82Req{email: "d@ac18.test", start: pac82At(10, 0), minutes: 45, key: pac82Key("ac18e")}); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid duration") {
		t.Errorf("keyed, duration not offered: %d %s", w.Code, w.Body.String())
	}
}

// AC-19: a keyed retry on a link deleted, or paused, after the original: 404.
func TestPAC82_RetryOnAGoneLinkIs404(t *testing.T) {
	for _, gone := range []string{"deleted", "paused"} {
		t.Run(gone, func(t *testing.T) {
			e := newPAC82Env(t)
			l := e.link(e.host("ac19-"+gone), "reusable", nil, []int{30}, true)
			q := pac82Req{email: "k@ac19.test", start: pac82At(10, 0), key: pac82Key("ac19")}
			e.mustStatus(e.post(l.Slug, q), http.StatusCreated, "original")
			stmt := `UPDATE scheduling_links SET active = false WHERE id = $1`
			if gone == "deleted" {
				stmt = `UPDATE scheduling_links SET active = false, deleted_at = now() WHERE id = $1`
			}
			if _, err := e.db.Exec(stmt, l.ID); err != nil {
				t.Fatal(err)
			}
			pac101NotFound(t, e.post(l.Slug, q), "retry on a "+gone+" link")
			if e.bookingCount(l.ID) != 1 {
				t.Fatal("rows changed")
			}
			e.settle()
		})
	}
}

// AC-23 (handler): a save waiting on another booking's save past the
// (shortened) bound is the 500 and writes nothing.
func TestPAC82_WaitPastTheBoundIs500(t *testing.T) {
	pac101ShortBound(t)
	e := newPAC82Env(t)
	l := e.link(e.host("ac23"), "reusable", nil, []int{30}, true)
	reached, release := make(chan struct{}), make(chan struct{})
	storage.AfterBookingLocks = func(b *storage.Booking) {
		if b.BookerEmail == "holder@ac23.test" {
			close(reached)
			<-release
		}
	}
	defer func() { storage.AfterBookingLocks = nil }()
	holder := e.async(l.Slug, pac82Req{email: "holder@ac23.test", start: pac82At(10, 0)})
	<-reached
	w := pac101WithinBound(t, "the waiting booking", func() *httptest.ResponseRecorder {
		return e.post(l.Slug, pac82Req{email: "w@ac23.test", start: pac82At(10, 0)})
	})
	close(release)
	if w.Code != http.StatusInternalServerError || strings.TrimSpace(w.Body.String()) != `{"error":"internal error"}` {
		t.Fatalf("%d %s, want the 500", w.Code, w.Body.String())
	}
	e.mustStatus(<-holder, http.StatusCreated, "holder")
	if e.bookingsBy("w@ac23.test") != 0 {
		t.Fatal("the waiter wrote a row")
	}
	e.settle()
}

// AC-24: a soak of concurrent bookings across shared hosts and a use-limited
// link, keyed retries, link deletes, pauses, PATCHes with co-host changes,
// accept / leave and an erasure run: no 40P01, 55P03 or 53200 (no 500); every
// 201 has its row; every refusal wrote nothing; no host is double booked.
func TestPAC82_SoakNeverDeadlocksNorDoubleBooks(t *testing.T) {
	e := newPAC82Env(t)
	a := e.host("ac24-a")
	b := e.host("ac24-b")
	c := e.host("ac24-c")
	cEmail := "pac82-ac24-c-" + e.sfx + "@example.com"
	bEmail := "pac82-ac24-b-" + e.sfx + "@example.com"
	l1 := e.link(a, "reusable", nil, []int{30, 60}, true)
	if _, err := storage.AddLinkHost(e.db, l1.ID, b, "accepted"); err != nil {
		t.Fatal(err)
	}
	l2 := e.link(b, "reusable", nil, []int{30}, true)
	five := 5
	l3 := e.link(a, "recurring", &five, []int{30}, true)
	l4 := e.link(c, "reusable", nil, []int{30}, true)
	if _, err := storage.AddLinkHost(e.db, l4.ID, a, "pending"); err != nil {
		t.Fatal(err)
	}
	doomed := e.link(c, "reusable", nil, []int{30}, true)
	if _, err := storage.CreateBooking(e.db, &storage.Booking{LinkID: doomed.ID, BookerName: "Old", BookerEmail: "old@ac24.test", StartTime: pac82At(9, 0).AddDate(0, 0, -30), EndTime: pac82At(9, 30).AddDate(0, 0, -30)}); err != nil {
		t.Fatal(err)
	}
	runStart := time.Now()
	type answer struct {
		email string
		code  int
		body  string
	}
	var mu sync.Mutex
	var answers []answer
	var failures []string
	var wg sync.WaitGroup
	book := func(slug, email string, s time.Time, minutes int, key string) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := e.post(slug, pac82Req{email: email, start: s, minutes: minutes, key: key})
			mu.Lock()
			answers = append(answers, answer{email, w.Code, w.Body.String()})
			mu.Unlock()
		}()
	}
	other := func(what string, f func() *httptest.ResponseRecorder) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w := f(); w.Code >= 500 {
				mu.Lock()
				failures = append(failures, fmt.Sprintf("%s: %d %s", what, w.Code, w.Body.String()))
				mu.Unlock()
			}
		}()
	}
	p1 := "/api/scheduling-links/" + l1.ID.String()
	p4 := "/api/scheduling-links/" + l4.ID.String()
	for round := 0; round < 4; round++ {
		for i := 0; i < 6; i++ {
			s := pac82At(9, 0).Add(time.Duration(round*2) * time.Hour).Add(time.Duration(i%3) * 15 * time.Minute)
			book(l1.Slug, fmt.Sprintf("l1-%d-%d@ac24.test", round, i), s, 30+30*(i%2), "")
			book(l2.Slug, fmt.Sprintf("l2-%d-%d@ac24.test", round, i), s.Add(15*time.Minute), 30, "")
			book(l3.Slug, fmt.Sprintf("l3-%d-%d@ac24.test", round, i), s.Add(time.Duration(i)*time.Hour+24*time.Hour), 30, "")
			key := pac82Key(fmt.Sprintf("ac24-%d-%d", round, i))
			ks := pac82At(9, 0).AddDate(0, 0, 2).Add(time.Duration(round*6+i) * 15 * time.Minute)
			book(l4.Slug, fmt.Sprintf("l4-%d-%d@ac24.test", round, i), ks, 30, key)
			book(l4.Slug, fmt.Sprintf("l4-%d-%d@ac24.test", round, i), ks, 30, key)
		}
		other("patch", func() *httptest.ResponseRecorder {
			emails := `["` + bEmail + `"]`
			if round%2 == 1 {
				emails = `[]`
			}
			return e.do(http.MethodPatch, p1, a, `{"title":"r","co_host_emails":`+emails+`}`)
		})
		other("pause", func() *httptest.ResponseRecorder { return e.do(http.MethodPatch, p4, c, `{"active":false}`) })
		other("resume", func() *httptest.ResponseRecorder { return e.do(http.MethodPatch, p4, c, `{"active":true}`) })
		other("accept", func() *httptest.ResponseRecorder { return e.do(http.MethodPost, p4+"/accept", a, "") })
		other("leave", func() *httptest.ResponseRecorder { return e.do(http.MethodPost, p4+"/leave", a, "") })
		if round == 1 {
			other("delete", func() *httptest.ResponseRecorder {
				return e.do(http.MethodDelete, "/api/scheduling-links/"+doomed.ID.String(), c, "")
			})
		}
		wg.Wait()
		if round == 2 {
			if _, err := storage.EraseLinkBookers(context.Background(), e.db, doomed.ID, time.Now().AddDate(1, 0, 0)); err != nil {
				t.Errorf("erasure: %v", err)
			}
		}
	}
	_ = cEmail
	for _, f := range failures {
		t.Error(f)
	}
	created := 0
	for _, an := range answers {
		n := e.bookingsBy(an.email)
		switch {
		case an.code == http.StatusCreated:
			created++
			if n < 1 {
				t.Errorf("%s answered 201 without a row", an.email)
			}
		case an.code >= 500:
			t.Errorf("%s answered %d %s", an.email, an.code, an.body)
		case an.code != http.StatusConflict && an.code != http.StatusGone && an.code != http.StatusNotFound:
			t.Errorf("%s answered %d %s", an.email, an.code, an.body)
		}
	}
	// A refusal wrote nothing: every row of the run belongs to a 201's email,
	// and a keyed pair stored one row.
	okEmails := map[string]bool{}
	for _, an := range answers {
		if an.code == http.StatusCreated {
			okEmails[an.email] = true
		}
	}
	rows, err := e.db.Query(`SELECT booker_email, count(*) FROM bookings WHERE booker_email LIKE '%@ac24.test' AND created_at >= $1 GROUP BY booker_email`, runStart.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var em string
		var n int
		if err := rows.Scan(&em, &n); err != nil {
			t.Fatal(err)
		}
		if !okEmails[em] || n != 1 {
			t.Errorf("%s has %d row(s) (a 201: %v)", em, n, okEmails[em])
		}
	}
	rows.Close()
	// No host has two overlapping non-cancelled bookings created in the run.
	var doubles int
	if err := e.db.QueryRow(`
		WITH hb AS (
			SELECT bk.id, bk.start_time, bk.end_time, h.user_id
			FROM bookings bk JOIN scheduling_link_hosts h ON h.link_id = bk.link_id AND h.status = 'accepted'
			WHERE bk.status <> 'cancelled' AND bk.created_at >= $1 AND bk.booker_email LIKE '%@ac24.test'
			UNION
			SELECT bk.id, bk.start_time, bk.end_time, l.owner_user_id
			FROM bookings bk JOIN scheduling_links l ON l.id = bk.link_id
			WHERE bk.status <> 'cancelled' AND bk.created_at >= $1 AND bk.booker_email LIKE '%@ac24.test')
		SELECT count(*) FROM hb x JOIN hb y ON x.user_id = y.user_id AND x.id < y.id
		WHERE x.start_time < y.end_time AND y.start_time < x.end_time`, runStart.Add(-time.Minute)).Scan(&doubles); err != nil {
		t.Fatal(err)
	}
	if doubles != 0 {
		t.Errorf("%d overlapping booking pairs for a host", doubles)
	}
	if created == 0 {
		t.Error("no booking succeeded: the soak proved nothing")
	}
	if n := e.bookingCount(l3.ID); n > five {
		t.Errorf("the max-5 link stores %d bookings", n)
	}
	e.settle()
}

// The middleware allows the key header cross-origin (AC-20, unit half).
func TestPAC82_CORSAllowsTheKeyHeader(t *testing.T) {
	h := corsMiddleware("https://app.example")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := httptest.NewRequest(http.MethodOptions, "/api/book/x", nil)
	req.Header.Set("Origin", "https://app.example")
	req.Header.Set("Access-Control-Request-Headers", "content-type, idempotency-key")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if got := w.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type, Authorization, Idempotency-Key" {
		t.Fatalf("Allow-Headers %q", got)
	}
}
