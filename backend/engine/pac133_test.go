package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/internal/budget"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	googlecalendar "google.golang.org/api/calendar/v3"
)

// PAC-133: a calendar the provider cannot read is unknown, never free.

func slot(start time.Time, d time.Duration) calendar.TimeSlot {
	return calendar.TimeSlot{Start: start, End: start.Add(d)}
}

// --- AC-38: the engine double tells the truth ---------------------------------

func TestPAC133_AC38_EngineDoubleAnswersOnlyWhatItWasTold(t *testing.T) {
	w := slot(time.Date(2030, 1, 7, 10, 0, 0, 0, time.UTC), time.Hour)
	m := &mockCalOps{calID: "primary", fb: map[string]calendar.FreeBusyEntry{"b@x.test": readable(w)}}
	got, err := m.getFreeBusy(context.Background(), []string{"primary", "b@x.test", "stranger@x.test"}, w.Start, w.End)
	if err != nil {
		t.Fatal(err)
	}
	if e := got["primary"]; !e.Known || len(e.Busy) != 0 {
		t.Errorf("own = %+v, want readable with no windows", e)
	}
	if e := got["b@x.test"]; !e.Known || len(e.Busy) != 1 {
		t.Errorf("listed = %+v, want readable with its window", e)
	}
	if e := got["stranger@x.test"]; e.Known || e.Busy == nil || len(e.Busy) != 0 {
		t.Errorf("unlisted = %+v, want unknown with no windows (never free)", e)
	}
	if c := m.calls(); len(c) != 1 || len(c[0].ids) != 3 {
		t.Errorf("calls = %+v, want one recorded with its ids", c)
	}
}

// --- Participant lookup (FreeBusyService) -------------------------------------

type stepClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *stepClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// countingOutlook is an Outlook reader double: it answers each call with
// answer and records it.
type countingOutlook struct {
	mu     sync.Mutex
	calls  []fbCall
	answer func(ctx context.Context, ids []string, start, end time.Time) (map[string]calendar.FreeBusyEntry, error)
}

func (o *countingOutlook) factory(oauth2.TokenSource) outlookFreeBusy {
	return outlookFunc(func(ctx context.Context, ids []string, start, end time.Time) (map[string]calendar.FreeBusyEntry, error) {
		o.mu.Lock()
		o.calls = append(o.calls, fbCall{ids: append([]string(nil), ids...), start: start, end: end})
		o.mu.Unlock()
		return o.answer(ctx, ids, start, end)
	})
}

func (o *countingOutlook) n() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.calls)
}

// calendarsAnswer answers from a table of readable calendars (windows clipped
// to the question); any other id is unknown with no windows.
func calendarsAnswer(table map[string][]calendar.TimeSlot) func(context.Context, []string, time.Time, time.Time) (map[string]calendar.FreeBusyEntry, error) {
	return func(_ context.Context, ids []string, start, end time.Time) (map[string]calendar.FreeBusyEntry, error) {
		out := map[string]calendar.FreeBusyEntry{}
		for _, id := range ids {
			windows, ok := table[id]
			if !ok {
				out[id] = unknownWith()
				continue
			}
			e := readable()
			for _, w := range windows {
				if w.Start.Before(end) && w.End.After(start) {
					e.Busy = append(e.Busy, w)
				}
			}
			out[id] = e
		}
		return out, nil
	}
}

// outlookRequester is a user whose provider is Outlook, with a valid stored
// credential (no renewal), and a service reading through rec.
func outlookRequester(t *testing.T, db *sql.DB, rec *countingOutlook) (uuid.UUID, *FreeBusyService, *stepClock) {
	t.Helper()
	user := pac67User(t, db, "pac133-"+uuid.NewString()+"@example.com")
	storeOutlookCredential(t, db, user, "m-access", "m-refresh", time.Now().Add(24*time.Hour))
	clock := &stepClock{t: time.Date(2030, 1, 1, 8, 0, 0, 0, time.UTC)}
	s := NewFreeBusyService(db, &oauth2.Config{})
	s.Microsoft = &auth.MicrosoftConfig{ClientID: "m", ClientSecret: "s"}
	s.newOutlook = rec.factory
	s.Clock = clock
	return user, s, clock
}

var monday = time.Date(2030, 1, 7, 0, 0, 0, 0, time.UTC)

func query(t *testing.T, s *FreeBusyService, user uuid.UUID, emails []string, start, end time.Time) map[string]ParticipantBusy {
	t.Helper()
	res, err := s.Query(context.Background(), user, emails, start, end)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	out := map[string]ParticipantBusy{}
	for _, r := range res {
		out[r.Email] = r
	}
	return out
}

// AC-7: an unknown answer is cached like a known one for 15 minutes, then
// asked again. (The credential-lost half is TestPAC67_RefusedLookupIsNotServedAfterReconnect.)
func TestPAC133_AC7_UnknownIsCachedFor15Minutes(t *testing.T) {
	db := openTestDB(t)
	rec := &countingOutlook{answer: calendarsAnswer(nil)}
	user, s, clock := outlookRequester(t, db, rec)
	for i := 0; i < 2; i++ {
		if got := query(t, s, user, []string{"u@corp.example"}, monday.Add(9*time.Hour), monday.Add(17*time.Hour)); got["u@corp.example"].Coverage != "unknown" {
			t.Fatalf("ask %d = %+v, want unknown", i+1, got)
		}
	}
	if rec.n() != 1 {
		t.Fatalf("provider calls = %d, want 1 (the second ask is served from the cache)", rec.n())
	}
	clock.advance(freeBusyTTL + time.Second)
	if got := query(t, s, user, []string{"u@corp.example"}, monday.Add(9*time.Hour), monday.Add(17*time.Hour)); got["u@corp.example"].Coverage != "unknown" {
		t.Fatalf("after the TTL = %+v", got)
	}
	if rec.n() != 2 {
		t.Errorf("provider calls = %d after 15 minutes, want 2", rec.n())
	}
}

// AC-8 (SF-033): the cache answers only the same range.
func TestPAC133_AC8_CacheAnswersOnlyTheSameRange(t *testing.T) {
	db := openTestDB(t)
	tuesday := slot(monday.Add(24*time.Hour+10*time.Hour), time.Hour)
	rec := &countingOutlook{answer: calendarsAnswer(map[string][]calendar.TimeSlot{
		"x@corp.example": {slot(monday.Add(9*time.Hour), time.Hour), tuesday},
	})}
	user, s, _ := outlookRequester(t, db, rec)

	day := query(t, s, user, []string{"x@corp.example"}, monday, monday.Add(24*time.Hour))
	if len(day["x@corp.example"].Busy) != 1 {
		t.Fatalf("day answer = %+v", day)
	}
	week := query(t, s, user, []string{"x@corp.example"}, monday, monday.Add(7*24*time.Hour))
	found := false
	for _, w := range week["x@corp.example"].Busy {
		if w.Start.Equal(tuesday.Start) {
			found = true
		}
	}
	if !found || rec.n() != 2 {
		t.Fatalf("week answer = %+v after %d provider calls, want Tuesday's window and 2 calls", week, rec.n())
	}

	query(t, s, user, []string{"x@corp.example"}, monday.Add(9*time.Hour), monday.Add(10*time.Hour))
	query(t, s, user, []string{"x@corp.example"}, monday.Add(14*time.Hour), monday.Add(15*time.Hour))
	if rec.n() != 4 {
		t.Errorf("provider calls = %d, want 4: 14:00-15:00 is a new question on the same day", rec.n())
	}
	// The same instants in another offset are the same question.
	paris := time.FixedZone("CET", 3600)
	query(t, s, user, []string{"X@corp.example"}, monday.Add(14*time.Hour).In(paris), monday.Add(15*time.Hour).In(paris))
	if rec.n() != 4 {
		t.Errorf("provider calls = %d, want 4: the same instants are the same key", rec.n())
	}
}

// AC-9: a bounded LRU; an expired entry is never served.
func TestPAC133_AC9_CacheIsABoundedLRU(t *testing.T) {
	db := openTestDB(t)
	rec := &countingOutlook{answer: calendarsAnswer(map[string][]calendar.TimeSlot{"a@corp.example": nil, "b@corp.example": nil, "c@corp.example": nil})}
	user, s, clock := outlookRequester(t, db, rec)
	s.maxEntries = 2
	ask := func(email string) { query(t, s, user, []string{email}, monday, monday.Add(time.Hour)) }

	ask("a@corp.example")
	ask("b@corp.example")
	ask("c@corp.example") // evicts a, the least recently used
	if rec.n() != 3 {
		t.Fatalf("calls = %d, want 3", rec.n())
	}
	ask("a@corp.example")
	if rec.n() != 4 {
		t.Fatalf("calls = %d: the evicted entry was served", rec.n())
	}
	// Now c and a are held; using c makes a the least recently used.
	ask("c@corp.example")
	ask("b@corp.example") // evicts a
	ask("c@corp.example")
	if rec.n() != 5 {
		t.Fatalf("calls = %d, want 5: c was recently used and must still be held", rec.n())
	}
	clock.advance(freeBusyTTL + time.Second)
	ask("c@corp.example")
	if rec.n() != 6 {
		t.Errorf("calls = %d: an expired entry was served", rec.n())
	}
	if s.lru.Len() > 2 {
		t.Errorf("cache holds %d entries, over the maximum 2", s.lru.Len())
	}
	if (&FreeBusyService{}).cacheMax() != 10000 {
		t.Error("the default maximum is not 10,000")
	}
}

// A lookup cut short by its own request's deadline is never cached (PAC-133 §4
// as PAC-129 §6 rules it).
func TestPAC133_DeadlineCutLookupIsNotCached(t *testing.T) {
	db := openTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	rec := &countingOutlook{answer: func(context.Context, []string, time.Time, time.Time) (map[string]calendar.FreeBusyEntry, error) {
		cancel() // the client went away during the call
		return nil, context.Canceled
	}}
	user, s, _ := outlookRequester(t, db, rec)
	if res, err := s.Query(ctx, user, []string{"u@corp.example"}, monday, monday.Add(time.Hour)); err != nil || res[0].Coverage != "unknown" {
		t.Fatalf("Query = %+v, %v", res, err)
	}
	rec.answer = calendarsAnswer(map[string][]calendar.TimeSlot{"u@corp.example": nil})
	if got := query(t, s, user, []string{"u@corp.example"}, monday, monday.Add(time.Hour)); got["u@corp.example"].Coverage != "known" || rec.n() != 2 {
		t.Errorf("after a cut lookup: %+v with %d calls, want known from a second call", got, rec.n())
	}
}

// AC-12, AC-13: partial windows on unknown; a group not fully read is unknown
// with its members' windows; a personal domain and a whole failure are
// unknown; readable and free stays known with [].
func TestPAC133_AC12_AC13_QueryOutcomes(t *testing.T) {
	db := openTestDB(t)
	p := slot(monday.Add(13*time.Hour), time.Hour)
	m3 := slot(monday.Add(15*time.Hour), time.Hour)
	fail := false
	rec := &countingOutlook{answer: func(_ context.Context, ids []string, _, _ time.Time) (map[string]calendar.FreeBusyEntry, error) {
		if fail {
			return nil, errors.New("graph down")
		}
		return map[string]calendar.FreeBusyEntry{
			"p@corp.example":    unknownWith(p),
			"g3@corp.example":   group(true, calendar.FreeBusyMember{ID: "m3@corp.example", Known: true, Busy: []calendar.TimeSlot{m3}}, calendar.FreeBusyMember{ID: "m4@corp.example", Busy: []calendar.TimeSlot{}}),
			"free@corp.example": readable(),
		}, nil
	}}
	user, s, _ := outlookRequester(t, db, rec)
	got := query(t, s, user, []string{"p@corp.example", "g3@corp.example", "free@corp.example", "friend@gmail.com"}, monday, monday.Add(24*time.Hour))
	if r := got["p@corp.example"]; r.Coverage != "unknown" || len(r.Busy) != 1 || !r.Busy[0].Start.Equal(p.Start) {
		t.Errorf("P = %+v, want unknown with 13:00-14:00", r)
	}
	if r := got["g3@corp.example"]; r.Coverage != "unknown" || len(r.Busy) != 1 || !r.Busy[0].Start.Equal(m3.Start) {
		t.Errorf("G3 = %+v, want unknown with M3's window", r)
	}
	if r := got["free@corp.example"]; r.Coverage != "known" || r.Busy == nil || len(r.Busy) != 0 {
		t.Errorf("readable free = %+v, want known with []", r)
	}
	if r := got["friend@gmail.com"]; r.Coverage != "unknown" {
		t.Errorf("personal domain = %+v", r)
	}
	if c := rec.calls; len(c) != 1 || len(c[0].ids) != 3 {
		t.Errorf("calls = %+v, want one without the personal address", c)
	}
	fail = true
	if r := query(t, s, user, []string{"other@corp.example"}, monday, monday.Add(24*time.Hour))["other@corp.example"]; r.Coverage != "unknown" || len(r.Busy) != 0 {
		t.Errorf("whole failure = %+v, want unknown with no windows", r)
	}
}

// AC-6: an Outlook requester, Graph answering A with a schedule error: A is
// unknown, through the real Outlook reader and the graphBaseURL seam.
func TestPAC133_AC6_OutlookScheduleErrorIsUnknown(t *testing.T) {
	db := openTestDB(t)
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.0/me/calendar/getSchedule" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":[
			{"scheduleId":"a@corp.example","scheduleItems":[],"error":{"message":"not found","responseCode":"ErrorMailboxNotFound"}},
			{"scheduleId":"b@corp.example","scheduleItems":[]}]}`))
	}))
	t.Cleanup(graph.Close)
	calendar.InstallGraphBaseURL(t, graph.URL+"/v1.0")
	user := pac67User(t, db, "pac133-ac6-"+uuid.NewString()+"@example.com")
	storeOutlookCredential(t, db, user, "m-access", "m-refresh", time.Now().Add(24*time.Hour))
	s := NewFreeBusyService(db, &oauth2.Config{})
	s.Microsoft = &auth.MicrosoftConfig{ClientID: "m", ClientSecret: "s"}
	got := query(t, s, user, []string{"a@corp.example", "b@corp.example"}, monday, monday.Add(24*time.Hour))
	if got["a@corp.example"].Coverage != "unknown" {
		t.Errorf("A = %+v, want unknown", got["a@corp.example"])
	}
	if got["b@corp.example"].Coverage != "known" {
		t.Errorf("B = %+v, want known (the stand-in is reached)", got["b@corp.example"])
	}
}

// pac133Google points the Google client at a stand-in whose freeBusy answers
// every requested id from calendars (raw Google JSON per id); an id not in
// calendars is left out of the answer.
func pac133Google(t *testing.T, calendars map[string]string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/freeBusy") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"kind":"calendar#events","items":[]}`))
			return
		}
		var req googlecalendar.FreeBusyRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		parts := []string{}
		for _, it := range req.Items {
			if body, ok := calendars[it.Id]; ok {
				b, _ := json.Marshal(it.Id)
				parts = append(parts, string(b)+":"+body)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"kind":"calendar#freeBusy","calendars":{` + strings.Join(parts, ",") + `}}`))
	}))
	t.Cleanup(srv.Close)
	calendar.Configure(config.CalendarEndpoint{BaseURL: srv.URL + config.CalendarAPIRoot, Guard: true})
	t.Cleanup(func() { calendar.Configure(config.ProductionCalendarEndpoint()) })
}

// AC-11: an external member the manager's credential cannot read is not
// measured, never 0 meeting minutes and a full free week.
func TestPAC133_AC11_ManagerExternalMemberUnknownIsNoData(t *testing.T) {
	db := openTestDB(t)
	pac133Google(t, map[string]string{"ext@partner.example": `{"busy":[],"errors":[{"domain":"global","reason":"notFound"}]}`})
	manager := pac67User(t, db, "pac133-manager-"+uuid.NewString()+"@example.com")
	if err := auth.UpsertUserToken(db, manager, &oauth2.Token{AccessToken: "g", RefreshToken: "r", Expiry: time.Now().Add(24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	e := &ManagerEngine{DB: db, OAuthConfig: &oauth2.Config{}}
	stats, err := e.GetMemberWeek(context.Background(), manager, &storage.ManagerTeamMember{MemberEmail: "ext@partner.example"}, monday)
	if err != nil {
		t.Fatal(err)
	}
	if stats.DataAvailable || stats.MeetingMinutes != 0 || stats.FreeMinutes != 0 {
		t.Errorf("stats = %+v, want not measured", stats)
	}
}

// --- Meeting suggestions -----------------------------------------------------

// suggestWindow returns a context for a new user and the start of one of
// their future work days (two clear hours from it, before lunch).
func suggestWindow(t *testing.T, db *sql.DB) (context.Context, time.Time) {
	t.Helper()
	ctx, user := smartScheduleUser(t, db, "pac133-suggest-"+uuid.NewString()+"@example.com")
	settings, err := storage.GetOrCreateSettingsByUser(db, user)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, futureEnabledWorkSlot(t, settings).Start
}

type suggestFix struct {
	db  *sql.DB
	ctx context.Context
	ws  time.Time
}

func suggestFixture(t *testing.T) suggestFix {
	t.Helper()
	db := openTestDB(t)
	ctx, ws := suggestWindow(t, db)
	return suggestFix{db: db, ctx: ctx, ws: ws}
}

// suggest asks for 30-minute slots over two hours from the work start.
func (f suggestFix) suggest(t *testing.T, m *mockCalOps, attendees ...string) *ScheduleSuggestions {
	t.Helper()
	got, err := (&SmartScheduler{DB: f.db, calOps: m}).Suggest(f.ctx, ScheduleRequest{
		DurationMinutes: 30, Attendees: attendees, RangeStart: f.ws, RangeEnd: f.ws.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	return got
}

func assertNoOverlap(t *testing.T, got *ScheduleSuggestions, busy calendar.TimeSlot) {
	t.Helper()
	for _, s := range got.Slots {
		if s.Start.Before(busy.End) && s.End.After(busy.Start) {
			t.Errorf("slot %s-%s overlaps reported busy time %s-%s", s.Start, s.End, busy.Start, busy.End)
		}
	}
}

// AC-21: an unknown attendee does not constrain; a readable one does; the
// answer names exactly the unknown one.
func TestPAC133_AC21_UnknownAttendeeIsNamedAndDoesNotConstrain(t *testing.T) {
	f := suggestFixture(t)
	busy := slot(f.ws.Add(30*time.Minute), time.Hour)
	m := &mockCalOps{calID: "primary", fb: map[string]calendar.FreeBusyEntry{"b@corp.example": readable(busy)}}
	got := f.suggest(t, m, "b@corp.example", "u@corp.example")
	assertNoOverlap(t, got, busy)
	if len(got.Slots) == 0 {
		t.Fatal("no slot offered: the unknown attendee constrained the answer")
	}
	if len(got.UnknownAttendees) != 1 || got.UnknownAttendees[0] != "u@corp.example" {
		t.Errorf("unknown_attendees = %v, want [u@corp.example]", got.UnknownAttendees)
	}
	if c := m.calls(); len(c) != 1 || c[0].ids[0] != "primary" {
		t.Errorf("calls = %+v, want one including the own calendar first", c)
	}
}

// AC-22: everyone read → an empty list that is present.
func TestPAC133_AC22_EveryoneReadIsAnEmptyList(t *testing.T) {
	f := suggestFixture(t)
	m := &mockCalOps{calID: "primary", fb: map[string]calendar.FreeBusyEntry{"b@corp.example": readable()}}
	got := f.suggest(t, m, "b@corp.example")
	body, _ := json.Marshal(got)
	if got.UnknownAttendees == nil || !strings.Contains(string(body), `"unknown_attendees":[]`) {
		t.Errorf("body = %s, want unknown_attendees present and empty", body)
	}
}

// AC-23: the own calendar unknown fails the request; no slot is offered.
func TestPAC133_AC23_OwnCalendarUnknownFails(t *testing.T) {
	f := suggestFixture(t)
	for name, own := range map[string]calendar.FreeBusyEntry{"no windows": unknownWith(), "with windows": unknownWith(slot(f.ws, time.Hour))} {
		t.Run(name, func(t *testing.T) {
			m := &mockCalOps{calID: "primary", fb: map[string]calendar.FreeBusyEntry{"primary": own}}
			got, err := (&SmartScheduler{DB: f.db, calOps: m}).Suggest(f.ctx, ScheduleRequest{DurationMinutes: 30, RangeStart: f.ws, RangeEnd: f.ws.Add(2 * time.Hour)})
			if !errors.Is(err, ErrOwnCalendarUnknown) || got != nil {
				t.Errorf("Suggest = %+v, %v; want ErrOwnCalendarUnknown and no slots", got, err)
			}
			if errors.Is(err, ErrCalendarNotConnected) {
				t.Error("own calendar unknown must not read as not connected (the 401)")
			}
		})
	}
}

// AC-24: windows reported for an unknown attendee constrain; the attendee is named.
func TestPAC133_AC24_PartialWindowsConstrain(t *testing.T) {
	f := suggestFixture(t)
	busy := slot(f.ws, 90*time.Minute)
	m := &mockCalOps{calID: "primary", fb: map[string]calendar.FreeBusyEntry{"p@corp.example": unknownWith(busy)}}
	got := f.suggest(t, m, "p@corp.example")
	assertNoOverlap(t, got, busy)
	if len(got.Slots) == 0 {
		t.Fatal("no slot: only P's reported window should constrain")
	}
	if len(got.UnknownAttendees) != 1 || got.UnknownAttendees[0] != "p@corp.example" {
		t.Errorf("unknown_attendees = %v, want [p@corp.example]", got.UnknownAttendees)
	}
}

// AC-25: a readable group constrains by its members' windows and is not
// named; a group with an unknown member still constrains and is named by the
// group address, never by its members.
func TestPAC133_AC25_Groups(t *testing.T) {
	f := suggestFixture(t)
	busy := slot(f.ws, 90*time.Minute)
	t.Run("readable group", func(t *testing.T) {
		m := &mockCalOps{calID: "primary", fb: map[string]calendar.FreeBusyEntry{"g@corp.example": group(true,
			calendar.FreeBusyMember{ID: "m1@corp.example", Known: true, Busy: []calendar.TimeSlot{busy}},
			calendar.FreeBusyMember{ID: "m2@corp.example", Known: true, Busy: []calendar.TimeSlot{}})}}
		got := f.suggest(t, m, "g@corp.example")
		assertNoOverlap(t, got, busy)
		if len(got.Slots) == 0 || len(got.UnknownAttendees) != 0 {
			t.Errorf("got %+v, want slots and nobody named", got)
		}
	})
	t.Run("group with an unknown member", func(t *testing.T) {
		m := &mockCalOps{calID: "primary", fb: map[string]calendar.FreeBusyEntry{"g3@corp.example": group(true,
			calendar.FreeBusyMember{ID: "m3@corp.example", Known: true, Busy: []calendar.TimeSlot{busy}},
			calendar.FreeBusyMember{ID: "m4@corp.example", Known: false, Busy: []calendar.TimeSlot{}})}}
		got := f.suggest(t, m, "g3@corp.example", "g3@corp.example")
		assertNoOverlap(t, got, busy)
		if len(got.Slots) == 0 {
			t.Fatal("no slot")
		}
		if len(got.UnknownAttendees) != 1 || got.UnknownAttendees[0] != "g3@corp.example" {
			t.Errorf("unknown_attendees = %v, want [g3@corp.example] once", got.UnknownAttendees)
		}
	})
}

// --- Compression ---------------------------------------------------------------

// compressionDay is a Monday with the new user's settings: a context, and the
// day's work start in the user's zone.
func compressionDay(t *testing.T, db *sql.DB) (context.Context, time.Time, time.Time) {
	t.Helper()
	ctx, user := smartScheduleUser(t, db, "pac133-compress-"+uuid.NewString()+"@example.com")
	s, err := storage.GetOrCreateSettingsByUser(db, user)
	if err != nil {
		t.Fatal(err)
	}
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		loc = time.UTC
	}
	day := time.Date(2025, 1, 6, 0, 0, 0, 0, loc)
	start, _, enabled := s.WorkWindow(day)
	if !enabled {
		t.Fatal("default settings have no Monday work window")
	}
	return ctx, day, parseHHMM(start, day, loc)
}

func organised(id string, start time.Time, d time.Duration, attendees ...string) *googlecalendar.Event {
	ev := &googlecalendar.Event{
		Id:        id,
		Summary:   id,
		Start:     &googlecalendar.EventDateTime{DateTime: start.Format(time.RFC3339)},
		End:       &googlecalendar.EventDateTime{DateTime: start.Add(d).Format(time.RFC3339)},
		Organizer: &googlecalendar.EventOrganizer{Self: true},
		Attendees: []*googlecalendar.EventAttendee{{Email: "me@example.com", Self: true, ResponseStatus: "accepted"}},
	}
	for _, a := range attendees {
		ev.Attendees = append(ev.Attendees, &googlecalendar.EventAttendee{Email: a, ResponseStatus: "accepted"})
	}
	return ev
}

// AC-29: a position is proposed only when every attendee is readable and free.
func TestPAC133_AC29_PreviewNeverMovesPastAnUnknownAttendee(t *testing.T) {
	db := openTestDB(t)
	ctx, day, ws := compressionDay(t, db)
	ev := organised("m", ws.Add(5*time.Hour), time.Hour, "u@corp.example")

	unknown := &mockCalOps{calID: "primary", events: []*googlecalendar.Event{ev}}
	got, err := (&CompressionEngine{DB: db, calOps: unknown}).SuggestForDay(ctx, day)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Proposals) != 0 {
		t.Errorf("proposals = %+v with U unknown, want none", got.Proposals)
	}
	if len(unknown.calls()) == 0 {
		t.Fatal("no free/busy call: the candidate was never checked, so the test proves nothing")
	}

	free := &mockCalOps{calID: "primary", events: []*googlecalendar.Event{ev}, fb: map[string]calendar.FreeBusyEntry{"u@corp.example": readable()}}
	got, err = (&CompressionEngine{DB: db, calOps: free}).SuggestForDay(ctx, day)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Proposals) != 1 || !strings.HasPrefix(got.Proposals[0].Reason, "Creates ") {
		t.Errorf("proposals = %+v with U readable and free, want one with today's reason", got.Proposals)
	}
	// A reported window over the position rejects it, whatever the outcome.
	reported := &mockCalOps{calID: "primary", events: []*googlecalendar.Event{ev}, fb: map[string]calendar.FreeBusyEntry{"u@corp.example": readable(slot(ws, 10*time.Hour))}}
	if got, _ := (&CompressionEngine{DB: db, calOps: reported}).SuggestForDay(ctx, day); len(got.Proposals) != 0 {
		t.Errorf("proposals = %+v over a busy attendee", got.Proposals)
	}
}

// AC-35: preview makes at most one call per evaluated position, for all the
// meeting's attendees at once; apply makes at most one call and exactly one
// event read per proposal.
func TestPAC133_AC35_CallBounds(t *testing.T) {
	db := openTestDB(t)
	ctx, day, ws := compressionDay(t, db)
	m := organised("m", ws.Add(5*time.Hour), time.Hour, "u1@corp.example", "u2@corp.example")
	others := []*googlecalendar.Event{
		organised("o1", ws.Add(1*time.Hour), 30*time.Minute),
		organised("o2", ws.Add(7*time.Hour), 30*time.Minute),
		organised("o3", ws.Add(8*time.Hour), 30*time.Minute),
	}
	mock := &mockCalOps{calID: "primary", events: append([]*googlecalendar.Event{m}, others...)}
	if _, err := (&CompressionEngine{DB: db, calOps: mock}).SuggestForDay(ctx, day); err != nil {
		t.Fatal(err)
	}
	calls := mock.calls()
	// Candidate positions: the work start, plus two per other movable meeting
	// and two per fixed interval (lunch).
	if bound := 1 + 2*len(others) + 2; len(calls) == 0 || len(calls) > bound {
		t.Fatalf("preview calls = %d, want between 1 and %d", len(calls), bound)
	}
	seen := map[int64]bool{}
	for _, c := range calls {
		if len(c.ids) != 2 || c.ids[0] != "u1@corp.example" || c.ids[1] != "u2@corp.example" {
			t.Errorf("call ids = %v, want one call for both attendees", c.ids)
		}
		if seen[c.start.UnixNano()] {
			t.Errorf("position %s checked twice", c.start)
		}
		seen[c.start.UnixNano()] = true
	}

	apply := &mockCalOps{calID: "primary", events: []*googlecalendar.Event{m, others[0]}}
	_, _, err := (&CompressionEngine{DB: db, calOps: apply}).Apply(ctx, []MoveProposal{
		{EventID: "m", ProposedStart: ws.Add(2 * time.Hour), ProposedEnd: ws.Add(3 * time.Hour)},
		{EventID: "o1", ProposedStart: ws, ProposedEnd: ws.Add(30 * time.Minute)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(apply.calls()) != 1 || len(apply.getCalls) != 2 {
		t.Errorf("apply: %d free/busy calls and %d event reads for 2 proposals, want 1 and 2", len(apply.calls()), len(apply.getCalls))
	}
}

func applyOne(t *testing.T, db *sql.DB, ctx context.Context, m *mockCalOps, p MoveProposal) ([]string, []string) {
	t.Helper()
	applied, failed, err := (&CompressionEngine{DB: db, calOps: m}).Apply(ctx, []MoveProposal{p})
	if err != nil {
		t.Fatal(err)
	}
	return applied, failed
}

func movedEntries(t *testing.T, db *sql.DB, ctx context.Context) int {
	t.Helper()
	entries, err := storage.ListAuditLog(db, auth.UserIDFromContext(ctx), 100)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if e.Action == "meeting_moved" {
			n++
		}
	}
	return n
}

// AC-30, AC-32: an unknown attendee, or a failed check, is not moved and is
// reported unverified, distinct from the conflict reason.
func TestPAC133_AC30_AC32_ApplyUnverified(t *testing.T) {
	db := openTestDB(t)
	ctx, _, ws := compressionDay(t, db)
	ev := organised("m", ws.Add(time.Hour), time.Hour, "u@corp.example")
	p := MoveProposal{EventID: "m", ProposedStart: ws.Add(5 * time.Hour), ProposedEnd: ws.Add(6 * time.Hour)}
	for name, m := range map[string]*mockCalOps{
		"unknown attendee": {calID: "primary", events: []*googlecalendar.Event{ev}},
		"failed check":     {calID: "primary", events: []*googlecalendar.Event{ev}, fbErr: errors.New("provider down"), fb: map[string]calendar.FreeBusyEntry{"u@corp.example": readable()}},
	} {
		t.Run(name, func(t *testing.T) {
			applied, failed := applyOne(t, db, ctx, m, p)
			if len(applied) != 0 || len(failed) != 1 || failed[0] != "m: attendee availability unverified" {
				t.Errorf("applied %v failed %v, want m unverified", applied, failed)
			}
			if len(m.updatedEvts) != 0 {
				t.Errorf("event written: %v", m.updatedEvts)
			}
		})
	}
	if n := movedEntries(t, db, ctx); n != 0 {
		t.Errorf("meeting_moved entries = %d, want 0", n)
	}
	if reasonAttendeeUnverified == reasonAttendeeConflict || reasonAttendeeUnverified == "invalid time format" {
		t.Error("the unverified reason is not distinct")
	}
}

// AC-31: a readable attendee busy over the proposed time is a conflict.
func TestPAC133_AC31_ApplyConflict(t *testing.T) {
	db := openTestDB(t)
	ctx, _, ws := compressionDay(t, db)
	ev := organised("m", ws.Add(time.Hour), time.Hour, "b@corp.example")
	proposed := slot(ws.Add(5*time.Hour), time.Hour)
	m := &mockCalOps{calID: "primary", events: []*googlecalendar.Event{ev}, fb: map[string]calendar.FreeBusyEntry{"b@corp.example": readable(proposed)}}
	applied, failed := applyOne(t, db, ctx, m, MoveProposal{EventID: "m", ProposedStart: proposed.Start, ProposedEnd: proposed.End})
	if len(applied) != 0 || len(failed) != 1 || failed[0] != "m: attendee conflict at proposed time" {
		t.Errorf("applied %v failed %v, want m in conflict", applied, failed)
	}
	// Conflict wins over unverified: a reported window is a known clash.
	m = &mockCalOps{calID: "primary", events: []*googlecalendar.Event{organised("m", ws.Add(time.Hour), time.Hour, "b@corp.example", "u@corp.example")},
		fb: map[string]calendar.FreeBusyEntry{"b@corp.example": readable(proposed)}}
	if _, failed := applyOne(t, db, ctx, m, MoveProposal{EventID: "m", ProposedStart: proposed.Start, ProposedEnd: proposed.End}); len(failed) != 1 || failed[0] != "m: attendee conflict at proposed time" {
		t.Errorf("failed %v, want the conflict reason ahead of unverified", failed)
	}
}

// AC-33: readable and free → moved, after one call naming exactly the
// non-self, non-declined attendees over the proposed interval; no other
// attendee → moved with no call.
func TestPAC133_AC33_ApplyChecksTheRealAttendees(t *testing.T) {
	db := openTestDB(t)
	ctx, _, ws := compressionDay(t, db)
	ev := organised("m", ws.Add(time.Hour), time.Hour, "b@corp.example", "c@corp.example")
	ev.Attendees = append(ev.Attendees, &googlecalendar.EventAttendee{Email: "declined@corp.example", ResponseStatus: "declined"})
	p := MoveProposal{EventID: "m", ProposedStart: ws.Add(5 * time.Hour), ProposedEnd: ws.Add(6 * time.Hour)}
	m := &mockCalOps{calID: "primary", events: []*googlecalendar.Event{ev}, fb: map[string]calendar.FreeBusyEntry{"b@corp.example": readable(), "c@corp.example": readable()}}
	applied, failed := applyOne(t, db, ctx, m, p)
	if len(applied) != 1 || len(failed) != 0 {
		t.Fatalf("applied %v failed %v, want m moved", applied, failed)
	}
	c := m.calls()
	if len(c) != 1 || strings.Join(c[0].ids, ",") != "b@corp.example,c@corp.example" || !c[0].start.Equal(p.ProposedStart) || !c[0].end.Equal(p.ProposedEnd) {
		t.Errorf("calls = %+v, want one for b and c over the proposed interval", c)
	}
	if movedEntries(t, db, ctx) != 1 {
		t.Error("no meeting_moved entry for the move")
	}

	alone := &mockCalOps{calID: "primary", events: []*googlecalendar.Event{organised("solo", ws.Add(time.Hour), time.Hour)}}
	if applied, _ := applyOne(t, db, ctx, alone, MoveProposal{EventID: "solo", ProposedStart: p.ProposedStart, ProposedEnd: p.ProposedEnd}); len(applied) != 1 || len(alone.calls()) != 0 {
		t.Errorf("meeting with no attendee: applied %v after %d calls, want moved with none", applied, len(alone.calls()))
	}
}

// AC-34: the meeting's own current time is excluded from the check.
func TestPAC133_AC34_ApplyExcludesTheMeetingsOwnTime(t *testing.T) {
	db := openTestDB(t)
	ctx, _, ws := compressionDay(t, db)
	current := slot(ws.Add(time.Hour), time.Hour) // "10:00-11:00"
	p := MoveProposal{EventID: "m", ProposedStart: current.Start.Add(30 * time.Minute), ProposedEnd: current.End.Add(30 * time.Minute)}
	ownOnly := &mockCalOps{calID: "primary", events: []*googlecalendar.Event{organised("m", current.Start, time.Hour, "b@corp.example")},
		fb: map[string]calendar.FreeBusyEntry{"b@corp.example": readable(current)}}
	if applied, failed := applyOne(t, db, ctx, ownOnly, p); len(applied) != 1 {
		t.Errorf("a shift over its own time: applied %v failed %v, want moved", applied, failed)
	}
	alsoBusy := &mockCalOps{calID: "primary", events: []*googlecalendar.Event{organised("m", current.Start, time.Hour, "b@corp.example")},
		fb: map[string]calendar.FreeBusyEntry{"b@corp.example": readable(current, slot(current.End, 30*time.Minute))}}
	if applied, failed := applyOne(t, db, ctx, alsoBusy, p); len(applied) != 0 || len(failed) != 1 || failed[0] != "m: attendee conflict at proposed time" {
		t.Errorf("busy 11:00-11:30 too: applied %v failed %v, want the conflict", applied, failed)
	}
}

// AC-36: a preview whose free/busy calls outlast the request budget answers
// the shared 503, never a 200 with fewer proposals.
func TestPAC133_AC36_PreviewPastTheBudgetIs503(t *testing.T) {
	db := openTestDB(t)
	ctx, day, ws := compressionDay(t, db)
	mock := &mockCalOps{calID: "primary", fbBlock: true, events: []*googlecalendar.Event{organised("m", ws.Add(5*time.Hour), time.Hour, "b@corp.example")},
		fb: map[string]calendar.FreeBusyEntry{"b@corp.example": readable()}}
	eng := &CompressionEngine{DB: db, calOps: mock}
	handler := budget.Middleware(150*time.Millisecond, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res, err := eng.SuggestForDay(r.Context(), day)
		if err != nil {
			http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]*CompressionResult{res})
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/schedule/compress", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || strings.TrimSpace(rec.Body.String()) != `{"error":"request timed out"}` {
		t.Errorf("answer = %d %s, want the shared 503", rec.Code, rec.Body.String())
	}
	if len(mock.calls()) == 0 {
		t.Error("the free/busy check was never reached")
	}
}
