package storage

// PAC-134, independent review of #326: tests that pin rules the review's
// surviving mutants showed unpinned (MX1, MX2, MX3, MX7, MX8, MX16, MX17).

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
)

// MX3: the run's transaction carries idle_in_transaction_session_timeout =
// 1 s: a client that stops between two lock-phase statements has its session
// ended by the server about 1 s later, and the locks go with it.
func TestPAC134_IdleClientIsEndedAfterOneSecond(t *testing.T) {
	db := openTestDB(t)
	s := p134Seed(t, db, "pat@ext.example")
	release := make(chan struct{})
	blocked := make(chan time.Time, 1)
	InstallEraseStatement(t, func(name, _ string, _ []any) {
		if name == "lock_roster" {
			select {
			case blocked <- time.Now():
				<-release
			default:
			}
		}
	})
	done := make(chan error, 1)
	go func() { _, err := p134Run(db, "pat@ext.example", nil); done <- err }()
	var at time.Time
	select {
	case at = <-blocked:
	case <-time.After(10 * time.Second):
		t.Fatal("the run never reached lock_roster")
	}
	// lock_teams has locked the seed's team; the frozen client holds it
	// until the server ends the idle session.
	freed := time.Duration(0)
	for time.Since(at) < 4*time.Second {
		probe, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		_, err = probe.Exec(`SELECT 1 FROM teams WHERE id = $1 FOR UPDATE NOWAIT`, s.team)
		_ = probe.Rollback()
		if err == nil {
			freed = time.Since(at)
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	close(release)
	err := <-done
	if freed == 0 || freed < 900*time.Millisecond || freed > 2500*time.Millisecond {
		t.Fatalf("the team row was freed after %s (want about 1 s)", freed)
	}
	if err == nil || p134Remaining(t, db, "pat@ext.example") != 10 || p134Audits(t, db) != 0 {
		t.Fatalf("the ended run: err %v", err)
	}
}

// MX7: every multi-row statement of the tool locks its rows in the key
// order of contract md §6.1. Two of X's rows; the one whose key sorts last
// is held by another transaction; while the tool waits on it, the row that
// sorts first is already locked by the tool (a NOWAIT probe fails).
func TestPAC134_ToolLocksRowsInKeyOrder(t *testing.T) {
	const x = "pat@ext.example"
	sorted := func() (uuid.UUID, uuid.UUID) {
		a, b := uuid.New(), uuid.New()
		if b.String() < a.String() {
			a, b = b, a
		}
		return a, b
	}
	type setup struct {
		lockLast, probeFirst string
		first, last          any
	}
	cases := []struct {
		name string
		set  func(t *testing.T, db *DBHandle) setup
	}{
		{"teams", func(t *testing.T, h *DBHandle) setup {
			a, b := sorted()
			for _, id := range []uuid.UUID{b, a} {
				p134Exec(t, h.db, `INSERT INTO teams (id, name, created_by) VALUES ($1, 'T', $2)`, id, h.owner)
				p134Exec(t, h.db, `INSERT INTO team_added_members (team_id, email, added_by) VALUES ($1, $2, $3)`, id, x, h.owner)
			}
			q := `SELECT 1 FROM teams WHERE id = $1 FOR UPDATE`
			return setup{q, q + " NOWAIT", a, b}
		}},
		{"team_added_members", func(t *testing.T, h *DBHandle) setup {
			a, b := sorted()
			for _, id := range []uuid.UUID{b, a} {
				p134Exec(t, h.db, `INSERT INTO teams (id, name, created_by) VALUES ($1, 'T', $2)`, id, h.owner)
				p134Exec(t, h.db, `INSERT INTO team_added_members (team_id, email, added_by) VALUES ($1, $2, $3)`, id, x, h.owner)
			}
			q := `SELECT 1 FROM team_added_members WHERE team_id = $1 AND email = '` + x + `' FOR UPDATE`
			return setup{q, q + " NOWAIT", a, b}
		}},
		{"team_invites", func(t *testing.T, h *DBHandle) setup {
			a, b := sorted()
			team := p134ID(t, h.db, `INSERT INTO teams (name, created_by) VALUES ('T', $1) RETURNING id`, h.owner)
			for i, id := range []uuid.UUID{b, a} {
				p134Exec(t, h.db, `INSERT INTO team_invites (id, team_id, invitee_email, invited_by, token, status, expires_at) VALUES ($1, $2, $3, $4, $5, $6, now())`,
					id, team, x, h.owner, uuid.NewString(), []string{"accepted", "declined"}[i])
			}
			q := `SELECT 1 FROM team_invites WHERE id = $1 FOR UPDATE`
			return setup{q, q + " NOWAIT", a, b}
		}},
		{"manager_team_members", func(t *testing.T, h *DBHandle) setup {
			a, b := sorted()
			for _, id := range []uuid.UUID{b, a} {
				p134Exec(t, h.db, `INSERT INTO users (id, email, name) VALUES ($1, $2, 'M')`, id, id.String()+"@m.example")
				p134Exec(t, h.db, `INSERT INTO manager_team_members (manager_user_id, member_email) VALUES ($1, $2)`, id, x)
			}
			q := `SELECT 1 FROM manager_team_members WHERE manager_user_id = $1 FOR UPDATE`
			return setup{q, q + " NOWAIT", a, b}
		}},
		{"manager_team_member_assignments", func(t *testing.T, h *DBHandle) setup {
			a, b := sorted()
			p134Exec(t, h.db, `INSERT INTO manager_team_members (manager_user_id, member_email) VALUES ($1, $2)`, h.owner, x)
			for _, id := range []uuid.UUID{b, a} {
				p134Exec(t, h.db, `INSERT INTO teams (id, name, created_by) VALUES ($1, 'T', $2)`, id, h.owner)
				p134Exec(t, h.db, `INSERT INTO manager_team_member_assignments (manager_user_id, team_id, member_email) VALUES ($1, $2, $3)`, h.owner, id, x)
			}
			q := `SELECT 1 FROM manager_team_member_assignments WHERE team_id = $1 FOR UPDATE`
			return setup{q, q + " NOWAIT", a, b}
		}},
		{"one_on_one_occurrences", func(t *testing.T, h *DBHandle) setup {
			a, b := sorted()
			for i, id := range []uuid.UUID{b, a} {
				p134Exec(t, h.db, `INSERT INTO one_on_one_occurrences (id, manager_user_id, member_email, calendar_event_id, occurred_at) VALUES ($1, $2, $3, $4, now())`, id, h.owner, x, fmt.Sprint("ev", i))
			}
			q := `SELECT 1 FROM one_on_one_occurrences WHERE id = $1 FOR UPDATE`
			return setup{q, q + " NOWAIT", a, b}
		}},
		{"team_analytics_cache", func(t *testing.T, h *DBHandle) setup {
			a, b := sorted()
			for i, id := range []uuid.UUID{b, a} {
				p134Exec(t, h.db, `INSERT INTO team_analytics_cache (id, manager_user_id, member_email, week_start) VALUES ($1, $2, $3, $4)`, id, h.owner, x, []string{"2026-11-02", "2026-11-09"}[i])
			}
			q := `SELECT 1 FROM team_analytics_cache WHERE id = $1 FOR UPDATE`
			return setup{q, q + " NOWAIT", a, b}
		}},
		{"scheduling_links", func(t *testing.T, h *DBHandle) setup {
			a, b := sorted()
			for _, id := range []uuid.UUID{b, a} {
				p134Exec(t, h.db, `INSERT INTO scheduling_links (id, owner_user_id, slug, title) VALUES ($1, $2, $3, 'L')`, id, h.owner, "s-"+id.String())
				p134Exec(t, h.db, `INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time) VALUES ($1, 'P', $2, now(), now())`, id, x)
			}
			q := `SELECT 1 FROM scheduling_links WHERE id = $1 FOR UPDATE`
			return setup{q, q + " NOWAIT", a, b}
		}},
		{"scheduling_link_hosts", func(t *testing.T, h *DBHandle) setup {
			a, b := sorted()
			for _, id := range []uuid.UUID{b, a} {
				link := p134ID(t, h.db, `INSERT INTO scheduling_links (owner_user_id, slug, title) VALUES ($1, $2, 'L') RETURNING id`, h.owner, "h-"+id.String())
				p134Exec(t, h.db, `INSERT INTO scheduling_link_hosts (id, link_id, invitee_email, status) VALUES ($1, $2, $3, 'pending')`, id, link, x)
			}
			q := `SELECT 1 FROM scheduling_link_hosts WHERE id = $1 FOR UPDATE`
			return setup{q, q + " NOWAIT", a, b}
		}},
		{"bookings", func(t *testing.T, h *DBHandle) setup {
			a, b := sorted()
			link := p134ID(t, h.db, `INSERT INTO scheduling_links (owner_user_id, slug, title) VALUES ($1, 'b-link', 'L') RETURNING id`, h.owner)
			for _, id := range []uuid.UUID{b, a} {
				p134Exec(t, h.db, `INSERT INTO bookings (id, link_id, booker_name, booker_email, start_time, end_time) VALUES ($1, $2, 'P', $3, now(), now())`, id, link, x)
			}
			q := `SELECT 1 FROM bookings WHERE id = $1 FOR UPDATE`
			return setup{q, q + " NOWAIT", a, b}
		}},
		{"scheduling_link_create_keys", func(t *testing.T, h *DBHandle) setup {
			keys := []string{"pac134-order-key-aaaaaa", "pac134-order-key-bbbbbb"}
			for _, k := range []string{keys[1], keys[0]} {
				link := p134ID(t, h.db, `INSERT INTO scheduling_links (owner_user_id, slug, title) VALUES ($1, $2, 'L') RETURNING id`, h.owner, "k-"+k)
				p134Exec(t, h.db, `INSERT INTO scheduling_link_create_keys (owner_user_id, idempotency_key, link_id, request_sha256, co_host_changes)
					VALUES ($1, $2, $3, sha256('k'::bytea), jsonb_build_object('addresses', jsonb_build_array(jsonb_build_object('email', $4::text)), 'removed', '[]'::jsonb))`, h.owner, k, link, x)
			}
			q := `SELECT 1 FROM scheduling_link_create_keys WHERE owner_user_id = '` + h.owner.String() + `' AND idempotency_key = $1 FOR UPDATE`
			return setup{q, q + " NOWAIT", keys[0], keys[1]}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := openTestDB(t)
			h := &DBHandle{db: db, owner: p134ID(t, db, `INSERT INTO users (email, name) VALUES ('owner@order.example', 'O') RETURNING id`)}
			st := c.set(t, h)
			ordered := p134LockOrderProbe(t, db, st.lockLast, st.probeFirst, []any{st.first, st.last}, func() error {
				_, err := p134Run(db, x, nil)
				return err
			})
			if !ordered {
				t.Fatal("while waiting on the last row, the tool did not hold the first: rows locked out of key order")
			}
		})
	}
}

// DBHandle is a test database with an owner account.
type DBHandle struct {
	db    *sql.DB
	owner uuid.UUID
}

// MX1, MX2: a T1 or T2 row found as a third-party copy whose author becomes
// a matching account before the lock phase (W1's UPDATE of added_by; the
// same for an invitation's author) is re-checked under its lock and kept.
func TestPAC134_OwnershipRecheckedUnderLock(t *testing.T) {
	db := openTestDB(t)
	s := p134Seed(t, db, "pat@ext.example")
	acct := p134ID(t, db, `INSERT INTO users (email, name) VALUES ('Pat@Ext.Example', 'A') RETURNING id`)
	moved := false
	InstallEraseAfterStatement(t, func(ctx context.Context, name string) error {
		if name == "counts" && !moved {
			moved = true
			if _, err := db.ExecContext(ctx, `UPDATE team_added_members SET added_by = $1 WHERE team_id = $2`, acct, s.team); err != nil {
				return err
			}
			_, err := db.ExecContext(ctx, `UPDATE team_invites SET invited_by = $1 WHERE team_id = $2`, acct, s.team)
			return err
		}
		return nil
	})
	res, err := p134Run(db, "pat@ext.example", func(o *EraseAddressOptions) { o.AccountHeldAcknowledged = true })
	if err != nil {
		t.Fatal(err)
	}
	got := p134Erased(res)
	if got["team_added_members"] != 0 || got["team_invites"] != 0 ||
		p134Count(t, db, `SELECT count(*) FROM team_added_members WHERE team_id = $1`, s.team) != 1 ||
		p134Count(t, db, `SELECT count(*) FROM team_invites WHERE team_id = $1`, s.team) != 1 {
		t.Fatalf("a row that became the account's own before its lock was erased: %v", got)
	}
}

// MX8: L-3's confirm processes the distinct addresses in byte order: given
// them in reverse, it holds the first while it waits on the last.
func TestPAC134_L3_ConfirmLocksInByteOrder(t *testing.T) {
	db := openTestDB(t)
	mgr := p134ID(t, db, `INSERT INTO users (email, name) VALUES ('mgr@l3.example', 'M') RETURNING id`)
	team := p134ID(t, db, `INSERT INTO teams (name, created_by) VALUES ('T', $1) RETURNING id`, mgr)
	emails := []string{"aaa@l3.example", "bbb@l3.example"}
	for _, e := range emails {
		p134Exec(t, db, `INSERT INTO manager_team_members (manager_user_id, member_email) VALUES ($1, $2)`, mgr, e)
	}
	q := `SELECT 1 FROM manager_team_members WHERE manager_user_id = '` + mgr.String() + `' AND member_email = $1 FOR UPDATE`
	reversed := append([]string(nil), emails...)
	sort.Sort(sort.Reverse(sort.StringSlice(reversed)))
	if !p134LockOrderProbe(t, db, q, q+" NOWAIT", []any{emails[0], emails[1]}, func() error {
		_, _, _, err := ConfirmManagerTeamMembers(db, mgr, team, reversed)
		return err
	}) {
		t.Fatal("confirm did not hold the first address while waiting on the last")
	}
}

// MX16, MX17: an earlier operator record is kept and counted when either
// its account_email or its details.freed_address matches; one that matches
// neither is not counted.
func TestPAC134_T10_EitherHalfCounts(t *testing.T) {
	db := openTestDB(t)
	p134Exec(t, db, `INSERT INTO operator_audit (action, operator, reason, account_email, details) VALUES
		('free_address', 'o', 'r', 'Pat@Ext.Example', '{}'),
		('free_address', 'o', 'r', 'other@x.example', '{"freed_address": "PAT@ext.example"}'),
		('free_address', 'o', 'r', 'other@x.example', '{"freed_address": "pat2@ext.example"}')`)
	res, err := p134Run(db, "pat@ext.example", func(o *EraseAddressOptions) { o.DryRun = true })
	if err != nil {
		t.Fatal(err)
	}
	if res.KeptOperatorRecords != 2 {
		t.Fatalf("kept %d operator records, want 2", res.KeptOperatorRecords)
	}
}

// MX4 (D-4): a pending co-host invitation of the address on a link owned by
// a matching account is that account's own record: kept and counted, never
// erased. The product refuses such an invitation since PAC-149; it can exist
// from before, or by direct insert.
func TestPAC134_T7OnAMatchingAccountsLinkIsKept(t *testing.T) {
	db := openTestDB(t)
	acct := p134ID(t, db, `INSERT INTO users (email, name) VALUES ('Pat@Ext.Example', 'A') RETURNING id`)
	link := p134ID(t, db, `INSERT INTO scheduling_links (owner_user_id, slug, title) VALUES ($1, 'own-link', 'L') RETURNING id`, acct)
	p134Exec(t, db, `INSERT INTO scheduling_link_hosts (link_id, invitee_email, status) VALUES ($1, 'pat@ext.example', 'pending')`, link)
	res, err := p134Run(db, "pat@ext.example", func(o *EraseAddressOptions) { o.AccountHeldAcknowledged = true })
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range res.Places {
		if p.Name == "scheduling_link_hosts" && (p.Erased != 0 || p.KeptAccountOwn != 1) {
			t.Fatalf("scheduling_link_hosts: %+v", p)
		}
	}
	if p134Count(t, db, `SELECT count(*) FROM scheduling_link_hosts WHERE link_id = $1`, link) != 1 {
		t.Fatal("the account's own invitation was erased")
	}
}
