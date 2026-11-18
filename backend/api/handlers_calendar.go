package api

import (
	"database/sql"
	"encoding/json"
	"github.com/Enach/paceday/backend/engine"
	"net/http"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/api/gen"
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
	ID              string         `json:"id"`
	Title           string         `json:"title"`
	Start           string         `json:"start"`
	End             string         `json:"end"`
	Color           string         `json:"color,omitempty"`
	Attendees       []string       `json:"attendees,omitempty"`
	AttendeeDetails []attendeeDTO  `json:"attendee_details,omitempty"`
	IsPersonalBlock bool           `json:"is_personal_block,omitempty"`
	Description     string         `json:"description,omitempty"`
	Location        string         `json:"location,omitempty"`
	Conference      *conferenceDTO `json:"conference,omitempty"`
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

	// Conference link — Google Meet or an external provider attached by Paceday.
	if e.HangoutLink != "" {
		dto.Conference = &conferenceDTO{
			Provider: "google_meet",
			URL:      e.HangoutLink,
			Label:    "Google Meet",
		}
	} else if provider, url := conferenceFromEvent(e); url != "" {
		dto.Conference = &conferenceDTO{
			Provider: provider,
			URL:      url,
			Label:    strings.ToUpper(provider),
		}
	}

	return dto
}

// getCalendarClient opens the caller's Google calendar. It returns
// engine.ErrCalendarNotConnected when they have no stored credential and an
// engine.ErrCredentialStore error when the store cannot be read (PAC-21).
func (h *calendarHandlers) getCalendarClient(r *http.Request) (*calendar.CalendarClient, error) {
	userID := userIDFromCtx(r.Context())
	token, err := engine.LoadCalendarTokenCtx(r.Context(), h.db, userID)
	if err != nil {
		return nil, err
	}
	ts := auth.UserTokenSource(r.Context(), h.db, h.oauthConfig, userID, token)
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
		writeCalendarFailure(w, r, "listEvents", err)
		return
	}

	events, err := client.ListEvents(r.Context(), client.CalendarID, timeMin, timeMax)
	if err != nil {
		writeServerError(w, r, "listEvents", err, http.StatusInternalServerError)
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
	// With the parse 400s, before the credential: a caller with no Google
	// credential gets this 400, not the 401 (PAC-133 §2.2, SF-034).
	if !timeMax.After(timeMin) {
		http.Error(w, "end must be after start", http.StatusBadRequest)
		return
	}

	// Attendees as sent: split on ",", no trimming, no lowercasing, each
	// distinct one once in first-appearance order.
	var attendees []string
	if attendeesStr != "" {
		seen := map[string]bool{}
		for _, a := range strings.Split(attendeesStr, ",") {
			if !seen[a] {
				seen[a] = true
				attendees = append(attendees, a)
			}
		}
	}

	client, err := h.getCalendarClient(r)
	if err != nil {
		writeCalendarFailure(w, r, "freeBusy", err)
		return
	}

	// The caller's own calendar is always read, so the provider is never
	// asked about zero calendars (PAC-133 §3.3 item 1).
	ownID := client.CalendarID
	result, err := client.GetFreeBusy(r.Context(), append([]string{ownID}, attendees...), timeMin, timeMax)
	if err != nil {
		writeServerError(w, r, "freeBusy", err, http.StatusInternalServerError)
		return
	}

	answer, err := rawFreeBusyAnswer(result, ownID, attendees)
	if err != nil {
		writeServerError(w, r, "freeBusy", err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(answer)
}

// rawFreeBusyAnswer builds the RawFreeBusyAnswer (PAC-133 §2.2): own is the
// caller's own calendar; calendars has one entry per requested attendee, as
// sent and in order. A known entry carries its complete busy list ([] =
// free); an unknown one carries busy only when windows were reported, so an
// unknown calendar never appears with an empty list. A group carries its
// members, each with its own outcome.
func rawFreeBusyAnswer(result map[string]calendar.FreeBusyEntry, ownID string, attendees []string) (gen.RawFreeBusyAnswer, error) {
	answer := gen.RawFreeBusyAnswer{Calendars: make([]gen.RawFreeBusyCalendar, 0, len(attendees))}

	own := result[ownID]
	var err error
	if own.Known {
		err = answer.Own.FromRawFreeBusyOwnKnown(gen.RawFreeBusyOwnKnown{Status: gen.RawFreeBusyOwnStatusKnown, Busy: rawWindows(own.Busy)})
	} else {
		err = answer.Own.FromRawFreeBusyOwnUnknown(gen.RawFreeBusyOwnUnknown{Status: gen.RawFreeBusyOwnStatusUnknown, Busy: reportedWindows(own.Busy)})
	}
	if err != nil {
		return answer, err
	}

	for _, address := range attendees {
		entry := result[address]
		var group *gen.RawFreeBusyGroup
		if entry.IsGroup() {
			group = &gen.RawFreeBusyGroup{Members: make([]gen.RawFreeBusyMember, 0, len(entry.Members))}
			for _, m := range entry.Members {
				var member gen.RawFreeBusyMember
				if m.Known {
					err = member.FromRawFreeBusyMemberKnown(gen.RawFreeBusyMemberKnown{Address: m.ID, Status: gen.RawFreeBusyMemberStatusKnown, Busy: rawWindows(m.Busy)})
				} else {
					err = member.FromRawFreeBusyMemberUnknown(gen.RawFreeBusyMemberUnknown{Address: m.ID, Status: gen.RawFreeBusyMemberStatusUnknown, Busy: reportedWindows(m.Busy)})
				}
				if err != nil {
					return answer, err
				}
				group.Members = append(group.Members, member)
			}
		}
		var cal gen.RawFreeBusyCalendar
		if entry.Known {
			err = cal.FromRawFreeBusyCalendarKnown(gen.RawFreeBusyCalendarKnown{Address: address, Status: gen.RawFreeBusyCalendarStatusKnown, Busy: rawWindows(entry.Busy), Group: group})
		} else {
			err = cal.FromRawFreeBusyCalendarUnknown(gen.RawFreeBusyCalendarUnknown{Address: address, Status: gen.RawFreeBusyCalendarStatusUnknown, Busy: reportedWindows(entry.Busy), Group: group})
		}
		if err != nil {
			return answer, err
		}
		answer.Calendars = append(answer.Calendars, cal)
	}
	return answer, nil
}

// rawWindows is a complete busy list: never nil, [] when free.
func rawWindows(slots []calendar.TimeSlot) []gen.RawGoogleBusyWindow {
	out := make([]gen.RawGoogleBusyWindow, 0, len(slots))
	for _, s := range slots {
		out = append(out, gen.RawGoogleBusyWindow{Start: s.Start, End: s.End})
	}
	return out
}

// reportedWindows is an unknown calendar's busy: absent when nothing was
// reported, never an empty list.
func reportedWindows(slots []calendar.TimeSlot) *[]gen.RawGoogleBusyWindow {
	if len(slots) == 0 {
		return nil
	}
	w := rawWindows(slots)
	return &w
}
