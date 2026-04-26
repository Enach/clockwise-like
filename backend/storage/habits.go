package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Habit is a recurring time-defense commitment.
type Habit struct {
	ID              uuid.UUID `json:"id"`
	UserID          uuid.UUID `json:"user_id"`
	Title           string    `json:"title"`
	DurationMinutes int       `json:"duration_minutes"`
	DaysOfWeek      []int     `json:"days_of_week"`
	WindowStart     string    `json:"window_start"`
	WindowEnd       string    `json:"window_end"`
	Priority        int       `json:"priority"`
	Color           string    `json:"color"`
	Active          bool      `json:"active"`
	CreatedAt       time.Time `json:"created_at"`
}

// HabitOccurrence is one scheduled instance of a habit on a specific date.
type HabitOccurrence struct {
	ID              uuid.UUID `json:"id"`
	HabitID         uuid.UUID `json:"habit_id"`
	ScheduledDate   time.Time `json:"scheduled_date"`
	StartTime       time.Time `json:"start_time"`
	EndTime         time.Time `json:"end_time"`
	Status          string    `json:"status"`
	CalendarEventID string    `json:"calendar_event_id"`
	CreatedAt       time.Time `json:"created_at"`
}

// --- Habit CRUD ------------------------------------------------------------

func CreateHabit(db *sql.DB, h *Habit) (*Habit, error) {
	row := db.QueryRowContext(context.Background(), `
		INSERT INTO habits (user_id, title, duration_minutes, days_of_week, window_start, window_end, priority, color)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id, user_id, title, duration_minutes, days_of_week, window_start, window_end, priority, color, active, created_at
	`, h.UserID, h.Title, h.DurationMinutes,
		pq.Array(h.DaysOfWeek), h.WindowStart, h.WindowEnd, h.Priority, h.Color)
	return scanHabit(row)
}

func GetHabitByID(db *sql.DB, id uuid.UUID) (*Habit, error) {
	row := db.QueryRowContext(context.Background(), `
		SELECT id, user_id, title, duration_minutes, days_of_week, window_start, window_end, priority, color, active, created_at
		FROM habits WHERE id = $1
	`, id)
	return scanHabit(row)
}

func ListHabitsByUser(db *sql.DB, userID uuid.UUID) ([]*Habit, error) {
	rows, err := db.QueryContext(context.Background(), `
		SELECT id, user_id, title, duration_minutes, days_of_week, window_start, window_end, priority, color, active, created_at
		FROM habits WHERE user_id = $1 ORDER BY priority DESC, created_at ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHabits(rows)
}

func ListActiveHabitsByUser(db *sql.DB, userID uuid.UUID) ([]*Habit, error) {
	rows, err := db.QueryContext(context.Background(), `
		SELECT id, user_id, title, duration_minutes, days_of_week, window_start, window_end, priority, color, active, created_at
		FROM habits WHERE user_id = $1 AND active = true ORDER BY priority DESC, created_at ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHabits(rows)
}

func UpdateHabit(db *sql.DB, id uuid.UUID, h *Habit) (*Habit, error) {
	row := db.QueryRowContext(context.Background(), `
		UPDATE habits SET
			title = $2, duration_minutes = $3, days_of_week = $4,
			window_start = $5, window_end = $6, priority = $7, color = $8, active = $9
		WHERE id = $1
		RETURNING id, user_id, title, duration_minutes, days_of_week, window_start, window_end, priority, color, active, created_at
	`, id, h.Title, h.DurationMinutes,
		pq.Array(h.DaysOfWeek), h.WindowStart, h.WindowEnd, h.Priority, h.Color, h.Active)
	return scanHabit(row)
}

func DeactivateHabit(db *sql.DB, id uuid.UUID) error {
	_, err := db.ExecContext(context.Background(),
		`UPDATE habits SET active = false WHERE id = $1`, id)
	return err
}

func scanHabit(row *sql.Row) (*Habit, error) {
	var h Habit
	var daysOfWeek pq.Int64Array
	err := row.Scan(
		&h.ID, &h.UserID, &h.Title, &h.DurationMinutes,
		&daysOfWeek, &h.WindowStart, &h.WindowEnd,
		&h.Priority, &h.Color, &h.Active, &h.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	h.DaysOfWeek = int64SliceToInt(daysOfWeek)
	return &h, nil
}

func scanHabits(rows *sql.Rows) ([]*Habit, error) {
	var out []*Habit
	for rows.Next() {
		var h Habit
		var daysOfWeek pq.Int64Array
		if err := rows.Scan(
			&h.ID, &h.UserID, &h.Title, &h.DurationMinutes,
			&daysOfWeek, &h.WindowStart, &h.WindowEnd,
			&h.Priority, &h.Color, &h.Active, &h.CreatedAt,
		); err != nil {
			return nil, err
		}
		h.DaysOfWeek = int64SliceToInt(daysOfWeek)
		out = append(out, &h)
	}
	return out, rows.Err()
}

func int64SliceToInt(s pq.Int64Array) []int {
	out := make([]int, len(s))
	for i, v := range s {
		out[i] = int(v)
	}
	return out
}

// --- HabitOccurrence CRUD -------------------------------------------------

// UpsertHabitOccurrence creates or replaces the occurrence for a (habit, date) pair.
func UpsertHabitOccurrence(db *sql.DB, o *HabitOccurrence) (*HabitOccurrence, error) {
	row := db.QueryRowContext(context.Background(), `
		INSERT INTO habit_occurrences (habit_id, scheduled_date, start_time, end_time, status, calendar_event_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (habit_id, scheduled_date) DO UPDATE SET
			start_time        = EXCLUDED.start_time,
			end_time          = EXCLUDED.end_time,
			status            = EXCLUDED.status,
			calendar_event_id = EXCLUDED.calendar_event_id
		RETURNING id, habit_id, scheduled_date, start_time, end_time, status, calendar_event_id, created_at
	`, o.HabitID, o.ScheduledDate.Format("2006-01-02"),
		o.StartTime, o.EndTime, o.Status, o.CalendarEventID)
	return scanOccurrence(row)
}

func ListHabitOccurrences(db *sql.DB, habitID uuid.UUID, from, to time.Time) ([]*HabitOccurrence, error) {
	rows, err := db.QueryContext(context.Background(), `
		SELECT id, habit_id, scheduled_date, start_time, end_time, status, calendar_event_id, created_at
		FROM habit_occurrences
		WHERE habit_id = $1 AND scheduled_date >= $2 AND scheduled_date <= $3
		ORDER BY scheduled_date ASC
	`, habitID, from.Format("2006-01-02"), to.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*HabitOccurrence
	for rows.Next() {
		o, err := scanOccurrenceRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ListScheduledOccurrencesForDay returns all scheduled habit_occurrences on a date.
// Used to build busy intervals when scheduling other habits.
func ListScheduledOccurrencesForDay(db *sql.DB, userID uuid.UUID, date time.Time) ([]*HabitOccurrence, error) {
	rows, err := db.QueryContext(context.Background(), `
		SELECT ho.id, ho.habit_id, ho.scheduled_date, ho.start_time, ho.end_time,
		       ho.status, ho.calendar_event_id, ho.created_at
		FROM habit_occurrences ho
		JOIN habits h ON h.id = ho.habit_id
		WHERE h.user_id = $1
		  AND ho.scheduled_date = $2
		  AND ho.status = 'scheduled'
	`, userID, date.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*HabitOccurrence
	for rows.Next() {
		o, err := scanOccurrenceRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func scanOccurrence(row *sql.Row) (*HabitOccurrence, error) {
	var o HabitOccurrence
	var dateStr string
	err := row.Scan(&o.ID, &o.HabitID, &dateStr, &o.StartTime, &o.EndTime,
		&o.Status, &o.CalendarEventID, &o.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	o.ScheduledDate, _ = time.Parse("2006-01-02", dateStr)
	return &o, nil
}

func scanOccurrenceRow(rows *sql.Rows) (*HabitOccurrence, error) {
	var o HabitOccurrence
	var dateStr string
	if err := rows.Scan(&o.ID, &o.HabitID, &dateStr, &o.StartTime, &o.EndTime,
		&o.Status, &o.CalendarEventID, &o.CreatedAt); err != nil {
		return nil, err
	}
	o.ScheduledDate, _ = time.Parse("2006-01-02", dateStr)
	return &o, nil
}
