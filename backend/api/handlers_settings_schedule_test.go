package api

import (
	"testing"

	"github.com/Enach/paceday/backend/storage"
)

func TestValidateSettings_DaySpecificSchedules(t *testing.T) {
	s := &storage.Settings{
		WorkingHours: storage.WorkingHoursSchedule{
			Mode: "by_day",
			Days: map[string]storage.DaySchedule{
				"monday": {Enabled: true, Start: "08:30", End: "17:00"},
				"friday": {Enabled: false},
			},
		},
		LunchBreaks: storage.LunchBreakSchedule{
			"monday": {Enabled: true, Start: "12:00", End: "13:00"},
		},
	}
	if err := validateSettings(s); err != nil {
		t.Fatalf("valid day-specific schedule rejected: %v", err)
	}
}

func TestValidateSettings_InvalidDaySpecificSchedules(t *testing.T) {
	cases := []struct {
		name string
		s    storage.Settings
	}{
		{
			name: "invalid mode",
			s:    storage.Settings{WorkingHours: storage.WorkingHoursSchedule{Mode: "weekly"}},
		},
		{
			name: "inverted work window",
			s: storage.Settings{WorkingHours: storage.WorkingHoursSchedule{
				Mode:    "all_days",
				Default: storage.DaySchedule{Enabled: true, Start: "17:00", End: "09:00"},
			}},
		},
		{
			name: "invalid weekday",
			s: storage.Settings{WorkingHours: storage.WorkingHoursSchedule{
				Mode: "by_day",
				Days: map[string]storage.DaySchedule{
					"mondayy": {Enabled: true, Start: "09:00", End: "17:00"},
				},
			}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateSettings(&tc.s); err == nil {
				t.Fatal("expected schedule validation error")
			}
		})
	}
}
