package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/oauth2"
	googlecalendar "google.golang.org/api/calendar/v3"
)

func TestToCalendarEventDTO_TimedEvent(t *testing.T) {
	e := &googlecalendar.Event{
		Id:      "abc123",
		Summary: "Team Standup",
		Start:   &googlecalendar.EventDateTime{DateTime: "2026-05-01T10:00:00+02:00"},
		End:     &googlecalendar.EventDateTime{DateTime: "2026-05-01T10:30:00+02:00"},
		ColorId: "7",
		HangoutLink: "https://meet.google.com/xyz-abc-def",
		Attendees: []*googlecalendar.EventAttendee{
			{Email: "alice@example.com", ResponseStatus: "accepted"},
			{Email: "room@resource.calendar.google.com", ResponseStatus: "accepted"},
		},
	}

	dto := toCalendarEventDTO(e)

	if dto.ID != "abc123" {
		t.Errorf("ID = %q, want abc123", dto.ID)
	}
	if dto.Title != "Team Standup" {
		t.Errorf("Title = %q, want Team Standup", dto.Title)
	}
	if dto.Start != "2026-05-01T10:00:00+02:00" {
		t.Errorf("Start = %q, want dateTime value", dto.Start)
	}
	if dto.End != "2026-05-01T10:30:00+02:00" {
		t.Errorf("End = %q, want dateTime value", dto.End)
	}
	if dto.Color != "#039BE5" {
		t.Errorf("Color = %q, want #039BE5 (Peacock/7)", dto.Color)
	}
	if dto.Conference == nil || dto.Conference.Provider != "google_meet" {
		t.Errorf("Conference.Provider = %v, want google_meet", dto.Conference)
	}
	// Room resource must be excluded
	if len(dto.Attendees) != 1 || dto.Attendees[0] != "alice@example.com" {
		t.Errorf("Attendees = %v, want [alice@example.com] (room filtered)", dto.Attendees)
	}
	if len(dto.AttendeeDetails) != 1 || dto.AttendeeDetails[0].RSVP != "accepted" {
		t.Errorf("AttendeeDetails RSVP wrong: %+v", dto.AttendeeDetails)
	}
}

func TestToCalendarEventDTO_AllDayEvent(t *testing.T) {
	e := &googlecalendar.Event{
		Id:      "allday1",
		Summary: "Holiday",
		Start:   &googlecalendar.EventDateTime{Date: "2026-05-01"},
		End:     &googlecalendar.EventDateTime{Date: "2026-05-02"},
	}

	dto := toCalendarEventDTO(e)

	if dto.Start != "2026-05-01" {
		t.Errorf("Start = %q, want 2026-05-01 (date-only)", dto.Start)
	}
	if dto.End != "2026-05-02" {
		t.Errorf("End = %q, want 2026-05-02 (date-only)", dto.End)
	}
	if dto.Color != "" {
		t.Errorf("Color = %q, want empty (no colorId)", dto.Color)
	}
}

func TestToCalendarEventDTO_JSONShape(t *testing.T) {
	e := &googlecalendar.Event{
		Id:      "shape1",
		Summary: "Sync",
		Start:   &googlecalendar.EventDateTime{DateTime: "2026-05-01T09:00:00Z"},
		End:     &googlecalendar.EventDateTime{DateTime: "2026-05-01T09:30:00Z"},
	}
	dto := toCalendarEventDTO(e)
	b, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	for _, field := range []string{"id", "title", "start", "end"} {
		if _, ok := m[field]; !ok {
			t.Errorf("JSON missing required field %q", field)
		}
	}
	// Ensure nested Google Calendar structure is NOT present
	if _, ok := m["summary"]; ok {
		t.Error("JSON must not contain 'summary' (should be 'title')")
	}
}

func TestListEvents_BadStart(t *testing.T) {
	db := openTestDB(t)
	h := &calendarHandlers{oauthConfig: &oauth2.Config{}, db: db}

	req := httptest.NewRequest(http.MethodGet, "/api/calendar/events?start=bad&end=2025-01-06T18:00:00Z", nil)
	w := httptest.NewRecorder()
	h.listEvents(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestListEvents_BadEnd(t *testing.T) {
	db := openTestDB(t)
	h := &calendarHandlers{oauthConfig: &oauth2.Config{}, db: db}

	req := httptest.NewRequest(http.MethodGet, "/api/calendar/events?start=2025-01-06T09:00:00Z&end=bad", nil)
	w := httptest.NewRecorder()
	h.listEvents(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}


func TestFreeBusy_BadStart(t *testing.T) {
	db := openTestDB(t)
	h := &calendarHandlers{oauthConfig: &oauth2.Config{}, db: db}

	req := httptest.NewRequest(http.MethodGet, "/api/calendar/freebusy?start=bad&end=2025-01-06T18:00:00Z", nil)
	w := httptest.NewRecorder()
	h.freeBusy(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestFreeBusy_BadEnd(t *testing.T) {
	db := openTestDB(t)
	h := &calendarHandlers{oauthConfig: &oauth2.Config{}, db: db}

	req := httptest.NewRequest(http.MethodGet, "/api/calendar/freebusy?start=2025-01-06T09:00:00Z&end=bad", nil)
	w := httptest.NewRecorder()
	h.freeBusy(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

