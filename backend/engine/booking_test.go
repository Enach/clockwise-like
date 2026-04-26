package engine

import (
	"testing"
	"time"
)

func TestSlugify(t *testing.T) {
	// slugify expects pre-lowercased input (GenerateSlug lowercases before calling).
	cases := []struct {
		in   string
		want string
	}{
		{"nicolas", "nicolas"},
		{"alice-bob", "alice-bob"},
		{"hello world", "hello-world"},
		{"o'brien", "obrien"},
		{"abc123", "abc123"},
	}
	for _, c := range cases {
		got := slugify(c.in)
		if got != c.want {
			t.Errorf("slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseTime(t *testing.T) {
	loc := time.UTC
	date := time.Date(2024, 3, 15, 0, 0, 0, 0, loc)

	got := parseTime("09:30", date)
	want := time.Date(2024, 3, 15, 9, 30, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("parseTime = %v, want %v", got, want)
	}

	// Invalid format returns the original date.
	got2 := parseTime("bad", date)
	if !got2.Equal(date) {
		t.Errorf("parseTime(bad) = %v, want %v", got2, date)
	}
}

func TestOverlapsMerged(t *testing.T) {
	base := time.Date(2024, 1, 1, 9, 0, 0, 0, time.UTC)
	merged := []interval{
		{start: base, end: base.Add(time.Hour)},           // 09:00–10:00
		{start: base.Add(2 * time.Hour), end: base.Add(3 * time.Hour)}, // 11:00–12:00
	}

	cases := []struct {
		iv   interval
		want bool
	}{
		{interval{start: base.Add(30 * time.Minute), end: base.Add(90 * time.Minute)}, true},   // overlaps first
		{interval{start: base.Add(2 * time.Hour), end: base.Add(150 * time.Minute)}, true},     // overlaps second
		{interval{start: base.Add(time.Hour), end: base.Add(2 * time.Hour)}, false},            // gap between
		{interval{start: base.Add(3 * time.Hour), end: base.Add(4 * time.Hour)}, false},        // after all
	}
	for _, c := range cases {
		got := overlapsMerged(c.iv, merged)
		if got != c.want {
			t.Errorf("overlapsMerged(%v) = %v, want %v", c.iv, got, c.want)
		}
	}
}

func TestCollectiveSlotsAlgorithm(t *testing.T) {
	base := time.Date(2024, 1, 1, 9, 0, 0, 0, time.UTC)

	// Busy: 09:00–09:30 and 10:00–10:30.
	busy := []interval{
		{start: base, end: base.Add(30 * time.Minute)},
		{start: base.Add(time.Hour), end: base.Add(90 * time.Minute)},
	}
	merged := mergeIntervals(busy)
	windowEnd := base.Add(2 * time.Hour) // 11:00

	dur := 30 * time.Minute
	var slots []AvailableSlot
	for t2 := base; t2.Add(dur).Before(windowEnd) || t2.Add(dur).Equal(windowEnd); t2 = t2.Add(15 * time.Minute) {
		slotEnd := t2.Add(dur)
		iv := interval{start: t2, end: slotEnd}
		if !overlapsMerged(iv, merged) {
			slots = append(slots, AvailableSlot{Start: t2, End: slotEnd})
		}
	}

	// Expected free slot: 09:30–10:00 and 10:30–11:00.
	if len(slots) < 1 {
		t.Fatalf("expected at least 1 slot, got %d", len(slots))
	}

	wantFirst := base.Add(30 * time.Minute)
	if !slots[0].Start.Equal(wantFirst) {
		t.Errorf("first slot start = %v, want %v", slots[0].Start, wantFirst)
	}
}

func TestGenerateICS(t *testing.T) {
	start := time.Date(2024, 6, 15, 14, 0, 0, 0, time.UTC)
	end := start.Add(30 * time.Minute)
	ics := generateICS("Team Sync", "Alice", "alice@example.com", "Bob", start, end)

	for _, want := range []string{
		"BEGIN:VCALENDAR",
		"SUMMARY:Team Sync",
		"ATTENDEE;CN=Alice:mailto:alice@example.com",
		"DTSTART:20240615T140000Z",
		"DTEND:20240615T143000Z",
	} {
		if !contains(ics, want) {
			t.Errorf("ICS missing %q", want)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
