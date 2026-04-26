package auth

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// UpsertUserToken stores an OAuth token keyed by user UUID.
func UpsertUserToken(db *sql.DB, userID uuid.UUID, token *oauth2.Token) error {
	_, err := db.Exec(`
		INSERT INTO oauth_tokens (id, user_id, access_token, refresh_token, expiry, calendar_id, updated_at)
		VALUES (1, $1, $2, $3, $4, 'primary', NOW())
		ON CONFLICT (id) DO UPDATE SET
			user_id       = EXCLUDED.user_id,
			access_token  = EXCLUDED.access_token,
			refresh_token = EXCLUDED.refresh_token,
			expiry        = EXCLUDED.expiry,
			updated_at    = NOW()
	`, userID, token.AccessToken, token.RefreshToken, token.Expiry.UTC())
	return err
}

// LoadUserToken retrieves the OAuth token for a specific user.
// Falls back to the legacy id=1 row when userID is uuid.Nil.
func LoadUserToken(db *sql.DB, userID uuid.UUID) (*oauth2.Token, error) {
	var row *sql.Row
	if userID == uuid.Nil {
		row = db.QueryRow(`SELECT access_token, refresh_token, expiry FROM oauth_tokens WHERE id = 1`)
	} else {
		row = db.QueryRow(`
			SELECT access_token, refresh_token, expiry
			FROM oauth_tokens WHERE user_id = $1
			ORDER BY updated_at DESC LIMIT 1
		`, userID)
	}
	return scanToken(row)
}

func scanToken(row *sql.Row) (*oauth2.Token, error) {
	var accessToken, refreshToken string
	var expiry time.Time
	if err := row.Scan(&accessToken, &refreshToken, &expiry); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &oauth2.Token{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		Expiry:       expiry,
	}, nil
}

// ── Legacy single-user aliases (id = 1) ──────────────────────────────────────

func UpsertToken(db *sql.DB, token *oauth2.Token) error {
	return UpsertUserToken(db, uuid.Nil, token)
}

func SaveToken(db *sql.DB, token *oauth2.Token) error {
	return UpsertToken(db, token)
}

func LoadToken(db *sql.DB) (*oauth2.Token, error) {
	return LoadUserToken(db, uuid.Nil)
}

func TokenFromDB(db *sql.DB) (*oauth2.Token, error) {
	return LoadToken(db)
}

func DeleteToken(db *sql.DB) error {
	_, err := db.Exec(`DELETE FROM oauth_tokens WHERE id = 1`)
	return err
}

func IsConnected(db *sql.DB) bool {
	token, err := LoadToken(db)
	return err == nil && token != nil && token.RefreshToken != ""
}
