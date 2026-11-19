package main

// PAC-134 (spec docs/specs/PAC-134.md §4; contract
// contracts/features/PAC-134.md §14): paceday-operator erase-address, driven
// as the operator drives it (address on standard input, exit code, output).

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/testdb"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

var pac134Args = []string{"-operator", "ops-oncall", "-reason", "GDPR-REQ-0042"}

func pac134DryArgs() []string { return append([]string{"-dry-run"}, pac134Args...) }

// wantCounts checks the place lines against want (count) and kept.
func wantCounts(t *testing.T, out, key string, want map[string]int64, kept map[string]int64) {
	t.Helper()
	got := placeCounts(t, out, key)
	if len(got) != len(storage.ErasePlaceNames) {
		t.Fatalf("%d place lines, want %d:\n%s", len(got), len(storage.ErasePlaceNames), out)
	}
	for _, name := range storage.ErasePlaceNames {
		if got[name] != [2]int64{want[name], kept[name]} {
			t.Errorf("place %s: %s=%d kept_account_own=%d, want %d and %d", name, key, got[name][0], got[name][1], want[name], kept[name])
		}
	}
}

func auditDetails(t *testing.T, p *p134, id string) map[string]any {
	t.Helper()
	var raw string
	if err := p.db.QueryRow(`SELECT details::text FROM operator_audit WHERE id = $1`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

var auditLine = regexp.MustCompile(`(?m)^audit=([0-9a-f-]{36})$`)

func auditID(t *testing.T, out string) string {
	t.Helper()
	m := auditLine.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no audit= line:\n%s", out)
	}
	return m[1]
}

// AC-1: the dry run counts every copy (case and space variants), exit 0,
// changes nothing but one address-free dry-run record, applies no migration,
// and sends no lock_* or erase_* statement.
func TestPAC134_AC1_DryRunCountsAndChangesNothing(t *testing.T) {
	p := newP134(t)
	p.fixture()
	before := p.snapshot()
	v0, _, _, _ := storage.SchemaVersion(context.Background(), p.db)
	var names []string
	storage.InstallEraseStatement(t, func(name, _ string, _ []any) { names = append(names, name) })

	code, out, errw := p.run(" PAT@ext.example \n", pac134DryArgs()...)
	if code != exitOK {
		t.Fatalf("exit %d\n%s\n%s", code, out, errw)
	}
	if !strings.HasPrefix(out, "erase-address: dry run, no data changed\naccounts=none\n") {
		t.Errorf("output:\n%s", out)
	}
	wantCounts(t, out, "would_erase", pac134Counts, nil)
	if !strings.Contains(out, "place=operator_audit kept=1\n") || !strings.Contains(out, "total would_erase=17 kept_account_own=0 kept_operator_records=1\n") {
		t.Errorf("output:\n%s", out)
	}
	for _, n := range names {
		if strings.HasPrefix(n, "lock_") || strings.HasPrefix(n, "erase_") || n == "budget" || n == "analyze_found" {
			t.Errorf("the dry run sent %s", n)
		}
	}
	gone, added := before.diff(p.snapshot())
	for tb, rows := range gone {
		t.Errorf("%s: %d row(s) changed or removed by the dry run", tb, len(rows))
	}
	for tb, rows := range added {
		if tb != "operator_audit" || len(rows) != 1 {
			t.Errorf("%s: %d row(s) added", tb, len(rows))
		}
	}
	if s := leaks(strings.Join(added["operator_audit"], "")); s != "" {
		t.Errorf("the dry-run record holds %s", s)
	}
	if !strings.Contains(added["operator_audit"][0], `"action":"erase_address_dry_run"`) {
		t.Errorf("record %s", added["operator_audit"][0])
	}
	if v1, _, _, _ := storage.SchemaVersion(context.Background(), p.db); v1 != v0 {
		t.Errorf("schema moved %d -> %d", v0, v1)
	}
}

// AC-2..AC-5, AC-7: the erasure erases every copy, keeps every control row
// byte for byte, keeps bookings as erased bookings, clears the keyed
// creation, and writes one address-free record whose counts equal the dry
// run's.
func TestPAC134_AC2_AC7_ErasureOnTheFixture(t *testing.T) {
	p := newP134(t)
	f := p.fixture()
	if p.resolve(f.owner, f.createKey) != storage.LinkCreateKeyReplay {
		t.Fatal("the keyed creation does not replay before the erasure")
	}
	usesBefore := p.count(`SELECT count(*) FROM bookings WHERE link_id = $1`, f.l1)
	busyBefore := p.count(`SELECT count(*) FROM bookings WHERE link_id = $1 AND status <> 'cancelled'`, f.l1)
	bookingsBefore := map[uuid.UUID]string{}
	for _, id := range []uuid.UUID{f.bookingL1, f.bookingL2, f.bookingL3} {
		var s string
		if err := p.db.QueryRow(`SELECT concat_ws('|', link_id, start_time, end_time, status, created_at, idempotency_key) FROM bookings WHERE id = $1`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		bookingsBefore[id] = s
	}

	code, dryOut, errw := p.run(pac134X+"\n", pac134DryArgs()...)
	if code != exitOK {
		t.Fatalf("dry run exit %d %s", code, errw)
	}
	before := p.snapshot()
	code, out, errw := p.run(pac134X+"\n", pac134Args...)
	if code != exitOK {
		t.Fatalf("exit %d\n%s\n%s", code, out, errw)
	}
	if errw != "" {
		t.Errorf("stderr %q", errw)
	}
	// AC-7: counts equal the dry run's.
	wantCounts(t, out, "erased", pac134Counts, nil)
	dry := placeCounts(t, dryOut, "would_erase")
	if got := placeCounts(t, out, "erased"); fmt.Sprint(got) != fmt.Sprint(dry) {
		t.Errorf("erased %v, dry run %v", got, dry)
	}
	after := p.snapshot()
	gone, added := before.diff(after)
	// AC-2: every changed or removed row is one of X's copies; every
	// control row is byte for byte unchanged.
	for tb, rows := range gone {
		for _, r := range rows {
			if !isTarget(tb, r) {
				t.Errorf("%s: a control row changed: %s", tb, r)
			}
		}
	}
	// No row of any place matches X any more.
	for tb, rows := range after {
		for _, r := range rows {
			if isTarget(tb, r) {
				t.Errorf("%s: still matches X: %s", tb, r)
			}
		}
	}
	// AC-3: the copies are gone; other hosts and entries remain.
	for _, c := range []struct {
		q    string
		arg  uuid.UUID
		want int
	}{
		{`SELECT count(*) FROM team_added_members WHERE team_id = $1`, f.ta, 2},
		{`SELECT count(*) FROM team_invites WHERE team_id = $1`, f.ta, 1},
		{`SELECT count(*) FROM manager_team_members WHERE manager_user_id = $1`, f.manager, 1},
		{`SELECT count(*) FROM manager_team_member_assignments WHERE team_id = $1`, f.ta, 1},
		{`SELECT count(*) FROM one_on_one_occurrences WHERE manager_user_id = $1`, f.manager, 1},
		{`SELECT count(*) FROM team_analytics_cache WHERE manager_user_id = $1`, f.manager, 1},
		{`SELECT count(*) FROM scheduling_link_hosts WHERE link_id = $1`, f.l1, 2},
		{`SELECT count(*) FROM scheduling_link_hosts WHERE link_id = $1 AND user_id IS NOT NULL`, f.l1, 1},
		{`SELECT count(*) FROM scheduling_link_hosts WHERE link_id = $1`, f.l2, 0},
	} {
		if n := p.count(c.q, c.arg); n != c.want {
			t.Errorf("%s: %d, want %d", c.q, n, c.want)
		}
	}
	// AC-4: bookings kept, anonymised, notes blanked, erasure time set.
	for _, id := range []uuid.UUID{f.bookingL1, f.bookingL2, f.bookingL3} {
		var s, name, email, notes string
		var erased *time.Time
		if err := p.db.QueryRow(`SELECT concat_ws('|', link_id, start_time, end_time, status, created_at, idempotency_key), booker_name, booker_email, notes, booker_erased_at
			FROM bookings WHERE id = $1`, id).Scan(&s, &name, &email, &notes, &erased); err != nil {
			t.Fatal(err)
		}
		if s != bookingsBefore[id] || name != "" || email != "" || notes != "" || erased == nil {
			t.Errorf("booking %s: %q %q %q %q %v", id, s, name, email, notes, erased)
		}
	}
	if p.count(`SELECT count(*) FROM bookings WHERE link_id = $1`, f.l1) != usesBefore ||
		p.count(`SELECT count(*) FROM bookings WHERE link_id = $1 AND status <> 'cancelled'`, f.l1) != busyBefore {
		t.Error("L1's use count or busy bookings changed")
	}
	// AC-5: outcomes and digest cleared; owner, key, link, time kept; a retry
	// gets key reused. The control key still replays.
	var digestNull, changesNull bool
	var link uuid.UUID
	if err := p.db.QueryRow(`SELECT request_sha256 IS NULL, co_host_changes IS NULL, link_id FROM scheduling_link_create_keys WHERE owner_user_id = $1 AND idempotency_key = $2`,
		f.owner, f.createKey).Scan(&digestNull, &changesNull, &link); err != nil || !digestNull || !changesNull || link != f.createLink {
		t.Errorf("keyed creation: digest null %v outcomes null %v link %s err %v", digestNull, changesNull, link, err)
	}
	if o := p.resolve(f.owner, f.createKey); o != storage.LinkCreateKeyReused {
		t.Errorf("retry of the cleared creation: outcome %d, want key reused", o)
	}
	if o := p.resolve(f.owner, f.controlCreateKey); o != storage.LinkCreateKeyReplay {
		t.Errorf("the control key: outcome %d, want replay", o)
	}
	// AC-7: exactly one new erase_address record.
	if len(added["operator_audit"]) != 1 {
		t.Fatalf("operator_audit: %d new rows", len(added["operator_audit"]))
	}
	for tb, rows := range added {
		// Anonymised bookings and cleared keys are updates: as many rows
		// appear as were replaced.
		updated := tb == "bookings" || tb == "scheduling_link_create_keys"
		if tb != "operator_audit" && (!updated || len(rows) != len(gone[tb])) {
			t.Errorf("%s: %d row(s) added", tb, len(rows))
		}
	}
	id := auditID(t, out)
	var action, operator, reason string
	var accountID, accountEmail *string
	if err := p.db.QueryRow(`SELECT action, operator, reason, account_id::text, account_email FROM operator_audit WHERE id = $1`, id).
		Scan(&action, &operator, &reason, &accountID, &accountEmail); err != nil {
		t.Fatal(err)
	}
	if action != "erase_address" || operator != "ops-oncall" || reason != "GDPR-REQ-0042" || accountID != nil || accountEmail != nil {
		t.Errorf("record %s %s %s %v %v", action, operator, reason, accountID, accountEmail)
	}
	d := auditDetails(t, p, id)
	places := d["places"].(map[string]any)
	for name, n := range pac134Counts {
		pl := places[name].(map[string]any)
		if pl["erased"] != float64(n) || pl["kept_account_own"] != float64(0) {
			t.Errorf("record %s: %v", name, pl)
		}
	}
	if places["operator_audit"].(map[string]any)["kept"] != float64(1) || len(d["accounts"].([]any)) != 0 || d["account_acknowledged"] != false ||
		d["schema"] != float64(1) || d["dry_run"] != false || d["max_duration_ms"] != float64(5000) {
		t.Errorf("record details %v", d)
	}
	// AC-7: no column of either new record, nor stdout or stderr, holds X
	// (any case), its display name or a digest of it.
	var all []string
	rows, err := p.db.Query(`SELECT row_to_json(o)::text FROM operator_audit o WHERE action IN ('erase_address', 'erase_address_dry_run')`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var r string
		_ = rows.Scan(&r)
		all = append(all, r)
	}
	rows.Close()
	if len(all) != 2 {
		t.Fatalf("%d erase records", len(all))
	}
	for _, text := range append(all, out, errw, dryOut) {
		if s := leaks(text); s != "" {
			t.Errorf("holds %s: %s", s, text)
		}
	}

	// AC-8: a second erasure erases nothing; its only write is its record.
	before = p.snapshot()
	code, out, errw = p.run(pac134X+"\n", pac134Args...)
	if code != exitOK {
		t.Fatalf("rerun exit %d %s", code, errw)
	}
	wantCounts(t, out, "erased", nil, nil)
	gone, added = before.diff(p.snapshot())
	if len(gone) != 0 || len(added) != 1 || len(added["operator_audit"]) != 1 {
		t.Errorf("rerun changed %v added %v", gone, added)
	}
}

// AC-19: an address with no match: exit 0, all counts 0, one record.
func TestPAC134_AC19_NobodyMatches(t *testing.T) {
	p := newP134(t)
	p.fixture()
	code, out, errw := p.run("nobody@ext.example\n", pac134Args...)
	if code != exitOK {
		t.Fatalf("exit %d %s", code, errw)
	}
	wantCounts(t, out, "erased", nil, nil)
	if !strings.Contains(out, "place=operator_audit kept=0\n") || !strings.Contains(out, "total erased=0 kept_account_own=0 kept_operator_records=0\n") {
		t.Errorf("output:\n%s", out)
	}
	if n := p.count(`SELECT count(*) FROM operator_audit WHERE action = 'erase_address'`); n != 1 {
		t.Errorf("%d records", n)
	}
}

// AC-10 (and D-7): every input refusal exits 2 with a fixed usage line that
// does not hold the address, before any database connection (the DSN points
// at a closed port: a dial would be exit 1).
func TestPAC134_AC10_UsageRefusals(t *testing.T) {
	p := &p134{t: t}
	closed := map[string]string{"DATABASE_URL": "postgres://u:p@127.0.0.1:1/none?sslmode=disable&connect_timeout=1"}
	long := strings.Repeat("a", 4090) + "@x.example"
	for _, c := range []struct {
		name, stdin string
		args        []string
		env         map[string]string
		want        string
	}{
		{"nothing on stdin", "", pac134Args, closed, "no address on standard input"},
		{"blank stdin", " \u00a0\n", pac134Args, closed, "no address on standard input"},
		{"not an address", "not-an-address\n", pac134Args, closed, "not a valid address"},
		{"two @", "pat@@ext.example\n", pac134Args, closed, "not a valid address"},
		{"display name", "Pat <pat@ext.example>\n", pac134Args, closed, "not a valid address"},
		{"address argument", pac134X + "\n", append([]string{pac134X}, pac134Args...), closed, "arguments are not accepted"},
		{"address argument last", "", append(append([]string{}, pac134Args...), pac134X), closed, "arguments are not accepted"},
		{"address as a flag", pac134X + "\n", append([]string{"-" + pac134X}, pac134Args...), closed, "invalid flags"},
		{"no operator", pac134X + "\n", []string{"-reason", "r"}, closed, "-operator is required"},
		{"blank operator", pac134X + "\n", []string{"-operator", " ", "-reason", "r"}, closed, "-operator is required"},
		{"no reason", pac134X + "\n", []string{"-operator", "o"}, closed, "-reason is required"},
		{"reason holds the address", pac134X + "\n", []string{"-operator", "o", "-reason", "req for PAT@EXT.EXAMPLE"}, closed, "must not contain the address"},
		{"operator holds the address", "PAT@EXT.EXAMPLE\n", []string{"-operator", "pat@ext.example", "-reason", "r"}, closed, "must not contain the address"},
		{"reason holds the address, folded", "pat@ext.example\n", []string{"-operator", "o", "-reason", "\u212aIM pat@ext.example"}, closed, "must not contain the address"},
		// Review of #326, MX19: only Unicode simple folding equates the long
		// s (U+017F) with s; Go's lowercasing leaves it alone.
		{"reason holds the address, long s", "sam@ext.example\n", []string{"-operator", "o", "-reason", "req \u017fam@ext.example"}, closed, "must not contain the address"},
		{"two lines", pac134X + "\nother@x.example\n", pac134Args, closed, "more than one line"},
		{"too long", long + "\n", pac134Args, closed, "longer than 4096 bytes"},
		{"not UTF-8", "pat@ext.example\xff\n", pac134Args, closed, "not valid UTF-8"},
		{"NUL", "pat@ext.example\x00\n", pac134Args, closed, "not valid UTF-8"},
		{"duration too long", pac134X + "\n", append([]string{"-max-duration", "9s"}, pac134Args...), closed, "between 1s and 8s"},
		{"duration too short", pac134X + "\n", append([]string{"-max-duration", "500ms"}, pac134Args...), closed, "between 1s and 8s"},
		{"no DATABASE_URL", pac134X + "\n", pac134Args, map[string]string{}, "DATABASE_URL is required"},
	} {
		code, out, errw := p.runEnv(c.env, c.stdin, c.args...)
		if code != exitUsage || out != "" || !strings.Contains(errw, c.want) || !strings.HasPrefix(errw, "erase-address: ") ||
			!strings.Contains(errw, eraseAddressUsage) || strings.Count(errw, "\n") != 1 {
			t.Errorf("%s: exit %d out %q err %q", c.name, code, out, errw)
		}
		if s := leaks(errw); s != "" {
			t.Errorf("%s: stderr holds %s", c.name, s)
		}
	}
}

// AC-15: a database error whose driver message would hold X never reaches
// either stream; no statement's text holds X (binds only), and the address
// is bound only to input_lower ($1 = t) and as :g, :d.
func TestPAC134_AC15_ErrorsAndStatementsHoldNoAddress(t *testing.T) {
	p := newP134(t)
	p.fixture()
	p.exec(`CREATE FUNCTION pac134_boom() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'cannot update booking of %', OLD.booker_email; END $$`)
	p.exec(`CREATE TRIGGER pac134_boom BEFORE UPDATE ON bookings FOR EACH ROW EXECUTE FUNCTION pac134_boom()`)
	before := p.snapshot()
	type stmt struct {
		name, sql string
		args      []any
	}
	var sent []stmt
	storage.InstallEraseStatement(t, func(name, sql string, args []any) { sent = append(sent, stmt{name, sql, args}) })
	code, out, errw := p.run("PAT@Ext.Example\n", pac134Args...)
	if code != exitFailed || errw != "erase-address: failed: sqlstate P0001; no data changed\n" {
		t.Fatalf("exit %d\n%s\n%s", code, out, errw)
	}
	if s := leaks(out + errw); s != "" {
		t.Errorf("output holds %s", s)
	}
	gone, added := before.diff(p.snapshot())
	if len(gone) != 0 || len(added) != 0 {
		t.Errorf("a failed run changed data: %v %v", gone, added)
	}
	if len(sent) < 20 {
		t.Fatalf("%d statements recorded", len(sent))
	}
	for _, s := range sent {
		if strings.Contains(strings.ToLower(s.sql), pac134X) {
			t.Errorf("%s: the statement text holds the address", s.name)
		}
		for i, a := range s.args {
			text := strings.ToLower(fmt.Sprint(a))
			if !strings.Contains(text, pac134X) {
				continue
			}
			ok := (s.name == "input_lower" && i == 0) || (i <= 1 && s.name != "settings" && s.name != "budget" && s.name != "audit")
			if !ok {
				t.Errorf("%s: argument %d holds the address", s.name, i+1)
			}
		}
	}
}

// AC-15, revision 2 (B-1): a DATABASE_URL that asks pgx for the simple
// protocol is overridden: while the run waits on a lock, the server's view of
// its statement holds placeholders, never the address, and the lock timeout
// it ends on is reported by class.
func TestPAC134_AC15_SimpleProtocolDSNIsOverridden(t *testing.T) {
	p := newP134(t)
	f := p.fixture()
	holder, err := p.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback() }()
	if _, err := holder.Exec(`SELECT 1 FROM bookings WHERE id = $1 FOR UPDATE`, f.bookingL1); err != nil {
		t.Fatal(err)
	}
	dsn := p.dsn + "&default_query_exec_mode=simple_protocol"
	if !strings.Contains(p.dsn, "?") {
		dsn = p.dsn + "?default_query_exec_mode=simple_protocol"
	}
	seen := make(chan []string, 1)
	go func() {
		admin, err := storage.OpenNoMigrate(testdb.AdminDSN(), "pac134-observer")
		if err != nil {
			seen <- []string{"observer: " + err.Error()}
			return
		}
		defer admin.Close()
		var queries []string
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			var q string
			err := admin.QueryRow(`SELECT query FROM pg_stat_activity WHERE application_name = $1 AND wait_event_type = 'Lock'`, storage.EraseAddressApp).Scan(&q)
			if err == nil {
				queries = append(queries, q)
				if len(queries) >= 3 {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		seen <- queries
	}()
	code, out, errw := p.runEnv(map[string]string{"DATABASE_URL": dsn}, pac134X+"\n", append([]string{"-max-duration", "1s"}, pac134Args...)...)
	queries := <-seen
	if code != exitFailed || !strings.Contains(errw, "erase-address: failed: ") || leaks(out+errw) != "" {
		t.Fatalf("exit %d\n%s\n%s", code, out, errw)
	}
	if len(queries) == 0 {
		t.Fatal("the run was never seen waiting on the held booking")
	}
	for _, q := range queries {
		if strings.Contains(strings.ToLower(q), pac134X) || !strings.Contains(q, "$1") {
			t.Errorf("the server's statement text: %s", q)
		}
	}
}
