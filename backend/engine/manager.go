package engine

import (
	"context"
	"database/sql"
	"errors"
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

type DetectedCandidate struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
}

type DetectResult struct {
	MembersAdded   int
	MembersUpdated int
	IsManager      bool
	Candidates     []DetectedCandidate
	ScannedAt      time.Time
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
	token, err := LoadCalendarTokenCtx(ctx, e.DB, managerID)
	if err != nil {
		return nil, err
	}
	ts := auth.UserTokenSource(ctx, e.DB, e.OAuthConfig, managerID, token)
	client, err := calendar.NewClient(ctx, ts)
	if err != nil {
		return nil, fmt.Errorf("calendar client: %w", err)
	}

	now := e.now()
	events, err := client.ListEvents(ctx, "primary", now.Add(-42*24*time.Hour), now.Add(42*24*time.Hour))
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}

	manager, err := storage.GetUserByIDNoOrg(e.DB, managerID)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("load manager: %w", err)
	}
	managerEmail := ""
	if manager != nil {
		managerEmail = manager.Email
	}
	byEmail := collectOneOnOneCandidates(events, managerEmail)

	// PAC-80 item 2: for a request, the default row is created only under
	// the session re-check; the scheduled job (no session) is unchanged.
	profile, err := storage.GetOrCreateUserProfileCtx(ctx, e.DB, managerID)
	if err != nil {
		return nil, fmt.Errorf("load manager profile: %w", err)
	}
	result := &DetectResult{ScannedAt: e.now().UTC(), Candidates: []DetectedCandidate{}}

	// Team members are named by position in errors, never by address: an
	// error here is logged and reported (PAC-122).
	n := 0
	for _, c := range byEmail {
		n++
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

		// PAC-130 (spec D-2, contract md §3.3): detection never looks an
		// address up to find an account and stores no account link. Whether
		// the person is "On Paceday" is decided at read time, from the
		// manager's teams and organisation (DecideMember).
		m := &storage.ManagerTeamMember{
			ManagerUserID:  managerID,
			MemberEmail:    c.email,
			DisplayName:    c.displayName,
			Source:         "auto",
			Cadence:        cadence,
			LastOneOnOneAt: lastOOOPtr,
		}
		result.Candidates = append(result.Candidates, DetectedCandidate{Email: c.email, DisplayName: c.displayName})

		existing, lookupErr := storage.GetManagerTeamMemberByEmail(e.DB, managerID, c.email)
		if lookupErr == sql.ErrNoRows {
			result.MembersAdded++
		} else if lookupErr != nil {
			return nil, fmt.Errorf("lookup detected member #%d: %w", n, lookupErr)
		} else if existing != nil {
			result.MembersUpdated++
		}
		if err := storage.UpsertManagerTeamMemberCtx(ctx, e.DB, m); err != nil {
			return nil, fmt.Errorf("persist detected member #%d: %w", n, err)
		}

		// Record occurrences
		for _, occ := range c.occurrences {
			if err := storage.UpsertOneOnOneOccurrenceCtx(ctx, e.DB, managerID, c.email, occ.eventID, occ.occurredAt); err != nil {
				return nil, fmt.Errorf("persist occurrence for detected member #%d: %w", n, err)
			}
		}
	}

	if result.MembersAdded+result.MembersUpdated > 0 {
		now2 := e.now().UTC()
		profile.IsManager = true
		profile.DetectedAt = &now2
		result.IsManager = true
		if err := storage.UpsertUserProfileCtx(ctx, e.DB, profile); err != nil {
			return nil, fmt.Errorf("persist manager profile: %w", err)
		}
	}

	sort.Slice(result.Candidates, func(i, j int) bool {
		return result.Candidates[i].Email < result.Candidates[j].Email
	})
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

// GetGaps returns the members of the given roster whose 1:1 cadence is at
// risk or overdue. The caller chooses the roster: the manager's global one, or
// a team's, whose cadences can differ from the global ones (PAC-54).
func (e *ManagerEngine) GetGaps(ctx context.Context, managerID uuid.UUID, members []*storage.ManagerTeamMember) ([]CadenceGap, error) {
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
		// PAC-122 SF-040: a failed read is the error, never a false gap.
		future, err := e.hasFutureEvent(ctx, managerID, m.MemberEmail, now, now.Add(14*24*time.Hour))
		if err != nil {
			return nil, err
		}
		if future {
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

// nextExpectedDate returns the date the next 1:1 with m should occur, or the
// zero time when m has no cadence. A member never met is due now (PAC-54).
// Uses calendar-month arithmetic for "monthly" cadence; day-based addition for the rest.
func (e *ManagerEngine) nextExpectedDate(m *storage.ManagerTeamMember) time.Time {
	if m.LastOneOnOneAt == nil {
		if !hasCadence(m) {
			return time.Time{}
		}
		return e.now().UTC()
	}
	base := *m.LastOneOnOneAt
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

func hasCadence(m *storage.ManagerTeamMember) bool {
	switch m.Cadence {
	case "weekly", "biweekly", "monthly":
		return true
	case "custom":
		return m.CadenceCustomDays != nil && *m.CadenceCustomDays > 0
	}
	return false
}

func (e *ManagerEngine) hasFutureEvent(_ context.Context, managerID uuid.UUID, memberEmail string, from, to time.Time) (bool, error) {
	var count int
	err := e.DB.QueryRow(`
		SELECT COUNT(*) FROM one_on_one_occurrences
		WHERE manager_user_id=$1 AND member_email=$2
		  AND occurred_at >= $3 AND occurred_at < $4`,
		managerID, memberEmail, from, to,
	).Scan(&count)
	err = managerStepErr(StepFutureOccurrences, memberEmail, err)
	if err != nil {
		return false, fmt.Errorf("gap check: future occurrences: %w", err)
	}
	return count > 0, nil
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

// MemberBranch is how one roster entry's weeks are answered for the caller
// (PAC-130 spec §3.4, D-7; contract md §2.2). It is decided once per entry per
// request, before any week is read.
type MemberBranch int

const (
	// BranchUnmeasured is a person added to a team under PAC-129: never
	// measured, nothing read or stored, not "On Paceday".
	BranchUnmeasured MemberBranch = iota
	// BranchFigures is a visible person whose account the caller may read:
	// their stored weekly analytics.
	BranchFigures
	// BranchNoRead is a visible person the caller may not read: "no data",
	// nothing read, nothing served from or written to the cache.
	BranchNoRead
	// BranchEstimate is an address the caller cannot already see on Paceday:
	// free/busy through the caller's own credential, exactly as for an
	// address nobody holds.
	BranchEstimate
)

// MemberDecision is the branch of one roster entry for one caller, and the
// account its figures belong to (BranchFigures only).
type MemberDecision struct {
	Branch  MemberBranch
	Account uuid.UUID
}

// IsPacedayUser is the wire's is_paceday_user: "visible on Paceday" to the
// caller (spec D-1, D-5), true on the figures and no-read branches.
func (d *MemberDecision) IsPacedayUser() bool {
	return d.Branch == BranchFigures || d.Branch == BranchNoRead
}

// DecideMember decides member's branch for managerID (contract md §2.2), in
// this order, each read firing the step seam after it runs:
//  1. source "added": unmeasured (PAC-129);
//  2. visibility: a "formal" entry (a Paceday member of the roster's team,
//     known through that membership) is visible by construction and reads
//     nothing; any other entry runs the one visibility statement
//     (storage.AddressVisibleTo, step visibility-read). Not visible: estimate;
//  3. the known account: a formal entry's own member, at any standing (as
//     before PAC-130); otherwise the single domain-confirmed holder of the
//     address (storage.ConfirmedHolder, PAC-71 granting site, step
//     holder-lookup). None: no read;
//  4. PAC-60: the caller owns a team the account is a member of (step
//     owner-check). Not: no read;
//  5. consent (step consent-read): no user_profiles row reads as NOT shared,
//     a NULL column as shared, as before. Withheld: no read;
//  6. otherwise figures.
//
// A stored account link (manager_team_members.member_user_id) is never read:
// only a formal entry, built in memory from team membership, names its
// account. Every failed read is the error (spec D-9): never "not visible",
// never an estimate, never "no data".
func (e *ManagerEngine) DecideMember(_ context.Context, managerID uuid.UUID, member *storage.ManagerTeamMember) (*MemberDecision, error) {
	if member.Source == "added" {
		return &MemberDecision{Branch: BranchUnmeasured}, nil
	}
	var account uuid.UUID
	if member.Source == "formal" && member.MemberUserID != nil {
		account = *member.MemberUserID
	} else {
		visible, err := storage.AddressVisibleTo(e.DB, managerID, member.MemberEmail)
		err = managerStepErr(StepVisibilityRead, member.MemberEmail, err)
		if err != nil {
			return nil, fmt.Errorf("member decision: visibility read: %w", err)
		}
		if !visible {
			return &MemberDecision{Branch: BranchEstimate}, nil
		}
		holder, ok, err := storage.ConfirmedHolder(e.DB, member.MemberEmail)
		err = managerStepErr(StepHolderLookup, member.MemberEmail, err)
		if err != nil {
			return nil, fmt.Errorf("member decision: holder lookup: %w", err)
		}
		if !ok {
			return &MemberDecision{Branch: BranchNoRead}, nil
		}
		account = holder
	}

	// PAC-60: a Paceday user's minutes are visible only to an owner of a team
	// that user is on.
	allowed, err := storage.OwnsTeamWithMember(e.DB, managerID, account)
	err = managerStepErr(StepOwnerCheck, member.MemberEmail, err)
	if err != nil {
		return nil, fmt.Errorf("member decision: owner check: %w", err)
	}
	if !allowed {
		return &MemberDecision{Branch: BranchNoRead}, nil
	}

	// Consent, decided before the cache (contract md §2.2): no profile row
	// reads as not shared, as before; a NULL column as shared.
	var shared bool
	err = e.DB.QueryRow(`
		SELECT COALESCE(analytics_shared_with_manager, true)
		FROM user_profiles WHERE user_id=$1`, account,
	).Scan(&shared)
	err = managerStepErr(StepConsentRead, member.MemberEmail, err)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("member decision: consent read: %w", err)
	}
	if !shared {
		return &MemberDecision{Branch: BranchNoRead}, nil
	}
	return &MemberDecision{Branch: BranchFigures, Account: account}, nil
}

// MemberWeeks is one roster entry's answer for a caller: whether the entry is
// "On Paceday" to them, and its weeks in the order asked.
type MemberWeeks struct {
	IsPacedayUser bool
	Weeks         []*MemberWeekStats
}

// MemberWeeks decides member's branch once and reads every week of weekStarts
// on it, in order. A failed read stops at once: nothing is cached for the
// failing week or any after it.
func (e *ManagerEngine) MemberWeeks(ctx context.Context, managerID uuid.UUID, member *storage.ManagerTeamMember, weekStarts []time.Time) (*MemberWeeks, error) {
	d, err := e.DecideMember(ctx, managerID, member)
	if err != nil {
		return nil, err
	}
	out := &MemberWeeks{IsPacedayUser: d.IsPacedayUser(), Weeks: make([]*MemberWeekStats, 0, len(weekStarts))}
	for _, ws := range weekStarts {
		stats, err := e.MemberWeek(ctx, managerID, member, d, ws)
		if err != nil {
			return nil, err
		}
		out.Weeks = append(out.Weeks, stats)
	}
	return out, nil
}

// GetMemberWeek is MemberWeeks for one week: the branch is decided, then the
// week is read on it.
func (e *ManagerEngine) GetMemberWeek(ctx context.Context, managerID uuid.UUID, member *storage.ManagerTeamMember, weekStart time.Time) (*MemberWeekStats, error) {
	res, err := e.MemberWeeks(ctx, managerID, member, []time.Time{weekStart})
	if err != nil {
		return nil, err
	}
	return res.Weeks[0], nil
}

// MemberWeek reads one week of member on the branch d (contract md §2.2).
// The analytics cache's is_paceday_user column is the branch marker of a
// cached row: the figures branch serves and writes only rows marked true, the
// estimate branch only rows marked false, and the no-read branch never reads
// or writes the cache. So a figure computed on one branch is never served on
// another (spec §3.4): not after the person leaves the caller's teams, not
// from a row cached before PAC-60, and an estimate never once they are
// visible.
//
// PAC-122 SF-040: every read that fails is returned as the error (the
// handlers answer the 500); nothing computed from a failed read is cached,
// and a failed cache write is the error too.
func (e *ManagerEngine) MemberWeek(ctx context.Context, managerID uuid.UUID, member *storage.ManagerTeamMember, d *MemberDecision, weekStart time.Time) (*MemberWeekStats, error) {
	if d.Branch != BranchFigures && d.Branch != BranchEstimate {
		// Unmeasured (PAC-129) and no read: "no data", nothing read or stored.
		return &MemberWeekStats{}, nil
	}
	isPacedayUser := d.Branch == BranchFigures

	// Check cache (4h TTL), on this branch only.
	cached, err := storage.GetTeamAnalytics(e.DB, managerID, member.MemberEmail, weekStart)
	err = managerStepErr(StepCacheRead, member.MemberEmail, err)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("member week: cache read: %w", err)
	}
	if err == nil && cached.IsPacedayUser == isPacedayUser && time.Since(cached.ComputedAt) < analyticsCache4h {
		return &MemberWeekStats{
			FocusMinutes:   cached.FocusMinutes,
			MeetingMinutes: cached.MeetingMinutes,
			FreeMinutes:    cached.FreeMinutes,
			DataAvailable:  cached.DataAvailable,
		}, nil
	}

	var stats MemberWeekStats
	// cacheable is false for an answer that is not a fact about the week: an
	// address whose free/busy coverage is not known (a provider failure
	// degrades to unknown, PAC-67 D-6). Such an answer is shown as "no data"
	// but never cached, so the next read asks again (SF-040).
	cacheable := true

	if isPacedayUser {
		var focusMin, meetMin, freeMin int
		err := e.DB.QueryRow(`
			SELECT COALESCE(focus_minutes,0), COALESCE(meeting_minutes,0), COALESCE(free_minutes,0)
			FROM analytics_weeks
			WHERE user_id=$1 AND week_start=$2`,
			d.Account, weekStart.Format("2006-01-02"),
		).Scan(&focusMin, &meetMin, &freeMin)
		err = managerStepErr(StepAnalyticsWeekRead, member.MemberEmail, err)
		switch {
		case err == nil:
			stats.FocusMinutes = focusMin
			stats.MeetingMinutes = meetMin
			stats.FreeMinutes = freeMin
			stats.DataAvailable = true
		case !errors.Is(err, sql.ErrNoRows):
			return nil, fmt.Errorf("member week: analytics read: %w", err)
		}
	} else {
		// Not visible: free/busy through the caller's own credential, the
		// same for every such address, account or not (spec D-3).
		fbSvc := NewFreeBusyService(e.DB, e.OAuthConfig)
		weekEnd := weekStart.Add(7 * 24 * time.Hour)
		results, err := fbSvc.Query(ctx, managerID, []string{member.MemberEmail}, weekStart, weekEnd)
		err = managerStepErr(StepFreeBusy, member.MemberEmail, err)
		if err != nil {
			return nil, fmt.Errorf("member week: free/busy: %w", err)
		}
		cacheable = false
		if len(results) > 0 {
			r := results[0]
			if r.Coverage == "known" {
				cacheable = true
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

	if !cacheable {
		return &stats, nil
	}
	// Cache the result. The step seam, when it fails the write, stands in
	// for it (nothing is written).
	if err = managerStepErr(StepCacheWrite, member.MemberEmail, nil); err == nil {
		err = storage.UpsertTeamAnalyticsCtx(ctx, e.DB, &storage.TeamAnalyticsRow{
			ManagerUserID:  managerID,
			MemberEmail:    member.MemberEmail,
			WeekStart:      weekStart,
			FocusMinutes:   stats.FocusMinutes,
			MeetingMinutes: stats.MeetingMinutes,
			FreeMinutes:    stats.FreeMinutes,
			IsPacedayUser:  isPacedayUser,
			DataAvailable:  stats.DataAvailable,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("member week: cache write: %w", err)
	}

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
