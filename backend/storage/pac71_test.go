package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// PAC-71 storage: emptiness (rule (b), contract §6), session generation
// primitives, sign-in resolution edges, the operator procedure (AC-44,
// AC-49) and migration 032 (AC-18).

func pac71User(t *testing.T, db *sql.DB, email, providerID string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := db.QueryRow(`INSERT INTO users (email, name, provider_id) VALUES ($1, 'U', $2) RETURNING id`, email, providerID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func TestPAC71_FreshAccountAndFreshSettingsRowAreEmpty(t *testing.T) {
	db := openTestDB(t)
	u := pac71User(t, db, "fresh@example.com", "")
	if k, err := FirstNonEmptyKind(db, u); err != nil || k != "" {
		t.Fatalf("no settings row: %q %v", k, err)
	}
	// The row the code creates on first read holds every default (contract §6).
	if _, err := GetOrCreateSettingsByUser(db, u); err != nil {
		t.Fatal(err)
	}
	if k, err := FirstNonEmptyKind(db, u); err != nil || k != "" {
		t.Fatalf("fresh settings row is not empty: %q %v", k, err)
	}
	// A save that only normalises the schedules changes nothing in meaning.
	s, _ := GetOrCreateSettingsByUser(db, u)
	if err := SaveSettingsForUser(db, u, s); err != nil {
		t.Fatal(err)
	}
	if k, err := FirstNonEmptyKind(db, u); err != nil || k != "" {
		t.Fatalf("a normalising save made the row non-empty: %q %v", k, err)
	}
	// A user_profiles row at its defaults says nothing about use.
	mustExec(t, db, `INSERT INTO user_profiles (user_id) VALUES ($1)`, u)
	if k, _ := FirstNonEmptyKind(db, u); k != "" {
		t.Fatalf("default profile counted: %q", k)
	}
}

// CONTRACT-5: the defaults table and the settings table name the same columns.
func TestPAC71_CONTRACT5_EverySettingsColumnIsClassified(t *testing.T) {
	db := openTestDB(t)
	rows, err := db.Query(`SELECT column_name FROM information_schema.columns WHERE table_name = 'settings' AND table_schema = current_schema()`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	classified := map[string]bool{}
	for _, c := range SettingsBookkeeping {
		classified[c] = true
	}
	for _, c := range settingsMustBeEmpty {
		classified[c.column] = true
	}
	for c := range SettingsDefaults {
		classified[c] = true
	}
	seen := map[string]bool{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		seen[c] = true
		if !classified[c] {
			t.Errorf("settings.%s is not classified for rule (b) (contract §6)", c)
		}
	}
	for c := range classified {
		if !seen[c] {
			t.Errorf("classified column %s does not exist", c)
		}
	}
}

func TestPAC71_EveryUserForeignKeyIsClassified(t *testing.T) {
	db := openTestDB(t)
	refs, err := UserForeignKeys(db)
	if err != nil {
		t.Fatal(err)
	}
	live := map[string]bool{}
	for _, r := range refs {
		live[r] = true
		if !classifiedUserRefs[r] {
			t.Errorf("foreign key %s to users is not classified for rule (b)", r)
		}
	}
	for r := range classifiedUserRefs {
		if !live[r] {
			t.Errorf("classified %s is not a foreign key to users", r)
		}
	}
}

func TestPAC71_UnclassifiedKindCountsAsNonEmpty(t *testing.T) {
	db := openTestDB(t)
	u := pac71User(t, db, "later@example.com", "")
	mustExec(t, db, `CREATE TABLE pac71_later_feature (owner UUID REFERENCES users(id))`)
	mustExec(t, db, `INSERT INTO pac71_later_feature VALUES ($1)`, u)
	if k, err := FirstNonEmptyKind(db, u); err != nil || k != "unclassified:pac71_later_feature.owner" {
		t.Fatalf("= %q %v", k, err)
	}
}

// Every preference changed from its default makes the account non-empty
// (AC-41 "one preference changed", each column at least once).
func TestPAC71_EveryChangedPreferenceCounts(t *testing.T) {
	db := openTestDB(t)
	changed := func(col string, def any) string {
		switch col {
		case "working_hours":
			return `'{"mode":"all_days","default":{"enabled":true,"start":"08:00","end":"18:00"},"days":{}}'::jsonb`
		case "lunch_breaks":
			return `'{"monday":{"enabled":false,"start":"","end":""}}'::jsonb`
		case "recap_send_time":
			return `'09:30'`
		case "recap_send_to":
			return `'channel'`
		case "time_format":
			return `'24h'`
		}
		switch d := def.(type) {
		case bool:
			return fmt.Sprint(!d)
		case int:
			return fmt.Sprint(d + 7)
		default:
			return `'changed-` + col + `'`
		}
	}
	cols := make([]string, 0, len(SettingsDefaults))
	for c := range SettingsDefaults {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	for _, col := range cols {
		t.Run(col, func(t *testing.T) {
			u := pac71User(t, db, col+"@example.com", "")
			mustExec(t, db, `INSERT INTO settings (user_id) VALUES ($1)`, u)
			if _, err := db.Exec(`UPDATE settings SET `+col+` = `+changed(col, SettingsDefaults[col])+` WHERE user_id = $1`, u); err != nil {
				t.Fatalf("set %s: %v", col, err)
			}
			if k, err := FirstNonEmptyKind(db, u); err != nil || k == "" {
				t.Fatalf("changed %s not counted: %q %v", col, k, err)
			}
		})
	}
	for _, c := range settingsMustBeEmpty {
		t.Run(c.column, func(t *testing.T) {
			u := pac71User(t, db, "e-"+c.column+"@example.com", "")
			mustExec(t, db, `INSERT INTO settings (user_id) VALUES ($1)`, u)
			mustExec(t, db, `UPDATE settings SET `+c.column+` = 'x' WHERE user_id = $1`, u)
			if k, _ := FirstNonEmptyKind(db, u); k != c.kind {
				t.Fatalf("= %q, want %q", k, c.kind)
			}
		})
	}
	t.Run("pre-029 llm_provider counts as changed", func(t *testing.T) {
		u := pac71User(t, db, "pre029@example.com", "")
		mustExec(t, db, `INSERT INTO settings (user_id, llm_provider) VALUES ($1, '')`, u)
		if k, _ := FirstNonEmptyKind(db, u); k != "ai_destination" {
			t.Fatalf("= %q", k)
		}
	})
}

func TestPAC71_AddressResolution(t *testing.T) {
	db := openTestDB(t)
	confirmed := pac71User(t, db, "Henry@Acme.test", "")
	mustExec(t, db, `UPDATE users SET address_standing = 'domain_confirmed' WHERE id = $1`, confirmed)
	unconfirmed := pac71User(t, db, "pat@acme.test", "")
	a := pac71User(t, db, "x@acme.test", "")
	b := pac71User(t, db, "X@acme.test", "")
	mustExec(t, db, `UPDATE users SET address_standing = 'domain_confirmed' WHERE id IN ($1, $2)`, a, b)

	if id, ok, _ := ConfirmedHolder(db, "henry@ACME.test"); !ok || id != confirmed {
		t.Fatal("confirmed holder not resolved ignoring case")
	}
	if _, ok, _ := ConfirmedHolder(db, "pat@acme.test"); ok {
		t.Fatal("unconfirmed resolved at a granting site")
	}
	if _, ok, _ := ConfirmedHolder(db, "x@acme.test"); ok {
		t.Fatal("a case collision resolved at a granting site")
	}
	ids, err := CarrierIDs(db, []string{"PAT@acme.test", "x@acme.test", "", "x@ACME.test"})
	if err != nil || len(ids) != 3 {
		t.Fatalf("carriers = %v %v", ids, err)
	}
	if ok, _ := HoldsConfirmed(db, confirmed, "HENRY@acme.test"); !ok {
		t.Fatal("HoldsConfirmed")
	}
	if ok, _ := HoldsConfirmed(db, unconfirmed, "pat@acme.test"); ok {
		t.Fatal("HoldsConfirmed for unconfirmed")
	}
	if s, _ := AddressStandingOf(db, unconfirmed); s != AddressUnconfirmed {
		t.Fatal(s)
	}
}

func TestPAC71_SessionGenerationPrimitives(t *testing.T) {
	db := openTestDB(t)
	u := pac71User(t, db, "gen@example.com", "")
	ctx := context.Background()
	ran := 0
	fn := func(Querier) error { ran++; return nil }

	if err := WithSessionGeneration(ctx, db, fn); err != nil || ran != 1 {
		t.Fatal("no guard: fn must run directly")
	}
	g := &SessionGuard{UserID: u, Gen: 0}
	if err := WithSessionGeneration(WithSessionGuard(ctx, g), db, fn); err != nil || ran != 2 || g.Stale() {
		t.Fatal("current generation: fn must run")
	}
	mustExec(t, db, `UPDATE users SET session_generation = 1 WHERE id = $1`, u)
	if err := WithSessionGeneration(WithSessionGuard(ctx, g), db, fn); !errors.Is(err, ErrStaleSession) || ran != 2 || !g.Stale() {
		t.Fatalf("stale: %v ran %d", err, ran)
	}
	ghost := &SessionGuard{UserID: uuid.New()}
	if err := WithSessionGeneration(WithSessionGuard(ctx, ghost), db, fn); !errors.Is(err, ErrStaleSession) || !ghost.Stale() {
		t.Fatal("missing account must be stale")
	}
	if err := WithGeneration(ctx, db, u, 0, fn); !errors.Is(err, ErrStaleSession) {
		t.Fatal("WithGeneration with an old generation")
	}
	if err := WithGeneration(ctx, db, u, 1, func(Querier) error { return errors.New("inner") }); err == nil || err.Error() != "inner" {
		t.Fatal("inner error not returned")
	}
	if gen, ok, err := CurrentGeneration(db, u); err != nil || !ok || gen != 1 {
		t.Fatal("CurrentGeneration")
	}
	if _, ok, _ := CurrentGeneration(db, uuid.New()); ok {
		t.Fatal("CurrentGeneration of nobody")
	}
	// Unreadable generation.
	closed, _ := sql.Open("pgx", "postgres://x:x@127.0.0.1:1/none")
	_ = closed.Close()
	ug := &SessionGuard{UserID: u, Gen: 1}
	if err := WithSessionGeneration(WithSessionGuard(ctx, ug), closed, fn); !errors.Is(err, ErrGenerationUnavailable) || !ug.Unavailable() {
		t.Fatal("unreadable generation must be unavailable")
	}
	if _, _, _, err := BeginSessionLease(ctx, closed, u); !errors.Is(err, ErrGenerationUnavailable) {
		t.Fatal("lease on a closed db")
	}
	if SessionGuardFrom(ctx) != nil || (*SessionGuard)(nil).Stale() || (*SessionGuard)(nil).Unavailable() {
		t.Fatal("nil guard")
	}
}

// The request lease makes every generation advance wait for the requests
// authenticated under the previous generation (plan §3).
func TestPAC71_LeaseHoldsOffEveryAdvance(t *testing.T) {
	db := openTestDB(t)
	u := pac71User(t, db, "lease@example.com", "")
	lease, gen, found, err := BeginSessionLease(context.Background(), db, u)
	if err != nil || !found || gen != 0 {
		t.Fatalf("lease: %v %v %d", err, found, gen)
	}
	done := make(chan error, 1)
	go func() {
		_, err := FreeAddress(context.Background(), db, u, false, OperatorRun{Operator: "op", Reason: "lease"})
		done <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var n int
		_ = db.QueryRow(`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted`).Scan(&n)
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the advance did not wait")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if g, _, _ := CurrentGeneration(db, u); g != 0 {
		t.Fatal("the advance committed during the lease")
	}
	lease.Release()
	lease.Release() // idempotent
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if g, _, _ := CurrentGeneration(db, u); g != 1 {
		t.Fatal("the advance did not commit after the lease")
	}
	if l, _, found, err := BeginSessionLease(context.Background(), db, uuid.New()); err != nil || found || l != nil {
		t.Fatal("lease for a missing account")
	}
}

func TestPAC71_ResolveSignInEdges(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	var refused *RefusalError
	if _, err := ResolveSignIn(ctx, db, SignInRequest{Identity: Identity{Provider: IdentityGoogle}}); !errors.As(err, &refused) {
		t.Fatal("no subject must be refused")
	}
	if _, err := ResolveSignIn(ctx, db, SignInRequest{Identity: Identity{Provider: IdentityGoogle, Subject: "1"}}); !errors.As(err, &refused) {
		t.Fatal("no address must be refused")
	}
	if !strings.Contains((&RefusalError{Cause: "c", Account: uuid.New(), Kind: "habit"}).Error(), "first non-empty kind: habit") {
		t.Fatal("refusal message")
	}
	if !SignInRuleA.Bound() || !SignInRuleB.Bound() || SignInCreated.Bound() || SignInReached.Bound() {
		t.Fatal("Bound")
	}
	if EvidenceNone.String() != "none" || EvidenceMailbox.String() != "mailbox" || EvidenceAuthoritative.String() != "authoritative" {
		t.Fatal("evidence names")
	}
	if e, ok := ParseEvidence(" Authoritative "); !ok || e != EvidenceAuthoritative {
		t.Fatal("ParseEvidence")
	}
	if _, ok := ParseEvidence("none"); ok {
		t.Fatal("ParseEvidence none")
	}
	if (Identity{Provider: "google", Subject: "1"}).String() != "google:1" || (Identity{Provider: "microsoft", Tenant: "t", Subject: "o"}).String() != "microsoft:t:o" {
		t.Fatal("Identity.String")
	}
	// A credential store failure inside creation writes nothing.
	_, err := ResolveSignIn(ctx, db, SignInRequest{
		Identity: Identity{Provider: IdentityGoogle, Subject: "2"}, Email: "fail@example.com", Evidence: EvidenceMailbox, FirstProvider: "google",
		StoreCredential: func(Querier, uuid.UUID, SignInHow) error { return errors.New("store failed") },
	})
	if err == nil || errors.As(err, &refused) {
		t.Fatalf("= %v, want a plain failure", err)
	}
	var n int
	_ = db.QueryRow(`SELECT count(*) FROM users WHERE email = 'fail@example.com'`).Scan(&n)
	if n != 0 {
		t.Fatal("a failed creation left an account")
	}
}

func TestPAC71_ParseOperatorIdentity(t *testing.T) {
	if id, err := ParseOperatorIdentity("google:123"); err != nil || id != (Identity{Provider: IdentityGoogle, Subject: "123"}) {
		t.Fatal(id, err)
	}
	if id, err := ParseOperatorIdentity("microsoft:TID:OID"); err != nil || id.Tenant != "tid" || id.Subject != "oid" {
		t.Fatal(id, err)
	}
	for _, bad := range []string{"", "google:", "oidc:x:y", "microsoft:t"} {
		if _, err := ParseOperatorIdentity(bad); !errors.Is(err, ErrOperatorInput) {
			t.Errorf("%q accepted", bad)
		}
	}
}

type auditRow struct {
	action, operator, reason, email, prev, next, evidence, removed string
}

func lastAudit(t *testing.T, db *sql.DB) auditRow {
	t.Helper()
	var a auditRow
	var email, prev, next, ev sql.NullString
	if err := db.QueryRow(`SELECT action, operator, reason, account_email, previous_identity, new_identity, evidence, removed::text FROM operator_audit ORDER BY created_at DESC, id DESC LIMIT 1`).
		Scan(&a.action, &a.operator, &a.reason, &email, &prev, &next, &ev, &a.removed); err != nil {
		t.Fatal(err)
	}
	a.email, a.prev, a.next, a.evidence = email.String, prev.String, next.String, ev.String
	return a
}

// AC-49: unbind and reassign.
func TestPAC71_AC49_OperatorReassign(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	a := pac71User(t, db, "henry@acme.test", "ga-1")
	mustExec(t, db, `INSERT INTO user_identities (user_id, provider, subject, evidence, bound_by) VALUES ($1, 'google', 'ga-1', 'mailbox', 'rule_a')`, a)
	mustExec(t, db, `INSERT INTO habits (user_id, title, duration_minutes) VALUES ($1, 'h', 30)`, a)
	mustExec(t, db, `INSERT INTO oauth_tokens (user_id, access_token, refresh_token, expiry, calendar_id) VALUES ($1, 'ga-cal', 'r', NOW(), 'primary')`, a)
	mustExec(t, db, `INSERT INTO workspace_connections (user_id, provider, access_token) VALUES ($1, 'slack', 'x')`, a)
	run := OperatorRun{Operator: "ops@paceday", Reason: "verified with acme's admin"}

	if _, err := ReassignAccount(ctx, db, ReassignRequest{Account: a, Identity: Identity{Provider: IdentityGoogle, Subject: "g1"}, Evidence: EvidenceAuthoritative, Run: OperatorRun{}}); !errors.Is(err, ErrOperatorInput) {
		t.Fatal("no operator accepted")
	}
	if _, err := ReassignAccount(ctx, db, ReassignRequest{Account: a, Identity: Identity{Provider: IdentityOIDC, Subject: "s"}, Evidence: EvidenceAuthoritative, Run: run}); !errors.Is(err, ErrOperatorInput) {
		t.Fatal("SSO identity accepted")
	}
	if _, err := ReassignAccount(ctx, db, ReassignRequest{Account: a, Identity: Identity{Provider: IdentityGoogle, Subject: "g1"}, Evidence: EvidenceNone, Run: run}); !errors.Is(err, ErrOperatorInput) {
		t.Fatal("no evidence accepted")
	}
	if _, err := ReassignAccount(ctx, db, ReassignRequest{Account: a, Identity: Identity{Provider: IdentityGoogle, Subject: "g1"}, Evidence: EvidenceAuthoritative, Remove: []string{"bogus"}, Run: run}); !errors.Is(err, ErrOperatorInput) {
		t.Fatal("unknown removal accepted")
	}
	if _, err := ReassignAccount(ctx, db, ReassignRequest{Account: uuid.New(), Identity: Identity{Provider: IdentityGoogle, Subject: "g1"}, Evidence: EvidenceAuthoritative, Run: run}); !errors.Is(err, ErrOperatorInput) {
		t.Fatal("missing account accepted")
	}

	res, err := ReassignAccount(ctx, db, ReassignRequest{Account: a, Identity: Identity{Provider: IdentityGoogle, Subject: "g1"}, Evidence: EvidenceAuthoritative, Remove: []string{"slack"}, Run: run})
	if err != nil {
		t.Fatal(err)
	}
	if res.Previous != "google:ga-1" || res.Gen != 1 || strings.Join(res.Removed, ",") != "google_calendar_credential,microsoft_credential,personal_calendars,slack_connection" {
		t.Fatalf("result = %+v", res)
	}
	b, _ := GetBoundIdentity(db, a)
	var standing, pid string
	_ = db.QueryRow(`SELECT address_standing, provider_id FROM users WHERE id = $1`, a).Scan(&standing, &pid)
	if b == nil || b.Subject != "g1" || standing != AddressDomainConfirmed || pid != "g1" {
		t.Fatalf("binding %v standing %s pid %s", b, standing, pid)
	}
	var habits, tokens, conns int
	_ = db.QueryRow(`SELECT (SELECT count(*) FROM habits WHERE user_id = $1), (SELECT count(*) FROM oauth_tokens WHERE user_id = $1), (SELECT count(*) FROM workspace_connections WHERE user_id = $1)`, a).Scan(&habits, &tokens, &conns)
	if habits != 1 || tokens != 0 || conns != 0 {
		t.Fatalf("habits %d tokens %d conns %d", habits, tokens, conns)
	}
	if got := lastAudit(t, db); got.action != "reassign" || got.operator != run.Operator || got.reason != run.Reason || got.email != "henry@acme.test" ||
		got.prev != "google:ga-1" || got.next != "google:g1" || got.evidence != "authoritative" || !strings.Contains(got.removed, "slack_connection") {
		t.Fatalf("audit = %+v", got)
	}
	// The identity is now bound: reassigning it to another account is refused.
	other := pac71User(t, db, "other@acme.test", "")
	if _, err := ReassignAccount(ctx, db, ReassignRequest{Account: other, Identity: Identity{Provider: IdentityGoogle, Subject: "g1"}, Evidence: EvidenceAuthoritative, Run: run}); !errors.Is(err, ErrOperatorInput) {
		t.Fatal("an identity bound elsewhere was reassigned")
	}
	// An unbound, non-empty account binds; calendar credentials kept on request.
	c := pac71User(t, db, "carol@acme.test", "")
	mustExec(t, db, `INSERT INTO oauth_tokens (user_id, access_token, refresh_token, expiry, calendar_id) VALUES ($1, 'c-cal', 'r', NOW(), 'primary')`, c)
	if res, err := ReassignAccount(ctx, db, ReassignRequest{Account: c, Identity: Identity{Provider: IdentityMicrosoft, Tenant: "t", Subject: "o"}, Evidence: EvidenceMailbox, KeepCalendarCredentials: true, Run: run}); err != nil || res.Previous != "" || len(res.Removed) != 0 {
		t.Fatalf("unbound: %+v %v", res, err)
	}
	var n int
	_ = db.QueryRow(`SELECT count(*) FROM oauth_tokens WHERE user_id = $1`, c).Scan(&n)
	if n != 1 {
		t.Fatal("kept calendar credential was removed")
	}
}

func TestPAC71_OperatorFreeAddress(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	a := pac71User(t, db, "squat@acme.test", "ga")
	mustExec(t, db, `INSERT INTO user_identities (user_id, provider, subject, evidence, bound_by) VALUES ($1, 'google', 'ga', 'mailbox', 'created')`, a)
	mustExec(t, db, `INSERT INTO settings (user_id, llm_api_key_openai, zoom_tokens) VALUES ($1, 'sk', 'z')`, a)
	mustExec(t, db, `INSERT INTO workspace_connections (user_id, provider, access_token) VALUES ($1, 'notion', 'x')`, a)
	if _, err := FreeAddress(ctx, db, a, true, OperatorRun{Operator: "op"}); !errors.Is(err, ErrOperatorInput) {
		t.Fatal("no reason accepted")
	}
	ph, err := FreeAddress(ctx, db, a, true, OperatorRun{Operator: "op", Reason: "squatter"})
	if err != nil || ph != FreedAddress(a) {
		t.Fatal(ph, err)
	}
	var email, standing string
	var gen int64
	_ = db.QueryRow(`SELECT email, address_standing, session_generation FROM users WHERE id = $1`, a).Scan(&email, &standing, &gen)
	if email != ph || standing != AddressUnconfirmed || gen != 1 {
		t.Fatalf("%s %s %d", email, standing, gen)
	}
	if k, _ := FirstNonEmptyKind(db, a); k != "" {
		t.Fatalf("squatter data left: %s", k)
	}
	if b, _ := GetBoundIdentity(db, a); b == nil {
		t.Fatal("binding removed")
	}
	if got := lastAudit(t, db); got.action != "free_address" || got.email != "squat@acme.test" {
		t.Fatalf("audit = %+v", got)
	}
	// The address is free again.
	if c, _ := CarriersOf(db, "squat@acme.test"); len(c) != 0 {
		t.Fatal("still carried")
	}
}

// AC-44 (conditional on OQ-6): every session ends; nothing else changes.
func TestPAC71_AC44_AdvanceAllGenerations(t *testing.T) {
	db := openTestDB(t)
	a := pac71User(t, db, "a@example.com", "")
	b := pac71User(t, db, "b@example.com", "")
	mustExec(t, db, `UPDATE users SET session_generation = 4 WHERE id = $1`, b)
	mustExec(t, db, `INSERT INTO settings (user_id, timezone) VALUES ($1, 'Europe/Paris')`, a)
	if _, err := AdvanceAllGenerations(context.Background(), db, OperatorRun{}); !errors.Is(err, ErrOperatorInput) {
		t.Fatal("no operator accepted")
	}
	n, err := AdvanceAllGenerations(context.Background(), db, OperatorRun{Operator: "op", Reason: "OQ-6 yes"})
	if err != nil || n != 2 {
		t.Fatal(n, err)
	}
	ga, _, _ := CurrentGeneration(db, a)
	gb, _, _ := CurrentGeneration(db, b)
	var tz string
	_ = db.QueryRow(`SELECT timezone FROM settings WHERE user_id = $1`, a).Scan(&tz)
	if ga != 1 || gb != 5 || tz != "Europe/Paris" {
		t.Fatalf("gens %d %d tz %s", ga, gb, tz)
	}
	if got := lastAudit(t, db); got.action != "advance_all" {
		t.Fatal(got)
	}
}

// AC-18 and spec §7: migration 032 deletes nothing and binds nobody; its down
// migration keeps the operator audit; up -> down -> up works.
func TestPAC71_Migration032_AC18_UpDownUp(t *testing.T) {
	db, to := migrationTestDB(t)
	to(31)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	u1 := seedMigrationUser(t, db, "henry@acme.test", base)
	seedMigrationUser(t, db, "x@acme.test", base.Add(time.Hour))
	seedMigrationUser(t, db, "X@acme.test", base.Add(2*time.Hour))
	mustExec(t, db, `INSERT INTO settings (user_id, timezone) VALUES ($1, 'Europe/Paris')`, u1)
	count := func() (users, settings int) {
		_ = db.QueryRow(`SELECT (SELECT count(*) FROM users), (SELECT count(*) FROM settings)`).Scan(&users, &settings)
		return
	}
	u0, s0 := count()

	to(32)
	if u, s := count(); u != u0 || s != s0 {
		t.Fatal("rows changed")
	}
	var ids, gens, confirmed, keyed int
	_ = db.QueryRow(`SELECT (SELECT count(*) FROM user_identities), (SELECT count(*) FROM users WHERE session_generation <> 0),
		(SELECT count(*) FROM users WHERE address_standing <> 'unconfirmed'), (SELECT count(*) FROM users WHERE email_key IS NOT NULL)`).Scan(&ids, &gens, &confirmed, &keyed)
	if ids != 0 || gens != 0 || confirmed != 0 || keyed != 1 {
		t.Fatalf("identities %d gens %d confirmed %d keyed %d (want 0 0 0 1: the collided pair is not keyed)", ids, gens, confirmed, keyed)
	}
	// A new account cannot take an address another carries in any case.
	if _, err := db.Exec(`INSERT INTO users (email, email_key) VALUES ('HENRY@acme.test', 'henry@acme.test')`); err == nil {
		t.Fatal("email_key uniqueness not enforced")
	}
	mustExec(t, db, `INSERT INTO operator_audit (action, operator, reason) VALUES ('advance_all', 'op', 'test')`)

	to(31)
	var col int
	_ = db.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_name = 'users' AND column_name IN ('session_generation', 'address_standing', 'email_key')`).Scan(&col)
	if col != 0 {
		t.Fatal("down left columns")
	}
	var audits int
	if err := db.QueryRow(`SELECT count(*) FROM operator_audit`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("operator audit not kept: %d %v", audits, err)
	}
	to(32)
	if err := db.QueryRow(`SELECT count(*) FROM operator_audit`).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("re-apply lost the audit")
	}
}
