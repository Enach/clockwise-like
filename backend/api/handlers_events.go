package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/Enach/paceday/backend/engine"
	"net/http"
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
	token, err := engine.LoadCalendarToken(h.db, userIDFromCtx(ctx))
	if err != nil {
		return nil, err
	}
	ts := auth.TokenSource(ctx, h.oauthConfig, token)
	return calendar.NewClient(ctx, ts)
}

// PATCH /api/events/:id
func (h *eventHandlers) patchEvent(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "id")
	var body struct {
		Title       *string    `json:"title"`
		Description *string    `json:"description"`
		Location    *string    `json:"location"`
		Start       *time.Time `json:"start"`
		End         *time.Time `json:"end"`
		Attendees   *[]string  `json:"attendees"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid body", http.StatusBadRequest)
		return
	}
	client, err := h.calClient(r.Context())
	if err != nil {
		writeCalendarFailure(w, r, "patchEvent", err)
		return
	}
	existing, err := client.GetEvent(r.Context(), client.CurrentCalendarID(), eventID)
	if err != nil {
		writeError(w, "event not found", http.StatusNotFound)
		return
	}
	if body.Title != nil {
		existing.Summary = *body.Title
	}
	if body.Description != nil {
		existing.Description = *body.Description
	}
	if body.Location != nil {
		existing.Location = *body.Location
	}
	if body.Start != nil {
		existing.Start = &googlecalendar.EventDateTime{DateTime: body.Start.UTC().Format(time.RFC3339)}
	}
	if body.End != nil {
		existing.End = &googlecalendar.EventDateTime{DateTime: body.End.UTC().Format(time.RFC3339)}
	}
	if body.Attendees != nil {
		attendees := make([]*googlecalendar.EventAttendee, len(*body.Attendees))
		for i, email := range *body.Attendees {
			attendees[i] = &googlecalendar.EventAttendee{Email: email}
		}
		existing.Attendees = attendees
	}
	updated, err := client.UpdateEvent(r.Context(), client.CurrentCalendarID(), eventID, existing)
	if err != nil {
		writeServerError(w, r, "update failed", err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(updated)
}

// DELETE /api/events/:id
func (h *eventHandlers) deleteEvent(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "id")
	client, err := h.calClient(r.Context())
	if err != nil {
		writeCalendarFailure(w, r, "deleteEvent", err)
		return
	}
	if err := client.DeleteEvent(r.Context(), client.CurrentCalendarID(), eventID); err != nil {
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
