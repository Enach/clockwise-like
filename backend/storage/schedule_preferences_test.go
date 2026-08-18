package storage

import (
	"testing"
	"time"
)

func TestScheduleWindows(t *testing.T) {
	monday := time.Date(2026, time.March, 2, 12, 0, 0, 0, time.UTC)
	s := &Settings{
		WorkStart:    "09:00",
		WorkEnd:      "18:00",
		LunchStart:   "12:00",
		LunchEnd:     "13:00",
		ProtectLunch: true,
		WorkingHours: WorkingHoursSchedule{
			Mode: "by_day",
			Days: map[string]DaySchedule{
				"monday":   {Enabled: true, Start: "08:30", End: "16:30"},
				"saturday": {Enabled: false},
			},
		},
		LunchBreaks: LunchBreakSchedule{
			"monday":  {Enabled: true, Start: "11:30", End: "12:15"},
			"tuesday": {Enabled: false},
		},
	}

	start, end, enabled := s.WorkWindow(monday)
	if !enabled || start != "08:30" || end != "16:30" {
		t.Fatalf("monday work window = %q-%q enabled=%v", start, end, enabled)
	}
	_, _, enabled = s.WorkWindow(monday.AddDate(0, 0, 5))
	if enabled {
		t.Fatal("saturday should be disabled")
	}

	start, end, enabled = s.LunchWindow(monday)
	if !enabled || start != "11:30" || end != "12:15" {
		t.Fatalf("monday lunch window = %q-%q enabled=%v", start, end, enabled)
	}
	_, _, enabled = s.LunchWindow(monday.AddDate(0, 0, 1))
	if enabled {
		t.Fatal("explicitly disabled tuesday lunch should override legacy lunch")
	}
}

func TestScheduleWindows_LegacyFallback(t *testing.T) {
	day := time.Date(2026, time.March, 2, 12, 0, 0, 0, time.UTC)
	s := &Settings{
		WorkStart:    "09:00",
		WorkEnd:      "18:00",
		LunchStart:   "12:00",
		LunchEnd:     "13:00",
		ProtectLunch: true,
	}
	start, end, enabled := s.WorkWindow(day)
	if !enabled || start != "09:00" || end != "18:00" {
		t.Fatalf("legacy work window = %q-%q enabled=%v", start, end, enabled)
	}
	start, end, enabled = s.LunchWindow(day)
	if !enabled || start != "12:00" || end != "13:00" {
		t.Fatalf("legacy lunch window = %q-%q enabled=%v", start, end, enabled)
	}
}
