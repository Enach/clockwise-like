package auth

import (
	"database/sql"
	"time"

	"golang.org/x/oauth2"
)

func UpsertToken(db *sql.DB, token *oauth2.Token) error {
	_, err := db.Exec(`
		INSERT INTO oauth_tokens (id, access_token, refresh_token, expiry, calendar_id, updated_at)
		VALUES (1, $1, $2, $3, 'primary', NOW())
		ON CONFLICT (id) DO UPDATE SET
			access_token = EXCLUDED.access_token,
			refresh_token = EXCLUDED.refresh_token,
			expiry = EXCLUDED.expiry,
			updated_at = NOW()
	`, token.AccessToken, token.RefreshToken, token.Expiry.UTC())
	return err
}

func LoadToken(db *sql.DB) (*oauth2.Token, error) {
	row := db.QueryRow(`SELECT access_token, refresh_token, expiry FROM oauth_tokens WHERE id = 1`)
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

func DeleteToken(db *sql.DB) error {
	_, err := db.Exec(`DELETE FROM oauth_tokens WHERE id = 1`)
	return err
}

func IsConnected(db *sql.DB) bool {
	token, err := LoadToken(db)
	return err == nil && token != nil && token.RefreshToken != ""
}

