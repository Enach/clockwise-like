package api

import "testing"

func TestValidateHabitFields(t *testing.T) {
	if err := validateHabitFields("Morning walk", 30, []int{1, 3, 7}, "08:00", "09:30", 50); err != nil {
		t.Fatalf("valid habit rejected: %v", err)
	}

	tests := []struct {
		name string
		call func() error
	}{
		{"empty title", func() error { return validateHabitFields(" ", 30, []int{1}, "08:00", "09:00", 50) }},
		{"duration too large", func() error { return validateHabitFields("Habit", 1441, []int{1}, "08:00", "09:00", 50) }},
		{"empty days", func() error { return validateHabitFields("Habit", 30, nil, "08:00", "09:00", 50) }},
		{"invalid weekday", func() error { return validateHabitFields("Habit", 30, []int{0}, "08:00", "09:00", 50) }},
		{"duplicate weekday", func() error { return validateHabitFields("Habit", 30, []int{1, 1}, "08:00", "09:00", 50) }},
		{"invalid time", func() error { return validateHabitFields("Habit", 30, []int{1}, "8:00", "09:00", 50) }},
		{"inverted window", func() error { return validateHabitFields("Habit", 30, []int{1}, "10:00", "09:00", 50) }},
		{"invalid priority", func() error { return validateHabitFields("Habit", 30, []int{1}, "08:00", "09:00", 101) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
