package auth

import (
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// UpsertUserToken stores an OAuth token for the given user (one row per
// user_id+calendar_id, enforced by migration 017). Returns ErrNoUser when
// userID is uuid.Nil — production callers must always supply the
// authenticated user's UUID (see UserIDFromContext).
func UpsertUserToken(db *sql.DB, userID uuid.UUID, token *oauth2.Token) error {
	if userID == uuid.Nil {
		return ErrNoUser
	}
	_, err := db.Exec(`
		INSERT INTO oauth_tokens (user_id, access_token, refresh_token, expiry, calendar_id, updated_at)
		VALUES ($1, $2, $3, $4, 'primary', NOW())
		ON CONFLICT (user_id, calendar_id) DO UPDATE SET
			access_token  = EXCLUDED.access_token,
			refresh_token = EXCLUDED.refresh_token,
			expiry        = EXCLUDED.expiry,
			updated_at    = NOW()
	`, userID, token.AccessToken, token.RefreshToken, token.Expiry.UTC())
	return err
}

// ErrNoUser is returned when LoadUserToken is invoked with uuid.Nil.
// All authenticated callers must resolve userID via UserIDFromContext first;
// background callers (cron) must iterate users explicitly.
var ErrNoUser = errors.New("auth.LoadUserToken: userID is required (received uuid.Nil)")

// LoadUserToken retrieves the most recent OAuth token for a specific user.
// Returns ErrNoUser if userID is uuid.Nil — silent fallback to a legacy
// id=1 row was removed because it leaked tokens across tenants.
func LoadUserToken(db *sql.DB, userID uuid.UUID) (*oauth2.Token, error) {
	if userID == uuid.Nil {
		return nil, ErrNoUser
	}
	row := db.QueryRow(`
		SELECT access_token, refresh_token, expiry
		FROM oauth_tokens WHERE user_id = $1
		ORDER BY updated_at DESC LIMIT 1
	`, userID)
	return scanToken(row)
}

// DeleteUserToken removes the OAuth token for a specific user.
func DeleteUserToken(db *sql.DB, userID uuid.UUID) error {
	if userID == uuid.Nil {
		return ErrNoUser
	}
	_, err := db.Exec(`DELETE FROM oauth_tokens WHERE user_id = $1`, userID)
	return err
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


