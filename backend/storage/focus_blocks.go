package storage

import (
	"log"
	"time"

	"github.com/google/uuid"
)

// SaveFocusBlock records a focus block created on userID's calendar. The owner
// is stored so readers can scope blocks to the user they belong to (PAC-64).
func SaveFocusBlock(db DBTX, userID uuid.UUID, googleEventID, date string, start, end time.Time) error {
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
func ListFocusBlocksForUserWeek(db DBTX, userID uuid.UUID, weekStart time.Time) ([]FocusBlock, error) {
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
func DeleteFocusBlock(db DBTX, userID uuid.UUID, googleEventID string) error {
	_, err := db.Exec(`DELETE FROM focus_blocks WHERE user_id = $1 AND google_event_id = $2`, userID, googleEventID)
	return err
}

// FocusMinutesForDay sums the minutes of userID's focus blocks on date.
func FocusMinutesForDay(db DBTX, userID uuid.UUID, date string) (int, error) {
	row := db.QueryRow(`
		SELECT COALESCE(SUM(
			EXTRACT(EPOCH FROM (end_time - start_time)) / 60
		)::INTEGER, 0) FROM focus_blocks WHERE user_id = $1 AND date = $2`, userID, date)
	var total int
	err := row.Scan(&total)
	return total, err
}

// WriteAuditLog records an activity entry about userID's calendar (PAC-23):
// userID is the subject, whose log the entry appears in. Recording is best
// effort and never fails the caller, but it is never silent either: an entry
// with no subject is refused before it reaches the database, and a failed
// insert is written to the server log.
func WriteAuditLog(db DBTX, userID uuid.UUID, action, details string) {
	if userID == uuid.Nil {
		log.Printf("audit: %s not recorded: no subject", action)
		return
	}
	if _, err := db.Exec(`INSERT INTO audit_log (user_id, action, details, created_at) VALUES ($1, $2, $3, NOW())`, userID, action, details); err != nil {
		log.Printf("audit: %s for user %s not recorded: %v", action, userID, err)
	}
}
