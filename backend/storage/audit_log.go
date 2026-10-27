package storage

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// The limit GET /api/audit applies when none is asked for, and the most it
// returns (contracts/openapi/paths/calendar.yaml, listAuditEntries `limit`).
const (
	AuditDefaultLimit = 50
	AuditMaxLimit     = 500
)

type AuditEntry struct {
	ID        int64     `json:"id"`
	Action    string    `json:"action"`
	Details   string    `json:"details"`
	CreatedAt time.Time `json:"created_at"`
}

// ListAuditLog returns userID's own entries, newest first (PAC-23). Entries
// recorded before migration 026 have no subject and are returned to nobody. A
// limit of zero or less means the default; above the maximum, the maximum.
func ListAuditLog(db *sql.DB, userID uuid.UUID, limit int) ([]AuditEntry, error) {
	switch {
	case limit <= 0:
		limit = AuditDefaultLimit
	case limit > AuditMaxLimit:
		limit = AuditMaxLimit
	}
	rows, err := db.Query(`SELECT id, action, details, created_at FROM audit_log WHERE user_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]AuditEntry, 0)
	for rows.Next() {
		var entry AuditEntry
		if err := rows.Scan(&entry.ID, &entry.Action, &entry.Details, &entry.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}
