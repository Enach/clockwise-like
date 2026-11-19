package storage

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// PAC-148 at the storage layer (docs/specs/PAC-148.md §3.1, §3.2;
// contracts/features/PAC-148.md §2): the membership rule is decided at every
// read through effectiveOrgSQL, and the sign-in step (ReconcileUserOrg)
// joins or leaves.

type pac148Store struct {
	db           *sql.DB
	acme, other  *Org
	dAcme, dOthr string
}

func newPAC148Store(t *testing.T) *pac148Store {
	t.Helper()
	db := openTestDB(t)
	run := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	s := &pac148Store{db: db, dAcme: "acme-" + run + ".pac148.test", dOthr: "other-" + run + ".pac148.test"}
	var err error
	if s.acme, err = UpsertOrg(db, s.dAcme, "ACME"); err != nil {
		t.Fatal(err)
	}
	if s.other, err = UpsertOrg(db, s.dOthr, "OTHER"); err != nil {
		t.Fatal(err)
	}
	return s
}

// user inserts an account at email with standing and a stored org (nil: none).
func (s *pac148Store) user(t *testing.T, email, standing string, org *Org) uuid.UUID {
	t.Helper()
	var orgID any
	if org != nil {
		orgID = org.ID
	}
	var id uuid.UUID
	if err := s.db.QueryRow(`INSERT INTO users (email, name, org_id, address_standing) VALUES ($1, 'N', $2, $3) RETURNING id`,
		email, orgID, standing).Scan(&id); err != nil {
		t.Fatalf("user %s: %v", email, err)
	}
	return id
}

func (s *pac148Store) confirmFor(t *testing.T, user uuid.UUID, org *Org) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO org_ai_confirmations (user_id, org_id, consent_since_version, confirmed_at) VALUES ($1, $2, 1, now())`, user, org.ID); err != nil {
		t.Fatal(err)
	}
}

func (s *pac148Store) confirmations(t *testing.T, user uuid.UUID) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM org_ai_confirmations WHERE user_id = $1`, user).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The rule at every reader (contract md §2.3): a member reads its
// organisation everywhere; each failing stored association (unconfirmed;
// another domain's organisation; a consumer-list domain's organisation; an
// address with no @) reads as no organisation everywhere, while its raw
// users.org_id is untouched.
func TestPAC148_EveryReaderAppliesTheRule(t *testing.T) {
	s := newPAC148Store(t)
	ctx := context.Background()
	hot, err := UpsertOrg(s.db, "hotmail.fr", "Hotmail")
	if err != nil {
		t.Fatal(err)
	}
	member := s.user(t, "a1@"+strings.ToUpper(s.dAcme), AddressDomainConfirmed, s.acme)
	if _, err := s.db.Exec(`INSERT INTO org_ai_configurations (org_id, provider, model, api_key) VALUES ($1, 'openai', 'm', '')`, s.acme.ID); err != nil {
		t.Fatal(err)
	}
	failing := map[string]uuid.UUID{
		"unconfirmed":         s.user(t, "u@"+s.dAcme, AddressUnconfirmed, s.acme),
		"other domain":        s.user(t, "a3@"+s.dOthr, AddressDomainConfirmed, s.acme),
		"consumer-list":       s.user(t, "x-"+s.dAcme+"@hotmail.fr", AddressDomainConfirmed, hot),
		"freed address, no @": s.user(t, "freed-"+uuid.NewString(), AddressDomainConfirmed, s.acme),
	}
	for _, id := range failing {
		s.confirmFor(t, id, s.acme)
	}

	check := func(name string, id uuid.UUID, want *uuid.UUID) {
		t.Helper()
		eq := func(got *uuid.UUID) bool { return (got == nil) == (want == nil) && (got == nil || *got == *want) }
		u, err := GetUserByIDNoOrg(s.db, id)
		if err != nil || !eq(u.OrgID) {
			t.Errorf("%s: GetUserByIDNoOrg OrgID = %v, %v", name, u.OrgID, err)
		}
		u, err = GetUserByID(s.db, id)
		if err != nil || !eq(u.OrgID) || (want == nil) != (u.Org == nil) {
			t.Errorf("%s: GetUserByID OrgID = %v Org = %v, %v", name, u.OrgID, u.Org, err)
		}
		var email string
		if err := s.db.QueryRow(`SELECT email FROM users WHERE id = $1`, id).Scan(&email); err != nil {
			t.Fatal(err)
		}
		if u, err := GetUserByEmail(s.db, email); err != nil || u == nil || !eq(u.OrgID) {
			t.Errorf("%s: GetUserByEmail = %+v, %v", name, u, err)
		}
		if in, err := UserInOrganization(s.db, id); err != nil || in != (want != nil) {
			t.Errorf("%s: UserInOrganization = %v, %v", name, in, err)
		}
		st, err := ReadOrgAIState(ctx, s.db, id)
		if err != nil || !eq(st.OrgID) || st.InOrg() != (want != nil) {
			t.Errorf("%s: ReadOrgAIState = %+v, %v", name, st, err)
		}
		if want == nil && (st.Config.Provider != "" || st.Confirmed()) {
			t.Errorf("%s: a non-member reads the organisation's configuration or confirmation: %+v", name, st)
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		locked, err := lockUserRow(ctx, tx, id)
		_ = tx.Rollback()
		if err != nil || locked.Valid != (want != nil) || (want != nil && locked.UUID != *want) {
			t.Errorf("%s: lockUserRow = %v, %v", name, locked, err)
		}
		members, err := GetOrgMembers(s.db, id)
		if err != nil || (want == nil) != (len(members) == 0) {
			t.Errorf("%s: GetOrgMembers = %d members, %v", name, len(members), err)
		}
	}
	check("member", member, &s.acme.ID)
	for name, id := range failing {
		check(name, id, nil)
		if raw := pac148RawOrg(t, s.db, id); !raw.Valid {
			t.Errorf("%s: a read changed the stored association", name)
		}
	}
	// The member's list holds the member alone: no failing account is listed,
	// and rule (b) agrees.
	members, err := GetOrgMembers(s.db, member)
	if err != nil || len(members) != 1 || members[0].ID != member {
		t.Fatalf("A1's members = %v, %v; want A1 alone", members, err)
	}
	for name, id := range failing {
		var email string
		_ = s.db.QueryRow(`SELECT email FROM users WHERE id = $1`, id).Scan(&email)
		if ok, err := AddressVisibleTo(s.db, member, email); err != nil || ok {
			t.Errorf("%s: visible to A1 through rule (b) = %v, %v", name, ok, err)
		}
	}
	// ConfirmOrgAI through lockUserRow: a planted association records nothing.
	st, _, err := ConfirmOrgAI(ctx, s.db, failing["unconfirmed"], s.acme.ID, 1, time.Now())
	if err != nil || st != OrgAIConfirmNotMember {
		t.Errorf("ConfirmOrgAI of a planted account = %v, %v; want not a member", st, err)
	}
}

// lockUserRow locks the users row only, never the organisation row (contract
// md §2.3, revision 2 N-2).
func TestPAC148_LockUserRowLocksNoOrganisation(t *testing.T) {
	s := newPAC148Store(t)
	ctx := context.Background()
	member := s.user(t, "a1@"+s.dAcme, AddressDomainConfirmed, s.acme)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if org, err := lockUserRow(ctx, tx, member); err != nil || !org.Valid || org.UUID != s.acme.ID {
		t.Fatalf("lockUserRow = %v, %v", org, err)
	}
	pctx, cancel := context.WithTimeout(ctx, pac148Deadline)
	defer cancel()
	other, err := s.db.BeginTx(pctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Rollback() }()
	if _, err := other.ExecContext(pctx, `SELECT id FROM organizations WHERE id = $1 FOR UPDATE NOWAIT`, s.acme.ID); err != nil {
		t.Fatalf("the organisation row is locked while a member's users row is: %v", err)
	}
	if _, err := other.ExecContext(pctx, `SELECT id FROM users WHERE id = $1 FOR NO KEY UPDATE NOWAIT`, member); err == nil {
		t.Fatal("the users row is not locked")
	}
}

// The sign-in step (contract md §2.4): it joins a qualifying account,
// leaves a non-qualifying one with its confirmations, writes nothing for a
// non-qualifying account with no association, and creates no organisation
// row for it.
func TestPAC148_ReconcileUserOrgJoinsAndLeaves(t *testing.T) {
	s := newPAC148Store(t)
	steps := func(fn func()) []string {
		t.Helper()
		var got []string
		uninstall := InstallOrgMembershipStatement(t, func(name string) { got = append(got, name) })
		fn()
		uninstall()
		return got
	}

	// Join: a confirmed account at an unlisted domain with no organisation
	// row yet; its confirmations of another organisation end.
	fresh := "new-" + uuid.NewString()[:8] + ".pac148.test"
	a := s.user(t, "a@"+fresh, AddressDomainConfirmed, nil)
	s.confirmFor(t, a, s.other)
	got := steps(func() {
		if err := ReconcileUserOrg(s.db, a); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Join(got, ",") != "membership-lock,org-upsert,membership-set" {
		t.Errorf("join statements = %v", got)
	}
	u, err := GetUserByID(s.db, a)
	if err != nil || u.Org == nil || u.Org.Domain != fresh || s.confirmations(t, a) != 0 {
		t.Fatalf("after the join: %+v, %v, %d confirmations", u, err, s.confirmations(t, a))
	}
	// Join again, unchanged: its confirmation of its own organisation stays.
	s.confirmFor(t, a, u.Org)
	if err := ReconcileUserOrg(s.db, a); err != nil || s.confirmations(t, a) != 1 {
		t.Fatalf("rejoin: %v, %d confirmations, want 1", err, s.confirmations(t, a))
	}

	// Leave: an unconfirmed account, with confirmations of two
	// organisations (every one ends).
	u1 := s.user(t, "u@"+s.dAcme, AddressUnconfirmed, s.acme)
	s.confirmFor(t, u1, s.acme)
	s.confirmFor(t, u1, s.other)
	got = steps(func() {
		if err := ReconcileUserOrg(s.db, u1); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Join(got, ",") != "membership-lock,membership-leave" {
		t.Errorf("leave statements = %v", got)
	}
	if raw := pac148RawOrg(t, s.db, u1); raw.Valid || s.confirmations(t, u1) != 0 {
		t.Fatalf("after the leave: stored org %v, %d confirmations", raw, s.confirmations(t, u1))
	}
	// Leave: a domain-confirmed account at a consumer-list domain.
	hot, err := UpsertOrg(s.db, "live.fr", "Live")
	if err != nil {
		t.Fatal(err)
	}
	x := s.user(t, "x-"+uuid.NewString()[:8]+"@live.fr", AddressDomainConfirmed, hot)
	s.confirmFor(t, x, hot)
	if err := ReconcileUserOrg(s.db, x); err != nil {
		t.Fatal(err)
	}
	if raw := pac148RawOrg(t, s.db, x); raw.Valid || s.confirmations(t, x) != 0 {
		t.Fatalf("live.fr after the step: stored org %v, %d confirmations", raw, s.confirmations(t, x))
	}

	// Nothing to do: no association, not qualifying; no organisation row
	// is created for its domain.
	lone := "lone-" + uuid.NewString()[:8] + ".pac148.test"
	n := s.user(t, "n@"+lone, AddressUnconfirmed, nil)
	got = steps(func() {
		if err := ReconcileUserOrg(s.db, n); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Join(got, ",") != "membership-lock" {
		t.Errorf("no-op statements = %v", got)
	}
	var rows int
	if err := s.db.QueryRow(`SELECT count(*) FROM organizations WHERE domain = $1`, lone).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("organisation rows for %s = %d, %v", lone, rows, err)
	}
	// A user that does not exist is nothing to do.
	if err := ReconcileUserOrg(s.db, uuid.New()); err != nil {
		t.Fatal(err)
	}
}
