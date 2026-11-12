package storage

import (
	"time"

	"github.com/Enach/paceday/backend/domain"
	"github.com/google/uuid"
)

type Org struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Domain    string    `json:"domain"`
	CreatedAt time.Time `json:"createdAt"`
}

func UpsertOrg(db DBTX, domain, name string) (*Org, error) {
	row := db.QueryRow(`
		INSERT INTO organizations (name, domain)
		VALUES ($1, $2)
		ON CONFLICT (domain) DO UPDATE SET name = EXCLUDED.name
		RETURNING id, name, domain, created_at
	`, name, domain)
	o := &Org{}
	return o, row.Scan(&o.ID, &o.Name, &o.Domain, &o.CreatedAt)
}

func GetOrgByID(db DBTX, id uuid.UUID) (*Org, error) {
	row := db.QueryRow(`SELECT id, name, domain, created_at FROM organizations WHERE id = $1`, id)
	o := &Org{}
	return o, row.Scan(&o.ID, &o.Name, &o.Domain, &o.CreatedAt)
}

// SetUserOrg makes orgID the user's organisation. Leaving an organisation
// ends the user's organisation-AI confirmation for it (PAC-87, factory
// decision 2026-11-12): a member who leaves and later rejoins is asked again
// before anything is sent. The membership change comes first and the delete
// is a separate statement, so it sees a confirmation that a concurrent
// ConfirmOrgAI committed before the change (that confirmation locks the same
// users row); one recorded after the change finds the caller no longer a
// member and records nothing.
func SetUserOrg(db DBTX, userID, orgID uuid.UUID) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE users SET org_id = $1 WHERE id = $2`, orgID, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM org_ai_confirmations WHERE user_id = $1 AND org_id <> $2`, userID, orgID); err != nil {
		return err
	}
	return tx.Commit()
}

// AssociateUserWithOrg derives the org from the user's email domain and links them.
// No-op for generic provider domains (gmail.com, outlook.com, etc.).
func AssociateUserWithOrg(db DBTX, userID uuid.UUID, email string) error {
	d := domain.ExtractDomain(email)
	if d == "" || domain.IsGenericDomain(d) {
		return nil
	}
	org, err := UpsertOrg(db, d, domain.DeriveOrgName(d))
	if err != nil {
		return err
	}
	return SetUserOrg(db, userID, org.ID)
}

// GetOrgMembers returns all users belonging to an organization.
func GetOrgMembers(db DBTX, orgID uuid.UUID) ([]*User, error) {
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
