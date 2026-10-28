package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
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
	if err == nil && result == nil {
		// Unreachable (PAC-42, audit U-01): the upsert's RETURNING always yields a row.
		// Kept as a guard against a nil dereference; the contract documents it as this 500.
		err = errors.New("analytics: compute returned no week")
	}
	if err != nil {
		writeServerError(w, r, "compute failed", err, http.StatusInternalServerError)
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
	out := make([]analyticsWeekResponse, 0, len(rows))
	for _, a := range rows {
		out = append(out, newAnalyticsWeekResponse(a))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// GET /api/analytics/meetings?date=YYYY-MM-DD
func (h *analyticsHandlers) meetings(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	date := parseDateParam(r, "date", time.Now())

	list, err := h.eng.WeekMeetings(r.Context(), userID, date)
	if err != nil {
		writeCalendarFailure(w, r, "weekMeetings", err)
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
	bgCtx := context.WithoutCancel(r.Context())
	go func() {
		_, _ = h.eng.ForceCompute(bgCtx, userID, time.Now())
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

// analyticsWeekResponse is one analytics week as both reads answer it (PAC-42):
// GET /api/analytics/trends items (AnalyticsWeekRow) and, with `breakdown` added,
// GET /api/analytics/week (AnalyticsWeekWithBreakdown). Building both from one type
// keeps their formats and scales equal:
//   - week_start is the calendar date of the Monday, "2006-01-02", never a timestamp;
//   - habit_completion_rate is a percentage (0..100), the scale of focus_score and
//     breakdown[].percentage. Storage keeps the 0..1 fraction (spec D-3), so the
//     ×100 happens here, on read only, and a code revert restores every row.
type analyticsWeekResponse struct {
	ID                          uuid.UUID              `json:"id"`
	UserID                      uuid.UUID              `json:"user_id"`
	WeekStart                   string                 `json:"week_start"`
	TotalWorkingMinutes         int                    `json:"total_working_minutes"`
	MeetingMinutes              int                    `json:"meeting_minutes"`
	FocusMinutes                int                    `json:"focus_minutes"`
	HabitMinutes                int                    `json:"habit_minutes"`
	BufferMinutes               int                    `json:"buffer_minutes"`
	PersonalMinutes             int                    `json:"personal_minutes"`
	FreeMinutes                 int                    `json:"free_minutes"`
	MeetingCount                int                    `json:"meeting_count"`
	FocusBlockCount             int                    `json:"focus_block_count"`
	HabitCompletionRate         float64                `json:"habit_completion_rate"`
	LargestFocusBlockMinutes    int                    `json:"largest_focus_block_minutes"`
	TopMeetingTitles            []storage.MeetingTitle `json:"top_meeting_titles"`
	FocusScore                  int                    `json:"focus_score"`
	EstimatedMeetingCostMinutes int                    `json:"estimated_meeting_cost_minutes"`
	ComputedAt                  time.Time              `json:"computed_at"`
}

type analyticsWeekWithBreakdownResponse struct {
	analyticsWeekResponse
	Breakdown []breakdownEntry `json:"breakdown"`
}

func newAnalyticsWeekResponse(a *storage.AnalyticsWeek) analyticsWeekResponse {
	titles := a.TopMeetingTitles
	if titles == nil {
		titles = make([]storage.MeetingTitle, 0)
	}
	return analyticsWeekResponse{
		ID:                          a.ID,
		UserID:                      a.UserID,
		WeekStart:                   a.WeekStart.Format("2006-01-02"),
		TotalWorkingMinutes:         a.TotalWorkingMinutes,
		MeetingMinutes:              a.MeetingMinutes,
		FocusMinutes:                a.FocusMinutes,
		HabitMinutes:                a.HabitMinutes,
		BufferMinutes:               a.BufferMinutes,
		PersonalMinutes:             a.PersonalMinutes,
		FreeMinutes:                 a.FreeMinutes,
		MeetingCount:                a.MeetingCount,
		FocusBlockCount:             a.FocusBlockCount,
		HabitCompletionRate:         a.HabitCompletionRate * 100,
		LargestFocusBlockMinutes:    a.LargestFocusBlockMinutes,
		TopMeetingTitles:            titles,
		FocusScore:                  a.FocusScore,
		EstimatedMeetingCostMinutes: a.EstimatedMeetingCostMinutes,
		ComputedAt:                  a.ComputedAt,
	}
}

func weekWithBreakdown(a *storage.AnalyticsWeek) analyticsWeekWithBreakdownResponse {
	total := float64(a.TotalWorkingMinutes)
	pct := func(m int) float64 {
		if total == 0 {
			return 0
		}
		return float64(m) / total * 100
	}
	return analyticsWeekWithBreakdownResponse{
		analyticsWeekResponse: newAnalyticsWeekResponse(a),
		Breakdown: []breakdownEntry{
			{"meeting", a.MeetingMinutes, pct(a.MeetingMinutes), "#FF6B6B"},
			{"focus", a.FocusMinutes, pct(a.FocusMinutes), "#5B7FFF"},
			{"habit", a.HabitMinutes, pct(a.HabitMinutes), "#9B7AE0"},
			{"personal", a.PersonalMinutes, pct(a.PersonalMinutes), "#E9B949"},
			{"free", a.FreeMinutes, pct(a.FreeMinutes), "#B0BEC5"},
		},
	}
}
