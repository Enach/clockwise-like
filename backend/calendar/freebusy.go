package calendar

import (
	"context"
	"strings"
	"time"

	googlecalendar "google.golang.org/api/calendar/v3"
)

type TimeSlot struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// FreeBusyEntry is a provider's answer for one requested id (PAC-133 §3,
// D-1). The reader decides the outcome; no consumer re-derives "free" from an
// empty list.
//
//   - Known: the id is readable and Busy is complete ([] means free).
//   - !Known: the provider could not read it (an error for it, not mentioned
//     in the answer, or, for a group, its expansion failed or a member is
//     unknown). Busy then holds the windows the provider did report, possibly
//     none. Those windows block wherever windows are used (D-11); an unknown
//     id with no windows blocks nothing, and is never presented as free.
//
// Busy is never nil. For a Google group, Busy is every window its members
// reported (in member order, not merged) and Members lists the expansion.
type FreeBusyEntry struct {
	Known   bool
	Busy    []TimeSlot
	Members []FreeBusyMember
}

// FreeBusyMember is one calendar a Google group expanded into.
type FreeBusyMember struct {
	ID    string
	Known bool
	Busy  []TimeSlot
}

// IsGroup reports whether the provider answered the id as a group.
func (e FreeBusyEntry) IsGroup() bool { return e.Members != nil }

// distinctIDs returns ids without repeats, in first-appearance order.
func distinctIDs(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// GetFreeBusy asks Google about every requested id in one freebusy.query and
// answers every distinct id with an outcome (PAC-133 §3.1). A failed call is
// the error: the whole call failed.
func (c *CalendarClient) GetFreeBusy(ctx context.Context, ids []string, timeMin, timeMax time.Time) (map[string]FreeBusyEntry, error) {
	ids = distinctIDs(ids)
	items := make([]*googlecalendar.FreeBusyRequestItem, len(ids))
	for i, id := range ids {
		items[i] = &googlecalendar.FreeBusyRequestItem{Id: id}
	}

	resp, err := c.service.Freebusy.Query(&googlecalendar.FreeBusyRequest{
		TimeMin: timeMin.Format(time.RFC3339),
		TimeMax: timeMax.Format(time.RFC3339),
		Items:   items,
	}).Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	return googleFreeBusyAnswer(ids, resp), nil
}

// googleFreeBusyAnswer applies the reader rules to a Google answer:
//
//   - a requested id that is a key of groups is a group: its members are the
//     expansion, each read by the calendar rule (absent from calendars means
//     unknown); the group is readable iff the expansion has no error and
//     every member is readable; its windows are all its members' windows;
//   - else a key of calendars: an entry carrying errors is unknown with its
//     windows kept, otherwise readable with its windows;
//   - else unknown with no windows.
//
// Keys are matched exactly, else case-insensitively. A calendars entry that is
// neither requested nor a member of a requested group is dropped.
func googleFreeBusyAnswer(ids []string, resp *googlecalendar.FreeBusyResponse) map[string]FreeBusyEntry {
	out := make(map[string]FreeBusyEntry, len(ids))
	for _, id := range ids {
		if g, ok := lookupGroup(resp.Groups, id); ok {
			entry := FreeBusyEntry{Known: len(g.Errors) == 0, Busy: []TimeSlot{}, Members: []FreeBusyMember{}}
			for _, memberID := range g.Calendars {
				known, busy := googleCalendarOutcome(resp.Calendars, memberID)
				entry.Members = append(entry.Members, FreeBusyMember{ID: memberID, Known: known, Busy: busy})
				entry.Busy = append(entry.Busy, busy...)
				if !known {
					entry.Known = false
				}
			}
			out[id] = entry
			continue
		}
		known, busy := googleCalendarOutcome(resp.Calendars, id)
		out[id] = FreeBusyEntry{Known: known, Busy: busy}
	}
	return out
}

// googleCalendarOutcome reads one calendars entry by the calendar rule.
func googleCalendarOutcome(cals map[string]googlecalendar.FreeBusyCalendar, id string) (bool, []TimeSlot) {
	cal, ok := cals[id]
	if !ok {
		for k, v := range cals {
			if strings.EqualFold(k, id) {
				cal, ok = v, true
				break
			}
		}
	}
	if !ok {
		return false, []TimeSlot{}
	}
	slots := make([]TimeSlot, 0, len(cal.Busy))
	for _, b := range cal.Busy {
		if b == nil {
			continue
		}
		// An unparseable timestamp stays the zero time (spec §5, unchanged).
		start, _ := time.Parse(time.RFC3339, b.Start)
		end, _ := time.Parse(time.RFC3339, b.End)
		slots = append(slots, TimeSlot{Start: start, End: end})
	}
	return len(cal.Errors) == 0, slots
}

func lookupGroup(groups map[string]googlecalendar.FreeBusyGroup, id string) (googlecalendar.FreeBusyGroup, bool) {
	if g, ok := groups[id]; ok {
		return g, true
	}
	for k, g := range groups {
		if strings.EqualFold(k, id) {
			return g, true
		}
	}
	return googlecalendar.FreeBusyGroup{}, false
}

// unknownForAll answers every distinct id unknown with no windows: what a
// reader that reads nothing must say (PAC-133 §3.3, D-11).
func unknownForAll(ids []string) map[string]FreeBusyEntry {
	ids = distinctIDs(ids)
	out := make(map[string]FreeBusyEntry, len(ids))
	for _, id := range ids {
		out[id] = FreeBusyEntry{Known: false, Busy: []TimeSlot{}}
	}
	return out
}
