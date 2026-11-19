package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// PAC-149 migration 044 (docs/specs/PAC-149.md §3.5, AC-10, D-8, D-17;
// contracts/features/PAC-149.md §4): pending co-host rows become address
// invitations in place; rows on deleted links, for freed placeholders, the
// owner's own pending row and the clashes are dropped; accepted and declined
// rows are not touched; the down step restores holders only and never fails
// on UNIQUE (link_id, user_id).

const pac149MigrationFile = "migrations/044_cohost_address_invitations.up.sql"

// pac149Fix is AC-10's fixture (plus contract revision 2's L8 when asked),
// written at version 43.
type pac149Fix struct {
	o, p, p2, k, kk, f, q, r, k1, k2, o8 uuid.UUID
	l1, l2, l3, l4, l5, l6, l7, l8       uuid.UUID
	rows                                 map[string]uuid.UUID
	times                                map[string]time.Time
}

func pac149Seed(t *testing.T, dsn string, withL8 bool) pac149Fix {
	t.Helper()
	f := pac149Fix{rows: map[string]uuid.UUID{}, times: map[string]time.Time{}}
	base := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	minute := 0
	pac148Exec(t, dsn, func(q func(string, ...any) *sql.Row, exec func(string, ...any)) {
		user := func(email, standing string) uuid.UUID {
			var id uuid.UUID
			if err := q(`INSERT INTO users (email, name, address_standing) VALUES ($1, 'N', $2) RETURNING id`, email, standing).Scan(&id); err != nil {
				t.Fatalf("user %s: %v", email, err)
			}
			return id
		}
		bind := func(id uuid.UUID) {
			exec(`INSERT INTO user_identities (user_id, provider, tenant, subject, evidence, bound_by)
				VALUES ($1, 'google', '', $2, 'authoritative', 'created')`, id, id.String())
		}
		link := func(owner uuid.UUID, slug string, ownerRow bool) uuid.UUID {
			var id uuid.UUID
			if err := q(`INSERT INTO scheduling_links (owner_user_id, slug, title) VALUES ($1, $2, 'L') RETURNING id`, owner, slug).Scan(&id); err != nil {
				t.Fatal(err)
			}
			if ownerRow {
				exec(`INSERT INTO scheduling_link_hosts (link_id, user_id, status, invited_at) VALUES ($1, $2, 'accepted', $3)`, id, owner, base.Add(-time.Hour))
			}
			return id
		}
		row := func(name string, linkID, userID uuid.UUID, status string) {
			minute++
			at := base.Add(time.Duration(minute) * time.Minute)
			var responded any
			if status != "pending" {
				responded = at.Add(30 * time.Second)
			}
			var id uuid.UUID
			if err := q(`INSERT INTO scheduling_link_hosts (link_id, user_id, status, invited_at, responded_at) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
				linkID, userID, status, at, responded).Scan(&id); err != nil {
				t.Fatalf("row %s: %v", name, err)
			}
			f.rows[name], f.times[name] = id, at
		}
		f.o = user("pac149-o@x.example", AddressDomainConfirmed)
		bind(f.o)
		f.p = user("p@x.example", AddressDomainConfirmed)
		bind(f.p)
		f.p2 = user("p2@x.example", AddressUnconfirmed) // unbound legacy
		f.k = user("Kim@x.example", AddressDomainConfirmed)
		f.kk = user("kim@x.example", AddressDomainConfirmed)
		f.f = user("pac149-freed@x.example", AddressUnconfirmed)
		exec(`UPDATE users SET email = 'freed+' || id::text || '@paceday.invalid' WHERE id = $1`, f.f)
		f.q = user("q@x.example", AddressDomainConfirmed)
		f.r = user("r@x.example", AddressDomainConfirmed)
		f.k1 = user("Kim@y.example", AddressDomainConfirmed)
		f.k2 = user("kim@y.example", AddressDomainConfirmed)

		f.l1 = link(f.o, "pac149-l1", true)
		row("L1/P", f.l1, f.p, "pending")
		row("L1/P2", f.l1, f.p2, "pending")
		row("L1/K", f.l1, f.k, "pending")
		row("L1/F", f.l1, f.f, "pending")
		row("L1/Q", f.l1, f.q, "accepted")
		row("L1/R", f.l1, f.r, "declined")
		f.l2 = link(f.o, "pac149-l2", true)
		row("L2/P", f.l2, f.p, "pending")
		exec(`UPDATE scheduling_links SET deleted_at = now(), active = false WHERE id = $1`, f.l2)
		f.l3 = link(f.o, "pac149-l3", true)
		row("L3/K1", f.l3, f.k1, "pending")
		row("L3/K2", f.l3, f.k2, "pending")
		f.l4 = link(f.o, "pac149-l4", true)
		row("L4/K1", f.l4, f.k1, "pending")
		row("L4/K2", f.l4, f.k2, "accepted")
		f.l5 = link(f.o, "pac149-l5", true)
		row("L5/K2", f.l5, f.k2, "pending")
		row("L5/K1", f.l5, f.k1, "declined")
		f.l6 = link(f.k2, "pac149-l6", true)
		row("L6/K1", f.l6, f.k1, "pending")
		f.l7 = link(f.o, "pac149-l7", true)
		row("L7/Q", f.l7, f.q, "accepted")
		if withL8 {
			f.o8 = user("pac149-o8@x.example", AddressDomainConfirmed)
			f.l8 = link(f.o8, "pac149-l8", false) // created before PAC-112: no owner row
			row("L8/O8", f.l8, f.o8, "pending")
			row("L8/P", f.l8, f.p, "pending")
		}
	})
	return f
}

// pac149CountQuery is the release note's pre-deploy count, read from
// docs/runbook.md (the text the operator runs), white space as written.
func pac149CountQuery(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "runbook.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := strings.ReplaceAll(string(raw), "\r\n", "\n")
	i := strings.Index(s, "### PAC-149 release")
	if i < 0 {
		t.Fatal("no PAC-149 release section in the runbook")
	}
	s = s[i:]
	a := strings.Index(s, "```sql\n")
	if a < 0 {
		t.Fatal("no count query in the PAC-149 release section")
	}
	s = s[a+len("```sql\n"):]
	b := strings.Index(s, "```")
	return s[:b]
}

func pac149Counts(t *testing.T, dsn string) map[string]int {
	t.Helper()
	out := map[string]int{}
	query := pac149CountQuery(t)
	pac116Observe(t, dsn, func(conn *sql.Conn) {
		rows, err := conn.QueryContext(context.Background(), query)
		if err != nil {
			t.Fatalf("count query: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var cat string
			var n int
			if err := rows.Scan(&cat, &n); err != nil {
				t.Fatal(err)
			}
			out[cat] = n
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
	})
	return out
}

func pac149Migrate(t *testing.T, dsn string, to uint) error {
	t.Helper()
	restore := SetMigrationCeilingForTest(to)
	defer restore()
	_, err := MigrateWhenStopped(context.Background(), dsn, MigrateOptions{Caller: "pac149 test"})
	return err
}

// pac149RunUpWithNotices runs 044's up file once through pgx and returns its
// NOTICEs (golang-migrate does not surface them).
func pac149RunUpWithNotices(t *testing.T, dsn string) []string {
	t.Helper()
	body, err := migrationFiles.ReadFile(pac149MigrationFile)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var notices []string
	cfg.OnNotice = func(_ *pgconn.PgConn, n *pgconn.Notice) {
		mu.Lock()
		defer mu.Unlock()
		notices = append(notices, n.Message)
	}
	ctx := context.Background()
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, string(body)); err != nil {
		t.Fatalf("run 044's up file: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), notices...)
}

// pac149WithDB runs fn on a pool of its own on dsn and leaves no session.
func pac149WithDB(t *testing.T, dsn string, fn func(db *sql.DB)) {
	t.Helper()
	db, err := OpenNoMigrate(dsn, "pac149-test")
	if err != nil {
		t.Fatal(err)
	}
	fn(db)
	db.Close()
	pac116Idle(t, dsn)
}

type pac149HostRow struct {
	userID   uuid.NullUUID
	invitee  sql.NullString
	status   string
	invited  time.Time
	position int
}

func pac149Rows(t *testing.T, db *sql.DB, linkID uuid.UUID) map[uuid.UUID]pac149HostRow {
	t.Helper()
	rows, err := db.Query(`SELECT id, user_id, invitee_email, status, invited_at, invite_position FROM scheduling_link_hosts WHERE link_id = $1`, linkID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[uuid.UUID]pac149HostRow{}
	for rows.Next() {
		var id uuid.UUID
		var r pac149HostRow
		if err := rows.Scan(&id, &r.userID, &r.invitee, &r.status, &r.invited, &r.position); err != nil {
			t.Fatal(err)
		}
		out[id] = r
	}
	return out
}

// AC-10: the spec's fixture through the real migration runner. The
// release-note count (the runbook's text) reports the spec's exact counts
// before the migration; afterwards P's, P2's and K's rows are invitations by
// address with their ids, times and roster order unchanged; F's and L2's rows
// are gone; L3 keeps one invitation at K1's time; L4-L6 keep only their
// entries; Q's and R's rows are unchanged; nothing else changes; P lists and
// accepts L1, P2 after binding, neither K account; the owner still sees K's.
// Contract revision 2's L8 (the owner's own pending row on an ownerless link)
// is checked on a second database.
func TestPAC149_AC10_Migration044(t *testing.T) {
	dsn := pac116DB(t, 43)
	f := pac149Seed(t, dsn, false)

	if got, want := pac149Counts(t, dsn), map[string]int{
		"converted: with a holder":                  1,
		"converted: without a holder":               3,
		"dropped: placeholder":                      1,
		"dropped: on a deleted link":                1,
		"dropped: clash with another pending row":   1,
		"dropped: clash with an entry or the owner": 3,
	}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("pre-deploy count = %v, want %v", got, want)
	}

	before := pac148Snapshot(t, dsn)
	entriesBefore := pac148Rows(t, dsn, `SELECT to_jsonb(h)::text FROM scheduling_link_hosts h WHERE status <> 'pending'`)
	var l1OrderBefore []uuid.UUID
	pac149WithDB(t, dsn, func(db *sql.DB) {
		rows, err := db.Query(`SELECT id FROM scheduling_link_hosts WHERE link_id = $1 AND id <> $2 ORDER BY invited_at`, f.l1, f.rows["L1/F"])
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id uuid.UUID
			_ = rows.Scan(&id)
			l1OrderBefore = append(l1OrderBefore, id)
		}
		rows.Close()
	})

	if err := pac149Migrate(t, dsn, 44); err != nil {
		t.Fatalf("044: %v", err)
	}
	if v, dirty := pac116Version(t, dsn); v != 44 || dirty {
		t.Fatalf("after 044: %d dirty=%v", v, dirty)
	}

	after := pac148Snapshot(t, dsn)
	for tb, rows := range before {
		if tb == "scheduling_link_hosts" {
			continue
		}
		if strings.Join(after[tb], "\n") != strings.Join(rows, "\n") {
			t.Errorf("table %s changed", tb)
		}
	}
	if len(after) != len(before) {
		t.Errorf("tables: %d before, %d after", len(before), len(after))
	}
	// Accepted and declined rows (Q, R, the owner rows, L4-L7's entries):
	// unchanged, invited_at and responded_at included; the new columns are at
	// their defaults.
	entriesAfter := pac148Rows(t, dsn, `SELECT (to_jsonb(h) - 'invitee_email' - 'invite_position')::text FROM scheduling_link_hosts h
		WHERE status <> 'pending' AND invitee_email IS NULL AND invite_position = 0`)
	if strings.Join(entriesAfter, "\n") != strings.Join(entriesBefore, "\n") {
		t.Errorf("entries changed:\nbefore %v\nafter  %v", entriesBefore, entriesAfter)
	}

	pac149WithDB(t, dsn, func(db *sql.DB) {
		want := map[string]string{"L1/P": "p@x.example", "L1/P2": "p2@x.example", "L1/K": "Kim@x.example", "L3/K1": "Kim@y.example"}
		var pending []string
		rows, err := db.Query(`SELECT id, user_id, invitee_email, invited_at, invite_position FROM scheduling_link_hosts WHERE status = 'pending'`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id uuid.UUID
			var uid uuid.NullUUID
			var email sql.NullString
			var at time.Time
			var pos int
			if err := rows.Scan(&id, &uid, &email, &at, &pos); err != nil {
				t.Fatal(err)
			}
			name := ""
			for n, rid := range f.rows {
				if rid == id {
					name = n
				}
			}
			pending = append(pending, name)
			if want[name] == "" || uid.Valid || email.String != want[name] || !at.Equal(f.times[name]) || pos != 0 {
				t.Errorf("pending row %s (%s): user %v email %q at %s pos %d; want %q at %s", name, id, uid, email.String, at, pos, want[name], f.times[name])
			}
		}
		rows.Close()
		sort.Strings(pending)
		if strings.Join(pending, ",") != "L1/K,L1/P,L1/P2,L3/K1" {
			t.Errorf("pending rows after 044 = %v", pending)
		}
		for _, l := range []uuid.UUID{f.l4, f.l5, f.l6} {
			for _, r := range pac149Rows(t, db, l) {
				if r.status == "pending" {
					t.Errorf("link %s kept a pending row", l)
				}
			}
		}
		if n := len(pac149Rows(t, db, f.l2)); n != 1 {
			t.Errorf("deleted link: %d rows, want the owner row alone", n)
		}

		// Roster order of L1 unchanged (distinct times, spec N-2, N-10).
		hosts, err := GetLinkHosts(db, f.l1)
		if err != nil {
			t.Fatal(err)
		}
		var order []uuid.UUID
		for _, h := range hosts {
			order = append(order, h.ID)
		}
		if fmt.Sprint(order) != fmt.Sprint(l1OrderBefore) {
			t.Errorf("L1 roster order %v, want %v", order, l1OrderBefore)
		}

		// The invitee side.
		lists := func(user, link uuid.UUID) bool {
			t.Helper()
			invites, err := GetPendingInvitesForUser(db, user)
			if err != nil {
				t.Fatal(err)
			}
			for _, inv := range invites {
				if inv.LinkID == link {
					return true
				}
			}
			return false
		}
		if !lists(f.p, f.l1) {
			t.Error("P does not list L1")
		}
		if lists(f.p2, f.l1) || lists(f.k, f.l1) || lists(f.kk, f.l1) {
			t.Error("a non-holder lists L1")
		}
		if kind, err := FirstNonEmptyKind(db, f.p2); err != nil || kind != "" {
			t.Errorf("P2 is not empty after 044 (rule (b) would refuse its binding): %q %v", kind, err)
		}
		if ok, err := RespondToHostInvite(db, f.l1, f.k, "accepted"); err != nil || ok {
			t.Errorf("K accepts L1: %v %v, want no match", ok, err)
		}
		if ok, err := RespondToHostInvite(db, f.l1, f.p, "accepted"); err != nil || !ok {
			t.Errorf("P accepts L1: %v %v", ok, err)
		}
		// P2 binds with authoritative evidence: it now holds its address.
		if _, err := db.Exec(`UPDATE users SET address_standing = 'domain_confirmed' WHERE id = $1`, f.p2); err != nil {
			t.Fatal(err)
		}
		if !lists(f.p2, f.l1) {
			t.Error("P2 does not list L1 after binding")
		}
		if ok, err := RespondToHostInvite(db, f.l1, f.p2, "accepted"); err != nil || !ok {
			t.Errorf("P2 accepts L1: %v %v", ok, err)
		}
		// The owner still sees K's invitation pending.
		found := false
		for _, h := range mustHosts(t, db, f.l1) {
			if h.IsInvitation() && h.InviteeEmail == "Kim@x.example" && h.Status == "pending" {
				found = true
			}
		}
		if !found {
			t.Error("the owner no longer sees K's invitation")
		}
	})

	// The NOTICE, on a copy of the fixture; then a second run (idempotent).
	cp := pac116DB(t, 43)
	pac149Seed(t, cp, false)
	if got := pac149RunUpWithNotices(t, cp); len(got) != 1 || got[0] != "PAC-149 migration 044: converted 1 with a holder, 3 without; dropped 1 on deleted links, 1 placeholder, 0 owner's own pending row, 3 clashing with an entry or the owner, 1 clashing with another pending row" {
		t.Errorf("notices = %q", got)
	}

	// Contract revision 2: L8.
	l8 := pac116DB(t, 43)
	g := pac149Seed(t, l8, true)
	if got, want := pac149Counts(t, l8), map[string]int{
		"converted: with a holder":                  2,
		"converted: without a holder":               3,
		"dropped: placeholder":                      1,
		"dropped: on a deleted link":                1,
		"dropped: the owner's own pending row":      1,
		"dropped: clash with another pending row":   1,
		"dropped: clash with an entry or the owner": 3,
	}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("pre-deploy count with L8 = %v, want %v", got, want)
	}
	if err := pac149Migrate(t, l8, 44); err != nil {
		t.Fatal(err)
	}
	pac149WithDB(t, l8, func(db *sql.DB) {
		rows := pac149Rows(t, db, g.l8)
		if len(rows) != 1 {
			t.Fatalf("L8 rows = %v, want P's invitation alone", rows)
		}
		for _, r := range rows {
			if r.userID.Valid || r.invitee.String != "p@x.example" {
				t.Errorf("L8 row = %+v", r)
			}
		}
	})
}

func mustHosts(t *testing.T, db *sql.DB, linkID uuid.UUID) []*LinkHost {
	t.Helper()
	hosts, err := GetLinkHosts(db, linkID)
	if err != nil {
		t.Fatal(err)
	}
	return hosts
}

// AC-10, down (spec N-3): at 044, with an invitation equal to the owner's
// address and one for the address of a holder who already has an accepted
// entry (both inserted directly: the storage accepts them, contract md §4.0),
// one with no holder and one for a holder without an entry, the down step
// through golang-migrate restores the last as that holder's pending row,
// keeps the owner row and the entry, drops the rest, never fails on
// UNIQUE (link_id, user_id), and leaves user_id NOT NULL; up re-applies.
func TestPAC149_AC10_Down044(t *testing.T) {
	dsn := pac116DB(t, 44)
	if v, _ := pac116Version(t, dsn); v != 44 {
		t.Fatalf("version %d, want 44", v)
	}
	var owner, holder, b, link uuid.UUID
	pac148Exec(t, dsn, func(q func(string, ...any) *sql.Row, exec func(string, ...any)) {
		user := func(email string) uuid.UUID {
			var id uuid.UUID
			if err := q(`INSERT INTO users (email, name, address_standing) VALUES ($1, 'N', 'domain_confirmed') RETURNING id`, email).Scan(&id); err != nil {
				t.Fatal(err)
			}
			return id
		}
		owner, holder, b = user("pac149-d-owner@x.example"), user("pac149-d-a@x.example"), user("pac149-d-b@x.example")
		if err := q(`INSERT INTO scheduling_links (owner_user_id, slug, title) VALUES ($1, 'pac149-down', 'D') RETURNING id`, owner).Scan(&link); err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO scheduling_link_hosts (link_id, user_id, status) VALUES ($1, $2, 'accepted')`, link, owner)
		exec(`INSERT INTO scheduling_link_hosts (link_id, user_id, status) VALUES ($1, $2, 'accepted')`, link, holder)
		for _, addr := range []string{"PAC149-D-OWNER@x.example", "Pac149-D-A@x.example", "pac149-d-nobody@x.example", "PAC149-d-b@x.example"} {
			exec(`INSERT INTO scheduling_link_hosts (link_id, invitee_email, status) VALUES ($1, $2, 'pending')`, link, addr)
		}
	})

	pac149Steps(t, dsn, -1)
	if v, dirty := pac116Version(t, dsn); v != 43 || dirty {
		t.Fatalf("after down: %d dirty=%v", v, dirty)
	}
	got := pac148Rows(t, dsn, `SELECT user_id::text || ' ' || status FROM scheduling_link_hosts WHERE link_id = '`+link.String()+`'`)
	want := []string{owner.String() + " accepted", holder.String() + " accepted", b.String() + " pending"}
	sort.Strings(want)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("after down: %v, want %v", got, want)
	}
	if n := pac148Rows(t, dsn, `SELECT is_nullable FROM information_schema.columns WHERE table_name = 'scheduling_link_hosts' AND column_name IN ('user_id', 'invitee_email', 'invite_position')`); strings.Join(n, ",") != "NO" {
		t.Errorf("columns after down: %v, want user_id NOT NULL alone", n)
	}

	pac149Steps(t, dsn, 1)
	if v, dirty := pac116Version(t, dsn); v != 44 || dirty {
		t.Fatalf("after up again: %d dirty=%v", v, dirty)
	}
	if got := pac148Rows(t, dsn, `SELECT coalesce(user_id::text, invitee_email) || ' ' || status FROM scheduling_link_hosts WHERE link_id = '`+link.String()+`' AND status = 'pending'`); strings.Join(got, ",") != "pac149-d-b@x.example pending" {
		t.Errorf("after up again: %v", got)
	}
}

// pac149Steps moves dsn by n migrations through golang-migrate, the runner
// the down step is documented for (runbook "Going back past PAC-149").
func pac149Steps(t *testing.T, dsn string, n int) {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	src, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "postgres", driver)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Steps(n); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("steps %d: %v", n, err)
	}
	_, _ = m.Close()
	_ = db.Close()
	pac116Idle(t, dsn)
}
