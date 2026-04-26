package engine

import (
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
)

func TestWeekStartFor(t *testing.T) {
	cases := []struct {
		in   time.Time
		want time.Time
	}{
		// Monday → same day
		{time.Date(2024, 6, 3, 9, 0, 0, 0, time.UTC), time.Date(2024, 6, 3, 0, 0, 0, 0, time.UTC)},
		// Wednesday → previous Monday
		{time.Date(2024, 6, 5, 15, 0, 0, 0, time.UTC), time.Date(2024, 6, 3, 0, 0, 0, 0, time.UTC)},
		// Sunday → previous Monday
		{time.Date(2024, 6, 9, 23, 59, 0, 0, time.UTC), time.Date(2024, 6, 3, 0, 0, 0, 0, time.UTC)},
		// Friday → Monday of same week
		{time.Date(2024, 6, 7, 8, 0, 0, 0, time.UTC), time.Date(2024, 6, 3, 0, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		got := weekStartFor(c.in)
		if !got.Equal(c.want) {
			t.Errorf("weekStartFor(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestHhmmToMinutes(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"09:00", 540},
		{"17:30", 1050},
		{"00:00", 0},
		{"24:00", 1440},
		{"bad", 0},
		{"", 0},
	}
	for _, c := range cases {
		got := hhmmToMinutes(c.in)
		if got != c.want {
			t.Errorf("hhmmToMinutes(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestFocusBlockMinutes(t *testing.T) {
	base := time.Date(2024, 6, 3, 9, 0, 0, 0, time.UTC)
	blocks := []storage.FocusBlock{
		{StartTime: base, EndTime: base.Add(90 * time.Minute)},
		{StartTime: base.Add(3 * time.Hour), EndTime: base.Add(4 * time.Hour)},
	}
	total, largest := focusBlockMinutes(blocks)
	if total != 150 {
		t.Errorf("total = %d, want 150", total)
	}
	if largest != 90 {
		t.Errorf("largest = %d, want 90", largest)
	}

	// Empty slice.
	t2, l2 := focusBlockMinutes(nil)
	if t2 != 0 || l2 != 0 {
		t.Errorf("empty: total=%d largest=%d, want 0 0", t2, l2)
	}
}

func TestHabitColorID(t *testing.T) {
	cases := []struct {
		hex  string
		want string
	}{
		{"#5B7FFF", "9"},
		{"#E9B949", "5"},
		{"#9B7AE0", "3"},
		{"#UNKNOWN", "8"},
		{"", "8"},
	}
	for _, c := range cases {
		got := habitColorID(c.hex)
		if got != c.want {
			t.Errorf("habitColorID(%q) = %q, want %q", c.hex, got, c.want)
		}
	}
}
