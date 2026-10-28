package storage

import (
	"database/sql"

	"github.com/google/uuid"
)

// ClearZoomTokens disconnects userID's Zoom account locally and returns their
// conferencing to Meet. Other users are untouched (PAC-24). It is
// RemoveZoomCredential without the removed value (PAC-67).
func ClearZoomTokens(db *sql.DB, userID uuid.UUID) error {
	_, err := RemoveZoomCredential(db, userID)
	return err
}
