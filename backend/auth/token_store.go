package auth

import (
	"database/sql"
	"time"

	"golang.org/x/oauth2"
)

func UpsertToken(db *sql.DB, token *oauth2.Token) error {
	_, err := db.Exec(`
		INSERT INTO oauth_tokens (id, access_token, refresh_token, expiry, calendar_id, updated_at)
		VALUES (1, ?, ?, ?, 'primary', CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			access_token = excluded.access_token,
			refresh_token = excluded.refresh_token,
			expiry = excluded.expiry,
			updated_at = excluded.updated_at
	`, token.AccessToken, token.RefreshToken, token.Expiry.UTC().Format(time.RFC3339))
	return err
}

func LoadToken(db *sql.DB) (*oauth2.Token, error) {
	row := db.QueryRow(`SELECT access_token, refresh_token, expiry FROM oauth_tokens WHERE id = 1`)
	var accessToken, refreshToken, expiryStr string
	if err := row.Scan(&accessToken, &refreshToken, &expiryStr); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	expiry, err := time.Parse(time.RFC3339, expiryStr)
	if err != nil {
		expiry = time.Time{}
	}
	return &oauth2.Token{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		Expiry:       expiry,
	}, nil
}

func DeleteToken(db *sql.DB) error {
	_, err := db.Exec(`DELETE FROM oauth_tokens WHERE id = 1`)
	return err
}

func IsConnected(db *sql.DB) bool {
	token, err := LoadToken(db)
	return err == nil && token != nil && token.RefreshToken != ""
}
