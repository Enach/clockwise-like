package storage

// PAC-134 (spec docs/specs/PAC-134.md §4; contract
// contracts/features/PAC-134.md §3, §4, §7, §10, §11, §14): the erasure
// transaction, the Go-lowercasing twin, the schema classification, the
// locale, the duration bound and the concurrency cases. The command's own
// cases (exit codes, output, AC-1..AC-10, AC-14, AC-15) are in
// cmd/paceday-operator/pac134_test.go.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/testdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// p134Open is a migrated database of its own (C collation and ctype when
// cLocale), with its DSN.
func p134Open(t *testing.T, cLocale bool) (*sql.DB, string) {
	t.Helper()
	create := testdb.Create
	if cLocale {
		create = testdb.CreateCLocale
	}
	dsn, cleanup, err := create()
	if err != nil {
		t.Fatalf("create test database: %v", err)
	}
	db, err := Open(dsn)
	if err != nil {
		_ = cleanup()
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close(); _ = cleanup() })
	return db, dsn
}

func p134Exec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func p134ID(t *testing.T, db *sql.DB, q string, args ...any) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := db.QueryRow(q, args...).Scan(&id); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return id
}

func p134Count(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func p134Run(db *sql.DB, addr string, mod func(*EraseAddressOptions)) (*EraseAddressResult, error) {
	o := EraseAddressOptions{Address: addr, MaxDuration: 20 * time.Second, Run: OperatorRun{Operator: "op", Reason: "PAC-134 test"}}
	if mod != nil {
		mod(&o)
	}
	return EraseAddress(context.Background(), db, o)
}

func p134Erased(res *EraseAddressResult) map[string]int64 {
	m := map[string]int64{}
	for _, p := range res.Places {
		m[p.Name] = p.Erased
	}
	return m
}

// p134Seed stores one copy of x in every place T1-T9 (two bookings), owned
// by accounts that do not match, and returns the expected counts and the
// ids the tests use.
type p134Seeded struct {
	owner, team, live, deleted, booking uuid.UUID
	counts                              map[string]int64
}

func p134Seed(t *testing.T, db *sql.DB, x string) p134Seeded {
	t.Helper()
	s := p134Seeded{}
	s.owner = p134ID(t, db, `INSERT INTO users (email, name) VALUES ($1, 'Owner') RETURNING id`, "owner-"+uuid.NewString()[:8]+"@pac134.example")
	s.team = p134ID(t, db, `INSERT INTO teams (name, created_by) VALUES ('T', $1) RETURNING id`, s.owner)
	p134Exec(t, db, `INSERT INTO team_added_members (team_id, email, added_by) VALUES ($1, $2, $3)`, s.team, strings.ToLower(x), s.owner)
	p134Exec(t, db, `INSERT INTO team_invites (team_id, invitee_email, invited_by, token, expires_at) VALUES ($1, $2, $3, $4, now() + interval '1 day')`, s.team, strings.ToLower(x), s.owner, uuid.NewString())
	p134Exec(t, db, `INSERT INTO manager_team_members (manager_user_id, member_email) VALUES ($1, $2)`, s.owner, strings.ToLower(x))
	p134Exec(t, db, `INSERT INTO manager_team_member_assignments (manager_user_id, team_id, member_email) VALUES ($1, $2, $3)`, s.owner, s.team, strings.ToLower(x))
	p134Exec(t, db, `INSERT INTO one_on_one_occurrences (manager_user_id, member_email, calendar_event_id, occurred_at) VALUES ($1, $2, 'e1', now())`, s.owner, x)
	p134Exec(t, db, `INSERT INTO team_analytics_cache (manager_user_id, member_email, week_start) VALUES ($1, $2, '2026-11-02')`, s.owner, x)
	s.live = p134ID(t, db, `INSERT INTO scheduling_links (owner_user_id, slug, title) VALUES ($1, $2, 'L') RETURNING id`, s.owner, "l-"+uuid.NewString()[:12])
	s.deleted = p134ID(t, db, `INSERT INTO scheduling_links (owner_user_id, slug, title, active, deleted_at) VALUES ($1, $2, 'D', false, now()) RETURNING id`, s.owner, "d-"+uuid.NewString()[:12])
	p134Exec(t, db, `INSERT INTO scheduling_link_hosts (link_id, invitee_email, status) VALUES ($1, $2, 'pending')`, s.live, x)
	p134Exec(t, db, `INSERT INTO scheduling_link_create_keys (owner_user_id, idempotency_key, link_id, request_sha256, co_host_changes)
		VALUES ($1, 'pac134-seed-key-000001', $2, sha256('k'::bytea), jsonb_build_object('addresses', jsonb_build_array(jsonb_build_object('email', $3::text)), 'removed', '[]'::jsonb))`, s.owner, s.live, x)
	s.booking = p134ID(t, db, `INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time, notes)
		VALUES ($1, 'Pat', $2, now() + interval '1 day', now() + interval '1 day 30 minutes', 'n') RETURNING id`, s.live, x)
	p134Exec(t, db, `INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time, notes)
		VALUES ($1, 'Pat', $2, now() - interval '1 day', now() - interval '1 day' + interval '30 minutes', 'n')`, s.deleted, x)
	s.counts = map[string]int64{
		"team_added_members": 1, "team_invites": 1, "manager_team_members": 1, "manager_team_member_assignments": 1,
		"one_on_one_occurrences": 1, "team_analytics_cache": 1, "scheduling_link_hosts": 1,
		"scheduling_link_create_keys": 1, "bookings": 2,
	}
	return s
}

// p134Remaining counts x's copies left in T1-T9 (the Go half of the rule;
// the seeds are ASCII).
func p134Remaining(t *testing.T, db *sql.DB, x string) int {
	t.Helper()
	l := strings.ToLower(x)
	return p134Count(t, db, `SELECT
		(SELECT count(*) FROM team_added_members WHERE lower(email) = $1) +
		(SELECT count(*) FROM team_invites WHERE lower(invitee_email) = $1) +
		(SELECT count(*) FROM manager_team_members WHERE lower(member_email) = $1) +
		(SELECT count(*) FROM manager_team_member_assignments WHERE lower(member_email) = $1) +
		(SELECT count(*) FROM one_on_one_occurrences WHERE lower(member_email) = $1) +
		(SELECT count(*) FROM team_analytics_cache WHERE lower(member_email) = $1) +
		(SELECT count(*) FROM scheduling_link_hosts WHERE lower(invitee_email) = $1) +
		(SELECT count(*) FROM scheduling_link_create_keys WHERE co_host_changes::text ILIKE '%' || $1 || '%') +
		(SELECT count(*) FROM bookings WHERE lower(booker_email) = $1)`, l)
}

func p134Audits(t *testing.T, db *sql.DB) int {
	return p134Count(t, db, `SELECT count(*) FROM operator_audit WHERE action IN ('erase_address', 'erase_address_dry_run')`)
}

// AC-21: paceday_go_lower equals Go's strings.ToLower on every Unicode
// scalar value U+0001..U+10FFFF (surrogates excluded; U+0000 cannot be held
// by text), in both directions, with the same length (the pre-filter's
// premise), on a default and on a C database; and on a sample of addresses.
// chr() is used, never convert_from(), whose result carries the "C"
// collation of its name argument (contract md §4.3).
func TestPAC134_AC21_GoLowerPinnedOverEveryScalar(t *testing.T) {
	sample := []string{
		"Ünal@Ext.Example", "ÜNAL@ext.example", "ünal@ext.example", "KIM@ext.example", "kim@ext.example",
		"İnal@ext.example", "inal@ext.example", "straße@x.example", "STRASSE@x.example", "ẞ@x.example",
		"ΣΣ@x.example", "\U00010400@x.example", "freed+" + uuid.NewString() + "@paceday.invalid", " pat@ext.example",
		"PAT@EXT.EXAMPLE", "Ǆ@x.example", "ǅ@x.example", "ϴ@x.example", "Ω@x.example", "Ω@x.example", "",
	}
	for _, c := range []bool{false, true} {
		t.Run(fmt.Sprintf("C=%v", c), func(t *testing.T) {
			db, _ := p134Open(t, c)
			rows, err := db.Query(`SELECT cp, paceday_go_lower(chr(cp)), char_length(paceday_go_lower(chr(cp)))
				FROM generate_series(1, 1114111) cp WHERE cp NOT BETWEEN 55296 AND 57343 ORDER BY cp`)
			if err != nil {
				t.Fatal(err)
			}
			n, changed, diffs := 0, 0, 0
			for rows.Next() {
				var cp int
				var got string
				var length int
				if err := rows.Scan(&cp, &got, &length); err != nil {
					t.Fatal(err)
				}
				n++
				in := string(rune(cp))
				want := strings.ToLower(in)
				if want != in {
					changed++
				}
				if got != want || length != 1 {
					diffs++
					if diffs <= 10 {
						t.Errorf("U+%04X: database %q (length %d), Go %q", cp, got, length, want)
					}
				}
			}
			rows.Close()
			if n != 1112063 || diffs != 0 {
				t.Fatalf("%d scalar values, %d differences", n, diffs)
			}
			t.Logf("%d scalar values, %d changed by Go, 0 differences", n, changed)
			for _, s := range sample {
				var got string
				var length int
				if err := db.QueryRow(`SELECT paceday_go_lower($1::text), char_length(paceday_go_lower($1::text))`, s).Scan(&got, &length); err != nil {
					t.Fatal(err)
				}
				if want := strings.ToLower(s); got != want || length != len([]rune(s)) {
					t.Errorf("%q: database %q, Go %q", s, got, want)
				}
			}
			var null sql.NullString
			if err := db.QueryRow(`SELECT paceday_go_lower(NULL)`).Scan(&null); err != nil || null.Valid {
				t.Errorf("NULL in: %v %v", null, err)
			}
		})
	}
}

// AC-17: every text, JSON and text-array column whose name holds email,
// invitee, attendee or address, and every JSON column, is classified (contract
// md §11, five classes). A migration adding one turns this red until it is
// classified here; a classified column that disappears does too.
func TestPAC134_AC17_EveryAddressColumnIsClassified(t *testing.T) {
	const (
		erased    = "erased by this tool (spec §3.4)"
		account   = "account-owned (spec §3.2)"
		kept      = "kept and counted (T10)"
		noAddress = "holds no third party's address"
		freeText  = "free text, not searched (spec §5)"
	)
	classes := map[string]string{
		"bookings.booker_email": erased, "team_added_members.email": erased, "team_invites.invitee_email": erased,
		"manager_team_members.member_email": erased, "manager_team_member_assignments.member_email": erased,
		"one_on_one_occurrences.member_email": erased, "team_analytics_cache.member_email": erased,
		"scheduling_link_hosts.invitee_email": erased, "scheduling_link_create_keys.co_host_changes": erased,
		"users.email": account, "users.email_key": account, "settings.calendar_email": account,
		"operator_audit.account_email": kept, "operator_audit.details": kept,
		"users.address_standing": noAddress, "operator_audit.removed": noAddress, "settings.working_hours": noAddress,
		"settings.lunch_breaks": noAddress, "sso_events.details": noAddress,
		"analytics_weeks.top_meeting_titles": freeText, "meeting_briefs.slack_results": freeText, "meeting_briefs.notion_results": freeText,
	}
	db := openTestDB(t)
	rows, err := db.Query(`SELECT table_name || '.' || column_name FROM information_schema.columns
		WHERE table_schema = 'public' AND (
		  ((data_type IN ('text', 'character varying', 'character', 'json', 'jsonb') OR (data_type = 'ARRAY' AND udt_name IN ('_text', '_varchar', '_bpchar', '_json', '_jsonb')))
		    AND column_name ~ '(email|invitee|attendee|address)')
		  OR data_type IN ('json', 'jsonb') OR (data_type = 'ARRAY' AND udt_name IN ('_json', '_jsonb')))`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		seen[c] = true
		if _, ok := classes[c]; !ok {
			t.Errorf("%s is not classified: add it to PAC-134's classification (erased, account-owned, kept, no address, or free text) and to the tool if it holds a third party's address", c)
		}
	}
	for c := range classes {
		if !seen[c] {
			t.Errorf("%s is classified but no longer in the schema", c)
		}
	}
}

// AC-20: on a database whose ctype is C, the Go half matches what lower()
// misses (non-ASCII uppercase), for the account check and every place; the
// negative controls stay; and every statement but input_lower and accounts
// returns counts only (no value of another row is read into the process).
func TestPAC134_AC20_CLocale(t *testing.T) {
	for _, input := range []string{"ünal@ext.example", "ÜNAL@EXT.EXAMPLE"} {
		t.Run(input, func(t *testing.T) {
			db, _ := p134Open(t, true)
			var lowered string
			if err := db.QueryRow(`SELECT lower('Ünal')`).Scan(&lowered); err != nil || lowered != "Ünal" {
				t.Fatalf("not a C database: lower('Ünal') = %q %v", lowered, err)
			}
			acct := p134ID(t, db, `INSERT INTO users (email, name) VALUES ('Ünal@Ext.Example', 'U') RETURNING id`)
			owner := p134ID(t, db, `INSERT INTO users (email, name) VALUES ('owner@c.example', 'O') RETURNING id`)
			team := p134ID(t, db, `INSERT INTO teams (name, created_by) VALUES ('T', $1) RETURNING id`, owner)
			link := p134ID(t, db, `INSERT INTO scheduling_links (owner_user_id, slug, title) VALUES ($1, 'c-link', 'L') RETURNING id`, owner)
			booking := p134ID(t, db, `INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time) VALUES ($1, 'U', 'Ünal@Ext.Example', now(), now()) RETURNING id`, link)
			control := p134ID(t, db, `INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time) VALUES ($1, 'U', 'unal@ext.example', now(), now()) RETURNING id`, link)
			p134Exec(t, db, `INSERT INTO scheduling_link_hosts (link_id, invitee_email, status) VALUES ($1, 'ÜNAL@ext.example', 'pending'), ($1, 'ünal@ext.example.org', 'pending')`, link)
			p134Exec(t, db, `INSERT INTO team_added_members (team_id, email, added_by) VALUES ($1, 'ünal@ext.example', $2), ($1, 'ünal2@ext.example', $2)`, team, owner)
			p134Exec(t, db, `INSERT INTO team_invites (team_id, invitee_email, invited_by, token, expires_at) VALUES ($1, 'ünal@ext.example.org', $2, 'tok', now())`, team, owner)
			p134Exec(t, db, `INSERT INTO manager_team_members (manager_user_id, member_email) VALUES ($1, 'ünal@ext.example')`, owner)
			acctBefore := p134Row(t, db, `SELECT row_to_json(u)::text FROM users u WHERE id = $1`, acct)

			type stmt struct{ name, sql string }
			var sent []stmt
			InstallEraseStatement(t, func(name, sql string, _ []any) { sent = append(sent, stmt{name, sql}) })
			res, err := p134Run(db, input, nil)
			if !errors.Is(err, ErrEraseAccountHeld) || len(res.Accounts) != 1 || res.Accounts[0] != acct || p134Audits(t, db) != 0 {
				t.Fatalf("without the acknowledgement: %v %+v", err, res)
			}
			res, err = p134Run(db, input, func(o *EraseAddressOptions) { o.AccountHeldAcknowledged = true })
			if err != nil {
				t.Fatal(err)
			}
			got := p134Erased(res)
			want := map[string]int64{"team_added_members": 1, "manager_team_members": 1, "scheduling_link_hosts": 1, "bookings": 1}
			for _, n := range ErasePlaceNames {
				if got[n] != want[n] {
					t.Errorf("%s: erased %d, want %d", n, got[n], want[n])
				}
			}
			if p134Count(t, db, `SELECT count(*) FROM bookings WHERE id = $1 AND booker_email = '' AND booker_erased_at IS NOT NULL`, booking) != 1 ||
				p134Count(t, db, `SELECT count(*) FROM bookings WHERE id = $1 AND booker_email = 'unal@ext.example' AND booker_erased_at IS NULL`, control) != 1 ||
				p134Count(t, db, `SELECT count(*) FROM scheduling_link_hosts WHERE invitee_email = 'ünal@ext.example.org'`) != 1 ||
				p134Count(t, db, `SELECT count(*) FROM team_added_members WHERE email = 'ünal2@ext.example'`) != 1 ||
				p134Count(t, db, `SELECT count(*) FROM team_invites`) != 1 {
				t.Error("a negative control changed")
			}
			if p134Row(t, db, `SELECT row_to_json(u)::text FROM users u WHERE id = $1`, acct) != acctBefore {
				t.Error("the account changed")
			}
			for _, s := range sent {
				switch {
				case s.name == "input_lower" || s.name == "accounts" || s.name == "settings" || s.name == "budget" || s.name == "audit":
				case s.name == "found_table" || s.name == "analyze_found" || strings.HasPrefix(s.sql, "INSERT INTO pac134_found"):
				case s.name == "counts" || strings.HasSuffix(strings.TrimSpace(s.sql), ") x") || strings.HasSuffix(strings.TrimSpace(s.sql), "SELECT count(*) FROM d") ||
					strings.HasSuffix(strings.TrimSpace(s.sql), "SELECT count(*) FROM u") || strings.HasPrefix(s.sql, "SELECT count(*) FROM operator_audit"):
				default:
					t.Errorf("%s returns something other than a count: %s", s.name, s.sql)
				}
			}
		})
	}
}

func p134Row(t *testing.T, db *sql.DB, q string, args ...any) string {
	t.Helper()
	var s string
	if err := db.QueryRow(q, args...).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// The statement-logging preflight (contract md §2.3, D-5; stage-4 amendment
// N-3): the conditions are evaluated in SQL on pg_settings' unitless values,
// so "2s" refuses as 2000 ms would; nothing is read and nothing written.
func TestPAC134_StatementLoggingPreflight(t *testing.T) {
	cases := []struct {
		name   string
		set    []string
		refuse bool
	}{
		{"defaults", nil, false},
		{"duration with a unit", []string{"log_min_duration_statement = '2s'"}, true},
		{"duration zero", []string{"log_min_duration_statement = 0"}, true},
		{"duration logged without parameters", []string{"log_min_duration_statement = '2s'", "log_parameter_max_length = 0"}, false},
		{"log_statement mod", []string{"log_statement = 'mod'"}, true},
		{"log_statement ddl", []string{"log_statement = 'ddl'"}, false},
		{"sample duration", []string{"log_min_duration_sample = '100ms'"}, true},
		{"transaction sample", []string{"log_transaction_sample_rate = 0.5"}, true},
		{"parameters on error", []string{"log_parameter_max_length_on_error = '1kB'"}, true},
		{"auto_explain placeholder not loaded", []string{`"auto_explain.log_min_duration" = '-1'`}, false},
		// A placeholder of a module that is not loaded logs nothing (and
		// pg_settings does not list it).
		{"auto_explain placeholder with a unit", []string{`"auto_explain.log_min_duration" = '2s'`}, false},
		{"auto_explain loaded", []string{"session_preload_libraries = 'auto_explain'", `"auto_explain.log_min_duration" = '2s'`}, true},
		{"auto_explain loaded, off", []string{"session_preload_libraries = 'auto_explain'", `"auto_explain.log_min_duration" = -1`}, false},
		// Review of #326, item 2: the debug_print_* trees hold the bound
		// address as a constant.
		{"debug_print_plan", []string{"debug_print_plan = on"}, true},
		{"debug_print_parse", []string{"debug_print_parse = on"}, true},
		{"debug_print_rewritten", []string{"debug_print_rewritten = on"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db, dsn := p134Open(t, false)
			p134Seed(t, db, "pat@ext.example")
			name := p134Row(t, db, `SELECT current_database()`)
			for _, s := range c.set {
				p134Exec(t, db, `ALTER DATABASE "`+name+`" SET `+s)
			}
			run, err := OpenNoMigrateDescribeExec(dsn, EraseAddressApp)
			if err != nil {
				t.Fatal(err)
			}
			defer run.Close()
			var names []string
			InstallEraseStatement(t, func(n, _ string, _ []any) { names = append(names, n) })
			_, err = p134Run(run, "pat@ext.example", nil)
			if c.refuse {
				if !errors.Is(err, ErrEraseStatementLogging) || len(names) != 1 || p134Remaining(t, db, "pat@ext.example") != 10 || p134Audits(t, db) != 0 {
					t.Fatalf("err %v, statements %v", err, names)
				}
				return
			}
			if err != nil || p134Remaining(t, db, "pat@ext.example") != 0 {
				t.Fatalf("err %v", err)
			}
		})
	}
}

// pgCodeOf is err's SQLSTATE.
func pgCodeOf(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code
	}
	return ""
}

// AC-11: another transaction holds one of X's bookings past the bound: the
// run ends within the bound plus a margin, by the bound or the lock wait,
// nothing changed (places before the booking included), no record.
func TestPAC134_AC11_HeldBookingEndsTheRunWithinTheBound(t *testing.T) {
	db := openTestDB(t)
	s := p134Seed(t, db, "pat@ext.example")
	holder, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback() }()
	if _, err := holder.Exec(`SELECT 1 FROM bookings WHERE id = $1 FOR UPDATE`, s.booking); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = p134Run(db, "pat@ext.example", func(o *EraseAddressOptions) { o.MaxDuration = time.Second })
	took := time.Since(start)
	if err == nil || (!errors.Is(err, ErrEraseBoundExceeded) && pgCodeOf(err) != "55P03") || took > 1500*time.Millisecond {
		t.Fatalf("err %v after %s", err, took)
	}
	if p134Remaining(t, db, "pat@ext.example") != 10 || p134Audits(t, db) != 0 {
		t.Fatal("the failed run changed data or wrote a record")
	}
}

// AC-22a: the bound forced below what the run needs: the in-flight work is
// cancelled, the run rolls back, ErrEraseBoundExceeded, nothing changed, no
// record.
func TestPAC134_AC22a_BoundCancelsAndRollsBack(t *testing.T) {
	db := openTestDB(t)
	p134Seed(t, db, "pat@ext.example")
	for _, at := range []string{"find_t5", "erase_t2"} {
		InstallEraseAfterStatement(t, func(ctx context.Context, name string) error {
			if name == at {
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		})
		start := time.Now()
		_, err := p134Run(db, "pat@ext.example", func(o *EraseAddressOptions) { o.MaxDuration = 200 * time.Millisecond })
		if !errors.Is(err, ErrEraseBoundExceeded) || time.Since(start) > time.Second {
			t.Fatalf("held after %s: err %v after %s", at, err, time.Since(start))
		}
		if p134Remaining(t, db, "pat@ext.example") != 10 || p134Audits(t, db) != 0 {
			t.Fatalf("held after %s: the run changed data or wrote a record", at)
		}
	}
}

// Stage-4 amendment N-1: statement_timeout and lock_timeout restart with
// every statement, so before each lock-phase statement they are re-armed to
// the time left until the deadline: a contended lock-phase statement near
// the deadline is ended by the server (lock_timeout) by the deadline, not
// later, and every re-arm is the budget left.
func TestPAC134_N1_LockPhaseEndsByTheDeadline(t *testing.T) {
	db := openTestDB(t)
	s := p134Seed(t, db, "pat@ext.example")
	holder, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback() }()
	if _, err := holder.Exec(`SELECT 1 FROM bookings WHERE id = $1 FOR UPDATE`, s.booking); err != nil {
		t.Fatal(err)
	}
	const bound = 2 * time.Second
	start := time.Now()
	deadline := start.Add(bound)
	var budgets []time.Duration
	var mu sync.Mutex
	InstallEraseStatement(t, func(name, _ string, args []any) {
		if name != "budget" {
			return
		}
		ms, _ := time.ParseDuration(args[0].(string))
		mu.Lock()
		defer mu.Unlock()
		if left := time.Until(deadline); ms > left+5*time.Millisecond || ms < left-200*time.Millisecond {
			t.Errorf("budget %s with %s left", ms, left)
		}
		budgets = append(budgets, ms)
	})
	// The discovery uses most of the bound, so the lock phase starts close
	// to the deadline with a full-bound lock_timeout left from [settings].
	InstallEraseAfterStatement(t, func(ctx context.Context, name string) error {
		if name == "analyze_found" {
			time.Sleep(time.Until(deadline.Add(-600 * time.Millisecond)))
		}
		return nil
	})
	_, err = EraseAddress(context.Background(), db, EraseAddressOptions{Address: "pat@ext.example", MaxDuration: bound, Run: OperatorRun{Operator: "op", Reason: "r"}})
	ended := time.Since(start)
	// Ended by the server's own timer (lock_timeout 55P03, or
	// statement_timeout 57014 "due to statement timeout"), not by the
	// client's cancel at the deadline (57014 "due to user request") nor by the
	// context.
	var pg *pgconn.PgError
	server := errors.As(err, &pg) && (pg.Code == "55P03" || pg.Code == "57014" && strings.Contains(pg.Message, "statement timeout"))
	if !server || ended > bound {
		t.Fatalf("err %v after %s; want the server's own timeout by the deadline", err, ended)
	}
	if len(budgets) < 10 || p134Remaining(t, db, "pat@ext.example") != 10 || p134Audits(t, db) != 0 {
		t.Fatalf("budgets %v", budgets)
	}
}

// AC-12: two concurrent runs both succeed; their erased counts add up to
// the copies that existed; nothing matches afterwards.
func TestPAC134_AC12_TwoConcurrentRuns(t *testing.T) {
	db := openTestDB(t)
	s := p134Seed(t, db, "pat@ext.example")
	var arrived sync.WaitGroup
	arrived.Add(2)
	InstallEraseAfterStatement(t, func(ctx context.Context, name string) error {
		if name == "counts" {
			arrived.Done()
			done := make(chan struct{})
			go func() { arrived.Wait(); close(done) }()
			select {
			case <-done:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	})
	results := make(chan *EraseAddressResult, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			res, err := p134Run(db, "pat@ext.example", nil)
			results <- res
			errs <- err
		}()
	}
	sum := map[string]int64{}
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		for k, v := range p134Erased(<-results) {
			sum[k] += v
		}
	}
	for k, v := range s.counts {
		if sum[k] != v {
			t.Errorf("%s: erased %d in total, want %d", k, sum[k], v)
		}
	}
	if p134Remaining(t, db, "pat@ext.example") != 0 || p134Audits(t, db) != 2 {
		t.Error("copies remain or records missing")
	}
}

// AC-13: a booking by X committed after the run's statement on bookings
// still holds X after the run commits; a rerun erases it (1 booking).
func TestPAC134_AC13_BookingCommittedDuringTheRun(t *testing.T) {
	db := openTestDB(t)
	s := p134Seed(t, db, "pat@ext.example")
	var newID uuid.UUID
	inserted := false
	InstallEraseAfterStatement(t, func(ctx context.Context, name string) error {
		if name == "find_t9" && !inserted {
			inserted = true
			return db.QueryRowContext(ctx, `INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time)
				VALUES ($1, 'Pat', 'Pat@Ext.Example', now() + interval '3 days', now() + interval '3 days 30 minutes') RETURNING id`, s.live).Scan(&newID)
		}
		return nil
	})
	if _, err := p134Run(db, "pat@ext.example", nil); err != nil {
		t.Fatal(err)
	}
	if p134Count(t, db, `SELECT count(*) FROM bookings WHERE id = $1 AND booker_email = 'Pat@Ext.Example'`, newID) != 1 {
		t.Fatal("the booking committed during the run was erased by it")
	}
	res, err := p134Run(db, "pat@ext.example", nil)
	if err != nil || p134Erased(res)["bookings"] != 1 || p134Remaining(t, db, "pat@ext.example") != 0 {
		t.Fatalf("rerun: %v %v", err, res)
	}
}

// AC-16: the PAC-40 job after an operator erasure does not count the erased
// booking as awaiting and does not erase it again.
func TestPAC134_AC16_BookerErasureJobAfterTheErasure(t *testing.T) {
	db := openTestDB(t)
	s := p134Seed(t, db, "pat@ext.example")
	p134Exec(t, db, `INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time) VALUES ($1, 'Other', 'other@x.example', now() - interval '2 days', now() - interval '2 days' + interval '30 minutes')`, s.deleted)
	if _, err := p134Run(db, "pat@ext.example", nil); err != nil {
		t.Fatal(err)
	}
	var erasedAt time.Time
	if err := db.QueryRow(`SELECT booker_erased_at FROM bookings WHERE link_id = $1 AND booker_name = ''`, s.deleted).Scan(&erasedAt); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(400 * 24 * time.Hour)
	st, err := ListBookerErasureStatus(context.Background(), db, future, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range st {
		if r.LinkID == s.deleted && r.Awaiting != 1 {
			t.Errorf("awaiting %d, want 1 (the other booker only)", r.Awaiting)
		}
	}
	n, err := EraseLinkBookers(context.Background(), db, s.deleted, future)
	if err != nil || n != 1 {
		t.Fatalf("job erased %d (%v), want 1", n, err)
	}
	var again time.Time
	if err := db.QueryRow(`SELECT booker_erased_at FROM bookings WHERE link_id = $1 AND booker_email = '' AND booker_name = '' AND notes = ''`, s.deleted).Scan(&again); err != nil || !again.Equal(erasedAt) {
		t.Errorf("the operator-erased booking was erased again: %v %v", again, err)
	}
}

// The validity rule shared with the Manager/team normaliser (contract md
// §2.1 item 3).
func TestPAC134_ValidAddress(t *testing.T) {
	for in, want := range map[string]bool{
		"pat@ext.example": true, "Pat@Ext.Example": true, "Ünal@Ext.Example": true, "": false, "not-an-address": false,
		"pat@@ext.example": false, "Pat <pat@ext.example>": false, "pat@ext.example ": false,
	} {
		l, ok := ValidAddress(in)
		if ok != want || (ok && l != strings.ToLower(in)) {
			t.Errorf("%q: %q %v, want %v", in, l, ok, want)
		}
	}
	// Input refusals of the storage entry point itself.
	db := openTestDB(t)
	for _, o := range []EraseAddressOptions{
		{Address: " pat@ext.example", MaxDuration: time.Second, Run: OperatorRun{"o", "r"}},
		{Address: "pat@ext.example", MaxDuration: 0, Run: OperatorRun{"o", "r"}},
		{Address: "pat@ext.example", MaxDuration: time.Second, Run: OperatorRun{"", "r"}},
	} {
		if _, err := EraseAddress(context.Background(), db, o); !errors.Is(err, ErrOperatorInput) {
			t.Errorf("%+v: %v", o, err)
		}
	}
	names := append([]string(nil), ErasePlaceNames...)
	sort.Strings(names)
	if len(names) != 9 {
		t.Fatal(names)
	}
}

// Both lowerings decide a match (spec §3.3, contract md §3.1). On the
// production image (musl, en_US.utf8) no code point is lowercased by the
// database and left alone by Go, so the database half is observable only on
// a database whose lower() differs: an ICU tr-TR one lowercases "I" to the
// dotless "ı", which Go lowercases to "i". There a stored "ıvan@…" matches
// the input "IVAN@…" by the database half only, and "ivan@…" by the Go half
// only; "ivan2@…" by neither.
func TestPAC134_BothLoweringsDecide(t *testing.T) {
	dsn, cleanup, err := testdb.CreateWithOptions("TEMPLATE template0 ENCODING 'UTF8' LOCALE_PROVIDER icu ICU_LOCALE 'tr-TR' LOCALE 'en_US.utf8'")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cleanup() }()
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := p134Row(t, db, `SELECT lower('IVAN')`); got != "ıvan" {
		t.Fatalf("not a tr-TR database: lower('IVAN') = %q", got)
	}
	owner := p134ID(t, db, `INSERT INTO users (email, name) VALUES ('owner@tr.example', 'O') RETURNING id`)
	link := p134ID(t, db, `INSERT INTO scheduling_links (owner_user_id, slug, title) VALUES ($1, 'tr-link', 'L') RETURNING id`, owner)
	for _, e := range []string{"ıvan@ext.example", "ivan@ext.example", "ivan2@ext.example"} {
		p134Exec(t, db, `INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time) VALUES ($1, 'B', $2, now(), now())`, link, e)
	}
	// The account check uses both halves too (review of #326, MX6): an
	// account whose address only the database half equates is named.
	acct := p134ID(t, db, `INSERT INTO users (email, name) VALUES ('ıvan@ext.example', 'A') RETURNING id`)
	held, err := p134Run(db, "IVAN@ext.example", nil)
	if !errors.Is(err, ErrEraseAccountHeld) || held == nil || len(held.Accounts) != 1 || held.Accounts[0] != acct {
		t.Fatalf("the account matched by the database half only was not named: %v %+v", err, held)
	}
	res, err := p134Run(db, "IVAN@ext.example", func(o *EraseAddressOptions) { o.AccountHeldAcknowledged = true })
	if err != nil {
		t.Fatal(err)
	}
	if n := p134Erased(res)["bookings"]; n != 2 {
		t.Fatalf("erased %d bookings, want 2 (one per lowering)", n)
	}
	if p134Count(t, db, `SELECT count(*) FROM bookings WHERE booker_email = 'ivan2@ext.example'`) != 1 ||
		p134Count(t, db, `SELECT count(*) FROM bookings WHERE booker_email IN ('ıvan@ext.example', 'ivan@ext.example')`) != 0 {
		t.Fatal("wrong bookings erased")
	}
}

// Stage-4 amendment N-2: the statistics of the found table never include
// row_key, which holds the found address variants (ANALYZE writes a column's
// common values to pg_statistic, which is WAL-logged).
func TestPAC134_N2_AnalyzeExcludesRowKey(t *testing.T) {
	db := openTestDB(t)
	p134Seed(t, db, "pat@ext.example")
	var analyze []string
	InstallEraseStatement(t, func(name, sql string, _ []any) {
		if name == "analyze_found" {
			analyze = append(analyze, sql)
		}
	})
	if _, err := p134Run(db, "pat@ext.example", nil); err != nil {
		t.Fatal(err)
	}
	if len(analyze) != 1 || analyze[0] != "ANALYZE pac134_found (place, own, row_id, team_id, link_id, owner_id)" {
		t.Fatalf("analyze statements %q", analyze)
	}
}

// A run that fails between two statements for a reason that is not a
// database error (here a seam's error after erase_t2) changes nothing and
// writes no record: the transaction is rolled back, never committed.
func TestPAC134_FailedRunChangesNothing(t *testing.T) {
	db := openTestDB(t)
	p134Seed(t, db, "pat@ext.example")
	boom := errors.New("stop after erase_t2")
	InstallEraseAfterStatement(t, func(_ context.Context, name string) error {
		if name == "erase_t2" {
			return boom
		}
		return nil
	})
	if _, err := p134Run(db, "pat@ext.example", nil); !errors.Is(err, boom) {
		t.Fatalf("err %v", err)
	}
	if p134Remaining(t, db, "pat@ext.example") != 10 || p134Audits(t, db) != 0 {
		t.Fatal("a failed run changed data or wrote a record")
	}
}

// p134LockOrderProbe holds the row whose key sorts last, runs sweep in the
// background, waits until it waits on that row, then reports whether the row
// that sorts first is already locked (by the sweep): it is when the sweep
// locks in key order, not when it locks in scan order (the last-sorting row
// was inserted first, so a scan meets it first).
func p134LockOrderProbe(t *testing.T, db *sql.DB, lockLast, probeFirst string, args []any, sweep func() error) bool {
	t.Helper()
	holder, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := holder.Exec(lockLast, args[1]); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- sweep() }()
	deadline := time.Now().Add(5 * time.Second)
	for p134Count(t, db, `SELECT count(*) FROM pg_locks WHERE NOT granted AND locktype = 'transactionid'`) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the sweep never waited on the held row")
		}
		time.Sleep(10 * time.Millisecond)
	}
	probe, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	_, perr := probe.Exec(probeFirst, args[0])
	_ = probe.Rollback()
	_ = holder.Rollback()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	return pgCodeOf(perr) == "55P03"
}

// L-1 (contract md §6.2): the invitation expiry sweep locks its rows in id
// order.
func TestPAC134_L1_SweepLocksInIDOrder(t *testing.T) {
	db := openTestDB(t)
	s := p134Seed(t, db, "pat@ext.example")
	ids := []uuid.UUID{uuid.New(), uuid.New()}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	tb := p134ID(t, db, `INSERT INTO teams (name, created_by) VALUES ('TB', $1) RETURNING id`, s.owner)
	for _, id := range []uuid.UUID{ids[1], ids[0]} { // the last-sorting id first in the heap
		p134Exec(t, db, `INSERT INTO team_invites (id, team_id, invitee_email, invited_by, token, expires_at) VALUES ($1, $2, $3, $4, $5, now() - interval '1 day')`,
			id, tb, "x"+id.String()[:8]+"@pac134.example", s.owner, uuid.NewString())
	}
	if !p134LockOrderProbe(t, db, `SELECT 1 FROM team_invites WHERE id = $1 FOR UPDATE`,
		`SELECT 1 FROM team_invites WHERE id = $1 FOR UPDATE NOWAIT`, []any{ids[0], ids[1]},
		func() error { return ExpireOldInvites(db) }) {
		t.Fatal("the sweep did not hold the first id while waiting on the last")
	}
}

// L-2 (contract md §6.2): the keyed-create cleanup locks its rows by
// (owner_user_id, idempotency_key "C").
func TestPAC134_L2_CleanupLocksInKeyOrder(t *testing.T) {
	db := openTestDB(t)
	s := p134Seed(t, db, "pat@ext.example")
	keys := []string{"pac134-l2-key-aaaaaaa", "pac134-l2-key-bbbbbbb"}
	for _, k := range []string{keys[1], keys[0]} {
		link := p134ID(t, db, `INSERT INTO scheduling_links (owner_user_id, slug, title) VALUES ($1, $2, 'K') RETURNING id`, s.owner, "k-"+k)
		p134Exec(t, db, `INSERT INTO scheduling_link_create_keys (owner_user_id, idempotency_key, link_id, created_at, request_sha256)
			VALUES ($1, $2, $3, now() - interval '25 hours', sha256('d'::bytea))`, s.owner, k, link)
	}
	q := `SELECT 1 FROM scheduling_link_create_keys WHERE owner_user_id = '` + s.owner.String() + `' AND idempotency_key = $1 FOR UPDATE`
	if !p134LockOrderProbe(t, db, q, q+" NOWAIT", []any{keys[0], keys[1]},
		func() error { _, err := ClearExpiredLinkCreateKeyData(context.Background(), db); return err }) {
		t.Fatal("the cleanup did not hold the first key while waiting on the last")
	}
}
