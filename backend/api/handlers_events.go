package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/Enach/clockwise-like/backend/auth"
	"github.com/Enach/clockwise-like/backend/calendar"
	googlecalendar "google.golang.org/api/calendar/v3"
	"golang.org/x/oauth2"
)

type eventHandlers struct {
	db          *sql.DB
	oauthConfig *oauth2.Config
}

func (h *eventHandlers) calClient(ctx context.Context) (*calendar.CalendarClient, error) {
	token, err := auth.TokenFromDB(h.db)
	if err != nil || token == nil {
		return nil, fmt.Errorf("not authenticated")
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
		Attendees   []string   `json:"attendees"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid body", http.StatusBadRequest)
		return
	}
	client, err := h.calClient(r.Context())
	if err != nil {
		writeError(w, err.Error(), http.StatusUnauthorized)
		return
	}
	existing, err := client.GetEvent(r.Context(), client.CalendarID, eventID)
	if err != nil {
		writeError(w, "event not found: "+err.Error(), http.StatusNotFound)
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
	if len(body.Attendees) > 0 {
		attendees := make([]*googlecalendar.EventAttendee, len(body.Attendees))
		for i, email := range body.Attendees {
			attendees[i] = &googlecalendar.EventAttendee{Email: email}
		}
		existing.Attendees = attendees
	}
	updated, err := client.UpdateEvent(r.Context(), client.CalendarID, eventID, existing)
	if err != nil {
		writeError(w, "update failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(updated)
}

// DELETE /api/events/:id
func (h *eventHandlers) deleteEvent(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "id")
	client, err := h.calClient(r.Context())
	if err != nil {
		writeError(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if err := client.DeleteEvent(r.Context(), client.CalendarID, eventID); err != nil {
		writeError(w, "delete failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/rooms?q=
func (h *eventHandlers) listRooms(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(r.URL.Query().Get("q"))
	client, err := h.calClient(r.Context())
	if err != nil {
		writeError(w, err.Error(), http.StatusUnauthorized)
		return
	}
	rooms, err := client.ListRooms(r.Context(), q)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if rooms == nil {
		rooms = []calendar.Room{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rooms)
}

// GET /api/attendees/suggest?q=
func (h *eventHandlers) suggestAttendees(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(r.URL.Query().Get("q"))
	client, err := h.calClient(r.Context())
	if err != nil {
		writeError(w, err.Error(), http.StatusUnauthorized)
		return
	}
	suggestions, err := client.SuggestAttendees(r.Context(), q, 30*24*time.Hour)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if suggestions == nil {
		suggestions = []calendar.AttendeeSuggestion{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(suggestions)
}
