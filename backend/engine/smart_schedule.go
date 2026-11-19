package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/storage"
	"golang.org/x/oauth2"
	googlecalendar "google.golang.org/api/calendar/v3"
)

type ScheduleRequest struct {
	DurationMinutes int
	Attendees       []string
	RangeStart      time.Time
	RangeEnd        time.Time
	Title           string
	Description     string
	Location        string
	// From NLP result — boost/penalize candidate slots accordingly
	PreferredTimes []string // e.g. ["10:00-12:00"]
	AvoidTimes     []string // e.g. ["08:00-09:00"]
}

type SuggestedSlot struct {
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Score   int       `json:"score"`
	Reasons []string  `json:"reasons"`
}

type ScheduleSuggestions struct {
	Slots []SuggestedSlot `json:"slots"`
	// UnknownAttendees names the requested attendees whose availability was
	// not checked (PAC-133 §3.5 item 2); always present, [] when all were.
	UnknownAttendees []string `json:"unknown_attendees"`
}

// ErrOwnCalendarUnknown means the provider could not read the requester's own
// calendar, so no slot can be offered to them (PAC-133 §3.5 item 3). It is a
// server failure (the 500), never "not connected".
var ErrOwnCalendarUnknown = errors.New("own calendar availability unknown")

// ErrOutOfHoursAllowance is CreateMeeting's policy refusal once the weekly
// out-of-hours allowance is spent. Its text is the documented 500 body of
// POST /api/schedule/create, so handlers match it with errors.Is (PAC-21).
var ErrOutOfHoursAllowance = errors.New("out-of-hours meeting allowance reached")

type SmartScheduler struct {
	DB          *sql.DB
	OAuthConfig *oauth2.Config
	calOps      calendarOps
}

func (e *SmartScheduler) calClient(ctx context.Context) (calendarOps, error) {
	if e.calOps != nil {
		return e.calOps, nil
	}
	return newCalOps(ctx, storage.Bind(ctx, e.DB), e.OAuthConfig)
}

func (e *SmartScheduler) Suggest(ctx context.Context, req ScheduleRequest) (*ScheduleSuggestions, error) {
	s, err := storage.GetOrCreateSettingsByUserCtx(ctx, e.DB, auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}

	client, err := e.calClient(ctx)
	if err != nil {
		return nil, err
	}

	ownID := client.calendarID()
	allEmails := append([]string{ownID}, req.Attendees...)
	busyMap, err := client.getFreeBusy(ctx, allEmails, req.RangeStart, req.RangeEnd)
	if err != nil {
		return nil, fmt.Errorf("freebusy: %w", err)
	}
	// Slots built on a calendar Paceday could not read are not offered to the
	// person they are for (PAC-133 §3.5 item 3): the operation's 500.
	if own := busyMap[ownID]; !own.Known {
		return nil, ErrOwnCalendarUnknown
	}

	// The busy set (D-11): every window reported for anyone, readable or
	// not, a group's members included. An unknown attendee's missing time
	// constrains nothing.
	var allBusy []interval
	for _, entry := range busyMap {
		for _, s := range entry.Busy {
			allBusy = append(allBusy, interval{s.Start, s.End})
		}
	}
	allBusy = mergeIntervals(allBusy)
	unknown := unknownAttendees(req.Attendees, busyMap)

	freeBlocks := subtractIntervals(interval{req.RangeStart, req.RangeEnd}, allBusy)

	dur := time.Duration(req.DurationMinutes) * time.Minute
	// The caller's timezone: settings.timezone (UTC by default; the settings
	// write rejects an unknown name), UTC if it still does not load. Slots are
	// filtered and scored in it, whatever offset the request carried (PAC-147).
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		loc = time.UTC
	}

	// The requester's own focus blocks over the whole requested range
	// (PAC-147: not only the 7 days from range_start); calClient uses the
	// same user. PAC-141 SF-111: a failed read is the error (the operation's
	// 500), never "no focus blocks", which would offer slots over the
	// caller's focus time.
	focusBlocks, err := storage.ListFocusBlocksForUserRangeCtx(ctx, e.DB, auth.UserIDFromContext(ctx), req.RangeStart, req.RangeEnd)
	if err != nil {
		return nil, fmt.Errorf("focus blocks: %w", err)
	}
	allowOutOfHours, err := canScheduleOutOfHours(ctx, client, s, req.RangeStart)
	if err != nil {
		return nil, fmt.Errorf("count out-of-hours meetings: %w", err)
	}

	var candidates []SuggestedSlot
	for _, free := range freeBlocks {
		t := free.start
		if t.Before(req.RangeStart) {
			t = req.RangeStart
		}
		for !t.Add(dur).After(free.end) {
			end := t.Add(dur)

			workStartRaw, workEndRaw, workEnabled := s.WorkWindow(t.In(loc))
			workStart := parseHHMM(workStartRaw, t, loc)
			workEnd := parseHHMM(workEndRaw, t, loc)
			outsideWork := !workEnabled || t.Before(workStart) || end.After(workEnd)
			if outsideWork && !allowOutOfHours {
				t = t.Add(30 * time.Minute)
				continue
			}
			lunchStartRaw, lunchEndRaw, lunchEnabled := s.LunchWindow(t.In(loc))
			if lunchEnabled {
				lunchStart := parseHHMM(lunchStartRaw, t, loc)
				lunchEnd := parseHHMM(lunchEndRaw, t, loc)
				if t.Before(lunchEnd) && end.After(lunchStart) {
					t = t.Add(30 * time.Minute)
					continue
				}
			}

			score, reasons := scoreCandidate(t.In(loc), end, focusBlocks, s)
			score, reasons = adjustScoreForPreferences(score, reasons, t, loc, req.PreferredTimes, req.AvoidTimes)
			candidates = append(candidates, SuggestedSlot{
				Start:   t,
				End:     end,
				Score:   score,
				Reasons: reasons,
			})

			t = t.Add(30 * time.Minute)
		}
	}

	sortSlotsByScore(candidates)
	top := pickTopUnique(candidates, 3, time.Hour)

	return &ScheduleSuggestions{Slots: top, UnknownAttendees: unknown}, nil
}

// unknownAttendees names every requested attendee whose outcome is unknown,
// spelled as sent, once each, in request order; [] when everyone was read
// (PAC-133 §3.5 item 2). A group is named by the group address; its members
// never by their own ids.
func unknownAttendees(attendees []string, answer map[string]calendar.FreeBusyEntry) []string {
	out := make([]string, 0)
	seen := make(map[string]bool, len(attendees))
	for _, a := range attendees {
		if seen[a] {
			continue
		}
		seen[a] = true
		if !answer[a].Known {
			out = append(out, a)
		}
	}
	return out
}

func canScheduleOutOfHours(ctx context.Context, client calendarOps, settings *storage.Settings, anchor time.Time) (bool, error) {
	if settings.OutOfHoursMeetingsPerWeek <= 0 {
		return false, nil
	}
	loc, err := time.LoadLocation(settings.Timezone)
	if err != nil {
		loc = time.UTC
	}
	weekStart := startOfWeek(anchor.In(loc))
	events, err := client.listEvents(ctx, weekStart, weekStart.AddDate(0, 0, 7))
	if err != nil {
		return false, err
	}
	outOfHours := 0
	for _, event := range events {
		start, end := parseEventTime(event)
		if start.IsZero() || end.IsZero() {
			continue
		}
		if eventIsOutsideWork(event, start, end, settings, loc) {
			outOfHours++
		}
	}
	return outOfHours < settings.OutOfHoursMeetingsPerWeek, nil
}

func eventIsOutsideWork(_ *googlecalendar.Event, start, end time.Time, settings *storage.Settings, loc *time.Location) bool {
	workStartRaw, workEndRaw, enabled := settings.WorkWindow(start.In(loc))
	if !enabled {
		return true
	}
	workStart := parseHHMM(workStartRaw, start.In(loc), loc)
	workEnd := parseHHMM(workEndRaw, start.In(loc), loc)
	return start.Before(workStart) || end.After(workEnd)
}

// scoreCandidate reads the hour and weekday from start as given: Suggest passes
// it already in the caller's timezone (PAC-147).
func scoreCandidate(start, end time.Time, focusBlocks []storage.FocusBlock, s *storage.Settings) (int, []string) {
	score := 0
	var reasons []string

	hour := start.Hour()
	wd := start.Weekday()

	if hour >= 9 && hour < 12 {
		score += 40
		reasons = append(reasons, "Morning slot preferred for important meetings")
	} else if hour >= 14 && hour < 16 {
		score += 20
		reasons = append(reasons, "Early afternoon slot")
	}

	for _, fb := range focusBlocks {
		if start.Before(fb.EndTime) && end.After(fb.StartTime) {
			score -= 20
			reasons = append(reasons, "Overlaps with focus block")
			break
		}
	}

	if wd == time.Friday && hour >= 14 {
		score -= 10
		reasons = append(reasons, "Friday afternoon — low energy")
	}

	if wd == time.Monday || wd == time.Tuesday || wd == time.Wednesday {
		score += 10
		reasons = append(reasons, "Earlier in week preferred for planning")
	}

	return score, reasons
}

func adjustScoreForPreferences(score int, reasons []string, t time.Time, loc *time.Location, preferred, avoid []string) (int, []string) {
	local := t.In(loc)
	h, m := local.Hour(), local.Minute()
	for _, r := range preferred {
		if timeInHHMMRange(h, m, r) {
			score += 25
			reasons = append(reasons, "Preferred time window")
		}
	}
	for _, r := range avoid {
		if timeInHHMMRange(h, m, r) {
			score -= 40
			reasons = append(reasons, "Avoid time window")
		}
	}
	return score, reasons
}

func timeInHHMMRange(h, m int, hhmmRange string) bool {
	parts := strings.SplitN(hhmmRange, "-", 2)
	if len(parts) != 2 {
		return false
	}
	sh, sm := parseHHMMParts(parts[0])
	eh, em := parseHHMMParts(parts[1])
	cur := h*60 + m
	return cur >= sh*60+sm && cur < eh*60+em
}

func parseHHMMParts(hhmm string) (int, int) {
	hhmm = strings.TrimSpace(hhmm)
	if len(hhmm) < 5 || hhmm[2] != ':' {
		return 0, 0
	}
	h := int(hhmm[0]-'0')*10 + int(hhmm[1]-'0')
	m := int(hhmm[3]-'0')*10 + int(hhmm[4]-'0')
	return h, m
}

func sortSlotsByScore(slots []SuggestedSlot) {
	for i := 1; i < len(slots); i++ {
		for j := i; j > 0 && slots[j].Score > slots[j-1].Score; j-- {
			slots[j], slots[j-1] = slots[j-1], slots[j]
		}
	}
}

func pickTopUnique(slots []SuggestedSlot, n int, minGap time.Duration) []SuggestedSlot {
	result := make([]SuggestedSlot, 0, n)
	for _, s := range slots {
		if len(result) >= n {
			break
		}
		tooClose := false
		for _, picked := range result {
			diff := s.Start.Sub(picked.Start)
			if diff < 0 {
				diff = -diff
			}
			if diff < minGap {
				tooClose = true
				break
			}
		}
		if !tooClose {
			result = append(result, s)
		}
	}
	return result
}

func (e *SmartScheduler) CreateMeeting(ctx context.Context, req ScheduleRequest, slot SuggestedSlot) (*googlecalendar.Event, error) {
	client, err := e.calClient(ctx)
	if err != nil {
		return nil, err
	}

	settings, err := storage.GetOrCreateSettingsByUserCtx(ctx, e.DB, auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}
	loc, err := time.LoadLocation(settings.Timezone)
	if err != nil {
		loc = time.UTC
	}
	if eventIsOutsideWork(nil, slot.Start, slot.End, settings, loc) {
		allowed, err := canScheduleOutOfHours(ctx, client, settings, slot.Start)
		if err != nil {
			return nil, fmt.Errorf("count out-of-hours meetings: %w", err)
		}
		if !allowed {
			return nil, ErrOutOfHoursAllowance
		}
	}

	attendees := make([]*googlecalendar.EventAttendee, len(req.Attendees))
	for i, email := range req.Attendees {
		attendees[i] = &googlecalendar.EventAttendee{Email: email}
	}

	event := &googlecalendar.Event{
		Summary:     req.Title,
		Description: req.Description,
		Location:    req.Location,
		Start:       &googlecalendar.EventDateTime{DateTime: slot.Start.Format(time.RFC3339)},
		End:         &googlecalendar.EventDateTime{DateTime: slot.End.Format(time.RFC3339)},
		Attendees:   attendees,
	}

	created, err := client.createEvent(ctx, event)
	if err != nil {
		return nil, err
	}

	storage.WriteAuditLogCtx(ctx, e.DB, auth.UserIDFromContext(ctx), "meeting_scheduled", storage.AuditJSON(map[string]string{"event_id": created.Id, "title": req.Title}))
	return created, nil
}
