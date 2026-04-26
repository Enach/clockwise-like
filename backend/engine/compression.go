package engine

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Enach/clockwise-like/backend/calendar"
	"github.com/Enach/clockwise-like/backend/storage"
	googlecalendar "google.golang.org/api/calendar/v3"
	"golang.org/x/oauth2"
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

func (e *CompressionEngine) SuggestForDay(ctx context.Context, date time.Time) (*CompressionResult, error) {
	s, err := storage.GetSettings(e.DB)
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

	dayStart := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc)
	dayEnd := dayStart.Add(24 * time.Hour)

	events, err := client.listEvents(ctx, dayStart, dayEnd)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}

	workStart := parseHHMM(s.WorkStart, date, loc)
	workEnd := parseHHMM(s.WorkEnd, date, loc)

	moveable, fixed := classifyEvents(events, workStart, workEnd)

	currentBusy := toIntervals(append(moveable, fixed...))
	if s.ProtectLunch && s.LunchStart != "" && s.LunchEnd != "" {
		ls := parseHHMM(s.LunchStart, date, loc)
		le := parseHHMM(s.LunchEnd, date, loc)
		currentBusy = append(currentBusy, interval{ls, le})
	}
	currentFree := subtractIntervals(interval{workStart, workEnd}, mergeIntervals(currentBusy))
	currentLargest := largestSlot(currentFree)

	result := &CompressionResult{Date: date.Format("2006-01-02")}

	for _, mv := range moveable {
		dur := mv.end.Sub(mv.start)
		attendeeEmails := extractAttendees(mv)

		otherFixed := toIntervals(fixed)
		otherMoveable := toIntervalsExcept(moveable, mv.event.Id)
		if s.ProtectLunch && s.LunchStart != "" && s.LunchEnd != "" {
			ls := parseHHMM(s.LunchStart, date, loc)
			le := parseHHMM(s.LunchEnd, date, loc)
			otherFixed = append(otherFixed, interval{ls, le})
		}

		candidates := generateAdjacentPositions(otherMoveable, otherFixed, workStart, workEnd, dur)

		for _, candidate := range candidates {
			if candidate == mv.start {
				continue
			}

			proposedEnd := candidate.Add(dur)
			if proposedEnd.After(workEnd) {
				continue
			}

			if overlapsWith(interval{candidate, proposedEnd}, append(otherMoveable, otherFixed...)) {
				continue
			}

			if len(attendeeEmails) > 0 {
				busy, err := client.getFreeBusy(ctx, attendeeEmails, candidate, proposedEnd)
				if err != nil || hasConflict(busy, candidate, proposedEnd) {
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

func (e *CompressionEngine) Apply(ctx context.Context, proposals []MoveProposal) ([]string, []string, error) {
	client, err := e.calClient(ctx)
	if err != nil {
		return nil, nil, err
	}

	var applied, failed []string
	for _, p := range proposals {
		busy, err := client.getFreeBusy(ctx, nil, p.ProposedStart, p.ProposedEnd)
		if err == nil && hasConflict(busy, p.ProposedStart, p.ProposedEnd) {
			failed = append(failed, p.EventID+": attendee conflict at proposed time")
			continue
		}

		existing, err := client.getEvent(ctx, p.EventID)
		if err != nil {
			failed = append(failed, p.EventID+": "+err.Error())
			continue
		}

		existing.Start = &googlecalendar.EventDateTime{DateTime: p.ProposedStart.Format(time.RFC3339)}
		existing.End = &googlecalendar.EventDateTime{DateTime: p.ProposedEnd.Format(time.RFC3339)}

		if _, err := client.updateEvent(ctx, p.EventID, existing); err != nil {
			failed = append(failed, p.EventID+": "+err.Error())
			continue
		}

		storage.WriteAuditLog(e.DB, "meeting_moved", `{"event_id":"`+p.EventID+`","new_start":"`+p.ProposedStart.Format(time.RFC3339)+`"}`)
		applied = append(applied, p.EventID)
	}

	return applied, failed, nil
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

func hasConflict(busy map[string][]calendar.TimeSlot, start, end time.Time) bool {
	for _, slots := range busy {
		for _, s := range slots {
			if start.Before(s.End) && end.After(s.Start) {
				return true
			}
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
