package calendar

// PAC-122 batch 2 review (#297): SF-044 multi-valued EXDATE/RDATE (major 2),
// TZID across a DST change (minor 1) and the expansion bounds (minor 2).

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// A comma-separated EXDATE (with its TZID) and RDATE list is read value by
// value; before, the whole series was unreadable.
func TestPAC122_SF044_MultiValuedExdateAndRdate(t *testing.T) {
	feed := b2Feed("BEGIN:VEVENT\r\nUID:list\r\nSUMMARY:Run\r\nDTSTAMP:20250101T000000Z\r\n" +
		"DTSTART;TZID=Europe/Paris:20250106T090000\r\nDTEND;TZID=Europe/Paris:20250106T100000\r\n" +
		"RRULE:FREQ=DAILY;COUNT=5\r\n" +
		"EXDATE;TZID=Europe/Paris:20250107T090000,20250109T090000\r\n" +
		"RDATE;VALUE=DATE-TIME:20250111T080000Z,20250112T080000Z\r\n" +
		"END:VEVENT\r\n")
	got := b2List(t, feed, time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC), time.Date(2025, 1, 13, 0, 0, 0, 0, time.UTC))
	var ids []string
	for id, e := range got {
		if e.Unreadable() {
			t.Fatalf("series read as unreadable: %+v", got)
		}
		ids = append(ids, id)
	}
	for _, want := range []string{"list/20250106T080000Z", "list/20250108T080000Z", "list/20250110T080000Z", "list/20250111T080000Z", "list/20250112T080000Z"} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing %s in %v", want, ids)
		}
	}
	if len(got) != 5 {
		t.Fatalf("instances = %v, want 5 (two excluded, two added)", ids)
	}
}

// A TZID series keeps its local time across the DST change (Europe/Paris,
// 30 March 2025): 09:00 local is 08:00Z before and 07:00Z after.
func TestPAC122_SF044_TZIDAcrossDST(t *testing.T) {
	feed := b2Feed("BEGIN:VEVENT\r\nUID:dst\r\nDTSTAMP:20250101T000000Z\r\n" +
		"DTSTART;TZID=Europe/Paris:20250328T090000\r\nDTEND;TZID=Europe/Paris:20250328T100000\r\n" +
		"RRULE:FREQ=DAILY;COUNT=4\r\nEND:VEVENT\r\n")
	got := b2List(t, feed, time.Date(2025, 3, 27, 0, 0, 0, 0, time.UTC), time.Date(2025, 4, 3, 0, 0, 0, 0, time.UTC))
	want := map[string]time.Time{
		"dst/20250328T080000Z": time.Date(2025, 3, 28, 8, 0, 0, 0, time.UTC),
		"dst/20250329T080000Z": time.Date(2025, 3, 29, 8, 0, 0, 0, time.UTC),
		"dst/20250330T070000Z": time.Date(2025, 3, 30, 7, 0, 0, 0, time.UTC),
		"dst/20250331T070000Z": time.Date(2025, 3, 31, 7, 0, 0, 0, time.UTC),
	}
	if len(got) != len(want) {
		t.Fatalf("instances = %v", got)
	}
	for id, at := range want {
		e, ok := got[id]
		if !ok || !e.Start.Equal(at) || e.End.Sub(e.Start) != time.Hour {
			t.Errorf("%s = %+v (present %v), want %s for an hour", id, e, ok, at)
		}
	}
}

// FREQ=SECONDLY and MINUTELY are unreadable; a series with more than
// maxInstancesPerSeries instances in the window lists that many and is also
// marked unreadable (so its other blockers are kept).
func TestPAC122_SF044_ExpansionBounds(t *testing.T) {
	start := time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 30)
	for _, freq := range []string{"SECONDLY", "MINUTELY"} {
		feed := b2Feed(fmt.Sprintf("BEGIN:VEVENT\r\nUID:fast\r\nDTSTAMP:20250101T000000Z\r\nDTSTART:20250106T090000Z\r\nDTEND:20250106T090001Z\r\nRRULE:FREQ=%s\r\nEND:VEVENT\r\n", freq))
		got := b2List(t, feed, start, end)
		if e, ok := got["fast"]; len(got) != 1 || !ok || !e.Unreadable() {
			t.Errorf("FREQ=%s: %d events, want only the series as unreadable", freq, len(got))
		}
	}

	// Hourly over 30 days: 720 instances in the window.
	feed := b2Feed("BEGIN:VEVENT\r\nUID:hourly\r\nDTSTAMP:20250101T000000Z\r\nDTSTART:20250106T000000Z\r\nDTEND:20250106T003000Z\r\nRRULE:FREQ=HOURLY\r\nEND:VEVENT\r\n")
	got := b2List(t, feed, start, end)
	instances := 0
	for id, e := range got {
		if strings.HasPrefix(id, "hourly/") && !e.Unreadable() {
			instances++
		}
	}
	if instances != maxInstancesPerSeries {
		t.Errorf("%d instances listed, want the cap %d", instances, maxInstancesPerSeries)
	}
	if e, ok := got["hourly"]; !ok || !e.Unreadable() {
		t.Error("a series over the cap is not marked unreadable")
	}

	// Under the cap: no unreadable marker.
	got = b2List(t, feed, start, start.AddDate(0, 0, 2))
	if _, ok := got["hourly"]; ok || len(got) != 48 {
		t.Errorf("two days: %d events (unreadable marker %v), want 48 instances", len(got), ok)
	}
}
