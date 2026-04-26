package storage

import (
	"database/sql"
	"time"
)

func SaveFocusBlock(db *sql.DB, googleEventID, date string, start, end time.Time) error {
	_, err := db.Exec(`
		INSERT OR IGNORE INTO focus_blocks (google_event_id, start_time, end_time, date, created_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		googleEventID,
		start.UTC().Format(time.RFC3339),
		end.UTC().Format(time.RFC3339),
		date,
	)
	return err
}

func ListFocusBlocksForWeek(db *sql.DB, weekStart time.Time) ([]FocusBlock, error) {
	weekEnd := weekStart.AddDate(0, 0, 7)
	rows, err := db.Query(`
		SELECT id, google_event_id, start_time, end_time, date, created_at
		FROM focus_blocks WHERE date >= ? AND date < ?
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
		var startStr, endStr, createdStr string
		if err := rows.Scan(&b.ID, &b.GoogleEventID, &startStr, &endStr, &b.Date, &createdStr); err != nil {
			return nil, err
		}
		b.StartTime, _ = time.Parse(time.RFC3339, startStr)
		b.EndTime, _ = time.Parse(time.RFC3339, endStr)
		b.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdStr)
		blocks = append(blocks, b)
	}
	return blocks, rows.Err()
}

func DeleteFocusBlock(db *sql.DB, googleEventID string) error {
	_, err := db.Exec(`DELETE FROM focus_blocks WHERE google_event_id = ?`, googleEventID)
	return err
}

func FocusMinutesForDay(db *sql.DB, date string) (int, error) {
	row := db.QueryRow(`
		SELECT COALESCE(SUM(
			CAST((julianday(end_time) - julianday(start_time)) * 1440 AS INTEGER)
		), 0) FROM focus_blocks WHERE date = ?`, date)
	var total int
	err := row.Scan(&total)
	return total, err
}

func WriteAuditLog(db *sql.DB, action, details string) {
	db.Exec(`INSERT INTO audit_log (action, details, created_at) VALUES (?, ?, CURRENT_TIMESTAMP)`, action, details)
}
