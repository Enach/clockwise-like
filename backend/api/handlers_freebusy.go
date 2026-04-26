package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

type freeBusyHandlers struct {
	svc *engine.FreeBusyService
}

func newFreeBusyHandlers(db *sql.DB, cfg *oauth2.Config) *freeBusyHandlers {
	return &freeBusyHandlers{svc: engine.NewFreeBusyService(db, cfg)}
}

// POST /api/freebusy
func (h *freeBusyHandlers) query(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	if userID == uuid.Nil {
		writeError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var body struct {
		Emails    []string `json:"emails"`
		StartTime string   `json:"start_time"`
		EndTime   string   `json:"end_time"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(body.Emails) == 0 {
		writeError(w, "emails is required", http.StatusBadRequest)
		return
	}
	if len(body.Emails) > 20 {
		writeError(w, "max 20 emails per request", http.StatusBadRequest)
		return
	}

	start, err := time.Parse(time.RFC3339, body.StartTime)
	if err != nil {
		writeError(w, "start_time must be RFC3339", http.StatusBadRequest)
		return
	}
	end, err := time.Parse(time.RFC3339, body.EndTime)
	if err != nil {
		writeError(w, "end_time must be RFC3339", http.StatusBadRequest)
		return
	}

	results, err := h.svc.Query(r.Context(), userID, body.Emails, start, end)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"results": results})
}
