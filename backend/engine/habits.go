package engine

import (
	"context"
	"database/sql"
	"fmt"
	"log"
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
}

// ReoptimizeAll schedules all active habits for the next 14 days.
func (e *HabitsEngine) ReoptimizeAll(ctx context.Context, userID uuid.UUID) error {
	habits, err := storage.ListActiveHabitsByUser(e.DB, userID)
	if err != nil {
		return fmt.Errorf("list habits: %w", err)
	}

	calOps, calErr := e.calOpsForUser(ctx, userID)
	// calOps may be nil if not connected — we degrade gracefully.

	now := time.Now()
	for i := 0; i < 14; i++ {
		day := now.AddDate(0, 0, i)
		if err := e.scheduleDay(ctx, userID, habits, day, calOps, calErr == nil); err != nil {
			log.Printf("habits: schedule day %s: %v", day.Format("2006-01-02"), err)
		}
	}
	return nil
}

// scheduleDay places habit occurrences for one day, processing habits in priority order.
func (e *HabitsEngine) scheduleDay(
	ctx context.Context,
	userID uuid.UUID,
	habits []*storage.Habit,
	day time.Time,
	calOps calendarOps,
	calConnected bool,
) error {
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	dayEnd := dayStart.Add(24 * time.Hour)

	settings, _ := storage.GetSettings(e.DB)

	// Fetch calendar busy intervals once for the day.
	var calBusy []interval
	if calConnected {
		events, err := calOps.listEvents(ctx, dayStart, dayEnd)
		if err == nil {
			for _, ev := range events {
				if ev.Transparency == "transparent" {
					continue
				}
				s, e2 := parseEventTime(ev)
				if !s.IsZero() && !e2.IsZero() {
					calBusy = append(calBusy, interval{start: s, end: e2})
				}
			}
			if settings != nil {
				loc, _ := time.LoadLocation(settings.Timezone)
				if loc == nil {
					loc = time.UTC
				}
				workStart := parseHHMM(settings.WorkStart, day, loc)
				workEnd := parseHHMM(settings.WorkEnd, day, loc)
				calBusy = append(calBusy, ComputeBufferBlocks(events, settings, workStart, workEnd)...)
			}
		}
	}

	// Focus blocks.
	focusBlocks, _ := storage.ListFocusBlocksForWeek(e.DB, day)
	for _, fb := range focusBlocks {
		if fb.StartTime.Year() == day.Year() &&
			fb.StartTime.Month() == day.Month() &&
			fb.StartTime.Day() == day.Day() {
			calBusy = append(calBusy, interval{start: fb.StartTime, end: fb.EndTime})
		}
	}

	// Habits already scheduled for this day — accumulated as we place each habit.
	placedBusy := calBusy

	weekday := int(day.Weekday())

	for _, h := range habits {
		if !containsInt(h.DaysOfWeek, weekday) {
			continue
		}

		windowStart := parseHabitTime(h.WindowStart, day)
		windowEnd := parseHabitTime(h.WindowEnd, day)
		dur := time.Duration(h.DurationMinutes) * time.Minute

		// Check existing occurrence for this day.
		existing, _ := e.existingOccurrence(h.ID, day)

		// If already scheduled and not displaced, verify it still fits.
		if existing != nil && existing.Status == "scheduled" {
			iv := interval{start: existing.StartTime, end: existing.EndTime}
			if !overlapsMerged(iv, mergeIntervals(placedBusy)) {
				// Still valid — add it to placedBusy and move on.
				placedBusy = append(placedBusy, iv)
				continue
			}
			// Mark displaced and try to reschedule below.
			existing.Status = "displaced"
			_, _ = storage.UpsertHabitOccurrence(e.DB, existing)
		}

		// Find a slot.
		merged := mergeIntervals(placedBusy)
		slot := findFirstSlot(windowStart, windowEnd, dur, merged)

		if slot == nil {
			_, _ = storage.UpsertHabitOccurrence(e.DB, &storage.HabitOccurrence{
				HabitID:       h.ID,
				ScheduledDate: day,
				StartTime:     windowStart,
				EndTime:       windowStart.Add(dur),
				Status:        "missed",
			})
			continue
		}

		calEventID := ""
		if calConnected {
			calEventID = e.createHabitEvent(ctx, calOps, h, slot.start, slot.end)
		}

		_, _ = storage.UpsertHabitOccurrence(e.DB, &storage.HabitOccurrence{
			HabitID:         h.ID,
			ScheduledDate:   day,
			StartTime:       slot.start,
			EndTime:         slot.end,
			Status:          "scheduled",
			CalendarEventID: calEventID,
		})

		placedBusy = append(placedBusy, *slot)
	}
	return nil
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
) string {
	ev := &googlecalendar.Event{
		Summary:      h.Title,
		Transparency: "opaque",
		Start:        &googlecalendar.EventDateTime{DateTime: start.Format(time.RFC3339)},
		End:          &googlecalendar.EventDateTime{DateTime: end.Format(time.RFC3339)},
		ColorId:      habitColorID(h.Color),
	}
	created, err := calOps.createEvent(ctx, ev)
	if err != nil {
		return ""
	}
	return created.Id
}

func (e *HabitsEngine) calOpsForUser(ctx context.Context, userID uuid.UUID) (calendarOps, error) {
	token, err := auth.LoadUserToken(e.DB, userID)
	if err != nil || token == nil {
		return nil, fmt.Errorf("no token for user %s", userID)
	}
	ts := e.OAuthConfig.TokenSource(ctx, token)
	client, err := calendar.NewClient(ctx, ts)
	if err != nil {
		return nil, err
	}
	return realOps{c: client}, nil
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
