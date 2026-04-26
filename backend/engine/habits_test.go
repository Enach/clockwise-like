package engine

import (
	"testing"
	"time"
)

func TestFindFirstSlot_Empty(t *testing.T) {
	base := time.Date(2024, 6, 3, 9, 0, 0, 0, time.UTC) // 09:00
	end := base.Add(3 * time.Hour)                       // 12:00
	dur := 30 * time.Minute

	slot := findFirstSlot(base, end, dur, nil)
	if slot == nil {
		t.Fatal("expected a slot, got nil")
		return
	}
	if !slot.start.Equal(base) {
		t.Errorf("slot.start = %v, want %v", slot.start, base)
	}
}

func TestFindFirstSlot_WithBusy(t *testing.T) {
	base := time.Date(2024, 6, 3, 9, 0, 0, 0, time.UTC)
	windowEnd := base.Add(2 * time.Hour) // 11:00
	dur := 30 * time.Minute

	// Busy: 09:00-10:00
	merged := []interval{{start: base, end: base.Add(time.Hour)}}
	slot := findFirstSlot(base, windowEnd, dur, merged)
	if slot == nil {
		t.Fatal("expected a slot after the busy block")
		return
	}
	want := base.Add(time.Hour) // 10:00
	if !slot.start.Equal(want) {
		t.Errorf("slot.start = %v, want %v", slot.start, want)
	}
}

func TestFindFirstSlot_NoRoom(t *testing.T) {
	base := time.Date(2024, 6, 3, 9, 0, 0, 0, time.UTC)
	windowEnd := base.Add(30 * time.Minute) // only 30 min window
	dur := 60 * time.Minute                 // needs 60 min — won't fit

	slot := findFirstSlot(base, windowEnd, dur, nil)
	if slot != nil {
		t.Errorf("expected nil slot, got %v", slot)
	}
}

func TestContainsInt(t *testing.T) {
	if !containsInt([]int{1, 2, 3, 4, 5}, 3) {
		t.Error("expected true for 3 in [1,2,3,4,5]")
	}
	if containsInt([]int{1, 2, 3, 4, 5}, 6) {
		t.Error("expected false for 6 in [1,2,3,4,5]")
	}
	if containsInt(nil, 1) {
		t.Error("expected false for nil slice")
	}
}

func TestHabitTemplatesCount(t *testing.T) {
	if len(HabitTemplates) != 12 {
		t.Errorf("expected 12 templates, got %d", len(HabitTemplates))
	}
	for i, tmpl := range HabitTemplates {
		if tmpl.Title == "" {
			t.Errorf("template[%d] has empty title", i)
		}
		if tmpl.DurationMinutes <= 0 {
			t.Errorf("template[%d] has non-positive duration", i)
		}
		if len(tmpl.DaysOfWeek) == 0 {
			t.Errorf("template[%d] has empty days_of_week", i)
		}
	}
}
