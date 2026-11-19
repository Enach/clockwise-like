package storage

import (
	"database/sql"
	"errors"
	"regexp"
	"strconv"
	"strings"
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

func UpsertOrg(db orgRowQuerier, domain, name string) (*Org, error) {
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
	if err := setUserOrgLocked(tx, userID, prev, orgID); err != nil {
		return err
	}
	return tx.Commit()
}

// setUserOrgLocked is SetUserOrg's write, for a caller holding the users-row
// lock under which it read prev, the stored organisation.
func setUserOrgLocked(tx *sql.Tx, userID uuid.UUID, prev uuid.NullUUID, orgID uuid.UUID) error {
	if _, err := tx.Exec(`UPDATE users SET org_id = $1 WHERE id = $2`, orgID, userID); err != nil {
		return err
	}
	var err error
	if prev.Valid && prev.UUID == orgID {
		_, err = tx.Exec(`DELETE FROM org_ai_confirmations WHERE user_id = $1 AND org_id <> $2`, userID, orgID)
	} else {
		_, err = tx.Exec(`DELETE FROM org_ai_confirmations WHERE user_id = $1`, userID)
	}
	return err
}

// The organisation membership rule (PAC-148, contract md §2.1): account A
// belongs to organisation O exactly when A.org_id = O.id, A's address
// standing is domain_confirmed, O's domain is not on the consumer list, and
// O's domain is the domain of A's stored address (addrDomainSQL, the SQL form
// of domain.ExtractDomain). It is decided at every read: every reader of
// membership goes through effectiveOrgSQL, so no stored association makes a
// member of someone who fails it (an older release's join, an operator step,
// a fixture, the restore rehearsal). The raw users.org_id column is written
// by SetUserOrg, ReconcileUserOrg and migration 043 only; a caller that needs
// the stored value reads it with SQL (User.OrgID is the effective
// organisation).

// addrDomainTrim is the set strings.TrimSpace trims: exactly the 25 code
// points unicode.IsSpace reports (contract md §2.2).
const addrDomainTrim = `U&'\0020\0009\000A\000B\000C\000D\0085\00A0\1680\2000\2001\2002\2003\2004\2005\2006\2007\2008\2009\200A\2028\2029\202F\205F\3000'`

// addrDomainSQL is the one SQL expression for the domain of the address in
// col (contract md §2.2): the text after the first @, ” with no @, trimmed
// of unicode white space, lower-cased with the database's default collation.
// It equals domain.ExtractDomain on the production image and ctype
// (TestPAC148_AddrDomainSQLEqualsExtractDomain); migration 043 and the
// runbook's pre-deploy query 1 carry the same text verbatim. Where the two
// could ever disagree, the rule fails closed (the account is not a member).
func addrDomainSQL(col string) string {
	return `CASE WHEN strpos(` + col + `, '@') = 0 THEN ''
     ELSE lower(btrim(substr(` + col + `, strpos(` + col + `, '@') + 1),
          ` + addrDomainTrim + `))
END`
}

// consumerDomainEntry is what a consumer-list entry may contain, so the
// literal built from it cannot inject anything.
var consumerDomainEntry = regexp.MustCompile(`^[a-z0-9.-]+$`)

// consumerListSQL is the consumer list as a SQL text[] literal, built once
// from domain.GenericDomains() (contract md §2.2): never passed by a caller,
// so it cannot differ from the Go list.
var consumerListSQL = buildConsumerListSQL(domain.GenericDomains())

func buildConsumerListSQL(list []string) string {
	quoted := make([]string, 0, len(list))
	for _, d := range list {
		if !consumerDomainEntry.MatchString(d) {
			panic("storage: consumer-list entry " + strconv.Quote(d) + " is not a plain lower-case domain")
		}
		quoted = append(quoted, "'"+d+"'")
	}
	return "ARRAY[" + strings.Join(quoted, ",") + "]::text[]"
}

// effectiveOrgSQL is the one SQL expression for the organisation the account
// row aliased u belongs to under the rule, NULL when none (contract md
// §2.2). It reads only u's own columns and the organisation row u.org_id
// names: never an organisation by domain. As a scalar subquery it takes no
// lock on organizations when its statement locks u.
func effectiveOrgSQL(u string) string {
	return `(SELECT eo.id FROM organizations eo
  WHERE eo.id = ` + u + `.org_id
    AND ` + u + `.address_standing = '` + AddressDomainConfirmed + `'
    AND eo.domain <> ALL(` + consumerListSQL + `)
    AND eo.domain = ` + addrDomainSQL(u+".email") + `)`
}

// orgRowQuerier is the part of DBTX and *sql.Tx that UpsertOrg uses.
type orgRowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

// ReconcileUserOrg brings userID's stored organisation association in line
// with the membership rule after a sign-in (PAC-148, contract md §2.4),
// replacing PAC-68's AssociateUserWithOrg. One transaction:
//
//  1. the users row FOR NO KEY UPDATE (PAC-87's users-row lock), reading the
//     STORED address and standing, never the sign-in's evidence;
//  2. the decision, in Go, before anything keyed on the domain runs: the
//     account qualifies when it is domain_confirmed, its address has a
//     domain, and that domain is not on the consumer list;
//  3. qualifies: the domain's organisation is upserted (lock order: the users
//     row, then organizations) and joined with SetUserOrg's semantics (a
//     changed organisation ends every organisation-AI confirmation; an
//     unchanged one ends those of other organisations);
//  4. does not qualify: a stored association is ended together with every
//     organisation-AI confirmation of the account; with none, nothing more
//     runs. No organisation is read, looked up or created.
//
// A user that does not exist is nothing to do. The caller reports a failure
// (SF-071) and lets the sign-in go on; the next sign-in applies the rule
// again.
func ReconcileUserOrg(db DBTX, userID uuid.UUID) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	fireOrgMembershipStatement("membership-lock")
	var email, standing string
	var prev uuid.NullUUID
	err = tx.QueryRow(`SELECT email, address_standing, org_id FROM users WHERE id = $1 FOR NO KEY UPDATE`, userID).
		Scan(&email, &standing, &prev)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	d := domain.ExtractDomain(email)
	if standing == AddressDomainConfirmed && d != "" && !domain.IsGenericDomain(d) {
		fireOrgMembershipStatement("org-upsert")
		org, err := UpsertOrg(tx, d, domain.DeriveOrgName(d))
		if err != nil {
			return err
		}
		fireOrgMembershipStatement("membership-set")
		if err := setUserOrgLocked(tx, userID, prev, org.ID); err != nil {
			return err
		}
		return tx.Commit()
	}
	if !prev.Valid {
		return nil // no association, and none to make: nothing written
	}
	fireOrgMembershipStatement("membership-leave")
	if _, err := tx.Exec(`DELETE FROM org_ai_confirmations WHERE user_id = $1`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE users SET org_id = NULL WHERE id = $1`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// orgMemberSetSQL is the one definition of a viewer's organisation member set
// (PAC-130 contract md §2.5, narrowed by PAC-148 contract md §2.5): the ids of
// every account that belongs to the viewer's organisation under the rule, the
// viewer included, minus the excluded ids. The viewer's effective
// organisation is computed once; the listed side joins on the raw, indexed
// o.org_id and is then filtered by its own effective organisation, so
// idx_users_org_id carries the join. A viewer who belongs nowhere gives NULL,
// which equals nothing: the set is empty. It is a SQL fragment, not a
// function returning a set, so the Manager visibility statement can embed it
// (rule (b) adds no statement) while GetOrgMembers reads the same set. viewer
// and excluded are the placeholders that carry the viewer's id and a uuid[]
// of excluded ids; a NULL excluded array is read as empty, never as "exclude
// everything" (PAC-130 contract challenge revision 2, item 1: NOT (id =
// ANY(NULL)) is NULL, which would empty the set).
func orgMemberSetSQL(viewer, excluded string) string {
	return `SELECT o.id FROM users o
		 WHERE o.org_id = (SELECT ` + effectiveOrgSQL("me") + ` FROM users me WHERE me.id = ` + viewer + `)
		   AND ` + effectiveOrgSQL("o") + ` = o.org_id
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

// GetOrgMembers returns every account that belongs to viewerID's
// organisation under the rule, the viewer included, oldest first: the set
// orgMemberSetSQL defines (PAC-130 rule (b) reads the same one). A viewer who
// belongs to no organisation has none. Every listed account belongs, so its
// stored org_id is its effective organisation.
func GetOrgMembers(db DBTX, viewerID uuid.UUID) ([]*User, error) {
	fireOrgMembershipStatement("members-read")
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
