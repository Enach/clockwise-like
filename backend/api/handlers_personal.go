package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

type personalHandlers struct {
	db      *sql.DB
	blocker PersonalCalendarBlocker
}

func newPersonalHandlers(db *sql.DB, oauthConfig *oauth2.Config) *personalHandlers {
	return newPersonalHandlersWithBlocker(db, &engine.PersonalBlocker{DB: db, OAuthConfig: oauthConfig})
}

func newPersonalHandlersWithBlocker(db *sql.DB, blocker PersonalCalendarBlocker) *personalHandlers {
	return &personalHandlers{db: db, blocker: blocker}
}

type personalCalendarDTO struct {
	ID           string  `json:"id"`
	Label        string  `json:"label"`
	Type         string  `json:"type"`
	URL          string  `json:"url,omitempty"`
	Enabled      bool    `json:"enabled"`
	LastSyncedAt *string `json:"last_synced_at,omitempty"`
}

func toPersonalCalendarDTO(c *storage.PersonalCalendar) personalCalendarDTO {
	dto := personalCalendarDTO{
		ID: strconv.FormatInt(c.ID, 10), Label: c.Name, Type: c.Provider,
		URL: c.URL, Enabled: c.Enabled,
	}
	if c.LastSyncedAt != nil {
		value := c.LastSyncedAt.UTC().Format(time.RFC3339)
		dto.LastSyncedAt = &value
	}
	return dto
}

func (h *personalHandlers) list(w http.ResponseWriter, r *http.Request) {
	cals, err := storage.ListPersonalCalendarsByUser(h.db, userIDFromCtx(r.Context()))
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]personalCalendarDTO, 0, len(cals))
	for i := range cals {
		out = append(out, toPersonalCalendarDTO(&cals[i]))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (h *personalHandlers) create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider string `json:"provider"`
		Name     string `json:"name"`
		URL      string `json:"url"`
		Enabled  bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid body", http.StatusBadRequest)
		return
	}
	if req.Provider == "" {
		writeError(w, "provider required", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		req.Name = "Personal"
	}
	pc := &storage.PersonalCalendar{
		UserID: userIDFromCtx(r.Context()), Provider: req.Provider, Name: req.Name,
		URL: req.URL, Enabled: req.Enabled,
	}
	id, err := storage.InsertPersonalCalendar(h.db, pc)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pc.ID = id
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(toPersonalCalendarDTO(pc))
}

func (h *personalHandlers) patch(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, "invalid id", http.StatusBadRequest)
		return
	}
	var req struct {
		Name    *string `json:"name"`
		URL     *string `json:"url"`
		Enabled *bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid body", http.StatusBadRequest)
		return
	}
	cal, err := storage.UpdatePersonalCalendar(h.db, id, userIDFromCtx(r.Context()), req.Name, req.URL, req.Enabled)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if cal == nil {
		writeError(w, "personal calendar not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(toPersonalCalendarDTO(cal))
}

func (h *personalHandlers) delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, "invalid id", http.StatusBadRequest)
		return
	}
	deleted, err := storage.DeletePersonalCalendarForUser(h.db, id, userIDFromCtx(r.Context()))
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !deleted {
		writeError(w, "personal calendar not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *personalHandlers) ownedCalendar(id int64, userID uuid.UUID) (*storage.PersonalCalendar, error) {
	cal, err := storage.GetPersonalCalendar(h.db, id)
	if err != nil {
		return nil, err
	}
	if cal == nil || cal.UserID != userID {
		return nil, sql.ErrNoRows
	}
	return cal, nil
}

func (h *personalHandlers) preview(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, "invalid id", http.StatusBadRequest)
		return
	}
	if _, err := h.ownedCalendar(id, userIDFromCtx(r.Context())); err != nil {
		writeError(w, "personal calendar not found", http.StatusNotFound)
		return
	}
	start := time.Now().Truncate(24 * time.Hour)
	end := start.AddDate(0, 0, 14)
	events, err := h.blocker.Preview(r.Context(), id, start, end)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if events == nil {
		events = []calendar.GenericEvent{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(events)
}

func (h *personalHandlers) sync(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, "invalid id", http.StatusBadRequest)
		return
	}
	userID := userIDFromCtx(r.Context())
	if _, err := h.ownedCalendar(id, userID); err != nil {
		writeError(w, "personal calendar not found", http.StatusNotFound)
		return
	}
	if err := h.blocker.Sync(r.Context(), id); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := storage.MarkPersonalCalendarSynced(h.db, id, userID); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cal, err := storage.GetPersonalCalendar(h.db, id)
	if err != nil || cal == nil || cal.UserID != userID {
		writeError(w, "personal calendar not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(toPersonalCalendarDTO(cal))
}
