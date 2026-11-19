package main

// PAC-134 test harness: the spec's fixture (spec §4, "Fixture shared by
// AC-1..AC-15"), a whole-database snapshot, and a runner that feeds the
// address on standard input.

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/internal/testdb"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// pac134X is the spec's address X.
const pac134X = "pat@ext.example"

// pac134Name is X's display name, stored beside it; it must never be output.
const pac134Name = "Pat Person"

const nbsp = " "

type p134 struct {
	t   *testing.T
	dsn string
	db  *sql.DB
}

func newP134(t *testing.T) *p134 {
	t.Helper()
	dsn, cleanup, err := testdb.Create()
	if err != nil {
		t.Fatalf("PostgreSQL unavailable: %v", err)
	}
	db, err := storage.Open(dsn)
	if err != nil {
		_ = cleanup()
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); _ = cleanup() })
	return &p134{t: t, dsn: dsn, db: db}
}

// run runs erase-address with stdin and args on the test database.
func (p *p134) run(stdin string, args ...string) (int, string, string) {
	p.t.Helper()
	return p.runEnv(map[string]string{"DATABASE_URL": p.dsn}, stdin, args...)
}

func (p *p134) runEnv(env map[string]string, stdin string, args ...string) (int, string, string) {
	p.t.Helper()
	var out, errw bytes.Buffer
	code := runEraseAddress(context.Background(), args, func(k string) string { return env[k] }, strings.NewReader(stdin), &out, &errw)
	return code, out.String(), errw.String()
}

func (p *p134) exec(q string, args ...any) {
	p.t.Helper()
	if _, err := p.db.Exec(q, args...); err != nil {
		p.t.Fatalf("%s: %v", q, err)
	}
}

func (p *p134) id(q string, args ...any) uuid.UUID {
	p.t.Helper()
	var id uuid.UUID
	if err := p.db.QueryRow(q, args...).Scan(&id); err != nil {
		p.t.Fatalf("%s: %v", q, err)
	}
	return id
}

func (p *p134) count(q string, args ...any) int {
	p.t.Helper()
	var n int
	if err := p.db.QueryRow(q, args...).Scan(&n); err != nil {
		p.t.Fatalf("%s: %v", q, err)
	}
	return n
}

func (p *p134) user(email string) uuid.UUID {
	return p.id(`INSERT INTO users (email, name) VALUES ($1, 'Account') RETURNING id`, email)
}

func (p *p134) team(creator uuid.UUID) uuid.UUID {
	id := p.id(`INSERT INTO teams (name, created_by) VALUES ('Team', $1) RETURNING id`, creator)
	p.exec(`INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $2, 'owner')`, id, creator)
	return id
}

// link stores a link: state is "live", "paused" or "deleted".
func (p *p134) link(owner uuid.UUID, state, title string) uuid.UUID {
	active := state == "live"
	var deleted any
	if state == "deleted" {
		deleted = "now"
	}
	return p.id(`INSERT INTO scheduling_links (owner_user_id, slug, title, active, deleted_at)
		VALUES ($1, $2, $3, $4, $5::timestamptz) RETURNING id`, owner, "s-"+uuid.NewString()[:12], title, active, deleted)
}

func (p *p134) booking(link uuid.UUID, name, email, notes, status, key string) uuid.UUID {
	var k any
	if key != "" {
		k = key
	}
	return p.id(`INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time, status, notes, idempotency_key)
		VALUES ($1, $2, $3, now() + interval '2 days', now() + interval '2 days 30 minutes', $4, $5, $6) RETURNING id`,
		link, name, email, status, notes, k)
}

// pac134Digest is the stored request digest of the fixture's keyed creations.
var pac134Digest = func() []byte { s := sha256.Sum256([]byte("pac134 keyed creation")); return s[:] }()

func (p *p134) keyedCreation(owner, link uuid.UUID, key string, changes string) {
	p.exec(`INSERT INTO scheduling_link_create_keys (owner_user_id, idempotency_key, link_id, request_sha256, co_host_changes)
		VALUES ($1, $2, $3, $4, $5::jsonb)`, owner, key, link, pac134Digest, changes)
}

// resolve resolves a keyed creation retry with the stored digest: a replay
// while the outcomes are stored, key reused once they are cleared.
func (p *p134) resolve(owner uuid.UUID, key string) storage.LinkCreateKeyOutcome {
	p.t.Helper()
	res, err := storage.ResolveLinkCreateKeyCtx(context.Background(), p.db, owner, &storage.LinkCreateAttempt{Key: key, Digest: pac134Digest})
	if err != nil {
		p.t.Fatal(err)
	}
	return res.Outcome
}

// pac134Fixture is the spec's fixture (spec §4): copies of X in every place,
// with case and space variants where the column allows them, and control rows.
type pac134Fixture struct {
	owner, manager, host            uuid.UUID
	ta                              uuid.UUID
	l1, l2, l3                      uuid.UUID
	bookingL1, bookingL3, bookingL2 uuid.UUID
	controlBooking, controlBooking2 uuid.UUID
	createKey, controlCreateKey     string
	bookingKey                      string
	createLink                      uuid.UUID
}

// pac134Counts is the fixture's per-place count of X's copies (AC-1).
var pac134Counts = map[string]int64{
	"team_added_members": 1, "team_invites": 4, "manager_team_members": 1,
	"manager_team_member_assignments": 1, "one_on_one_occurrences": 2, "team_analytics_cache": 2,
	"scheduling_link_hosts": 2, "scheduling_link_create_keys": 1, "bookings": 3,
}

func (p *p134) fixture() *pac134Fixture {
	p.t.Helper()
	f := &pac134Fixture{}
	f.owner = p.user("owner@pac134.example")
	f.manager = p.user("manager@pac134.example")
	f.host = p.user("host@pac134.example")
	f.ta = p.team(f.owner)

	// T1: the CHECK keeps the normalised form only.
	p.exec(`INSERT INTO team_added_members (team_id, email, display_name, added_by) VALUES ($1, $2, $3, $4)`, f.ta, pac134X, pac134Name, f.owner)
	p.exec(`INSERT INTO team_added_members (team_id, email, display_name, added_by) VALUES ($1, 'pat2@ext.example', 'Control', $2)`, f.ta, f.owner)
	p.exec(`INSERT INTO team_added_members (team_id, email, display_name, added_by) VALUES ($1, 'xpat@ext.example', 'Control', $2)`, f.ta, f.owner)
	// T2: all four statuses.
	for _, st := range []string{"pending", "accepted", "declined", "expired"} {
		p.exec(`INSERT INTO team_invites (team_id, invitee_email, invited_by, token, status, expires_at) VALUES ($1, $2, $3, $4, $5, now() + interval '7 days')`,
			f.ta, pac134X, f.owner, "tok-x-"+st+uuid.NewString(), st)
	}
	p.exec(`INSERT INTO team_invites (team_id, invitee_email, invited_by, token, status, expires_at) VALUES ($1, 'pat2@ext.example', $2, $3, 'pending', now() + interval '7 days')`, f.ta, f.owner, "tok-c-"+uuid.NewString())
	// T3 (btrim-only CHECK: a leading U+00A0 is allowed), T4.
	p.exec(`INSERT INTO manager_team_members (manager_user_id, member_email, display_name) VALUES ($1, $2, $3)`, f.manager, nbsp+pac134X, pac134Name)
	p.exec(`INSERT INTO manager_team_members (manager_user_id, member_email, display_name) VALUES ($1, 'pat2@ext.example', 'Control')`, f.manager)
	p.exec(`INSERT INTO manager_team_member_assignments (manager_user_id, team_id, member_email) VALUES ($1, $2, $3)`, f.manager, f.ta, nbsp+pac134X)
	p.exec(`INSERT INTO manager_team_member_assignments (manager_user_id, team_id, member_email) VALUES ($1, $2, 'pat2@ext.example')`, f.manager, f.ta)
	// T5, T6: no constraint, so case and space variants.
	p.exec(`INSERT INTO one_on_one_occurrences (manager_user_id, member_email, calendar_event_id, occurred_at) VALUES ($1, 'Pat@Ext.Example', 'ev1', now()), ($1, $2, 'ev2', now()), ($1, 'xpat@ext.example', 'ev3', now())`, f.manager, nbsp+pac134X)
	p.exec(`INSERT INTO team_analytics_cache (manager_user_id, member_email, week_start) VALUES ($1, 'PAT@ext.example', '2026-11-02'), ($1, $2, '2026-11-09'), ($1, 'pat2@ext.example', '2026-11-09')`, f.manager, pac134X+" ")
	// Links: L1 live (title holds X: free text), L2 deleted, L3 paused.
	f.l1 = p.link(f.owner, "live", "Meeting with "+pac134X)
	f.l2 = p.link(f.owner, "deleted", "Old link")
	f.l3 = p.link(f.owner, "paused", "Paused link")
	// T7: pending co-host invitations on L1 and L2; an accepted host on L1.
	p.exec(`INSERT INTO scheduling_link_hosts (link_id, user_id, status) VALUES ($1, $2, 'accepted')`, f.l1, f.host)
	p.exec(`INSERT INTO scheduling_link_hosts (link_id, invitee_email, status) VALUES ($1, 'Pat@Ext.Example', 'pending')`, f.l1)
	p.exec(`INSERT INTO scheduling_link_hosts (link_id, invitee_email, status) VALUES ($1, 'pat2@ext.example', 'pending')`, f.l1)
	p.exec(`INSERT INTO scheduling_link_hosts (link_id, invitee_email, status) VALUES ($1, $2, 'pending')`, f.l2, nbsp+pac134X)
	// T8: a keyed creation younger than 24 h listing X in addresses and removed.
	f.createLink = p.link(f.owner, "live", "Created")
	f.createKey = "pac134-create-key-0001"
	p.keyedCreation(f.owner, f.createLink, f.createKey,
		`{"invited_count":1,"not_added_count":0,"addresses":[{"email":"Pat@Ext.Example","outcome":"invited"},{"email":"other@ext.example","outcome":"invited"}],"removed":[{"email":"PAT@EXT.EXAMPLE","status":"pending"}]}`)
	controlLink := p.link(f.owner, "live", "Control created")
	f.controlCreateKey = "pac134-create-key-0002"
	p.keyedCreation(f.owner, controlLink, f.controlCreateKey,
		`{"invited_count":1,"not_added_count":0,"addresses":[{"email":"pat2@ext.example","outcome":"invited"}],"removed":[]}`)
	// T9: bookings by X on live L1 (keyed), paused L3 (cancelled), deleted L2.
	f.bookingKey = "pac134-booking-key-0001"
	f.bookingL1 = p.booking(f.l1, pac134Name, "Pat@Ext.Example", "Please call "+pac134Name, "confirmed", f.bookingKey)
	f.bookingL3 = p.booking(f.l3, pac134Name, nbsp+pac134X, "notes", "cancelled", "")
	f.bookingL2 = p.booking(f.l2, pac134Name, pac134X, "notes", "confirmed", "")
	// Controls: another booker whose notes hold X, and xpat.
	f.controlBooking = p.booking(f.l1, "Other", "pat2@ext.example", "Bring "+pac134X, "confirmed", "")
	f.controlBooking2 = p.booking(f.l1, "Other", "xpat@ext.example", "", "confirmed", "")
	// T10: an earlier free-address record whose old address was X.
	p.exec(`INSERT INTO operator_audit (action, operator, reason, account_id, account_email, details)
		VALUES ('free_address', 'op', 'PAC-1', $1, 'Pat@Ext.Example', '{"freed_address":"Pat@Ext.Example"}')`, uuid.New())
	// Free text: an nlp_parsed activity entry holding X.
	p.exec(`INSERT INTO audit_log (action, details, user_id) VALUES ('nlp_parsed', $1, $2)`, "meet "+pac134X+" tomorrow", f.owner)
	return f
}

// snapshot is every row of every public table, as JSON text, per table.
type snapshot map[string][]string

func (p *p134) snapshot() snapshot {
	p.t.Helper()
	rows, err := p.db.Query(`SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' AND table_type = 'BASE TABLE'`)
	if err != nil {
		p.t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		tables = append(tables, n)
	}
	rows.Close()
	s := snapshot{}
	for _, tb := range tables {
		rs, err := p.db.Query(`SELECT row_to_json(t)::text FROM "` + tb + `" t`)
		if err != nil {
			p.t.Fatal(err)
		}
		list := []string{}
		for rs.Next() {
			var r string
			_ = rs.Scan(&r)
			list = append(list, r)
		}
		rs.Close()
		sort.Strings(list)
		s[tb] = list
	}
	return s
}

// diff returns, per table, the rows of a missing from b and of b missing
// from a.
func (a snapshot) diff(b snapshot) (gone, added map[string][]string) {
	gone, added = map[string][]string{}, map[string][]string{}
	for tb := range a {
		ga, gb := multiset(a[tb]), multiset(b[tb])
		for r, n := range ga {
			for i := gb[r]; i < n; i++ {
				gone[tb] = append(gone[tb], r)
			}
		}
		for r, n := range gb {
			for i := ga[r]; i < n; i++ {
				added[tb] = append(added[tb], r)
			}
		}
	}
	return gone, added
}

func multiset(l []string) map[string]int {
	m := map[string]int{}
	for _, r := range l {
		m[r]++
	}
	return m
}

// pac134MatchesX is the §3.3 rule on ASCII-only test values: the Go
// lowercasing of the trimmed value equals X.
func pac134MatchesX(v string) bool {
	return strings.ToLower(storage.TrimAddressSpace(v)) == pac134X
}

// pac134TargetColumn is the column of each place that holds the address.
var pac134TargetColumn = map[string]string{
	"team_added_members": "email", "team_invites": "invitee_email", "manager_team_members": "member_email",
	"manager_team_member_assignments": "member_email", "one_on_one_occurrences": "member_email",
	"team_analytics_cache": "member_email", "scheduling_link_hosts": "invitee_email", "bookings": "booker_email",
}

// isTarget reports whether a snapshot row is one of X's copies.
func isTarget(table, row string) bool {
	var m map[string]any
	if err := json.Unmarshal([]byte(row), &m); err != nil {
		return false
	}
	if table == "scheduling_link_create_keys" {
		c, _ := m["co_host_changes"].(map[string]any)
		for _, k := range []string{"addresses", "removed"} {
			items, _ := c[k].([]any)
			for _, it := range items {
				if e, _ := it.(map[string]any)["email"].(string); pac134MatchesX(e) {
					return true
				}
			}
		}
		return false
	}
	col, ok := pac134TargetColumn[table]
	if !ok {
		return false
	}
	v, _ := m[col].(string)
	return pac134MatchesX(v)
}

// digests are the hex digests of the trimmed address in either lowercasing
// (AC-7), lowercase.
func digests(t, g, d string) []string {
	var out []string
	for _, v := range []string{t, g, d} {
		m := md5.Sum([]byte(v))
		s1 := sha1.Sum([]byte(v))
		s2 := sha256.Sum256([]byte(v))
		out = append(out, hex.EncodeToString(m[:]), hex.EncodeToString(s1[:]), hex.EncodeToString(s2[:]))
	}
	return out
}

// leaks reports what of X (any case), its display name or a digest of X a
// text holds; "" when nothing.
func leaks(text string) string {
	l := strings.ToLower(text)
	if strings.Contains(l, pac134X) {
		return "the address"
	}
	if strings.Contains(l, strings.ToLower(pac134Name)) {
		return "the display name"
	}
	for _, d := range digests(pac134X, pac134X, pac134X) {
		if strings.Contains(l, d) {
			return "a digest " + d
		}
	}
	for _, d := range digests("PAT@ext.example", pac134X, pac134X) {
		if strings.Contains(l, d) {
			return "a digest " + d
		}
	}
	return ""
}

// placeCounts parses the place= lines of the output: name -> [count, kept].
func placeCounts(t *testing.T, out, key string) map[string][2]int64 {
	t.Helper()
	got := map[string][2]int64{}
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "place=") || strings.HasPrefix(line, "place=operator_audit") {
			continue
		}
		var name string
		var n, k int64
		f := strings.Fields(line)
		if len(f) != 3 {
			t.Fatalf("place line %q", line)
		}
		name = strings.TrimPrefix(f[0], "place=")
		if _, err := fmt.Sscanf(f[1], key+"=%d", &n); err != nil {
			t.Fatalf("place line %q: %v", line, err)
		}
		if _, err := fmt.Sscanf(f[2], "kept_account_own=%d", &k); err != nil {
			t.Fatalf("place line %q: %v", line, err)
		}
		got[name] = [2]int64{n, k}
	}
	return got
}
