package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
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
	if body.Title == "" || body.DurationMinutes <= 0 {
		writeError(w, "title and duration_minutes are required", http.StatusBadRequest)
		return
	}

	if len(body.DaysOfWeek) == 0 {
		body.DaysOfWeek = []int{1, 2, 3, 4, 5}
	}
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
	go func() {
		_ = h.eng.ReoptimizeAll(r.Context(), userID)
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

	result, err := storage.UpdateHabit(h.db, id, &updated)
	if err != nil {
		writeError(w, "update failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	go func() {
		_ = h.eng.ReoptimizeAll(r.Context(), userID)
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

	now := time.Now()
	from := now.AddDate(0, -1, 0)
	to := now.AddDate(0, 1, 0)

	if s := r.URL.Query().Get("from"); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			from = t
		}
	}
	if s := r.URL.Query().Get("to"); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			to = t
		}
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

// POST /api/habits/reoptimize
func (h *habitsHandlers) reoptimize(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	go func() {
		if err := h.eng.ReoptimizeAll(r.Context(), userID); err != nil {
			// logged inside ReoptimizeAll
			_ = err
		}
	}()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "reoptimization started"})
}
