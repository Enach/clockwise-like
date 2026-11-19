package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// PAC-148 fixture helpers (contract md §8): an account planted into an
// organisation must satisfy the membership rule (domain-confirmed, at an
// address whose domain is the organisation's, not a consumer domain), or it
// is not a member at any read.

// pac148ConfirmAndJoin gives userID a domain-confirmed standing and runs the
// sign-in step, which joins the organisation of its address's domain: the
// fixture form of PAC-68's AssociateUserWithOrg on a vouched-for account.
func pac148ConfirmAndJoin(t testing.TB, db *sql.DB, userID uuid.UUID) error {
	t.Helper()
	if _, err := db.Exec(`UPDATE users SET address_standing = 'domain_confirmed' WHERE id = $1`, userID); err != nil {
		t.Fatalf("confirm %s: %v", userID, err)
	}
	return storage.ReconcileUserOrg(db, userID)
}

// pac148Plant writes users.org_id directly, as an older release, an operator
// step or a fixture would leave it, with no sign-in step or migration after.
func pac148Plant(t testing.TB, db *sql.DB, userID, orgID uuid.UUID) {
	t.Helper()
	if _, err := db.Exec(`UPDATE users SET org_id = $1 WHERE id = $2`, orgID, userID); err != nil {
		t.Fatalf("plant %s in %s: %v", userID, orgID, err)
	}
}

// pac148RawOrg reads the STORED users.org_id (User.OrgID is the effective
// organisation, contract md §2.3).
func pac148RawOrg(t testing.TB, db *sql.DB, userID uuid.UUID) uuid.NullUUID {
	t.Helper()
	var org uuid.NullUUID
	if err := db.QueryRow(`SELECT org_id FROM users WHERE id = $1`, userID).Scan(&org); err != nil && err != sql.ErrNoRows {
		t.Fatalf("raw org of %s: %v", userID, err)
	}
	return org
}

// pac148OrgOf is the organisation userID belongs to (the effective one),
// failing the test when it belongs to none.
func pac148OrgOf(t testing.TB, db *sql.DB, userID uuid.UUID) uuid.UUID {
	t.Helper()
	u, err := storage.GetUserByIDNoOrg(db, userID)
	if err != nil || u.OrgID == nil {
		t.Fatalf("%s belongs to no organisation: %v", userID, err)
	}
	return *u.OrgID
}

// pac148Contains reports whether names contains name.
func pac148Contains(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// pac148Record installs the PAC-148 statement seam (contract md §7) and
// returns a function reading what it has recorded so far, in order.
func pac148Record(t testing.TB) func() []string {
	t.Helper()
	var mu sync.Mutex
	var names []string
	storage.InstallOrgMembershipStatement(t, func(name string) {
		mu.Lock()
		defer mu.Unlock()
		names = append(names, name)
	})
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), names...)
	}
}

// pac148Into makes userID a member of orgID under the rule (contract md §8):
// its address moves to the organisation's domain (same local part), its
// standing becomes domain-confirmed, and the sign-in step joins. Fixtures
// that put an account into an organisation of another domain use it, so the
// test still reaches the gate it meant to reach instead of the membership
// 403.
func pac148Into(t testing.TB, db *sql.DB, userID, orgID uuid.UUID) {
	t.Helper()
	var d string
	if err := db.QueryRow(`SELECT domain FROM organizations WHERE id = $1`, orgID).Scan(&d); err != nil {
		t.Fatalf("organisation %s: %v", orgID, err)
	}
	if _, err := db.Exec(`UPDATE users SET
			email = split_part(email, '@', 1) || '.' || left(id::text, 8) || '@' || $2,
			email_key = CASE WHEN email_key IS NULL THEN NULL ELSE lower(split_part(email, '@', 1) || '.' || left(id::text, 8) || '@' || $2) END,
			address_standing = 'domain_confirmed'
		WHERE id = $1`, userID, d); err != nil {
		t.Fatalf("move %s to %s: %v", userID, d, err)
	}
	if err := storage.ReconcileUserOrg(db, userID); err != nil {
		t.Fatalf("join %s: %v", d, err)
	}
	if got := pac148OrgOf(t, db, userID); got != orgID {
		t.Fatalf("%s joined %s, want %s", userID, got, orgID)
	}
}

// pac148AssertBreakGlassRefused is the membership 403 of a break-glass write
// by an account that belongs to no organisation, with nothing written.
func pac148AssertBreakGlassRefused(t *testing.T, e *ssoEnv, rec *httptest.ResponseRecorder, d string) {
	t.Helper()
	if rec.Code != http.StatusForbidden || strings.TrimSpace(rec.Body.String()) != `{"error":"`+string(gen.OrgMembershipRequired)+`"}` {
		t.Fatalf("= %d %s, want the membership 403", rec.Code, rec.Body.String())
	}
	if n, _ := e.providerRow(t, d); n != 0 {
		t.Fatalf("%d SSO provider rows for %s, want 0", n, d)
	}
}
