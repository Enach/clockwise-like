package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"golang.org/x/oauth2"
)

type focusHandlers struct {
	eng FocusEngine
	db  *sql.DB
}

func (h *focusHandlers) runFocus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Week string `json:"week"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		body.Week = ""
	}

	targetWeek := time.Now()
	if body.Week != "" {
		parsed, err := time.Parse("2006-01-02", body.Week)
		if err != nil {
			writeError(w, "invalid week format, use YYYY-MM-DD", http.StatusBadRequest)
			return
		}
		targetWeek = parsed
	}

	result, err := h.eng.Run(r.Context(), targetWeek)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (h *focusHandlers) listBlocks(w http.ResponseWriter, r *http.Request) {
	weekStr := r.URL.Query().Get("week")
	weekStart := time.Now()
	if weekStr != "" {
		parsed, err := time.Parse("2006-01-02", weekStr)
		if err != nil {
			writeError(w, "invalid week format", http.StatusBadRequest)
			return
		}
		weekStart = parsed
	}

	blocks, err := storage.ListFocusBlocksForWeek(h.db, weekStart)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(blocks)
}

func (h *focusHandlers) clearBlocks(w http.ResponseWriter, r *http.Request) {
	weekStr := r.URL.Query().Get("week")
	weekStart := time.Now()
	if weekStr != "" {
		parsed, err := time.Parse("2006-01-02", weekStr)
		if err != nil {
			writeError(w, "invalid week format", http.StatusBadRequest)
			return
		}
		weekStart = parsed
	}

	n, err := h.eng.ClearWeek(r.Context(), weekStart)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]int{"deleted": n})
}

func newFocusHandlers(db *sql.DB, oauthConfig *oauth2.Config) *focusHandlers {
	return &focusHandlers{
		eng: &engine.FocusTimeEngine{DB: db, OAuthConfig: oauthConfig},
		db:  db,
	}
}
