package storage

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// PAC-73 Part 2 storage (spec docs/specs/NEW-llm-bring-your-own-part2.md
// §3.2, §3.4, §7; contract revision 3.1): the merge rules, the one-transaction
// conditional write with the proof, the confirmation and migration 031.

func strp(v string) *string { return &v }
func boolp(v bool) *bool    { return &v }

// The merge rules of OrganizationAIConfigurationUpdate (spec P2-D4, P2-D7,
// P2-D17, revision 2.1 NB1).
func TestOrgAIPatchMerge(t *testing.T) {
	when := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	openai := OrgAIConfig{Provider: "openai", Model: "gpt-x", BaseURL: "https://gw.example/openai", APIKey: "k1",
		PersonalKeysAllowed: false, Version: 4, UpdatedAt: &when}
	for _, tc := range []struct {
		name    string
		stored  OrgAIConfig
		patch   OrgAIPatch
		want    OrgAIConfig
		changed []string
	}{
		{"model only keeps the version", openai, OrgAIPatch{Model: strp("gpt-y")},
			OrgAIConfig{Provider: "openai", Model: "gpt-y", BaseURL: "https://gw.example/openai", APIKey: "k1", Version: 4, UpdatedAt: &when},
			[]string{"model"}},
		{"personal keys only keeps the version", openai, OrgAIPatch{PersonalKeysAllowed: boolp(true)},
			OrgAIConfig{Provider: "openai", Model: "gpt-x", BaseURL: "https://gw.example/openai", APIKey: "k1", PersonalKeysAllowed: true, Version: 4, UpdatedAt: &when},
			[]string{"personalKeysAllowed"}},
		{"the same key again advances (keys are never compared)", openai, OrgAIPatch{APIKey: strp("k1")},
			OrgAIConfig{Provider: "openai", Model: "gpt-x", BaseURL: "https://gw.example/openai", APIKey: "k1", Version: 5, UpdatedAt: &when},
			[]string{"apiKey"}},
		{"a destination change advances", openai, OrgAIPatch{BaseURL: strp("")},
			OrgAIConfig{Provider: "openai", Model: "gpt-x", APIKey: "k1", Version: 5, UpdatedAt: &when},
			[]string{"baseUrl"}},
		{"an unchanged destination does not", openai, OrgAIPatch{BaseURL: strp("https://gw.example/openai")},
			OrgAIConfig{Provider: "openai", Model: "gpt-x", BaseURL: "https://gw.example/openai", APIKey: "k1", Version: 4, UpdatedAt: &when},
			[]string{}},
		{"a provider change replaces everything but personal keys", openai, OrgAIPatch{Provider: strp("bedrock"), APIKey: strp("k2"), AWSRegion: strp("eu-west-1")},
			OrgAIConfig{Provider: "bedrock", APIKey: "k2", AWSRegion: "eu-west-1", Version: 5, UpdatedAt: &when},
			[]string{"provider", "model", "baseUrl", "awsRegion", "apiKey"}},
		{"the removal keeps personal keys and the count", openai, OrgAIPatch{Provider: strp("")},
			OrgAIConfig{Version: 5, UpdatedAt: &when},
			[]string{"provider", "model", "baseUrl", "apiKey"}},
		{"a first setup from nothing", OrgAIConfig{PersonalKeysAllowed: true}, OrgAIPatch{Provider: strp("vertex"), APIKey: strp("k")},
			OrgAIConfig{Provider: "vertex", APIKey: "k", PersonalKeysAllowed: true, Version: 1},
			[]string{"provider", "apiKey"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := tc.patch.Merge(tc.stored)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Merge = %+v\nwant    %+v", got, tc.want)
			}
			if !reflect.DeepEqual(changed, tc.changed) {
				t.Errorf("changed = %v, want %v", changed, tc.changed)
			}
		})
	}
}

func TestOrgAIConfig_Complete(t *testing.T) {
	for _, tc := range []struct {
		c    OrgAIConfig
		want bool
	}{
		{OrgAIConfig{}, false},
		{OrgAIConfig{Provider: "openai"}, false},
		{OrgAIConfig{Provider: "openai", APIKey: "k"}, true},
		{OrgAIConfig{Provider: "azure_openai", APIKey: "k", AzureEndpoint: "https://x"}, false},
		{OrgAIConfig{Provider: "azure_openai", APIKey: "k", AzureEndpoint: "https://x", AzureDeployment: "d"}, true},
		{OrgAIConfig{APIKey: "k"}, false},
	} {
		if got := tc.c.Complete(); got != tc.want {
			t.Errorf("%+v.Complete() = %v, want %v", tc.c, got, tc.want)
		}
	}
	st := OrgAIState{Config: OrgAIConfig{Version: 3}, ConfirmedVersion: 3}
	if st.Confirmed() || st.InOrg() || st.Forbids() {
		t.Errorf("no org: %+v", st)
	}
	if (OrgAIState{Config: OrgAIConfig{Version: 0}, ConfirmedVersion: 0}).Confirmed() {
		t.Error("version 0 with no confirmation reads as confirmed")
	}
}

func orgAIWrite(user, org uuid.UUID, version int64, patch OrgAIPatch, now time.Time) OrgAIWrite {
	return OrgAIWrite{UserID: user, OrgID: org, ExpectedVersion: version, Patch: patch, Now: now}
}

// WriteOrgAIConfig: the grant of its own purpose only, consumed exactly when
// a change is stored, with the operator record in the same transaction
// (P2-AC-2, P2-AC-5, P2-AC-6, P2-AC-40; contract revision 3.1).
func TestWriteOrgAIConfig_ProofVersionAndRecord(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	user, org, _ := stepupFixture(t, db, "orgai")
	t0 := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	setup := OrgAIPatch{Provider: strp("openai"), APIKey: strp("sk-org-secret-1")}

	// No proof: the step-up answer, nothing stored.
	out, err := WriteOrgAIConfig(ctx, db, orgAIWrite(user, org, 0, setup, t0))
	if err != nil || out.Status != OrgAIStepupRequired {
		t.Fatalf("no proof = %+v, %v; want step-up required", out, err)
	}
	// An sso_write grant is never spent here (PAC-50 AC-27).
	sso, _ := IssueSSOStepupGrant(ctx, db, user, org, "google", SSOGrantPurposeSSOWrite, t0)
	if out, _ := WriteOrgAIConfig(ctx, db, orgAIWrite(user, org, 0, setup, t0)); out.Status != OrgAIStepupRequired {
		t.Fatalf("with an sso_write grant = %+v; want step-up required", out)
	}
	if g, _ := GetSSOStepupGrant(ctx, db, sso.ID); g.ConsumedAt != nil {
		t.Fatal("an organisation-AI change spent an sso_write grant")
	}

	// A store failure after the proof rolls everything back (P2-AC-5).
	g1, _ := IssueSSOStepupGrant(ctx, db, user, org, "google", SSOGrantPurposeLLMOrgKeyWrite, t0)
	w := orgAIWrite(user, org, 0, setup, t0)
	w.Hooks = &OrgAIWriteHooks{AfterAuthorize: func() error { return errors.New("injected store failure") }}
	if _, err := WriteOrgAIConfig(ctx, db, w); err == nil {
		t.Fatal("the injected failure was not returned")
	}
	if g, _ := GetSSOStepupGrant(ctx, db, g1.ID); g.ConsumedAt != nil {
		t.Fatal("a failed write spent the grant")
	}
	if c, _ := GetOrgAIConfig(ctx, db, org); c.Version != 0 || c.Provider != "" {
		t.Fatalf("a failed write stored %+v", c)
	}

	// A stale version is a conflict that spends nothing (P2-AC-40).
	if out, err := WriteOrgAIConfig(ctx, db, orgAIWrite(user, org, 3, setup, t0)); err != nil || out.Status != OrgAIConflict || out.Config.Version != 0 {
		t.Fatalf("stale = %+v, %v; want conflict at 0", out, err)
	}
	if g, _ := GetSSOStepupGrant(ctx, db, g1.ID); g.ConsumedAt != nil {
		t.Fatal("a conflict spent the grant")
	}

	// The change: stored, grant spent, recorded (no key).
	out, err = WriteOrgAIConfig(ctx, db, orgAIWrite(user, org, 0, setup, t0.Add(time.Minute)))
	if err != nil || out.Status != OrgAIStored || out.Via != "stepup" || out.Config.Version != 1 {
		t.Fatalf("write = %+v, %v", out, err)
	}
	if g, _ := GetSSOStepupGrant(ctx, db, g1.ID); g.ConsumedAt == nil {
		t.Fatal("a stored change did not spend the grant")
	}
	c, err := GetOrgAIConfig(ctx, db, org)
	if err != nil || c.Provider != "openai" || c.APIKey != "sk-org-secret-1" || c.Version != 1 || c.UpdatedAt == nil || !c.PersonalKeysAllowed {
		t.Fatalf("stored = %+v, %v", c, err)
	}
	// One grant, one change.
	if out, _ := WriteOrgAIConfig(ctx, db, orgAIWrite(user, org, 1, OrgAIPatch{Model: strp("m")}, t0)); out.Status != OrgAIStepupRequired {
		t.Fatalf("second change with the same grant = %+v", out)
	}
	events, err := ListSSOEventsByUser(ctx, db, user)
	if err != nil {
		t.Fatal(err)
	}
	var written []SSOEvent
	for _, e := range events {
		if e.Kind == SSOEventLLMOrgAIWritten {
			written = append(written, e)
		}
		if strings.Contains(string(e.Details), "sk-org-secret") {
			t.Errorf("an operator record carries the key: %s", e.Details)
		}
	}
	if len(written) != 1 {
		t.Fatalf("records = %d, want 1 (failed, conflicting and refused changes record nothing)", len(written))
	}
	var det map[string]any
	_ = json.Unmarshal(written[0].Details, &det)
	if det["via"] != "stepup" || det["org_id"] != org.String() || det["configuration_version"] != float64(1) {
		t.Errorf("record = %s", written[0].Details)
	}
	if ch, _ := det["changed"].([]any); len(ch) != 2 || ch[0] != "provider" || ch[1] != "apiKey" {
		t.Errorf("changed = %v, want [provider apiKey]", det["changed"])
	}
}

// Break-glass: one change per (user, session issue time), atomically, also
// when two arrive at once (P2-AC-2, spec P2-D13; contract revision 3).
func TestWriteOrgAIConfig_BreakGlassOncePerSession(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	user, org, _ := stepupFixture(t, db, "orgai-bg")
	now := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	iat := now.Add(-2 * time.Minute)
	bg := func(version int64, patch OrgAIPatch, iat time.Time) OrgAIWrite {
		w := orgAIWrite(user, org, version, patch, now)
		w.BreakGlass, w.SessionFresh, w.SessionIAT = true, true, iat
		return w
	}
	stale := bg(0, OrgAIPatch{PersonalKeysAllowed: boolp(false)}, iat)
	stale.SessionFresh = false
	if out, _ := WriteOrgAIConfig(ctx, db, stale); out.Status != OrgAIFreshLoginRequired {
		t.Fatalf("stale session = %+v", out)
	}

	// Two concurrent changes with one fresh session: the first holds its
	// transaction after claiming the session until the second has started
	// its own claim; exactly one is stored.
	secondAtCheck := make(chan struct{})
	var once sync.Once
	first := bg(0, OrgAIPatch{PersonalKeysAllowed: boolp(false)}, iat)
	first.Hooks = &OrgAIWriteHooks{AfterAuthorize: func() error {
		select {
		case <-secondAtCheck:
		case <-time.After(5 * time.Second):
		}
		time.Sleep(100 * time.Millisecond) // the second is now blocked on the claim
		return nil
	}}
	second := bg(0, OrgAIPatch{Model: strp("x"), Provider: strp("openai"), APIKey: strp("k")}, iat)
	second.Hooks = &OrgAIWriteHooks{BeforeAuthorize: func() { once.Do(func() { close(secondAtCheck) }) }}
	var wg sync.WaitGroup
	results := make([]OrgAIWriteOutcome, 2)
	errs := make([]error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); results[0], errs[0] = WriteOrgAIConfig(ctx, db, first) }()
	time.Sleep(50 * time.Millisecond)
	go func() { defer wg.Done(); results[1], errs[1] = WriteOrgAIConfig(ctx, db, second) }()
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("errors %v %v", errs[0], errs[1])
	}
	if results[0].Status != OrgAIStored || results[1].Status != OrgAIFreshLoginRequired {
		t.Fatalf("concurrent = %v / %v, want stored / fresh login", results[0].Status, results[1].Status)
	}
	if c, _ := GetOrgAIConfig(ctx, db, org); c.PersonalKeysAllowed || c.Provider != "" {
		t.Fatalf("stored = %+v, want only the first change", c)
	}
	// The same session again, sequentially: refused; a new sign-in: one more.
	if out, _ := WriteOrgAIConfig(ctx, db, bg(0, OrgAIPatch{Model: strp("y")}, iat)); out.Status != OrgAIFreshLoginRequired {
		t.Fatalf("second change, same session = %+v", out)
	}
	if out, _ := WriteOrgAIConfig(ctx, db, bg(0, OrgAIPatch{PersonalKeysAllowed: boolp(true)}, iat.Add(time.Minute))); out.Status != OrgAIStored || out.Via != "breakglass" {
		t.Fatalf("new sign-in = %+v", out)
	}
	// A conflict does not use the session.
	fresh := iat.Add(90 * time.Second)
	if out, _ := WriteOrgAIConfig(ctx, db, bg(7, OrgAIPatch{Model: strp("z")}, fresh)); out.Status != OrgAIConflict {
		t.Fatalf("stale version = %+v", out)
	}
	if out, _ := WriteOrgAIConfig(ctx, db, bg(0, OrgAIPatch{Model: strp("z")}, fresh)); out.Status != OrgAIStored {
		t.Fatalf("after a conflict the session = %+v, want still usable", out)
	}
	events, _ := ListSSOEventsByUser(ctx, db, user)
	n := 0
	for _, e := range events {
		if e.Kind == SSOEventLLMOrgAIWritten {
			n++
			var det map[string]any
			_ = json.Unmarshal(e.Details, &det)
			if det["via"] != "breakglass" || det["session_iat"] == nil {
				t.Errorf("break-glass record = %s", e.Details)
			}
		}
	}
	if n != 3 {
		t.Errorf("break-glass records = %d, want 3", n)
	}
}

// Two first setups naming 0 (contract revision 3.1 NB-C): exactly one is
// stored; a row left at version 0 by a personal-keys-only change is not a
// conflict for a first setup naming 0.
func TestWriteOrgAIConfig_FirstSetupConditionIsTheVersion(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	user, org, _ := stepupFixture(t, db, "orgai-first")
	now := time.Now().UTC()
	bg := func(iat time.Time, version int64, p OrgAIPatch) OrgAIWrite {
		w := orgAIWrite(user, org, version, p, now)
		w.BreakGlass, w.SessionFresh, w.SessionIAT = true, true, iat
		return w
	}
	if out, _ := WriteOrgAIConfig(ctx, db, bg(now.Add(-time.Minute), 0, OrgAIPatch{PersonalKeysAllowed: boolp(false)})); out.Status != OrgAIStored || out.Config.Version != 0 {
		t.Fatalf("personal keys only = %+v", out)
	}
	setup := OrgAIPatch{Provider: strp("anthropic"), APIKey: strp("k")}
	if out, _ := WriteOrgAIConfig(ctx, db, bg(now.Add(-2*time.Minute), 0, setup)); out.Status != OrgAIStored || out.Config.Version != 1 {
		t.Fatalf("first setup after a v0 row = %+v", out)
	}
	if out, _ := WriteOrgAIConfig(ctx, db, bg(now.Add(-3*time.Minute), 0, setup)); out.Status != OrgAIConflict || out.Config.Version != 1 {
		t.Fatalf("a second first setup naming 0 = %+v, want conflict carrying version 1", out)
	}
}

// One consistent read; the confirmation names one version; settings
// choices end it (P2-AC-24, P2-AC-25, P2-AC-36, P2-AC-42).
func TestOrgAIStateAndConfirmation(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	user, org, domain := stepupFixture(t, db, "orgai-confirm")
	other, err := UpsertUser(db, "m3@"+domain, "M3", "", "google", "sub-m3-"+domain)
	if err != nil {
		t.Fatal(err)
	}
	if err := AssociateUserWithOrg(db, other.ID, other.Email); err != nil {
		t.Fatal(err)
	}
	loner, _ := UpsertUser(db, "loner-orgai@gmail.com", "L", "", "google", "sub-loner-orgai")
	st, err := ReadOrgAIState(ctx, db, loner.ID)
	if err != nil || st.InOrg() || st.Config.Version != 0 || !st.Config.PersonalKeysAllowed {
		t.Fatalf("no org = %+v, %v", st, err)
	}
	if st, _ := ReadOrgAIState(ctx, db, uuid.New()); st.InOrg() {
		t.Fatal("an unknown user reads as in an organisation")
	}

	now := time.Now().UTC()
	if s, _, _ := ConfirmOrgAI(ctx, db, user, org, 1, now); s != OrgAIConfirmNotConfigured {
		t.Fatalf("confirm with nothing configured = %v", s)
	}
	_, _ = IssueSSOStepupGrant(ctx, db, user, org, "google", SSOGrantPurposeLLMOrgKeyWrite, now)
	if out, err := WriteOrgAIConfig(ctx, db, orgAIWrite(user, org, 0, OrgAIPatch{Provider: strp("openai"), APIKey: strp("k")}, now)); err != nil || out.Status != OrgAIStored {
		t.Fatalf("setup = %+v, %v", out, err)
	}
	if s, c, _ := ConfirmOrgAI(ctx, db, user, org, 2, now); s != OrgAIConfirmChanged || c.Version != 1 {
		t.Fatalf("stale confirm = %v %+v", s, c)
	}
	for i := 0; i < 2; i++ { // confirming the current version twice
		if s, _, err := ConfirmOrgAI(ctx, db, user, org, 1, now); s != OrgAIConfirmed || err != nil {
			t.Fatalf("confirm #%d = %v, %v", i, s, err)
		}
	}
	st, _ = ReadOrgAIState(ctx, db, user)
	if !st.InOrg() || *st.OrgID != org || !st.Confirmed() || st.Config.APIKey != "k" || !st.Config.Complete() {
		t.Fatalf("state = %+v", st)
	}
	if st3, _ := ReadOrgAIState(ctx, db, other.ID); st3.Confirmed() {
		t.Fatal("one member's confirmation confirmed another")
	}
	if err := DeleteOrgAIConfirmations(ctx, db, user); err != nil {
		t.Fatal(err)
	}
	if st, _ := ReadOrgAIState(ctx, db, user); st.Confirmed() || st.ConfirmedVersion != 0 {
		t.Fatalf("after delete = %+v", st)
	}
	// Removal then the same setup again: a version never had before.
	_, _, _ = ConfirmOrgAI(ctx, db, user, org, 1, now)
	for i, step := range []struct {
		v int64
		p OrgAIPatch
	}{{1, OrgAIPatch{Provider: strp("")}}, {2, OrgAIPatch{Provider: strp("openai"), APIKey: strp("k")}}} {
		_, _ = IssueSSOStepupGrant(ctx, db, user, org, "google", SSOGrantPurposeLLMOrgKeyWrite, now)
		if out, err := WriteOrgAIConfig(ctx, db, orgAIWrite(user, org, step.v, step.p, now)); err != nil || out.Status != OrgAIStored {
			t.Fatalf("step %d = %+v, %v", i, out, err)
		}
	}
	st, _ = ReadOrgAIState(ctx, db, user)
	if st.Config.Version != 3 || st.Confirmed() || st.ConfirmedVersion != 1 {
		t.Fatalf("after removal and the same setup = %+v, want version 3 unconfirmed", st)
	}
}

// Migration 031 up, down, up (spec §7): the down loses the configuration,
// the confirmations, the org-AI records and the new-purpose grants, and
// restores the event-kind CHECK; 030 stays applied.
func TestMigration031_UpDownUp(t *testing.T) {
	db, to := migrationTestDB(t)
	to(31)
	var userID, orgID uuid.UUID
	if err := db.QueryRow(`INSERT INTO users (email, name) VALUES ('m31@acme31.com', 'M') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO organizations (name, domain) VALUES ('Acme', 'acme31.com') RETURNING id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO org_ai_configurations (org_id, provider, api_key, configuration_version, updated_by) VALUES ($2, 'openai', 'k', 1, $1)`,
		`INSERT INTO org_ai_confirmations (user_id, org_id, configuration_version) VALUES ($1, $2, 1)`,
		`INSERT INTO sso_events (user_id, org_id, kind, details) VALUES ($1, $2, 'llm_org_ai_written', '{"via":"breakglass","session_iat":5}')`,
		`INSERT INTO sso_events (user_id, org_id, kind, details) VALUES ($1, $2, 'sso_stepup', '{}')`,
		`INSERT INTO sso_stepup_grants (user_id, org_id, provider, purpose, issued_at, expires_at) VALUES ($1, $2, 'google', 'llm_org_key_write', NOW(), NOW() + interval '1 minute')`,
		`INSERT INTO sso_stepup_grants (user_id, org_id, provider, purpose, issued_at, expires_at) VALUES ($1, $2, 'google', 'sso_write', NOW(), NOW() + interval '1 minute')`,
	} {
		if _, err := db.Exec(q, userID, orgID); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// The once-per-session index.
	if _, err := db.Exec(`INSERT INTO sso_events (user_id, org_id, kind, details) VALUES ($1, $2, 'llm_org_ai_written', '{"via":"breakglass","session_iat":5}')`, userID, orgID); err == nil {
		t.Fatal("a second break-glass record for one session was accepted")
	}
	if _, err := db.Exec(`INSERT INTO org_ai_confirmations (user_id, org_id, configuration_version) VALUES ($1, $2, 0)`, userID, orgID); err == nil {
		t.Fatal("a confirmation of version 0 was accepted")
	}
	to(30)
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM information_schema.tables WHERE table_name IN ('org_ai_configurations','org_ai_confirmations')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("tables after down: %d, %v", n, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM sso_events`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("events after down: %d, %v; want the sso_stepup row only", n, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM sso_stepup_grants`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("grants after down: %d, %v; want the sso_write grant only", n, err)
	}
	if _, err := db.Exec(`INSERT INTO sso_events (user_id, org_id, kind, details) VALUES ($1, $2, 'llm_org_ai_written', '{}')`, userID, orgID); err == nil {
		t.Fatal("the event-kind CHECK was not restored")
	}
	to(31)
	if err := db.QueryRow(`SELECT count(*) FROM org_ai_configurations`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("re-upgrade: %d, %v", n, err)
	}
}
