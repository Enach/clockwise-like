package storage

import (
	"testing"

	"github.com/Enach/paceday/backend/domain"
)

// PAC-98 AC-21 (docs/specs/PAC-98.md §6): organisation matching skips every
// consumer-list domain, enumerated from the domain package. PAC-98 changes
// sign-in detection for these domains, never organisation matching; no test
// pinned the skip before.
func TestPAC98_AC21_AssociateUserWithOrgSkipsEveryConsumerDomain(t *testing.T) {
	db := openTestDB(t)
	list := domain.GenericDomains()
	if len(list) == 0 {
		t.Fatal("the consumer list is empty")
	}
	for _, d := range list {
		email := "pac98-user@" + d
		u, err := UpsertUser(db, email, "PAC-98", "", "google", "sub-pac98-"+d)
		if err != nil {
			t.Fatalf("UpsertUser(%s): %v", email, err)
		}
		if err := AssociateUserWithOrg(db, u.ID, email); err != nil {
			t.Fatalf("AssociateUserWithOrg(%s): %v", email, err)
		}
		got, err := GetUserByID(db, u.ID)
		if err != nil {
			t.Fatalf("GetUserByID(%s): %v", email, err)
		}
		if got.OrgID != nil {
			t.Errorf("%s: org_id = %v, want null", d, *got.OrgID)
		}
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM organizations WHERE domain = $1`, d).Scan(&n); err != nil {
			t.Fatalf("count organizations for %s: %v", d, err)
		}
		if n != 0 {
			t.Errorf("%s: %d organisation rows, want 0", d, n)
		}
	}

	// A company domain is linked, so the skip above is not a no-op.
	const company = "acme-pac98.test"
	u, err := UpsertUser(db, "alice@"+company, "Alice", "", "google", "sub-pac98-alice")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	if err := AssociateUserWithOrg(db, u.ID, u.Email); err != nil {
		t.Fatalf("AssociateUserWithOrg: %v", err)
	}
	got, err := GetUserByID(db, u.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.OrgID == nil {
		t.Fatalf("alice@%s: org_id is null, want the %s organisation", company, company)
	}
	org, err := GetOrgByID(db, *got.OrgID)
	if err != nil {
		t.Fatalf("GetOrgByID: %v", err)
	}
	if org.Domain != company {
		t.Errorf("org domain = %q, want %q", org.Domain, company)
	}
}
