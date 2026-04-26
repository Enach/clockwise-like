package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"golang.org/x/oauth2"
)

type analyticsHandlers struct {
	eng *engine.AnalyticsEngine
	db  *sql.DB
}

func newAnalyticsHandlers(db *sql.DB, oauthConfig *oauth2.Config) *analyticsHandlers {
	return &analyticsHandlers{
		eng: &engine.AnalyticsEngine{DB: db, OAuthConfig: oauthConfig},
		db:  db,
	}
}

// GET /api/analytics/week?date=YYYY-MM-DD
func (h *analyticsHandlers) week(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	date := parseDateParam(r, "date", time.Now())

	result, err := h.eng.ComputeWeek(r.Context(), userID, date)
	if err != nil {
		writeError(w, "compute failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if result == nil {
		writeError(w, "not found", http.StatusNotFound)
		return
	}

	resp := weekWithBreakdown(result)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// GET /api/analytics/trends?weeks=8
func (h *analyticsHandlers) trends(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	weeksStr := r.URL.Query().Get("weeks")
	weeks := 8
	if n, err := strconv.Atoi(weeksStr); err == nil && n > 0 && n <= 52 {
		weeks = n
	}

	rows, err := storage.ListAnalyticsTrends(h.db, userID, weeks)
	if err != nil {
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []*storage.AnalyticsWeek{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rows)
}

// GET /api/analytics/meetings?date=YYYY-MM-DD
func (h *analyticsHandlers) meetings(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	date := parseDateParam(r, "date", time.Now())

	list, err := h.eng.WeekMeetings(r.Context(), userID, date)
	if err != nil {
		writeError(w, "internal error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if list == nil {
		list = []map[string]any{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

// POST /api/analytics/recompute
func (h *analyticsHandlers) recompute(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	go func() {
		_, _ = h.eng.ForceCompute(r.Context(), userID, time.Now())
	}()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "recompute accepted"})
}

// --- helpers -----------------------------------------------------------------

func parseDateParam(r *http.Request, param string, fallback time.Time) time.Time {
	s := r.URL.Query().Get(param)
	if s == "" {
		return fallback
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return fallback
	}
	return t
}

type breakdownEntry struct {
	Category   string  `json:"category"`
	Minutes    int     `json:"minutes"`
	Percentage float64 `json:"percentage"`
	Color      string  `json:"color"`
}

func weekWithBreakdown(a *storage.AnalyticsWeek) map[string]any {
	total := float64(a.TotalWorkingMinutes)
	pct := func(m int) float64 {
		if total == 0 {
			return 0
		}
		return float64(m) / total * 100
	}
	breakdown := []breakdownEntry{
		{"meeting", a.MeetingMinutes, pct(a.MeetingMinutes), "#FF6B6B"},
		{"focus", a.FocusMinutes, pct(a.FocusMinutes), "#5B7FFF"},
		{"habit", a.HabitMinutes, pct(a.HabitMinutes), "#9B7AE0"},
		{"personal", a.PersonalMinutes, pct(a.PersonalMinutes), "#E9B949"},
		{"free", a.FreeMinutes, pct(a.FreeMinutes), "#B0BEC5"},
	}
	return map[string]any{
		"id":                              a.ID,
		"user_id":                         a.UserID,
		"week_start":                      a.WeekStart.Format("2006-01-02"),
		"total_working_minutes":           a.TotalWorkingMinutes,
		"meeting_minutes":                 a.MeetingMinutes,
		"focus_minutes":                   a.FocusMinutes,
		"habit_minutes":                   a.HabitMinutes,
		"buffer_minutes":                  a.BufferMinutes,
		"personal_minutes":                a.PersonalMinutes,
		"free_minutes":                    a.FreeMinutes,
		"meeting_count":                   a.MeetingCount,
		"focus_block_count":               a.FocusBlockCount,
		"habit_completion_rate":           a.HabitCompletionRate,
		"largest_focus_block_minutes":     a.LargestFocusBlockMinutes,
		"top_meeting_titles":              a.TopMeetingTitles,
		"focus_score":                     a.FocusScore,
		"estimated_meeting_cost_minutes":  a.EstimatedMeetingCostMinutes,
		"computed_at":                     a.ComputedAt,
		"breakdown":                       breakdown,
	}
}
