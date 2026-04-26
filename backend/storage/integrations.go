package storage

import (
	"database/sql"
	"time"

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
}

func UpsertWorkspaceConnection(db *sql.DB, c *WorkspaceConnection) error {
	_, err := db.Exec(`
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
		c.UserID, c.Provider, c.AccessToken, c.BotToken,
		c.WorkspaceID, c.WorkspaceName, pq.Array(c.Scopes), c.ExpiresAt,
	)
	return err
}

func GetWorkspaceConnection(db *sql.DB, userID uuid.UUID, provider string) (*WorkspaceConnection, error) {
	row := db.QueryRow(`
		SELECT id, user_id, provider, access_token, bot_token,
		       workspace_id, workspace_name, scopes, connected_at, expires_at
		FROM workspace_connections
		WHERE user_id = $1 AND provider = $2`, userID, provider)

	var c WorkspaceConnection
	var scopes pq.StringArray
	if err := row.Scan(&c.ID, &c.UserID, &c.Provider, &c.AccessToken, &c.BotToken,
		&c.WorkspaceID, &c.WorkspaceName, &scopes, &c.ConnectedAt, &c.ExpiresAt); err != nil {
		return nil, err
	}
	c.Scopes = []string(scopes)
	return &c, nil
}

func DeleteWorkspaceConnection(db *sql.DB, userID uuid.UUID, provider string) error {
	_, err := db.Exec(`DELETE FROM workspace_connections WHERE user_id = $1 AND provider = $2`,
		userID, provider)
	return err
}
