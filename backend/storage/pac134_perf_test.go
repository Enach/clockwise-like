//go:build pac134perf

package storage

import (
	"context"
	"testing"
	"time"
)

// AC-22b (contract md §7): the scale measurement, opt-in (build tag
// pac134perf: go test -tags pac134perf -run TestPAC134_AC22b ./storage/),
// not a CI gate. 1,000,000 bookings over 10,000
// links; 100,000 rows in each of T1-T8 and in operator_audit; 10,000
// accounts; 5 % of the stored addresses non-ASCII; no index on any matched
// expression; serial plans (max_parallel_workers_per_gather = 0, the worst
// case). The dry run, the erasure and a rerun must each finish inside the
// default 5 s bound; the erasure phase's lock-hold time (from the statement
// before lock_teams to the commit) is logged and must stay under 500 ms.
func TestPAC134_AC22b_ScaleMeasurement(t *testing.T) {
	db, dsn := p134Open(t, false)
	name := p134Row(t, db, `SELECT current_database()`)
	p134Exec(t, db, `ALTER DATABASE "`+name+`" SET max_parallel_workers_per_gather = 0`)
	build := []string{
		`INSERT INTO users (email, name) SELECT 'user' || g || CASE WHEN g % 20 = 0 THEN 'Ü' ELSE '' END || '@scale.example', 'U' FROM generate_series(1, 10000) g`,
		`CREATE TEMP TABLE u AS SELECT id, row_number() OVER (ORDER BY id) AS n FROM users`,
		`INSERT INTO teams (name, created_by) SELECT 'T' || n, id FROM u WHERE n <= 1000`,
		`CREATE TEMP TABLE tm AS SELECT id, row_number() OVER (ORDER BY id) AS n FROM teams`,
		`INSERT INTO team_added_members (team_id, email, added_by) SELECT tm.id, 'm' || g || CASE WHEN g % 20 = 0 THEN 'ü' ELSE '' END || '@scale.example', NULL
		   FROM generate_series(1, 100000) g JOIN tm ON tm.n = 1 + g % 1000`,
		`INSERT INTO team_invites (team_id, invitee_email, invited_by, token, status, expires_at)
		   SELECT tm.id, 'i' || g || CASE WHEN g % 20 = 0 THEN 'ü' ELSE '' END || '@scale.example', u.id, 'tok' || g, 'expired', now()
		   FROM generate_series(1, 100000) g JOIN tm ON tm.n = 1 + g % 1000 JOIN u ON u.n = 1 + g % 1000`,
		`INSERT INTO manager_team_members (manager_user_id, member_email) SELECT u.id, 'r' || g || '@scale.example' FROM generate_series(1, 100000) g JOIN u ON u.n = 1 + g % 1000`,
		`INSERT INTO manager_team_member_assignments (manager_user_id, team_id, member_email)
		   SELECT r.manager_user_id, tm.id, r.member_email FROM manager_team_members r JOIN tm ON tm.n = 1 + (hashtext(r.member_email) & 1023) % 1000`,
		`INSERT INTO one_on_one_occurrences (manager_user_id, member_email, calendar_event_id, occurred_at)
		   SELECT u.id, 'O' || g || CASE WHEN g % 20 = 0 THEN 'Ü' ELSE '' END || '@Scale.Example', 'ev' || g, now() FROM generate_series(1, 100000) g JOIN u ON u.n = 1 + g % 1000`,
		`INSERT INTO team_analytics_cache (manager_user_id, member_email, week_start)
		   SELECT u.id, 'c' || g || '@scale.example', '2026-11-02' FROM generate_series(1, 100000) g JOIN u ON u.n = 1 + g % 1000`,
		`INSERT INTO scheduling_links (owner_user_id, slug, title) SELECT u.id, 'sl' || g, 'L' FROM generate_series(1, 110000) g JOIN u ON u.n = 1 + g % 10000`,
		`CREATE TEMP TABLE ln AS SELECT id, owner_user_id, row_number() OVER (ORDER BY id) AS n FROM scheduling_links`,
		`INSERT INTO scheduling_link_hosts (link_id, invitee_email, status) SELECT ln.id, 'H' || g || CASE WHEN g % 20 = 0 THEN 'Ü' ELSE '' END || '@scale.example', 'pending'
		   FROM generate_series(1, 100000) g JOIN ln ON ln.n = g`,
		`INSERT INTO scheduling_link_create_keys (owner_user_id, idempotency_key, link_id, request_sha256, co_host_changes)
		   SELECT ln.owner_user_id, 'scale-key-' || lpad(g::text, 8, '0'), ln.id, sha256(g::text::bytea),
		     jsonb_build_object('addresses', jsonb_build_array(jsonb_build_object('email', 'a' || g || '@scale.example'), jsonb_build_object('email', 'B' || g || '@scale.example'),
		       jsonb_build_object('email', 'c' || g || CASE WHEN g % 20 = 0 THEN 'Ü' ELSE '' END || '@scale.example')), 'removed', jsonb_build_array(jsonb_build_object('email', 'd' || g || '@scale.example')))
		   FROM generate_series(1, 100000) g JOIN ln ON ln.n = 10000 + g`,
		`INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time, notes)
		   SELECT ln.id, 'B', 'Booker' || g || CASE WHEN g % 20 = 0 THEN 'Ü' ELSE '' END || '@Scale.Example', now(), now(), ''
		   FROM generate_series(1, 1000000) g JOIN ln ON ln.n = 1 + g % 10000`,
		`INSERT INTO operator_audit (action, operator, reason, account_email, details)
		   SELECT 'free_address', 'op', 'r', 'f' || g || '@scale.example', jsonb_build_object('freed_address', 'f' || g || '@scale.example') FROM generate_series(1, 100000) g`,
		`ANALYZE`,
	}
	start := time.Now()
	for _, q := range build {
		p134Exec(t, db, q)
	}
	p134Seed(t, db, "pat@ext.example")
	t.Logf("fixture built in %s", time.Since(start))
	run, err := OpenNoMigrateDescribeExec(dsn, EraseAddressApp)
	if err != nil {
		t.Fatal(err)
	}
	defer run.Close()
	for _, step := range []struct {
		name string
		dry  bool
	}{{"dry run", true}, {"erasure", false}, {"rerun", false}} {
		res, err := EraseAddress(context.Background(), run, EraseAddressOptions{
			Address: "pat@ext.example", DryRun: step.dry, MaxDuration: 5 * time.Second, Run: OperatorRun{Operator: "perf", Reason: "AC-22b"},
		})
		if err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		t.Logf("%s: total %s, lock phase %s, counts %v", step.name, res.Total, res.LockPhase, p134Erased(res))
		if !step.dry && res.LockPhase > 500*time.Millisecond {
			t.Errorf("%s: locks held %s", step.name, res.LockPhase)
		}
	}
}
