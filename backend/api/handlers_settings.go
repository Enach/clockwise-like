package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/Enach/paceday/backend/storage"
	"github.com/robfig/cron/v3"
)

var timePattern = regexp.MustCompile(`^\d{2}:\d{2}$`)

var validLLMProviders = map[string]bool{
	"openai":       true,
	"anthropic":    true,
	"ollama":       true,
	"bedrock":      true,
	"azure_openai": true,
	"vertex":       true,
	"":             true,
}

type settingsHandlers struct {
	db *sql.DB
}

func (h *settingsHandlers) getSettings(w http.ResponseWriter, r *http.Request) {
	s, err := storage.GetSettings(h.db)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s)
}

func (h *settingsHandlers) putSettings(w http.ResponseWriter, r *http.Request) {
	var s storage.Settings
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		writeError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := validateSettings(&s); err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := storage.SaveSettings(h.db, &s); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	updated, err := storage.GetSettings(h.db)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(updated)
}

func validateSettings(s *storage.Settings) error {
	timeFields := map[string]string{
		"workStart":  s.WorkStart,
		"workEnd":    s.WorkEnd,
		"lunchStart": s.LunchStart,
		"lunchEnd":   s.LunchEnd,
	}
	for field, val := range timeFields {
		if val != "" && !timePattern.MatchString(val) {
			return &validationError{field + " must be in HH:MM format"}
		}
	}

	if s.FocusMinBlockMinutes < 0 {
		return &validationError{"focusMinBlockMinutes must be positive"}
	}
	if s.FocusMaxBlockMinutes < 0 {
		return &validationError{"focusMaxBlockMinutes must be positive"}
	}
	if s.FocusDailyTargetMinutes < 0 {
		return &validationError{"focusDailyTargetMinutes must be positive"}
	}
	if s.BufferBeforeMinutes < 0 {
		return &validationError{"bufferBeforeMinutes must be positive"}
	}
	if s.BufferAfterMinutes < 0 {
		return &validationError{"bufferAfterMinutes must be positive"}
	}

	if s.AutoScheduleCron != "" {
		parts := strings.Fields(s.AutoScheduleCron)
		if len(parts) != 5 {
			return &validationError{"autoScheduleCron must be a 5-part cron expression"}
		}
		p := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		if _, err := p.Parse(s.AutoScheduleCron); err != nil {
			return &validationError{"autoScheduleCron is invalid: " + err.Error()}
		}
	}

	if s.LLMProvider != "" && !validLLMProviders[s.LLMProvider] {
		return &validationError{"llmProvider must be one of: openai, anthropic, ollama, bedrock, azure_openai, vertex"}
	}

	return nil
}

type validationError struct {
	msg string
}

func (e *validationError) Error() string { return e.msg }

func writeError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
