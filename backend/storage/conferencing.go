package storage

import (
	"database/sql"

	"github.com/google/uuid"
)

// ClearZoomTokens disconnects userID's Zoom account and returns their
// conferencing to Meet. Other users are untouched (PAC-24).
func ClearZoomTokens(db *sql.DB, userID uuid.UUID) error {
	_, err := db.Exec(`UPDATE settings SET zoom_tokens = '', conferencing_provider = 'meet' WHERE user_id = $1`, userID)
	return err
}
