package storage

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/domain"
	"github.com/golang-migrate/migrate/v4"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// PAC-148 migration 043 (docs/specs/PAC-148.md §3.5, AC-7, AC-16, AC-17;
// contracts/features/PAC-148.md §5): every stored organisation membership
// that fails the rule ends, with the accounts' organisation-AI confirmations;
// nothing else changes; a configuration on a consumer-list domain aborts it
// with no data change, leaving 43 dirty, and forcing to 42 is startable by an
// image whose highest migration is 42.

const pac148MigrationFile = "migrations/043_org_membership_rule.up.sql"

// pac148Exec runs statements on one short-lived connection to dsn, which is
// gone when it returns (PAC-116: the migration procedure counts sessions).
func pac148Exec(t *testing.T, dsn string, fn func(q func(query string, args ...any) *sql.Row, exec func(query string, args ...any))) {
	t.Helper()
	pac116Observe(t, dsn, func(conn *sql.Conn) {
		ctx := context.Background()
		fn(func(query string, args ...any) *sql.Row { return conn.QueryRowContext(ctx, query, args...) },
			func(query string, args ...any) {
				t.Helper()
				if _, err := conn.ExecContext(ctx, query, args...); err != nil {
					t.Fatalf("%s: %v", query, err)
				}
			})
	})
}

// pac148Fix is AC-7's fixture, written at version 42.
type pac148Fix struct {
	acme, other, hotmail, isp                 uuid.UUID
	u, l, a1, a4, x, a3, i1, i2, i3, outsider uuid.UUID
}

// pac148Seed writes AC-7's fixture on dsn (at 42) and returns its ids.
func pac148Seed(t *testing.T, dsn string, withGateRows bool) pac148Fix {
	t.Helper()
	var f pac148Fix
	pac148Exec(t, dsn, func(q func(string, ...any) *sql.Row, exec func(string, ...any)) {
		org := func(d string) uuid.UUID {
			var id uuid.UUID
			if err := q(`INSERT INTO organizations (name, domain) VALUES ($1, $1) RETURNING id`, d).Scan(&id); err != nil {
				t.Fatal(err)
			}
			return id
		}
		user := func(email string, org uuid.UUID, standing string) uuid.UUID {
			var orgID any
			if org != uuid.Nil {
				orgID = org
			}
			var id uuid.UUID
			if err := q(`INSERT INTO users (email, name, org_id, address_standing) VALUES ($1, 'N', $2, $3) RETURNING id`,
				email, orgID, standing).Scan(&id); err != nil {
				t.Fatalf("user %s: %v", email, err)
			}
			return id
		}
		bind := func(id uuid.UUID, evidence string) {
			exec(`INSERT INTO user_identities (user_id, provider, tenant, subject, evidence, bound_by)
				VALUES ($1, 'google', '', $2, $3, 'created')`, id, id.String(), evidence)
		}
		confirm := func(user, org uuid.UUID) {
			exec(`INSERT INTO org_ai_confirmations (user_id, org_id, consent_since_version, confirmed_at) VALUES ($1, $2, 1, now())`, user, org)
		}
		f.acme, f.other, f.hotmail, f.isp = org("acme.pac148.test"), org("other.pac148.test"), org("hotmail.fr"), org("isp.pac148.test")
		f.u = user("u@acme.pac148.test", f.acme, AddressUnconfirmed)
		bind(f.u, "mailbox")
		f.l = user("l@acme.pac148.test", f.acme, AddressUnconfirmed) // unbound legacy
		f.a1 = user("a1@acme.pac148.test", f.acme, AddressDomainConfirmed)
		bind(f.a1, "authoritative")
		f.a4 = user(" A4@ ACME.pac148.TEST ", f.acme, AddressDomainConfirmed) // white space and case
		f.x = user("x@hotmail.fr", f.hotmail, AddressDomainConfirmed)
		f.a3 = user("a3@other.pac148.test", f.acme, AddressDomainConfirmed)
		f.i1 = user("i1@isp.pac148.test", f.isp, AddressUnconfirmed)
		bind(f.i1, "mailbox")
		f.i2 = user("i2@isp.pac148.test", f.isp, AddressUnconfirmed)
		f.i3 = user("i3@isp.pac148.test", f.isp, AddressDomainConfirmed)
		f.outsider = user("n@nowhere.pac148.test", uuid.Nil, AddressUnconfirmed)
		confirm(f.u, f.acme)
		confirm(f.u, f.other) // every confirmation ends, not only the organisation left
		confirm(f.l, f.acme)
		confirm(f.a1, f.acme)
		confirm(f.a3, f.acme)
		exec(`INSERT INTO org_ai_configurations (org_id, provider, model) VALUES ($1, 'openai', 'gpt-x')`, f.acme)
		exec(`INSERT INTO sso_providers (domain, provider_name, provider_type, oidc_issuer, oidc_client_id) VALUES ('acme.pac148.test', 'ACME', 'oidc', 'https://idp.acme.test', 'c')`)
		exec(`INSERT INTO sso_events (user_id, org_id, kind, details) VALUES ($1, $2, 'sso_stepup', '{"outcome":"granted"}')`, f.a1, f.acme)
		exec(`INSERT INTO sso_stepup_grants (user_id, org_id, provider, purpose, issued_at, expires_at) VALUES ($1, $2, 'google', 'sso_provider_write', now(), now() + interval '5 minutes')`, f.a1, f.acme)
		if withGateRows {
			exec(`INSERT INTO sso_providers (domain, provider_name, provider_type) VALUES ('Hotmail.FR', 'Hot', 'oidc')`)
			exec(`INSERT INTO org_ai_configurations (org_id, provider) VALUES ($1, 'openai')`, f.hotmail)
		}
	})
	return f
}

// pac148Snapshot is every row of every public table but schema_migrations,
// as text, per table, sorted.
func pac148Snapshot(t *testing.T, dsn string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	pac116Observe(t, dsn, func(conn *sql.Conn) {
		ctx := context.Background()
		rows, err := conn.QueryContext(ctx, `SELECT table_name FROM information_schema.tables
			WHERE table_schema = 'public' AND table_type = 'BASE TABLE' AND table_name <> 'schema_migrations' ORDER BY 1`)
		if err != nil {
			t.Fatal(err)
		}
		var tables []string
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				t.Fatal(err)
			}
			tables = append(tables, n)
		}
		_ = rows.Close()
		for _, tb := range tables {
			r, err := conn.QueryContext(ctx, fmt.Sprintf(`SELECT to_jsonb(x)::text FROM %q x`, tb))
			if err != nil {
				t.Fatal(err)
			}
			var vals []string
			for r.Next() {
				var s string
				if err := r.Scan(&s); err != nil {
					t.Fatal(err)
				}
				vals = append(vals, s)
			}
			_ = r.Close()
			sort.Strings(vals)
			out[tb] = vals
		}
	})
	return out
}

// pac148Rows is one query's single-text-column answer, sorted.
func pac148Rows(t *testing.T, dsn, query string) []string {
	t.Helper()
	var vals []string
	pac116Observe(t, dsn, func(conn *sql.Conn) {
		r, err := conn.QueryContext(context.Background(), query)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		for r.Next() {
			var s string
			if err := r.Scan(&s); err != nil {
				t.Fatal(err)
			}
			vals = append(vals, s)
		}
	})
	sort.Strings(vals)
	return vals
}

func pac148Migrate(t *testing.T, dsn string) error {
	t.Helper()
	_, err := MigrateWhenStopped(context.Background(), dsn, MigrateOptions{Caller: "pac148 test"})
	return err
}

// pac148RunUpWithNotices runs the up file's text once on dsn through a pgx
// connection and returns the NOTICEs it raised (golang-migrate does not
// surface them; contract md §5.1, revision 2 N-5).
func pac148RunUpWithNotices(t *testing.T, dsn string) []string {
	t.Helper()
	body, err := migrationFiles.ReadFile(pac148MigrationFile)
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
		t.Fatalf("run 043's up file: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), notices...)
}

// AC-7: the migration ends exactly the failing memberships (unconfirmed,
// unbound legacy, a listed domain, another domain's organisation) and every
// organisation-AI confirmation of those accounts; A1, A4 (white space and
// case in the stored address) and I3 stay; nothing else changes; the counts
// are reported.
func TestPAC148_AC7_Migration043(t *testing.T) {
	dsn := pac116DB(t, 42)
	f := pac148Seed(t, dsn, false)
	before := pac148Snapshot(t, dsn)
	usersBefore := pac148Rows(t, dsn, `SELECT (to_jsonb(u) - 'org_id')::text FROM users u`)

	if err := pac148Migrate(t, dsn); err != nil {
		t.Fatalf("043: %v", err)
	}
	if v, dirty := pac116Version(t, dsn); v != 43 || dirty {
		t.Fatalf("after 043: %d dirty=%v", v, dirty)
	}
	after := pac148Snapshot(t, dsn)

	ended := map[uuid.UUID]bool{f.u: true, f.l: true, f.i1: true, f.i2: true, f.x: true, f.a3: true}
	kept := map[uuid.UUID]uuid.UUID{f.a1: f.acme, f.a4: f.acme, f.i3: f.isp}
	pac116Observe(t, dsn, func(conn *sql.Conn) {
		for id := range ended {
			var org uuid.NullUUID
			if err := conn.QueryRowContext(context.Background(), `SELECT org_id FROM users WHERE id = $1`, id).Scan(&org); err != nil {
				t.Fatal(err)
			}
			if org.Valid {
				t.Errorf("%s: org_id = %s, want null", id, org.UUID)
			}
		}
		for id, want := range kept {
			var org uuid.NullUUID
			if err := conn.QueryRowContext(context.Background(), `SELECT org_id FROM users WHERE id = $1`, id).Scan(&org); err != nil {
				t.Fatal(err)
			}
			if !org.Valid || org.UUID != want {
				t.Errorf("%s: org_id = %v, want %s (kept)", id, org, want)
			}
		}
	})
	// Confirmations: only A1's is left, unchanged.
	var a1Conf []string
	for _, c := range before["org_ai_confirmations"] {
		if strings.Contains(c, f.a1.String()) {
			a1Conf = append(a1Conf, c)
		}
	}
	if got := after["org_ai_confirmations"]; strings.Join(got, "\n") != strings.Join(a1Conf, "\n") || len(got) != 1 {
		t.Errorf("confirmations after 043 = %v, want A1's alone %v", got, a1Conf)
	}
	// users: no column but org_id changes.
	if got := pac148Rows(t, dsn, `SELECT (to_jsonb(u) - 'org_id')::text FROM users u`); strings.Join(got, "\n") != strings.Join(usersBefore, "\n") {
		t.Errorf("a users column other than org_id changed")
	}
	// Every other table, row for row.
	for tb, rows := range before {
		if tb == "users" || tb == "org_ai_confirmations" {
			continue
		}
		if strings.Join(after[tb], "\n") != strings.Join(rows, "\n") {
			t.Errorf("table %s changed:\nbefore %v\nafter  %v", tb, rows, after[tb])
		}
	}
	if len(after) != len(before) {
		t.Errorf("tables: %d before, %d after", len(before), len(after))
	}

	// The count, observed through a NOTICE handler on a copy of the fixture.
	cp := pac116DB(t, 42)
	pac148Seed(t, cp, false)
	notices := pac148RunUpWithNotices(t, cp)
	want := "PAC-148 migration 043: ended 6 organisation memberships and 4 organisation-AI confirmations"
	if len(notices) != 1 || notices[0] != want {
		t.Errorf("notices = %q, want [%q]", notices, want)
	}
	// Idempotent: a second run on the result ends nothing.
	if again := pac148RunUpWithNotices(t, cp); len(again) != 1 || again[0] != "PAC-148 migration 043: ended 0 organisation memberships and 0 organisation-AI confirmations" {
		t.Errorf("second run notices = %q", again)
	}
}

// pac148MigrationList parses the consumer-list block of 043's up file:
// exactly one quoted, lower-case entry per line between the markers.
func pac148MigrationList(t *testing.T) []string {
	t.Helper()
	body, err := migrationFiles.ReadFile(pac148MigrationFile)
	if err != nil {
		t.Fatal(err)
	}
	entry := regexp.MustCompile(`^\s*'([a-z0-9.-]+)',?$`)
	var list []string
	in, seenEnd := false, false
	sc := bufio.NewScanner(strings.NewReader(string(body)))
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		switch strings.TrimSpace(line) {
		case "-- consumer-list:begin":
			if in || list != nil {
				t.Fatal("the begin marker appears twice")
			}
			in = true
			continue
		case "-- consumer-list:end":
			if !in {
				t.Fatal("the end marker comes before the begin marker")
			}
			in, seenEnd = false, true
			continue
		}
		if !in {
			continue
		}
		m := entry.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("consumer-list line %q is not one quoted lower-case entry", line)
		}
		list = append(list, m[1])
	}
	if !seenEnd || len(list) == 0 {
		t.Fatal("no consumer-list block in 043's up file")
	}
	if !sort.StringsAreSorted(list) {
		t.Errorf("the consumer-list block is not sorted: %v", list)
	}
	return list
}

// AC-16: the migration's consumer set is domain.GenericDomains() of the same
// tree, and applied to one domain-confirmed member per listed domain it
// detaches every one of them while A1 (unlisted) is kept.
func TestPAC148_AC16_MigrationListIsTheGoList(t *testing.T) {
	got := pac148MigrationList(t)
	want := domain.GenericDomains()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("043's consumer list differs from domain.GenericDomains():\n043: %v\nGo:  %v", got, want)
	}

	dsn := pac116DB(t, 42)
	members := map[string]uuid.UUID{}
	var a1 uuid.UUID
	pac148Exec(t, dsn, func(q func(string, ...any) *sql.Row, _ func(string, ...any)) {
		for _, d := range append(want, "acme16.pac148.test") {
			var org, id uuid.UUID
			if err := q(`INSERT INTO organizations (name, domain) VALUES ($1, $1) RETURNING id`, d).Scan(&org); err != nil {
				t.Fatal(err)
			}
			if err := q(`INSERT INTO users (email, name, org_id, address_standing) VALUES ($1, 'M', $2, 'domain_confirmed') RETURNING id`,
				"m@"+d, org).Scan(&id); err != nil {
				t.Fatal(err)
			}
			members[d] = id
		}
		a1 = members["acme16.pac148.test"]
		delete(members, "acme16.pac148.test")
	})
	if err := pac148Migrate(t, dsn); err != nil {
		t.Fatalf("043: %v", err)
	}
	stored := map[string]bool{}
	for _, s := range pac148Rows(t, dsn, `SELECT id::text FROM users WHERE org_id IS NOT NULL`) {
		stored[s] = true
	}
	for d, id := range members {
		if stored[id.String()] {
			t.Errorf("%s: the domain-confirmed member was not detached", d)
		}
	}
	if !stored[a1.String()] || len(stored) != 1 {
		t.Errorf("after 043, members left = %v, want A1 (%s) alone", stored, a1)
	}
}

// AC-17: with an SSO provider for hotmail.fr (any case), and separately an
// organisation AI configuration on organisation hotmail.fr, the real runner
// fails naming the kind and the domain, no data row changes, and the version
// is 43 dirty. The runbook's force to 42 leaves 42 clean, which the start-up
// classification of an image whose highest migration is 42 accepts as
// current. With neither, 043 applies.
func TestPAC148_AC17_GateAbortsAndForceRecovers(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		plant      func(t *testing.T, dsn string, f pac148Fix)
	}{
		{"sso provider", "sso_provider hotmail.fr", func(t *testing.T, dsn string, _ pac148Fix) {
			pac148Exec(t, dsn, func(_ func(string, ...any) *sql.Row, exec func(string, ...any)) {
				exec(`INSERT INTO sso_providers (domain, provider_name, provider_type) VALUES ('Hotmail.FR', 'Hot', 'oidc')`)
			})
		}},
		{"organisation ai configuration", "org_ai_configuration hotmail.fr", func(t *testing.T, dsn string, f pac148Fix) {
			pac148Exec(t, dsn, func(_ func(string, ...any) *sql.Row, exec func(string, ...any)) {
				exec(`INSERT INTO org_ai_configurations (org_id, provider) VALUES ($1, 'openai')`, f.hotmail)
			})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dsn := pac116DB(t, 42)
			f := pac148Seed(t, dsn, false)
			tc.plant(t, dsn, f)
			before := pac148Snapshot(t, dsn)

			err := pac148Migrate(t, dsn)
			if err == nil {
				t.Fatal("043 applied over a configuration on a consumer-list domain")
			}
			if msg := err.Error(); !strings.Contains(msg, "PAC-148 migration 043 refused: configuration on a consumer-list domain: "+tc.want) {
				t.Fatalf("error = %v, want it to name %q", err, tc.want)
			}
			if v, dirty := pac116Version(t, dsn); v != 43 || !dirty {
				t.Fatalf("after the abort: %d dirty=%v, want 43 dirty", v, dirty)
			}
			after := pac148Snapshot(t, dsn)
			for tb, rows := range before {
				if strings.Join(after[tb], "\n") != strings.Join(rows, "\n") {
					t.Errorf("table %s changed although 043 aborted", tb)
				}
			}
			// Neither image starts on the dirty version.
			if err := pac148Migrate(t, dsn); !errors.As(err, new(migrate.ErrDirty)) {
				t.Fatalf("a run on the dirty version = %v, want ErrDirty", err)
			}

			// The runbook's force to P = 42.
			pac116Set(t, dsn, 42, false)
			if v, dirty := pac116Version(t, dsn); v != 42 || dirty {
				t.Fatalf("after the force: %d dirty=%v", v, dirty)
			}
			e, err := embeddedVersions()
			if err != nil {
				t.Fatal(err)
			}
			pac116Observe(t, dsn, func(conn *sql.Conn) {
				st, err := readState(context.Background(), conn, e, e.highestFor(42))
				if err != nil || st.Kind != VersionCurrent || st.Version != 42 {
					t.Fatalf("the PAC-130 image's start-up check on the forced database = %+v, %v, want VersionCurrent at 42", st, err)
				}
			})
		})
	}

	t.Run("both, named together", func(t *testing.T) {
		dsn := pac116DB(t, 42)
		pac148Seed(t, dsn, true)
		err := pac148Migrate(t, dsn)
		if err == nil || !strings.Contains(err.Error(), "org_ai_configuration hotmail.fr, sso_provider hotmail.fr") {
			t.Fatalf("error = %v, want both configurations named", err)
		}
	})

	t.Run("neither", func(t *testing.T) {
		dsn := pac116DB(t, 42)
		pac148Seed(t, dsn, false)
		if err := pac148Migrate(t, dsn); err != nil {
			t.Fatalf("043 without a configuration on a listed domain: %v", err)
		}
		if v, dirty := pac116Version(t, dsn); v != 43 || dirty {
			t.Fatalf("after 043: %d dirty=%v", v, dirty)
		}
	})
}

// The down step changes nothing (contract md §5.2): up, down, up again.
func TestPAC148_Migration043DownIsANoOp(t *testing.T) {
	db, to := migrationTestDB(t)
	to(42)
	var org, id uuid.UUID
	if err := db.QueryRow(`INSERT INTO organizations (name, domain) VALUES ('d', 'd.pac148.test') RETURNING id`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO users (email, name, org_id) VALUES ('u@d.pac148.test', 'U', $1) RETURNING id`, org).Scan(&id); err != nil {
		t.Fatal(err)
	}
	to(43)
	to(42)
	var got uuid.NullUUID
	if err := db.QueryRow(`SELECT org_id FROM users WHERE id = $1`, id).Scan(&got); err != nil || got.Valid {
		t.Fatalf("after 043 and its down step: org_id = %v, %v; want null (not restored)", got, err)
	}
	to(43)
}

// pac148ProdPostgresTag is the postgres image tag docker-compose.prod.yml's
// postgres service runs (contract md §2.2, revision 2 N-4): read from the
// file, never a literal.
func pac148ProdPostgresTag(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile("../../docker-compose.prod.yml")
	if err != nil {
		t.Fatalf("read docker-compose.prod.yml: %v", err)
	}
	m := regexp.MustCompile(`(?m)^\s+image:\s*postgres:([0-9]+(?:\.[0-9]+)*)-alpine\s*$`).FindAllStringSubmatch(string(body), -1)
	if len(m) != 1 {
		t.Fatalf("docker-compose.prod.yml names %d postgres:<tag>-alpine images, want exactly 1", len(m))
	}
	return m[0][1]
}

// AC-14, contract sub-test (contract md §2.2, §7): addrDomainSQL equals
// domain.ExtractDomain on every fixture, on a database that runs the
// production image's version with the ctype it was verified against.
func TestPAC148_AddrDomainSQLEqualsExtractDomain(t *testing.T) {
	db := openTestDB(t)
	var version, ctype string
	if err := db.QueryRow(`SELECT current_setting('server_version'), datctype FROM pg_database WHERE datname = current_database()`).Scan(&version, &ctype); err != nil {
		t.Fatal(err)
	}
	if tag := pac148ProdPostgresTag(t); version != tag {
		t.Fatalf("test database server_version = %s, production (docker-compose.prod.yml) runs postgres:%s-alpine: run the suite through scripts/test-backend.sh", version, tag)
	}
	if ctype != "en_US.utf8" {
		t.Fatalf("test database ctype = %s, want en_US.utf8 (the ctype addrDomainSQL was verified against)", ctype)
	}
	fixtures := []string{
		"a@ACME.COM", "  a@ acme.com ", "a@b@ACME.com", "noat", "x@", "x@ÉCOLE.FR", "x@ΣΧΟΛΗΣ.GR",
		"x@İSTANBUL.TR", "x@KELVIN.COM", "x@ẞ.de", "x@\tacme.com ",
		"x@　acme.com ", "x@acme.com\u0085", "x@​acme.com", "x@ÅNGSTRÖM.SE", "x@ǅ.com", "x@Ǆ.com",
		"x@Ω.com", "x@\u000bacme.com\u000c", "x@      acme.com",
		"x@ÀÉÎÕÜ.FR", "x@ДОМЕН.РФ", "x@İ.tr", "x@I.tr", "x@\U00010400.com",
		// the remaining white space code points, one each
		"x@\u000aacme.com\u000d", "x@   acme.com   ", "x@   acme.com ",
		"x@ acme.com ", "",
	}
	for _, in := range fixtures {
		var got string
		if err := db.QueryRow(`SELECT `+addrDomainSQL("e.v")+` FROM (SELECT $1::text AS v) e`, in).Scan(&got); err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if want := domain.ExtractDomain(in); got != want {
			t.Errorf("addrDomainSQL(%q) = %q, ExtractDomain = %q", in, got, want)
		}
	}
	// The migration and the read rule carry the same text verbatim.
	body, err := migrationFiles.ReadFile(pac148MigrationFile)
	if err != nil {
		t.Fatal(err)
	}
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	if !strings.Contains(norm(string(body)), norm(addrDomainSQL("u.email"))) {
		t.Error("043's up file does not carry addrDomainSQL(u.email) verbatim")
	}
}

// pac148Deadline bounds the lock probe below.
const pac148Deadline = 5 * time.Second
