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
// weekStart. Blocks saved before owners were recorded (user_id NULL) belong to
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

	var blocks []FocusBlock
	for rows.Next() {
		var b FocusBlock
		if err := rows.Scan(&b.ID, &b.GoogleEventID, &b.StartTime, &b.EndTime, &b.Date, &b.CreatedAt); err != nil {
			return nil, err
		}
		blocks = append(blocks, b)
	}
	return blocks, rows.Err()
}

func ListFocusBlocksForWeek(db *sql.DB, weekStart time.Time) ([]FocusBlock, error) {
	weekEnd := weekStart.AddDate(0, 0, 7)
	rows, err := db.Query(`
		SELECT id, google_event_id, start_time, end_time, date, created_at
		FROM focus_blocks WHERE date >= $1 AND date < $2
		ORDER BY start_time`,
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

func DeleteFocusBlock(db *sql.DB, googleEventID string) error {
	_, err := db.Exec(`DELETE FROM focus_blocks WHERE google_event_id = $1`, googleEventID)
	return err
}

func FocusMinutesForDay(db *sql.DB, date string) (int, error) {
	row := db.QueryRow(`
		SELECT COALESCE(SUM(
			EXTRACT(EPOCH FROM (end_time - start_time)) / 60
		)::INTEGER, 0) FROM focus_blocks WHERE date = $1`, date)
	var total int
	err := row.Scan(&total)
	return total, err
}

func WriteAuditLog(db *sql.DB, action, details string) {
	_, _ = db.Exec(`INSERT INTO audit_log (action, details, created_at) VALUES ($1, $2, NOW())`, action, details)
}
