package engine

// PAC-136 (follow-ups of PAC-122 batch 5): one habit reoptimize per user at
// a time (a Postgres advisory lock), habit events created under an id
// derived from (habit, day) so a create that already happened is adopted on
// Google's 409 rather than duplicated, the habits engine reading every page
// of a day, and the daily recap reading the Slack connection (and, through
// RunAll, the settings) only at the user's send minute.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/internal/lifecycle"
	"github.com/Enach/paceday/backend/internal/lifecycle/lifecycletest"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	googlecalendar "google.golang.org/api/calendar/v3"
	"google.golang.org/api/googleapi"
)

// pac136Cal is a calendar double that keeps state the way Google does: an
// insert keeps a caller-supplied id and refuses one already taken (by a live
// or a deleted event) with 409 duplicate; an update of a deleted event
// restores it; a list returns the live events of the window.
type pac136Cal struct {
	mu      sync.Mutex
	live    map[string]*googlecalendar.Event
	deleted map[string]bool
	inserts int
	dups    int
	updates int
	deletes int
	nextID  int
}

func newPAC136Cal() *pac136Cal {
	return &pac136Cal{live: map[string]*googlecalendar.Event{}, deleted: map[string]bool{}}
}

func (c *pac136Cal) calendarID() string { return "primary" }

func (c *pac136Cal) listEvents(_ context.Context, tMin, tMax time.Time) ([]*googlecalendar.Event, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*googlecalendar.Event, 0)
	for _, ev := range c.live {
		s, e := parseEventTime(ev)
		if s.Before(tMax) && e.After(tMin) {
			cp := *ev
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (c *pac136Cal) createEvent(_ context.Context, ev *googlecalendar.Event) (*googlecalendar.Event, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inserts++
	cp := *ev
	if cp.Id == "" {
		c.nextID++
		cp.Id = fmt.Sprintf("random%04d", c.nextID)
	}
	if _, taken := c.live[cp.Id]; taken || c.deleted[cp.Id] {
		c.dups++
		return nil, &googleapi.Error{Code: http.StatusConflict, Message: "The requested identifier already exists.",
			Errors: []googleapi.ErrorItem{{Reason: "duplicate"}}}
	}
	c.live[cp.Id] = &cp
	out := cp
	return &out, nil
}

func (c *pac136Cal) updateEvent(_ context.Context, eventID string, ev *googlecalendar.Event) (*googlecalendar.Event, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updates++
	_, live := c.live[eventID]
	if !live && !c.deleted[eventID] {
		return nil, &googleapi.Error{Code: http.StatusNotFound}
	}
	delete(c.deleted, eventID)
	cp := *ev
	cp.Id = eventID
	c.live[eventID] = &cp
	out := cp
	return &out, nil
}

func (c *pac136Cal) deleteEvent(_ context.Context, eventID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deletes++
	if _, ok := c.live[eventID]; !ok {
		return &googleapi.Error{Code: http.StatusGone}
	}
	delete(c.live, eventID)
	c.deleted[eventID] = true
	return nil
}

func (c *pac136Cal) getEvent(_ context.Context, eventID string) (*googlecalendar.Event, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ev, ok := c.live[eventID]; ok {
		cp := *ev
		return &cp, nil
	}
	return nil, &googleapi.Error{Code: http.StatusNotFound}
}

func (c *pac136Cal) getFreeBusy(context.Context, []string, time.Time, time.Time) (map[string]calendar.FreeBusyEntry, error) {
	return map[string]calendar.FreeBusyEntry{}, nil
}

// onDay returns the live events starting on day (YYYY-MM-DD, UTC).
func (c *pac136Cal) onDay(day string) []*googlecalendar.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*googlecalendar.Event
	for _, ev := range c.live {
		if strings.HasPrefix(ev.Start.DateTime, day) {
			out = append(out, ev)
		}
	}
	return out
}

func (c *pac136Cal) counts() (inserts, dups, updates, deletes int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inserts, c.dups, c.updates, c.deletes
}

var pac136Mondays = []string{"2026-11-02", "2026-11-09"}

// pac136AssertOneEventPerMonday checks each Monday has exactly one live
// event and that the stored row points at it.
func pac136AssertOneEventPerMonday(t *testing.T, cal *pac136Cal, rows map[string]*storage.HabitOccurrence) {
	t.Helper()
	for _, day := range pac136Mondays {
		evs := cal.onDay(day)
		row := rows[day]
		if len(evs) != 1 {
			t.Errorf("%s: %d events on the calendar, want exactly one", day, len(evs))
			continue
		}
		if row == nil || row.Status != "scheduled" || row.CalendarEventID != evs[0].Id {
			t.Errorf("%s: row %+v, want scheduled with the calendar's event %s", day, row, evs[0].Id)
		}
	}
}

// Item 1, the lock: a second run for the same user waits, in Postgres, for
// the first to finish, and the two together leave one event per (habit, day).
// The first run is held just after it takes the lock; the second must not
// get past its own lock while the first is held.
func TestPAC136_ReoptimizeRunsOnePerUser(t *testing.T) {
	db := openTestDB(t)
	user, h := b5Habit(t, db, "pac136-lock@pac136.test")
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
	second := make(chan struct{})
	go func() {
		errs <- eng.ReoptimizeAll(context.Background(), user)
		close(second)
	}()

	// The second run is queued behind the first's lock, in Postgres.
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case <-second:
			close(hold)
			t.Fatal("the second run finished while the first held the user's lock")
		default:
		}
		var waiting int
		if err := db.QueryRow(`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND classid = $1 AND NOT granted
			AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`,
			uint32(storage.HabitLockClass)).Scan(&waiting); err != nil {
			close(hold)
			t.Fatal(err)
		}
		if waiting == 1 {
			break
		}
		if time.Now().After(deadline) {
			close(hold)
			t.Fatal("the second run never waited for the user's lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if ins, _, _, _ := cal.counts(); ins != 0 {
		close(hold)
		t.Fatalf("%d calendar inserts while the first run was held, want none", ins)
	}
	close(hold)
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("run: %v", err)
		}
	}
	pac136AssertOneEventPerMonday(t, cal, b5Occurrences(t, db, h.ID))
	if ins, _, _, del := cal.counts(); ins != 2 || del != 0 {
		t.Fatalf("%d inserts, %d deletes; want one insert per Monday and no delete (the second run kept the first's)", ins, del)
	}

	// The lock is released: nothing is left held or waiting.
	var held int
	if err := db.QueryRow(`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND classid = $1`,
		uint32(storage.HabitLockClass)).Scan(&held); err != nil || held != 0 {
		t.Fatalf("%d advisory locks of the class left, %v; want none", held, err)
	}
}

// Item 1, a cancelled wait: a run that cannot get the lock before its
// context ends fails without touching the calendar.
func TestPAC136_ReoptimizeWaitEndsWithContext(t *testing.T) {
	db := openTestDB(t)
	user, _ := b5Habit(t, db, "pac136-cancel@pac136.test")
	release, err := storage.LockHabitReoptimize(context.Background(), db, user)
	if err != nil {
		t.Fatal(err)
	}
	cal := newPAC136Cal()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err = (&HabitsEngine{DB: db, Clock: FixedClock{T: b5Monday}, calOps: cal}).ReoptimizeAll(ctx, user)
	if err == nil || !strings.Contains(err.Error(), "habit lock") {
		t.Fatalf("ReoptimizeAll = %v, want the lock wait's failure", err)
	}
	if ins, _, _, _ := cal.counts(); ins != 0 {
		t.Fatalf("%d inserts, want none", ins)
	}
	if err := release(); err != nil {
		t.Fatalf("release: %v", err)
	}
}

// Item 1, idempotent creates: a run that created a day's event and stopped
// before storing it (a crash) left the event with the (habit, day) id and no
// row. The next run adopts it on Google's 409 instead of adding a second.
func TestPAC136_CreatedButUnstoredEventIsAdopted(t *testing.T) {
	db := openTestDB(t)
	user, h := b5Habit(t, db, "pac136-adopt@pac136.test")
	cal := newPAC136Cal()
	monday := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	seven := time.Date(2026, 11, 2, 7, 0, 0, 0, time.UTC)
	left := b5Event(habitEventID(h.ID, monday), seven, false)
	left.ExtendedProperties = &googlecalendar.EventExtendedProperties{Private: map[string]string{habitEventMarker: h.ID.String()}}
	cal.live[left.Id] = left

	if err := (&HabitsEngine{DB: db, Clock: FixedClock{T: b5Monday}, calOps: cal}).ReoptimizeAll(context.Background(), user); err != nil {
		t.Fatalf("ReoptimizeAll = %v", err)
	}
	pac136AssertOneEventPerMonday(t, cal, b5Occurrences(t, db, h.ID))
	if got := b5Occurrences(t, db, h.ID)["2026-11-02"]; got == nil || got.CalendarEventID != left.Id {
		t.Fatalf("Monday row %+v, want the adopted event %s", got, left.Id)
	}
	if _, dups, updates, _ := cal.counts(); dups != 1 || updates != 1 {
		t.Fatalf("%d duplicates, %d updates; want the 409 answered by one update", dups, updates)
	}
}

// Item 1, idempotent creates: a habit that moves within its day has its
// event deleted and created again under the same (habit, day) id. Google
// keeps a deleted id taken, so the create is a 409 and the deleted event is
// restored at the new time.
func TestPAC136_MovedHabitRestoresItsDeletedEvent(t *testing.T) {
	db := openTestDB(t)
	user, h := b5Habit(t, db, "pac136-move@pac136.test")
	cal := newPAC136Cal()
	eng := &HabitsEngine{DB: db, Clock: FixedClock{T: b5Monday}, calOps: cal}
	if err := eng.ReoptimizeAll(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	monday := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	id := habitEventID(h.ID, monday)
	if got := b5Occurrences(t, db, h.ID)["2026-11-02"]; got == nil || got.CalendarEventID != id {
		t.Fatalf("Monday row %+v, want the (habit, day) id %s", got, id)
	}

	// A meeting now takes 07:00.
	cal.live["meeting"] = b5Event("meeting", time.Date(2026, 11, 2, 7, 0, 0, 0, time.UTC), true)
	if err := eng.ReoptimizeAll(context.Background(), user); err != nil {
		t.Fatalf("second run = %v", err)
	}
	got := b5Occurrences(t, db, h.ID)["2026-11-02"]
	if got == nil || got.Status != "scheduled" || got.CalendarEventID != id || !got.StartTime.Equal(time.Date(2026, 11, 2, 7, 30, 0, 0, time.UTC)) {
		t.Fatalf("Monday row %+v, want scheduled at 07:30 with %s", got, id)
	}
	var habitEvents int
	for _, ev := range cal.onDay("2026-11-02") {
		if ev.Id != "meeting" {
			habitEvents++
			if ev.Id != id || ev.Start.DateTime != "2026-11-02T07:30:00Z" || ev.Status != "confirmed" {
				t.Fatalf("habit event %+v, want %s restored at 07:30", ev, id)
			}
		}
	}
	if habitEvents != 1 {
		t.Fatalf("%d habit events on Monday, want 1", habitEvents)
	}
}

// Item 1: the (habit, day) id is a valid Google event id, stable, and
// distinct per habit and per day.
func TestPAC136_HabitEventIDIsValidAndDeterministic(t *testing.T) {
	valid := regexp.MustCompile(`^[a-v0-9]+$`)
	h1, h2 := uuid.New(), uuid.New()
	d1 := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	d2 := d1.AddDate(0, 0, 1)
	a := habitEventID(h1, d1)
	if !valid.MatchString(a) || len(a) < 5 || len(a) > 1024 {
		t.Fatalf("id %q is not 5-1024 characters of [a-v0-9]", a)
	}
	if habitEventID(h1, d1) != a {
		t.Fatal("the id is not deterministic")
	}
	if habitEventID(h1, d2) == a || habitEventID(h2, d1) == a {
		t.Fatal("two (habit, day) pairs share an id")
	}
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}
	if habitEventID(h1, time.Date(2026, 11, 2, 0, 0, 0, 0, paris)) != a {
		t.Fatal("the id depends on more than the calendar date")
	}
}

// Item 2: the habits engine reads every page of a day. The habit's own event
// on the second page is kept (it was deleted and re-created on every run).
func TestPAC136_HabitEventOnALaterPageIsKept(t *testing.T) {
	db := openTestDB(t)
	user, h := b5Habit(t, db, "pac136-pages@pac136.test")
	seven := map[string]time.Time{}
	for i, day := range pac136Mondays {
		start := time.Date(2026, 11, 2+7*i, 7, 0, 0, 0, time.UTC)
		seven[day] = start
		b5Store(t, db, h.ID, day, start, "evtown"+strings.ReplaceAll(day, "-", ""))
	}

	var mu sync.Mutex
	var writes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			mu.Lock()
			writes = append(writes, r.Method+" "+r.URL.Path)
			mu.Unlock()
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"code":500,"message":"unexpected write"}}`))
			return
		}
		day := strings.SplitN(r.URL.Query().Get("timeMin"), "T", 2)[0]
		start, mine := seven[day]
		if !mine {
			_, _ = w.Write([]byte(`{"items":[]}`))
			return
		}
		if r.URL.Query().Get("pageToken") == "" {
			// The first page: a meeting elsewhere in the day, and more to come.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items":         []*googlecalendar.Event{b5Event("meeting"+strings.ReplaceAll(day, "-", ""), start.Add(4*time.Hour), true)},
				"nextPageToken": "page2",
			})
			return
		}
		own := b5Event("evtown"+strings.ReplaceAll(day, "-", ""), start, false)
		own.ExtendedProperties = &googlecalendar.EventExtendedProperties{Private: map[string]string{habitEventMarker: h.ID.String()}}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []*googlecalendar.Event{own}})
	}))
	t.Cleanup(srv.Close)
	calendar.Configure(config.CalendarEndpoint{BaseURL: srv.URL + config.CalendarAPIRoot, Guard: true})
	t.Cleanup(func() { calendar.Configure(config.ProductionCalendarEndpoint()) })
	client, err := calendar.NewClient(context.Background(), oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "T"}))
	if err != nil {
		t.Fatal(err)
	}

	if err := (&HabitsEngine{DB: db, Clock: FixedClock{T: b5Monday}, calOps: realOps{c: client}}).ReoptimizeAll(context.Background(), user); err != nil {
		t.Fatalf("ReoptimizeAll = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(writes) != 0 {
		t.Fatalf("calendar writes %v, want none (each habit's event is on page 2)", writes)
	}
	rows := b5Occurrences(t, db, h.ID)
	for _, day := range pac136Mondays {
		if got := rows[day]; got == nil || got.Status != "scheduled" || got.CalendarEventID != "evtown"+strings.ReplaceAll(day, "-", "") {
			t.Fatalf("%s row %+v, want kept with its event", day, got)
		}
	}
}

// Item 3: outside the send minute a failing Slack-connection read is not
// read, so not reported (it was, once per user per minute during an
// outage); at the send minute it is still reported.
func TestPAC136_RecapSlackReadOnlyAtSendTime(t *testing.T) {
	rec := lifecycletest.Record(t)
	svc, user, posts := pac122Recap(t, true)
	if _, err := svc.DB.Exec(`ALTER TABLE workspace_connections RENAME TO workspace_connections_pac136`); err != nil {
		t.Fatal(err)
	}
	sendMinute := svc.Clock
	svc.Clock = FixedClock{T: time.Date(2026, 11, 2, 8, 31, 0, 0, time.UTC)}
	svc.RunForUser(context.Background(), user, "R")
	if got := rec.All(); len(got) != 0 {
		t.Fatalf("reports outside the send minute: %+v; want none", got)
	}
	svc.Clock = sendMinute
	svc.RunForUser(context.Background(), user, "R")
	got := rec.Matching("daily-recap", "read slack connection")
	if posts() != 0 || len(got) != 1 || got[0].Unit.ID != user.String() {
		t.Fatalf("%d posts, reports %+v; want the failed read reported once at the send minute", posts(), rec.All())
	}
	if strings.Contains(got[0].Err.Error(), "@") {
		t.Fatalf("report %q carries an address, want ids only", got[0].Err)
	}
}

// Item 3: RunAll runs a user only at their send minute, so a failing
// settings read is reported then, once, not every minute.
func TestPAC136_RecapSettingsReadReportedOnlyAtSendTime(t *testing.T) {
	rec := lifecycletest.Record(t)
	svc, user, posts := pac122Recap(t, true)
	// The per-user settings read fails; the list of recap users still reads.
	if _, err := svc.DB.Exec(`ALTER TABLE settings RENAME COLUMN work_start TO work_start_pac136`); err != nil {
		t.Fatal(err)
	}
	run := func(at time.Time) {
		svc.Clock = FixedClock{T: at}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		svc.RunAll(ctx)
		lifecycle.Default().Wait(context.Background())
	}
	for _, minute := range []int{27, 28, 29, 31} {
		run(time.Date(2026, 11, 2, 8, minute, 0, 0, time.UTC))
	}
	if got := rec.All(); len(got) != 0 {
		t.Fatalf("reports outside the send minute: %+v; want none", got)
	}
	run(time.Date(2026, 11, 2, 8, 30, 0, 0, time.UTC))
	got := rec.Matching("daily-recap", "read settings")
	if posts() != 0 || len(got) != 1 || got[0].Unit.ID != user.String() {
		t.Fatalf("%d posts, reports %+v; want the failed read reported once at the send minute", posts(), rec.All())
	}
}

// Item 3: the gate's own rule: the user's zone (UTC when unknown) and an
// HH:MM send time; a send time that is not HH:MM is an error, passed on.
func TestPAC136_RecapDue(t *testing.T) {
	at := time.Date(2026, 11, 2, 7, 30, 0, 0, time.UTC) // 08:30 in Paris
	cases := []struct {
		zone, send string
		due, err   bool
	}{
		{"Europe/Paris", "08:30", true, false},
		{"Europe/Paris", "07:30", false, false},
		{"UTC", "07:30", true, false},
		{"Not/AZone", "07:30", true, false},
		{"", "07:30", true, false},
		{"UTC", "", false, true},
	}
	for _, c := range cases {
		_, due, err := recapDue(at, c.zone, c.send)
		if due != c.due || (err != nil) != c.err {
			t.Errorf("recapDue(%q, %q) = %v, %v; want due=%v err=%v", c.zone, c.send, due, err, c.due, c.err)
		}
	}
}
