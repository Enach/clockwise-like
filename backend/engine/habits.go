package engine

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	googlecalendar "google.golang.org/api/calendar/v3"
)

// HabitTemplate is a predefined habit starter.
type HabitTemplate struct {
	Title           string `json:"title"`
	DurationMinutes int    `json:"duration_minutes"`
	DaysOfWeek      []int  `json:"days_of_week"`
	WindowStart     string `json:"window_start"`
	WindowEnd       string `json:"window_end"`
	Priority        int    `json:"priority"`
	Color           string `json:"color"`
}

// HabitTemplates is the set of 12 built-in starter templates.
var HabitTemplates = []HabitTemplate{
	{"Morning deep work", 90, []int{1, 2, 3, 4, 5}, "07:00", "10:00", 80, "#5B7FFF"},
	{"Afternoon deep work", 90, []int{1, 2, 3, 4, 5}, "13:00", "17:00", 80, "#5B7FFF"},
	{"Exercise", 45, []int{1, 2, 3, 4, 5}, "07:00", "09:00", 70, "#E9B949"},
	{"Lunch break", 60, []int{1, 2, 3, 4, 5}, "12:00", "14:00", 60, "#9B7AE0"},
	{"Email triage AM", 30, []int{1, 2, 3, 4, 5}, "09:00", "10:00", 50, "#5B7FFF"},
	{"Email triage PM", 30, []int{1, 2, 3, 4, 5}, "16:00", "17:30", 50, "#5B7FFF"},
	{"Learning / reading", 30, []int{1, 2, 3, 4, 5}, "17:00", "18:30", 40, "#E9B949"},
	{"Weekly planning", 60, []int{1}, "09:00", "11:00", 70, "#9B7AE0"},
	{"Weekly review", 30, []int{5}, "16:00", "18:00", 60, "#9B7AE0"},
	{"Team sync prep", 15, []int{1, 2, 3, 4, 5}, "09:00", "09:30", 55, "#5B7FFF"},
	{"Personal admin", 30, []int{5}, "15:00", "17:00", 40, "#E9B949"},
	{"Walk / movement", 20, []int{1, 2, 3, 4, 5}, "14:00", "15:30", 45, "#9B7AE0"},
}

// HabitsEngine schedules and maintains habit occurrences.
type HabitsEngine struct {
	DB          *sql.DB
	OAuthConfig *oauth2.Config
	Clock       Clock

	calOps calendarOps // test seam: used instead of the user's calendar when set
}

func (e *HabitsEngine) now() time.Time {
	if e.Clock != nil {
		return e.Clock.Now()
	}
	return SystemClock.Now()
}

// ReoptimizeAll schedules all active habits for the next 14 days.
//
// PAC-122 SF-015: every day that fails is part of the returned error
// (errors.Join, naming the user and the day; ids only), so the tracked unit
// that runs it reports it (SF-096). A day that fails places what it could,
// and the other days still run. A failed read of the habits, the user's
// settings or the user's calendar credential fails the whole run before
// anything is written. A user with no connected calendar is not a failure:
// occurrences are stored without a calendar event, as before.
//
// PAC-136: one run per user at a time, across replicas (a Postgres advisory
// lock, storage.LockHabitReoptimize): habit create, update and the
// reoptimize endpoint each start one, and two that overlapped each placed an
// event for the same (habit, day). A run waits for the one before it and
// then reads what that run stored.
//
// PAC-136 (review B1): before the lock a run passes the process's gate
// (habits_gate.go), holding no connection while it waits: one run per user
// runs and one waits (a further request is dropped: the waiting run reads
// the latest state), and at most K runs hold connections, so runs can never
// take the whole pool. A run, waits included, ends after habitRunTimeout,
// and every read takes its context, so a cancelled run frees its
// connections.
func (e *HabitsEngine) ReoptimizeAll(ctx context.Context, userID uuid.UUID) (err error) {
	ctx, cancel := context.WithTimeout(ctx, habitRunTimeout)
	defer cancel()
	leave, run, err := habitGateFor(e.DB).enter(ctx, userID)
	if err != nil {
		return fmt.Errorf("habits: user %s: wait for a run slot: %w", userID, err)
	}
	if !run {
		log.Printf("debug: habits: user %s: a reoptimize is already queued; request coalesced into it", userID)
		return nil
	}
	defer leave()
	release, err := storage.LockHabitReoptimize(ctx, e.DB, userID)
	if err != nil {
		return fmt.Errorf("habits: user %s: %w", userID, err)
	}
	defer func() {
		if relErr := release(); relErr != nil {
			err = errors.Join(err, fmt.Errorf("habits: user %s: %w", userID, relErr))
		}
	}()
	if hook, ok := habitRunLocked.get(); ok {
		hook(userID)
	}
	return e.reoptimizeLocked(ctx, userID)
}

// reoptimizeLocked is ReoptimizeAll's run, under the user's lock.
func (e *HabitsEngine) reoptimizeLocked(ctx context.Context, userID uuid.UUID) error {
	habits, err := storage.ListActiveHabitsByUserCtx(ctx, e.DB, userID)
	if err != nil {
		return fmt.Errorf("list habits: %w", err)
	}

	calOps, err := e.calendarFor(ctx, userID)
	if err != nil {
		return fmt.Errorf("habits: user %s: calendar: %w", userID, err)
	}

	settings, err := storage.GetOrCreateSettingsByUserCtx(ctx, e.DB, userID)
	if err != nil {
		return fmt.Errorf("habits: user %s: read settings: %w", userID, err)
	}
	now := e.now()
	if loc, locErr := time.LoadLocation(settings.Timezone); locErr == nil {
		now = now.In(loc)
	}
	var failed []error
	for i := 0; i < 14; i++ {
		day := now.AddDate(0, 0, i)
		if err := e.scheduleDay(ctx, userID, habits, settings, day, calOps); err != nil {
			failed = append(failed, fmt.Errorf("habits: user %s day %s: %w", userID, day.Format("2006-01-02"), err))
		}
	}
	return errors.Join(failed...)
}

// calendarFor returns the user's calendar, or nil when the user has no
// connected calendar. A failure to read the credential is an error: it used
// to read as "not connected", and occurrences were stored with no event.
func (e *HabitsEngine) calendarFor(ctx context.Context, userID uuid.UUID) (calendarOps, error) {
	if e.calOps != nil {
		return e.calOps, nil
	}
	token, err := LoadCalendarTokenCtx(ctx, e.DB, userID)
	if errors.Is(err, ErrCalendarNotConnected) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ts := auth.UserTokenSource(ctx, e.DB, e.OAuthConfig, userID, token)
	client, err := calendar.NewClient(ctx, ts)
	if err != nil {
		return nil, err
	}
	return realOps{c: client}, nil
}

// scheduleDay places habit occurrences for one day, processing habits in
// priority order. calOps is nil when the user has no connected calendar.
//
// PAC-122: a failed read places nothing that day (SF-013: a failed calendar
// read left the busy list empty and habits were created over meetings). An
// occurrence that moves or is missed has its old calendar event deleted
// first (SF-014: it was left behind). With a connected calendar an
// occurrence is stored as scheduled only with its calendar event, and every
// write is checked (SF-015). The returned error joins every failure of the
// day, by habit id.
func (e *HabitsEngine) scheduleDay(
	ctx context.Context,
	userID uuid.UUID,
	habits []*storage.Habit,
	settings *storage.Settings,
	day time.Time,
	calOps calendarOps,
) error {
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	dayEnd := dayStart.Add(24 * time.Hour)
	loc, err := time.LoadLocation(settings.Timezone)
	if err != nil || loc == nil {
		loc = time.UTC
	}
	weekday := habitWeekday(day)

	// Every read comes before any write: the day's existing occurrences
	// (whose own calendar events are theirs, not something they collide
	// with), the calendar, and the focus blocks.
	existing := make(map[uuid.UUID]*storage.HabitOccurrence, len(habits))
	ownEvents := map[string]struct{}{}
	for _, h := range habits {
		if !containsInt(h.DaysOfWeek, weekday) {
			continue
		}
		occ, err := e.existingOccurrence(ctx, h.ID, day)
		if err != nil {
			return fmt.Errorf("habit %s: read occurrence: %w", h.ID, err)
		}
		if occ != nil {
			existing[h.ID] = occ
			if occ.CalendarEventID != "" {
				ownEvents[occ.CalendarEventID] = struct{}{}
			}
		}
	}

	var calBusy []interval
	// onCalendar is every event id the day's calendar read returned: a
	// scheduled occurrence whose event is not among them is re-placed.
	onCalendar := map[string]struct{}{}
	if calOps != nil {
		listed, err := calOps.listEvents(ctx, dayStart, dayEnd)
		if err != nil {
			return fmt.Errorf("read calendar: %w", err)
		}
		events := make([]*googlecalendar.Event, 0, len(listed))
		for _, ev := range listed {
			onCalendar[ev.Id] = struct{}{}
			if _, own := ownEvents[ev.Id]; own || isHabitEvent(ev) {
				continue
			}
			events = append(events, ev)
		}
		for _, ev := range events {
			if ev.Transparency == "transparent" {
				continue
			}
			s, e2 := parseEventTime(ev)
			if !s.IsZero() && !e2.IsZero() {
				calBusy = append(calBusy, interval{start: s, end: e2})
			}
		}
		workStartRaw, workEndRaw, workEnabled := settings.WorkWindow(day.In(loc))
		if workEnabled {
			workStart := parseHHMM(workStartRaw, day, loc)
			workEnd := parseHHMM(workEndRaw, day, loc)
			calBusy = append(calBusy, ComputeBufferBlocks(events, settings, workStart, workEnd)...)
		}
	}

	lunchStartRaw, lunchEndRaw, lunchEnabled := settings.LunchWindow(day.In(loc))
	if lunchEnabled {
		calBusy = append(calBusy, interval{
			start: parseHHMM(lunchStartRaw, day, loc),
			end:   parseHHMM(lunchEndRaw, day, loc),
		})
	}

	// Focus blocks.
	focusBlocks, err := storage.ListFocusBlocksForUserWeekCtx(ctx, e.DB, userID, day)
	if err != nil {
		return fmt.Errorf("read focus blocks: %w", err)
	}
	for _, fb := range focusBlocks {
		if fb.StartTime.Year() == day.Year() &&
			fb.StartTime.Month() == day.Month() &&
			fb.StartTime.Day() == day.Day() {
			calBusy = append(calBusy, interval{start: fb.StartTime, end: fb.EndTime})
		}
	}

	// Habits already scheduled for this day — accumulated as we place each habit.
	placedBusy := calBusy
	var failed []error
	fail := func(h *storage.Habit, what string, err error) {
		failed = append(failed, fmt.Errorf("habit %s: %s: %w", h.ID, what, err))
	}

	for _, h := range habits {
		if !containsInt(h.DaysOfWeek, weekday) {
			continue
		}

		windowStart := parseHabitTime(h.WindowStart, day)
		windowEnd := parseHabitTime(h.WindowEnd, day)
		dur := time.Duration(h.DurationMinutes) * time.Minute
		prev := existing[h.ID]

		// Completed occurrences are user-confirmed and must survive reoptimization.
		if prev != nil && prev.Status == "completed" {
			placedBusy = append(placedBusy, interval{start: prev.StartTime, end: prev.EndTime})
			continue
		}

		// If already scheduled, verify it still fits. With a connected
		// calendar it also needs its event on the calendar: a row with no
		// event id, or one whose event the day's read did not return (a
		// store that failed after its old event was deleted, PAC-122 SF-014
		// review M1), is re-placed rather than kept.
		if prev != nil && prev.Status == "scheduled" {
			iv := interval{start: prev.StartTime, end: prev.EndTime}
			_, present := onCalendar[prev.CalendarEventID]
			hasEvent := calOps == nil || (prev.CalendarEventID != "" && present)
			if hasEvent && !overlapsMerged(iv, mergeIntervals(placedBusy)) {
				placedBusy = append(placedBusy, iv)
				continue
			}
		}

		// The occurrence is (re)placed: its old calendar event goes first
		// (SF-014). When that delete fails the event stays, so the row keeps
		// its id as displaced (the next run retries the delete) and its slot
		// stays busy for the other habits.
		if prev != nil && prev.CalendarEventID != "" && calOps != nil {
			if err := calOps.deleteEvent(ctx, prev.CalendarEventID); err != nil && !calendar.IsEventGone(err) {
				fail(h, "delete old event", err)
				kept := *prev
				kept.Status = "displaced"
				if _, err := storage.UpsertHabitOccurrenceCtx(ctx, e.DB, &kept); err != nil {
					fail(h, "store displaced", err)
				}
				placedBusy = append(placedBusy, interval{start: prev.StartTime, end: prev.EndTime})
				continue
			}
		}

		// Find a slot.
		slot := findFirstSlot(windowStart, windowEnd, dur, mergeIntervals(placedBusy))
		if slot == nil {
			if _, err := storage.UpsertHabitOccurrenceCtx(ctx, e.DB, &storage.HabitOccurrence{
				HabitID:       h.ID,
				ScheduledDate: day,
				StartTime:     windowStart,
				EndTime:       windowStart.Add(dur),
				Status:        "missed",
			}); err != nil {
				fail(h, "store missed", err)
			}
			continue
		}

		calEventID := ""
		if calOps != nil {
			id, err := e.createHabitEvent(ctx, calOps, h, day, slot.start, slot.end)
			if err != nil {
				// SF-015: never "scheduled" without its event. A row that
				// existed no longer has one (deleted above): displaced.
				fail(h, "create event", err)
				if prev != nil {
					gone := *prev
					gone.Status, gone.CalendarEventID = "displaced", ""
					if _, err := storage.UpsertHabitOccurrenceCtx(ctx, e.DB, &gone); err != nil {
						fail(h, "store displaced", err)
					}
				}
				continue
			}
			calEventID = id
		}

		if _, err := storage.UpsertHabitOccurrenceCtx(ctx, e.DB, &storage.HabitOccurrence{
			HabitID:         h.ID,
			ScheduledDate:   day,
			StartTime:       slot.start,
			EndTime:         slot.end,
			Status:          "scheduled",
			CalendarEventID: calEventID,
		}); err != nil {
			fail(h, "store scheduled", err)
			// No row points at the new event: it is removed again.
			if calEventID != "" {
				if err := calOps.deleteEvent(ctx, calEventID); err != nil && !calendar.IsEventGone(err) {
					fail(h, "delete unrecorded event", err)
				}
			}
			continue
		}

		placedBusy = append(placedBusy, *slot)
	}
	return errors.Join(failed...)
}

func (e *HabitsEngine) existingOccurrence(ctx context.Context, habitID uuid.UUID, day time.Time) (*storage.HabitOccurrence, error) {
	occs, err := storage.ListHabitOccurrencesCtx(ctx, e.DB, habitID, day, day)
	if err != nil || len(occs) == 0 {
		return nil, err
	}
	return occs[0], nil
}

func (e *HabitsEngine) createHabitEvent(
	ctx context.Context,
	calOps calendarOps,
	h *storage.Habit,
	day, start, end time.Time,
) (string, error) {
	ev := &googlecalendar.Event{
		Summary:      h.Title,
		Transparency: "opaque",
		Start:        &googlecalendar.EventDateTime{DateTime: start.Format(time.RFC3339)},
		End:          &googlecalendar.EventDateTime{DateTime: end.Format(time.RFC3339)},
		ColorId:      habitColorID(h.Color),
		// PAC-122 SF-014: the event says which habit it belongs to, so a
		// later run never counts it as a meeting even when no row points
		// at it.
		ExtendedProperties: &googlecalendar.EventExtendedProperties{
			Private: map[string]string{habitEventMarker: h.ID.String()},
		},
	}
	// PAC-136: the id is chosen here, from (habit, day), so a create that
	// already happened (a run that crashed before storing it, or whose
	// store failed and whose clean-up delete left the id taken) is found
	// again rather than duplicated: Google answers 409 and that event is
	// adopted, updated to this slot (and restored if it was deleted).
	ev.Id = habitEventID(h.ID, day)
	created, err := calOps.createEvent(ctx, ev)
	if calendar.IsDuplicate(err) {
		created, err = e.adoptHabitEvent(ctx, calOps, h, ev)
	}
	if err != nil {
		return "", err
	}
	if created == nil || created.Id == "" {
		return "", errors.New("calendar returned no event id")
	}
	return created.Id, nil
}

// adoptHabitEvent answers a 409 duplicate on the (habit, day) id (PAC-136).
// The existing event is read first: one whose habit marker names another
// habit is never overwritten. Otherwise it is updated to ev, with status
// confirmed (restoring it when it was deleted). Google documents the 409 for
// an id in use, not that a deleted event's id stays taken nor that an update
// restores it (review M1): when the read shows a foreign event, or the
// update is refused as not found, gone or forbidden, the event is inserted
// again under an id Google generates.
func (e *HabitsEngine) adoptHabitEvent(ctx context.Context, calOps calendarOps, h *storage.Habit, ev *googlecalendar.Event) (*googlecalendar.Event, error) {
	existing, err := calOps.getEvent(ctx, ev.Id)
	switch {
	case err == nil:
		if existing != nil && existing.ExtendedProperties != nil {
			if owner := existing.ExtendedProperties.Private[habitEventMarker]; owner != "" && owner != h.ID.String() {
				return createWithGeneratedID(ctx, calOps, ev)
			}
		}
	case calendar.IsEventGone(err):
		// A deleted event may not be readable: the update decides.
	default:
		return nil, fmt.Errorf("read existing event: %w", err)
	}
	adopt := *ev
	adopt.Status = "confirmed"
	updated, err := calOps.updateEvent(ctx, adopt.Id, &adopt)
	if calendar.IsEventGone(err) || calendar.IsForbidden(err) {
		return createWithGeneratedID(ctx, calOps, ev)
	}
	if err != nil {
		return nil, fmt.Errorf("adopt existing event: %w", err)
	}
	return updated, nil
}

// createWithGeneratedID inserts ev under an id Google chooses.
func createWithGeneratedID(ctx context.Context, calOps calendarOps, ev *googlecalendar.Event) (*googlecalendar.Event, error) {
	fresh := *ev
	fresh.Id = ""
	created, err := calOps.createEvent(ctx, &fresh)
	if err != nil {
		return nil, fmt.Errorf("create under a generated id: %w", err)
	}
	return created, nil
}

// habitEventID is the Google event id of a habit's occurrence on day (its
// date in the user's zone, as the occurrence row stores it): base32hex (lowercase, unpadded) of a SHA-256 over
// the habit id and the date. Google ids are 5-1024 characters of [a-v0-9];
// this is 52.
func habitEventID(habitID uuid.UUID, day time.Time) string {
	sum := sha256.Sum256([]byte("paceday-habit:" + habitID.String() + ":" + day.Format("2006-01-02")))
	return strings.ToLower(base32.HexEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:]))
}

// habitEventMarker is the private extended property a habit event carries:
// the id of its habit (PAC-122 SF-014).
const habitEventMarker = "paceday_habit_id"

// isHabitEvent reports whether ev is a habit event Paceday created (one
// created before the marker existed is not recognised: PAC-137).
func isHabitEvent(ev *googlecalendar.Event) bool {
	return ev.ExtendedProperties != nil && ev.ExtendedProperties.Private[habitEventMarker] != ""
}

// findFirstSlot returns the first 15-min-aligned slot within [windowStart, windowEnd]
// that fits `dur` without overlapping any merged busy interval.
func findFirstSlot(windowStart, windowEnd time.Time, dur time.Duration, merged []interval) *interval {
	for t := windowStart; t.Add(dur).Before(windowEnd) || t.Add(dur).Equal(windowEnd); t = t.Add(15 * time.Minute) {
		iv := interval{start: t, end: t.Add(dur)}
		if !overlapsMerged(iv, merged) {
			return &iv
		}
	}
	return nil
}

func parseHabitTime(hhmm string, day time.Time) time.Time {
	return parseTime(hhmm, day)
}

func containsInt(slice []int, v int) bool {
	for _, x := range slice {
		if x == v {
			return true
		}
	}
	return false
}

// habitWeekday maps time.Weekday to the API convention Monday=1 through Sunday=7.
func habitWeekday(day time.Time) int {
	weekday := int(day.Weekday())
	if weekday == 0 {
		return 7
	}
	return weekday
}

// habitColorID maps hex color to Google Calendar color ID (best-effort).
func habitColorID(hex string) string {
	switch hex {
	case "#5B7FFF":
		return "9" // blueberry
	case "#E9B949":
		return "5" // banana
	case "#9B7AE0":
		return "3" // grape
	default:
		return "8" // graphite
	}
}
