package engine

import (
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
	googlecalendar "google.golang.org/api/calendar/v3"
)

func TestNextWorkingDaysWindowCountsWeekdaysOnly(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 8, 19, 10, 30, 0, 0, loc) // Wednesday
	start, end := nextWorkingDaysWindow(now, AutoDeclineWorkingDays)
	if !start.Equal(now) {
		t.Fatalf("start = %s, want %s", start, now)
	}
	wantEnd := time.Date(2026, 9, 2, 0, 0, 0, 0, loc)
	if !end.Equal(wantEnd) {
		t.Fatalf("end = %s, want %s", end, wantEnd)
	}
}

func TestNextWorkingDaysWindowSkipsWeekendStart(t *testing.T) {
	now := time.Date(2026, 8, 22, 10, 30, 0, 0, time.UTC) // Saturday
	_, end := nextWorkingDaysWindow(now, AutoDeclineWorkingDays)
	wantEnd := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	if !end.Equal(wantEnd) {
		t.Fatalf("end = %s, want %s", end, wantEnd)
	}
}

func TestShouldAutoDeclinePolicy(t *testing.T) {
	settings := &storage.Settings{
		WorkStart:    "09:00",
		WorkEnd:      "18:00",
		LunchStart:   "12:30",
		LunchEnd:     "13:30",
		ProtectLunch: true,
		Timezone:     "UTC",
	}
	loc := time.UTC
	cases := []struct {
		name          string
		start         string
		end           string
		status        string
		organizerSelf bool
		allDay        bool
		want          bool
	}{
		{name: "inside working hours", start: "10:00", end: "11:00", status: "needsAction", want: false},
		{name: "before working hours", start: "08:00", end: "09:00", status: "needsAction", want: true},
		{name: "overlapping lunch", start: "13:00", end: "14:00", status: "needsAction", want: true},
		{name: "accepted invitation", start: "08:00", end: "09:00", status: "accepted", want: false},
		{name: "organizer event", start: "08:00", end: "09:00", status: "needsAction", organizerSelf: true, want: false},
		{name: "all-day event", start: "08:00", end: "09:00", status: "needsAction", allDay: true, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event := &googlecalendar.Event{
				Organizer: &googlecalendar.EventOrganizer{Self: tc.organizerSelf},
				Attendees: []*googlecalendar.EventAttendee{
					{Self: true, ResponseStatus: tc.status},
				},
			}
			if tc.allDay {
				event.Start = &googlecalendar.EventDateTime{Date: "2026-08-19"}
				event.End = &googlecalendar.EventDateTime{Date: "2026-08-20"}
			} else {
				event.Start = &googlecalendar.EventDateTime{DateTime: "2026-08-19T" + tc.start + ":00Z"}
				event.End = &googlecalendar.EventDateTime{DateTime: "2026-08-19T" + tc.end + ":00Z"}
			}
			if got := shouldAutoDecline(event, settings, loc); got != tc.want {
				t.Fatalf("shouldAutoDecline = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestShouldAutoDeclineRequiresIncomingPendingInvitation(t *testing.T) {
	settings := &storage.Settings{
		WorkStart: "09:00",
		WorkEnd:   "18:00",
		Timezone:  "UTC",
	}
	event := &googlecalendar.Event{
		Organizer: &googlecalendar.EventOrganizer{Self: false},
		Start:     &googlecalendar.EventDateTime{DateTime: "2026-08-19T08:00:00Z"},
		End:       &googlecalendar.EventDateTime{DateTime: "2026-08-19T09:00:00Z"},
		Attendees: []*googlecalendar.EventAttendee{
			{Self: true, ResponseStatus: "declined"},
		},
	}
	if shouldAutoDecline(event, settings, time.UTC) {
		t.Fatal("already-declined event should not be changed")
	}
	event.Attendees[0].ResponseStatus = "tentative"
	if !shouldAutoDecline(event, settings, time.UTC) {
		t.Fatal("tentative incoming event should be eligible")
	}
}
