package storage

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	AvatarURL string    `json:"avatarUrl"`
	Provider  string    `json:"provider"`
	// AddressStanding is PAC-71's address standing (domain_confirmed or
	// unconfirmed). GetUserByID and GetUserByEmail fill it, so /api/auth/me
	// carries it (CurrentUserResponse.addressStanding, required); the org
	// member list does not read it and omits it.
	AddressStanding string     `json:"addressStanding,omitempty"`
	OrgID           *uuid.UUID `json:"-"`
	Org             *Org       `json:"org,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
}

// UpsertUser inserts or updates a user row matched by its exact address. It is
// NOT a sign-in: since PAC-71 the callbacks resolve accounts by identity
// (ResolveSignIn). It remains for fixtures that need a pre-PAC-71 (unbound)
// account.
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

func GetUserByEmail(db *sql.DB, email string) (*User, error) {
	row := db.QueryRow(`
		SELECT id, email, name, avatar_url, provider, address_standing, org_id, created_at
		FROM users WHERE email = $1
	`, email)

	u := &User{}
	var orgID uuid.NullUUID
	if err := row.Scan(&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.Provider, &u.AddressStanding, &orgID, &u.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if orgID.Valid {
		u.OrgID = &orgID.UUID
	}
	return u, nil
}

func GetUserByID(db *sql.DB, id uuid.UUID) (*User, error) {
	row := db.QueryRow(`
		SELECT id, email, name, avatar_url, provider, address_standing, org_id, created_at
		FROM users WHERE id = $1
	`, id)

	u := &User{}
	var orgID uuid.NullUUID
	if err := row.Scan(&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.Provider, &u.AddressStanding, &orgID, &u.CreatedAt); err != nil {
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
