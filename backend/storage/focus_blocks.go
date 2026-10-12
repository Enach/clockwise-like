package storage

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// SaveFocusBlock records a focus block created on userID's calendar. The owner
// is stored so readers can scope blocks to the user they belong to (PAC-64).
func SaveFocusBlock(db *sql.DB, userID uuid.UUID, googleEventID, date string, start, end time.Time) error {
	_, err := db.Exec(`
		INSERT INTO focus_blocks (user_id, google_event_id, start_time, end_time, date, created_at)
		VALUES ($5, $1, $2, $3, $4, NOW())
		ON CONFLICT (google_event_id) DO NOTHING`,
		googleEventID,
		start.UTC(),
		end.UTC(),
		date,
		userID,
	)
	return err
}

// ListFocusBlocksForUserWeek returns userID's focus blocks for the 7 days from
// weekStart, never nil. Every reader goes through this: focus blocks belong to
// the user whose calendar holds them. Blocks saved before owners were recorded (user_id NULL) belong to
// nobody and are not returned.
func ListFocusBlocksForUserWeek(db *sql.DB, userID uuid.UUID, weekStart time.Time) ([]FocusBlock, error) {
	weekEnd := weekStart.AddDate(0, 0, 7)
	rows, err := db.Query(`
		SELECT id, google_event_id, start_time, end_time, date, created_at
		FROM focus_blocks WHERE user_id = $1 AND date >= $2 AND date < $3
		ORDER BY start_time`,
		userID,
		weekStart.Format("2006-01-02"),
		weekEnd.Format("2006-01-02"),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	blocks := make([]FocusBlock, 0)
	for rows.Next() {
		var b FocusBlock
		if err := rows.Scan(&b.ID, &b.GoogleEventID, &b.StartTime, &b.EndTime, &b.Date, &b.CreatedAt); err != nil {
			return nil, err
		}
		blocks = append(blocks, b)
	}
	return blocks, rows.Err()
}

// DeleteFocusBlock removes userID's focus block for googleEventID. Another
// user's block with that event ID is left alone.
func DeleteFocusBlock(db *sql.DB, userID uuid.UUID, googleEventID string) error {
	_, err := db.Exec(`DELETE FROM focus_blocks WHERE user_id = $1 AND google_event_id = $2`, userID, googleEventID)
	return err
}

// FocusMinutesForDay sums the minutes of userID's focus blocks on date.
func FocusMinutesForDay(db *sql.DB, userID uuid.UUID, date string) (int, error) {
	row := db.QueryRow(`
		SELECT COALESCE(SUM(
			EXTRACT(EPOCH FROM (end_time - start_time)) / 60
		)::INTEGER, 0) FROM focus_blocks WHERE user_id = $1 AND date = $2`, userID, date)
	var total int
	err := row.Scan(&total)
	return total, err
}

func WriteAuditLog(db *sql.DB, action, details string) {
	_, _ = db.Exec(`INSERT INTO audit_log (action, details, created_at) VALUES ($1, $2, NOW())`, action, details)
}
