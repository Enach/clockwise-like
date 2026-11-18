package engine

// PAC-122 phase 2, batch 5 (habits, analytics, recap; light rows): a failed
// calendar read places no habit that day (SF-013), a habit that moves or is
// missed has its old event deleted (SF-014), a habit is never stored as
// scheduled without its event and every failure reaches the caller
// (SF-015), analytics never stores a week computed from a failed read
// (SF-017), and the daily recap neither reads a failed query as "a clear
// calendar" (SF-046) nor returns silently or sends twice (SF-082).

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/lifecycle/lifecycletest"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	googlecalendar "google.golang.org/api/calendar/v3"
	"google.golang.org/api/googleapi"
)

// b5Monday is the run's "now": a Monday, so the 14-day horizon holds two
// Mondays (2026-11-02 and 2026-11-09).
var b5Monday = time.Date(2026, 11, 2, 6, 0, 0, 0, time.UTC)

// b5Habit is a user (zone UTC) with one Monday habit, 30 minutes in
// 07:00-08:00.
func b5Habit(t *testing.T, db *sql.DB, email string) (uuid.UUID, *storage.Habit) {
	t.Helper()
	u, err := storage.UpsertUser(db, email, "B5", "", "google", email)
	if err != nil {
		t.Fatal(err)
	}
	s, err := storage.GetOrCreateSettingsByUser(db, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.Timezone = "UTC"
	if err := storage.SaveSettingsForUser(db, u.ID, s); err != nil {
		t.Fatal(err)
	}
	h, err := storage.CreateHabit(db, &storage.Habit{UserID: u.ID, Title: "Run", DurationMinutes: 30,
		DaysOfWeek: []int{1}, WindowStart: "07:00", WindowEnd: "08:00", Priority: 50, Color: "#5B7FFF"})
	if err != nil {
		t.Fatal(err)
	}
	return u.ID, h
}

func b5Event(id string, start time.Time, attendees bool) *googlecalendar.Event {
	ev := &googlecalendar.Event{
		Id:    id,
		Start: &googlecalendar.EventDateTime{DateTime: start.Format(time.RFC3339)},
		End:   &googlecalendar.EventDateTime{DateTime: start.Add(30 * time.Minute).Format(time.RFC3339)},
	}
	if attendees {
		ev.Attendees = []*googlecalendar.EventAttendee{{Email: "a@b5.test"}}
	}
	return ev
}

func b5Occurrences(t *testing.T, db *sql.DB, habit uuid.UUID) map[string]*storage.HabitOccurrence {
	t.Helper()
	occs, err := storage.ListHabitOccurrences(db, habit, b5Monday.AddDate(0, 0, -7), b5Monday.AddDate(0, 0, 21))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*storage.HabitOccurrence{}
	for _, o := range occs {
		out[o.ScheduledDate.Format("2006-01-02")] = o
	}
	return out
}

func b5Store(t *testing.T, db *sql.DB, habit uuid.UUID, day string, start time.Time, event string) {
	t.Helper()
	d, _ := time.Parse("2006-01-02", day)
	if _, err := storage.UpsertHabitOccurrence(db, &storage.HabitOccurrence{HabitID: habit, ScheduledDate: d,
		StartTime: start, EndTime: start.Add(30 * time.Minute), Status: "scheduled", CalendarEventID: event}); err != nil {
		t.Fatal(err)
	}
}

// SF-013: when the day's calendar read fails, nothing is placed that day
// (it was placed over the meetings it could not see) and the failure is
// the run's error.
func TestPAC122_SF013_FailedCalendarReadPlacesNothing(t *testing.T) {
	db := openTestDB(t)
	user, h := b5Habit(t, db, "sf013@pac122.test")
	mock := &mockCalOps{calID: "primary", listErr: errors.New("calendar unavailable")}
	err := (&HabitsEngine{DB: db, Clock: FixedClock{T: b5Monday}, calOps: mock}).ReoptimizeAll(context.Background(), user)
	if err == nil || !strings.Contains(err.Error(), "read calendar") || !strings.Contains(err.Error(), user.String()) {
		t.Fatalf("ReoptimizeAll = %v, want the failed calendar read naming the user", err)
	}
	if len(mock.createdEvts) != 0 {
		t.Fatalf("%d events created over an unread calendar", len(mock.createdEvts))
	}
	if occs := b5Occurrences(t, db, h.ID); len(occs) != 0 {
		t.Fatalf("occurrences stored over an unread calendar: %+v", occs)
	}
}

// SF-014: an occurrence a meeting now overlaps moves, and its old event is
// deleted (it was left on the calendar).
func TestPAC122_SF014_MovedOccurrenceDeletesItsOldEvent(t *testing.T) {
	db := openTestDB(t)
	user, h := b5Habit(t, db, "sf014-move@pac122.test")
	seven := time.Date(2026, 11, 2, 7, 0, 0, 0, time.UTC)
	b5Store(t, db, h.ID, "2026-11-02", seven, "evt-old")
	mock := &mockCalOps{calID: "primary", events: []*googlecalendar.Event{b5Event("meeting", seven, true)}}
	if err := (&HabitsEngine{DB: db, Clock: FixedClock{T: b5Monday}, calOps: mock}).ReoptimizeAll(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	if len(mock.deletedEvts) != 1 || mock.deletedEvts[0] != "evt-old" {
		t.Fatalf("deleted %q, want the old event evt-old", mock.deletedEvts)
	}
	got := b5Occurrences(t, db, h.ID)["2026-11-02"]
	if got == nil || got.Status != "scheduled" || !got.StartTime.Equal(seven.Add(30*time.Minute)) ||
		got.CalendarEventID == "" || got.CalendarEventID == "evt-old" {
		t.Fatalf("Monday occurrence = %+v, want scheduled at 07:30 with its new event", got)
	}
}

// SF-014: when the old event cannot be deleted, the row keeps its id as
// displaced (a later run retries) and the failure is the run's error.
func TestPAC122_SF014_FailedOldEventDeleteKeepsItsID(t *testing.T) {
	db := openTestDB(t)
	user, h := b5Habit(t, db, "sf014-fail@pac122.test")
	seven := time.Date(2026, 11, 2, 7, 0, 0, 0, time.UTC)
	b5Store(t, db, h.ID, "2026-11-02", seven, "evt-old")
	ops := &b2DeleteOps{
		mockCalOps: &mockCalOps{calID: "primary", events: []*googlecalendar.Event{b5Event("meeting", seven, true)}},
		deleteErr:  map[string]error{"evt-old": &googleapi.Error{Code: http.StatusInternalServerError, Message: "backend error"}},
	}
	err := (&HabitsEngine{DB: db, Clock: FixedClock{T: b5Monday}, calOps: ops}).ReoptimizeAll(context.Background(), user)
	if err == nil || !strings.Contains(err.Error(), "delete old event") || !strings.Contains(err.Error(), h.ID.String()) {
		t.Fatalf("ReoptimizeAll = %v, want the failed delete naming the habit", err)
	}
	got := b5Occurrences(t, db, h.ID)["2026-11-02"]
	if got == nil || got.Status != "displaced" || got.CalendarEventID != "evt-old" || !got.StartTime.Equal(seven) {
		t.Fatalf("Monday occurrence = %+v, want displaced, still pointing at evt-old", got)
	}
	for _, ev := range ops.createdEvts {
		if strings.HasPrefix(ev.Start.DateTime, "2026-11-02") {
			t.Fatalf("a second Monday event was created while evt-old stays: %s", ev.Start.DateTime)
		}
	}
}

// SF-014: an occurrence's own event is not a meeting it collides with, so a
// re-run leaves it where it is (it was displaced by itself on every run,
// creating a new event each time).
func TestPAC122_SF014_OwnEventIsNotACollision(t *testing.T) {
	db := openTestDB(t)
	user, h := b5Habit(t, db, "sf014-own@pac122.test")
	seven := time.Date(2026, 11, 2, 7, 0, 0, 0, time.UTC)
	b5Store(t, db, h.ID, "2026-11-02", seven, "evt-own")
	own := b5Event("evt-own", seven, false)
	own.Transparency = "opaque"
	mock := &mockCalOps{calID: "primary", events: []*googlecalendar.Event{own}}
	if err := (&HabitsEngine{DB: db, Clock: FixedClock{T: b5Monday}, calOps: mock}).ReoptimizeAll(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	if len(mock.deletedEvts) != 0 {
		t.Fatalf("deleted %q, want nothing", mock.deletedEvts)
	}
	got := b5Occurrences(t, db, h.ID)["2026-11-02"]
	if got == nil || got.CalendarEventID != "evt-own" || !got.StartTime.Equal(seven) || got.Status != "scheduled" {
		t.Fatalf("Monday occurrence = %+v, want it unchanged", got)
	}
	if len(mock.createdEvts) != 1 || !strings.HasPrefix(mock.createdEvts[0].Start.DateTime, "2026-11-09") {
		t.Fatalf("created %d events, want only the next Monday's", len(mock.createdEvts))
	}
}

// SF-015: a failed event create stores no "scheduled" occurrence without
// its event, and the failure is the run's error (it was nil).
func TestPAC122_SF015_FailedCreateStoresNoScheduledOccurrence(t *testing.T) {
	db := openTestDB(t)
	user, h := b5Habit(t, db, "sf015-create@pac122.test")
	mock := &mockCalOps{calID: "primary", events: []*googlecalendar.Event{}, createErr: errors.New("quota exceeded")}
	err := (&HabitsEngine{DB: db, Clock: FixedClock{T: b5Monday}, calOps: mock}).ReoptimizeAll(context.Background(), user)
	if err == nil || !strings.Contains(err.Error(), "create event") ||
		!strings.Contains(err.Error(), "2026-11-02") || !strings.Contains(err.Error(), "2026-11-09") {
		t.Fatalf("ReoptimizeAll = %v, want both Mondays' failed creates", err)
	}
	for day, o := range b5Occurrences(t, db, h.ID) {
		if o.Status == "scheduled" {
			t.Fatalf("%s stored as scheduled with event %q after the create failed", day, o.CalendarEventID)
		}
	}
}

// SF-015: when the occurrence cannot be stored, the event just created is
// deleted again (no row points at it) and the failure is the run's error.
func TestPAC122_SF015_FailedStoreRemovesTheNewEvent(t *testing.T) {
	db := openTestDB(t)
	user, _ := b5Habit(t, db, "sf015-store@pac122.test")
	if _, err := db.Exec(`CREATE FUNCTION pac122_b5_refuse() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'pac122 b5 refused'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER pac122_b5_refuse BEFORE INSERT OR UPDATE ON habit_occurrences
		FOR EACH ROW EXECUTE FUNCTION pac122_b5_refuse()`); err != nil {
		t.Fatal(err)
	}
	mock := &mockCalOps{calID: "primary", events: []*googlecalendar.Event{}}
	err := (&HabitsEngine{DB: db, Clock: FixedClock{T: b5Monday}, calOps: mock}).ReoptimizeAll(context.Background(), user)
	if err == nil || !strings.Contains(err.Error(), "store scheduled") {
		t.Fatalf("ReoptimizeAll = %v, want the failed store", err)
	}
	if len(mock.createdEvts) != 2 || len(mock.deletedEvts) != 2 {
		t.Fatalf("created %d, deleted %q; want both new events deleted again", len(mock.createdEvts), mock.deletedEvts)
	}
	for i, ev := range mock.createdEvts {
		if mock.deletedEvts[i] != ev.Id {
			t.Fatalf("deleted %q, want %q", mock.deletedEvts[i], ev.Id)
		}
	}
}

// SF-015: a user with no connected calendar is not a failure; occurrences
// are stored without an event, as before.
func TestPAC122_SF015_NoCalendarStillSchedulesWithoutEvents(t *testing.T) {
	db := openTestDB(t)
	user, h := b5Habit(t, db, "sf015-nocal@pac122.test")
	if err := (&HabitsEngine{DB: db, Clock: FixedClock{T: b5Monday}}).ReoptimizeAll(context.Background(), user); err != nil {
		t.Fatalf("ReoptimizeAll = %v, want nil for a user with no calendar", err)
	}
	occs := b5Occurrences(t, db, h.ID)
	if len(occs) != 2 {
		t.Fatalf("occurrences = %+v, want both Mondays", occs)
	}
	for day, o := range occs {
		if o.Status != "scheduled" || o.CalendarEventID != "" {
			t.Fatalf("%s = %+v, want scheduled with no event", day, o)
		}
	}
}

// SF-017: a week whose calendar read fails is an error and is not stored
// (it was stored as "zero meetings" and served from the cache for 24h).
func TestPAC122_SF017_FailedCalendarReadStoresNoWeek(t *testing.T) {
	db := openTestDB(t)
	user, _ := b5Habit(t, db, "sf017@pac122.test")
	eng := &AnalyticsEngine{DB: db, calOps: &mockCalOps{calID: "primary", listErr: errors.New("calendar unavailable")}}
	if _, err := eng.ForceCompute(context.Background(), user, b5Monday); err == nil || !strings.Contains(err.Error(), "read calendar") {
		t.Fatalf("ForceCompute = %v, want the failed calendar read", err)
	}
	week, err := storage.GetAnalyticsWeek(db, user, weekStartFor(b5Monday))
	if err != nil {
		t.Fatal(err)
	}
	if week != nil {
		t.Fatalf("a week was stored from a failed read: %+v", week)
	}

	// Once the calendar answers, the week is computed and stored.
	eng.calOps = &mockCalOps{calID: "primary", events: []*googlecalendar.Event{b5Event("m", b5Monday.Add(3*time.Hour), true)}}
	got, err := eng.ComputeWeek(context.Background(), user, b5Monday)
	if err != nil || got == nil || got.MeetingCount != 1 {
		t.Fatalf("ComputeWeek = %+v, %v; want the week with its meeting", got, err)
	}
}

// SF-017: an unreadable habit occurrence is an error, not a week with
// fewer habits.
func TestPAC122_SF017_FailedHabitReadStoresNoWeek(t *testing.T) {
	for table, want := range map[string]string{"habit_occurrences": "occurrences", "habits": "read habits"} {
		t.Run(table, func(t *testing.T) {
			db := openTestDB(t)
			user, _ := b5Habit(t, db, "sf017-"+table+"@pac122.test")
			if _, err := db.Exec(`ALTER TABLE ` + table + ` RENAME TO ` + table + `_pac122`); err != nil {
				t.Fatal(err)
			}
			eng := &AnalyticsEngine{DB: db, calOps: &mockCalOps{calID: "primary", events: []*googlecalendar.Event{}}}
			if _, err := eng.ForceCompute(context.Background(), user, b5Monday); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("ForceCompute = %v, want the failed habit read", err)
			}
			if week, err := storage.GetAnalyticsWeek(db, user, weekStartFor(b5Monday)); err != nil || week != nil {
				t.Fatalf("stored week = %+v, %v; want none", week, err)
			}
		})
	}
}

// SF-046: the recap's focus blocks are the recap day's, in the recap's zone
// (the day was cut in the database session's zone, so a 09:00 Paris block
// was missing from a Paris recap).
func TestPAC122_SF046_FocusBlocksAreTheRecapZonesDay(t *testing.T) {
	db := openTestDB(t)
	user, _ := b5Habit(t, db, "sf046-zone@pac122.test")
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}
	nine := time.Date(2026, 11, 2, 9, 0, 0, 0, paris)
	if err := storage.SaveFocusBlock(db, user, "evt-focus", "2026-11-02", nine, nine.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// The block of the day before, at 23:30 Paris, is not today's.
	late := time.Date(2026, 11, 1, 23, 30, 0, 0, paris)
	if err := storage.SaveFocusBlock(db, user, "evt-late", "2026-11-01", late, late.Add(20*time.Minute)); err != nil {
		t.Fatal(err)
	}
	blocks, err := (&DailyRecapService{DB: db}).fetchFocusBlocks(user, time.Date(2026, 11, 2, 0, 0, 0, 0, paris), paris)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || blocks[0].Start != "09:00" {
		t.Fatalf("focus blocks = %+v, want only the 09:00 Paris block", blocks)
	}
}

// SF-046: a failed read of the day's meetings sends nothing (the recap said
// "a clear calendar today") and is reported.
func TestPAC122_SF046_FailedMeetingReadSendsNothing(t *testing.T) {
	rec := lifecycletest.Record(t)
	svc, user, posts := pac122Recap(t, true)
	if _, err := svc.DB.Exec(`ALTER TABLE meeting_briefs RENAME TO meeting_briefs_pac122`); err != nil {
		t.Fatal(err)
	}
	svc.RunForUser(context.Background(), user, "R")
	if posts() != 0 {
		t.Fatalf("%d posts, want none", posts())
	}
	got := rec.Matching("daily-recap", "build message")
	if len(got) != 1 || got[0].Unit.ID != user.String() || !strings.Contains(got[0].Err.Error(), "read meetings") {
		t.Fatalf("reports %+v", rec.All())
	}
}

// SF-082: a failed read of whether today's recap was sent sends nothing (it
// read as "not sent", so a recap could go out twice) and is reported.
func TestPAC122_SF082_UnreadableSendRecordSendsNothing(t *testing.T) {
	rec := lifecycletest.Record(t)
	svc, user, posts := pac122Recap(t, true)
	if _, err := svc.DB.Exec(`ALTER TABLE recap_sends RENAME TO recap_sends_pac122`); err != nil {
		t.Fatal(err)
	}
	svc.RunForUser(context.Background(), user, "R")
	if posts() != 0 {
		t.Fatalf("%d posts, want none", posts())
	}
	got := rec.Matching("daily-recap", "read whether the recap was sent")
	if len(got) != 1 || got[0].Unit.ID != user.String() {
		t.Fatalf("reports %+v", rec.All())
	}
}

// SF-082: a recap already sent today is not sent again.
func TestPAC122_SF082_SentRecapIsNotResent(t *testing.T) {
	rec := lifecycletest.Record(t)
	svc, user, posts := pac122Recap(t, true)
	svc.RunForUser(context.Background(), user, "R")
	svc.RunForUser(context.Background(), user, "R")
	if posts() != 1 || len(rec.All()) != 0 {
		t.Fatalf("%d posts, reports %+v; want one post and no report", posts(), rec.All())
	}
}

// SF-082: a Slack connection that cannot be read stops the recap and is
// reported, not silent; no connection at all is not a failure. (A send time
// that is not HH:MM is reported too, but the column is a TIME, so no stored
// value reaches that branch.)
func TestPAC122_SF082_StoppedRecapIsReported(t *testing.T) {
	t.Run("slack connection read", func(t *testing.T) {
		rec := lifecycletest.Record(t)
		svc, user, posts := pac122Recap(t, true)
		if _, err := svc.DB.Exec(`ALTER TABLE workspace_connections RENAME TO workspace_connections_pac122`); err != nil {
			t.Fatal(err)
		}
		svc.RunForUser(context.Background(), user, "R")
		if got := rec.Matching("daily-recap", "read slack connection"); posts() != 0 || len(got) != 1 || got[0].Unit.ID != user.String() {
			t.Fatalf("%d posts, reports %+v", posts(), rec.All())
		}
	})
	t.Run("no slack connection is not a failure", func(t *testing.T) {
		rec := lifecycletest.Record(t)
		svc, user, posts := pac122Recap(t, true)
		if err := storage.DeleteWorkspaceConnection(svc.DB, user, "slack"); err != nil {
			t.Fatal(err)
		}
		svc.RunForUser(context.Background(), user, "R")
		if posts() != 0 || len(rec.All()) != 0 {
			t.Fatalf("%d posts, reports %+v; want neither", posts(), rec.All())
		}
	})
}

// b5FailWrites makes every habit_occurrences write fail (reads still work);
// the returned function lifts it.
func b5FailWrites(t *testing.T, db *sql.DB) (lift func()) {
	t.Helper()
	if _, err := db.Exec(`CREATE FUNCTION pac122_b5_refuse_w() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'pac122 b5 refused'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER pac122_b5_refuse_w BEFORE INSERT OR UPDATE ON habit_occurrences
		FOR EACH ROW EXECUTE FUNCTION pac122_b5_refuse_w()`); err != nil {
		t.Fatal(err)
	}
	return func() {
		if _, err := db.Exec(`DROP TRIGGER pac122_b5_refuse_w ON habit_occurrences`); err != nil {
			t.Fatal(err)
		}
	}
}

// SF-014 (review M1): when the old event was deleted but the new row could
// not be stored, the row still says scheduled with the deleted event. A
// later run sees the event is not on the calendar and re-places it (it kept
// the row, so the habit was no longer on the calendar).
func TestPAC122_SF014_ScheduledRowWithoutItsEventIsRepaired(t *testing.T) {
	db := openTestDB(t)
	user, h := b5Habit(t, db, "sf014-repair@pac122.test")
	seven := time.Date(2026, 11, 2, 7, 0, 0, 0, time.UTC)
	b5Store(t, db, h.ID, "2026-11-02", seven, "evt-old")
	lift := b5FailWrites(t, db)
	run1 := &mockCalOps{calID: "primary", events: []*googlecalendar.Event{b5Event("meeting", seven, true)}}
	if err := (&HabitsEngine{DB: db, Clock: FixedClock{T: b5Monday}, calOps: run1}).ReoptimizeAll(context.Background(), user); err == nil {
		t.Fatal("run 1 succeeded with every write refused")
	}
	if len(run1.deletedEvts) == 0 || run1.deletedEvts[0] != "evt-old" {
		t.Fatalf("run 1 deleted %q, want evt-old first", run1.deletedEvts)
	}
	if got := b5Occurrences(t, db, h.ID)["2026-11-02"]; got == nil || got.CalendarEventID != "evt-old" || got.Status != "scheduled" {
		t.Fatalf("setup: after run 1 the row is %+v, want still scheduled with evt-old", got)
	}
	lift()

	run2 := &mockCalOps{calID: "primary", events: []*googlecalendar.Event{}}
	if err := (&HabitsEngine{DB: db, Clock: FixedClock{T: b5Monday}, calOps: run2}).ReoptimizeAll(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	got := b5Occurrences(t, db, h.ID)["2026-11-02"]
	var monday *googlecalendar.Event
	for _, ev := range run2.createdEvts {
		if strings.HasPrefix(ev.Start.DateTime, "2026-11-02") {
			monday = ev
		}
	}
	if monday == nil || got == nil || got.Status != "scheduled" || got.CalendarEventID != monday.Id {
		t.Fatalf("run 2: row %+v, Monday event %+v; want the row scheduled with a new event", got, monday)
	}
}

// SF-014 (review M1): a legacy scheduled row with no event id is re-placed
// with an event once a calendar is connected.
func TestPAC122_SF014_ScheduledRowWithNoEventIDGetsOne(t *testing.T) {
	db := openTestDB(t)
	user, h := b5Habit(t, db, "sf014-noid@pac122.test")
	seven := time.Date(2026, 11, 2, 7, 0, 0, 0, time.UTC)
	b5Store(t, db, h.ID, "2026-11-02", seven, "")
	mock := &mockCalOps{calID: "primary", events: []*googlecalendar.Event{}}
	if err := (&HabitsEngine{DB: db, Clock: FixedClock{T: b5Monday}, calOps: mock}).ReoptimizeAll(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	if got := b5Occurrences(t, db, h.ID)["2026-11-02"]; got == nil || got.Status != "scheduled" || got.CalendarEventID == "" {
		t.Fatalf("row %+v, want scheduled with an event", got)
	}
	if len(mock.deletedEvts) != 0 {
		t.Fatalf("deleted %q, want nothing (there was no event)", mock.deletedEvts)
	}
}

// SF-014 (review minor 6): a displaced row that kept its event id (its
// delete failed earlier) has that event deleted and is re-placed by a later
// run.
func TestPAC122_SF014_DisplacedRowWithItsEventIsRetried(t *testing.T) {
	db := openTestDB(t)
	user, h := b5Habit(t, db, "sf014-retry@pac122.test")
	seven := time.Date(2026, 11, 2, 7, 0, 0, 0, time.UTC)
	if _, err := storage.UpsertHabitOccurrence(db, &storage.HabitOccurrence{HabitID: h.ID, ScheduledDate: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC),
		StartTime: seven, EndTime: seven.Add(30 * time.Minute), Status: "displaced", CalendarEventID: "evt-kept"}); err != nil {
		t.Fatal(err)
	}
	mock := &mockCalOps{calID: "primary", events: []*googlecalendar.Event{b5Event("evt-kept", seven, false)}}
	if err := (&HabitsEngine{DB: db, Clock: FixedClock{T: b5Monday}, calOps: mock}).ReoptimizeAll(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	if len(mock.deletedEvts) != 1 || mock.deletedEvts[0] != "evt-kept" {
		t.Fatalf("deleted %q, want evt-kept", mock.deletedEvts)
	}
	got := b5Occurrences(t, db, h.ID)["2026-11-02"]
	if got == nil || got.Status != "scheduled" || got.CalendarEventID == "" || got.CalendarEventID == "evt-kept" {
		t.Fatalf("row %+v, want scheduled with a new event", got)
	}
}

// SF-014 (review M2): a habit event carries its habit id, and an event with
// that marker is never a meeting, even when no row points at it.
func TestPAC122_SF014_HabitEventsAreMarkedAndNeverMeetings(t *testing.T) {
	db := openTestDB(t)
	user, h := b5Habit(t, db, "sf014-marker@pac122.test")
	seven := time.Date(2026, 11, 2, 7, 0, 0, 0, time.UTC)
	orphan := b5Event("evt-orphan", seven, false)
	orphan.ExtendedProperties = &googlecalendar.EventExtendedProperties{Private: map[string]string{habitEventMarker: h.ID.String()}}
	mock := &mockCalOps{calID: "primary", events: []*googlecalendar.Event{orphan}}
	if err := (&HabitsEngine{DB: db, Clock: FixedClock{T: b5Monday}, calOps: mock}).ReoptimizeAll(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	if len(mock.createdEvts) == 0 {
		t.Fatal("no event created")
	}
	for _, ev := range mock.createdEvts {
		if ev.ExtendedProperties == nil || ev.ExtendedProperties.Private[habitEventMarker] != h.ID.String() {
			t.Fatalf("created event %+v carries no habit marker", ev)
		}
	}
	if got := b5Occurrences(t, db, h.ID)["2026-11-02"]; got == nil || !got.StartTime.Equal(seven) {
		t.Fatalf("Monday row %+v, want 07:00 (the marked event is not busy)", got)
	}
}

// SF-046 (review minor): the recap day is the calendar day in the recap
// zone, 25h on a DST fall-back day and 23h on a spring-forward day, for both
// focus blocks and meetings (it was local midnight + 24h).
func TestPAC122_SF046_RecapDayFollowsDST(t *testing.T) {
	db := openTestDB(t)
	user, _ := b5Habit(t, db, "sf046-dst@pac122.test")
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}
	svc := &DailyRecapService{DB: db}
	settings, err := storage.GetOrCreateSettingsByUser(db, user)
	if err != nil {
		t.Fatal(err)
	}
	summary := func(day time.Time) string {
		t.Helper()
		blocks, err := svc.BuildMessage(context.Background(), user, "D", day.Add(8*time.Hour), settings)
		if err != nil {
			t.Fatal(err)
		}
		text, _ := blocks[2]["text"].(slackBlock)
		s, _ := text["text"].(string)
		return s
	}

	// Fall back: 2026-10-25 has 25 hours; 23:30 is still that day.
	fallBack := time.Date(2026, 10, 25, 0, 0, 0, 0, paris)
	late := time.Date(2026, 10, 25, 23, 30, 0, 0, paris)
	if err := storage.SaveFocusBlock(db, user, "evt-late", "2026-10-25", late, late.Add(20*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO meeting_briefs (calendar_event_id, user_id, generated_at) VALUES ('brief-late', $1, $2)`, user, late); err != nil {
		t.Fatal(err)
	}
	if blocks, err := svc.fetchFocusBlocks(user, fallBack, paris); err != nil || len(blocks) != 1 {
		t.Fatalf("fall-back day focus blocks = %+v, %v; want the 23:30 block", blocks, err)
	}
	if s := summary(fallBack); !strings.Contains(s, "*1 meeting*") {
		t.Fatalf("fall-back day summary %q, want the 23:30 meeting", s)
	}

	// Spring forward: 2026-03-29 has 23 hours; 00:30 the next day is not it.
	springForward := time.Date(2026, 3, 29, 0, 0, 0, 0, paris)
	early := time.Date(2026, 3, 30, 0, 30, 0, 0, paris)
	if err := storage.SaveFocusBlock(db, user, "evt-early", "2026-03-30", early, early.Add(20*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO meeting_briefs (calendar_event_id, user_id, generated_at) VALUES ('brief-early', $1, $2)`, user, early); err != nil {
		t.Fatal(err)
	}
	if blocks, err := svc.fetchFocusBlocks(user, springForward, paris); err != nil || len(blocks) != 0 {
		t.Fatalf("spring-forward day focus blocks = %+v, %v; want none", blocks, err)
	}
	if s := summary(springForward); !strings.Contains(s, "clear calendar") {
		t.Fatalf("spring-forward day summary %q, want no meeting", s)
	}
}
