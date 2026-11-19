package storage

import (
	"database/sql"
	"errors"
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
	if hook, ok := orgRead.get(); ok {
		if err := hook(id); err != nil {
			return nil, err
		}
	}
	row := db.QueryRow(`SELECT id, name, domain, created_at FROM organizations WHERE id = $1`, id)
	o := &Org{}
	return o, row.Scan(&o.ID, &o.Name, &o.Domain, &o.CreatedAt)
}

// SetUserOrg makes orgID the user's organisation. Leaving an organisation
// ends the user's organisation-AI confirmation for it (PAC-87, factory
// decision 2026-11-12): a member who leaves and later rejoins is asked again
// before anything is sent. The previous organisation is read under the
// users-row lock ConfirmOrgAI also takes first, so a concurrent confirmation
// lands wholly before (and is deleted here) or wholly after (and finds the
// caller no longer a member, recording nothing). When the organisation
// changes, EVERY confirmation of the user is deleted, including one for the
// organisation being joined: a row kept from an earlier membership (for
// example one recorded before PAC-87, when a move kept it) never confirms a
// rejoin (review finding 3). When it does not change, only confirmations of
// other organisations are deleted.
func SetUserOrg(db DBTX, userID, orgID uuid.UUID) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var prev uuid.NullUUID
	err = tx.QueryRow(`SELECT org_id FROM users WHERE id = $1 FOR NO KEY UPDATE`, userID).Scan(&prev)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // no such user: nothing to move (as the bare UPDATE did)
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE users SET org_id = $1 WHERE id = $2`, orgID, userID); err != nil {
		return err
	}
	if prev.Valid && prev.UUID == orgID {
		_, err = tx.Exec(`DELETE FROM org_ai_confirmations WHERE user_id = $1 AND org_id <> $2`, userID, orgID)
	} else {
		_, err = tx.Exec(`DELETE FROM org_ai_confirmations WHERE user_id = $1`, userID)
	}
	if err != nil {
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

// orgMemberSetSQL is the one definition of a viewer's organisation member set
// (PAC-130 contract md §2.5): the ids of every account in the viewer's
// organisation, the viewer included, minus the excluded ids. It is a SQL
// fragment, not a function returning a set, so the Manager visibility
// statement can embed it (rule (b) adds no statement) while GetOrgMembers
// reads the same set. viewer and excluded are the placeholders that carry the
// viewer's id and a uuid[] of excluded ids; a NULL excluded array is read as
// empty, never as "exclude everything" (contract challenge revision 2, item 1:
// NOT (id = ANY(NULL)) is NULL, which would empty the set).
func orgMemberSetSQL(viewer, excluded string) string {
	return `SELECT o.id FROM users o JOIN users me ON me.org_id = o.org_id
		 WHERE me.id = ` + viewer + ` AND me.org_id IS NOT NULL
		   AND NOT (o.id = ANY(COALESCE(` + excluded + `::uuid[], '{}'::uuid[])))`
}

// orgMembersExcluded is the exclusion list orgMemberSetSQL receives for
// viewerID: empty in production, the orgMembersExclude seam's answer in a
// test (PAC-130 AC-17). It is never nil.
func orgMembersExcluded(viewerID uuid.UUID) []uuid.UUID {
	if fn, ok := orgMembersExclude.get(); ok {
		if ids := fn(viewerID); ids != nil {
			return ids
		}
	}
	return []uuid.UUID{}
}

// GetOrgMembers returns every account in viewerID's organisation, the viewer
// included, oldest first: the set orgMemberSetSQL defines (PAC-130 rule (b)
// reads the same one). A viewer with no organisation has none.
func GetOrgMembers(db DBTX, viewerID uuid.UUID) ([]*User, error) {
	rows, err := db.Query(`
		SELECT id, email, name, avatar_url, provider, org_id, created_at
		FROM users WHERE id IN (`+orgMemberSetSQL("$1", "$2")+`) ORDER BY created_at
	`, viewerID, orgMembersExcluded(viewerID))
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
