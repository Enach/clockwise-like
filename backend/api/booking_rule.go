package api

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// The offerable-start rule (PAC-72), revision 2: the link's zone is its
// owner's profile time zone (D-9), its horizon is the link's own setting
// (D-1), and the refusal body is the link owner's choice (D-2/D-5).

// ownerZoneReader reads a user's stored zone without creating settings.
// found is false when the user has no settings row.
type ownerZoneReader func(db storage.DBTX, userID uuid.UUID) (tz string, found bool, err error)

// linkLocation is the zone a link's days, hours, grid, "today" and horizon
// are evaluated in: the owner's profile time zone, read now, never stored on
// the link (A-7). No settings row, an empty name or one that does not load is
// UTC (A-4). A failed read is the error: the caller answers 500 and never
// falls back to UTC (revision 2.1, D-7).
func linkLocation(db storage.DBTX, read ownerZoneReader, ownerID uuid.UUID) (*time.Location, error) {
	if read == nil {
		read = storage.GetUserTimezone
	}
	tz, found, err := read(db, ownerID)
	if err != nil {
		return nil, err
	}
	if !found || tz == "" || tz == "Local" {
		// "Local" would load the server process's zone, which must have
		// no effect (AC-28).
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC, nil
	}
	return loc, nil
}

// calendarDay is the calendar date of t in loc, at noon there: a time on that
// date whose wall clock always exists, so Date and Weekday are the zone's.
func calendarDay(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 12, 0, 0, 0, loc)
}

// parseCalendarDay reads an explicit YYYY-MM-DD listing date as that day in
// loc (AC-33).
func parseCalendarDay(s string, loc *time.Location) (time.Time, error) {
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, loc), nil
}

// daysBetween is the number of calendar days from a to b (both calendar
// days, any zones): negative when b is before a.
func daysBetween(a, b time.Time) int {
	ua := time.Date(a.Year(), a.Month(), a.Day(), 0, 0, 0, 0, time.UTC)
	ub := time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, time.UTC)
	return int(ub.Sub(ua) / (24 * time.Hour))
}

// horizonDays is the link's horizon, 60 when unset.
func horizonDays(link *storage.SchedulingLink) int {
	if link.BookingHorizonDays <= 0 {
		return storage.DefaultBookingHorizonDays
	}
	return link.BookingHorizonDays
}

// inHorizon reports whether day is today (in the link's zone) or one of the
// next horizon-1 days.
func inHorizon(link *storage.SchedulingLink, today, day time.Time) bool {
	n := daysBetween(today, day)
	return n >= 0 && n < horizonDays(link)
}

// linkOpenOn reports whether day (a calendar day in the link's zone) is one of
// the link's days of the week (0 = Sunday).
func linkOpenOn(link *storage.SchedulingLink, day time.Time) bool {
	for _, d := range link.DaysOfWeek {
		if d == int(day.Weekday()) {
			return true
		}
	}
	return false
}

// offeredSlots is the offerable-start rule both listing modes apply, and the
// booking through refusalFor: the slots of duration minutes on day (a
// calendar day in loc, the link's zone) that are on one of the link's days,
// inside the link's horizon, on the link's grid, free for every accepted host
// (CollectiveSlots) and at least the minimum notice after now. A day outside
// the horizon or not a link day is answered before any host is read. An error
// means busy time could not be gathered; nothing may be offered on that day.
func (h *bookingHandlers) offeredSlots(ctx context.Context, link *storage.SchedulingLink, loc *time.Location, day time.Time, duration int, now time.Time) ([]engine.AvailableSlot, error) {
	out := make([]engine.AvailableSlot, 0)
	if !inHorizon(link, calendarDay(now, loc), day) || !linkOpenOn(link, day) {
		return out, nil
	}
	slots, err := h.eng.CollectiveSlots(ctx, link, day, duration)
	if err != nil {
		return nil, err
	}
	earliest := now.Add(time.Duration(link.MinNoticeMinutes) * time.Minute)
	for _, slot := range slots {
		if !slot.Start.Before(earliest) {
			out = append(out, engine.AvailableSlot{Start: slot.Start.UTC(), End: slot.End.UTC()})
		}
	}
	return out, nil
}

// refusalText is the specific text paired with each reason (contract
// BookingSlotUnavailableError).
var refusalText = map[gen.BookingRefusalReason]gen.BookingSlotUnavailableErrorError{
	gen.TooSoon:          gen.SlotIsTooSoon,
	gen.OutsideLinkHours: gen.SlotIsOutsideTheLinksDaysOrHours,
	gen.BeyondHorizon:    gen.SlotIsBeyondTheLinksBookingHorizon,
	gen.HostBusy:         gen.AHostIsBusyAtThatTime,
	gen.Taken:            gen.SlotIsAlreadyBooked,
}

// writeRefusal writes the 409 of a refused public booking: the generic body
// unless the link owner chose specific reasons, then the reason and its one
// paired text. The status is 409 either way.
func writeRefusal(w http.ResponseWriter, link *storage.SchedulingLink, reason gen.BookingRefusalReason) {
	if link.RefusalMessage != storage.RefusalSpecific {
		writeError(w, errSlotUnavailable, http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(gen.BookingSlotUnavailableError{Error: refusalText[reason], Reason: &reason})
}

// cheapRefusal applies the checks that need no host data, in the precedence
// order (revision 2.1): beyond the horizon, outside the link's days, hours or
// grid, inside the notice. A start on a day before today is never beyond the
// horizon: it is too soon unless outside the link's hours comes first. ok is
// false when none refuses.
func cheapRefusal(link *storage.SchedulingLink, loc *time.Location, start, end time.Time, duration int, now time.Time) (gen.BookingRefusalReason, bool) {
	day := calendarDay(start, loc)
	if daysBetween(calendarDay(now, loc), day) >= horizonDays(link) {
		return gen.BeyondHorizon, true
	}
	onGrid := false
	if linkOpenOn(link, day) {
		for _, s := range engine.GridSlots(link, day, duration) {
			if s.Start.Equal(start) && s.End.Equal(end) {
				onGrid = true
				break
			}
		}
	}
	if !onGrid {
		return gen.OutsideLinkHours, true
	}
	if start.Before(now.Add(time.Duration(link.MinNoticeMinutes) * time.Minute)) {
		return gen.TooSoon, true
	}
	return "", false
}

// --- PAC-88: the day zone, the overview range and the listing's days ---------

// displayZonePattern is the contract's display_time_zone pattern.
var displayZonePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_+-]*(/[A-Za-z0-9_+-]+){0,2}$`)

// errInvalidDisplayZone is the 400 body of a refused display_time_zone
// (contract PAC-88 section 2.4, AC-32).
const errInvalidDisplayZone = "invalid display_time_zone, use an IANA time zone name"

// maxOverviewDays is the overview's bound: a six-week month grid (PAC-88
// contract section 2.2).
const maxOverviewDays = 42

// loadDisplayZone loads a requested display zone. ok is false for a name that
// fails the pattern or 64 characters, names the server's own zone (Local,
// localtime, posixrules: AC-28), is a leap-second variant (right/...) or does
// not load from the tz database. The name is never canonicalised.
func loadDisplayZone(name string) (*time.Location, bool) {
	if len(name) > 64 || !displayZonePattern.MatchString(name) {
		return nil, false
	}
	switch name {
	case "Local", "localtime", "posixrules":
		return nil, false
	}
	if strings.HasPrefix(name, "right/") {
		return nil, false
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, false
	}
	return loc, true
}

// zoneDayStart is the first instant whose calendar date in z is day's date
// (y-m-d of day, read as a plain date) or later. No midnight is computed, so
// a zone whose midnight is skipped needs no special case: the answer is the
// first instant that reads as that date (or the next one, for a day the zone
// skipped entirely). Offsets are within +-14 h, so the search bracket of
// +-36 h around the date's UTC midnight always holds the boundary.
func zoneDayStart(day time.Time, z *time.Location) time.Time {
	y, m, d := day.Date()
	base := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	target := base.Format(time.DateOnly)
	lo, hi := base.Unix()-36*3600, base.Unix()+36*3600
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		if time.Unix(mid, 0).In(z).Format(time.DateOnly) >= target {
			hi = mid
		} else {
			lo = mid
		}
	}
	return time.Unix(hi, 0).UTC()
}

// neededLinkDays are the link days (calendar days in loc, at noon) a request
// for the z days from..to (inclusive, plain dates) needs: the link-zone dates
// of every instant of those days (PAC-88 contract section 2.1). Two link days
// for one z day, three where either zone changes offset, at most n+2 for n.
func neededLinkDays(from, to time.Time, z, loc *time.Location) []time.Time {
	start := zoneDayStart(from, z)
	end := zoneDayStart(to.AddDate(0, 0, 1), z)
	first := calendarDay(start, loc)
	last := calendarDay(end.Add(-time.Second), loc)
	out := make([]time.Time, 0, daysBetween(first, last)+1)
	for i := 0; i <= daysBetween(first, last); i++ {
		out = append(out, time.Date(first.Year(), first.Month(), first.Day()+i, 12, 0, 0, 0, loc))
	}
	return out
}

// offeredOnLinkDays is offeredSlots for several link days with one calendar
// read per host: the offered slots (UTC instants, ascending) of every link
// day in days that is inside the horizon and one of the link's days. Days
// failing those checks are answered before any host is read; when none is
// left, no host is read at all (PAC-88 C-3).
func (h *bookingHandlers) offeredOnLinkDays(ctx context.Context, link *storage.SchedulingLink, loc *time.Location, days []time.Time, duration int, now time.Time) ([]engine.AvailableSlot, error) {
	out := make([]engine.AvailableSlot, 0)
	today := calendarDay(now, loc)
	open := make([]time.Time, 0, len(days))
	for _, d := range days {
		if inHorizon(link, today, d) && linkOpenOn(link, d) {
			open = append(open, d)
		}
	}
	if len(open) == 0 {
		return out, nil
	}
	perDay, err := h.eng.CollectiveSlotsDays(ctx, link, open, duration)
	if err != nil {
		return nil, err
	}
	earliest := now.Add(time.Duration(link.MinNoticeMinutes) * time.Minute)
	for _, slots := range perDay {
		for _, slot := range slots {
			if !slot.Start.Before(earliest) {
				out = append(out, engine.AvailableSlot{Start: slot.Start.UTC(), End: slot.End.UTC()})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, nil
}
