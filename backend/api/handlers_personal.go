package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/Enach/clockwise-like/backend/calendar"
	"github.com/Enach/clockwise-like/backend/engine"
	"github.com/Enach/clockwise-like/backend/storage"
	"golang.org/x/oauth2"
)

type personalHandlers struct {
	db      *sql.DB
	blocker *engine.PersonalBlocker
}

func newPersonalHandlers(db *sql.DB, oauthConfig *oauth2.Config) *personalHandlers {
	return &personalHandlers{
		db:      db,
		blocker: &engine.PersonalBlocker{DB: db, OAuthConfig: oauthConfig},
	}
}

func (h *personalHandlers) list(w http.ResponseWriter, r *http.Request) {
	cals, err := storage.ListPersonalCalendars(h.db)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if cals == nil {
		cals = []storage.PersonalCalendar{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cals)
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
	pc := &storage.PersonalCalendar{
		Provider: req.Provider,
		Name:     req.Name,
		URL:      req.URL,
		Enabled:  req.Enabled,
	}
	id, err := storage.InsertPersonalCalendar(h.db, pc)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pc.ID = id
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(pc)
}

func (h *personalHandlers) delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := storage.DeletePersonalCalendar(h.db, id); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *personalHandlers) preview(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, "invalid id", http.StatusBadRequest)
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
	json.NewEncoder(w).Encode(events)
}

func (h *personalHandlers) sync(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := h.blocker.Sync(r.Context(), id); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
