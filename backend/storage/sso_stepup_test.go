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

	g, err := IssueSSOStepupGrant(ctx, db, user, org, "google", t0)
	if err != nil {
		t.Fatalf("IssueSSOStepupGrant: %v", err)
	}
	if !g.ExpiresAt.Equal(t0.Add(SSOStepupGrantTTL)) {
		t.Fatalf("ExpiresAt = %v, want issue + 10 min", g.ExpiresAt)
	}

	// AC-4b: at T+11 the grant is expired and the attempt leaves it unconsumed.
	if _, ok, err := ConsumeSSOStepupGrant(ctx, db, user, org, t0.Add(11*time.Minute)); err != nil || ok {
		t.Fatalf("consume at T+11 = %v, %v; want no grant", ok, err)
	}
	if got, _ := GetSSOStepupGrant(ctx, db, g.ID); got.ConsumedAt != nil {
		t.Fatal("an expired attempt consumed the grant")
	}

	// AC-4a: at T+9 it is live, and it is consumed exactly once (AC-3a).
	id, ok, err := ConsumeSSOStepupGrant(ctx, db, user, org, t0.Add(9*time.Minute))
	if err != nil || !ok || id != g.ID {
		t.Fatalf("consume at T+9 = %v, %v, %v; want the grant", id, ok, err)
	}
	if _, ok, _ := ConsumeSSOStepupGrant(ctx, db, user, org, t0.Add(9*time.Minute)); ok {
		t.Fatal("a grant was consumed twice")
	}
}

func TestSSOStepupGrant_BoundToUserAndOrg(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	user, org, _ := stepupFixture(t, db, "bound-a")
	other, otherOrg, _ := stepupFixture(t, db, "bound-b")
	now := time.Now().UTC()

	g, err := IssueSSOStepupGrant(ctx, db, user, org, "microsoft", now)
	if err != nil {
		t.Fatal(err)
	}
	// AC-2b: another user of the same org cannot use it.
	if _, ok, _ := ConsumeSSOStepupGrant(ctx, db, other, org, now); ok {
		t.Fatal("another user consumed the grant")
	}
	// AC-2a: the same user, now in another org, cannot use it.
	if _, ok, _ := ConsumeSSOStepupGrant(ctx, db, user, otherOrg, now); ok {
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
	g, err := IssueSSOStepupGrant(ctx, db, user, org, "google", now)
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
			_, ok, err := ConsumeSSOStepupGrant(ctx, db, user, org, now)
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
	if _, ok, _ := ConsumeSSOStepupGrant(ctx, db, user, org, now); ok {
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

	p, err := UpsertSSOProviderRecorded(ctx, db, &SSOProvider{
		Domain: strings.ToUpper(d), ProviderName: "Okta", ProviderType: "oidc", Enabled: true,
	}, user, org, "stepup")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.Domain != d {
		t.Fatalf("stored domain %q, want lower-cased %q", p.Domain, d)
	}
	// AC-20b at this layer: another casing updates the same row.
	p2, err := UpsertSSOProviderRecorded(ctx, db, &SSOProvider{
		Domain: d, ProviderName: "Okta 2", ProviderType: "oidc", Enabled: true,
	}, user, org, "breakglass")
	if err != nil || p2.ID != p.ID || !p2.UpdatedAt.After(p.UpdatedAt) {
		t.Fatalf("update = %+v, %v", p2, err)
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

// AC-20a: migration 029 (029_sso_admin_stepup, spec §9 D-7) lower-cases
// existing rows and makes uniqueness case-insensitive; the down migration
// restores the raw constraint and leaves main's 026 (PAC-23), 027 (PAC-49)
// and 028 (PAC-67, credential_losses) applied.
func TestMigration029_LowerCasesAndUniquesProviderDomains(t *testing.T) {
	db, to := migrationTestDB(t)
	to(28)
	if _, err := db.Exec(`INSERT INTO sso_providers (domain, provider_name, provider_type) VALUES ('ACME.com', 'Okta', 'oidc')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	to(29)
	var got string
	if err := db.QueryRow(`SELECT domain FROM sso_providers`).Scan(&got); err != nil || got != "acme.com" {
		t.Fatalf("domain after 029 = %q, %v; want acme.com", got, err)
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

	// Down to 028, then up again: both directions run.
	to(28)
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
	// 026's and 027's columns and 028's table are still there: 029's down
	// touches none of them.
	if err := db.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE (table_name = 'audit_log' AND column_name = 'user_id') OR (table_name = 'settings' AND column_name = 'auto_schedule_last_run')`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("026/027 columns after 029 down: %d, %v; want 2", n, err)
	}
	var losses bool
	if err := db.QueryRow(`SELECT to_regclass('credential_losses') IS NOT NULL`).Scan(&losses); err != nil || !losses {
		t.Fatalf("028's credential_losses after 029 down: %v, %v; want present", losses, err)
	}
	to(29)
}
