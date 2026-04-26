package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// MeetingTitle is one entry in the top_meeting_titles JSONB field.
type MeetingTitle struct {
	Title           string `json:"title"`
	DurationMinutes int    `json:"duration_minutes"`
}

// AnalyticsWeek holds the computed time breakdown for one calendar week.
type AnalyticsWeek struct {
	ID                          uuid.UUID      `json:"id"`
	UserID                      uuid.UUID      `json:"user_id"`
	WeekStart                   time.Time      `json:"week_start"`
	TotalWorkingMinutes         int            `json:"total_working_minutes"`
	MeetingMinutes              int            `json:"meeting_minutes"`
	FocusMinutes                int            `json:"focus_minutes"`
	HabitMinutes                int            `json:"habit_minutes"`
	BufferMinutes               int            `json:"buffer_minutes"`
	PersonalMinutes             int            `json:"personal_minutes"`
	FreeMinutes                 int            `json:"free_minutes"`
	MeetingCount                int            `json:"meeting_count"`
	FocusBlockCount             int            `json:"focus_block_count"`
	HabitCompletionRate         float64        `json:"habit_completion_rate"`
	LargestFocusBlockMinutes    int            `json:"largest_focus_block_minutes"`
	TopMeetingTitles            []MeetingTitle `json:"top_meeting_titles"`
	FocusScore                  int            `json:"focus_score"`
	EstimatedMeetingCostMinutes int            `json:"estimated_meeting_cost_minutes"`
	ComputedAt                  time.Time      `json:"computed_at"`
}

// UpsertAnalyticsWeek inserts or replaces an analytics row for (user, week_start).
func UpsertAnalyticsWeek(db *sql.DB, a *AnalyticsWeek) (*AnalyticsWeek, error) {
	titlesJSON, err := json.Marshal(a.TopMeetingTitles)
	if err != nil {
		titlesJSON = []byte("[]")
	}
	row := db.QueryRowContext(context.Background(), `
		INSERT INTO analytics_weeks
			(user_id, week_start, total_working_minutes,
			 meeting_minutes, focus_minutes, habit_minutes, buffer_minutes,
			 personal_minutes, free_minutes, meeting_count, focus_block_count,
			 habit_completion_rate, largest_focus_block_minutes, top_meeting_titles,
			 focus_score, estimated_meeting_cost_minutes, computed_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,NOW())
		ON CONFLICT (user_id, week_start) DO UPDATE SET
			total_working_minutes        = EXCLUDED.total_working_minutes,
			meeting_minutes              = EXCLUDED.meeting_minutes,
			focus_minutes                = EXCLUDED.focus_minutes,
			habit_minutes                = EXCLUDED.habit_minutes,
			buffer_minutes               = EXCLUDED.buffer_minutes,
			personal_minutes             = EXCLUDED.personal_minutes,
			free_minutes                 = EXCLUDED.free_minutes,
			meeting_count                = EXCLUDED.meeting_count,
			focus_block_count            = EXCLUDED.focus_block_count,
			habit_completion_rate        = EXCLUDED.habit_completion_rate,
			largest_focus_block_minutes  = EXCLUDED.largest_focus_block_minutes,
			top_meeting_titles           = EXCLUDED.top_meeting_titles,
			focus_score                  = EXCLUDED.focus_score,
			estimated_meeting_cost_minutes = EXCLUDED.estimated_meeting_cost_minutes,
			computed_at                  = NOW()
		RETURNING id, user_id, week_start, total_working_minutes,
			meeting_minutes, focus_minutes, habit_minutes, buffer_minutes,
			personal_minutes, free_minutes, meeting_count, focus_block_count,
			habit_completion_rate, largest_focus_block_minutes, top_meeting_titles,
			focus_score, estimated_meeting_cost_minutes, computed_at
	`, a.UserID, a.WeekStart.Format("2006-01-02"),
		a.TotalWorkingMinutes, a.MeetingMinutes, a.FocusMinutes,
		a.HabitMinutes, a.BufferMinutes, a.PersonalMinutes, a.FreeMinutes,
		a.MeetingCount, a.FocusBlockCount, a.HabitCompletionRate,
		a.LargestFocusBlockMinutes, titlesJSON,
		a.FocusScore, a.EstimatedMeetingCostMinutes)
	return scanAnalyticsWeek(row)
}

func GetAnalyticsWeek(db *sql.DB, userID uuid.UUID, weekStart time.Time) (*AnalyticsWeek, error) {
	row := db.QueryRowContext(context.Background(), `
		SELECT id, user_id, week_start, total_working_minutes,
			meeting_minutes, focus_minutes, habit_minutes, buffer_minutes,
			personal_minutes, free_minutes, meeting_count, focus_block_count,
			habit_completion_rate, largest_focus_block_minutes, top_meeting_titles,
			focus_score, estimated_meeting_cost_minutes, computed_at
		FROM analytics_weeks WHERE user_id = $1 AND week_start = $2
	`, userID, weekStart.Format("2006-01-02"))
	return scanAnalyticsWeek(row)
}

func ListAnalyticsTrends(db *sql.DB, userID uuid.UUID, limit int) ([]*AnalyticsWeek, error) {
	rows, err := db.QueryContext(context.Background(), `
		SELECT id, user_id, week_start, total_working_minutes,
			meeting_minutes, focus_minutes, habit_minutes, buffer_minutes,
			personal_minutes, free_minutes, meeting_count, focus_block_count,
			habit_completion_rate, largest_focus_block_minutes, top_meeting_titles,
			focus_score, estimated_meeting_cost_minutes, computed_at
		FROM analytics_weeks WHERE user_id = $1
		ORDER BY week_start DESC LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AnalyticsWeek
	for rows.Next() {
		a, err := scanAnalyticsWeekRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func scanAnalyticsWeek(row *sql.Row) (*AnalyticsWeek, error) {
	var a AnalyticsWeek
	var weekStartStr string
	var titlesJSON []byte
	err := row.Scan(
		&a.ID, &a.UserID, &weekStartStr, &a.TotalWorkingMinutes,
		&a.MeetingMinutes, &a.FocusMinutes, &a.HabitMinutes, &a.BufferMinutes,
		&a.PersonalMinutes, &a.FreeMinutes, &a.MeetingCount, &a.FocusBlockCount,
		&a.HabitCompletionRate, &a.LargestFocusBlockMinutes, &titlesJSON,
		&a.FocusScore, &a.EstimatedMeetingCostMinutes, &a.ComputedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a.WeekStart, _ = time.Parse("2006-01-02", weekStartStr)
	_ = json.Unmarshal(titlesJSON, &a.TopMeetingTitles)
	return &a, nil
}

func scanAnalyticsWeekRow(rows *sql.Rows) (*AnalyticsWeek, error) {
	var a AnalyticsWeek
	var weekStartStr string
	var titlesJSON []byte
	if err := rows.Scan(
		&a.ID, &a.UserID, &weekStartStr, &a.TotalWorkingMinutes,
		&a.MeetingMinutes, &a.FocusMinutes, &a.HabitMinutes, &a.BufferMinutes,
		&a.PersonalMinutes, &a.FreeMinutes, &a.MeetingCount, &a.FocusBlockCount,
		&a.HabitCompletionRate, &a.LargestFocusBlockMinutes, &titlesJSON,
		&a.FocusScore, &a.EstimatedMeetingCostMinutes, &a.ComputedAt,
	); err != nil {
		return nil, err
	}
	a.WeekStart, _ = time.Parse("2006-01-02", weekStartStr)
	_ = json.Unmarshal(titlesJSON, &a.TopMeetingTitles)
	return &a, nil
}
