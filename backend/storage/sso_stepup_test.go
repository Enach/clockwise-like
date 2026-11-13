package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// PAC-50 storage: the step-up grant (spec §4.1, §4.2 step 5a), the SSO event
// table (§4.6), the tenant pin (§4.3) and migration 029 (§4.8).

// stepupFixture creates a user in a fresh org and returns both ids.
func stepupFixture(t *testing.T, db *sql.DB, label string) (uuid.UUID, uuid.UUID, string) {
	t.Helper()
	d := "pac50-" + label + "-" + strings.ReplaceAll(uuid.NewString()[:8], "-", "") + ".test"
	u, err := UpsertUser(db, "admin@"+d, "Admin", "", "google", "sub-"+d)
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	if err := AssociateUserWithOrg(db, u.ID, u.Email); err != nil {
		t.Fatalf("AssociateUserWithOrg: %v", err)
	}
	u, err = GetUserByID(db, u.ID)
	if err != nil || u.OrgID == nil {
		t.Fatalf("GetUserByID: %v (org %v)", err, u)
	}
	return u.ID, *u.OrgID, d
}

func TestSSOStepupGrant_ConsumeOnceWithinTTL(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	user, org, _ := stepupFixture(t, db, "consume")
	t0 := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

	g, err := IssueSSOStepupGrant(ctx, db, user, org, "google", SSOGrantPurposeSSOWrite, t0)
	if err != nil {
		t.Fatalf("IssueSSOStepupGrant: %v", err)
	}
	if !g.ExpiresAt.Equal(t0.Add(SSOStepupGrantTTL)) {
		t.Fatalf("ExpiresAt = %v, want issue + 10 min", g.ExpiresAt)
	}

	// AC-4b: at T+11 the grant is expired and the attempt leaves it unconsumed.
	if _, ok, err := ConsumeSSOStepupGrant(ctx, db, user, org, SSOGrantPurposeSSOWrite, t0.Add(11*time.Minute)); err != nil || ok {
		t.Fatalf("consume at T+11 = %v, %v; want no grant", ok, err)
	}
	if got, _ := GetSSOStepupGrant(ctx, db, g.ID); got.ConsumedAt != nil {
		t.Fatal("an expired attempt consumed the grant")
	}

	// AC-4a: at T+9 it is live, and it is consumed exactly once (AC-3a).
	id, ok, err := ConsumeSSOStepupGrant(ctx, db, user, org, SSOGrantPurposeSSOWrite, t0.Add(9*time.Minute))
	if err != nil || !ok || id != g.ID {
		t.Fatalf("consume at T+9 = %v, %v, %v; want the grant", id, ok, err)
	}
	if _, ok, _ := ConsumeSSOStepupGrant(ctx, db, user, org, SSOGrantPurposeSSOWrite, t0.Add(9*time.Minute)); ok {
		t.Fatal("a grant was consumed twice")
	}
}

func TestSSOStepupGrant_BoundToUserAndOrg(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	user, org, _ := stepupFixture(t, db, "bound-a")
	other, otherOrg, _ := stepupFixture(t, db, "bound-b")
	now := time.Now().UTC()

	g, err := IssueSSOStepupGrant(ctx, db, user, org, "microsoft", SSOGrantPurposeSSOWrite, now)
	if err != nil {
		t.Fatal(err)
	}
	// AC-2b: another user of the same org cannot use it.
	if _, ok, _ := ConsumeSSOStepupGrant(ctx, db, other, org, SSOGrantPurposeSSOWrite, now); ok {
		t.Fatal("another user consumed the grant")
	}
	// AC-2a: the same user, now in another org, cannot use it.
	if _, ok, _ := ConsumeSSOStepupGrant(ctx, db, user, otherOrg, SSOGrantPurposeSSOWrite, now); ok {
		t.Fatal("the grant was consumed for another org")
	}
	if got, _ := GetSSOStepupGrant(ctx, db, g.ID); got.ConsumedAt != nil {
		t.Fatal("a refused consume marked the grant")
	}
}

// AC-3b at the storage layer: two consumers released together, one grant.
func TestSSOStepupGrant_ConcurrentConsumeIsSingle(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	user, org, _ := stepupFixture(t, db, "race")
	now := time.Now().UTC()
	g, err := IssueSSOStepupGrant(ctx, db, user, org, "google", SSOGrantPurposeSSOWrite, now)
	if err != nil {
		t.Fatal(err)
	}
	const n = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	wins := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, ok, err := ConsumeSSOStepupGrant(ctx, db, user, org, SSOGrantPurposeSSOWrite, now)
			if err != nil {
				t.Errorf("consume: %v", err)
			}
			wins <- ok
		}()
	}
	close(start)
	wg.Wait()
	close(wins)
	won := 0
	for ok := range wins {
		if ok {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("%d consumers won, want exactly 1", won)
	}
	got, _ := GetSSOStepupGrant(ctx, db, g.ID)
	if got.ConsumedAt == nil {
		t.Fatal("consumed_at not set")
	}
}

func TestRecordSSOStepupOutcome_GrantedIssuesGrantPinsAndRecords(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	user, org, _ := stepupFixture(t, db, "outcome")
	now := time.Now().UTC()

	g, err := RecordSSOStepupOutcome(ctx, db, SSOStepupOutcome{
		UserID: user, OrgID: &org, Provider: "microsoft", Granted: true, PinTenant: "tenant-1", Now: now,
	})
	if err != nil || g == nil {
		t.Fatalf("RecordSSOStepupOutcome = %v, %v", g, err)
	}
	// Every grant a step-up issues is for an SSO configuration write (AC-27).
	if stored, _ := GetSSOStepupGrant(ctx, db, g.ID); stored == nil || stored.Purpose != SSOGrantPurposeSSOWrite {
		t.Fatalf("issued grant = %+v, want purpose %q", stored, SSOGrantPurposeSSOWrite)
	}
	if tid, _ := GetOrgMSTenantID(ctx, db, org); tid != "tenant-1" {
		t.Fatalf("pin = %q, want tenant-1", tid)
	}
	// Idempotent for the same tenant.
	if _, err := RecordSSOStepupOutcome(ctx, db, SSOStepupOutcome{
		UserID: user, OrgID: &org, Provider: "microsoft", Granted: true, PinTenant: "tenant-1", Now: now,
	}); err != nil {
		t.Fatalf("second pin for the same tenant: %v", err)
	}
	// A different tenant never moves the pin and issues nothing.
	if _, err := RecordSSOStepupOutcome(ctx, db, SSOStepupOutcome{
		UserID: user, OrgID: &org, Provider: "microsoft", Granted: true, PinTenant: "tenant-2", Now: now,
	}); !errors.Is(err, ErrTenantPinned) {
		t.Fatalf("pin to another tenant: err = %v, want ErrTenantPinned", err)
	}
	if tid, _ := GetOrgMSTenantID(ctx, db, org); tid != "tenant-1" {
		t.Fatalf("pin moved to %q", tid)
	}

	events, err := ListSSOEventsByUser(ctx, db, user)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("%d events, want 2 (the refused pin records nothing at this layer)", len(events))
	}
	var d struct {
		OrgID    string  `json:"org_id"`
		Provider string  `json:"provider"`
		Outcome  string  `json:"outcome"`
		Reason   *string `json:"reason"`
	}
	if err := json.Unmarshal(events[0].Details, &d); err != nil {
		t.Fatalf("details are not JSON: %v (%s)", err, events[0].Details)
	}
	if events[0].Kind != "sso_stepup" || d.OrgID != org.String() || d.Provider != "microsoft" || d.Outcome != "granted" || d.Reason != nil {
		t.Fatalf("event = %s %s", events[0].Kind, events[0].Details)
	}
}

func TestRecordSSOStepupOutcome_DeniedIssuesNothing(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	user, org, _ := stepupFixture(t, db, "denied")
	now := time.Now().UTC()
	g, err := RecordSSOStepupOutcome(ctx, db, SSOStepupOutcome{
		UserID: user, OrgID: &org, Provider: "google", Granted: false, Reason: "not_admin", PinTenant: "ignored", Now: now,
	})
	if err != nil || g != nil {
		t.Fatalf("denied outcome = %v, %v; want no grant", g, err)
	}
	if _, ok, _ := ConsumeSSOStepupGrant(ctx, db, user, org, SSOGrantPurposeSSOWrite, now); ok {
		t.Fatal("a denied step-up left a usable grant")
	}
	if tid, _ := GetOrgMSTenantID(ctx, db, org); tid != "" {
		t.Fatalf("a denied step-up pinned %q", tid)
	}
	// An outcome with no org (state_mismatch for an orgless caller) is still recorded.
	if _, err := RecordSSOStepupOutcome(ctx, db, SSOStepupOutcome{
		UserID: user, Provider: "google", Reason: "state_mismatch", Now: now,
	}); err != nil {
		t.Fatalf("orgless outcome: %v", err)
	}
	events, _ := ListSSOEventsByUser(ctx, db, user)
	if len(events) != 2 || events[1].OrgID != nil {
		t.Fatalf("events = %+v", events)
	}
	var d map[string]any
	if err := json.Unmarshal(events[1].Details, &d); err != nil || d["reason"] != "state_mismatch" || d["org_id"] != nil {
		t.Fatalf("details = %s (%v)", events[1].Details, err)
	}
}

func TestSSOProviderRecordedWrites(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	user, org, d := stepupFixture(t, db, "recorded")

	secret := "s-recorded"
	p, err := SaveOIDCProviderRecorded(ctx, db, OIDCProviderWrite{
		Domain: strings.ToUpper(d), ProviderName: "Okta", Issuer: "https://i", ClientID: "c", ClientSecret: &secret,
	}, user, org, "stepup")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.Domain != d {
		t.Fatalf("stored domain %q, want lower-cased %q", p.Domain, d)
	}
	// AC-20b at this layer: another casing updates the same row.
	// The secret left out is kept (PAC-22), and the replace is recorded.
	p2, err := SaveOIDCProviderRecorded(ctx, db, OIDCProviderWrite{
		Domain: d, ProviderName: "Okta 2", Issuer: "https://i", ClientID: "c",
	}, user, org, "breakglass")
	if err != nil || p2.ID != p.ID || !p2.UpdatedAt.After(p.UpdatedAt) || p2.OIDCClientSecret != secret {
		t.Fatalf("update = %+v, %v", p2, err)
	}
	// A refused store (the kept secret no longer applies) records nothing.
	if _, err := SaveOIDCProviderRecorded(ctx, db, OIDCProviderWrite{
		Domain: d, ProviderName: "Okta 3", Issuer: "https://other", ClientID: "c",
	}, user, org, "stepup"); err != ErrSSOClientSecretTargetChanged {
		t.Fatalf("refused replace = %v, want ErrSSOClientSecretTargetChanged", err)
	}
	found, err := DeleteSSOProviderRecorded(ctx, db, strings.ToUpper(d), user, org, "stepup")
	if err != nil || !found {
		t.Fatalf("delete = %v, %v", found, err)
	}
	found, err = DeleteSSOProviderRecorded(ctx, db, d, user, org, "stepup")
	if err != nil || found {
		t.Fatalf("delete of an absent row = %v, %v; want not found", found, err)
	}

	events, _ := ListSSOEventsByUser(ctx, db, user)
	wantOps := []string{"create", "update", "delete"}
	wantVia := []string{"stepup", "breakglass", "stepup"}
	if len(events) != 3 {
		t.Fatalf("%d events, want 3 (an absent delete records nothing)", len(events))
	}
	for i, e := range events {
		var det struct {
			OrgID      string `json:"org_id"`
			ProviderID string `json:"provider_id"`
			Op         string `json:"op"`
			Via        string `json:"via"`
		}
		if err := json.Unmarshal(e.Details, &det); err != nil {
			t.Fatalf("event %d details: %v", i, err)
		}
		if e.Kind != "sso_provider_written" || det.Op != wantOps[i] || det.Via != wantVia[i] || det.ProviderID != p.ID.String() || det.OrgID != org.String() {
			t.Errorf("event %d = %s %s", i, e.Kind, e.Details)
		}
	}
}

// AC-20a: migration 030 (030_sso_admin_stepup, spec §9 D-7) lower-cases
// existing rows and makes uniqueness case-insensitive; the down migration
// restores the raw constraint and leaves main's 026 (PAC-23), 027 (PAC-49),
// 028 (PAC-67, credential_losses) and 029 (PAC-73, llm_bring_your_own)
// applied. The grant table carries a purpose with no default (N5, AC-27).
func TestMigration030_LowerCasesAndUniquesProviderDomains(t *testing.T) {
	db, to := migrationTestDB(t)
	to(29)
	if _, err := db.Exec(`INSERT INTO sso_providers (domain, provider_name, provider_type) VALUES ('ACME.com', 'Okta', 'oidc')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	to(30)
	var got string
	if err := db.QueryRow(`SELECT domain FROM sso_providers`).Scan(&got); err != nil || got != "acme.com" {
		t.Fatalf("domain after 030 = %q, %v; want acme.com", got, err)
	}
	// purpose has no default: an insert that does not name it is refused.
	if _, err := db.Exec(`INSERT INTO users (email, name) VALUES ('m30@acme.com', 'M')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO organizations (name, domain) VALUES ('Acme', 'acme.com')`); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO sso_stepup_grants (user_id, org_id, provider, issued_at, expires_at)
		SELECT u.id, o.id, 'google', NOW(), NOW() + interval '10 minutes' FROM users u, organizations o`); err == nil {
		t.Fatal("a grant without a purpose was accepted")
	}
	if _, err := db.Exec(`INSERT INTO sso_providers (domain, provider_name, provider_type) VALUES ('Acme.com', 'Dup', 'oidc')`); err == nil {
		t.Fatal("a second casing of the same domain was accepted")
	}
	for _, q := range []string{
		`SELECT count(*) FROM sso_stepup_grants`,
		`SELECT count(*) FROM sso_events`,
		`SELECT count(ms_tenant_id) FROM organizations`,
	} {
		var n int
		if err := db.QueryRow(q).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	// Down to 029, then up again: both directions run.
	to(29)
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM information_schema.tables WHERE table_name IN ('sso_stepup_grants','sso_events')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("tables left after down: %d, %v", n, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_name = 'organizations' AND column_name = 'ms_tenant_id'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("ms_tenant_id left after down: %d, %v", n, err)
	}
	if _, err := db.Exec(`INSERT INTO sso_providers (domain, provider_name, provider_type) VALUES ('acme.com', 'Dup', 'oidc')`); err == nil {
		t.Fatal("raw UNIQUE(domain) not restored by the down migration")
	}
	// 026's and 027's columns, 028's table and 029's columns are still there:
	// 030's down touches none of them.
	if err := db.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE (table_name = 'audit_log' AND column_name = 'user_id') OR (table_name = 'settings' AND column_name = 'auto_schedule_last_run')`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("026/027 columns after 030 down: %d, %v; want 2", n, err)
	}
	var losses bool
	if err := db.QueryRow(`SELECT to_regclass('credential_losses') IS NOT NULL`).Scan(&losses); err != nil || !losses {
		t.Fatalf("028's credential_losses after 030 down: %v, %v; want present", losses, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_name = 'settings' AND column_name = 'llm_api_key_openai'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("029's llm_api_key_openai after 030 down: %d, %v; want 1", n, err)
	}
	to(30)
}

// AC-27 at the storage layer (contract revision 8.1, N5): a grant is
// consumed only by a write of its own purpose.
func TestConsumeSSOStepupGrant_MatchesPurpose(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	user, org, _ := stepupFixture(t, db, "purpose")
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

	other, err := IssueSSOStepupGrant(ctx, db, user, org, "google", "llm_org_key", now)
	if err != nil {
		t.Fatalf("issue other purpose: %v", err)
	}
	if _, ok, err := ConsumeSSOStepupGrant(ctx, db, user, org, SSOGrantPurposeSSOWrite, now); err != nil || ok {
		t.Fatalf("an sso_write consume took a grant of another purpose: ok=%v err=%v", ok, err)
	}
	sso, err := IssueSSOStepupGrant(ctx, db, user, org, "google", SSOGrantPurposeSSOWrite, now)
	if err != nil {
		t.Fatalf("issue sso_write: %v", err)
	}
	id, ok, err := ConsumeSSOStepupGrant(ctx, db, user, org, SSOGrantPurposeSSOWrite, now)
	if err != nil || !ok || id != sso.ID {
		t.Fatalf("consume = %v %v %v, want the sso_write grant", id, ok, err)
	}
	if got, _ := GetSSOStepupGrant(ctx, db, other.ID); got == nil || got.ConsumedAt != nil || got.Purpose != "llm_org_key" {
		t.Fatalf("other-purpose grant after the sso_write consume = %+v; want live", got)
	}
	// And the reverse: an sso_write grant is not spent by another purpose.
	sso2, _ := IssueSSOStepupGrant(ctx, db, user, org, "google", SSOGrantPurposeSSOWrite, now)
	if id, ok, _ := ConsumeSSOStepupGrant(ctx, db, user, org, "llm_org_key", now); !ok || id != other.ID {
		t.Fatalf("llm_org_key consume = %v %v; want its own grant", id, ok)
	}
	if got, _ := GetSSOStepupGrant(ctx, db, sso2.ID); got.ConsumedAt != nil {
		t.Fatal("an sso_write grant was spent by a write of another purpose")
	}
}
