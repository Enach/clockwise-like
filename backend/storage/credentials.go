package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// The providers whose stored credential has a lifecycle (PAC-67). They are
// also the `provider` values of the connection activity entries.
const (
	CredentialGoogle  = "google"
	CredentialOutlook = "outlook"
	CredentialZoom    = "zoom"
)

func validCredentialProvider(p string) bool {
	return p == CredentialGoogle || p == CredentialOutlook || p == CredentialZoom
}

// RecordCredentialLoss marks userID's credential for provider as lost after the
// provider permanently refused to renew it (spec PAC-67 D-1: the credential
// itself is kept). started is true only for the refusal that starts a loss;
// every later refusal in the same loss, from any request or process, reports
// false, so a loss is announced once (AC-9, AC-25).
func RecordCredentialLoss(db *sql.DB, userID uuid.UUID, provider string) (started bool, err error) {
	if userID == uuid.Nil {
		return false, fmt.Errorf("RecordCredentialLoss: userID is required")
	}
	if !validCredentialProvider(provider) {
		return false, fmt.Errorf("RecordCredentialLoss: unknown provider %q", provider)
	}
	res, err := db.Exec(`INSERT INTO credential_losses (user_id, provider) VALUES ($1, $2) ON CONFLICT (user_id, provider) DO NOTHING`, userID, provider)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// ClearCredentialLoss ends userID's loss for provider: they reconnected or
// disconnected it. A later refusal starts a new loss.
func ClearCredentialLoss(db *sql.DB, userID uuid.UUID, provider string) error {
	_, err := db.Exec(`DELETE FROM credential_losses WHERE user_id = $1 AND provider = $2`, userID, provider)
	return err
}

// IsCredentialLost reports whether userID's credential for provider is
// recorded as lost.
func IsCredentialLost(db *sql.DB, userID uuid.UUID, provider string) (bool, error) {
	var lost bool
	err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM credential_losses WHERE user_id = $1 AND provider = $2)`, userID, provider).Scan(&lost)
	return lost, err
}

// CredentialLosses returns the providers whose credential userID has lost.
func CredentialLosses(db *sql.DB, userID uuid.UUID) (map[string]bool, error) {
	rows, err := db.Query(`SELECT provider FROM credential_losses WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out[p] = true
	}
	return out, rows.Err()
}

// ZoomCredential is the stored Zoom record (settings.zoom_tokens). Expiry is
// absent on records stored before PAC-67; such a credential is treated as
// expired when it is used, so it is renewed rather than used blind.
type ZoomCredential struct {
	AccessToken  string     `json:"access_token"`
	RefreshToken string     `json:"refresh_token"`
	Expiry       *time.Time `json:"expiry,omitempty"`
}

// DecodeZoomCredential parses a stored zoom_tokens value. The caller decides
// what a blank value means; an undecodable one is an error.
func DecodeZoomCredential(raw string) (*ZoomCredential, error) {
	var c ZoomCredential
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func encodeZoomCredential(c ZoomCredential) (string, error) {
	if c.Expiry != nil {
		e := c.Expiry.UTC()
		c.Expiry = &e
	}
	data, err := json.Marshal(c)
	return string(data), err
}

// LoadZoomCredential returns userID's stored Zoom credential, nil when none is
// stored, or an error when the stored value does not decode.
func LoadZoomCredential(db *sql.DB, userID uuid.UUID) (*ZoomCredential, error) {
	var raw sql.NullString
	err := db.QueryRow(`SELECT zoom_tokens FROM settings WHERE user_id = $1`, userID).Scan(&raw)
	if err == sql.ErrNoRows || (err == nil && strings.TrimSpace(raw.String) == "") {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return DecodeZoomCredential(raw.String)
}

// SaveZoomCredential stores userID's Zoom credential from a (re)connect, makes
// Zoom their conferencing provider and ends any recorded loss (PAC-67 AC-12).
// A connect that returned no refresh token keeps the stored one (AC-11).
func SaveZoomCredential(db *sql.DB, userID uuid.UUID, c ZoomCredential) error {
	if userID == uuid.Nil {
		return fmt.Errorf("SaveZoomCredential: userID is required")
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO settings (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, userID); err != nil {
		return err
	}
	if c.RefreshToken == "" {
		var raw string
		if err := tx.QueryRow(`SELECT COALESCE(zoom_tokens, '') FROM settings WHERE user_id = $1 FOR UPDATE`, userID).Scan(&raw); err != nil {
			return err
		}
		if old, err := DecodeZoomCredential(raw); err == nil {
			c.RefreshToken = old.RefreshToken
		}
	}
	data, err := encodeZoomCredential(c)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE settings SET zoom_tokens = $2, conferencing_provider = 'zoom' WHERE user_id = $1`, userID, data); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM credential_losses WHERE user_id = $1 AND provider = $2`, userID, CredentialZoom); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateZoomCredential writes a renewed credential back for its owner only,
// and only while a credential is still stored: a renewal that finishes after a
// disconnect never re-stores what the user removed.
func UpdateZoomCredential(db *sql.DB, userID uuid.UUID, c ZoomCredential) error {
	data, err := encodeZoomCredential(c)
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE settings SET zoom_tokens = $2 WHERE user_id = $1 AND COALESCE(zoom_tokens, '') <> ''`, userID, data)
	return err
}

// UpdateMicrosoftCredential writes a renewed Microsoft record back for its
// owner only, and only while a credential is still stored.
func UpdateMicrosoftCredential(db *sql.DB, userID uuid.UUID, record string) error {
	_, err := db.Exec(`UPDATE settings SET microsoft_tokens = $2 WHERE user_id = $1 AND COALESCE(microsoft_tokens, '') <> ''`, userID, record)
	return err
}

// RemoveZoomCredential is step 1 of a Zoom disconnect (PAC-67): in one
// transaction it reads the stored value, removes it whether or not it decodes,
// returns conferencing to Meet (D-9) and ends any recorded loss. It returns
// the removed value ("" when nothing was stored). On error nothing changed.
func RemoveZoomCredential(db *sql.DB, userID uuid.UUID) (string, error) {
	return removeCredential(db, userID, "zoom_tokens",
		`UPDATE settings SET zoom_tokens = '', conferencing_provider = 'meet' WHERE user_id = $1`, CredentialZoom)
}

// RemoveMicrosoftCredential is steps 1 and 2 of an Outlook disconnect (PAC-67),
// in one transaction: the stored Microsoft credential is removed whether or
// not it decodes, conferencing `teams` becomes `meet` (D-8), any recorded loss
// ends, and the calendar provider is left alone (D-3). It returns the removed
// value ("" when nothing was stored). On error nothing changed.
func RemoveMicrosoftCredential(db *sql.DB, userID uuid.UUID) (string, error) {
	return removeCredential(db, userID, "microsoft_tokens",
		`UPDATE settings SET microsoft_tokens = '',
			conferencing_provider = CASE WHEN conferencing_provider = 'teams' THEN 'meet' ELSE conferencing_provider END
		 WHERE user_id = $1`, CredentialOutlook)
}

func removeCredential(db *sql.DB, userID uuid.UUID, column, update, provider string) (string, error) {
	if userID == uuid.Nil {
		return "", fmt.Errorf("remove %s: userID is required", provider)
	}
	tx, err := db.Begin()
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	var raw sql.NullString
	err = tx.QueryRow(`SELECT `+column+` FROM settings WHERE user_id = $1 FOR UPDATE`, userID).Scan(&raw)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	if err == nil {
		if _, err := tx.Exec(update, userID); err != nil {
			return "", err
		}
	}
	if _, err := tx.Exec(`DELETE FROM credential_losses WHERE user_id = $1 AND provider = $2`, userID, provider); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	if strings.TrimSpace(raw.String) == "" {
		return "", nil
	}
	return raw.String, nil
}
