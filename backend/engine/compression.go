package engine

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/storage"
	"golang.org/x/oauth2"
	googlecalendar "google.golang.org/api/calendar/v3"
)

type MoveProposal struct {
	EventID          string    `json:"eventId"`
	EventTitle       string    `json:"eventTitle"`
	CurrentStart     time.Time `json:"currentStart"`
	CurrentEnd       time.Time `json:"currentEnd"`
	ProposedStart    time.Time `json:"proposedStart"`
	ProposedEnd      time.Time `json:"proposedEnd"`
	Reason           string    `json:"reason"`
	FocusGainMinutes int       `json:"focusGainMinutes"`
}

type CompressionResult struct {
	Date                  string         `json:"date"`
	Proposals             []MoveProposal `json:"proposals"`
	TotalFocusGainMinutes int            `json:"totalFocusGainMinutes"`
}

type CompressionEngine struct {
	DB          *sql.DB
	OAuthConfig *oauth2.Config
	calOps      calendarOps
}

func (e *CompressionEngine) calClient(ctx context.Context) (calendarOps, error) {
	if e.calOps != nil {
		return e.calOps, nil
	}
	return newCalOps(ctx, e.DB, e.OAuthConfig)
}

// CheckCalendar fails exactly when SuggestForDay and Apply would fail to open
// the caller's calendar: ErrCalendarNotConnected when there is no stored
// credential, ErrCredentialStore when it cannot be read. The preview runs it
// once, before any day is computed, so week mode cannot turn either into an
// empty success (PAC-21).
func (e *CompressionEngine) CheckCalendar(ctx context.Context) error {
	_, err := e.calClient(ctx)
	return err
}

func (e *CompressionEngine) SuggestForDay(ctx context.Context, date time.Time) (*CompressionResult, error) {
	// The user whose calendar calClient opens.
	s, err := storage.GetOrCreateSettingsByUserCtx(ctx, e.DB, auth.UserIDFromContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}

	client, err := e.calClient(ctx)
	if err != nil {
		return nil, err
	}

	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		loc = time.UTC
	}
	workStartRaw, workEndRaw, workEnabled := s.WorkWindow(date.In(loc))
	lunchStartRaw, lunchEndRaw, lunchEnabled := s.LunchWindow(date.In(loc))
	result := &CompressionResult{Date: date.Format("2006-01-02"), Proposals: make([]MoveProposal, 0)}
	if !workEnabled {
		return result, nil
	}

	dayStart := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc)
	dayEnd := dayStart.Add(24 * time.Hour)

	events, err := client.listEvents(ctx, dayStart, dayEnd)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}

	workStart := parseHHMM(workStartRaw, date, loc)
	workEnd := parseHHMM(workEndRaw, date, loc)

	moveable, fixed := classifyEvents(events, workStart, workEnd)

	currentBusy := toIntervals(append(moveable, fixed...))
	if lunchEnabled {
		ls := parseHHMM(lunchStartRaw, date, loc)
		le := parseHHMM(lunchEndRaw, date, loc)
		currentBusy = append(currentBusy, interval{ls, le})
	}
	currentFree := subtractIntervals(interval{workStart, workEnd}, mergeIntervals(currentBusy))
	currentLargest := largestSlot(currentFree)

	for _, mv := range moveable {
		dur := mv.end.Sub(mv.start)
		attendeeEmails := extractAttendees(mv)

		otherFixed := toIntervals(fixed)
		otherMoveable := toIntervalsExcept(moveable, mv.event.Id)
		if lunchEnabled {
			ls := parseHHMM(lunchStartRaw, date, loc)
			le := parseHHMM(lunchEndRaw, date, loc)
			otherFixed = append(otherFixed, interval{ls, le})
		}

		candidates := generateAdjacentPositions(otherMoveable, otherFixed, workStart, workEnd, dur)

		// A position listed twice (two neighbours ending or starting at the
		// same time) is evaluated once: one check per position (PAC-133 §2.4).
		evaluated := make(map[int64]bool, len(candidates))
		for _, candidate := range candidates {
			if candidate == mv.start || evaluated[candidate.UnixNano()] {
				continue
			}
			evaluated[candidate.UnixNano()] = true

			proposedEnd := candidate.Add(dur)
			if proposedEnd.After(workEnd) {
				continue
			}

			if overlapsWith(interval{candidate, proposedEnd}, append(otherMoveable, otherFixed...)) {
				continue
			}

			// PAC-133 §2.4: a position is proposed only if every attendee
			// is readable and no reported window (anyone's, a group's
			// members' included) overlaps it. A failed check, or an unknown
			// attendee, rejects the position like a busy one: at most one
			// free/busy call per evaluated candidate.
			if len(attendeeEmails) > 0 {
				busy, err := client.getFreeBusy(ctx, attendeeEmails, candidate, proposedEnd)
				if err != nil || anyUnknown(busy) || hasConflict(busy, []interval{{candidate, proposedEnd}}) {
					continue
				}
			}

			allBusy := append(otherMoveable, otherFixed...)
			allBusy = append(allBusy, interval{candidate, proposedEnd})
			newFree := subtractIntervals(interval{workStart, workEnd}, mergeIntervals(allBusy))
			newLargest := largestSlot(newFree)
			gain := int((newLargest - currentLargest).Minutes())

			if gain > 30 {
				result.Proposals = append(result.Proposals, MoveProposal{
					EventID:          mv.event.Id,
					EventTitle:       mv.event.Summary,
					CurrentStart:     mv.start,
					CurrentEnd:       mv.end,
					ProposedStart:    candidate,
					ProposedEnd:      proposedEnd,
					Reason:           fmt.Sprintf("Creates %d min larger focus block", gain),
					FocusGainMinutes: gain,
				})
				result.TotalFocusGainMinutes += gain
				break
			}
		}
	}

	return result, nil
}

// Failure reasons in Apply's failed list (CompressionApplyResult.failed), after
// "<event_id>: ".
const (
	reasonAttendeeConflict   = "attendee conflict at proposed time"
	reasonAttendeeUnverified = "attendee availability unverified"
)

// Apply moves each proposal's event after re-checking its real attendees
// (PAC-133 §2.5): read the event once; its non-self, non-declined attendees;
// none → move with no free/busy call; else one call for them over the
// proposed interval, checked over the proposed interval minus the event's
// current one. A reported window there is a conflict; otherwise a failed call
// or an unknown attendee is unverified (conflict wins); else the move.
func (e *CompressionEngine) Apply(ctx context.Context, proposals []MoveProposal) ([]string, []string, error) {
	client, err := e.calClient(ctx)
	if err != nil {
		return nil, nil, err
	}

	// The subject of every move is the caller (PAC-23); read it once.
	subject := auth.UserIDFromContext(ctx)
	var applied, failed []string
	for _, p := range proposals {
		existing, err := client.getEvent(ctx, p.EventID)
		if err != nil {
			failed = append(failed, p.EventID+": "+err.Error())
			continue
		}

		if attendees := extractAttendees(eventInterval{event: existing}); len(attendees) > 0 {
			check := []interval{{p.ProposedStart, p.ProposedEnd}}
			if cs, ce, ok := timedBounds(existing); ok {
				check = subtractIntervals(check[0], []interval{{cs, ce}})
			}
			busy, err := client.getFreeBusy(ctx, attendees, p.ProposedStart, p.ProposedEnd)
			switch {
			case hasConflict(busy, check):
				failed = append(failed, p.EventID+": "+reasonAttendeeConflict)
				continue
			case err != nil || anyUnknown(busy):
				failed = append(failed, p.EventID+": "+reasonAttendeeUnverified)
				continue
			}
		}

		existing.Start = &googlecalendar.EventDateTime{DateTime: p.ProposedStart.Format(time.RFC3339)}
		existing.End = &googlecalendar.EventDateTime{DateTime: p.ProposedEnd.Format(time.RFC3339)}

		if _, err := client.updateEvent(ctx, p.EventID, existing); err != nil {
			failed = append(failed, p.EventID+": "+err.Error())
			continue
		}

		storage.WriteAuditLogCtx(ctx, e.DB, subject, "meeting_moved", storage.AuditJSON(map[string]string{"event_id": p.EventID, "new_start": p.ProposedStart.Format(time.RFC3339)}))
		applied = append(applied, p.EventID)
	}

	return applied, failed, nil
}

// timedBounds is an event's current interval; an event with no timed start
// and end (all-day, or unparseable) has none.
func timedBounds(ev *googlecalendar.Event) (time.Time, time.Time, bool) {
	if ev.Start == nil || ev.End == nil || ev.Start.DateTime == "" || ev.End.DateTime == "" {
		return time.Time{}, time.Time{}, false
	}
	start, err1 := time.Parse(time.RFC3339, ev.Start.DateTime)
	end, err2 := time.Parse(time.RFC3339, ev.End.DateTime)
	if err1 != nil || err2 != nil || !end.After(start) {
		return time.Time{}, time.Time{}, false
	}
	return start, end, true
}

type eventInterval struct {
	event *googlecalendar.Event
	start time.Time
	end   time.Time
}

func classifyEvents(events []*googlecalendar.Event, workStart, workEnd time.Time) ([]eventInterval, []eventInterval) {
	var moveable, fixed []eventInterval
	for _, ev := range events {
		if ev.Start == nil || ev.End == nil || ev.Start.DateTime == "" {
			continue
		}
		start, err1 := time.Parse(time.RFC3339, ev.Start.DateTime)
		end, err2 := time.Parse(time.RFC3339, ev.End.DateTime)
		if err1 != nil || err2 != nil {
			continue
		}
		if end.Before(workStart) || start.After(workEnd) {
			continue
		}
		ei := eventInterval{event: ev, start: start, end: end}
		if ev.Organizer != nil && ev.Organizer.Self && ev.Transparency != "transparent" {
			moveable = append(moveable, ei)
		} else {
			fixed = append(fixed, ei)
		}
	}
	return moveable, fixed
}

func toIntervals(eis []eventInterval) []interval {
	ivs := make([]interval, len(eis))
	for i, ei := range eis {
		ivs[i] = interval{ei.start, ei.end}
	}
	return ivs
}

func toIntervalsExcept(eis []eventInterval, excludeID string) []interval {
	var ivs []interval
	for _, ei := range eis {
		if ei.event.Id != excludeID {
			ivs = append(ivs, interval{ei.start, ei.end})
		}
	}
	return ivs
}

func extractAttendees(ei eventInterval) []string {
	var emails []string
	for _, a := range ei.event.Attendees {
		if !a.Self && a.ResponseStatus != "declined" {
			emails = append(emails, a.Email)
		}
	}
	return emails
}

func generateAdjacentPositions(others []interval, fixed []interval, workStart, workEnd time.Time, dur time.Duration) []time.Time {
	var positions []time.Time
	positions = append(positions, workStart)

	for _, oi := range others {
		positions = append(positions, oi.end)
		t := oi.start.Add(-dur)
		if !t.Before(workStart) {
			positions = append(positions, t)
		}
	}
	for _, fi := range fixed {
		positions = append(positions, fi.end)
		t := fi.start.Add(-dur)
		if !t.Before(workStart) {
			positions = append(positions, t)
		}
	}
	_ = workEnd
	return positions
}

func overlapsWith(iv interval, others []interval) bool {
	for _, o := range others {
		if iv.start.Before(o.end) && iv.end.After(o.start) {
			return true
		}
	}
	return false
}

// hasConflict reports whether any reported window, of any address and any
// outcome (a group's members' included in its Busy), overlaps one of the
// intervals (D-11: reported windows block whatever the outcome).
func hasConflict(busy map[string]calendar.FreeBusyEntry, within []interval) bool {
	for _, entry := range busy {
		for _, s := range entry.Busy {
			for _, iv := range within {
				if iv.start.Before(s.End) && iv.end.After(s.Start) {
					return true
				}
			}
		}
	}
	return false
}

// anyUnknown reports whether any address in the answer is not readable.
func anyUnknown(busy map[string]calendar.FreeBusyEntry) bool {
	for _, entry := range busy {
		if !entry.Known {
			return true
		}
	}
	return false
}

func largestSlot(slots []interval) time.Duration {
	var max time.Duration
	for _, s := range slots {
		d := s.end.Sub(s.start)
		if d > max {
			max = d
		}
	}
	return max
}
