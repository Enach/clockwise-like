package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
func (e *HabitsEngine) ReoptimizeAll(ctx context.Context, userID uuid.UUID) error {
	habits, err := storage.ListActiveHabitsByUser(e.DB, userID)
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
		occ, err := e.existingOccurrence(h.ID, day)
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
	if calOps != nil {
		listed, err := calOps.listEvents(ctx, dayStart, dayEnd)
		if err != nil {
			return fmt.Errorf("read calendar: %w", err)
		}
		events := make([]*googlecalendar.Event, 0, len(listed))
		for _, ev := range listed {
			if _, own := ownEvents[ev.Id]; own {
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
	focusBlocks, err := storage.ListFocusBlocksForUserWeek(e.DB, userID, day)
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

		// If already scheduled, verify it still fits.
		if prev != nil && prev.Status == "scheduled" {
			iv := interval{start: prev.StartTime, end: prev.EndTime}
			if !overlapsMerged(iv, mergeIntervals(placedBusy)) {
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
			id, err := e.createHabitEvent(ctx, calOps, h, slot.start, slot.end)
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

func (e *HabitsEngine) existingOccurrence(habitID uuid.UUID, day time.Time) (*storage.HabitOccurrence, error) {
	occs, err := storage.ListHabitOccurrences(e.DB, habitID, day, day)
	if err != nil || len(occs) == 0 {
		return nil, err
	}
	return occs[0], nil
}

func (e *HabitsEngine) createHabitEvent(
	ctx context.Context,
	calOps calendarOps,
	h *storage.Habit,
	start, end time.Time,
) (string, error) {
	ev := &googlecalendar.Event{
		Summary:      h.Title,
		Transparency: "opaque",
		Start:        &googlecalendar.EventDateTime{DateTime: start.Format(time.RFC3339)},
		End:          &googlecalendar.EventDateTime{DateTime: end.Format(time.RFC3339)},
		ColorId:      habitColorID(h.Color),
	}
	created, err := calOps.createEvent(ctx, ev)
	if err != nil {
		return "", err
	}
	if created == nil || created.Id == "" {
		return "", errors.New("calendar returned no event id")
	}
	return created.Id, nil
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
