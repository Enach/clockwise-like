package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/Enach/paceday/backend/engine"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/go-chi/chi/v5"
	"golang.org/x/oauth2"
	googlecalendar "google.golang.org/api/calendar/v3"
)

type eventHandlers struct {
	db          *sql.DB
	oauthConfig *oauth2.Config
}

// calClient opens the caller's Google calendar; see getCalendarClient for the
// two errors it distinguishes (PAC-21).
func (h *eventHandlers) calClient(ctx context.Context) (*calendar.CalendarClient, error) {
	userID := userIDFromCtx(ctx)
	token, err := engine.LoadCalendarToken(h.db, userID)
	if err != nil {
		return nil, err
	}
	ts := auth.UserTokenSource(ctx, h.db, h.oauthConfig, userID, token)
	return calendar.NewClient(ctx, ts)
}

// The fixed 400 bodies PAC-29 adds to PATCH and DELETE /api/events/{id}.
const (
	errInvalidSendUpdates       = "invalid send_updates"
	errInvalidRoomResourceEmail = "invalid room_resource_email"
	errMixedEventTimeKinds      = "start and end must both be dates or both be date-times"
)

// roomResourceSuffix marks a Google room resource calendar address.
const roomResourceSuffix = "@resource.calendar.google.com"

// roomResourceEmail is the contract's room_resource_email pattern
// (EventPatchRequest) for a non-empty value.
var roomResourceEmail = regexp.MustCompile(`^[^@\s]+@resource\.calendar\.google\.com$`)

// sendUpdatesParam validates send_updates (PAC-29, D-5): absent means "none",
// what every call did before PAC-29; only "all" and "none" are accepted.
func sendUpdatesParam(v *string) (string, bool) {
	if v == nil {
		return "none", true
	}
	switch *v {
	case "all", "none":
		return *v, true
	}
	return "", false
}

// parseEventTime parses a start/end of EventPatchRequest: a bare YYYY-MM-DD
// date is written as Google `date`, an RFC3339 date-time as `dateTime` in
// UTC (PAC-29, D-8). Anything else is not accepted.
func parseEventTime(s string) (*googlecalendar.EventDateTime, bool) {
	if len(s) == len("2006-01-02") {
		if _, err := time.Parse("2006-01-02", s); err != nil {
			return nil, false
		}
		return &googlecalendar.EventDateTime{Date: s}, true
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, false
	}
	return &googlecalendar.EventDateTime{DateTime: t.UTC().Format(time.RFC3339)}, true
}

// isAllDay reports whether a start/end is a bare date. An absent one is
// neither kind (known is false) and never makes a pair mixed.
func isAllDay(t *googlecalendar.EventDateTime) (allDay, known bool) {
	if t == nil || (t.Date == "" && t.DateTime == "") {
		return false, false
	}
	return t.DateTime == "", true
}

func isRoomAttendee(a *googlecalendar.EventAttendee) bool {
	return a.Resource || strings.HasSuffix(strings.ToLower(a.Email), roomResourceSuffix)
}

// mergeAttendees applies PATCH's attendees and room_resource_email to the
// stored list (PAC-29, D-6, D-7). emails, when non-nil, replaces the non-room
// attendees: an email already on the event keeps its Google entry (name,
// RSVP, organizer), a new one is added email-only, and duplicates collapse
// case-insensitively. Every room already on the event is kept. room, when
// non-empty, is added as a resource attendee if absent. Nothing here removes
// a room.
func mergeAttendees(existing []*googlecalendar.EventAttendee, emails *[]string, room string) []*googlecalendar.EventAttendee {
	out := existing
	if emails != nil {
		byEmail := make(map[string]*googlecalendar.EventAttendee, len(existing))
		rooms := make([]*googlecalendar.EventAttendee, 0)
		roomEmails := make(map[string]bool)
		for _, a := range existing {
			if a == nil {
				continue
			}
			if isRoomAttendee(a) {
				rooms = append(rooms, a)
				roomEmails[strings.ToLower(a.Email)] = true
				continue
			}
			byEmail[strings.ToLower(a.Email)] = a
		}
		out = make([]*googlecalendar.EventAttendee, 0, len(*emails)+len(rooms)+1)
		seen := make(map[string]bool, len(*emails))
		for _, email := range *emails {
			key := strings.ToLower(email)
			if seen[key] || roomEmails[key] {
				continue
			}
			seen[key] = true
			if a, ok := byEmail[key]; ok {
				out = append(out, a)
			} else {
				// A room address listed here is still a room (D-6).
				out = append(out, &googlecalendar.EventAttendee{Email: email, Resource: strings.HasSuffix(key, roomResourceSuffix)})
			}
		}
		out = append(out, rooms...)
	}
	if room != "" {
		for _, a := range out {
			if a != nil && strings.EqualFold(a.Email, room) {
				return out
			}
		}
		out = append(out, &googlecalendar.EventAttendee{Email: room, Resource: true})
	}
	return out
}

// patchEventBody is EventPatchRequest as the handler reads it.
// attendee_details is declared by the contract and accepted and ignored
// (PAC-29, D-7), so it has no field: encoding/json drops it.
type patchEventBody struct {
	Title             *string   `json:"title"`
	Description       *string   `json:"description"`
	Location          *string   `json:"location"`
	Start             *string   `json:"start"`
	End               *string   `json:"end"`
	Attendees         *[]string `json:"attendees"`
	SendUpdates       *string   `json:"send_updates"`
	RoomResourceEmail *string   `json:"room_resource_email"`
}

// patchEventRequest is a patchEventBody that passed every request-only check.
type patchEventRequest struct {
	body        patchEventBody
	start, end  *googlecalendar.EventDateTime
	sendUpdates string
	room        string
}

// readPatchEvent runs PATCH's request-only checks (D-10: before the
// credential and before any Google call). On failure it returns the fixed
// 400 message.
func readPatchEvent(r *http.Request) (*patchEventRequest, string) {
	var req patchEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req.body); err != nil {
		return nil, "invalid body"
	}
	var ok bool
	if req.body.Start != nil {
		if req.start, ok = parseEventTime(*req.body.Start); !ok {
			return nil, "invalid body"
		}
	}
	if req.body.End != nil {
		if req.end, ok = parseEventTime(*req.body.End); !ok {
			return nil, "invalid body"
		}
	}
	if req.sendUpdates, ok = sendUpdatesParam(req.body.SendUpdates); !ok {
		return nil, errInvalidSendUpdates
	}
	if v := req.body.RoomResourceEmail; v != nil && *v != "" {
		if !roomResourceEmail.MatchString(*v) {
			return nil, errInvalidRoomResourceEmail
		}
		req.room = *v
	}
	return &req, ""
}

// apply writes the patch onto the stored event. It returns false, writing
// nothing, when start and end would end up one a date and one a date-time.
func (req *patchEventRequest) apply(ev *googlecalendar.Event) bool {
	start, end := ev.Start, ev.End
	if req.start != nil {
		start = req.start
	}
	if req.end != nil {
		end = req.end
	}
	startAllDay, startKnown := isAllDay(start)
	endAllDay, endKnown := isAllDay(end)
	if startKnown && endKnown && startAllDay != endAllDay {
		return false
	}
	ev.Start, ev.End = start, end
	if req.body.Title != nil {
		ev.Summary = *req.body.Title
	}
	if req.body.Description != nil {
		ev.Description = *req.body.Description
	}
	if req.body.Location != nil {
		ev.Location = *req.body.Location
	}
	if req.body.Attendees != nil || req.room != "" {
		ev.Attendees = mergeAttendees(ev.Attendees, req.body.Attendees, req.room)
		if len(ev.Attendees) == 0 {
			// The client library omits an empty list; send it, so
			// "attendees": [] still removes every guest.
			ev.ForceSendFields = append(ev.ForceSendFields, "Attendees")
		}
	}
	return true
}

// PATCH /api/events/:id
//
// Error precedence (PAC-29, D-10): request-only checks (400), then the
// credential (401/500), then GetEvent (404/500), then the start/end kind
// check against the stored event (400). Google is written only after all of
// them pass.
func (h *eventHandlers) patchEvent(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "id")
	req, msg := readPatchEvent(r)
	if req == nil {
		writeError(w, msg, http.StatusBadRequest)
		return
	}
	client, err := h.calClient(r.Context())
	if err != nil {
		writeCalendarFailure(w, r, "patchEvent", err)
		return
	}
	existing, err := client.GetEvent(r.Context(), client.CurrentCalendarID(), eventID)
	if err != nil {
		if calendar.IsEventGone(err) {
			writeError(w, "event not found", http.StatusNotFound)
			return
		}
		writeServerError(w, r, "patchEvent get", err, http.StatusInternalServerError)
		return
	}
	// Google answers a recently deleted event with 200 and status
	// cancelled; writing it would restore it (PAC-29, D-11).
	if existing.Status == "cancelled" {
		writeError(w, "event not found", http.StatusNotFound)
		return
	}
	if !req.apply(existing) {
		writeError(w, errMixedEventTimeKinds, http.StatusBadRequest)
		return
	}
	updated, err := client.UpdateEventSendUpdates(r.Context(), client.CurrentCalendarID(), eventID, existing, req.sendUpdates)
	if err != nil {
		writeServerError(w, r, "update failed", err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(toCalendarEventDTO(updated))
}

// DELETE /api/events/:id
//
// An invalid send_updates is a 400 before the credential is read and before
// any Google call (PAC-29, D-10). A Google 404/410 is a 404 (D-9).
func (h *eventHandlers) deleteEvent(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "id")
	var param *string
	if v, present := r.URL.Query()["send_updates"]; present && len(v) > 0 {
		param = &v[0]
	}
	sendUpdates, ok := sendUpdatesParam(param)
	if !ok {
		writeError(w, errInvalidSendUpdates, http.StatusBadRequest)
		return
	}
	client, err := h.calClient(r.Context())
	if err != nil {
		writeCalendarFailure(w, r, "deleteEvent", err)
		return
	}
	if err := client.DeleteEventSendUpdates(r.Context(), client.CurrentCalendarID(), eventID, sendUpdates); err != nil {
		if calendar.IsEventGone(err) {
			writeError(w, "event not found", http.StatusNotFound)
			return
		}
		writeServerError(w, r, "delete failed", err, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/rooms?q=
func (h *eventHandlers) listRooms(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(r.URL.Query().Get("q"))
	client, err := h.calClient(r.Context())
	if err != nil {
		writeCalendarFailure(w, r, "listRooms", err)
		return
	}
	rooms, err := client.ListRooms(r.Context(), q)
	if err != nil {
		writeServerError(w, r, "listRooms", err, http.StatusInternalServerError)
		return
	}
	if rooms == nil {
		rooms = []calendar.Room{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rooms)
}

// GET /api/attendees/suggest?q=
func (h *eventHandlers) suggestAttendees(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(r.URL.Query().Get("q"))
	client, err := h.calClient(r.Context())
	if err != nil {
		writeCalendarFailure(w, r, "suggestAttendees", err)
		return
	}
	suggestions, err := client.SuggestAttendees(r.Context(), q, 30*24*time.Hour)
	if err != nil {
		writeServerError(w, r, "suggestAttendees", err, http.StatusInternalServerError)
		return
	}
	if suggestions == nil {
		suggestions = []calendar.AttendeeSuggestion{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(suggestions)
}
