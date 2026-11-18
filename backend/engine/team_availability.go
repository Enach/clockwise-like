package engine

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
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

// TeamSlot is a candidate meeting slot for a whole team. The counts (PAC-129,
// SF-054) cover members of both kinds: an offered slot never has a busy
// member, so every member whose availability is known is free in it.
type TeamSlot struct {
	Start        time.Time `json:"start"`
	End          time.Time `json:"end"`
	QualityScore int       `json:"quality_score"`
	FreeCount    int       `json:"free_count"`
	UnknownCount int       `json:"unknown_count"`
	TotalCount   int       `json:"total_count"`
}

// TeamAnalytics aggregates calendar data across team members.
type TeamAnalytics struct {
	// WeekStart is the Monday of the week asked for (PAC-129, SF-053).
	WeekStart         string                   `json:"week_start"`
	AvgMeetingMinutes int                      `json:"avg_meeting_minutes"`
	AvgFocusMinutes   int                      `json:"avg_focus_minutes"`
	MemberBreakdown   []MemberAnalyticsSummary `json:"member_breakdown"`
	People            []TeamAnalyticsPerson    `json:"people"`
}

// MemberAnalyticsSummary is the per-member analytics row returned in team
// analytics: kept for clients built before PAC-129, Paceday members only.
type MemberAnalyticsSummary struct {
	UserID         uuid.UUID `json:"user_id"`
	Name           string    `json:"name"`
	MeetingMinutes int       `json:"meeting_minutes"`
	FocusMinutes   int       `json:"focus_minutes"`
	FocusScore     int       `json:"focus_score"`
}

// The figures of a TeamAnalyticsPerson (PAC-129 contract §3.6).
const (
	FiguresMeasured = "measured"
	FiguresNoData   = "no_data"
	FiguresWithheld = "withheld"
)

// TeamAnalyticsPerson is one member's analytics row, of either kind
// (PAC-129): attributed by email and kind, never by name or index.
type TeamAnalyticsPerson struct {
	Email          string     `json:"email"`
	Name           string     `json:"name"`
	Kind           string     `json:"kind"`
	UserID         *uuid.UUID `json:"userId"`
	Figures        string     `json:"figures"`
	MeetingMinutes *int       `json:"meeting_minutes"`
	FocusMinutes   *int       `json:"focus_minutes"`
	FocusScore     *int       `json:"focus_score"`
}

type memberBusyData struct {
	busy  []interval
	focus []interval
	// known is false when the member's calendar could not be read: no
	// credential, a failed client or listing (PAC-129: counted unknown; as
	// before, such a member does not block).
	known bool
}

// TeamAvailabilityEngine computes availability across team members.
type TeamAvailabilityEngine struct {
	DB          *sql.DB
	OAuthConfig *oauth2.Config
	// FreeBusy reads added people through their adding owner's credential
	// (PAC-129 §6). The engine is built once per process (newTeamHandlers),
	// so its cache is process-wide; nil builds one on first use.
	FreeBusy *FreeBusyService

	fbMu sync.Mutex
}

func (e *TeamAvailabilityEngine) freeBusy() *FreeBusyService {
	e.fbMu.Lock()
	defer e.fbMu.Unlock()
	if e.FreeBusy == nil {
		e.FreeBusy = NewFreeBusyService(e.DB, e.OAuthConfig)
	}
	return e.FreeBusy
}

// addedCoverage reads every added person over [start, end) through their
// adding owner's credential, grouped by owner. A person with no adding owner
// (spec OQ-1) is unknown with no call. The error is the budget error (503) or
// a storage failure.
func (e *TeamAvailabilityEngine) addedCoverage(ctx context.Context, added []*storage.AddedMember, start, end time.Time) (map[string]ParticipantBusy, error) {
	out := make(map[string]ParticipantBusy, len(added))
	byOwner := map[uuid.UUID][]string{}
	var owners []uuid.UUID
	db := storage.Bind(ctx, e.DB)
	for _, a := range added {
		owner, ok, err := storage.AddingOwner(db, a)
		if err != nil {
			return nil, fmt.Errorf("adding owner: %w", err)
		}
		if !ok {
			out[a.Email] = ParticipantBusy{Email: a.Email, Coverage: "unknown", Busy: []calendar.TimeSlot{}}
			continue
		}
		if _, seen := byOwner[owner]; !seen {
			owners = append(owners, owner)
		}
		byOwner[owner] = append(byOwner[owner], a.Email)
	}
	fb := e.freeBusy()
	for _, owner := range owners {
		res, err := fb.QueryForTeam(ctx, owner, byOwner[owner], start, end)
		if err != nil {
			return nil, err
		}
		for _, r := range res {
			out[r.Email] = r
		}
	}
	return out, nil
}

// FindSlots returns time slots on the given day where every team member who
// can be read is free, excluding no-meeting zones. Each slot is scored by how
// few Paceday members' focus blocks it disrupts and carries the free and
// unknown counts over members of both kinds (PAC-129).
func (e *TeamAvailabilityEngine) FindSlots(ctx context.Context, teamID uuid.UUID, day time.Time, durationMinutes int) ([]TeamSlot, error) {
	members, err := storage.ListTeamMembers(storage.Bind(ctx, e.DB), teamID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	added, err := storage.ListAddedMembers(storage.Bind(ctx, e.DB), teamID)
	if err != nil {
		return nil, fmt.Errorf("list added people: %w", err)
	}
	if len(members)+len(added) == 0 {
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
	// its own limit, does not block (and is counted unknown, PAC-129); a
	// member not read because of the budget, or cut by it, is a budget error
	// (503).
	mdata := make([]memberBusyData, len(members))
	if err := peritem.Run(ctx, len(members), peritem.Read, HostReadLimit(), func(callCtx context.Context, i int) error {
		mdata[i] = e.memberBusy(callCtx, ctx, members[i].UserID, day, dayStart, dayEnd)
		return nil
	}); err != nil {
		return nil, err
	}
	// PAC-129: added people, through their adding owner's credential only.
	cover, err := e.addedCoverage(ctx, added, dayStart, dayEnd)
	if err != nil {
		return nil, err
	}

	var allBusy []interval
	unknown := 0
	for _, md := range mdata {
		allBusy = append(allBusy, md.busy...)
		if !md.known {
			unknown++
		}
	}
	for _, a := range added {
		pb := cover[a.Email]
		if pb.Coverage != "known" {
			unknown++
		}
		// PAC-133 D-11: windows reported for an address block, even when the
		// address is unknown.
		for _, b := range pb.Busy {
			allBusy = append(allBusy, interval{b.Start, b.End})
		}
	}
	total := len(members) + len(added)

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
			slots = append(slots, TeamSlot{
				Start: cursor, End: end, QualityScore: score,
				FreeCount: total - unknown, UnknownCount: unknown, TotalCount: total,
			})
			cursor = cursor.Add(15 * time.Minute)
		}
	}

	return slots, nil
}

// memberBusy reads one member's busy time and focus blocks for the day. The
// calendar runs on ctx (the per-item deadline); the database reads on dbCtx
// (the request), so focus blocks are never left out by a slow calendar.
func (e *TeamAvailabilityEngine) memberBusy(ctx, dbCtx context.Context, userID uuid.UUID, day, dayStart, dayEnd time.Time) memberBusyData {
	db := storage.Bind(dbCtx, e.DB)
	var md memberBusyData

	// Per-member calendar access.
	token, err := auth.LoadUserTokenCtx(dbCtx, db, userID) // PAC-71: re-check the session account
	if err == nil && token != nil {
		// The member's own credential: renewal and loss are theirs (PAC-67, D-12).
		ts := auth.UserTokenSource(ctx, db, e.OAuthConfig, userID, token)
		client, err := calendar.NewClient(ctx, ts)
		if err == nil {
			ops := realOps{c: client}
			events, err := ops.listEvents(ctx, dayStart, dayEnd)
			if err == nil {
				md.known = true
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

// teamQualityScore returns 0-100: score = 100 - (members_with_disrupted_focus/total)*100.
// Focus blocks are Paceday members' only, so the divisor is the Paceday
// members (PAC-129: adding people does not move a score).
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

// GetTeamAnalytics returns the team's analytics for the week starting
// weekStart (PAC-129 contract §3.6). Added people's figures are read, through
// their adding owner's credential, only when viewerIsOwner; otherwise their
// rows are withheld and no provider call is made. Nothing about an added
// person is stored. The error is the budget error (503) or a read failure.
func (e *TeamAvailabilityEngine) GetTeamAnalytics(ctx context.Context, teamID uuid.UUID, weekStart time.Time, viewerIsOwner bool) (*TeamAnalytics, error) {
	db := storage.Bind(ctx, e.DB)
	members, err := storage.ListTeamMembers(db, teamID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	added, err := storage.ListAddedMembers(db, teamID)
	if err != nil {
		return nil, fmt.Errorf("list added people: %w", err)
	}

	var totalMeeting, totalFocus, measured int
	breakdown := make([]MemberAnalyticsSummary, 0, len(members))
	people := make([]TeamAnalyticsPerson, 0, len(members)+len(added))

	for _, m := range members {
		row, rerr := storage.GetAnalyticsWeek(db, m.UserID, weekStart)
		s := MemberAnalyticsSummary{UserID: m.UserID, Name: m.Name}
		id := m.UserID
		p := TeamAnalyticsPerson{Email: storage.NormaliseStoredAddress(m.Email), Name: m.Name, Kind: "paceday", UserID: &id, Figures: FiguresNoData}
		// SF-041: a missing week, or a failed read, is "no data", never zero.
		if rerr == nil && row != nil {
			s.MeetingMinutes = row.MeetingMinutes
			s.FocusMinutes = row.FocusMinutes
			s.FocusScore = row.FocusScore
			totalMeeting += row.MeetingMinutes
			totalFocus += row.FocusMinutes
			measured++
			meeting, focus, score := row.MeetingMinutes, row.FocusMinutes, row.FocusScore
			p.Figures, p.MeetingMinutes, p.FocusMinutes, p.FocusScore = FiguresMeasured, &meeting, &focus, &score
		}
		breakdown = append(breakdown, s)
		people = append(people, p)
	}

	var cover map[string]ParticipantBusy
	weekEnd := weekStart.AddDate(0, 0, 7)
	if viewerIsOwner && len(added) > 0 {
		if cover, err = e.addedCoverage(ctx, added, weekStart, weekEnd); err != nil {
			return nil, err
		}
	}
	for _, a := range added {
		p := TeamAnalyticsPerson{Email: a.Email, Name: a.DisplayName, Kind: "added", Figures: FiguresWithheld}
		if viewerIsOwner {
			p.Figures = FiguresNoData
			if pb := cover[a.Email]; pb.Coverage == "known" {
				minutes := busyMinutes(pb.Busy, weekStart, weekEnd)
				p.Figures, p.MeetingMinutes = FiguresMeasured, &minutes
			}
		}
		people = append(people, p)
	}

	// Averages over the Paceday members whose figures are measured (1 when
	// none); added people never count (PAC-129, SF-041).
	n := measured
	if n == 0 {
		n = 1
	}
	return &TeamAnalytics{
		WeekStart:         weekStart.Format("2006-01-02"),
		AvgMeetingMinutes: totalMeeting / n,
		AvgFocusMinutes:   totalFocus / n,
		MemberBreakdown:   breakdown,
		People:            people,
	}, nil
}

// busyMinutes is the length of the union of slots inside [start, end).
func busyMinutes(slots []calendar.TimeSlot, start, end time.Time) int {
	var ivs []interval
	for _, s := range slots {
		a, b := s.Start, s.End
		if a.Before(start) {
			a = start
		}
		if b.After(end) {
			b = end
		}
		if a.Before(b) {
			ivs = append(ivs, interval{a, b})
		}
	}
	total := time.Duration(0)
	for _, iv := range mergeIntervals(ivs) {
		total += iv.end.Sub(iv.start)
	}
	return int(total / time.Minute)
}
