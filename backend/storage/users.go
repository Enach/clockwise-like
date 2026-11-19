package storage

import (
	"database/sql"
	"errors"
	"fmt"
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
func UpsertUser(db DBTX, email, name, avatarURL, provider, providerID string) (*User, error) {
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

// GetUserByEmail reads the user holding exactly email; OrgID is the
// effective organisation (PAC-148 contract md §2.3), as for getUserRow. A
// missing row is (nil, nil). No production caller reads it today.
func GetUserByEmail(db DBTX, email string) (*User, error) {
	row := db.QueryRow(`
		SELECT u.id, u.email, u.name, u.avatar_url, u.provider, u.address_standing, `+effectiveOrgSQL("u")+`, u.created_at
		FROM users u WHERE u.email = $1
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

// GetUserByID reads the user with its organisation (User.Org), for the
// callers that answer with the whole user (GET /api/auth/me). A user row that
// does not exist is sql.ErrNoRows. An organisation that no longer exists is
// no organisation (Org nil, OrgID still set); any other failed organisation
// read is the error (PAC-142), never a user without an org, which a
// serialised User (org is omitempty) would show as "no organisation".
// Callers that do not read Org use GetUserByIDNoOrg.
func GetUserByID(db DBTX, id uuid.UUID) (*User, error) {
	u, err := getUserRow(db, id)
	if err != nil {
		return nil, err
	}
	if u.OrgID != nil {
		org, err := GetOrgByID(db, *u.OrgID)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return nil, fmt.Errorf("read organisation of user %s: %w", id, err)
		default:
			u.Org = org
		}
	}
	return u, nil
}

// GetUserByIDNoOrg reads the user row only: OrgID is set, Org is always nil.
// It is for callers that read the user's name, email, picture or OrgID and
// never Org (PAC-142): a failed organisation read must not fail them, and
// they save the query. A user row that does not exist is sql.ErrNoRows.
func GetUserByIDNoOrg(db DBTX, id uuid.UUID) (*User, error) {
	return getUserRow(db, id)
}

// getUserRow is the users-row read both readers share. The InstallUserRead
// seam fires here, so it covers both. OrgID is the EFFECTIVE organisation
// (PAC-148 contract md §2.3): the stored association only when the account
// belongs to it under the membership rule (effectiveOrgSQL), nil otherwise,
// whatever users.org_id holds. A caller that needs the stored column reads it
// with SQL.
func getUserRow(db DBTX, id uuid.UUID) (*User, error) {
	if hook, ok := userRead.get(); ok {
		if err := hook(id); err != nil {
			return nil, err
		}
	}
	fireOrgMembershipStatement("user-row")
	row := db.QueryRow(`
		SELECT u.id, u.email, u.name, u.avatar_url, u.provider, u.address_standing, `+effectiveOrgSQL("u")+`, u.created_at
		FROM users u WHERE u.id = $1
	`, id)

	u := &User{}
	var orgID uuid.NullUUID
	if err := row.Scan(&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.Provider, &u.AddressStanding, &orgID, &u.CreatedAt); err != nil {
		return nil, err
	}
	if orgID.Valid {
		u.OrgID = &orgID.UUID
	}
	return u, nil
}
