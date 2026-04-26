package engine

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// AnalyticsEngine computes weekly time breakdowns.
type AnalyticsEngine struct {
	DB          *sql.DB
	OAuthConfig *oauth2.Config
}

// weekStartFor returns the Monday of the week containing date.
func weekStartFor(date time.Time) time.Time {
	d := date
	for d.Weekday() != time.Monday {
		d = d.AddDate(0, 0, -1)
	}
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, d.Location())
}

// ComputeWeek computes and stores analytics for the week containing date.
// Returns cached data if it was already computed today.
func (e *AnalyticsEngine) ComputeWeek(ctx context.Context, userID uuid.UUID, date time.Time) (*storage.AnalyticsWeek, error) {
	weekStart := weekStartFor(date)

	// Return cached result if computed today.
	cached, err := storage.GetAnalyticsWeek(e.DB, userID, weekStart)
	if err != nil {
		return nil, err
	}
	if cached != nil && cached.ComputedAt.After(time.Now().Add(-24*time.Hour)) {
		return cached, nil
	}

	return e.compute(ctx, userID, weekStart)
}

// ForceCompute recomputes analytics for the week containing date, ignoring cache.
func (e *AnalyticsEngine) ForceCompute(ctx context.Context, userID uuid.UUID, date time.Time) (*storage.AnalyticsWeek, error) {
	return e.compute(ctx, userID, weekStartFor(date))
}

func (e *AnalyticsEngine) compute(ctx context.Context, userID uuid.UUID, weekStart time.Time) (*storage.AnalyticsWeek, error) {
	weekEnd := weekStart.AddDate(0, 0, 7)

	settings, err := storage.GetSettings(e.DB)
	if err != nil {
		return nil, fmt.Errorf("get settings: %w", err)
	}

	workStartMin := hhmmToMinutes(settings.WorkStart)
	workEndMin := hhmmToMinutes(settings.WorkEnd)
	workDayMinutes := workEndMin - workStartMin
	if workDayMinutes <= 0 {
		workDayMinutes = 480 // 8 hours default
	}
	totalWorkingMinutes := workDayMinutes * 5 // Mon-Fri

	// Build focus event ID set.
	focusBlocks, _ := storage.ListFocusBlocksForWeek(e.DB, weekStart)
	focusIDs := make(map[string]struct{}, len(focusBlocks))
	for _, fb := range focusBlocks {
		focusIDs[fb.GoogleEventID] = struct{}{}
	}
	focusBlockCount := len(focusBlocks)
	focusMinutes, largestFocusBlock := focusBlockMinutes(focusBlocks)

	// Build habit event ID set + completion rate.
	habitOccs := habitOccurrencesForWeek(e.DB, userID, weekStart, weekEnd)
	habitIDs := make(map[string]struct{}, len(habitOccs))
	habitMinutes := 0
	scheduledCount, completedCount := 0, 0
	for _, occ := range habitOccs {
		if occ.CalendarEventID != "" {
			habitIDs[occ.CalendarEventID] = struct{}{}
		}
		mins := int(occ.EndTime.Sub(occ.StartTime).Minutes())
		if occ.Status == "scheduled" || occ.Status == "completed" {
			habitMinutes += mins
			scheduledCount++
		}
		if occ.Status == "completed" {
			completedCount++
		}
	}
	habitCompletionRate := 0.0
	if scheduledCount > 0 {
		habitCompletionRate = float64(completedCount) / float64(scheduledCount)
	}

	// Fetch calendar events.
	meetingMinutes := 0
	meetingCount := 0
	personalMinutes := 0
	estimatedCost := 0
	meetingMap := make(map[string]int) // title → duration minutes (deduped by event ID)

	calOps, calErr := e.calOpsForUser(ctx, userID)
	if calErr == nil {
		events, err := calOps.listEvents(ctx, weekStart, weekEnd)
		if err == nil {
			for _, ev := range events {
				if ev.Start == nil || ev.Start.DateTime == "" {
					continue // skip all-day events
				}
				s, end := parseEventTime(ev)
				if s.IsZero() || end.IsZero() {
					continue
				}
				// Skip if before or after work week.
				if end.Before(weekStart) || s.After(weekEnd) {
					continue
				}
				dur := int(end.Sub(s).Minutes())
				if dur <= 0 {
					continue
				}

				if _, ok := focusIDs[ev.Id]; ok {
					continue // already counted via DB
				}
				if _, ok := habitIDs[ev.Id]; ok {
					continue // already counted via DB
				}

				if len(ev.Attendees) > 0 {
					meetingMinutes += dur
					meetingCount++
					estimatedCost += dur * len(ev.Attendees)
					if ev.Summary != "" {
						meetingMap[ev.Summary] += dur
					}
				} else {
					personalMinutes += dur
				}
			}
		}
	}

	// Top 5 meeting titles by duration.
	type titleDur struct {
		title string
		dur   int
	}
	var titleList []titleDur
	for t, d := range meetingMap {
		titleList = append(titleList, titleDur{t, d})
	}
	sort.Slice(titleList, func(i, j int) bool { return titleList[i].dur > titleList[j].dur })
	top := make([]storage.MeetingTitle, 0, 5)
	for i, td := range titleList {
		if i >= 5 {
			break
		}
		top = append(top, storage.MeetingTitle{Title: td.title, DurationMinutes: td.dur})
	}

	freeMinutes := totalWorkingMinutes - meetingMinutes - focusMinutes - habitMinutes - personalMinutes
	if freeMinutes < 0 {
		freeMinutes = 0
	}

	focusScore := 50
	if focusMinutes+meetingMinutes > 0 {
		focusScore = (focusMinutes * 100) / (focusMinutes + meetingMinutes)
		if focusScore > 100 {
			focusScore = 100
		}
	}

	result, err := storage.UpsertAnalyticsWeek(e.DB, &storage.AnalyticsWeek{
		UserID:                      userID,
		WeekStart:                   weekStart,
		TotalWorkingMinutes:         totalWorkingMinutes,
		MeetingMinutes:              meetingMinutes,
		FocusMinutes:                focusMinutes,
		HabitMinutes:                habitMinutes,
		PersonalMinutes:             personalMinutes,
		FreeMinutes:                 freeMinutes,
		MeetingCount:                meetingCount,
		FocusBlockCount:             focusBlockCount,
		HabitCompletionRate:         habitCompletionRate,
		LargestFocusBlockMinutes:    largestFocusBlock,
		TopMeetingTitles:            top,
		FocusScore:                  focusScore,
		EstimatedMeetingCostMinutes: estimatedCost,
	})
	return result, err
}

func (e *AnalyticsEngine) calOpsForUser(ctx context.Context, userID uuid.UUID) (calendarOps, error) {
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

// WeekMeetings returns the meeting list for a given week (for the /meetings endpoint).
func (e *AnalyticsEngine) WeekMeetings(ctx context.Context, userID uuid.UUID, date time.Time) ([]map[string]any, error) {
	weekStart := weekStartFor(date)
	weekEnd := weekStart.AddDate(0, 0, 7)

	// Build skip-sets.
	focusBlocks, _ := storage.ListFocusBlocksForWeek(e.DB, weekStart)
	focusIDs := make(map[string]struct{}, len(focusBlocks))
	for _, fb := range focusBlocks {
		focusIDs[fb.GoogleEventID] = struct{}{}
	}

	habitOccs := habitOccurrencesForWeek(e.DB, userID, weekStart, weekEnd)
	habitIDs := make(map[string]struct{}, len(habitOccs))
	for _, occ := range habitOccs {
		if occ.CalendarEventID != "" {
			habitIDs[occ.CalendarEventID] = struct{}{}
		}
	}

	calOps, err := e.calOpsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	events, err := calOps.listEvents(ctx, weekStart, weekEnd)
	if err != nil {
		return nil, err
	}

	var meetings []map[string]any
	for _, ev := range events {
		if ev.Start == nil || ev.Start.DateTime == "" {
			continue
		}
		if len(ev.Attendees) == 0 {
			continue
		}
		if _, ok := focusIDs[ev.Id]; ok {
			continue
		}
		if _, ok := habitIDs[ev.Id]; ok {
			continue
		}
		s, end := parseEventTime(ev)
		if s.IsZero() {
			continue
		}
		meetings = append(meetings, map[string]any{
			"title":            ev.Summary,
			"duration_minutes": int(end.Sub(s).Minutes()),
			"attendee_count":   len(ev.Attendees),
			"start_time":       s.Format(time.RFC3339),
		})
	}
	// Sort by duration desc.
	sort.Slice(meetings, func(i, j int) bool {
		di, _ := meetings[i]["duration_minutes"].(int)
		dj, _ := meetings[j]["duration_minutes"].(int)
		return di > dj
	})
	return meetings, nil
}

// --- helpers ----------------------------------------------------------------

func hhmmToMinutes(hhmm string) int {
	parts := strings.SplitN(hhmm, ":", 2)
	if len(parts) != 2 {
		return 0
	}
	h, m := 0, 0
	_, _ = fmt.Sscanf(parts[0], "%d", &h)
	_, _ = fmt.Sscanf(parts[1], "%d", &m)
	return h*60 + m
}

func focusBlockMinutes(blocks []storage.FocusBlock) (total, largest int) {
	for _, fb := range blocks {
		dur := int(fb.EndTime.Sub(fb.StartTime).Minutes())
		total += dur
		if dur > largest {
			largest = dur
		}
	}
	return
}

func habitOccurrencesForWeek(db *sql.DB, userID uuid.UUID, from, to time.Time) []*storage.HabitOccurrence {
	habits, err := storage.ListHabitsByUser(db, userID)
	if err != nil {
		return nil
	}
	var all []*storage.HabitOccurrence
	for _, h := range habits {
		occs, err := storage.ListHabitOccurrences(db, h.ID, from, to)
		if err != nil {
			continue
		}
		all = append(all, occs...)
	}
	return all
}
