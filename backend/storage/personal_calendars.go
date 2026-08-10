package storage

import (
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"time"
)

type PersonalCalendar struct {
	ID              int64      `json:"id"`
	UserID          uuid.UUID  `json:"-"`
	Provider        string     `json:"provider"`
	Name            string     `json:"name"`
	URL             string     `json:"url,omitempty"`
	CredentialsJSON string     `json:"-"`
	Enabled         bool       `json:"enabled"`
	LastSyncedAt    *time.Time `json:"-"`
	CreatedAt       time.Time  `json:"createdAt"`
}

type PersonalBlocker struct {
	ID                 int64
	PersonalCalendarID int64
	PersonalEventID    string
	WorkEventID        string
	CreatedAt          time.Time
}

func ListPersonalCalendars(db *sql.DB) ([]PersonalCalendar, error) {
	rows, err := db.Query(`SELECT id, user_id, provider, name, url, credentials_json, enabled, last_synced_at, created_at FROM personal_calendars ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cals []PersonalCalendar
	for rows.Next() {
		var c PersonalCalendar
		if err := rows.Scan(&c.ID, &c.UserID, &c.Provider, &c.Name, &c.URL, &c.CredentialsJSON, &c.Enabled, &c.LastSyncedAt, &c.CreatedAt); err != nil {
			return nil, err
		}
		cals = append(cals, c)
	}
	return cals, rows.Err()
}

func GetPersonalCalendar(db *sql.DB, id int64) (*PersonalCalendar, error) {
	row := db.QueryRow(`SELECT id, user_id, provider, name, url, credentials_json, enabled, last_synced_at, created_at FROM personal_calendars WHERE id = $1`, id)
	var c PersonalCalendar
	if err := row.Scan(&c.ID, &c.UserID, &c.Provider, &c.Name, &c.URL, &c.CredentialsJSON, &c.Enabled, &c.LastSyncedAt, &c.CreatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

func InsertPersonalCalendar(db *sql.DB, c *PersonalCalendar) (int64, error) {
	var id int64
	err := db.QueryRow(
		`INSERT INTO personal_calendars (user_id, provider, name, url, credentials_json, enabled) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		c.UserID, c.Provider, c.Name, c.URL, c.CredentialsJSON, c.Enabled,
	).Scan(&id)
	return id, err
}

func UpdatePersonalCalendar(db *sql.DB, id int64, userID uuid.UUID, name, url *string, enabled *bool) (*PersonalCalendar, error) {
	row := db.QueryRow(`
		UPDATE personal_calendars SET
			name = COALESCE($3, name), url = COALESCE($4, url), enabled = COALESCE($5, enabled)
		WHERE id = $1 AND user_id = $2
		RETURNING id, user_id, provider, name, url, credentials_json, enabled, last_synced_at, created_at`,
		id, userID, name, url, enabled)
	var c PersonalCalendar
	if err := row.Scan(&c.ID, &c.UserID, &c.Provider, &c.Name, &c.URL, &c.CredentialsJSON, &c.Enabled, &c.LastSyncedAt, &c.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

func DeletePersonalCalendarForUser(db *sql.DB, id int64, userID uuid.UUID) (bool, error) {
	result, err := db.Exec(`DELETE FROM personal_calendars WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

func MarkPersonalCalendarSynced(db *sql.DB, id int64, userID uuid.UUID) error {
	_, err := db.Exec(`UPDATE personal_calendars SET last_synced_at = NOW() WHERE id = $1 AND user_id = $2`, id, userID)
	return err
}

func DeletePersonalCalendar(db *sql.DB, id int64) error {
	_, err := db.Exec(`DELETE FROM personal_calendars WHERE id = $1`, id)
	return err
}

func GetPersonalBlocker(db *sql.DB, calID int64, eventID string) (*PersonalBlocker, error) {
	row := db.QueryRow(
		`SELECT id, personal_calendar_id, personal_event_id, work_event_id, created_at FROM personal_blockers WHERE personal_calendar_id = $1 AND personal_event_id = $2`,
		calID, eventID,
	)
	var b PersonalBlocker
	if err := row.Scan(&b.ID, &b.PersonalCalendarID, &b.PersonalEventID, &b.WorkEventID, &b.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &b, nil
}

func UpsertPersonalBlocker(db *sql.DB, calID int64, personalEventID, workEventID string) error {
	_, err := db.Exec(
		`INSERT INTO personal_blockers (personal_calendar_id, personal_event_id, work_event_id) VALUES ($1,$2,$3)
		 ON CONFLICT (personal_calendar_id, personal_event_id) DO UPDATE SET work_event_id = EXCLUDED.work_event_id`,
		calID, personalEventID, workEventID,
	)
	return err
}

func ListPersonalBlockers(db *sql.DB, calID int64) ([]PersonalBlocker, error) {
	rows, err := db.Query(
		`SELECT id, personal_calendar_id, personal_event_id, work_event_id, created_at FROM personal_blockers WHERE personal_calendar_id = $1`,
		calID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var blockers []PersonalBlocker
	for rows.Next() {
		var b PersonalBlocker
		if err := rows.Scan(&b.ID, &b.PersonalCalendarID, &b.PersonalEventID, &b.WorkEventID, &b.CreatedAt); err != nil {
			return nil, err
		}
		blockers = append(blockers, b)
	}
	return blockers, rows.Err()
}

func DeletePersonalBlocker(db *sql.DB, calID int64, personalEventID string) error {
	_, err := db.Exec(
		`DELETE FROM personal_blockers WHERE personal_calendar_id = $1 AND personal_event_id = $2`,
		calID, personalEventID,
	)
	return err
}

func SaveZoomTokens(db *sql.DB, accessToken, refreshToken string) error {
	_, err := db.Exec(
		`UPDATE settings SET zoom_tokens = $1, conferencing_provider = 'zoom' WHERE id = 1`,
		`{"access_token":"`+accessToken+`","refresh_token":"`+refreshToken+`"}`,
	)
	return err
}

// ListPersonalCalendarsByUser returns calendars owned by the given user.
func ListPersonalCalendarsByUser(db *sql.DB, userID uuid.UUID) ([]PersonalCalendar, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("ListPersonalCalendarsByUser: userID is required")
	}
	rows, err := db.Query(`SELECT id, user_id, provider, name, url, credentials_json, enabled, last_synced_at, created_at
		FROM personal_calendars WHERE user_id = $1 ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cals []PersonalCalendar
	for rows.Next() {
		var c PersonalCalendar
		if err := rows.Scan(&c.ID, &c.UserID, &c.Provider, &c.Name, &c.URL, &c.CredentialsJSON, &c.Enabled, &c.LastSyncedAt, &c.CreatedAt); err != nil {
			return nil, err
		}
		cals = append(cals, c)
	}
	return cals, rows.Err()
}

// ListUsersWithPersonalCalendars returns the distinct user IDs that own at
// least one personal calendar. Used by the personal-blocker cron to fan out
// per user. Empty slice when nobody has connected a personal calendar.
func ListUsersWithPersonalCalendars(db *sql.DB) ([]uuid.UUID, error) {
	rows, err := db.Query(`SELECT DISTINCT user_id FROM personal_calendars WHERE enabled = TRUE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
