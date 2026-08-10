package storage

import "database/sql"

func ClearZoomTokens(db *sql.DB) error {
	_, err := db.Exec(`UPDATE settings SET zoom_tokens = '', conferencing_provider = 'meet' WHERE id = 1`)
	return err
}
