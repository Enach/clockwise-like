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
)

type calendarHandlers struct {
	oauthConfig *oauth2.Config
	db          *sql.DB
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(events)
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
	json.NewEncoder(w).Encode(result)
}
