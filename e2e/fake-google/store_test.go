package main

import (
	"testing"
	"time"

	googlecalendar "google.golang.org/api/calendar/v3"
)

func ev(summary, start, end string) *googlecalendar.Event {
	return &googlecalendar.Event{
		Summary: summary,
		Start:   &googlecalendar.EventDateTime{DateTime: start},
		End:     &googlecalendar.EventDateTime{DateTime: end},
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// The window filter is the real service's: an event is in the window when it
// overlaps it, so one straddling either boundary is in and one that only
// touches a boundary is out.
func TestList_WindowFilter(t *testing.T) {
	s := NewStore()
	const cal = "owner@example.test"
	s.Insert(cal, ev("before", "2030-01-07T07:00:00Z", "2030-01-07T08:00:00Z"))
	s.Insert(cal, ev("touches-start", "2030-01-07T08:00:00Z", "2030-01-07T09:00:00Z"))
	s.Insert(cal, ev("straddles-start", "2030-01-07T08:30:00Z", "2030-01-07T09:30:00Z"))
	s.Insert(cal, ev("inside", "2030-01-07T10:00:00Z", "2030-01-07T11:00:00Z"))
	s.Insert(cal, ev("straddles-end", "2030-01-07T11:30:00Z", "2030-01-07T12:30:00Z"))
	s.Insert(cal, ev("touches-end", "2030-01-07T12:00:00Z", "2030-01-07T13:00:00Z"))
	s.Insert(cal, ev("after", "2030-01-07T14:00:00Z", "2030-01-07T15:00:00Z"))
	cancelled := ev("cancelled", "2030-01-07T10:00:00Z", "2030-01-07T10:30:00Z")
	cancelled.Status = "cancelled"
	s.Insert(cal, cancelled)
	s.Insert("someone-else@example.test", ev("other calendar", "2030-01-07T10:00:00Z", "2030-01-07T11:00:00Z"))

	got := s.List("OWNER@example.test", mustTime(t, "2030-01-07T09:00:00Z"), mustTime(t, "2030-01-07T12:00:00Z"))
	var names []string
	for _, e := range got {
		names = append(names, e.Summary)
	}
	want := []string{"straddles-start", "inside", "straddles-end"}
	if len(names) != len(want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("got %v, want %v (in start order)", names, want)
		}
	}
}

// Free/busy reports opaque events only, clipped to the window, and says when
// it holds no calendar at all, which is what lets the backend tell "known and
// free" from "unknown".
func TestBusy_Projection(t *testing.T) {
	s := NewStore()
	const cal = "attendee@example.test"
	s.Insert(cal, ev("opaque", "2030-01-07T10:00:00Z", "2030-01-07T11:00:00Z"))
	s.Insert(cal, ev("straddles", "2030-01-07T16:30:00Z", "2030-01-07T18:00:00Z"))
	tr := ev("transparent focus", "2030-01-07T13:00:00Z", "2030-01-07T14:00:00Z")
	tr.Transparency = "transparent"
	s.Insert(cal, tr)

	busy, known := s.Busy(cal, mustTime(t, "2030-01-07T09:00:00Z"), mustTime(t, "2030-01-07T17:00:00Z"))
	if !known {
		t.Fatal("a calendar with events must be known")
	}
	want := [][2]string{
		{"2030-01-07T10:00:00Z", "2030-01-07T11:00:00Z"},
		{"2030-01-07T16:30:00Z", "2030-01-07T17:00:00Z"},
	}
	if len(busy) != len(want) {
		t.Fatalf("got %d busy windows, want %d", len(busy), len(want))
	}
	for i, w := range want {
		if busy[i].Start != w[0] || busy[i].End != w[1] {
			t.Errorf("window %d = %s..%s, want %s..%s", i, busy[i].Start, busy[i].End, w[0], w[1])
		}
	}

	if _, known := s.Busy("nobody@example.test", mustTime(t, "2030-01-07T09:00:00Z"), mustTime(t, "2030-01-07T17:00:00Z")); known {
		t.Fatal("a calendar the stand-in does not hold must be unknown")
	}
}

func TestDelete_RemovesOnlyThatEvent(t *testing.T) {
	s := NewStore()
	a := s.Insert("c", ev("a", "2030-01-07T10:00:00Z", "2030-01-07T11:00:00Z"))
	s.Insert("c", ev("b", "2030-01-07T12:00:00Z", "2030-01-07T13:00:00Z"))
	if !s.Delete("c", a.Id) {
		t.Fatal("delete of a held event must succeed")
	}
	if s.Delete("c", a.Id) {
		t.Fatal("a second delete must report the event gone")
	}
	if got := s.List("c", zeroTime, zeroTime); len(got) != 1 || got[0].Summary != "b" {
		t.Fatalf("after delete got %v", got)
	}
}
