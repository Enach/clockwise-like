package storage

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID        uuid.UUID  `json:"id"`
	Email     string     `json:"email"`
	Name      string     `json:"name"`
	AvatarURL string     `json:"avatarUrl"`
	Provider  string     `json:"provider"`
	OrgID     *uuid.UUID `json:"-"`
	Org       *Org       `json:"org,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
}

func UpsertUser(db *sql.DB, email, name, avatarURL, provider, providerID string) (*User, error) {
	row := db.QueryRow(`
		INSERT INTO users (email, name, avatar_url, provider, provider_id, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (email) DO UPDATE SET
			name        = EXCLUDED.name,
			avatar_url  = EXCLUDED.avatar_url,
			provider_id = EXCLUDED.provider_id,
			updated_at  = NOW()
		RETURNING id, email, name, avatar_url, provider, created_at
	`, email, name, avatarURL, provider, providerID)

	u := &User{}
	return u, row.Scan(&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.Provider, &u.CreatedAt)
}

func GetUserByID(db *sql.DB, id uuid.UUID) (*User, error) {
	row := db.QueryRow(`
		SELECT id, email, name, avatar_url, provider, org_id, created_at
		FROM users WHERE id = $1
	`, id)

	u := &User{}
	var orgID uuid.NullUUID
	if err := row.Scan(&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.Provider, &orgID, &u.CreatedAt); err != nil {
		return nil, err
	}
	if orgID.Valid {
		u.OrgID = &orgID.UUID
		if org, err := GetOrgByID(db, orgID.UUID); err == nil {
			u.Org = org
		}
	}
	return u, nil
}
