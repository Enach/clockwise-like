package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"golang.org/x/oauth2"
)

type bookingHandlers struct {
	eng *engine.BookingEngine
	db  *sql.DB
}

func newBookingHandlers(db *sql.DB, oauthConfig *oauth2.Config) *bookingHandlers {
	return &bookingHandlers{
		eng: &engine.BookingEngine{DB: db, OAuthConfig: oauthConfig},
		db:  db,
	}
}

// GET /api/book/{slug}
func (h *bookingHandlers) getLinkInfo(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	link, err := storage.GetSchedulingLinkBySlug(h.db, slug)
	if err != nil {
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if link == nil {
		writeError(w, "not found", http.StatusNotFound)
		return
	}

	hosts, err := storage.GetAcceptedHosts(h.db, link.ID)
	if err != nil {
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}

	var hostNames []string
	for _, host := range hosts {
		u, err := storage.GetUserByID(h.db, host.UserID)
		if err != nil || u == nil {
			continue
		}
		hostNames = append(hostNames, u.Name)
	}

	resp := map[string]any{
		"id":               link.ID,
		"title":            link.Title,
		"slug":             link.Slug,
		"duration_options": link.DurationOptions,
		"days_of_week":     link.DaysOfWeek,
		"host_names":       hostNames,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// GET /api/book/{slug}/slots?date=YYYY-MM-DD&duration=30
func (h *bookingHandlers) getSlots(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	link, err := storage.GetSchedulingLinkBySlug(h.db, slug)
	if err != nil {
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if link == nil {
		writeError(w, "not found", http.StatusNotFound)
		return
	}

	dateStr := r.URL.Query().Get("date")
	if dateStr == "" {
		writeError(w, "date query param required (YYYY-MM-DD)", http.StatusBadRequest)
		return
	}
	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		writeError(w, "invalid date format, use YYYY-MM-DD", http.StatusBadRequest)
		return
	}

	durationStr := r.URL.Query().Get("duration")
	if durationStr == "" {
		if len(link.DurationOptions) > 0 {
			durationStr = strconv.Itoa(link.DurationOptions[0])
		} else {
			durationStr = "30"
		}
	}
	duration, err := strconv.Atoi(durationStr)
	if err != nil || duration <= 0 {
		writeError(w, "invalid duration", http.StatusBadRequest)
		return
	}

	slots, err := h.eng.CollectiveSlots(r.Context(), link, date, duration)
	if err != nil {
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}

	if slots == nil {
		slots = []engine.AvailableSlot{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(slots)
}

// POST /api/book/{slug}
func (h *bookingHandlers) createBooking(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	link, err := storage.GetSchedulingLinkBySlug(h.db, slug)
	if err != nil {
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if link == nil {
		writeError(w, "not found", http.StatusNotFound)
		return
	}

	var body struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Start    string `json:"start"`
		End      string `json:"end"`
		Notes    string `json:"notes"`
		Duration int    `json:"duration"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if body.Name == "" || body.Email == "" || body.Start == "" || body.End == "" {
		writeError(w, "name, email, start, and end are required", http.StatusBadRequest)
		return
	}

	start, err := time.Parse(time.RFC3339, body.Start)
	if err != nil {
		writeError(w, "invalid start time, use RFC3339", http.StatusBadRequest)
		return
	}
	end, err := time.Parse(time.RFC3339, body.End)
	if err != nil {
		writeError(w, "invalid end time, use RFC3339", http.StatusBadRequest)
		return
	}
	if !end.After(start) {
		writeError(w, "end must be after start", http.StatusBadRequest)
		return
	}

	booking, err := h.eng.ConfirmBooking(r.Context(), link, body.Name, body.Email, start, end, body.Notes)
	if err != nil {
		writeError(w, "booking failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(booking)
}
