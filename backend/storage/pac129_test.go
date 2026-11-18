package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// PAC-129 (spec docs/specs/PAC-129.md; contract contracts/features/PAC-129.md).

// trimSet is contract §2.2's list, written out independently of
// address_trim.go: the expected value of both enumeration tests.
var trimSet = []rune{
	0x0009, 0x000A, 0x000B, 0x000C, 0x000D, 0x0020, 0x0085, 0x00A0, 0x1680,
	0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006, 0x2007, 0x2008,
	0x2009, 0x200A, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF,
}

func isSurrogate(r rune) bool { return r >= 0xD800 && r <= 0xDFFF }

// T-TRIM-GO: TrimAddressSpace(r+"a"+r) == "a" exactly for the 26 code points,
// over U+0000-U+FFFF without the surrogates; every other one is unchanged.
func TestPAC129_TTrimGo_TrimAddressSpaceIsExactlyTheContractSet(t *testing.T) {
	if len(trimSet) != 26 {
		t.Fatalf("the table holds %d code points, want 26", len(trimSet))
	}
	want := map[rune]bool{}
	for _, r := range trimSet {
		want[r] = true
	}
	var got []rune
	for r := rune(0); r <= 0xFFFF; r++ {
		if isSurrogate(r) {
			continue
		}
		in := string(r) + "a" + string(r)
		switch out := TrimAddressSpace(in); out {
		case "a":
			got = append(got, r)
		case in:
		default:
			t.Errorf("U+%04X: partial trim %q", r, out)
		}
	}
	if fmt.Sprint(got) != fmt.Sprint(trimSet) {
		t.Fatalf("trimmed set = %U, want %U", got, trimSet)
	}
	if TrimAddressSpace("victor@agency.gov") != "victor@agency.gov" || NormaliseStoredAddress("\uFEFF Victor@Agency.gov\u0085") != "victor@agency.gov" {
		t.Error("victor@agency.gov is not kept")
	}
}

// T-TRIM-SQL: after migration 041, paceday_trim_ecma(chr(c)||'a'||chr(c)) =
// 'a' exactly for the 26 code points over U+0001-U+FFFF without the
// surrogates (U+0000 and the surrogates cannot be held by PostgreSQL text);
// every other value is unchanged.
func TestPAC129_TTrimSQL_PacedayTrimEcmaIsExactlyTheContractSet(t *testing.T) {
	db := openTestDB(t)
	rows, err := db.Query(`
		SELECT c FROM generate_series(1, 65535) AS c
		WHERE (c < 55296 OR c > 57343) AND paceday_trim_ecma(chr(c) || 'a' || chr(c)) = 'a'
		ORDER BY c`)
	if err != nil {
		t.Fatal(err)
	}
	var got []rune
	for rows.Next() {
		var c int
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		got = append(got, rune(c))
	}
	rows.Close()
	if fmt.Sprint(got) != fmt.Sprint(trimSet) {
		t.Fatalf("SQL trimmed set = %U, want %U", got, trimSet)
	}
	var partial int
	if err := db.QueryRow(`
		SELECT count(*) FROM generate_series(1, 65535) AS c
		WHERE (c < 55296 OR c > 57343)
		  AND paceday_trim_ecma(chr(c) || 'a' || chr(c)) NOT IN ('a', chr(c) || 'a' || chr(c))`).Scan(&partial); err != nil {
		t.Fatal(err)
	}
	if partial != 0 {
		t.Errorf("%d code points were trimmed partially", partial)
	}
	var v1, v2, vt string
	if err := db.QueryRow(`SELECT paceday_trim_ecma('victor@agency.gov'), lower(paceday_trim_ecma('Victor@Agency.gov')), paceday_trim_ecma(chr(11) || 'v' || chr(11))`).Scan(&v1, &v2, &vt); err != nil {
		t.Fatal(err)
	}
	if v1 != "victor@agency.gov" || v2 != "victor@agency.gov" || vt != "v" {
		t.Errorf("got %q %q %q", v1, v2, vt)
	}
}

// AC-26, T-041-EMPTY and the up/down/up of migration 041.
func TestPAC129_AC26_Migration041(t *testing.T) {
	db, to := migrationTestDB(t)
	to(40)
	owner := seedMigrationUser(t, db, "m41-owner@example.com", time.Now())
	member := seedMigrationUser(t, db, "m41-member@example.com", time.Now())
	var team uuid.UUID
	if err := db.QueryRow(`INSERT INTO teams (name, created_by) VALUES ('T', $1) RETURNING id`, owner).Scan(&team); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $2, 'owner'), ($1, $3, 'member')`, team, owner, member)
	mustExec(t, db, `INSERT INTO manager_team_members (manager_user_id, member_email, display_name) VALUES ($1, 'ext@x.example', 'Ext')`, owner)
	mustExec(t, db, `INSERT INTO manager_team_member_assignments (manager_user_id, team_id, member_email) VALUES ($1, $2, 'ext@x.example')`, owner, team)
	now := time.Now()
	invite := func(email, status string, created, expires time.Time) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := db.QueryRow(`INSERT INTO team_invites (team_id, invitee_email, invited_by, token, status, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`, team, email, owner, uuid.NewString(), status, created, expires).Scan(&id); err != nil {
			t.Fatalf("seed %q: %v", email, err)
		}
		return id
	}
	annOld := invite("Ann@X.example", "pending", now.Add(-2*time.Hour), now.Add(48*time.Hour))
	annNew := invite("ann@x.example", "pending", now.Add(-1*time.Hour), now.Add(72*time.Hour))
	bob := invite("bob@x.example", "expired", now.Add(-200*time.Hour), now.Add(-30*time.Hour))
	cy1 := invite("cy@x.example", "pending", now.Add(-300*time.Hour), now.Add(-100*time.Hour))
	cy2 := invite("cy@x.example", "pending", now.Add(-250*time.Hour), now.Add(-50*time.Hour))
	victor := invite("Victor@Agency.gov", "accepted", now.Add(-5*time.Hour), now.Add(48*time.Hour))
	emptyA := invite(string(rune(12288))+string(rune(8232)), "pending", now, now.Add(48*time.Hour))
	emptyB := invite(string(rune(65279)), "pending", now, now.Add(48*time.Hour))
	_ = emptyA
	_ = emptyB

	var bobBefore string
	if err := db.QueryRow(`SELECT invitee_email || status || expires_at::text FROM team_invites WHERE id = $1`, bob).Scan(&bobBefore); err != nil {
		t.Fatal(err)
	}
	to(41)

	status := func(id uuid.UUID) (email, st string, ok bool) {
		err := db.QueryRow(`SELECT invitee_email, status FROM team_invites WHERE id = $1`, id).Scan(&email, &st)
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", false
		}
		if err != nil {
			t.Fatal(err)
		}
		return email, st, true
	}
	if _, _, ok := status(annOld); ok {
		t.Error("the older pending ann invitation survived")
	}
	if e, st, ok := status(annNew); !ok || e != "ann@x.example" || st != "pending" {
		t.Errorf("newer ann = %q %q %v", e, st, ok)
	}
	var bobAfter string
	if err := db.QueryRow(`SELECT invitee_email || status || expires_at::text FROM team_invites WHERE id = $1`, bob).Scan(&bobAfter); err != nil || bobAfter != bobBefore {
		t.Errorf("bob changed: %q -> %q (%v)", bobBefore, bobAfter, err)
	}
	for _, id := range []uuid.UUID{cy1, cy2} {
		if e, st, ok := status(id); !ok || st != "expired" || e != "cy@x.example" {
			t.Errorf("lapsed cy = %q %q %v, want kept and expired", e, st, ok)
		}
	}
	if e, st, ok := status(victor); !ok || e != "victor@agency.gov" || st != "accepted" {
		t.Errorf("victor = %q %q %v", e, st, ok)
	}
	var invitesLeft, empties int
	_ = db.QueryRow(`SELECT count(*) FROM team_invites WHERE team_id = $1`, team).Scan(&invitesLeft)
	_ = db.QueryRow(`SELECT count(*) FROM team_invites WHERE invitee_email = ''`).Scan(&empties)
	if invitesLeft != 5 || empties != 0 {
		t.Errorf("invitations left = %d (empty %d), want 5 (the two empty-after-trim rows deleted, T-041-EMPTY)", invitesLeft, empties)
	}
	var members, assignments, added int
	_ = db.QueryRow(`SELECT count(*) FROM team_members WHERE team_id = $1`, team).Scan(&members)
	_ = db.QueryRow(`SELECT count(*) FROM manager_team_member_assignments WHERE team_id = $1`, team).Scan(&assignments)
	_ = db.QueryRow(`SELECT count(*) FROM team_added_members`).Scan(&added)
	if members != 2 || assignments != 1 || added != 0 {
		t.Errorf("members %d assignments %d added %d, want 2, 1, 0 (no backfill, OQ-8)", members, assignments, added)
	}
	_, err := db.Exec(`INSERT INTO team_invites (team_id, invitee_email, invited_by, token, expires_at) VALUES ($1, 'ann@x.example', $2, $3, now() + interval '1 day')`, team, owner, uuid.NewString())
	if err == nil || !strings.Contains(err.Error(), "team_invites_one_pending") {
		t.Errorf("a second pending row for ann: %v, want the unique index refusal", err)
	}
	// Inviting cy afterwards creates a fresh invitation (AC-20).
	res, err := InviteTeamPersonCtx(context.Background(), db, team, owner, "cy@x.example", "Cy")
	if err != nil || res.Outcome != InviteAddedAndInvited {
		t.Fatalf("invite cy after 041: %+v, %v", res, err)
	}

	// Down and up again.
	to(40)
	var exists bool
	_ = db.QueryRow(`SELECT to_regclass('team_added_members') IS NOT NULL OR EXISTS (SELECT 1 FROM pg_proc WHERE proname = 'paceday_trim_ecma')`).Scan(&exists)
	if exists {
		t.Error("the down step left the table or the function")
	}
	to(41)
}

func TestPAC129_EmptyAfterTrimIsLogged(t *testing.T) {
	// The up step's DO block raises the count (contract §2.2, P2-m1); the
	// migration file carries both RAISE LOG and RAISE NOTICE.
	raw, err := migrationFiles.ReadFile("migrations/041_team_added_members.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"RAISE LOG", "RAISE NOTICE", "paceday_trim_ecma(invitee_email) = ''"} {
		if !strings.Contains(string(raw), s) {
			t.Errorf("041 up lacks %q", s)
		}
	}
	if strings.Contains(string(raw), "E'") {
		t.Error("041 up uses an escape string; the trim set must be built from chr() only (P2-B1)")
	}
}

// --- fixtures ---------------------------------------------------------------

type pac129Team struct {
	db    *sql.DB
	id    uuid.UUID
	owner uuid.UUID
}

func newPAC129Team(t *testing.T, db *sql.DB) pac129Team {
	t.Helper()
	owner := pac71User(t, db, "owner-"+uuid.NewString()[:8]+"@corp.example", "")
	team, err := CreateTeam(db, "T", owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := AddTeamMember(db, team.ID, owner, "owner"); err != nil {
		t.Fatal(err)
	}
	return pac129Team{db: db, id: team.ID, owner: owner}
}

func (p pac129Team) fillTo(t *testing.T, n int) {
	t.Helper()
	mustExec(t, p.db, `INSERT INTO team_added_members (team_id, email, added_by)
		SELECT $1, 'fill' || g || '-' || $3 || '@ext.example', $2 FROM generate_series(1, $4) g`,
		p.id, p.owner, uuid.NewString()[:8], n-1)
}

func (p pac129Team) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := p.db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// waitBlocked waits until n sessions of this database wait on a lock.
func waitBlocked(t *testing.T, db *sql.DB, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var c int
		if err := db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&c); err == nil && c >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the second write never waited on the team lock")
}

// raceAfterLock runs first; when its transaction holds the team lock (op
// firstOp), second starts and is observed waiting on the lock before first
// continues. It returns both errors.
func raceAfterLock(t *testing.T, db *sql.DB, firstOp string, first, second func() error) (error, error) {
	t.Helper()
	var once sync.Once
	var secondErr error
	done := make(chan struct{})
	InstallAfterTeamLock(t, func(op string, _ uuid.UUID) error {
		if op != firstOp {
			return nil
		}
		once.Do(func() {
			go func() { secondErr = second(); close(done) }()
			waitBlocked(t, db, 1)
		})
		return nil
	})
	firstErr := first()
	<-done
	return firstErr, secondErr
}

// --- AC-23: the cap, decided under the lock -----------------------------------

func TestPAC129_AC23_CapRefusesAndConcurrentAddsAtFortyNineGiveOne(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	full := newPAC129Team(t, db)
	full.fillTo(t, 50)
	if _, _, err := AddTeamPersonCtx(ctx, db, full.id, full.owner, "new@ext.example", ""); !errors.Is(err, ErrTeamMemberLimit) {
		t.Errorf("add at 50: %v", err)
	}
	if _, err := InviteTeamPersonCtx(ctx, db, full.id, full.owner, "new@ext.example", ""); !errors.Is(err, ErrTeamMemberLimit) {
		t.Errorf("invite at 50: %v", err)
	}
	if n := full.count(t, `SELECT count(*) FROM team_invites WHERE team_id = $1`, full.id) + full.count(t, `SELECT count(*) FROM team_added_members WHERE team_id = $1 AND email = 'new@ext.example'`, full.id); n != 0 {
		t.Errorf("stored %d rows at the cap", n)
	}

	for _, run := range []int{1, 2, 3} {
		p := newPAC129Team(t, db)
		p.fillTo(t, 49)
		var c1, c2 bool
		e1, e2 := raceAfterLock(t, db, "add",
			func() (err error) { _, c1, err = AddTeamPersonCtx(ctx, db, p.id, p.owner, "a@ext.example", ""); return },
			func() (err error) { _, c2, err = AddTeamPersonCtx(ctx, db, p.id, p.owner, "b@ext.example", ""); return })
		if !(e1 == nil && c1 && errors.Is(e2, ErrTeamMemberLimit) && !c2) {
			t.Errorf("run %d: first (%v, %v), second (%v, %v); want one 201 and one limit", run, c1, e1, c2, e2)
		}
		if n := p.count(t, `SELECT count(*) FROM team_added_members WHERE team_id = $1`, p.id) + 1; n != 50 {
			t.Errorf("run %d: %d members, want 50", run, n)
		}
	}
}

// --- AC-24: one entry per address ----------------------------------------------

func TestPAC129_AC24_ConcurrentAddsOfOneAddressAndAddWithAcceptanceGiveOneEntry(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	p := newPAC129Team(t, db)
	var c1, c2 bool
	e1, e2 := raceAfterLock(t, db, "add",
		func() (err error) {
			_, c1, err = AddTeamPersonCtx(ctx, db, p.id, p.owner, "x@ext.example", "X")
			return
		},
		func() (err error) {
			_, c2, err = AddTeamPersonCtx(ctx, db, p.id, p.owner, " X@Ext.Example ", "Other")
			return
		})
	if e1 != nil || e2 != nil || !c1 || c2 {
		t.Fatalf("adds: (%v %v) (%v %v), want one 201 and one 200", c1, e1, c2, e2)
	}
	if n := p.count(t, `SELECT count(*) FROM team_added_members WHERE team_id = $1`, p.id); n != 1 {
		t.Errorf("%d entries", n)
	}

	// An add of X concurrent with X's acceptance, both orders.
	for _, addFirst := range []bool{true, false} {
		q := newPAC129Team(t, db)
		x := "u-" + uuid.NewString()[:8] + "@corp.example"
		u := pac71User(t, db, x, "")
		inv, err := CreateTeamInvite(db, q.id, x, q.owner)
		if err != nil {
			t.Fatal(err)
		}
		add := func() error { _, _, err := AddTeamPersonCtx(ctx, db, q.id, q.owner, x, ""); return err }
		accept := func() error { return AcceptTeamInviteForUserCtx(ctx, db, inv.ID, q.id, u) }
		var addErr, accErr error
		if addFirst {
			addErr, accErr = raceAfterLock(t, db, "add", add, accept)
		} else {
			accErr, addErr = raceAfterLock(t, db, "accept", accept, add)
		}
		if accErr != nil {
			t.Errorf("addFirst=%v: accept %v", addFirst, accErr)
		}
		if !addFirst && !errors.Is(addErr, ErrPacedayMemberAddress) {
			t.Errorf("acceptance first: add = %v, want the Paceday-member refusal", addErr)
		}
		entries := q.count(t, `SELECT count(*) FROM team_added_members WHERE team_id = $1 AND email = $2`, q.id, x) +
			q.count(t, `SELECT count(*) FROM team_members WHERE team_id = $1 AND user_id = $2`, q.id, u)
		if entries != 1 {
			t.Errorf("addFirst=%v: %d entries for X, want 1", addFirst, entries)
		}
	}
}

// --- AC-30: removal racing acceptance, both paths, both orders ------------------

func TestPAC129_AC30_RemovalRacingAcceptanceAlwaysEndsAbsent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for _, path := range []string{"remove", "manager-remove"} {
		for _, removalFirst := range []bool{true, false} {
			for run := 0; run < 3; run++ {
				p := newPAC129Team(t, db)
				x := "x-" + uuid.NewString()[:8] + "@corp.example"
				u := pac71User(t, db, x, "")
				if _, _, err := AddTeamPersonCtx(ctx, db, p.id, p.owner, x, "X"); err != nil {
					t.Fatal(err)
				}
				res, err := InviteTeamPersonCtx(ctx, db, p.id, p.owner, x, "")
				if err != nil {
					t.Fatal(err)
				}
				mustExec(t, db, `INSERT INTO team_invites (team_id, invitee_email, invited_by, token, status, expires_at) VALUES ($1, $2, $3, $4, 'expired', now())`, p.id, x, p.owner, uuid.NewString())
				remove := func() error {
					if path == "remove" {
						return RemoveTeamPersonCtx(ctx, db, p.id, p.owner, x)
					}
					return RemoveManagerTeamPersonCtx(ctx, db, p.owner, p.id, x)
				}
				accept := func() error { return AcceptTeamInviteForUserCtx(ctx, db, res.Invitation.ID, p.id, u) }
				var remErr, accErr error
				if removalFirst {
					remErr, accErr = raceAfterLock(t, db, path, remove, accept)
				} else {
					accErr, remErr = raceAfterLock(t, db, "accept", accept, remove)
				}
				label := fmt.Sprintf("%s removalFirst=%v run %d", path, removalFirst, run)
				if remErr != nil {
					t.Errorf("%s: removal %v", label, remErr)
				}
				if removalFirst && !errors.Is(accErr, ErrInviteGone) {
					t.Errorf("%s: acceptance = %v, want invite not found (404)", label, accErr)
				}
				if !removalFirst && accErr != nil {
					t.Errorf("%s: acceptance = %v, want success (204)", label, accErr)
				}
				left := p.count(t, `SELECT count(*) FROM team_members WHERE team_id = $1 AND user_id = $2`, p.id, u) +
					p.count(t, `SELECT count(*) FROM team_added_members WHERE team_id = $1`, p.id) +
					p.count(t, `SELECT count(*) FROM team_invites WHERE team_id = $1`, p.id)
				if left != 0 {
					t.Errorf("%s: %d rows of X left on T, want none", label, left)
				}
			}
		}
	}
}

// A PAC-25 removal racing the address removal: the team always keeps an owner.
func TestPAC129_AC30_PAC25RemovalRacingKeepsAnOwner(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for run := 0; run < 5; run++ {
		p := newPAC129Team(t, db)
		o2Email := "o2-" + uuid.NewString()[:8] + "@corp.example"
		o2 := pac71User(t, db, o2Email, "")
		mustExec(t, db, `INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $2, 'owner')`, p.id, o2)
		var wg sync.WaitGroup
		errs := make([]error, 2)
		wg.Add(2)
		go func() { defer wg.Done(); errs[0] = RemoveTeamMemberKeepingOwnerCtx(ctx, db, p.id, p.owner) }()
		go func() { defer wg.Done(); errs[1] = RemoveTeamPersonCtx(ctx, db, p.id, o2, o2Email) }()
		wg.Wait()
		if n := p.count(t, `SELECT count(*) FROM team_members WHERE team_id = $1 AND role = 'owner'`, p.id); n != 1 {
			t.Fatalf("run %d: %d owners (%v), want exactly one kept", run, n, errs)
		}
		failed := 0
		for _, e := range errs {
			if errors.Is(e, ErrLastOwner) {
				failed++
			} else if e != nil {
				t.Errorf("run %d: %v", run, e)
			}
		}
		if failed != 1 {
			t.Errorf("run %d: %d refusals, want one", run, failed)
		}
	}
}

// --- AC-19: a fault after the membership step changes nothing --------------------

func TestPAC129_AC19_FaultAfterMembershipStepRollsBack(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	p := newPAC129Team(t, db)
	x := "x-" + uuid.NewString()[:8] + "@corp.example"
	u := pac71User(t, db, x, "")
	res, err := InviteTeamPersonCtx(ctx, db, p.id, p.owner, x, "X")
	if err != nil {
		t.Fatal(err)
	}
	InstallAfterAcceptMembershipStep(t, func(uuid.UUID) error { return errors.New("injected") })
	if err := AcceptTeamInviteForUserCtx(ctx, db, res.Invitation.ID, p.id, u); err == nil {
		t.Fatal("the fault was not reported")
	}
	if n := p.count(t, `SELECT count(*) FROM team_added_members WHERE team_id = $1 AND email = $2`, p.id, x); n != 1 {
		t.Error("X is no longer added")
	}
	if n := p.count(t, `SELECT count(*) FROM team_members WHERE team_id = $1 AND user_id = $2`, p.id, u); n != 0 {
		t.Error("a membership survived the fault")
	}
	if n := p.count(t, `SELECT count(*) FROM team_invites WHERE id = $1 AND status = 'pending'`, res.Invitation.ID); n != 1 {
		t.Error("the invitation is no longer pending")
	}
}

// --- AC-34: PAC-71 classification; a non-creator owner's deletion ----------------

func TestPAC129_AC34_AddedByIsClassifiedAndSetNullOnDelete(t *testing.T) {
	db := openTestDB(t)
	refs, err := UserForeignKeys(db)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range refs {
		found = found || r == "team_added_members.added_by"
	}
	if !found || !classifiedUserRefs["team_added_members.added_by"] {
		t.Fatalf("team_added_members.added_by found=%v classified=%v", found, classifiedUserRefs["team_added_members.added_by"])
	}
	p := newPAC129Team(t, db)
	o2 := pac71User(t, db, "o2-"+uuid.NewString()[:8]+"@corp.example", "")
	mustExec(t, db, `INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $2, 'owner')`, p.id, o2)
	if _, _, err := AddTeamPersonCtx(context.Background(), db, p.id, o2, "x@ext.example", "X"); err != nil {
		t.Fatal(err)
	}
	if k, err := FirstNonEmptyKind(db, o2); err != nil || k == "" {
		t.Errorf("an account that added someone reads empty (%q, %v)", k, err)
	}
	mustExec(t, db, `DELETE FROM users WHERE id = $1`, o2)
	a, err := GetAddedMember(db, p.id, "x@ext.example")
	if err != nil {
		t.Fatalf("X left the team: %v", err)
	}
	if a.AddedBy != nil {
		t.Error("added_by still names the deleted owner")
	}
	if _, ok, err := AddingOwner(db, a); ok || err != nil {
		t.Errorf("X still has an adding owner (%v)", err)
	}
}

// A repeat add re-points added_by when the adder is no longer an owner (OQ-1).
func TestPAC129_RepeatAddRepointsAnOrphanEntry(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	p := newPAC129Team(t, db)
	mustExec(t, db, `INSERT INTO team_added_members (team_id, email, display_name, added_by) VALUES ($1, 'x@ext.example', 'X', NULL)`, p.id)
	a, created, err := AddTeamPersonCtx(ctx, db, p.id, p.owner, "x@ext.example", "New name")
	if err != nil || created {
		t.Fatalf("repeat add: %v created=%v", err, created)
	}
	if a.AddedBy == nil || *a.AddedBy != p.owner || a.DisplayName != "X" {
		t.Errorf("entry = %+v, want added_by the caller and the name kept", a)
	}
}

// The storage write normalises whatever the caller passes (M-2).
func TestPAC129_StorageStoresTheNormalisedAddress(t *testing.T) {
	db := openTestDB(t)
	p := newPAC129Team(t, db)
	inv, err := CreateTeamInvite(db, p.id, "\uFEFF Henry@Corp.Example\t", p.owner)
	if err != nil || inv.InviteeEmail != "henry@corp.example" {
		t.Fatalf("invite = %+v, %v", inv, err)
	}
	a, _, err := AddTeamPersonCtx(context.Background(), db, p.id, p.owner, " Pat@Ext.Example\u0085", "  Pat  ")
	if err != nil || a.Email != "pat@ext.example" || a.DisplayName != "Pat" {
		t.Fatalf("added = %+v, %v", a, err)
	}
	people, err := ListAddedMembers(db, p.id)
	if err != nil || len(people) != 1 || people[0].Email != "pat@ext.example" {
		t.Fatalf("list = %v, %v", people, err)
	}
}
