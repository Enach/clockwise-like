package storage

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/Enach/paceday/backend/internal/secretbox"
	"github.com/google/uuid"
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
	// SecretErr is the decryption failure of URL or CredentialsJSON, if any
	// (PAC-84 contract 6.2): the read never fails on it; reading the
	// calendar's events does.
	SecretErr error `json:"-"`
}

type PersonalBlocker struct {
	ID                 int64
	PersonalCalendarID int64
	PersonalEventID    string
	WorkEventID        string
	CreatedAt          time.Time
}

func ListPersonalCalendars(db DBTX) ([]PersonalCalendar, error) {
	return queryPersonalCalendars(db, `SELECT `+personalCalendarCols+` FROM personal_calendars ORDER BY id`)
}

func queryPersonalCalendars(db Querier, q string, args ...any) ([]PersonalCalendar, error) {
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cals []PersonalCalendar
	for rows.Next() {
		c, err := scanPersonalCalendar(rows)
		if err != nil {
			return nil, err
		}
		cals = append(cals, *c)
	}
	return cals, rows.Err()
}

func GetPersonalCalendar(db Querier, id int64) (*PersonalCalendar, error) {
	return scanPersonalCalendar(db.QueryRow(`SELECT `+personalCalendarCols+` FROM personal_calendars WHERE id = $1`, id))
}

// InsertPersonalCalendar stores c. The id is taken first, so the secrets are
// sealed for this row in the one insert (PAC-84 contract 3.3).
func InsertPersonalCalendar(db Querier, c *PersonalCalendar) (int64, error) {
	var id int64
	if err := db.QueryRow(`SELECT nextval(pg_get_serial_sequence('personal_calendars', 'id'))`).Scan(&id); err != nil {
		return 0, err
	}
	url, creds, err := sealPersonal(id, c.URL, c.CredentialsJSON)
	if err != nil {
		return 0, err
	}
	err = db.QueryRow(
		`INSERT INTO personal_calendars (id, user_id, provider, name, url, credentials_json, enabled) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		id, c.UserID, c.Provider, c.Name, url, creds, c.Enabled,
	).Scan(&id)
	return id, err
}

// UpdatePersonalCalendar changes the given fields of userID's calendar id.
// A url left out (nil) keeps the stored text byte-identical (PAC-84 K-10);
// a url given is sealed for this row.
func UpdatePersonalCalendar(db Querier, id int64, userID uuid.UUID, name, url *string, enabled *bool) (*PersonalCalendar, error) {
	var sealedURL *string
	if url != nil {
		s, err := secretbox.Seal(bindPersonal("url", id), *url)
		if err != nil {
			return nil, err
		}
		sealedURL = &s
	}
	row := db.QueryRow(`
		UPDATE personal_calendars SET
			name = COALESCE($3, name), url = COALESCE($4, url), enabled = COALESCE($5, enabled)
		WHERE id = $1 AND user_id = $2
		RETURNING `+personalCalendarCols,
		id, userID, name, sealedURL, enabled)
	c, err := scanPersonalCalendar(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return c, nil
}

func DeletePersonalCalendarForUser(db Querier, id int64, userID uuid.UUID) (bool, error) {
	result, err := db.Exec(`DELETE FROM personal_calendars WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

func MarkPersonalCalendarSynced(db Querier, id int64, userID uuid.UUID) error {
	_, err := db.Exec(`UPDATE personal_calendars SET last_synced_at = NOW() WHERE id = $1 AND user_id = $2`, id, userID)
	return err
}

func DeletePersonalCalendar(db DBTX, id int64) error {
	_, err := db.Exec(`DELETE FROM personal_calendars WHERE id = $1`, id)
	return err
}

func GetPersonalBlocker(db DBTX, calID int64, eventID string) (*PersonalBlocker, error) {
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

func UpsertPersonalBlocker(db Querier, calID int64, personalEventID, workEventID string) error {
	_, err := db.Exec(
		`INSERT INTO personal_blockers (personal_calendar_id, personal_event_id, work_event_id) VALUES ($1,$2,$3)
		 ON CONFLICT (personal_calendar_id, personal_event_id) DO UPDATE SET work_event_id = EXCLUDED.work_event_id`,
		calID, personalEventID, workEventID,
	)
	return err
}

func ListPersonalBlockers(db DBTX, calID int64) ([]PersonalBlocker, error) {
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

func DeletePersonalBlocker(db Querier, calID int64, personalEventID string) error {
	_, err := db.Exec(
		`DELETE FROM personal_blockers WHERE personal_calendar_id = $1 AND personal_event_id = $2`,
		calID, personalEventID,
	)
	return err
}

// SaveZoomTokens stores userID's Zoom credential on their own settings row
// and makes Zoom their conferencing provider (PAC-24). It stores no expiry;
// the Zoom connect callback uses SaveZoomCredential, which does (PAC-67).
func SaveZoomTokens(db DBTX, userID uuid.UUID, accessToken, refreshToken string) error {
	return SaveZoomCredential(db, userID, ZoomCredential{AccessToken: accessToken, RefreshToken: refreshToken})
}

// ListPersonalCalendarsByUser returns calendars owned by the given user.
func ListPersonalCalendarsByUser(db Querier, userID uuid.UUID) ([]PersonalCalendar, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("ListPersonalCalendarsByUser: userID is required")
	}
	return queryPersonalCalendars(db, `SELECT `+personalCalendarCols+` FROM personal_calendars WHERE user_id = $1 ORDER BY id`, userID)
}

// ListUsersWithPersonalCalendars returns the distinct user IDs that own at
// least one personal calendar. Used by the personal-blocker cron to fan out
// per user. Empty slice when nobody has connected a personal calendar.
func ListUsersWithPersonalCalendars(db DBTX) ([]uuid.UUID, error) {
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
