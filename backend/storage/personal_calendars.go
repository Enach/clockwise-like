package storage

import (
	"database/sql"
	"time"
)

type PersonalCalendar struct {
	ID              int64     `json:"id"`
	Provider        string    `json:"provider"`
	Name            string    `json:"name"`
	URL             string    `json:"url,omitempty"`
	CredentialsJSON string    `json:"-"`
	Enabled         bool      `json:"enabled"`
	CreatedAt       time.Time `json:"createdAt"`
}

type PersonalBlocker struct {
	ID                 int64
	PersonalCalendarID int64
	PersonalEventID    string
	WorkEventID        string
	CreatedAt          time.Time
}

func ListPersonalCalendars(db *sql.DB) ([]PersonalCalendar, error) {
	rows, err := db.Query(`SELECT id, provider, name, url, credentials_json, enabled, created_at FROM personal_calendars ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cals []PersonalCalendar
	for rows.Next() {
		var c PersonalCalendar
		if err := rows.Scan(&c.ID, &c.Provider, &c.Name, &c.URL, &c.CredentialsJSON, &c.Enabled, &c.CreatedAt); err != nil {
			return nil, err
		}
		cals = append(cals, c)
	}
	return cals, rows.Err()
}

func GetPersonalCalendar(db *sql.DB, id int64) (*PersonalCalendar, error) {
	row := db.QueryRow(`SELECT id, provider, name, url, credentials_json, enabled, created_at FROM personal_calendars WHERE id = $1`, id)
	var c PersonalCalendar
	if err := row.Scan(&c.ID, &c.Provider, &c.Name, &c.URL, &c.CredentialsJSON, &c.Enabled, &c.CreatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

func InsertPersonalCalendar(db *sql.DB, c *PersonalCalendar) (int64, error) {
	var id int64
	err := db.QueryRow(
		`INSERT INTO personal_calendars (provider, name, url, credentials_json, enabled) VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		c.Provider, c.Name, c.URL, c.CredentialsJSON, c.Enabled,
	).Scan(&id)
	return id, err
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
