package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

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
	_ = json.NewEncoder(w).Encode(s)
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
	_ = json.NewEncoder(w).Encode(updated)
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

	if err := validateSchedulePreferences(s); err != nil {
		return err
	}

	return nil
}

var validScheduleWeekdays = map[string]bool{
	"monday": true, "tuesday": true, "wednesday": true, "thursday": true,
	"friday": true, "saturday": true, "sunday": true,
}

func validateSchedulePreferences(s *storage.Settings) error {
	mode := strings.ToLower(strings.TrimSpace(s.WorkingHours.Mode))
	if mode != "" && mode != "all_days" && mode != "by_day" {
		return &validationError{"workingHours.mode must be all_days or by_day"}
	}
	if mode == "" && len(s.WorkingHours.Days) > 0 {
		return &validationError{"workingHours.mode is required when day-specific hours are provided"}
	}

	if mode == "all_days" {
		if scheduleHasValues(s.WorkingHours.Default) {
			if err := validateDaySchedule("workingHours.default", s.WorkingHours.Default); err != nil {
				return err
			}
		}
	}
	if mode == "by_day" {
		for day, schedule := range s.WorkingHours.Days {
			if !validScheduleWeekdays[strings.ToLower(day)] {
				return &validationError{"workingHours.days." + day + " is not a valid weekday"}
			}
			if err := validateDaySchedule("workingHours.days."+day, schedule); err != nil {
				return err
			}
		}
	}
	for day, schedule := range s.LunchBreaks {
		if !validScheduleWeekdays[strings.ToLower(day)] {
			return &validationError{"lunchBreaks." + day + " is not a valid weekday"}
		}
		if err := validateDaySchedule("lunchBreaks."+day, schedule); err != nil {
			return err
		}
	}
	return nil
}

func scheduleHasValues(s storage.DaySchedule) bool {
	return s.Enabled || s.Start != "" || s.End != ""
}

func validateDaySchedule(field string, schedule storage.DaySchedule) error {
	if !schedule.Enabled {
		if schedule.Start != "" && !validClockTime(schedule.Start) {
			return &validationError{field + ".start must be in HH:MM format"}
		}
		if schedule.End != "" && !validClockTime(schedule.End) {
			return &validationError{field + ".end must be in HH:MM format"}
		}
		return nil
	}
	if schedule.Start == "" || schedule.End == "" {
		return &validationError{field + " requires start and end when enabled"}
	}
	if !validClockTime(schedule.Start) || !validClockTime(schedule.End) {
		return &validationError{field + " start and end must be in HH:MM format"}
	}
	start, _ := time.Parse("15:04", schedule.Start)
	end, _ := time.Parse("15:04", schedule.End)
	if !end.After(start) {
		return &validationError{field + " end must be after start"}
	}
	return nil
}

func validClockTime(value string) bool {
	if !timePattern.MatchString(value) {
		return false
	}
	_, err := time.Parse("15:04", value)
	return err == nil
}

type validationError struct {
	msg string
}

func (e *validationError) Error() string { return e.msg }

func writeError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
