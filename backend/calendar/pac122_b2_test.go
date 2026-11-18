package calendar

// PAC-122 phase 2, batch 2, SF-044: the webcal parser reads DURATION,
// expands RRULE (with EXDATE and overrides) and keeps an unreadable event as
// unreadable instead of dropping it.

import (
	"strings"
	"testing"
	"time"
)

func b2Feed(events string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//pac122//b2//EN\r\n" + events + "END:VCALENDAR\r\n"
}

func b2List(t *testing.T, feed string, start, end time.Time) map[string]GenericEvent {
	t.Helper()
	all, err := parseICS(strings.NewReader(feed))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]GenericEvent{}
	for _, e := range expandICS(all, start, end) {
		if _, dup := out[e.ID]; dup {
			t.Fatalf("duplicate id %s", e.ID)
		}
		out[e.ID] = e
	}
	return out
}

func TestPAC122_SF044_DurationAndDefaultEnd(t *testing.T) {
	feed := b2Feed("BEGIN:VEVENT\r\nUID:dur\r\nDTSTAMP:20250101T000000Z\r\nDTSTART:20250106T100000Z\r\nDURATION:PT90M\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:allday\r\nDTSTAMP:20250101T000000Z\r\nDTSTART;VALUE=DATE:20250107\r\nEND:VEVENT\r\n")
	got := b2List(t, feed, time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC), time.Date(2025, 1, 13, 0, 0, 0, 0, time.UTC))
	if e := got["dur"]; !e.End.Equal(time.Date(2025, 1, 6, 11, 30, 0, 0, time.UTC)) {
		t.Errorf("DURATION event = %+v, want it to end at 11:30", e)
	}
	if e := got["allday"]; !e.End.Equal(time.Date(2025, 1, 8, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("date event with no end = %+v, want one day", e)
	}
}

func TestPAC122_SF044_RecurrenceExpanded(t *testing.T) {
	feed := b2Feed("BEGIN:VEVENT\r\nUID:weekly\r\nSUMMARY:Gym\r\nDTSTAMP:20250101T000000Z\r\nDTSTART:20250106T180000Z\r\nDTEND:20250106T190000Z\r\nRRULE:FREQ=DAILY;COUNT=5\r\nEXDATE:20250107T180000Z\r\nEND:VEVENT\r\n" +
		// The 8th moved to 07:00, the 9th cancelled.
		"BEGIN:VEVENT\r\nUID:weekly\r\nSUMMARY:Gym\r\nDTSTAMP:20250101T000000Z\r\nRECURRENCE-ID:20250108T180000Z\r\nDTSTART:20250108T070000Z\r\nDTEND:20250108T080000Z\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:weekly\r\nDTSTAMP:20250101T000000Z\r\nRECURRENCE-ID:20250109T180000Z\r\nSTATUS:CANCELLED\r\nDTSTART:20250109T180000Z\r\nDTEND:20250109T190000Z\r\nEND:VEVENT\r\n")
	got := b2List(t, feed, time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC), time.Date(2025, 1, 13, 0, 0, 0, 0, time.UTC))
	want := map[string]time.Time{
		"weekly/20250106T180000Z": time.Date(2025, 1, 6, 18, 0, 0, 0, time.UTC),
		"weekly/20250108T180000Z": time.Date(2025, 1, 8, 7, 0, 0, 0, time.UTC),
		"weekly/20250110T180000Z": time.Date(2025, 1, 10, 18, 0, 0, 0, time.UTC),
	}
	if len(got) != len(want) {
		t.Fatalf("instances = %v, want %v", got, want)
	}
	for id, start := range want {
		if e, ok := got[id]; !ok || !e.Start.Equal(start) || e.Title != "Gym" {
			t.Errorf("%s = %+v, want start %s", id, e, start)
		}
	}
	// Only instances in the window are listed.
	if got := b2List(t, feed, time.Date(2025, 1, 10, 0, 0, 0, 0, time.UTC), time.Date(2025, 1, 11, 0, 0, 0, 0, time.UTC)); len(got) != 1 {
		t.Fatalf("window of one day = %v", got)
	}
}

func TestPAC122_SF044_UnreadableEventIsKept(t *testing.T) {
	feed := b2Feed("BEGIN:VEVENT\r\nUID:bad-date\r\nDTSTAMP:20250101T000000Z\r\nDTSTART:yesterday\r\nDTEND:20250106T100000Z\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:bad-rule\r\nDTSTAMP:20250101T000000Z\r\nDTSTART:20250106T100000Z\r\nDTEND:20250106T110000Z\r\nRRULE:FREQ=SOMETIMES\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:no-start\r\nDTSTAMP:20250101T000000Z\r\nEND:VEVENT\r\n")
	// A window far from every date: unreadable events are listed whatever the window.
	got := b2List(t, feed, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC))
	for _, id := range []string{"bad-date", "bad-rule", "no-start"} {
		if e, ok := got[id]; !ok || !e.Unreadable() {
			t.Errorf("%s = %+v (present %v), want unreadable", id, e, ok)
		}
	}
}
