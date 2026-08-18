package engine

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	googlecalendar "google.golang.org/api/calendar/v3"
)

// ManagerEngine handles team detection, 1:1 gap analysis, and analytics.
type ManagerEngine struct {
	DB          *sql.DB
	OAuthConfig *oauth2.Config
	Clock       Clock // optional; falls back to SystemClock when nil
}

// now returns the current time via the injected Clock, or SystemClock when unset.
func (e *ManagerEngine) now() time.Time {
	if e.Clock != nil {
		return e.Clock.Now()
	}
	return SystemClock.Now()
}

// ── Team Detection ────────────────────────────────────────────────────────────

type DetectResult struct {
	MembersAdded   int
	MembersUpdated int
	IsManager      bool
}

type oneOnOneOccurrence struct {
	eventID    string
	occurredAt time.Time
}

type oneOnOneCandidate struct {
	email                string
	displayName          string
	rrule                string
	hasRecurringMetadata bool
	occurrences          []oneOnOneOccurrence
}

// collectOneOnOneCandidates extracts likely 1:1s from expanded calendar
// instances. Google returns recurring instances with RecurringEventId and an
// empty Recurrence field when SingleEvents=true, so Recurrence alone is not a
// reliable recurring-meeting signal.
func collectOneOnOneCandidates(events []*googlecalendar.Event, managerEmail string) map[string]*oneOnOneCandidate {
	byEmail := map[string]*oneOnOneCandidate{}
	for _, ev := range events {
		if ev == nil {
			continue
		}
		dur := calcEventDurationMinutes(
			func() string {
				if ev.Start != nil {
					return ev.Start.DateTime
				}
				return ""
			}(),
			func() string {
				if ev.End != nil {
					return ev.End.DateTime
				}
				return ""
			}(),
		)
		if dur < 15 || dur > 90 {
			continue
		}

		// Keep exactly one participant other than the manager. This tolerates
		// resource/organizer entries while still excluding group meetings.
		var other *googlecalendar.EventAttendee
		for _, attendee := range ev.Attendees {
			if attendee == nil {
				continue
			}
			email := strings.ToLower(strings.TrimSpace(attendee.Email))
			if email == "" || attendee.Self || strings.EqualFold(email, managerEmail) {
				continue
			}
			if other != nil {
				other = nil
				break
			}
			other = attendee
		}
		if other == nil {
			continue
		}

		rrule := ""
		for _, recurrence := range ev.Recurrence {
			if strings.HasPrefix(strings.ToUpper(recurrence), "RRULE:") {
				rrule = recurrence
				break
			}
		}
		candidateEmail := strings.ToLower(strings.TrimSpace(other.Email))
		candidate := byEmail[candidateEmail]
		if candidate == nil {
			candidate = &oneOnOneCandidate{
				email:                candidateEmail,
				displayName:          other.DisplayName,
				rrule:                rrule,
				hasRecurringMetadata: rrule != "" || ev.RecurringEventId != "",
			}
			byEmail[candidateEmail] = candidate
		} else if candidate.displayName == "" {
			candidate.displayName = other.DisplayName
		}
		if candidate.rrule == "" && rrule != "" {
			candidate.rrule = rrule
		}
		candidate.hasRecurringMetadata = candidate.hasRecurringMetadata || rrule != "" || ev.RecurringEventId != ""

		var occurredAt time.Time
		if ev.Start != nil && ev.Start.DateTime != "" {
			occurredAt, _ = time.Parse(time.RFC3339, ev.Start.DateTime)
		}
		candidate.occurrences = append(candidate.occurrences, oneOnOneOccurrence{eventID: ev.Id, occurredAt: occurredAt})
	}

	// Two separate instances with no recurrence metadata can still represent a
	// recurring 1:1. A single isolated meeting must not be auto-added.
	for email, candidate := range byEmail {
		if !candidate.hasRecurringMetadata && len(candidate.occurrences) < 2 {
			delete(byEmail, email)
		}
	}
	return byEmail
}

func inferCandidateCadence(candidate *oneOnOneCandidate) string {
	if cadence := inferCadence(candidate.rrule); cadence != "none" {
		return cadence
	}
	if len(candidate.occurrences) < 2 {
		return "none"
	}
	times := make([]time.Time, 0, len(candidate.occurrences))
	for _, occurrence := range candidate.occurrences {
		if !occurrence.occurredAt.IsZero() {
			times = append(times, occurrence.occurredAt)
		}
	}
	if len(times) < 2 {
		return "none"
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	var totalDays float64
	for i := 1; i < len(times); i++ {
		totalDays += times[i].Sub(times[i-1]).Hours() / 24
	}
	averageDays := totalDays / float64(len(times)-1)
	switch {
	case averageDays >= 5 && averageDays <= 9:
		return "weekly"
	case averageDays > 9 && averageDays <= 18:
		return "biweekly"
	case averageDays >= 25 && averageDays <= 35:
		return "monthly"
	default:
		return "none"
	}
}

// DetectTeam scans a six-week window around now and identifies 1:1
// recurring meetings to build the manager's team member list.
func (e *ManagerEngine) DetectTeam(ctx context.Context, managerID uuid.UUID) (*DetectResult, error) {
	token, err := auth.LoadUserToken(e.DB, managerID)
	if err != nil {
		return nil, fmt.Errorf("load token: %w", err)
	}
	ts := auth.TokenSource(ctx, e.OAuthConfig, token)
	client, err := calendar.NewClient(ctx, ts)
	if err != nil {
		return nil, fmt.Errorf("calendar client: %w", err)
	}

	now := e.now()
	events, err := client.ListEvents(ctx, "primary", now.Add(-42*24*time.Hour), now.Add(42*24*time.Hour))
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}

	manager, _ := storage.GetUserByID(e.DB, managerID)
	managerEmail := ""
	if manager != nil {
		managerEmail = manager.Email
	}
	byEmail := collectOneOnOneCandidates(events, managerEmail)

	profile, _ := storage.GetOrCreateUserProfile(e.DB, managerID)
	result := &DetectResult{}

	for _, c := range byEmail {
		if len(c.occurrences) == 0 {
			continue
		}
		cadence := inferCandidateCadence(c)

		// Latest occurrence
		var lastOOO time.Time
		for _, occ := range c.occurrences {
			if !occ.occurredAt.IsZero() && !occ.occurredAt.After(now) && occ.occurredAt.After(lastOOO) {
				lastOOO = occ.occurredAt
			}
		}
		var lastOOOPtr *time.Time
		if !lastOOO.IsZero() {
			lastOOOPtr = &lastOOO
		}

		// Check if member is a Paceday user
		var memberUserID *uuid.UUID
		var uid uuid.UUID
		err := e.DB.QueryRow(`SELECT id FROM users WHERE email=$1`, c.email).Scan(&uid)
		if err == nil {
			memberUserID = &uid
		}

		m := &storage.ManagerTeamMember{
			ManagerUserID:  managerID,
			MemberEmail:    c.email,
			MemberUserID:   memberUserID,
			DisplayName:    c.displayName,
			Source:         "auto",
			Cadence:        cadence,
			LastOneOnOneAt: lastOOOPtr,
		}

		existing, err := storage.GetManagerTeamMemberByEmail(e.DB, managerID, c.email)
		if err != nil {
			result.MembersAdded++
		} else if existing != nil {
			result.MembersUpdated++
		}
		_ = storage.UpsertManagerTeamMember(e.DB, m)

		// Record occurrences
		for _, occ := range c.occurrences {
			_ = storage.UpsertOneOnOneOccurrence(e.DB, managerID, c.email, occ.eventID, occ.occurredAt)
		}
	}

	if result.MembersAdded+result.MembersUpdated > 0 {
		now2 := e.now().UTC()
		profile.IsManager = true
		profile.DetectedAt = &now2
		result.IsManager = true
		_ = storage.UpsertUserProfile(e.DB, profile)
	}

	return result, nil
}

func inferCadence(rrule string) string {
	upper := strings.ToUpper(rrule)
	switch {
	case strings.Contains(upper, "FREQ=WEEKLY;INTERVAL=2"):
		return "biweekly"
	case strings.Contains(upper, "FREQ=WEEKLY"):
		return "weekly"
	case strings.Contains(upper, "FREQ=MONTHLY"):
		return "monthly"
	default:
		return "none"
	}
}

// ── Gap Detection ─────────────────────────────────────────────────────────────

type CadenceGap struct {
	MemberEmail    string     `json:"member_email"`
	DisplayName    string     `json:"display_name"`
	Cadence        string     `json:"cadence"`
	LastOneOnOneAt *time.Time `json:"last_one_on_one_at"`
	NextExpectedBy time.Time  `json:"next_expected_by"`
	DaysOverdue    int        `json:"days_overdue"`
}

// GetGaps returns members whose 1:1 cadence is at risk or overdue.
func (e *ManagerEngine) GetGaps(ctx context.Context, managerID uuid.UUID) ([]CadenceGap, error) {
	members, err := storage.ListManagerTeamMembers(e.DB, managerID)
	if err != nil {
		return nil, err
	}

	now := e.now().UTC()
	var gaps []CadenceGap

	for _, m := range members {
		if m.Cadence == "none" {
			continue
		}
		nextExpected := e.nextExpectedDate(m)
		if nextExpected.IsZero() {
			continue
		}

		// Check if there's a future event with this member in the next 14 days.
		if e.hasFutureEvent(ctx, managerID, m.MemberEmail, now, now.Add(14*24*time.Hour)) {
			continue
		}

		// Gap if next_expected_by is within 7 days from now or already past.
		daysUntil := int(math.Ceil(nextExpected.Sub(now).Hours() / 24))
		if daysUntil > 7 {
			continue
		}
		daysOverdue := 0
		if daysUntil < 0 {
			daysOverdue = -daysUntil
		}
		gaps = append(gaps, CadenceGap{
			MemberEmail:    m.MemberEmail,
			DisplayName:    m.DisplayName,
			Cadence:        m.Cadence,
			LastOneOnOneAt: m.LastOneOnOneAt,
			NextExpectedBy: nextExpected,
			DaysOverdue:    daysOverdue,
		})
	}

	// Sort by days_overdue DESC
	for i := 0; i < len(gaps)-1; i++ {
		for j := i + 1; j < len(gaps); j++ {
			if gaps[j].DaysOverdue > gaps[i].DaysOverdue {
				gaps[i], gaps[j] = gaps[j], gaps[i]
			}
		}
	}
	return gaps, nil
}

// nextExpectedDate returns the date the next 1:1 with m should occur.
// Uses calendar-month arithmetic for "monthly" cadence; day-based addition for the rest.
func (e *ManagerEngine) nextExpectedDate(m *storage.ManagerTeamMember) time.Time {
	base := e.now().UTC()
	if m.LastOneOnOneAt != nil {
		base = *m.LastOneOnOneAt
	}
	switch m.Cadence {
	case "weekly":
		return base.AddDate(0, 0, 7)
	case "biweekly":
		return base.AddDate(0, 0, 14)
	case "monthly":
		return base.AddDate(0, 1, 0)
	case "custom":
		if m.CadenceCustomDays != nil && *m.CadenceCustomDays > 0 {
			return base.AddDate(0, 0, *m.CadenceCustomDays)
		}
	}
	return time.Time{}
}

func (e *ManagerEngine) hasFutureEvent(_ context.Context, managerID uuid.UUID, memberEmail string, from, to time.Time) bool {
	var count int
	_ = e.DB.QueryRow(`
		SELECT COUNT(*) FROM one_on_one_occurrences
		WHERE manager_user_id=$1 AND member_email=$2
		  AND occurred_at >= $3 AND occurred_at < $4`,
		managerID, memberEmail, from, to,
	).Scan(&count)
	return count > 0
}

// ── Team Analytics ────────────────────────────────────────────────────────────

type MemberWeekStats struct {
	FocusMinutes   int  `json:"focus_minutes"`
	MeetingMinutes int  `json:"meeting_minutes"`
	FreeMinutes    int  `json:"free_minutes"`
	DataAvailable  bool `json:"data_available"`
}

const analyticsCache4h = 4 * time.Hour
const totalWorkMinutesPerWeek = 5 * 8 * 60 // Mon–Fri, 8h/day

func (e *ManagerEngine) GetMemberWeek(ctx context.Context, managerID uuid.UUID, member *storage.ManagerTeamMember, weekStart time.Time) (*MemberWeekStats, error) {
	// Check cache (4h TTL)
	cached, err := storage.GetTeamAnalytics(e.DB, managerID, member.MemberEmail, weekStart)
	if err == nil && time.Since(cached.ComputedAt) < analyticsCache4h {
		return &MemberWeekStats{
			FocusMinutes:   cached.FocusMinutes,
			MeetingMinutes: cached.MeetingMinutes,
			FreeMinutes:    cached.FreeMinutes,
			DataAvailable:  cached.DataAvailable,
		}, nil
	}

	var stats MemberWeekStats
	var isPacedayUser bool

	if member.MemberUserID != nil {
		// Member is a Paceday user — read their analytics_weeks row
		// Check consent first
		var shared bool
		_ = e.DB.QueryRow(`
			SELECT COALESCE(analytics_shared_with_manager, true)
			FROM user_profiles WHERE user_id=$1`, *member.MemberUserID,
		).Scan(&shared)
		if shared {
			var focusMin, meetMin, freeMin int
			err := e.DB.QueryRow(`
				SELECT COALESCE(focus_minutes,0), COALESCE(meeting_minutes,0), COALESCE(free_minutes,0)
				FROM analytics_weeks
				WHERE user_id=$1 AND week_start=$2`,
				*member.MemberUserID, weekStart.Format("2006-01-02"),
			).Scan(&focusMin, &meetMin, &freeMin)
			if err == nil {
				stats.FocusMinutes = focusMin
				stats.MeetingMinutes = meetMin
				stats.FreeMinutes = freeMin
				stats.DataAvailable = true
			}
		}
		isPacedayUser = true
	} else {
		// External user — use FreeBusyService
		fbSvc := &FreeBusyService{DB: e.DB, OAuthConfig: e.OAuthConfig}
		weekEnd := weekStart.Add(7 * 24 * time.Hour)
		results, err := fbSvc.Query(ctx, managerID, []string{member.MemberEmail}, weekStart, weekEnd)
		if err == nil && len(results) > 0 {
			r := results[0]
			if r.Coverage == "known" {
				busy := 0
				for _, slot := range r.Busy {
					busy += int(slot.End.Sub(slot.Start).Minutes())
				}
				stats.MeetingMinutes = busy
				stats.FreeMinutes = totalWorkMinutesPerWeek - busy
				if stats.FreeMinutes < 0 {
					stats.FreeMinutes = 0
				}
				stats.DataAvailable = true
			}
		}
	}

	// Cache the result
	_ = storage.UpsertTeamAnalytics(e.DB, &storage.TeamAnalyticsRow{
		ManagerUserID:  managerID,
		MemberEmail:    member.MemberEmail,
		WeekStart:      weekStart,
		FocusMinutes:   stats.FocusMinutes,
		MeetingMinutes: stats.MeetingMinutes,
		FreeMinutes:    stats.FreeMinutes,
		IsPacedayUser:  isPacedayUser,
		DataAvailable:  stats.DataAvailable,
	})

	return &stats, nil
}

func TrendPct(current, prior int) float64 {
	if prior == 0 {
		if current == 0 {
			return 0
		}
		return 200 // cap
	}
	t := float64(current-prior) / float64(prior) * 100
	if t > 200 {
		return 200
	}
	if t < -200 {
		return -200
	}
	return math.Round(t*10) / 10
}

// eventDurationMinutes calculates meeting duration from start/end strings.
func calcEventDurationMinutes(startDT, endDT string) int {
	st, err1 := time.Parse(time.RFC3339, startDT)
	et, err2 := time.Parse(time.RFC3339, endDT)
	if err1 != nil || err2 != nil {
		return 0
	}
	return int(et.Sub(st).Minutes())
}
