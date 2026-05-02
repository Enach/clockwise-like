package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"golang.org/x/oauth2"
	googlecalendar "google.golang.org/api/calendar/v3"
)

type calendarHandlers struct {
	oauthConfig *oauth2.Config
	db          *sql.DB
}

// calendarEventDTO is the shape expected by the frontend CalendarEvent type.
type calendarEventDTO struct {
	ID              string          `json:"id"`
	Title           string          `json:"title"`
	Start           string          `json:"start"`
	End             string          `json:"end"`
	Color           string          `json:"color,omitempty"`
	Attendees       []string        `json:"attendees,omitempty"`
	AttendeeDetails []attendeeDTO   `json:"attendee_details,omitempty"`
	IsPersonalBlock bool            `json:"is_personal_block,omitempty"`
	Description     string          `json:"description,omitempty"`
	Location        string          `json:"location,omitempty"`
	Conference      *conferenceDTO  `json:"conference,omitempty"`
}

type attendeeDTO struct {
	Email     string `json:"email"`
	Name      string `json:"name,omitempty"`
	RSVP      string `json:"rsvp,omitempty"`
	Organizer bool   `json:"organizer,omitempty"`
}

type conferenceDTO struct {
	Provider string `json:"provider"`
	URL      string `json:"url"`
	Label    string `json:"label,omitempty"`
}

// googleEventColors maps Google Calendar event colorId to hex.
var googleEventColors = map[string]string{
	"1":  "#7986CB", // Lavender
	"2":  "#33B679", // Sage
	"3":  "#8E24AA", // Grape
	"4":  "#E67C73", // Flamingo
	"5":  "#F6BF26", // Banana
	"6":  "#F4511E", // Tangerine
	"7":  "#039BE5", // Peacock
	"8":  "#3F51B5", // Blueberry
	"9":  "#0B8043", // Basil
	"10": "#D50000", // Tomato
	"11": "#616161", // Graphite
}

func toCalendarEventDTO(e *googlecalendar.Event) calendarEventDTO {
	dto := calendarEventDTO{
		ID:          e.Id,
		Title:       e.Summary,
		Description: e.Description,
		Location:    e.Location,
	}

	// Flatten start/end — prefer dateTime over date (all-day events have date only).
	if e.Start != nil {
		if e.Start.DateTime != "" {
			dto.Start = e.Start.DateTime
		} else {
			dto.Start = e.Start.Date
		}
	}
	if e.End != nil {
		if e.End.DateTime != "" {
			dto.End = e.End.DateTime
		} else {
			dto.End = e.End.Date
		}
	}

	if c, ok := googleEventColors[e.ColorId]; ok {
		dto.Color = c
	}

	// Attendees — skip room resources.
	for _, a := range e.Attendees {
		if strings.HasSuffix(a.Email, "@resource.calendar.google.com") {
			continue
		}
		dto.Attendees = append(dto.Attendees, a.Email)
		rsvp := "pending"
		switch a.ResponseStatus {
		case "accepted":
			rsvp = "accepted"
		case "declined":
			rsvp = "declined"
		case "tentative":
			rsvp = "tentative"
		}
		dto.AttendeeDetails = append(dto.AttendeeDetails, attendeeDTO{
			Email:     a.Email,
			Name:      a.DisplayName,
			RSVP:      rsvp,
			Organizer: a.Organizer,
		})
	}

	// Conference link — Google Meet via hangoutLink.
	if e.HangoutLink != "" {
		dto.Conference = &conferenceDTO{
			Provider: "google_meet",
			URL:      e.HangoutLink,
			Label:    "Google Meet",
		}
	}

	return dto
}

func (h *calendarHandlers) getCalendarClient(r *http.Request) (*calendar.CalendarClient, error) {
	token, err := auth.TokenFromDB(h.db)
	if err != nil {
		return nil, err
	}
	ts := auth.TokenSource(r.Context(), h.oauthConfig, token)
	return calendar.NewClient(r.Context(), ts)
}

func (h *calendarHandlers) listEvents(w http.ResponseWriter, r *http.Request) {
	startStr := r.URL.Query().Get("start")
	endStr := r.URL.Query().Get("end")

	timeMin, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		http.Error(w, "invalid start param", http.StatusBadRequest)
		return
	}
	timeMax, err := time.Parse(time.RFC3339, endStr)
	if err != nil {
		http.Error(w, "invalid end param", http.StatusBadRequest)
		return
	}

	client, err := h.getCalendarClient(r)
	if err != nil {
		http.Error(w, "not connected: "+err.Error(), http.StatusUnauthorized)
		return
	}

	events, err := client.ListEvents(r.Context(), client.CalendarID, timeMin, timeMax)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	dtos := make([]calendarEventDTO, len(events))
	for i, e := range events {
		dtos[i] = toCalendarEventDTO(e)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dtos)
}

func (h *calendarHandlers) freeBusy(w http.ResponseWriter, r *http.Request) {
	startStr := r.URL.Query().Get("start")
	endStr := r.URL.Query().Get("end")
	attendeesStr := r.URL.Query().Get("attendees")

	timeMin, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		http.Error(w, "invalid start param", http.StatusBadRequest)
		return
	}
	timeMax, err := time.Parse(time.RFC3339, endStr)
	if err != nil {
		http.Error(w, "invalid end param", http.StatusBadRequest)
		return
	}

	var emails []string
	if attendeesStr != "" {
		emails = strings.Split(attendeesStr, ",")
	}

	client, err := h.getCalendarClient(r)
	if err != nil {
		http.Error(w, "not connected: "+err.Error(), http.StatusUnauthorized)
		return
	}

	result, err := client.GetFreeBusy(r.Context(), emails, timeMin, timeMax)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
