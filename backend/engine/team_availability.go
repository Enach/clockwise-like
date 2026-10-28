package engine

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/internal/peritem"
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
	members, err := storage.ListTeamMembers(storage.Bind(ctx, e.DB), teamID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	if len(members) == 0 {
		return nil, nil
	}

	zones, err := storage.ListNoMeetingZones(storage.Bind(ctx, e.DB), teamID)
	if err != nil {
		return nil, fmt.Errorf("list zones: %w", err)
	}

	loc := time.UTC
	workStart := parseHHMM(defaultWorkStart, day, loc)
	workEnd := parseHHMM(defaultWorkEnd, day, loc)
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	dayEnd := dayStart.Add(24 * time.Hour)

	// PAC-81 AC-A18: each member's read (renewal plus one list) runs on the
	// bounded per-item pool. A member whose calendar fails, or times out on
	// its own limit, is treated as free, as documented; a member not read
	// because of the budget, or cut by it, is a budget error (503).
	mdata := make([]memberBusyData, len(members))
	if err := peritem.Run(ctx, len(members), peritem.Read, HostReadLimit(), func(callCtx context.Context, i int) error {
		mdata[i] = e.memberBusy(callCtx, members[i].UserID, day, dayStart, dayEnd)
		return nil
	}); err != nil {
		return nil, err
	}
	var allBusy []interval
	for _, md := range mdata {
		allBusy = append(allBusy, md.busy...)
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

// memberBusy reads one member's busy time and focus blocks for the day.
func (e *TeamAvailabilityEngine) memberBusy(ctx context.Context, userID uuid.UUID, day, dayStart, dayEnd time.Time) memberBusyData {
	db := storage.Bind(ctx, e.DB)
	var md memberBusyData

	// Per-member calendar access.
	token, err := auth.LoadUserToken(db, userID)
	if err == nil && token != nil {
		// The member's own credential: renewal and loss are theirs (PAC-67, D-12).
		ts := auth.UserTokenSource(ctx, db, e.OAuthConfig, userID, token)
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

	// The member's own focus blocks for the day (soft-busy for scoring).
	fbs, _ := storage.ListFocusBlocksForUserWeek(db, userID, day)
	for _, fb := range fbs {
		if fb.StartTime.Year() == day.Year() && fb.StartTime.Month() == day.Month() && fb.StartTime.Day() == day.Day() {
			md.focus = append(md.focus, interval{fb.StartTime, fb.EndTime})
			md.busy = append(md.busy, interval{fb.StartTime, fb.EndTime})
		}
	}

	return md
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
	breakdown := make([]MemberAnalyticsSummary, 0, len(members))

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
