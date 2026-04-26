package storage

import (
	"database/sql"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/google/uuid"
)

type Org struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Domain    string    `json:"domain"`
	CreatedAt time.Time `json:"createdAt"`
}

func UpsertOrg(db *sql.DB, domain, name string) (*Org, error) {
	row := db.QueryRow(`
		INSERT INTO organizations (name, domain)
		VALUES ($1, $2)
		ON CONFLICT (domain) DO UPDATE SET name = EXCLUDED.name
		RETURNING id, name, domain, created_at
	`, name, domain)
	o := &Org{}
	return o, row.Scan(&o.ID, &o.Name, &o.Domain, &o.CreatedAt)
}

func GetOrgByID(db *sql.DB, id uuid.UUID) (*Org, error) {
	row := db.QueryRow(`SELECT id, name, domain, created_at FROM organizations WHERE id = $1`, id)
	o := &Org{}
	return o, row.Scan(&o.ID, &o.Name, &o.Domain, &o.CreatedAt)
}

func SetUserOrg(db *sql.DB, userID, orgID uuid.UUID) error {
	_, err := db.Exec(`UPDATE users SET org_id = $1 WHERE id = $2`, orgID, userID)
	return err
}

// AssociateUserWithOrg derives the org from the user's email domain and links them.
// No-op for generic provider domains (gmail.com, outlook.com, etc.).
func AssociateUserWithOrg(db *sql.DB, userID uuid.UUID, email string) error {
	domain := auth.ExtractDomain(email)
	if domain == "" || auth.IsGenericDomain(domain) {
		return nil
	}
	org, err := UpsertOrg(db, domain, auth.DeriveOrgName(domain))
	if err != nil {
		return err
	}
	return SetUserOrg(db, userID, org.ID)
}

// GetOrgMembers returns all users belonging to an organization.
func GetOrgMembers(db *sql.DB, orgID uuid.UUID) ([]*User, error) {
	rows, err := db.Query(`
		SELECT id, email, name, avatar_url, provider, org_id, created_at
		FROM users WHERE org_id = $1 ORDER BY created_at
	`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		u := &User{}
		var oid uuid.NullUUID
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.Provider, &oid, &u.CreatedAt); err != nil {
			return nil, err
		}
		if oid.Valid {
			u.OrgID = &oid.UUID
		}
		users = append(users, u)
	}
	return users, rows.Err()
}
