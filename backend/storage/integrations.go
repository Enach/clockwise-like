package storage

import (
	"time"

	"github.com/Enach/paceday/backend/internal/secretbox"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

type WorkspaceConnection struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	Provider      string // "slack" or "notion"
	AccessToken   string
	BotToken      string // Slack only
	WorkspaceID   string
	WorkspaceName string
	Scopes        []string
	ConnectedAt   time.Time
	ExpiresAt     *time.Time
	// TokenErr is the decryption failure of AccessToken or BotToken, if any
	// (PAC-84): the read itself never fails on it (contract 6.2, K-2); a
	// caller that acts with a token checks it first. Status and presence
	// callers keep their existing mapping.
	TokenErr error
}

// UpsertWorkspaceConnection stores c, its tokens sealed for (user_id,
// provider), the upsert's conflict key (PAC-84 contract 3.3).
func UpsertWorkspaceConnection(db DBTX, c *WorkspaceConnection) error {
	access, err := secretbox.Seal(bindWorkspace("access_token", c.UserID, c.Provider), c.AccessToken)
	if err != nil {
		return err
	}
	bot, err := secretbox.Seal(bindWorkspace("bot_token", c.UserID, c.Provider), c.BotToken)
	if err != nil {
		return err
	}
	_, err = db.Exec(`
		INSERT INTO workspace_connections
			(user_id, provider, access_token, bot_token, workspace_id, workspace_name, scopes, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (user_id, provider) DO UPDATE SET
			access_token   = EXCLUDED.access_token,
			bot_token      = EXCLUDED.bot_token,
			workspace_id   = EXCLUDED.workspace_id,
			workspace_name = EXCLUDED.workspace_name,
			scopes         = EXCLUDED.scopes,
			expires_at     = EXCLUDED.expires_at,
			connected_at   = now()`,
		c.UserID, c.Provider, access, bot,
		c.WorkspaceID, c.WorkspaceName, pq.Array(c.Scopes), c.ExpiresAt,
	)
	return err
}

// GetWorkspaceConnection returns userID's connection to provider. A token
// that cannot be decrypted is left "" and reported in TokenErr; it is never
// the returned error (PAC-84 K-2: callers map any error to "not connected").
func GetWorkspaceConnection(db DBTX, userID uuid.UUID, provider string) (*WorkspaceConnection, error) {
	row := db.QueryRow(`
		SELECT id, user_id, provider, access_token, bot_token,
		       workspace_id, workspace_name, scopes, connected_at, expires_at
		FROM workspace_connections
		WHERE user_id = $1 AND provider = $2`, userID, provider)

	var c WorkspaceConnection
	var scopes pq.StringArray
	var access, bot string
	if err := row.Scan(&c.ID, &c.UserID, &c.Provider, &access, &bot,
		&c.WorkspaceID, &c.WorkspaceName, &scopes, &c.ConnectedAt, &c.ExpiresAt); err != nil {
		return nil, err
	}
	c.Scopes = []string(scopes)
	var err error
	if c.AccessToken, err = openSecret(bindWorkspace("access_token", c.UserID, c.Provider), access, c.UserID); err != nil {
		c.TokenErr = err
	}
	if c.BotToken, err = openSecret(bindWorkspace("bot_token", c.UserID, c.Provider), bot, c.UserID); err != nil && c.TokenErr == nil {
		c.TokenErr = err
	}
	return &c, nil
}

func DeleteWorkspaceConnection(db DBTX, userID uuid.UUID, provider string) error {
	_, err := db.Exec(`DELETE FROM workspace_connections WHERE user_id = $1 AND provider = $2`,
		userID, provider)
	return err
}
