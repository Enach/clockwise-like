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

type freeBusyWindowDTO struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type freeBusyParticipantDTO struct {
	Email  string              `json:"email"`
	Status string              `json:"status"`
	Busy   []freeBusyWindowDTO `json:"busy"`
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

	participants := make([]freeBusyParticipantDTO, 0, len(results))
	busy := make(map[string][]freeBusyWindowDTO, len(results))
	for _, result := range results {
		windows := make([]freeBusyWindowDTO, 0, len(result.Busy))
		for _, slot := range result.Busy {
			windows = append(windows, freeBusyWindowDTO{Start: slot.Start, End: slot.End})
		}
		participants = append(participants, freeBusyParticipantDTO{
			Email:  result.Email,
			Status: result.Coverage,
			Busy:   windows,
		})
		busy[result.Email] = windows
	}

	w.Header().Set("Content-Type", "application/json")
	// Keep results for older API consumers while exposing the canonical
	// participants/busy shape used by the frontend.
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"start_time":   start,
		"end_time":     end,
		"participants": participants,
		"busy":         busy,
		"results":      results,
	})
}
