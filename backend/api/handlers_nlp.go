package api

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/nlp"
	"github.com/Enach/paceday/backend/storage"
)

type nlpHandlers struct {
	svc   NLPParser
	smart Scheduler
	db    *sql.DB
}

func (h *nlpHandlers) parse(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Text == "" {
		writeError(w, "missing text field", http.StatusBadRequest)
		return
	}

	result, err := h.svc.Parse(r.Context(), body.Text)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (h *nlpHandlers) confirm(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ParseResult       nlp.ParseResult `json:"parse_result"`
		SelectedSlotIndex int             `json:"selected_slot_index"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	pr := body.ParseResult
	if len(pr.SuggestedSlots) == 0 || body.SelectedSlotIndex >= len(pr.SuggestedSlots) {
		writeError(w, "invalid selected_slot_index", http.StatusBadRequest)
		return
	}

	slot := pr.SuggestedSlots[body.SelectedSlotIndex]
	req := engine.ScheduleRequest{
		Title:       pr.Title,
		Attendees:   pr.Attendees,
		Description: pr.Constraints,
	}

	created, err := h.smart.CreateMeeting(r.Context(), req, slot)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	storage.WriteAuditLog(h.db, "nlp_confirmed", `{"event_id":"`+created.Id+`"}`)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(created)
}
