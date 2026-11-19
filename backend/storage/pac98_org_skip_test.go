package storage

import (
	"testing"

	"github.com/Enach/paceday/backend/domain"
)

// pac148AddedConsumerDomains are the 17 Microsoft personal-account domains
// PAC-148 adds to the consumer list (contract md §4); none was dropped by the
// MX admission rule (docs/plans/PAC-148.md).
var pac148AddedConsumerDomains = []string{
	"hotmail.fr", "hotmail.co.uk", "hotmail.de", "hotmail.it", "hotmail.es", "hotmail.be",
	"live.fr", "live.co.uk", "live.de", "live.it", "live.nl", "live.be",
	"outlook.fr", "outlook.de", "outlook.it", "outlook.es",
	"windowslive.com",
}

// PAC-98 AC-21 (docs/specs/PAC-98.md §6), extended by PAC-148 AC-2
// (docs/specs/PAC-148.md §4): organisation matching skips every
// consumer-list domain, enumerated from the domain package, even for a
// DOMAIN-CONFIRMED account (before PAC-148 the fixture accounts were
// unconfirmed, which the membership rule now refuses on its own, so the
// skip has to be shown for an account that would otherwise qualify). The
// sign-in step (ReconcileUserOrg) neither joins nor creates an organisation
// row for the domain.
func TestPAC148_AC2_NoConsumerDomainFormsAnOrg(t *testing.T) {
	db := openTestDB(t)
	list := domain.GenericDomains()
	if len(list) == 0 {
		t.Fatal("the consumer list is empty")
	}
	listed := map[string]bool{}
	for _, d := range list {
		listed[d] = true
	}
	for _, d := range pac148AddedConsumerDomains {
		if !listed[d] {
			t.Errorf("%s (PAC-148 contract md §4) is not on the consumer list", d)
		}
	}
	for _, d := range list {
		email := "pac98-user@" + d
		u, err := UpsertUser(db, email, "PAC-98", "", "google", "sub-pac98-"+d)
		if err != nil {
			t.Fatalf("UpsertUser(%s): %v", email, err)
		}
		pac148Confirm(t, db, u.ID)
		if err := ReconcileUserOrg(db, u.ID); err != nil {
			t.Fatalf("ReconcileUserOrg(%s): %v", email, err)
		}
		if raw := pac148RawOrg(t, db, u.ID); raw.Valid {
			t.Errorf("%s: stored org_id = %v, want null", d, raw.UUID)
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

	// A company domain is linked, so the skip above is not a no-op: the same
	// domain-confirmed account at an unlisted domain joins.
	const company = "acme-pac98.test"
	u, err := UpsertUser(db, "alice@"+company, "Alice", "", "google", "sub-pac98-alice")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	if err := pac148ConfirmAndJoin(t, db, u.ID); err != nil {
		t.Fatalf("ReconcileUserOrg: %v", err)
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
