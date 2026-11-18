package engine

// PAC-122 phase 2, batch 2 (calendar, focus, personal calendars; light
// rows): a failed calendar delete no longer drops the row that tracks the
// event (SF-018, SF-020), a failed read is no longer a default that creates
// overlapping or extra events (SF-019, SF-021), auto-decline no longer runs
// in UTC for a user without a usable zone (SF-006), blockers carry a marker
// (SF-035) and an unreadable feed event no longer deletes its blocker
// (SF-044).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	googlecalendar "google.golang.org/api/calendar/v3"
	"google.golang.org/api/googleapi"
)

// b2DeleteOps is mockCalOps whose deletes answer per event id.
type b2DeleteOps struct {
	*mockCalOps
	deleteErr map[string]error
}

func (m *b2DeleteOps) deleteEvent(ctx context.Context, id string) error {
	if err := m.deleteErr[id]; err != nil {
		return err
	}
	return m.mockCalOps.deleteEvent(ctx, id)
}

// SF-018: a block whose calendar event could not be deleted keeps its row
// (the call fails, a retry deletes it); an event already gone (404/410) is
// treated as deleted.
func TestPAC122_SF018_ClearWeekKeepsRowWhenEventDeleteFails(t *testing.T) {
	db := openTestDB(t)
	owner, err := storage.UpsertUser(db, "sf018@pac122.test", "SF018", "", "google", "sf018")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	for i, id := range []string{"evt-fails", "evt-gone", "evt-ok"} {
		s := start.Add(time.Duration(i) * 2 * time.Hour)
		if err := storage.SaveFocusBlock(db, owner.ID, id, "2025-01-06", s, s.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	ops := &b2DeleteOps{mockCalOps: &mockCalOps{calID: "primary"}, deleteErr: map[string]error{
		"evt-fails": &googleapi.Error{Code: http.StatusInternalServerError, Message: "backend error"},
		"evt-gone":  &googleapi.Error{Code: http.StatusGone, Message: "deleted"},
	}}
	eng := &FocusTimeEngine{DB: db, calOps: ops}
	ctx := context.WithValue(context.Background(), auth.UserIDKey, owner.ID)
	monday := time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC)

	if _, err := eng.ClearWeek(ctx, monday); err == nil || !strings.Contains(err.Error(), "evt-fails") {
		t.Fatalf("ClearWeek = %v, want the failed event delete", err)
	}
	left, err := storage.ListFocusBlocksForUserWeek(db, owner.ID, monday)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].GoogleEventID != "evt-fails" {
		t.Fatalf("rows left = %+v, want only evt-fails (its event is still on the calendar)", left)
	}

	// The retry finds the block and deletes it once the calendar answers.
	delete(ops.deleteErr, "evt-fails")
	n, err := eng.ClearWeek(ctx, monday)
	if err != nil || n != 1 {
		t.Fatalf("retry = %d, %v; want 1 cleared", n, err)
	}
	if left, _ := storage.ListFocusBlocksForUserWeek(db, owner.ID, monday); len(left) != 0 {
		t.Fatalf("rows left after the retry = %+v", left)
	}
}

// SF-019: when the day's existing focus minutes cannot be read, the day is
// an error and gets no new block (read as 0, its existing blocks, which are
// transparent, were not seen as busy and new ones were placed over them).
func TestPAC122_SF019_UnreadableFocusMinutesCreateNothingThatDay(t *testing.T) {
	db := openTestDB(t)
	user, err := storage.UpsertUser(db, "sf019@pac122.test", "SF019", "", "google", "sf019")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO settings (user_id, work_start, work_end, timezone, focus_min_block_minutes, focus_max_block_minutes, focus_daily_target_minutes, updated_at)
		VALUES ($1, '09:00', '17:00', 'UTC', 30, 120, 120, NOW())`, user.ID); err != nil {
		t.Fatal(err)
	}
	// A stored Monday block whose length overflows the minutes total: the
	// day's read fails (integer out of range) while every other read works.
	far := time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := storage.SaveFocusBlock(db, user.ID, "evt-huge", "2025-01-06", far, far.AddDate(9998, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.FocusMinutesForDay(db, user.ID, "2025-01-06"); err == nil {
		t.Fatal("setup: the Monday read did not fail")
	}
	mock := &mockCalOps{calID: "primary", events: []*googlecalendar.Event{}}
	result, err := (&FocusTimeEngine{DB: db, calOps: mock}).RunForUser(context.Background(), user.ID, time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range mock.createdEvts {
		if strings.HasPrefix(ev.Start.DateTime, "2025-01-06") {
			t.Fatalf("a block was created on the unreadable Monday: %s", ev.Start.DateTime)
		}
	}
	found := false
	for _, e := range result.Errors {
		if strings.HasPrefix(e, "2025-01-06: read existing focus minutes") {
			found = true
		}
	}
	if !found {
		t.Fatalf("errors = %q, want the Monday read failure", result.Errors)
	}
	if len(mock.createdEvts) == 0 {
		t.Fatal("no other day was scheduled: the failure must stay on its day")
	}
}

// SF-006: a user whose zone is empty or cannot be loaded is skipped with an
// error naming the user (never the zone) and the calendar is not touched.
func TestPAC122_SF006_AutoDeclineSkipsUserWithoutUsableZone(t *testing.T) {
	for _, zone := range []string{"", "Mars/Olympus_Mons"} {
		t.Run(fmt.Sprintf("zone=%q", zone), func(t *testing.T) {
			pac81Scale(t)
			db := openTestDB(t)
			g := newGoogleStub(t)
			user, _ := pac81User(t, db, "sf006-"+uuid.NewString()[:6]+"@pac122.test")
			if _, err := storage.GetOrCreateSettingsByUser(db, user); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE settings SET auto_decline_outside_working_hours = true, timezone = $2 WHERE user_id = $1`, user, zone); err != nil {
				t.Fatal(err)
			}
			n, err := (&AutoDeclineService{DB: db, OAuthConfig: &oauth2.Config{}}).RunForUser(context.Background(), user, time.Now())
			if err == nil || n != 0 || !strings.Contains(err.Error(), user.String()) || !strings.Contains(err.Error(), "timezone") {
				t.Fatalf("RunForUser = %d, %v; want the user skipped with an error", n, err)
			}
			if zone != "" && strings.Contains(err.Error(), zone) {
				t.Fatalf("error carries the zone: %v", err)
			}
			g.mu.Lock()
			defer g.mu.Unlock()
			if len(g.lists) != 0 {
				t.Fatalf("%d calendar reads for a skipped user", len(g.lists))
			}
		})
	}
}

// b2Calendar stands in for the work calendar of the personal-sync tests:
// it records created events and deletes, and answers deletes with
// deleteStatus (0 = 204).
type b2Calendar struct {
	mu           sync.Mutex
	created      []googlecalendar.Event
	deletes      []string
	deleteStatus int
}

func newB2Calendar(t *testing.T) *b2Calendar {
	t.Helper()
	c := &b2Calendar{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			var ev googlecalendar.Event
			_ = json.NewDecoder(r.Body).Decode(&ev)
			ev.Id = fmt.Sprintf("work-%d", len(c.created)+1)
			c.created = append(c.created, ev)
			_ = json.NewEncoder(w).Encode(ev)
		case http.MethodDelete:
			c.deletes = append(c.deletes, r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
			if c.deleteStatus != 0 {
				w.WriteHeader(c.deleteStatus)
				_, _ = w.Write([]byte(fmt.Sprintf(`{"error":{"code":%d,"message":"stub"}}`, c.deleteStatus)))
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			_, _ = w.Write([]byte(`{"items":[]}`))
		}
	}))
	t.Cleanup(srv.Close)
	calendar.Configure(config.CalendarEndpoint{BaseURL: srv.URL + config.CalendarAPIRoot, Guard: true})
	t.Cleanup(func() { calendar.Configure(config.ProductionCalendarEndpoint()) })
	return c
}

func (c *b2Calendar) setDeleteStatus(code int) {
	c.mu.Lock()
	c.deleteStatus = code
	c.mu.Unlock()
}

func (c *b2Calendar) counts() (created, deletes int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.created), len(c.deletes)
}

// b2Personal is a user with a work calendar and a webcal personal calendar
// serving ics; it returns the user's context and the calendar id.
func b2Personal(t *testing.T, db storage.DBTX, ics string) (context.Context, int64) {
	t.Helper()
	user, _ := pac81User(t, db, "b2-"+uuid.NewString()[:6]+"@pac122.test")
	if _, err := storage.GetOrCreateSettingsByUser(db, user); err != nil {
		t.Fatal(err)
	}
	feed := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//pac122//b2//EN\r\n" + ics + "END:VCALENDAR\r\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(feed)) }))
	t.Cleanup(srv.Close)
	id, err := storage.InsertPersonalCalendar(db, &storage.PersonalCalendar{UserID: user, Provider: "webcal", Name: "Home", URL: srv.URL, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return context.WithValue(context.Background(), auth.UserIDKey, user), id
}

// b2Event is one VEVENT at day+from..day+to (UTC).
func b2Event(uid string, day time.Time, from, to time.Duration) string {
	return fmt.Sprintf("BEGIN:VEVENT\r\nUID:%s\r\nSUMMARY:Dentist\r\nDTSTAMP:20250101T000000Z\r\nDTSTART:%s\r\nDTEND:%s\r\nEND:VEVENT\r\n",
		uid, day.Add(from).Format("20060102T150405Z"), day.Add(to).Format("20060102T150405Z"))
}

func blockerIDs(t *testing.T, db storage.DBTX, cal int64) []string {
	t.Helper()
	bl, err := storage.ListPersonalBlockers(db, cal)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, b := range bl {
		out = append(out, b.PersonalEventID)
	}
	return out
}

// SF-020: a stale blocker whose work-calendar event could not be deleted
// keeps its row and the sync fails; the next sync deletes it. An event
// already gone (404) counts as deleted.
func TestPAC122_SF020_FailedBlockerDeleteKeepsTheRow(t *testing.T) {
	pac81Scale(t)
	db := openTestDB(t)
	c := newB2Calendar(t)
	ctx, cal := b2Personal(t, db, "")
	if err := storage.UpsertPersonalBlocker(db, cal, "cancelled-evt", "work-stale"); err != nil {
		t.Fatal(err)
	}
	c.setDeleteStatus(http.StatusInternalServerError)
	if err := (&PersonalBlocker{DB: db}).Sync(ctx, cal); err == nil {
		t.Fatal("Sync succeeded with the blocker's event still on the work calendar")
	}
	if got := blockerIDs(t, db, cal); len(got) != 1 {
		t.Fatalf("blockers = %v, want the stale one kept (its event was not deleted)", got)
	}
	c.setDeleteStatus(http.StatusNotFound)
	if err := (&PersonalBlocker{DB: db}).Sync(ctx, cal); err != nil {
		t.Fatalf("Sync with the event already gone: %v", err)
	}
	if got := blockerIDs(t, db, cal); len(got) != 0 {
		t.Fatalf("blockers = %v, want none", got)
	}
	if _, deletes := c.counts(); deletes != 2 {
		t.Fatalf("%d deletes, want 2 (one per sync)", deletes)
	}
}

// SF-021 (settings): without readable settings the sync stops; before, a
// nil settings read as "always during work hours" and an event outside
// work hours got a blocker.
func TestPAC122_SF021_UnreadableSettingsCreateNoBlocker(t *testing.T) {
	pac81Scale(t)
	db := openTestDB(t)
	c := newB2Calendar(t)
	day := nextWeekday(time.Now())
	ctx, cal := b2Personal(t, db, b2Event("night", day, 2*time.Hour, 3*time.Hour))
	// This test's own database: the settings read fails, nothing else does.
	if _, err := db.Exec(`ALTER TABLE settings RENAME COLUMN work_start TO work_start_pac122`); err != nil {
		t.Fatal(err)
	}
	if err := (&PersonalBlocker{DB: db}).Sync(ctx, cal); err == nil || !strings.Contains(err.Error(), "read settings") {
		t.Fatalf("Sync = %v, want the settings read failure", err)
	}
	if created, _ := c.counts(); created != 0 {
		t.Fatalf("%d blockers created without settings", created)
	}
}

// SF-021 (lookup): a failed blocker lookup is not "no blocker yet": nothing
// is created (a duplicate of the existing blocker, before).
func TestPAC122_SF021_FailedBlockerLookupCreatesNoDuplicate(t *testing.T) {
	pac81Scale(t)
	db := openTestDB(t)
	c := newB2Calendar(t)
	day := nextWeekday(time.Now())
	ctx, cal := b2Personal(t, db, b2Event("standup", day, 10*time.Hour, 11*time.Hour))
	if err := storage.UpsertPersonalBlocker(db, cal, "standup", "work-1"); err != nil {
		t.Fatal(err)
	}
	prev := getPersonalBlocker
	getPersonalBlocker = func(storage.DBTX, int64, string) (*storage.PersonalBlocker, error) {
		return nil, errors.New("pac122 forced lookup failure")
	}
	t.Cleanup(func() { getPersonalBlocker = prev })
	if err := (&PersonalBlocker{DB: db}).Sync(ctx, cal); err == nil || !strings.Contains(err.Error(), "look up blocker") {
		t.Fatalf("Sync = %v, want the lookup failure", err)
	}
	if created, _ := c.counts(); created != 0 {
		t.Fatalf("%d blockers created over an existing one", created)
	}
}

// SF-035: a blocker is created with the private marker, and is recognised
// by it.
func TestPAC122_SF035_BlockerCarriesTheMarker(t *testing.T) {
	pac81Scale(t)
	db := openTestDB(t)
	c := newB2Calendar(t)
	day := nextWeekday(time.Now())
	ctx, cal := b2Personal(t, db, b2Event("gym", day, 10*time.Hour, 11*time.Hour))
	if err := (&PersonalBlocker{DB: db}).Sync(ctx, cal); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.created) != 1 {
		t.Fatalf("%d blockers created", len(c.created))
	}
	ev := c.created[0]
	if ev.ExtendedProperties == nil || ev.ExtendedProperties.Private[personalBlockerMarker] != "1" {
		t.Fatalf("blocker created without the marker: %+v", ev.ExtendedProperties)
	}
	if !IsPersonalBlocker(&ev) {
		t.Fatal("IsPersonalBlocker does not recognise a created blocker")
	}
}

func TestPAC122_SF035_IsPersonalBlocker(t *testing.T) {
	cases := []struct {
		name string
		ev   *googlecalendar.Event
		want bool
	}{
		{"marker", &googlecalendar.Event{Summary: "renamed", ExtendedProperties: &googlecalendar.EventExtendedProperties{Private: map[string]string{personalBlockerMarker: "1"}}}, true},
		{"legacy blocker (title and description)", &googlecalendar.Event{Summary: personalBlockerTitle, Description: personalBlockerDescription}, true},
		{"title only (a user's own event)", &googlecalendar.Event{Summary: personalBlockerTitle}, false},
		{"meeting", &googlecalendar.Event{Summary: "1:1"}, false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		if got := IsPersonalBlocker(tc.ev); got != tc.want {
			t.Errorf("%s: IsPersonalBlocker = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// SF-044: an event the feed carries but whose time cannot be read keeps its
// blocker (it was dropped, read as gone, and its blocker deleted), and an
// event given by DURATION gets one (it was skipped).
func TestPAC122_SF044_UnreadableEventKeepsItsBlockerAndDurationEventsBlock(t *testing.T) {
	pac81Scale(t)
	db := openTestDB(t)
	c := newB2Calendar(t)
	day := nextWeekday(time.Now())
	ics := "BEGIN:VEVENT\r\nUID:unreadable\r\nSUMMARY:x\r\nDTSTAMP:20250101T000000Z\r\nDTSTART:not-a-date\r\nDTEND:20250101T100000Z\r\nEND:VEVENT\r\n" +
		fmt.Sprintf("BEGIN:VEVENT\r\nUID:by-duration\r\nSUMMARY:x\r\nDTSTAMP:20250101T000000Z\r\nDTSTART:%s\r\nDURATION:PT1H\r\nEND:VEVENT\r\n", day.Add(13*time.Hour).Format("20060102T150405Z"))
	ctx, cal := b2Personal(t, db, ics)
	if err := storage.UpsertPersonalBlocker(db, cal, "unreadable", "work-kept"); err != nil {
		t.Fatal(err)
	}
	if err := (&PersonalBlocker{DB: db}).Sync(ctx, cal); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(blockerIDs(t, db, cal), ",")
	if !strings.Contains(got, "unreadable") || !strings.Contains(got, "by-duration") {
		t.Fatalf("blockers = %s, want the unreadable event's kept and the DURATION event's created", got)
	}
	if created, deletes := c.counts(); created != 1 || deletes != 0 {
		t.Fatalf("created %d, deleted %d; want 1 and 0", created, deletes)
	}
}

// SF-070: the engine's audit entries (meeting_scheduled, meeting_moved) are
// valid JSON whatever the title or event id.
func TestPAC122_SF070_EngineAuditDetailsAreJSON(t *testing.T) {
	db := openTestDB(t)
	user, err := storage.UpsertUser(db, "sf070-engine@pac122.test", "SF070", "", "google", "sf070-engine")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), auth.UserIDKey, user.ID)
	start := time.Date(2030, 1, 7, 10, 0, 0, 0, time.UTC)
	mock := &mockCalOps{calID: "primary", events: []*googlecalendar.Event{{Id: `move"me`}}}

	title := `Q3 "kick-off" \ review`
	if _, err := (&SmartScheduler{DB: db, calOps: mock}).CreateMeeting(ctx, ScheduleRequest{Title: title}, SuggestedSlot{Start: start, End: start.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	applied, failed, err := (&CompressionEngine{DB: db, calOps: mock}).Apply(ctx, []MoveProposal{{EventID: `move"me`, ProposedStart: start, ProposedEnd: start.Add(30 * time.Minute)}})
	if err != nil || len(applied) != 1 {
		t.Fatalf("Apply = %v, %v, %v", applied, failed, err)
	}
	entries, err := storage.ListAuditLog(db, user.ID, storage.AuditMaxLimit)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]map[string]string{}
	for _, e := range entries {
		var d map[string]string
		if err := json.Unmarshal([]byte(e.Details), &d); err != nil {
			t.Errorf("%s details are not JSON: %s", e.Action, e.Details)
			continue
		}
		seen[e.Action] = d
	}
	if seen["meeting_scheduled"]["title"] != title || seen["meeting_moved"]["event_id"] != `move"me` {
		t.Fatalf("entries = %+v", seen)
	}
}
