package storage

import (
	"database/sql"
	"time"
)

func SaveFocusBlock(db *sql.DB, googleEventID, date string, start, end time.Time) error {
	_, err := db.Exec(`
		INSERT INTO focus_blocks (google_event_id, start_time, end_time, date, created_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (google_event_id) DO NOTHING`,
		googleEventID,
		start.UTC(),
		end.UTC(),
		date,
	)
	return err
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
