package main

// PAC-134 AC-9 (an address held by accounts) and AC-14 (migrations, old
// schema).

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
)

// rowsOf is a table's rows (JSON text) matching where, sorted.
func (p *p134) rowsOf(table, where string, args ...any) string {
	p.t.Helper()
	rows, err := p.db.Query(`SELECT row_to_json(x)::text FROM `+table+` x WHERE `+where+` ORDER BY 1`, args...)
	if err != nil {
		p.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var r string
		_ = rows.Scan(&r)
		out = append(out, r)
	}
	return strings.Join(out, "\n")
}

// AC-9: an address held by accounts (two case variants: one person).
func TestPAC134_AC9_AccountHeldAddress(t *testing.T) {
	p := newP134(t)
	p.fixture()
	a1 := p.user("Pat@Ext.Example")
	a2 := p.user(pac134X) // (h): the other case variant
	b := p.user("b@pac134.example")
	other := p.user("o2@pac134.example")
	// (a) a keyed link creation by A1 listing its own address.
	la := p.link(a1, "live", "A1 link")
	p.keyedCreation(a1, la, "pac134-own-key-000001", `{"invited_count":0,"not_added_count":1,"addresses":[{"email":"Pat@Ext.Example","outcome":"own_address"}],"removed":[]}`)
	// (b) a booking by A1's address with notes on A1's own link.
	p.booking(la, pac134Name, "Pat@Ext.Example", "own notes", "confirmed", "")
	// (c) A1 as manager: its own roster entry, 1:1 and cached figure.
	p.exec(`INSERT INTO manager_team_members (manager_user_id, member_email) VALUES ($1, $2)`, a1, pac134X)
	p.exec(`INSERT INTO one_on_one_occurrences (manager_user_id, member_email, calendar_event_id, occurred_at) VALUES ($1, $2, 'own', now())`, a1, pac134X)
	p.exec(`INSERT INTO team_analytics_cache (manager_user_id, member_email, week_start) VALUES ($1, $2, '2026-11-09')`, a1, pac134X)
	// (d) an added person and a pending invitation authored by A1.
	td := p.team(a1)
	p.exec(`INSERT INTO team_added_members (team_id, email, added_by) VALUES ($1, $2, $3)`, td, pac134X, a1)
	p.exec(`INSERT INTO team_invites (team_id, invitee_email, invited_by, token, expires_at) VALUES ($1, $2, $3, 'tok-d', now() + interval '1 day')`, td, pac134X, a1)
	// (e), (f) on TC co-owned by A1 and B, authored by B; (g) author deleted.
	tc := p.team(a1)
	p.exec(`INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $2, 'owner')`, tc, b)
	p.exec(`INSERT INTO team_added_members (team_id, email, added_by) VALUES ($1, $2, $3)`, tc, pac134X, b)
	p.exec(`INSERT INTO team_invites (team_id, invitee_email, invited_by, token, expires_at) VALUES ($1, $2, $3, 'tok-f', now() + interval '1 day')`, tc, pac134X, b)
	tg := p.team(other)
	p.exec(`INSERT INTO team_added_members (team_id, email, added_by) VALUES ($1, $2, NULL)`, tg, pac134X)
	// (h) A2 authors an added person of X (on a team of another owner: TA
	// already holds X under its (team, email) key).
	th := p.team(other)
	p.exec(`INSERT INTO team_added_members (team_id, email, added_by) VALUES ($1, $2, $3)`, th, pac134X, a2)

	ids := []string{a1.String(), a2.String()}
	if ids[1] < ids[0] {
		ids[0], ids[1] = ids[1], ids[0]
	}
	accounts := "accounts=" + ids[0] + "," + ids[1]
	accountTables := func() string {
		return p.rowsOf("users", "true") + p.rowsOf("settings", "true") + p.rowsOf("user_identities", "true") +
			p.rowsOf("team_members", "true") + p.rowsOf("scheduling_links", "true") +
			p.rowsOf("scheduling_link_hosts", "user_id IS NOT NULL")
	}
	own := func() string {
		return p.rowsOf("scheduling_link_create_keys", "owner_user_id = $1", a1) + p.rowsOf("bookings", "link_id = $1", la) +
			p.rowsOf("manager_team_members", "manager_user_id = $1", a1) + p.rowsOf("one_on_one_occurrences", "manager_user_id = $1", a1) +
			p.rowsOf("team_analytics_cache", "manager_user_id = $1", a1) + p.rowsOf("team_added_members", "team_id = $1", td) +
			p.rowsOf("team_invites", "team_id = $1", td)
	}
	hRow := func() string { return p.rowsOf("team_added_members", "team_id = $1", th) }

	// Without the acknowledgement: exit 3, the ids, nothing changed, no record.
	before := p.snapshot()
	code, out, errw := p.run(pac134X+"\n", pac134Args...)
	if code != exitAccountHeld || out != accounts+" acknowledged=false\n" || !strings.Contains(errw, "-account-held-acknowledged") || leaks(out+errw) != "" {
		t.Fatalf("exit %d\n%s\n%s", code, out, errw)
	}
	if gone, added := before.diff(p.snapshot()); len(gone) != 0 || len(added) != 0 {
		t.Fatalf("a refused erasure changed data: %v %v", gone, added)
	}
	// The dry run without it: exit 3, counts and ids, only its record.
	code, out, errw = p.run(pac134X+"\n", pac134DryArgs()...)
	if code != exitAccountHeld || !strings.Contains(out, accounts+" acknowledged=false\n") || !strings.Contains(out, "place=bookings would_erase=3 kept_account_own=1") ||
		!strings.Contains(errw, "needs -account-held-acknowledged") || leaks(out+errw) != "" {
		t.Fatalf("dry run: exit %d\n%s\n%s", code, out, errw)
	}
	if gone, added := before.diff(p.snapshot()); len(gone) != 0 || len(added) != 1 || len(added["operator_audit"]) != 1 {
		t.Fatalf("the dry run changed %v added %v", gone, added)
	}

	// With the acknowledgement.
	accountsBefore, ownBefore, hBefore := accountTables(), own(), hRow()
	if p.resolve(a1, "pac134-own-key-000001") != storage.LinkCreateKeyReplay {
		t.Fatal("(a) does not replay before the erasure")
	}
	ack := append([]string{"-account-held-acknowledged"}, pac134Args...)
	code, out, errw = p.run(pac134X+"\n", ack...)
	if code != exitOK || errw != "" || !strings.Contains(out, accounts+" acknowledged=true\n") || leaks(out) != "" {
		t.Fatalf("acknowledged: exit %d\n%s\n%s", code, out, errw)
	}
	erased := map[string]int64{}
	for k, v := range pac134Counts {
		erased[k] = v
	}
	erased["team_added_members"] += 2 // (e), (g)
	erased["team_invites"]++          // (f)
	wantCounts(t, out, "erased", erased, map[string]int64{
		"team_added_members": 2, "team_invites": 1, "manager_team_members": 1, "one_on_one_occurrences": 1,
		"team_analytics_cache": 1, "scheduling_link_create_keys": 1, "bookings": 1,
	})
	if accountTables() != accountsBefore || own() != ownBefore || hRow() != hBefore {
		t.Fatal("an account, its own records or (h) changed")
	}
	if p.count(`SELECT count(*) FROM team_added_members WHERE team_id IN ($1, $2)`, tc, tg) != 0 || p.count(`SELECT count(*) FROM team_invites WHERE team_id = $1`, tc) != 0 {
		t.Error("(e), (f) or (g) was kept")
	}
	if p.resolve(a1, "pac134-own-key-000001") != storage.LinkCreateKeyReplay {
		t.Error("(a) no longer replays")
	}
	d := auditDetails(t, p, auditID(t, out))
	if fmt.Sprint(d["accounts"]) != fmt.Sprint([]any{ids[0], ids[1]}) || d["account_acknowledged"] != true {
		t.Errorf("record %v", d)
	}

	// free-address on A1, then a rerun: (a)-(d) are erased, (h) kept, A1
	// unchanged by the rerun.
	if _, err := storage.FreeAddress(context.Background(), p.db, a1, false, storage.OperatorRun{Operator: "op", Reason: "PAC-134 test"}); err != nil {
		t.Fatal(err)
	}
	a1Row := p.rowsOf("users", "id = $1", a1)
	code, out, errw = p.run(pac134X+"\n", ack...)
	if code != exitOK || !strings.Contains(out, "accounts="+a2.String()+" acknowledged=true\n") {
		t.Fatalf("rerun: exit %d\n%s\n%s", code, out, errw)
	}
	wantCounts(t, out, "erased", map[string]int64{
		"team_added_members": 1, "team_invites": 1, "manager_team_members": 1, "one_on_one_occurrences": 1,
		"team_analytics_cache": 1, "scheduling_link_create_keys": 1, "bookings": 1,
	}, map[string]int64{"team_added_members": 1})
	if p.rowsOf("users", "id = $1", a1) != a1Row || hRow() != hBefore {
		t.Error("the rerun changed the freed account or (h)")
	}
	if p.resolve(a1, "pac134-own-key-000001") != storage.LinkCreateKeyReused {
		t.Error("(a) still replays after the rerun")
	}
}

// AC-14: the app serving with a migration pending refuses the erasure
// (exit 1, nothing changed); the dry run on a schema older than 045 exits
// 1, says so, and applies nothing.
func TestPAC134_AC14_MigrationsAndOldSchema(t *testing.T) {
	want, err := storage.EraseAddressMigrationVersion()
	if err != nil {
		t.Fatal(err)
	}
	p := newP116(t, want-1)
	p.heartbeat(5 * time.Second)
	run := func(args ...string) (int, string, string) {
		var out, errw strings.Builder
		code := runEraseAddress(context.Background(), args, p.envWith(nil), strings.NewReader(pac134X+"\n"), &out, &errw)
		p.settle()
		return code, out.String(), errw.String()
	}
	code, out, errw := run(pac134Args...)
	if code != exitFailed || out != "" || !strings.HasPrefix(errw, "erase-address: failed: migration refused: ") || !p.servingText(errw) {
		t.Fatalf("serving: exit %d\n%s\n%s", code, out, errw)
	}
	p.wantVersion(want-1, false)
	if p.audits() != 0 {
		t.Fatal("a refused run wrote a record")
	}
	code, out, errw = run(pac134DryArgs()...)
	if code != exitFailed || out != "" || errw != fmt.Sprintf("erase-address: failed: schema too old (at %d, needs %d, PAC-134); no data changed and no migration applied\n", want-1, want) {
		t.Fatalf("dry run on %d: exit %d\n%s\n%s", want-1, code, out, errw)
	}
	p.wantVersion(want-1, false)
}
