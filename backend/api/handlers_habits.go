package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

type habitsHandlers struct {
	eng *engine.HabitsEngine
	db  *sql.DB
}

func newHabitsHandlers(db *sql.DB, oauthConfig *oauth2.Config) *habitsHandlers {
	return &habitsHandlers{
		eng: &engine.HabitsEngine{DB: db, OAuthConfig: oauthConfig},
		db:  db,
	}
}

func validateHabitFields(title string, duration int, days []int, windowStart, windowEnd string, priority int) error {
	if strings.TrimSpace(title) == "" {
		return fmt.Errorf("title is required")
	}
	if len([]rune(strings.TrimSpace(title))) > 200 {
		return fmt.Errorf("title must be 200 characters or fewer")
	}
	if duration <= 0 || duration > 24*60 {
		return fmt.Errorf("duration_minutes must be between 1 and 1440")
	}
	if len(days) == 0 {
		return fmt.Errorf("days_of_week must contain at least one weekday")
	}
	seen := make(map[int]bool, len(days))
	for _, day := range days {
		if day < 1 || day > 7 {
			return fmt.Errorf("days_of_week must use Monday=1 through Sunday=7")
		}
		if seen[day] {
			return fmt.Errorf("days_of_week must not contain duplicates")
		}
		seen[day] = true
	}
	if !validHabitClock(windowStart) || !validHabitClock(windowEnd) {
		return fmt.Errorf("window_start and window_end must be in HH:MM format")
	}
	start, _ := time.Parse("15:04", windowStart)
	end, _ := time.Parse("15:04", windowEnd)
	if !end.After(start) {
		return fmt.Errorf("window_end must be after window_start")
	}
	if priority < 0 || priority > 100 {
		return fmt.Errorf("priority must be between 0 and 100")
	}
	return nil
}

func validHabitClock(value string) bool {
	if len(value) != 5 || value[2] != ':' {
		return false
	}
	_, err := time.Parse("15:04", value)
	return err == nil
}

// GET /api/habits/templates
func (h *habitsHandlers) templates(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(engine.HabitTemplates)
}

// POST /api/habits
func (h *habitsHandlers) create(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	var body struct {
		Title           string `json:"title"`
		DurationMinutes int    `json:"duration_minutes"`
		DaysOfWeek      []int  `json:"days_of_week"`
		WindowStart     string `json:"window_start"`
		WindowEnd       string `json:"window_end"`
		Priority        int    `json:"priority"`
		Color           string `json:"color"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	body.Title = strings.TrimSpace(body.Title)
	if body.DaysOfWeek == nil {
		body.DaysOfWeek = []int{1, 2, 3, 4, 5}
	}
	body.WindowStart = strings.TrimSpace(body.WindowStart)
	body.WindowEnd = strings.TrimSpace(body.WindowEnd)
	if body.WindowStart == "" {
		body.WindowStart = "09:00"
	}
	if body.WindowEnd == "" {
		body.WindowEnd = "17:00"
	}
	if body.Priority == 0 {
		body.Priority = 50
	}
	if body.Color == "" {
		body.Color = "#5B7FFF"
	}
	if err := validateHabitFields(body.Title, body.DurationMinutes, body.DaysOfWeek, body.WindowStart, body.WindowEnd, body.Priority); err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}

	habit, err := storage.CreateHabit(h.db, &storage.Habit{
		UserID:          userID,
		Title:           body.Title,
		DurationMinutes: body.DurationMinutes,
		DaysOfWeek:      body.DaysOfWeek,
		WindowStart:     body.WindowStart,
		WindowEnd:       body.WindowEnd,
		Priority:        body.Priority,
		Color:           body.Color,
	})
	if err != nil {
		writeError(w, "create failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Trigger background scheduling for the next 14 days.
	bgCtx := context.WithoutCancel(r.Context())
	go func() {
		_ = h.eng.ReoptimizeAll(bgCtx, userID)
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(habit)
}

// GET /api/habits
func (h *habitsHandlers) list(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	habits, err := storage.ListHabitsByUser(h.db, userID)
	if err != nil {
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if habits == nil {
		habits = []*storage.Habit{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(habits)
}

// PATCH /api/habits/:id
func (h *habitsHandlers) update(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, "invalid id", http.StatusBadRequest)
		return
	}

	existing, err := storage.GetHabitByID(h.db, id)
	if err != nil {
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if existing == nil {
		writeError(w, "not found", http.StatusNotFound)
		return
	}
	if existing.UserID != userID {
		writeError(w, "forbidden", http.StatusForbidden)
		return
	}

	var body struct {
		Title           *string `json:"title"`
		DurationMinutes *int    `json:"duration_minutes"`
		DaysOfWeek      []int   `json:"days_of_week"`
		WindowStart     *string `json:"window_start"`
		WindowEnd       *string `json:"window_end"`
		Priority        *int    `json:"priority"`
		Color           *string `json:"color"`
		Active          *bool   `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	updated := *existing
	if body.Title != nil {
		updated.Title = *body.Title
	}
	if body.DurationMinutes != nil {
		updated.DurationMinutes = *body.DurationMinutes
	}
	if body.DaysOfWeek != nil {
		updated.DaysOfWeek = body.DaysOfWeek
	}
	if body.WindowStart != nil {
		updated.WindowStart = *body.WindowStart
	}
	if body.WindowEnd != nil {
		updated.WindowEnd = *body.WindowEnd
	}
	if body.Priority != nil {
		updated.Priority = *body.Priority
	}
	if body.Color != nil {
		updated.Color = *body.Color
	}
	if body.Active != nil {
		updated.Active = *body.Active
	}
	updated.Title = strings.TrimSpace(updated.Title)
	updated.WindowStart = strings.TrimSpace(updated.WindowStart)
	updated.WindowEnd = strings.TrimSpace(updated.WindowEnd)
	if err := validateHabitFields(updated.Title, updated.DurationMinutes, updated.DaysOfWeek, updated.WindowStart, updated.WindowEnd, updated.Priority); err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}

	result, err := storage.UpdateHabit(h.db, id, &updated)
	if err != nil {
		writeError(w, "update failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	bgCtx := context.WithoutCancel(r.Context())
	go func() {
		_ = h.eng.ReoptimizeAll(bgCtx, userID)
	}()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// DELETE /api/habits/:id
func (h *habitsHandlers) deactivate(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, "invalid id", http.StatusBadRequest)
		return
	}

	existing, err := storage.GetHabitByID(h.db, id)
	if err != nil {
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if existing == nil {
		writeError(w, "not found", http.StatusNotFound)
		return
	}
	if existing.UserID != userID {
		writeError(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := storage.DeactivateHabit(h.db, id); err != nil {
		writeError(w, "deactivate failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/habits/:id/occurrences?from=YYYY-MM-DD&to=YYYY-MM-DD
func (h *habitsHandlers) occurrences(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, "invalid id", http.StatusBadRequest)
		return
	}

	habit, err := storage.GetHabitByID(h.db, id)
	if err != nil {
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if habit == nil {
		writeError(w, "not found", http.StatusNotFound)
		return
	}
	if habit.UserID != userID {
		writeError(w, "forbidden", http.StatusForbidden)
		return
	}

	now := time.Now().UTC()
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)

	if value := r.URL.Query().Get("from"); value != "" {
		t, err := time.Parse("2006-01-02", value)
		if err != nil {
			writeError(w, "from must use YYYY-MM-DD", http.StatusBadRequest)
			return
		}
		from = t
	}
	if value := r.URL.Query().Get("to"); value != "" {
		t, err := time.Parse("2006-01-02", value)
		if err != nil {
			writeError(w, "to must use YYYY-MM-DD", http.StatusBadRequest)
			return
		}
		to = t
	}
	if from.After(to) {
		writeError(w, "from must be on or before to", http.StatusBadRequest)
		return
	}

	occs, err := storage.ListHabitOccurrences(h.db, id, from, to)
	if err != nil {
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if occs == nil {
		occs = []*storage.HabitOccurrence{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(occs)
}

// PATCH /api/habits/:habitID/occurrences/:occurrenceId
func (h *habitsHandlers) updateOccurrence(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	habitID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, "invalid habit id", http.StatusBadRequest)
		return
	}
	occurrenceID, err := uuid.Parse(chi.URLParam(r, "occurrenceId"))
	if err != nil {
		writeError(w, "invalid occurrence id", http.StatusBadRequest)
		return
	}

	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	body.Status = strings.ToLower(strings.TrimSpace(body.Status))
	if body.Status != "completed" && body.Status != "scheduled" {
		writeError(w, "status must be completed or scheduled", http.StatusBadRequest)
		return
	}

	occurrence, err := storage.UpdateHabitOccurrenceStatus(h.db, occurrenceID, habitID, userID, body.Status)
	if err != nil {
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if occurrence == nil {
		writeError(w, "not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(occurrence)
}

// POST /api/habits/reoptimize
func (h *habitsHandlers) reoptimize(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	bgCtx := context.WithoutCancel(r.Context())
	go func() {
		if err := h.eng.ReoptimizeAll(bgCtx, userID); err != nil {
			// logged inside ReoptimizeAll
			_ = err
		}
	}()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "reoptimization started"})
}
