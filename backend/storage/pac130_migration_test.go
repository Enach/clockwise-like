package storage

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// PAC-130 (spec docs/specs/PAC-130.md AC-12, AC-18, AC-17; contract
// contracts/features/PAC-130.md §2.5, §4): migration 042 erases every Manager
// roster account link outside the visibility rule for the roster's owner,
// changes nothing else, and the organisation member set has one definition.

type pac130MigFixture struct {
	db *sql.DB
	to func(version uint)
}

// pac130MigDB is a fresh database at migration 041, the version before 042.
func pac130MigDB(t *testing.T) *pac130MigFixture {
	t.Helper()
	db, to := migrationTestDB(t)
	to(41)
	return &pac130MigFixture{db: db, to: to}
}

func (f *pac130MigFixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := f.db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func (f *pac130MigFixture) org(t *testing.T, domain string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.db.QueryRow(`INSERT INTO organizations (name, domain) VALUES ($1, $1) RETURNING id`, domain).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// user inserts an account at email, in org (uuid.Nil: none), with standing.
func (f *pac130MigFixture) user(t *testing.T, email string, org uuid.UUID, standing string) uuid.UUID {
	t.Helper()
	var orgID any
	if org != uuid.Nil {
		orgID = org
	}
	var id uuid.UUID
	if err := f.db.QueryRow(`INSERT INTO users (email, name, org_id, address_standing) VALUES ($1, $1, $2, $3) RETURNING id`,
		email, orgID, standing).Scan(&id); err != nil {
		t.Fatalf("user %s: %v", email, err)
	}
	return id
}

func (f *pac130MigFixture) team(t *testing.T, owner uuid.UUID, members ...uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.db.QueryRow(`INSERT INTO teams (name, created_by) VALUES ('T', $1) RETURNING id`, owner).Scan(&id); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $2, 'owner')`, id, owner)
	for _, m := range members {
		f.exec(t, `INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $2, 'member')`, id, m)
	}
	return id
}

func (f *pac130MigFixture) link(t *testing.T, manager uuid.UUID, email string, member uuid.UUID) {
	t.Helper()
	f.exec(t, `INSERT INTO manager_team_members (manager_user_id, member_email, member_user_id, display_name, source, cadence)
		VALUES ($1, $2, $3, 'Name', 'manual', 'weekly')`, manager, email, member)
}

func (f *pac130MigFixture) linkOf(t *testing.T, manager uuid.UUID, email string) *uuid.UUID {
	t.Helper()
	var mu uuid.NullUUID
	if err := f.db.QueryRow(`SELECT member_user_id FROM manager_team_members WHERE manager_user_id = $1 AND member_email = $2`, manager, email).Scan(&mu); err != nil {
		t.Fatal(err)
	}
	if !mu.Valid {
		return nil
	}
	return &mu.UUID
}

// snapshot is every row of every table the migration could touch, every
// column but member_user_id, as text.
func (f *pac130MigFixture) snapshot(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, q := range []string{
		`SELECT (to_jsonb(m) - 'member_user_id')::text FROM manager_team_members m ORDER BY id`,
		`SELECT to_jsonb(c)::text FROM team_analytics_cache c ORDER BY manager_user_id, member_email, week_start`,
		`SELECT to_jsonb(u)::text FROM users u ORDER BY id`,
		`SELECT to_jsonb(m)::text FROM team_members m ORDER BY team_id, user_id`,
	} {
		rows, err := f.db.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			out = append(out, s)
		}
		_ = rows.Close()
	}
	return out
}

// AC-12: H's link (no shared team, no organisation) is erased; R's (a
// co-member of V's team) and an organisation link of another manager are
// kept; so is a mixed-case account carrying the row's address; a link to an
// account whose address changed is erased; nothing else changes.
func TestPAC130_AC12_Migration042(t *testing.T) {
	f := pac130MigDB(t)
	vOrg := f.org(t, "v.pac130.test")
	m2Org := f.org(t, "m2.pac130.test")
	v := f.user(t, "v@v.pac130.test", vOrg, "domain_confirmed")
	h := f.user(t, "hold@ext.pac130.test", uuid.Nil, "domain_confirmed")
	r := f.user(t, "r@other.pac130.test", uuid.Nil, "domain_confirmed")
	k := f.user(t, "k-new@other.pac130.test", uuid.Nil, "domain_confirmed")
	x := f.user(t, "Mixed@v.pac130.test", vOrg, "domain_confirmed")
	m2 := f.user(t, "m2@m2.pac130.test", m2Org, "domain_confirmed")
	q := f.user(t, "q@m2.pac130.test", m2Org, "unconfirmed")
	f.team(t, v, r, k)
	f.link(t, v, "hold@ext.pac130.test", h)
	f.link(t, v, "r@other.pac130.test", r)
	f.link(t, v, "k-old@other.pac130.test", k) // the account's address changed since
	f.link(t, v, "mixed@v.pac130.test", x)
	f.link(t, m2, "q@m2.pac130.test", q)
	f.link(t, m2, "hold@ext.pac130.test", h)
	f.exec(t, `INSERT INTO manager_team_members (manager_user_id, member_email, display_name) VALUES ($1, 'none@ext.pac130.test', 'N')`, v)
	f.exec(t, `INSERT INTO team_analytics_cache (manager_user_id, member_email, week_start, focus_minutes, is_paceday_user, data_available)
		VALUES ($1, 'hold@ext.pac130.test', '2026-11-09', 615, true, true)`, v)
	before := f.snapshot(t)

	f.to(42)

	for _, c := range []struct {
		manager uuid.UUID
		email   string
		want    *uuid.UUID
	}{
		{v, "hold@ext.pac130.test", nil},
		{v, "r@other.pac130.test", &r},
		{v, "k-old@other.pac130.test", nil},
		{v, "mixed@v.pac130.test", &x},
		{m2, "q@m2.pac130.test", &q},
		{m2, "hold@ext.pac130.test", nil},
		{v, "none@ext.pac130.test", nil},
	} {
		got := f.linkOf(t, c.manager, c.email)
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("%s: link = %v, want %v", c.email, got, c.want)
		}
	}
	after := f.snapshot(t)
	if len(after) != len(before) {
		t.Fatalf("rows changed: %d -> %d", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("row changed:\n%s\n%s", before[i], after[i])
		}
	}

	// The down step changes nothing, and a re-run of the up step erases
	// nothing more while memberships are unchanged.
	f.to(41)
	if got := f.linkOf(t, v, "r@other.pac130.test"); got == nil || *got != r {
		t.Fatalf("down changed R's link: %v", got)
	}
	if got := f.linkOf(t, v, "hold@ext.pac130.test"); got != nil {
		t.Fatalf("down restored H's link: %v", got)
	}
	f.to(42)
	if got := f.linkOf(t, v, "r@other.pac130.test"); got == nil || *got != r {
		t.Fatalf("re-run erased R's link: %v", got)
	}
}

// pac130SignIn signs in to email with authoritative evidence through a new
// Google identity, so only rule (b) can bind an unbound legacy carrier.
func pac130SignIn(db *sql.DB, email string) (*SignInResult, error) {
	return ResolveSignIn(context.Background(), db, SignInRequest{
		Identity:      Identity{Provider: IdentityGoogle, Subject: "pac130-" + uuid.NewString()},
		Email:         email,
		Name:          "L",
		Evidence:      EvidenceAuthoritative,
		FirstProvider: "google",
		RuleA:         &RuleA{Matches: func(string, string) bool { return false }, MinEvidence: EvidenceAuthoritative},
	})
}

// AC-18, sign-in half (spec D-12): an unbound legacy account whose only
// "data" was another manager's link, outside that manager's teams and
// organisation, is empty after 042 and binds under PAC-71 rule (b); when the
// link is kept (the account is in that manager's organisation) it is still
// refused with kind roster_entry_pointing_at_account.
func TestPAC130_AC18_ErasedLinkLetsLegacyAccountBind(t *testing.T) {
	f := pac130MigDB(t)
	m2Org := f.org(t, "m2.pac130.test")
	m2 := f.user(t, "m2@m2.pac130.test", m2Org, "domain_confirmed")
	erased := f.user(t, "l@ext.pac130.test", uuid.Nil, "unconfirmed")
	kept := f.user(t, "l@m2.pac130.test", m2Org, "unconfirmed")
	f.link(t, m2, "l@ext.pac130.test", erased)
	f.link(t, m2, "l@m2.pac130.test", kept)

	// Before 042 both are refused for the link (PAC-71 D12, unchanged).
	for _, e := range []string{"l@ext.pac130.test", "l@m2.pac130.test"} {
		kind, err := FirstNonEmptyKind(f.db, map[string]uuid.UUID{"l@ext.pac130.test": erased, "l@m2.pac130.test": kept}[e])
		if err != nil || kind != "roster_entry_pointing_at_account" {
			t.Fatalf("%s before 042: kind %q, %v", e, kind, err)
		}
	}

	f.to(42)

	res, err := pac130SignIn(f.db, "l@ext.pac130.test")
	if err != nil {
		t.Fatalf("sign-in of the erased-link account: %v", err)
	}
	if res.User.ID != erased || res.How != SignInRuleB {
		t.Fatalf("sign-in = %s %s, want %s bound by rule (b)", res.User.ID, res.How, erased)
	}

	_, err = pac130SignIn(f.db, "l@m2.pac130.test")
	var refused *RefusalError
	if !errors.As(err, &refused) || refused.Kind != "roster_entry_pointing_at_account" || refused.Account != kept {
		t.Fatalf("sign-in of the kept-link account = %v, want refused for roster_entry_pointing_at_account", err)
	}
}

// AC-17 at the storage layer (contract md §2.5): GetOrgMembers and the
// visibility rule (b) read one set. Excluding an account removes it from both;
// a nil exclusion (the seam answering nil) never empties the set; a viewer
// with no organisation has no members.
func TestPAC130_OrgMemberSetIsOneDefinition(t *testing.T) {
	db := openTestDB(t)
	dom := uuid.NewString()[:8] + ".pac130.test"
	org, err := UpsertOrg(db, dom, dom)
	if err != nil {
		t.Fatal(err)
	}
	mk := func(local string, inOrg bool) uuid.UUID {
		u, err := UpsertUser(db, local+"@"+dom, local, "", "google", local+dom)
		if err != nil {
			t.Fatal(err)
		}
		pac148Confirm(t, db, u.ID) // PAC-148 (contract md §8)
		if inOrg {
			if err := SetUserOrg(db, u.ID, org.ID); err != nil {
				t.Fatal(err)
			}
		}
		return u.ID
	}
	v := mk("v", true)
	q := mk("q", true)
	loner := mk("loner", false)

	ids := func(viewer uuid.UUID) map[uuid.UUID]bool {
		t.Helper()
		us, err := GetOrgMembers(db, viewer)
		if err != nil {
			t.Fatal(err)
		}
		out := map[uuid.UUID]bool{}
		for _, u := range us {
			out[u.ID] = true
		}
		return out
	}
	visible := func(viewer uuid.UUID, email string) bool {
		t.Helper()
		ok, err := AddressVisibleTo(db, viewer, email)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if got := ids(v); len(got) != 2 || !got[v] || !got[q] {
		t.Fatalf("members = %v, want V and Q", got)
	}
	if !visible(v, "Q@"+dom) {
		t.Fatal("Q not visible to V through the organisation")
	}
	if len(ids(loner)) != 0 || visible(loner, "q@"+dom) {
		t.Fatal("a viewer with no organisation sees members")
	}

	InstallOrgMembersExclude(t, func(viewer uuid.UUID) []uuid.UUID {
		if viewer == v {
			return []uuid.UUID{q}
		}
		return nil // nil: no exclusion, never "exclude everything"
	})
	if got := ids(v); len(got) != 1 || !got[v] {
		t.Fatalf("members with Q excluded = %v, want V only", got)
	}
	if visible(v, "q@"+dom) {
		t.Fatal("Q still visible to V with Q excluded from the organisation set")
	}
	if got := ids(q); len(got) != 2 {
		t.Fatalf("a nil exclusion emptied the set: %v", got)
	}
}

// Contract md §4.3: docs/runbook.md "Rollback" carries the PAC-130 bullet:
// migration 042, the irreversible erase, the snapshot advice and the PAC-71
// rule (b) release note.
func TestPAC130_RunbookRollbackBullet(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "runbook.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.ReplaceAll(string(raw), "\r\n", "\n")
	i := strings.Index(doc, "- **Going back past PAC-130**")
	if i < 0 {
		t.Fatal(`docs/runbook.md has no "Going back past PAC-130" bullet`)
	}
	bullet := doc[i:]
	if j := strings.Index(bullet[2:], "\n- "); j >= 0 {
		bullet = bullet[:j+2]
	}
	if j := strings.Index(bullet, "\n#"); j >= 0 {
		bullet = bullet[:j]
	}
	for _, need := range []string{"042_manager_member_link_erase", "**erased**", "pg_dump -t manager_team_members", "rule (b)", "image-only rollback does not work"} {
		if !strings.Contains(bullet, need) {
			t.Errorf("the PAC-130 rollback bullet does not mention %q", need)
		}
	}
}
