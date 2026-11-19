package storage

import (
	"database/sql"
	"testing"

	"github.com/google/uuid"
)

// PAC-148 fixture helpers (contract md §8): an account planted into an
// organisation must satisfy the membership rule, i.e. be domain-confirmed at
// an address whose domain is the organisation's, or it is not a member.

// pac148Confirm gives userID a domain-confirmed standing.
func pac148Confirm(t testing.TB, db DBTX, userID uuid.UUID) {
	t.Helper()
	if _, err := db.Exec(`UPDATE users SET address_standing = 'domain_confirmed' WHERE id = $1`, userID); err != nil {
		t.Fatalf("confirm %s: %v", userID, err)
	}
}

// pac148ConfirmAndJoin makes userID domain-confirmed and runs the sign-in
// step, which joins the organisation of its address's domain (the fixture
// form of PAC-68's AssociateUserWithOrg on a vouched-for account).
func pac148ConfirmAndJoin(t testing.TB, db DBTX, userID uuid.UUID) error {
	t.Helper()
	pac148Confirm(t, db, userID)
	return ReconcileUserOrg(db, userID)
}

// pac148RawOrg reads the STORED users.org_id (User.OrgID is the effective
// organisation, contract md §2.3).
func pac148RawOrg(t testing.TB, db DBTX, userID uuid.UUID) uuid.NullUUID {
	t.Helper()
	var org uuid.NullUUID
	if err := db.QueryRow(`SELECT org_id FROM users WHERE id = $1`, userID).Scan(&org); err != nil && err != sql.ErrNoRows {
		t.Fatalf("raw org of %s: %v", userID, err)
	}
	return org
}
