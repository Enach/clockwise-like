package engine

import (
	"context"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
)

// PAC-72 revision 2 (D-9) and 2.1: the link's window and grid are wall-clock
// times in the link's zone, with the daylight-saving rules of spec R2.2/R21.2.
// Pure functions: no database.

func pac72Loc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return loc
}

func windowLink(start, end string) *storage.SchedulingLink {
	return &storage.SchedulingLink{WindowStart: start, WindowEnd: end, DaysOfWeek: []int{0, 1, 2, 3, 4, 5, 6}}
}

func utcStarts(slots []AvailableSlot) []string {
	out := make([]string, 0, len(slots))
	for _, s := range slots {
		out = append(out, s.Start.UTC().Format("15:04"))
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPAC72_WallClock(t *testing.T) {
	paris := pac72Loc(t, "Europe/Paris")
	spring := time.Date(2030, 3, 31, 12, 0, 0, 0, paris)
	autumn := time.Date(2030, 10, 27, 12, 0, 0, 0, paris)
	for _, tc := range []struct {
		name   string
		day    time.Time
		h, m   int
		want   time.Time
		exists bool
	}{
		{"spring 01:45 CET exists", spring, 1, 45, time.Date(2030, 3, 31, 0, 45, 0, 0, time.UTC), true},
		{"spring 02:00 skipped: the change instant", spring, 2, 0, time.Date(2030, 3, 31, 1, 0, 0, 0, time.UTC), false},
		{"spring 02:30 skipped: the change instant", spring, 2, 30, time.Date(2030, 3, 31, 1, 0, 0, 0, time.UTC), false},
		{"spring 03:00 CEST", spring, 3, 0, time.Date(2030, 3, 31, 1, 0, 0, 0, time.UTC), true},
		{"autumn 02:00 first occurrence (CEST)", autumn, 2, 0, time.Date(2030, 10, 27, 0, 0, 0, 0, time.UTC), true},
		{"autumn 02:45 first occurrence (CEST)", autumn, 2, 45, time.Date(2030, 10, 27, 0, 45, 0, 0, time.UTC), true},
		{"autumn 03:00 CET", autumn, 3, 0, time.Date(2030, 10, 27, 2, 0, 0, 0, time.UTC), true},
		{"plain day in UTC", time.Date(2030, 1, 7, 12, 0, 0, 0, time.UTC), 9, 0, time.Date(2030, 1, 7, 9, 0, 0, 0, time.UTC), true},
	} {
		got, exists := wallClock(tc.day, tc.h, tc.m)
		if !got.Equal(tc.want) || exists != tc.exists {
			t.Errorf("%s: %v exists=%v, want %v exists=%v", tc.name, got.UTC(), exists, tc.want, tc.exists)
		}
	}
}

// AC-31 (revision 2.1), listing half: Europe/Paris, 01:00-04:00, 15-minute
// slots.
func TestPAC72_GridSlots_DaylightSaving(t *testing.T) {
	paris := pac72Loc(t, "Europe/Paris")
	spring := time.Date(2030, 3, 31, 12, 0, 0, 0, paris)
	autumn := time.Date(2030, 10, 27, 12, 0, 0, 0, paris)

	// Spring: 01:00..01:45 CET (00:00Z..00:45Z), no 02:xx, then 03:00..03:45
	// CEST (01:00Z..01:45Z).
	got := utcStarts(GridSlots(windowLink("01:00", "04:00"), spring, 15))
	want := []string{"00:00", "00:15", "00:30", "00:45", "01:00", "01:15", "01:30", "01:45"}
	if !equalStrings(got, want) {
		t.Errorf("spring 01:00-04:00: %v, want %v", got, want)
	}
	for _, s := range GridSlots(windowLink("01:00", "04:00"), spring, 15) {
		if h := s.Start.In(paris).Hour(); h == 2 {
			t.Errorf("spring: a slot at a skipped wall time %v", s.Start.In(paris))
		}
	}

	// Autumn: 01:00 CEST (23:00Z the day before) .. 02:45 CEST (00:45Z) once
	// each, then 03:00..03:45 CET (02:00Z..02:45Z); the second 02:xx (01:00Z..
	// 01:45Z) never.
	got = utcStarts(GridSlots(windowLink("01:00", "04:00"), autumn, 15))
	want = []string{"23:00", "23:15", "23:30", "23:45", "00:00", "00:15", "00:30", "00:45", "02:00", "02:15", "02:30", "02:45"}
	if !equalStrings(got, want) {
		t.Errorf("autumn 01:00-04:00: %v, want %v", got, want)
	}

	// Window edges on the spring day: a skipped opening is the change
	// instant (first slot 03:00 CEST, nothing earlier); a skipped closing is
	// the change instant (last 15-minute slot 01:45 CET).
	if got := utcStarts(GridSlots(windowLink("02:30", "04:00"), spring, 15)); len(got) == 0 || got[0] != "01:00" {
		t.Errorf("spring 02:30-04:00 starts %v, want first 01:00Z (03:00 CEST)", got)
	}
	got = utcStarts(GridSlots(windowLink("01:00", "02:30"), spring, 15))
	if want := []string{"00:00", "00:15", "00:30", "00:45"}; !equalStrings(got, want) {
		t.Errorf("spring 01:00-02:30: %v, want %v (closes at 01:00Z)", got, want)
	}
	ws, we, ok := LinkWindow(windowLink("02:30", "04:00"), spring)
	if !ok || !ws.Equal(time.Date(2030, 3, 31, 1, 0, 0, 0, time.UTC)) || !we.Equal(time.Date(2030, 3, 31, 2, 0, 0, 0, time.UTC)) {
		t.Errorf("spring 02:30-04:00 window = %v..%v (%v)", ws.UTC(), we.UTC(), ok)
	}
	// An edge in the repeated hour is its first occurrence.
	ws, _, _ = LinkWindow(windowLink("02:30", "04:00"), autumn)
	if !ws.Equal(time.Date(2030, 10, 27, 0, 30, 0, 0, time.UTC)) {
		t.Errorf("autumn opening 02:30 = %v, want 00:30Z (first occurrence)", ws.UTC())
	}
}

func TestPAC72_GridSlots_PlainDaysAndBadInput(t *testing.T) {
	ny := pac72Loc(t, "America/New_York")
	summer := time.Date(2030, 7, 2, 12, 0, 0, 0, ny)
	got := GridSlots(windowLink("09:00", "17:00"), summer, 30)
	if len(got) != 31 || got[0].Start.UTC().Format("15:04") != "13:00" || got[30].Start.UTC().Format("15:04") != "20:30" {
		t.Errorf("New York summer 09:00-17:00/30: %d slots %v", len(got), utcStarts(got))
	}
	winter := time.Date(2030, 1, 8, 12, 0, 0, 0, ny)
	if got := GridSlots(windowLink("09:00", "17:00"), winter, 30); got[0].Start.UTC().Format("15:04") != "14:00" {
		t.Errorf("New York winter first slot %v, want 14:00Z", got[0].Start.UTC())
	}
	day := time.Date(2030, 1, 7, 12, 0, 0, 0, time.UTC)
	for name, l := range map[string]*storage.SchedulingLink{
		"unreadable start": windowLink("nine", "17:00"),
		"unreadable end":   windowLink("09:00", "25:00"),
		"empty window":     windowLink("17:00", "09:00"),
		"no colon":         windowLink("0900", "17:00"),
	} {
		if got := GridSlots(l, day, 30); len(got) != 0 {
			t.Errorf("%s: %d slots, want none", name, len(got))
		}
	}
	if got := GridSlots(windowLink("09:00", "17:00"), day, 0); len(got) != 0 {
		t.Errorf("zero duration: %d slots", len(got))
	}
	// 60 minutes on a 09:00-10:00 window: exactly one slot.
	if got := GridSlots(windowLink("09:00", "10:00"), day, 60); len(got) != 1 {
		t.Errorf("09:00-10:00/60: %v", utcStarts(got))
	}
}

// CollectiveSlots in the link's zone (AC-29 engine half): the window is the
// date's location's wall clock; an empty window reads no busy time.
func TestPAC72_CollectiveSlots_InTheLinksZone(t *testing.T) {
	db := openTestDB(t)
	newGoogleStub(t)
	host, _ := pac81User(t, db, "pac72-e-zone@example.com")
	link := pac72EngineLink(t, db, host, "pac72-e-zone", host) // 09:00-12:00
	paris := pac72Loc(t, "Europe/Paris")
	tokyo := pac72Loc(t, "Asia/Tokyo")
	eng := &BookingEngine{DB: db}

	inParis, err := eng.CollectiveSlots(context.Background(), link, time.Date(2030, 1, 7, 12, 0, 0, 0, paris), 30)
	if err != nil || len(inParis) != 11 || !inParis[0].Start.Equal(time.Date(2030, 1, 7, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("Paris: %v %v, want 11 slots from 08:00Z", utcStarts(inParis), err)
	}
	inTokyo, err := eng.CollectiveSlots(context.Background(), link, time.Date(2030, 1, 7, 12, 0, 0, 0, tokyo), 30)
	if err != nil || len(inTokyo) != 11 || !inTokyo[0].Start.Equal(time.Date(2030, 1, 7, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("Tokyo: %v %v, want 11 slots from 00:00Z", utcStarts(inTokyo), err)
	}
	empty := *link
	empty.WindowStart, empty.WindowEnd = "12:00", "09:00"
	if got, err := eng.CollectiveSlots(context.Background(), &empty, pac72Monday, 30); err != nil || len(got) != 0 {
		t.Fatalf("empty window: %v %v", got, err)
	}
}
