package api

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/Enach/clockwise-like/backend/nlp"
	"github.com/Enach/clockwise-like/backend/storage"
)

type llmHandlers struct {
	db *sql.DB
}

func (h *llmHandlers) testLLM(w http.ResponseWriter, r *http.Request) {
	s, err := storage.GetSettings(h.db)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	client, err := nlp.NewLLMClientFromSettings(s)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	response, err := client.Complete(r.Context(), "You are a helpful assistant.", "Say hello")
	if err != nil {
		writeError(w, err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":       true,
		"response": response,
		"provider": s.LLMProvider,
	})
}
