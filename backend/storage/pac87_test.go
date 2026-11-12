package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// PAC-87 (spec docs/specs/PAC-87.md; contract contracts/features/PAC-87.md):
// consent periods, the operator record's consent fields, the settings write
// ordered against a confirmation, leaving an organisation, and migration 036.

// pac87Org is an organisation with an admin holding a fresh grant per change.
type pac87Org struct {
	t     *testing.T
	db    *sql.DB
	admin uuid.UUID
	org   uuid.UUID
	dom   string
}

func newPAC87Org(t *testing.T, db *sql.DB, label string) *pac87Org {
	t.Helper()
	admin, org, dom := stepupFixture(t, db, label)
	return &pac87Org{t: t, db: db, admin: admin, org: org, dom: dom}
}

// change applies patch as a proven administrator and returns the stored
// configuration.
func (o *pac87Org) change(patch OrgAIPatch) OrgAIConfig {
	o.t.Helper()
	ctx := context.Background()
	cur, err := GetOrgAIConfig(ctx, o.db, o.org)
	if err != nil {
		o.t.Fatal(err)
	}
	if _, err := IssueSSOStepupGrant(ctx, o.db, o.admin, o.org, "google", SSOGrantPurposeLLMOrgKeyWrite, time.Now()); err != nil {
		o.t.Fatal(err)
	}
	out, err := WriteOrgAIConfig(ctx, o.db, orgAIWrite(o.admin, o.org, cur.Version, patch, time.Now()))
	if err != nil || out.Status != OrgAIStored {
		o.t.Fatalf("change %+v = %+v, %v", patch, out, err)
	}
	return out.Config
}

// member adds a member to the organisation.
func (o *pac87Org) member(label string) uuid.UUID {
	o.t.Helper()
	u, err := UpsertUser(o.db, label+"@"+o.dom, label, "", "google", "sub-"+label+"-"+o.dom)
	if err != nil {
		o.t.Fatal(err)
	}
	if err := SetUserOrg(o.db, u.ID, o.org); err != nil {
		o.t.Fatal(err)
	}
	return u.ID
}

func (o *pac87Org) confirm(user uuid.UUID, version int64) OrgAIConfirmStatus {
	o.t.Helper()
	s, _, err := ConfirmOrgAI(context.Background(), o.db, user, o.org, version, time.Now())
	if err != nil {
		o.t.Fatal(err)
	}
	return s
}

func (o *pac87Org) confirmed(user uuid.UUID) bool {
	o.t.Helper()
	st, err := ReadOrgAIState(context.Background(), o.db, user)
	if err != nil {
		o.t.Fatal(err)
	}
	return st.Confirmed()
}

// PAC87-AC-1 to -5 at the storage level: the confirmation covers the
// provider for one consent period, accepted when the version shown lies in
// it; stored is the period, never the version.
func TestPAC87_ConsentPeriod(t *testing.T) {
	db := openTestDB(t)
	o := newPAC87Org(t, db, "pac87-period")
	m := o.member("m")
	if s := o.confirm(m, 1); s != OrgAIConfirmNotConfigured {
		t.Fatalf("nothing configured: %v", s)
	}
	v1 := o.change(OrgAIPatch{Provider: strp("openai"), APIKey: strp("k1")})
	if v1.Version != 1 || v1.ConsentSince != 1 {
		t.Fatalf("setup = %+v", v1)
	}
	// AC-5: shown 1, key rotated (2) and the gateway set (3), then confirmed:
	// accepted, the period (1) stored.
	o.change(OrgAIPatch{APIKey: strp("k1")}) // re-entering the same key
	v3 := o.change(OrgAIPatch{BaseURL: strp("https://gw.example/v1")})
	if v3.Version != 3 || v3.ConsentSince != 1 {
		t.Fatalf("after rotation and destination = %+v, want version 3 in period 1", v3)
	}
	if s := o.confirm(m, 1); s != OrgAIConfirmed {
		t.Fatalf("confirm of the shown version after a rotation = %v", s)
	}
	var stored int64
	if err := db.QueryRow(`SELECT consent_since_version FROM org_ai_confirmations WHERE user_id = $1`, m).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("stored = %d, %v; want the period 1", stored, err)
	}
	// AC-1, AC-2, AC-4: rotation, destination, model and personal-keys
	// changes keep the confirmation.
	o.change(OrgAIPatch{APIKey: strp("k2")})
	o.change(OrgAIPatch{BaseURL: strp("")})
	o.change(OrgAIPatch{Model: strp("gpt-x")})
	o.change(OrgAIPatch{PersonalKeysAllowed: boolp(false)})
	if !o.confirmed(m) {
		t.Fatal("a key, destination, model or personal-keys change ended the confirmation")
	}
	// A version above the current one is refused.
	cur, _ := GetOrgAIConfig(context.Background(), db, o.org)
	if s := o.confirm(m, cur.Version+1); s != OrgAIConfirmChanged {
		t.Fatalf("future version = %v", s)
	}
	// AC-3: P -> Q asks again; the versions of P's period are refused.
	shown := cur.Version
	q := o.change(OrgAIPatch{Provider: strp("anthropic"), APIKey: strp("k3")})
	if q.ConsentSince != q.Version || o.confirmed(m) {
		t.Fatalf("after P -> Q: %+v, confirmed %v", q, o.confirmed(m))
	}
	if s := o.confirm(m, shown); s != OrgAIConfirmChanged {
		t.Fatalf("confirm of P's version after P -> Q = %v", s)
	}
	// P -> Q -> P: shown Q's version, then back to P: refused, asked again.
	p := o.change(OrgAIPatch{Provider: strp("openai"), APIKey: strp("k1")})
	if s := o.confirm(m, q.Version); s != OrgAIConfirmChanged || o.confirmed(m) {
		t.Fatalf("P -> Q -> P: confirm of Q's version = %v", s)
	}
	if s := o.confirm(m, p.Version); s != OrgAIConfirmed || !o.confirmed(m) {
		t.Fatalf("confirm of the current period = %v", s)
	}
	// AC-4: removal then set up again with P: asked again, and the
	// removal's own version is not a period one can confirm.
	removed := o.change(OrgAIPatch{Provider: strp("")})
	if s := o.confirm(m, removed.Version); s != OrgAIConfirmNotConfigured {
		t.Fatalf("confirm while removed = %v", s)
	}
	again := o.change(OrgAIPatch{Provider: strp("openai"), APIKey: strp("k1")})
	if o.confirmed(m) || again.ConsentSince != again.Version {
		t.Fatalf("after removal and the same setup: confirmed %v, %+v", o.confirmed(m), again)
	}
	if s := o.confirm(m, removed.Version); s != OrgAIConfirmChanged {
		t.Fatalf("confirm of the removal's version after the new setup = %v", s)
	}
	// The CHECK holds the period at or below the version.
	if _, err := db.Exec(`UPDATE org_ai_configurations SET consent_since_version = configuration_version + 1 WHERE org_id = $1`, o.org); err == nil {
		t.Fatal("a consent period above the version was accepted")
	}
}

// PAC87-AC-7 (P87-D7): the operator record says whether a change starts a
// consent period, and where it starts; never a key.
func TestPAC87_OperatorRecordConsentFields(t *testing.T) {
	db := openTestDB(t)
	o := newPAC87Org(t, db, "pac87-record")
	o.change(OrgAIPatch{Provider: strp("openai"), APIKey: strp("sk-pac87-rec-1")})    // v1 started
	o.change(OrgAIPatch{APIKey: strp("sk-pac87-rec-2")})                              // v2 not
	o.change(OrgAIPatch{BaseURL: strp("https://gw.example/v1")})                      // v3 not
	o.change(OrgAIPatch{PersonalKeysAllowed: boolp(false)})                           // v3 not
	o.change(OrgAIPatch{Provider: strp("anthropic"), APIKey: strp("sk-pac87-rec-3")}) // v4 started
	o.change(OrgAIPatch{Provider: strp("")})                                          // v5 started
	rows, err := db.Query(`SELECT details FROM sso_events WHERE org_id = $1 AND kind = $2 ORDER BY created_at, id`, o.org, SSOEventLLMOrgAIWritten)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(raw, "sk-pac87-rec") {
			t.Errorf("an operator record carries a key: %s", raw)
		}
		var d struct {
			Version int64  `json:"configuration_version"`
			Started *bool  `json:"consent_period_started"`
			Since   *int64 `json:"consent_since_version"`
		}
		if err := json.Unmarshal([]byte(raw), &d); err != nil || d.Started == nil || d.Since == nil {
			t.Fatalf("record %s lacks the consent fields", raw)
		}
		got = append(got, fmt.Sprintf("v%d:%v:%d", d.Version, *d.Started, *d.Since))
	}
	want := "v1:true:1 v2:false:1 v3:false:1 v3:false:1 v4:true:4 v5:true:5"
	if strings.Join(got, " ") != want {
		t.Fatalf("records = %v, want %s", got, want)
	}
}

// PAC87-AC-12, -13 (P87-D3) at the storage level: a settings write ends the
// confirmation only when it changes the stored choice to off or to another
// provider.
func TestPAC87_SettingsWriteEndsOnlyAChange(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	o := newPAC87Org(t, db, "pac87-settings")
	o.change(OrgAIPatch{Provider: strp("openai"), APIKey: strp("k")})
	m := o.member("m")
	save := func(choice string, key string) bool {
		t.Helper()
		s, err := GetOrCreateSettingsByUser(db, m)
		if err != nil {
			t.Fatal(err)
		}
		s.LLMProvider = choice
		if key != "" {
			s.LLMAPIKeys.Set(choice, key)
			s.MarkSecretReplaced(LLMKeyColumn(choice))
		}
		ended, err := SaveSettingsEndingOrgAIConfirmation(ctx, db, m, s, nil)
		if err != nil {
			t.Fatal(err)
		}
		return ended
	}
	o.confirm(m, 1)
	if save("organization", "") || !o.confirmed(m) {
		t.Fatal("storing organization again ended the confirmation")
	}
	if !save("openai", "sk-own") || o.confirmed(m) {
		t.Fatal("organization -> openai did not end the confirmation")
	}
	o.confirm(m, 1) // row 10 -> row 9 is a confirmation without a settings write
	for _, key := range []string{"", "sk-own-2"} {
		if save("openai", key) || !o.confirmed(m) {
			t.Fatalf("storing openai unchanged (key %q) ended the confirmation", key)
		}
	}
	if !save("anthropic", "") || o.confirmed(m) {
		t.Fatal("openai -> anthropic did not end the confirmation")
	}
	o.confirm(m, 1)
	if !save("", "") || o.confirmed(m) {
		t.Fatal("anthropic -> off did not end the confirmation")
	}
	o.confirm(m, 1)
	if save("", "") || !o.confirmed(m) {
		t.Fatal("off -> off ended the confirmation")
	}
	// A removed provider stays stored as itself; a save keeping it ends
	// nothing.
	if _, err := db.Exec(`UPDATE settings SET llm_provider = 'ollama' WHERE user_id = $1`, m); err != nil {
		t.Fatal(err)
	}
	o.confirm(m, 1)
	s, _ := GetOrCreateSettingsByUser(db, m)
	if ended, err := SaveSettingsEndingOrgAIConfirmation(ctx, db, m, s, nil); err != nil || ended || !o.confirmed(m) {
		t.Fatalf("keeping ollama: ended %v, %v", ended, err)
	}
	var stored string
	_ = db.QueryRow(`SELECT llm_provider FROM settings WHERE user_id = $1`, m).Scan(&stored)
	if stored != "ollama" {
		t.Fatalf("stored %q, want ollama kept", stored)
	}
}

// waitBlockedOrDone waits until done is closed or a backend of this
// database waits on a lock.
func waitBlockedOrDone(t *testing.T, db *sql.DB, done <-chan struct{}) (blocked bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			return false
		default:
		}
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the concurrent confirmation neither finished nor waited on a lock")
	return false
}

// PAC87-AC-23 (contract §5a): M's own confirmation applied, concurrently, at
// each storage access of M's settings write OpenAI -> Anthropic. In no run
// is Anthropic stored with a live confirmation recorded before the change;
// a live confirmation exists exactly when the confirmation committed after
// the write; after a forbid, M is unconfirmed (row 10) in every run where M
// did not confirm after the change.
func TestPAC87_SettingsWriteOrderedAgainstConfirmation(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for step := SettingsWriteLock; step <= SettingsWriteDone; step++ {
		t.Run(fmt.Sprintf("step%d", step), func(t *testing.T) {
			o := newPAC87Org(t, db, fmt.Sprintf("pac87-ac23-%d", step))
			cfg := o.change(OrgAIPatch{Provider: strp("openai"), APIKey: strp("k")})
			m := o.member("m")
			o.confirm(m, cfg.Version) // confirmed while following
			s, _ := GetOrCreateSettingsByUser(db, m)
			s.LLMProvider = "openai"
			s.LLMAPIKeys.Set("openai", "sk-own")
			s.MarkSecretReplaced(LLMKeyColumn("openai"))
			if _, err := SaveSettingsEndingOrgAIConfirmation(ctx, db, m, s, nil); err != nil {
				t.Fatal(err)
			}
			if o.confirmed(m) {
				t.Fatal("choosing OpenAI kept the confirmation")
			}

			s, _ = GetOrCreateSettingsByUser(db, m)
			s.LLMProvider = "anthropic"
			var (
				once        sync.Once
				done        = make(chan struct{})
				confirmedAt time.Time
				committedAt time.Time
				status      OrgAIConfirmStatus
				confirmErr  error
			)
			hook := func(at SettingsWriteStep) {
				if at == SettingsWriteDone {
					committedAt = time.Now()
				}
				if at != step {
					return
				}
				once.Do(func() {
					go func() {
						defer close(done)
						status, _, confirmErr = ConfirmOrgAI(context.Background(), db, m, o.org, cfg.Version, time.Now())
						confirmedAt = time.Now()
					}()
					waitBlockedOrDone(t, db, done)
				})
			}
			if _, err := SaveSettingsEndingOrgAIConfirmation(ctx, db, m, s, hook); err != nil {
				t.Fatal(err)
			}
			<-done
			if confirmErr != nil || status != OrgAIConfirmed {
				t.Fatalf("confirmation = %v, %v", status, confirmErr)
			}
			var stored string
			_ = db.QueryRow(`SELECT llm_provider FROM settings WHERE user_id = $1`, m).Scan(&stored)
			if stored != "anthropic" {
				t.Fatalf("stored %q", stored)
			}
			live := o.confirmed(m)
			after := confirmedAt.After(committedAt)
			if live != after {
				t.Fatalf("live confirmation %v, confirmation committed after the change %v: a confirmation recorded before the change survived it, or one after it was lost", live, after)
			}
			if wantLive := step != SettingsWriteLock; live != wantLive {
				t.Fatalf("live = %v, want %v (the users-row lock orders the confirmation after the write from the lock on)", live, wantLive)
			}
			o.change(OrgAIPatch{PersonalKeysAllowed: boolp(false)})
			if o.confirmed(m) != live {
				t.Fatal("after the forbid the confirmation state changed")
			}
		})
	}
}

// Factory decision 2026-11-12: leaving an organisation ends the member's
// confirmation for it, so rejoining asks again; a confirmation racing the
// move records nothing for the organisation left.
func TestPAC87_LeavingTheOrganisationEndsTheConfirmation(t *testing.T) {
	db := openTestDB(t)
	a := newPAC87Org(t, db, "pac87-leave-a")
	b := newPAC87Org(t, db, "pac87-leave-b")
	a.change(OrgAIPatch{Provider: strp("openai"), APIKey: strp("k")})
	m := a.member("m")
	if a.confirm(m, 1) != OrgAIConfirmed || !a.confirmed(m) {
		t.Fatal("setup")
	}
	// Signing in again into the same organisation keeps it.
	if err := SetUserOrg(db, m, a.org); err != nil || !a.confirmed(m) {
		t.Fatalf("same organisation again ended the confirmation (%v)", err)
	}
	if err := SetUserOrg(db, m, b.org); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.QueryRow(`SELECT count(*) FROM org_ai_confirmations WHERE user_id = $1`, m).Scan(&n)
	if n != 0 {
		t.Fatalf("%d confirmations left after leaving", n)
	}
	// A confirmation for the organisation left records nothing.
	if s := a.confirm(m, 1); s != OrgAIConfirmNotMember {
		t.Fatalf("confirm after leaving = %v", s)
	}
	if err := SetUserOrg(db, m, a.org); err != nil {
		t.Fatal(err)
	}
	if a.confirmed(m) {
		t.Fatal("rejoining restored the old confirmation")
	}
	if a.confirm(m, 1) != OrgAIConfirmed || !a.confirmed(m) {
		t.Fatal("confirming after rejoining")
	}
}

// Review finding 3: a confirmation of A kept from before PAC-87 (when a move
// to B kept the row) never confirms a later rejoin of A. The pre-deploy move
// is reproduced by updating users.org_id directly, as the old SetUserOrg
// did, and the member's confirmation for A is inserted as that code left it.
// Signing in back to A asks again; a confirmation then is recorded anew.
func TestPAC87_RejoinWithAPreDeployConfirmationAsksAgain(t *testing.T) {
	db := openTestDB(t)
	a := newPAC87Org(t, db, "pac87-rejoin-a")
	b := newPAC87Org(t, db, "pac87-rejoin-b")
	cfg := a.change(OrgAIPatch{Provider: strp("openai"), APIKey: strp("k")})
	m := a.member("m")
	if a.confirm(m, cfg.Version) != OrgAIConfirmed || !a.confirmed(m) {
		t.Fatal("setup: confirm in A")
	}
	// Before the deploy: moved to B by the old code, A's row kept.
	if _, err := db.Exec(`UPDATE users SET org_id = $1 WHERE id = $2`, b.org, m); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM org_ai_confirmations WHERE user_id = $1`, m); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO org_ai_confirmations (user_id, org_id, consent_since_version) VALUES ($1, $2, $3)`, m, a.org, cfg.ConsentSince); err != nil {
		t.Fatal(err)
	}
	// After the deploy: the first sign-in puts the member back in A.
	if err := SetUserOrg(db, m, a.org); err != nil {
		t.Fatal(err)
	}
	if a.confirmed(m) {
		t.Fatal("the confirmation kept from before the move confirmed the rejoin: the member must be asked again")
	}
	var n int
	_ = db.QueryRow(`SELECT count(*) FROM org_ai_confirmations WHERE user_id = $1`, m).Scan(&n)
	if n != 0 {
		t.Fatalf("%d confirmations left after rejoining", n)
	}
	if a.confirm(m, cfg.Version) != OrgAIConfirmed || !a.confirmed(m) {
		t.Fatal("confirming after the rejoin")
	}
	// Signing in again into the same organisation keeps the new one; a
	// stray row for another organisation goes.
	if _, err := db.Exec(`INSERT INTO org_ai_confirmations (user_id, org_id, consent_since_version) VALUES ($1, $2, 1)`, m, b.org); err != nil {
		t.Fatal(err)
	}
	if err := SetUserOrg(db, m, a.org); err != nil || !a.confirmed(m) {
		t.Fatalf("same organisation again: confirmed %v, %v", a.confirmed(m), err)
	}
	_ = db.QueryRow(`SELECT count(*) FROM org_ai_confirmations WHERE user_id = $1 AND org_id = $2`, m, b.org).Scan(&n)
	if n != 0 {
		t.Fatal("a confirmation for another organisation survived a sign-in")
	}
	// An unknown user is a no-op, as before.
	if err := SetUserOrg(db, uuid.New(), a.org); err != nil {
		t.Fatalf("unknown user: %v", err)
	}
}

// pac87Seed is AC-18's population before migration 036: A at version 7 on
// OpenAI forbidding personal keys; X follower confirmed 7, Y follower
// confirmed 5, Z on OpenAI with a key holding 7.
type pac87Seed struct {
	org, admin, x, y, z uuid.UUID
}

func seedPAC87(t *testing.T, db *sql.DB) pac87Seed {
	t.Helper()
	var s pac87Seed
	if err := db.QueryRow(`INSERT INTO organizations (name, domain) VALUES ('A', 'a87.example') RETURNING id`).Scan(&s.org); err != nil {
		t.Fatal(err)
	}
	user := func(email string) uuid.UUID {
		var id uuid.UUID
		if err := db.QueryRow(`INSERT INTO users (email, name, org_id) VALUES ($1, $1, $2) RETURNING id`, email, s.org).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	s.admin, s.x, s.y, s.z = user("admin@a87.example"), user("x@a87.example"), user("y@a87.example"), user("z@a87.example")
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO org_ai_configurations (org_id, provider, api_key, personal_keys_allowed, configuration_version, updated_by) VALUES ($1, 'openai', 'sk-a87', false, 7, $2)`, []any{s.org, s.admin}},
		{`INSERT INTO settings (user_id, llm_provider) VALUES ($1, 'organization')`, []any{s.x}},
		{`INSERT INTO settings (user_id, llm_provider, llm_api_key_openai, llm_model) VALUES ($1, 'openai', 'sk-z-own', 'gpt-z')`, []any{s.z}},
		{`INSERT INTO org_ai_confirmations (user_id, org_id, configuration_version) VALUES ($2, $1, 7), ($3, $1, 5), ($4, $1, 7)`, []any{s.org, s.x, s.y, s.z}},
	} {
		if _, err := db.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}
	return s
}

// settingsFacts is every stored choice and key, to compare across a
// migration.
func settingsFacts(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`SELECT user_id, llm_provider, llm_model, COALESCE(llm_api_key_openai,''), COALESCE(llm_api_key,'') FROM settings ORDER BY user_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var u, p, m, k1, k2 string
		if err := rows.Scan(&u, &p, &m, &k1, &k2); err != nil {
			t.Fatal(err)
		}
		out = append(out, strings.Join([]string{u, p, m, k1, k2}, "|"))
	}
	return strings.Join(out, "\n")
}

func constraintExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM pg_constraint WHERE conname = $1`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

// PAC87-AC-18, -19: migration 036 up keeps followers confirmed at the
// current version, ends older confirmations and those of members not
// following; down keeps only followers confirmed at exactly the current
// state; neither touches a stored choice or key; up, down, up.
func TestMigration036_ConsentPeriod(t *testing.T) {
	db, to := migrationTestDB(t)
	to(34)
	s := seedPAC87(t, db)
	before := settingsFacts(t, db)
	to(36)
	if !constraintExists(t, db, "org_ai_confirmations_consent_since_version_check") || constraintExists(t, db, "org_ai_confirmations_configuration_version_check") ||
		!constraintExists(t, db, "org_ai_configurations_consent_since_check") {
		t.Fatal("constraint names after up")
	}
	if after := settingsFacts(t, db); after != before {
		t.Fatalf("up changed stored choices or keys:\n%s\nvs\n%s", before, after)
	}
	ctx := context.Background()
	state := func(u uuid.UUID) OrgAIState {
		t.Helper()
		st, err := ReadOrgAIState(ctx, db, u)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	if st := state(s.x); !st.Confirmed() || st.Config.ConsentSince != 7 {
		t.Fatalf("X after up = %+v, want confirmed in period 7", st)
	}
	if st := state(s.y); st.Confirmed() || st.ConfirmedSince != 5 {
		t.Fatalf("Y after up = %+v, want row 5", st)
	}
	if st := state(s.z); st.Confirmed() || st.ConfirmedSince != 0 {
		t.Fatalf("Z after up = %+v, want no confirmation (row 10)", st)
	}
	// A later key rotation keeps X; a later provider change matches no
	// confirmation issued before.
	o := &pac87Org{t: t, db: db, admin: s.admin, org: s.org}
	rotated := o.change(OrgAIPatch{APIKey: strp("sk-a87-2")})
	if !state(s.x).Confirmed() {
		t.Fatal("X after a key rotation")
	}

	// AC-19 population: M = X (confirmed at S1, key rotated to S2); M2 = Z
	// confirmed (row 9); N confirmed at S2 itself.
	if o.confirm(s.z, rotated.Version) != OrgAIConfirmed || !state(s.z).Confirmed() {
		t.Fatal("Z confirming (row 9)")
	}
	var n uuid.UUID
	if err := db.QueryRow(`INSERT INTO users (email, name, org_id) VALUES ('n@a87.example', 'N', $1) RETURNING id`, s.org).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if o.confirm(n, rotated.Version) != OrgAIConfirmed {
		t.Fatal("N confirming")
	}
	mid := settingsFacts(t, db)
	to(34)
	if after := settingsFacts(t, db); after != mid {
		t.Fatal("down changed stored choices or keys")
	}
	if !constraintExists(t, db, "org_ai_confirmations_configuration_version_check") || constraintExists(t, db, "org_ai_configurations_consent_since_check") {
		t.Fatal("constraint names after down")
	}
	// Under the reverted code a member is confirmed when the stored value
	// equals the current version (8): nobody here was recorded at exactly
	// the current state's period start, so every confirmation is ended or
	// does not match.
	var current int64
	_ = db.QueryRow(`SELECT configuration_version FROM org_ai_configurations WHERE org_id = $1`, s.org).Scan(&current)
	rows, err := db.Query(`SELECT user_id, configuration_version FROM org_ai_confirmations`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var u uuid.UUID
		var v int64
		_ = rows.Scan(&u, &v)
		if u == s.x || u == s.z {
			t.Errorf("down kept %v's confirmation (%d)", u, v)
		}
		if u == n && v != current {
			t.Errorf("down kept N at %d, not the current state %d", v, current)
		}
	}
	rows.Close()
	to(36)
	if state(s.x).Confirmed() || state(s.z).Confirmed() {
		t.Fatal("a confirmation ended by down came back with up")
	}
	// After a provider change no confirmation issued before matches.
	o.change(OrgAIPatch{Provider: strp("anthropic"), APIKey: strp("k")})
	for _, u := range []uuid.UUID{s.x, s.y, s.z, n} {
		if state(u).Confirmed() {
			t.Errorf("%v confirmed after a provider change", u)
		}
	}
}
