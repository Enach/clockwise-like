package engine

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

const defaultWorkStart = "09:00"
const defaultWorkEnd = "18:00"

// TeamSlot is a candidate meeting slot for a whole team.
type TeamSlot struct {
	Start        time.Time `json:"start"`
	End          time.Time `json:"end"`
	QualityScore int       `json:"quality_score"`
}

// TeamAnalytics aggregates calendar data across team members.
type TeamAnalytics struct {
	AvgMeetingMinutes int                      `json:"avg_meeting_minutes"`
	AvgFocusMinutes   int                      `json:"avg_focus_minutes"`
	MemberBreakdown   []MemberAnalyticsSummary `json:"member_breakdown"`
}

// MemberAnalyticsSummary is the per-member analytics row returned in team analytics.
type MemberAnalyticsSummary struct {
	UserID         uuid.UUID `json:"user_id"`
	Name           string    `json:"name"`
	MeetingMinutes int       `json:"meeting_minutes"`
	FocusMinutes   int       `json:"focus_minutes"`
	FocusScore     int       `json:"focus_score"`
}

type memberBusyData struct {
	busy  []interval
	focus []interval
}

// TeamAvailabilityEngine computes availability across team members.
type TeamAvailabilityEngine struct {
	DB          *sql.DB
	OAuthConfig *oauth2.Config
}

// FindSlots returns time slots on the given day where all team members are free,
// excluding no-meeting zones. Each slot is scored by how few focus blocks it disrupts.
func (e *TeamAvailabilityEngine) FindSlots(ctx context.Context, teamID uuid.UUID, day time.Time, durationMinutes int) ([]TeamSlot, error) {
	members, err := storage.ListTeamMembers(e.DB, teamID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	if len(members) == 0 {
		return nil, nil
	}

	zones, err := storage.ListNoMeetingZones(e.DB, teamID)
	if err != nil {
		return nil, fmt.Errorf("list zones: %w", err)
	}

	loc := time.UTC
	workStart := parseHHMM(defaultWorkStart, day, loc)
	workEnd := parseHHMM(defaultWorkEnd, day, loc)
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	dayEnd := dayStart.Add(24 * time.Hour)

	mdata := make([]memberBusyData, 0, len(members))
	var allBusy []interval

	for _, m := range members {
		var md memberBusyData

		// Per-member calendar access.
		token, err := auth.LoadUserToken(e.DB, m.UserID)
		if err == nil && token != nil {
			ts := auth.TokenSource(ctx, e.OAuthConfig, token)
			client, err := calendar.NewClient(ctx, ts)
			if err == nil {
				ops := realOps{c: client}
				events, err := ops.listEvents(ctx, dayStart, dayEnd)
				if err == nil {
					for _, ev := range events {
						if ev.Transparency == "transparent" {
							continue
						}
						s, e2 := parseEventTime(ev)
						if !s.IsZero() && !e2.IsZero() {
							md.busy = append(md.busy, interval{s, e2})
						}
					}
				}
			}
		}

		// Focus blocks for the day (soft-busy for scoring).
		fbs, _ := storage.ListFocusBlocksForWeek(e.DB, day)
		for _, fb := range fbs {
			if fb.StartTime.Year() == day.Year() && fb.StartTime.Month() == day.Month() && fb.StartTime.Day() == day.Day() {
				md.focus = append(md.focus, interval{fb.StartTime, fb.EndTime})
				md.busy = append(md.busy, interval{fb.StartTime, fb.EndTime})
			}
		}

		allBusy = append(allBusy, md.busy...)
		mdata = append(mdata, md)
	}

	// No-meeting zones are hard blocks.
	weekday := int(day.Weekday())
	for _, z := range zones {
		if z.DayOfWeek != weekday {
			continue
		}
		zs := parseHHMM(z.StartTime, day, loc)
		ze := parseHHMM(z.EndTime, day, loc)
		allBusy = append(allBusy, interval{zs, ze})
	}

	merged := mergeIntervals(allBusy)
	free := subtractIntervals(interval{workStart, workEnd}, merged)

	dur := time.Duration(durationMinutes) * time.Minute
	var slots []TeamSlot
	for _, f := range free {
		if f.end.Sub(f.start) < dur {
			continue
		}
		cursor := f.start
		for !cursor.Add(dur).After(f.end) {
			end := cursor.Add(dur)
			score := teamQualityScore(interval{cursor, end}, mdata)
			slots = append(slots, TeamSlot{Start: cursor, End: end, QualityScore: score})
			cursor = cursor.Add(15 * time.Minute)
		}
	}

	return slots, nil
}

// teamQualityScore returns 0-100: score = 100 - (members_with_disrupted_focus/total)*100
func teamQualityScore(slot interval, members []memberBusyData) int {
	if len(members) == 0 {
		return 100
	}
	disrupted := 0
	for _, m := range members {
		for _, fb := range m.focus {
			if slot.start.Before(fb.end) && slot.end.After(fb.start) {
				disrupted++
				break
			}
		}
	}
	return 100 - (disrupted*100)/len(members)
}

// GetTeamAnalytics returns aggregate analytics for the team for the week containing weekStart.
func (e *TeamAvailabilityEngine) GetTeamAnalytics(_ context.Context, teamID uuid.UUID, weekStart time.Time) (*TeamAnalytics, error) {
	members, err := storage.ListTeamMembers(e.DB, teamID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}

	var totalMeeting, totalFocus int
	var breakdown []MemberAnalyticsSummary

	for _, m := range members {
		row, _ := storage.GetAnalyticsWeek(e.DB, m.UserID, weekStart)
		s := MemberAnalyticsSummary{UserID: m.UserID, Name: m.Name}
		if row != nil {
			s.MeetingMinutes = row.MeetingMinutes
			s.FocusMinutes = row.FocusMinutes
			s.FocusScore = row.FocusScore
			totalMeeting += row.MeetingMinutes
			totalFocus += row.FocusMinutes
		}
		breakdown = append(breakdown, s)
	}

	n := len(members)
	if n == 0 {
		n = 1
	}
	return &TeamAnalytics{
		AvgMeetingMinutes: totalMeeting / n,
		AvgFocusMinutes:   totalFocus / n,
		MemberBreakdown:   breakdown,
	}, nil
}
