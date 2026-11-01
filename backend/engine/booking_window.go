package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/storage"
)

// The link's daily window and its 15-minute grid in the link's zone (PAC-72
// revision 2, owner decision D-9; spec R2.2 and R21.2).
//
// A link's hours are wall-clock times in the link owner's profile zone. On a
// daylight-saving change day:
//
//   - a slot start whose wall time does not exist that day is not offered;
//   - a slot start whose wall time occurs twice is offered once, at its first
//     occurrence;
//   - a window edge (opening or closing time) that does not exist that day is
//     taken at the instant of the change, so the window never grows;
//   - a window edge in a repeated hour is taken at its first occurrence;
//   - the grid is counted in wall time from the opening time.
//
// Every instant returned is in the day's location; callers compare instants.

// gridStep is the listing's slot grid.
const gridStep = 15

// wallClock resolves the wall-clock time h:m on day's calendar date in day's
// location. exists is false when that wall time is skipped by a change of
// offset; t is then the instant of the change (the first instant after the
// gap). A wall time that occurs twice resolves to its first occurrence.
func wallClock(day time.Time, h, m int) (t time.Time, exists bool) {
	loc := day.Location()
	y, mo, d := day.Date()
	naive := time.Date(y, mo, d, h, m, 0, 0, time.UTC)
	var best, earliest time.Time
	found := false
	// The offsets in force around that wall time: one transition a day at
	// most, so the offsets a day before, at, and a day after cover it.
	for _, probe := range []time.Duration{-24 * time.Hour, 0, 24 * time.Hour} {
		_, off := naive.Add(probe).In(loc).Zone()
		c := naive.Add(-time.Duration(off) * time.Second)
		if earliest.IsZero() || c.Before(earliest) {
			earliest = c
		}
		lc := c.In(loc)
		ly, lmo, ld := lc.Date()
		if ly == y && lmo == mo && ld == d && lc.Hour() == h && lc.Minute() == m && lc.Second() == 0 {
			if !found || c.Before(best) {
				best, found = c, true
			}
		}
	}
	if found {
		return best.In(loc), true
	}
	// Skipped: the earliest candidate lies before the gap (it reads as an
	// earlier wall time); the zone period it is in ends at the change.
	_, end := earliest.In(loc).ZoneBounds()
	return end.In(loc), false
}

// clockMinutes parses "HH:MM" into minutes after midnight.
func clockMinutes(hhmm string) (int, bool) {
	parts := strings.SplitN(strings.TrimSpace(hhmm), ":", 2)
	if len(parts) != 2 {
		return 0, false
	}
	var h, m int
	if _, err := fmt.Sscanf(parts[0], "%d", &h); err != nil {
		return 0, false
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &m); err != nil {
		return 0, false
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// LinkWindow is the link's daily window on day's calendar date, in day's
// location, with the daylight-saving edge rules above. ok is false when the
// link's window times cannot be read or the window is empty that day.
func LinkWindow(link *storage.SchedulingLink, day time.Time) (start, end time.Time, ok bool) {
	sm, ok1 := clockMinutes(link.WindowStart)
	em, ok2 := clockMinutes(link.WindowEnd)
	if !ok1 || !ok2 {
		return time.Time{}, time.Time{}, false
	}
	start, _ = wallClock(day, sm/60, sm%60)
	end, _ = wallClock(day, em/60, em%60)
	return start, end, end.After(start)
}

// GridSlots is every slot of durationMinutes the link's window offers on
// day's calendar date before busy time, notice and horizon are applied: the
// starts on the 15-minute wall-clock grid from the opening time that exist
// that day (first occurrence when repeated), each ending no later than the
// window's close. day's location is the link's zone.
func GridSlots(link *storage.SchedulingLink, day time.Time, durationMinutes int) []AvailableSlot {
	out := make([]AvailableSlot, 0)
	if durationMinutes <= 0 {
		return out
	}
	ws, we, ok := LinkWindow(link, day)
	if !ok {
		return out
	}
	sm, _ := clockMinutes(link.WindowStart)
	dur := time.Duration(durationMinutes) * time.Minute
	for m := sm; m < 24*60; m += gridStep {
		t, exists := wallClock(day, m/60, m%60)
		if !exists || t.Before(ws) {
			continue
		}
		end := t.Add(dur)
		if end.After(we) {
			continue
		}
		out = append(out, AvailableSlot{Start: t, End: end})
	}
	return out
}
