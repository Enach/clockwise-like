package auth

import (
	"database/sql"
	"errors"
	"time"

	"github.com/Enach/paceday/backend/internal/secretbox"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// UpsertUserToken stores an OAuth token for the given user (one row per
// user_id+calendar_id, enforced by migration 017). Returns ErrNoUser when
// userID is uuid.Nil — production callers must always supply the
// authenticated user's UUID (see UserIDFromContext).
//
// It is the Google (re)connect (PAC-67): a token without a refresh token keeps
// the stored one, so a reconnect never erases a working credential, and
// storing ends any recorded loss of the Google connection (AC-12, AC-24).
func UpsertUserToken(db storage.DBTX, userID uuid.UUID, token *oauth2.Token) error {
	if userID == uuid.Nil {
		return ErrNoUser
	}
	// PAC-84: both tokens are sealed for (user_id, 'primary'), the row the
	// upsert writes (contract 3.3). An empty refresh token stays "" and so
	// keeps the stored one (AC-7).
	access, err := secretbox.Seal(storage.BindOAuthToken("access_token", userID, primaryCalendar), token.AccessToken)
	if err != nil {
		return err
	}
	refresh, err := secretbox.Seal(storage.BindOAuthToken("refresh_token", userID, primaryCalendar), token.RefreshToken)
	if err != nil {
		return err
	}
	if _, err := db.Exec(`
		INSERT INTO oauth_tokens (user_id, access_token, refresh_token, expiry, calendar_id, updated_at)
		VALUES ($1, $2, $3, $4, 'primary', NOW())
		ON CONFLICT (user_id, calendar_id) DO UPDATE SET
			access_token  = EXCLUDED.access_token,
			refresh_token = COALESCE(NULLIF(EXCLUDED.refresh_token, ''), oauth_tokens.refresh_token),
			expiry        = EXCLUDED.expiry,
			updated_at    = NOW()
	`, userID, access, refresh, token.Expiry.UTC()); err != nil {
		return err
	}
	return storage.ClearCredentialLoss(db, userID, storage.CredentialGoogle)
}

// primaryCalendar is the only calendar_id UpsertUserToken writes.
const primaryCalendar = "primary"

// UpdateUserToken writes a renewed Google token back for its owner only, and
// only while the owner still has a stored row: a renewal finishing after a
// disconnect never re-stores what the user removed (PAC-67). Every row of the
// owner gets the token, as before; each is sealed for its own calendar_id
// (PAC-84 contract 3.3).
func UpdateUserToken(db storage.DBTX, userID uuid.UUID, token *oauth2.Token) error {
	if userID == uuid.Nil {
		return ErrNoUser
	}
	rows, err := db.Query(`SELECT calendar_id FROM oauth_tokens WHERE user_id = $1`, userID)
	if err != nil {
		return err
	}
	var calendars []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			rows.Close()
			return err
		}
		calendars = append(calendars, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, cal := range calendars {
		access, err := secretbox.Seal(storage.BindOAuthToken("access_token", userID, cal), token.AccessToken)
		if err != nil {
			return err
		}
		refresh, err := secretbox.Seal(storage.BindOAuthToken("refresh_token", userID, cal), token.RefreshToken)
		if err != nil {
			return err
		}
		if _, err := db.Exec(`
			UPDATE oauth_tokens SET access_token = $3, refresh_token = $4, expiry = $5, updated_at = NOW()
			WHERE user_id = $1 AND calendar_id = $2
		`, userID, cal, access, refresh, token.Expiry.UTC()); err != nil {
			return err
		}
	}
	return nil
}

// ErrNoUser is returned when LoadUserToken is invoked with uuid.Nil.
// All authenticated callers must resolve userID via UserIDFromContext first;
// background callers (cron) must iterate users explicitly.
var ErrNoUser = errors.New("auth.LoadUserToken: userID is required (received uuid.Nil)")

// LoadUserToken retrieves the most recent OAuth token for a specific user.
// Returns ErrNoUser if userID is uuid.Nil — silent fallback to a legacy
// id=1 row was removed because it leaked tokens across tenants.
//
// PAC-84: a token that cannot be decrypted is an error with errors.Is
// secretbox.ErrDecrypt (logged with contract 9.2's line); status callers map
// it to "not connected" as any error, act callers to a 500.
func LoadUserToken(db storage.DBTX, userID uuid.UUID) (*oauth2.Token, error) {
	if userID == uuid.Nil {
		return nil, ErrNoUser
	}
	row := db.QueryRow(`
		SELECT calendar_id, access_token, refresh_token, expiry
		FROM oauth_tokens WHERE user_id = $1
		ORDER BY updated_at DESC LIMIT 1
	`, userID)
	return scanToken(row, userID)
}

// DeleteUserToken removes the OAuth token for a specific user, and with it
// any recorded loss of it (PAC-67: a disconnect ends a loss).
func DeleteUserToken(db storage.DBTX, userID uuid.UUID) error {
	if userID == uuid.Nil {
		return ErrNoUser
	}
	if _, err := db.Exec(`DELETE FROM oauth_tokens WHERE user_id = $1`, userID); err != nil {
		return err
	}
	return storage.ClearCredentialLoss(db, userID, storage.CredentialGoogle)
}

func scanToken(row *sql.Row, userID uuid.UUID) (*oauth2.Token, error) {
	var calendarID, accessToken, refreshToken string
	var expiry time.Time
	if err := row.Scan(&calendarID, &accessToken, &refreshToken, &expiry); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	access, err := storage.OpenSecret(storage.BindOAuthToken("access_token", userID, calendarID), accessToken, userID)
	if err != nil {
		return nil, err
	}
	refresh, err := storage.OpenSecret(storage.BindOAuthToken("refresh_token", userID, calendarID), refreshToken, userID)
	if err != nil {
		return nil, err
	}
	return &oauth2.Token{
		AccessToken:  access,
		RefreshToken: refresh,
		Expiry:       expiry,
	}, nil
}
