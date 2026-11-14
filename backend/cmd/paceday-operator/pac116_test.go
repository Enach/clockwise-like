package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/secretbox/secretboxtest"
	"github.com/Enach/paceday/backend/internal/testdb"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// PAC-116 (contracts/features/PAC-116.md sections 3-6): paceday-operator
// applies pending migrations only while the app is not serving.

type p116 struct {
	t      *testing.T
	dsn    string
	name   string
	key    string
	latest uint
	kept   map[int]bool // sessions the test keeps open on purpose
}

// newP116 is a database of its own at version at (0: every migration),
// with no session on it.
func newP116(t *testing.T, at uint) *p116 {
	t.Helper()
	latest, err := storage.LatestMigrationVersion()
	if err != nil {
		t.Fatal(err)
	}
	dsn, cleanup, err := testdb.Create()
	if err != nil {
		t.Fatalf("PostgreSQL unavailable: %v", err)
	}
	t.Cleanup(func() { _ = cleanup() })
	if at > 0 {
		restore := storage.SetMigrationCeilingForTest(at)
		defer restore()
	}
	db, err := storage.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	u, _ := url.Parse(dsn)
	p := &p116{t: t, dsn: dsn, name: strings.TrimPrefix(u.Path, "/"), key: secretboxtest.KeyValue("p116-key"), latest: latest, kept: map[int]bool{}}
	p.settle()
	return p
}

// settle waits until every client session on the database is one the test
// keeps open on purpose or a migrator's: a pool an operator run closed may linger
// server-side for a moment, and the next run would count it.
func (p *p116) settle() {
	p.t.Helper()
	admin, err := sql.Open("pgx", testdb.AdminDSN())
	if err != nil {
		p.t.Fatal(err)
	}
	defer admin.Close()
	deadline := time.Now().Add(10 * time.Second)
	for {
		// A migrator's own session (paceday-migrate:) is never another
		// session for the procedure; a test may hold one on purpose.
		rows, err := admin.Query(`SELECT pid FROM pg_stat_activity WHERE datname = $1 AND backend_type = 'client backend'
			AND application_name NOT LIKE 'paceday-migrate:%'`, p.name)
		if err != nil {
			p.t.Fatal(err)
		}
		extra := 0
		for rows.Next() {
			var pid int
			_ = rows.Scan(&pid)
			if !p.kept[pid] {
				extra++
			}
		}
		rows.Close()
		if extra == 0 {
			return
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("%d session(s) still on %s", extra, p.name)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// observe runs fn on a short-lived connection, then settles.
func (p *p116) observe(fn func(conn *sql.Conn)) {
	p.t.Helper()
	db, err := storage.OpenNoMigrate(p.dsn, "pac116-test-observer")
	if err != nil {
		p.t.Fatal(err)
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		p.t.Fatal(err)
	}
	fn(conn)
	conn.Close()
	db.Close()
	p.settle()
}

func (p *p116) exec(q string, args ...any) {
	p.t.Helper()
	p.observe(func(conn *sql.Conn) {
		if _, err := conn.ExecContext(context.Background(), q, args...); err != nil {
			p.t.Fatal(err)
		}
	})
}

func (p *p116) count(q string, args ...any) (n int) {
	p.t.Helper()
	p.observe(func(conn *sql.Conn) {
		if err := conn.QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
			p.t.Fatal(err)
		}
	})
	return n
}

func (p *p116) version() (v uint, dirty bool) {
	p.t.Helper()
	p.observe(func(conn *sql.Conn) {
		var err error
		if v, dirty, _, err = storage.SchemaVersion(context.Background(), conn); err != nil {
			p.t.Fatal(err)
		}
	})
	return v, dirty
}

func (p *p116) wantVersion(v uint, dirty bool) {
	p.t.Helper()
	if got, d := p.version(); got != v || d != dirty {
		p.t.Fatalf("schema version %d dirty=%v, want %d dirty=%v", got, d, v, dirty)
	}
}

// heartbeat writes a backend heartbeat seen age ago on host web-1.
func (p *p116) heartbeat(age time.Duration) uuid.UUID {
	p.t.Helper()
	id := uuid.New()
	p.exec(`INSERT INTO encryption_backend_heartbeats (instance_id, hostname, seen_at) VALUES ($1, 'web-1', now() - make_interval(secs => $2))`, id, age.Seconds())
	return id
}

// session opens one idle session named app, kept until the test ends.
func (p *p116) session(app string) int {
	p.t.Helper()
	cfg, err := pgx.ParseConfig(p.dsn)
	if err != nil {
		p.t.Fatal(err)
	}
	cfg.RuntimeParams["application_name"] = app
	db := stdlib.OpenDB(*cfg)
	conn, err := db.Conn(context.Background())
	if err != nil {
		p.t.Fatal(err)
	}
	var pid int
	if err := conn.QueryRowContext(context.Background(), `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		p.t.Fatal(err)
	}
	p.kept[pid] = true
	p.t.Cleanup(func() { conn.Close(); db.Close() })
	return pid
}

func (p *p116) user() uuid.UUID {
	p.t.Helper()
	var id uuid.UUID
	p.observe(func(conn *sql.Conn) {
		if err := conn.QueryRowContext(context.Background(), `INSERT INTO users (email, name) VALUES ($1, 'P116') RETURNING id`, uuid.NewString()+"@p116.example").Scan(&id); err != nil {
			p.t.Fatal(err)
		}
	})
	return id
}

func (p *p116) generation(id uuid.UUID) int {
	return p.count(`SELECT session_generation FROM users WHERE id = $1`, id)
}

func (p *p116) audits() int {
	p.t.Helper()
	return p.count(`SELECT CASE WHEN to_regclass('public.operator_audit') IS NULL THEN 0 ELSE (SELECT count(*) FROM operator_audit)::int END`)
}

func (p *p116) envWith(extra map[string]string) func(string) string {
	all := map[string]string{"DATABASE_URL": p.dsn, "PACEDAY_ENCRYPTION_KEY": p.key}
	for k, v := range extra {
		all[k] = v
	}
	return func(k string) string { return all[k] }
}

// enc runs paceday-operator encryption with args, then settles.
func (p *p116) enc(extra map[string]string, args ...string) (int, string, string) {
	p.t.Helper()
	var out, errw bytes.Buffer
	code := runEncryption(args, p.envWith(extra), &out, &errw)
	p.settle()
	return code, out.String(), errw.String()
}

// op runs one of PAC-71's subcommands, then settles.
func (p *p116) op(extra map[string]string, args ...string) (int, string, string) {
	p.t.Helper()
	var out, errw bytes.Buffer
	code := runPAC71(args, p.envWith(extra), &out, &errw)
	p.settle()
	return code, out.String(), errw.String()
}

var (
	p116Convert = []string{"convert", "--operator", "o", "--reason", "r"}
	p116Rotate  = []string{"rotate-data-key", "--operator", "o", "--reason", "r", "--retire", "1", "--wait", "1ms"}
	p116Advance = []string{"advance-all", "-operator", "o", "-reason", "r"}
)

func (p *p116) servingText(stderr string) bool {
	return strings.Contains(stderr, fmt.Sprintf("pending migrations from version %d to %d were not applied: the app is serving.", p.latest-1, p.latest)) &&
		strings.Contains(stderr, "Stop every backend first") && strings.Contains(stderr, "Nothing was changed.")
}

// AC-1: a heartbeat seen 5 s ago refuses convert and rotate-data-key (exit
// 6) and advance-all (exit 1), naming instance, host and age; nothing
// applied, not dirty, no audit row, no account touched.
func TestPAC116_AC1_LiveHeartbeatRefuses(t *testing.T) {
	latest, _ := storage.LatestMigrationVersion()
	p := newP116(t, latest-1)
	u := p.user()
	inst := p.heartbeat(5 * time.Second)
	age := regexp.MustCompile(`on "web-1", seen [5-9]s ago`)
	for _, args := range [][]string{p116Convert, p116Rotate} {
		code, _, stderr := p.enc(nil, args...)
		if code != exitBackends || !p.servingText(stderr) || !strings.Contains(stderr, "live backend heartbeat: instance "+inst.String()) ||
			!age.MatchString(stderr) || !strings.HasPrefix(stderr, "encryption "+args[0]+": ") {
			t.Fatalf("%s: exit %d\n%s", args[0], code, stderr)
		}
	}
	code, _, stderr := p.op(nil, p116Advance...)
	if code != 1 || !p.servingText(stderr) || !strings.Contains(stderr, inst.String()) || !strings.HasPrefix(stderr, "paceday-operator advance-all: ") {
		t.Fatalf("advance-all: exit %d\n%s", code, stderr)
	}
	p.wantVersion(p.latest-1, false)
	if p.audits() != 0 || p.generation(u) != 0 {
		t.Fatal("a refusal wrote an audit row or advanced a generation")
	}
}

// AC-2: no heartbeat, one other session: a pre-PAC-116 backend, an unnamed
// one (pre-PAC-84) and psql all refuse, the session listed by pid and name.
func TestPAC116_AC2_OtherSessionRefuses(t *testing.T) {
	latest, _ := storage.LatestMigrationVersion()
	for _, app := range []string{storage.AppBackend, "", "psql"} {
		p := newP116(t, latest-1)
		pid := p.session(app)
		listed := fmt.Sprintf("other database session: pid %d, application_name %q", pid, app)
		code, _, stderr := p.enc(nil, p116Convert...)
		if code != exitBackends || !p.servingText(stderr) || !strings.Contains(stderr, listed) {
			t.Fatalf("convert with session %q: exit %d\n%s", app, code, stderr)
		}
		code, _, stderr = p.op(nil, p116Advance...)
		if code != 1 || !p.servingText(stderr) || !strings.Contains(stderr, listed) {
			t.Fatalf("advance-all with session %q: exit %d\n%s", app, code, stderr)
		}
		p.wantVersion(latest-1, false)
	}
}

// AC-3: a heartbeat last seen 91 s ago is not live: convert applies (highest,
// not dirty) and then converts as today, with its audit row.
func TestPAC116_AC3_StaleHeartbeatApplies(t *testing.T) {
	latest, _ := storage.LatestMigrationVersion()
	p := newP116(t, latest-1)
	p.heartbeat(91 * time.Second)
	code, out, stderr := p.enc(nil, p116Convert...)
	if code != exitOK || !strings.Contains(stderr, fmt.Sprintf("encryption convert: migrations applied, schema version %d -> %d", latest-1, latest)) || !strings.Contains(out, "values encrypted") {
		t.Fatalf("convert: exit %d\n%s\n%s", code, out, stderr)
	}
	p.wantVersion(latest, false)
	if n := p.count(`SELECT count(*) FROM operator_audit WHERE action = $1`, storage.AuditEncryptionConvert); n != 1 {
		t.Fatalf("audit rows %d", n)
	}
}

// AC-4: pending and nothing else: advance-all applies, then runs. Nothing
// pending, a live heartbeat and an app session: convert, rotate-data-key,
// status and advance-all run as today (no check, no refusal).
func TestPAC116_AC4_NothingPendingRunsAsToday(t *testing.T) {
	latest, _ := storage.LatestMigrationVersion()
	p := newP116(t, latest-1)
	u := p.user()
	code, out, stderr := p.op(nil, p116Advance...)
	if code != 0 || !strings.Contains(out, "advanced the session generation of 1 accounts") || !strings.Contains(stderr, "migrations applied") {
		t.Fatalf("advance-all: %d %s %s", code, out, stderr)
	}
	p.wantVersion(latest, false)
	if p.generation(u) != 1 {
		t.Fatal("generation not advanced")
	}

	p.heartbeat(0)
	p.session(storage.AppBackend)
	for _, run := range []func() (int, string, string){
		func() (int, string, string) { return p.enc(nil, p116Convert...) },
		func() (int, string, string) { return p.enc(nil, "status") },
		func() (int, string, string) { return p.op(nil, p116Advance...) },
	} {
		if code, out, stderr := run(); code != exitOK || strings.Contains(out+stderr, "were not applied") || strings.Contains(out, "pending") {
			t.Fatalf("nothing pending: exit %d\n%s\n%s", code, out, stderr)
		}
	}
	// rotate-data-key reaches its own logic (here: the live backend's
	// heartbeat has not switched within --wait), not a PAC-116 refusal.
	if code, _, stderr := p.enc(nil, p116Rotate...); code == exitBackends || code == exitLock || strings.Contains(stderr, "were not applied") {
		t.Fatalf("rotate-data-key: exit %d %s", code, stderr)
	}
	p.wantVersion(latest, false)
}

// AC-5: below 034 (no heartbeat table) advance-all applies with nothing
// else connected and refuses with one session; encryption subcommands keep
// the below-034 refusal and apply nothing.
func TestPAC116_AC5_BelowHeartbeatTable(t *testing.T) {
	want, err := storage.EncryptionMigrationVersion()
	if err != nil {
		t.Fatal(err)
	}
	below := want - 2 // 032: 033 was never used
	p := newP116(t, below)
	if n := p.count(`SELECT count(*) FROM pg_tables WHERE tablename = 'encryption_backend_heartbeats'`); n != 0 {
		t.Fatal("the heartbeat table exists below 034")
	}
	if code, _, stderr := p.op(nil, p116Advance...); code != 0 {
		t.Fatalf("advance-all below 034: %d %s", code, stderr)
	}
	p.wantVersion(p.latest, false)

	p = newP116(t, below)
	pid := p.session(storage.AppBackend)
	code, _, stderr := p.op(nil, p116Advance...)
	if code != 1 || !strings.Contains(stderr, fmt.Sprintf("pending migrations from version %d to %d were not applied: the app is serving.", below, p.latest)) || !strings.Contains(stderr, fmt.Sprintf("pid %d", pid)) {
		t.Fatalf("advance-all below 034 with a session: %d %s", code, stderr)
	}
	for _, args := range [][]string{p116Convert, {"status"}} {
		code, out, stderr := p.enc(nil, args...)
		if !strings.Contains(out+stderr, fmt.Sprintf("below this release's encryption migration %d", want)) || (args[0] == "convert" && code != exitFailed) {
			t.Fatalf("%s below 034: %d %s %s", args[0], code, out, stderr)
		}
	}
	p.wantVersion(below, false)
}

// AC-12: a second operator's working connection counts; a session named
// paceday-migrate: does not; the operator's own pool is opened only after
// the procedure, so it never refuses itself.
func TestPAC116_AC12_WhoCounts(t *testing.T) {
	latest, _ := storage.LatestMigrationVersion()
	p := newP116(t, latest-1)
	pid := p.session("paceday-operator advance-all")
	code, _, stderr := p.enc(nil, p116Convert...)
	if code != exitBackends || !strings.Contains(stderr, fmt.Sprintf("pid %d, application_name \"paceday-operator advance-all\"", pid)) {
		t.Fatalf("another operator's connection: %d %s", code, stderr)
	}
	p = newP116(t, latest-1)
	p.session("paceday-migrate: test")
	if code, _, stderr := p.enc(nil, p116Convert...); code != exitOK {
		t.Fatalf("a paceday-migrate: session made convert refuse: %d %s", code, stderr)
	}
	p.wantVersion(latest, false)
}

// AC-13: no override. --force/-force/--yes/--override are usage errors;
// every flag each subcommand registers (read from its own -h listing) set to
// a valid value still refuses under AC-1; every environment variable the run
// reads, set to "true" or "1", still refuses.
func TestPAC116_AC13_NoOverride(t *testing.T) {
	latest, _ := storage.LatestMigrationVersion()
	p := newP116(t, latest-1)
	p.heartbeat(0)
	base := map[string][]string{
		"convert":         {"--operator", "o", "--reason", "r"},
		"rotate-data-key": {"--operator", "o", "--reason", "r", "--retire", "1"},
		"decrypt-all":     {"--operator", "o", "--reason", "r"},
		"reset-lost-key":  {"--operator", "o", "--reason", "r", "--confirm-lost-key"},
		"advance-all":     {"-operator", "o", "-reason", "r"},
		"free-address":    {"-operator", "o", "-reason", "r", "-account", uuid.NewString()},
		"reassign":        {"-operator", "o", "-reason", "r", "-account", uuid.NewString(), "-identity", "google:1", "-evidence", "mailbox"},
	}
	encryptionSub := map[string]bool{"convert": true, "rotate-data-key": true, "decrypt-all": true, "reset-lost-key": true}
	runSub := func(sub string, env map[string]string, args ...string) (int, string) {
		if encryptionSub[sub] {
			code, _, stderr := p.enc(env, append([]string{sub}, args...)...)
			return code, stderr
		}
		code, _, stderr := p.op(env, append([]string{sub}, args...)...)
		return code, stderr
	}
	refused := func(sub string, code int, stderr string) bool {
		want := 1
		if encryptionSub[sub] {
			want = exitBackends
		}
		return code == want && p.servingText(stderr)
	}
	flagLine := regexp.MustCompile(`(?m)^\s+-([a-z-]+)(?: (\w+))?`)
	for sub, args := range base {
		// Keep the heartbeat live (90 s) however long the loop runs.
		p.exec(`UPDATE encryption_backend_heartbeats SET seen_at = now()`)
		for _, bad := range []string{"--force", "-force", "--yes", "--override"} {
			if code, stderr := runSub(sub, nil, append(append([]string{}, args...), bad)...); code != exitUsage {
				t.Fatalf("%s %s: exit %d %s", sub, bad, code, stderr)
			}
		}
		// The flag set's own listing.
		var help string
		if encryptionSub[sub] {
			_, _, help = p.enc(nil, sub, "-h")
		} else {
			_, help, _ = p.op(nil, sub, "-h")
		}
		flags := flagLine.FindAllStringSubmatch(help, -1)
		if len(flags) < 2 {
			t.Fatalf("%s: no flags listed in %q", sub, help)
		}
		for _, f := range flags {
			name, typ := f[1], f[2]
			var val string
			switch {
			case typ == "":
				val = "true"
			case typ == "int":
				val = "1"
			case typ == "duration":
				val = "1ms"
			case name == "account":
				val = uuid.NewString()
			case name == "identity":
				val = "google:2"
			case name == "evidence":
				val = "authoritative"
			case name == "remove":
				val = "slack"
			default:
				val = "x"
			}
			if code, stderr := runSub(sub, nil, append(append([]string{}, args...), "-"+name+"="+val)...); !refused(sub, code, stderr) {
				t.Fatalf("%s -%s=%s did not refuse: exit %d %s", sub, name, val, code, stderr)
			}
		}
		// Every environment name the run reads.
		var mu sync.Mutex
		read := map[string]bool{}
		record := func(k string) string {
			mu.Lock()
			read[k] = true
			mu.Unlock()
			return p.envWith(nil)(k)
		}
		var out, errw bytes.Buffer
		if encryptionSub[sub] {
			runEncryption(append([]string{sub}, args...), record, &out, &errw)
		} else {
			runPAC71(append([]string{sub}, args...), record, &out, &errw)
		}
		p.settle()
		if !read["DATABASE_URL"] {
			t.Fatalf("%s: getenv not recorded", sub)
		}
		for _, v := range []string{"true", "1"} {
			env := map[string]string{"PACEDAY_MIGRATE_WHILE_SERVING": v, "PACEDAY_FORCE": v, "MIGRATE_FORCE": v}
			for k := range read {
				// Key material is parsed: "true" is no key (exit 4).
				if k != "DATABASE_URL" && k != "PACEDAY_ENCRYPTION_KEY" && k != "PACEDAY_ENCRYPTION_KEY_PREVIOUS" {
					env[k] = v
				}
			}
			if code, stderr := runSub(sub, env, args...); !refused(sub, code, stderr) {
				t.Fatalf("%s with %v: exit %d %s", sub, env, code, stderr)
			}
		}
	}
	p.wantVersion(latest-1, false)
	if p.audits() != 0 {
		t.Fatal("audit rows written")
	}
}

// AC-14: dirty with no migrating holder: today's dirty error on every path,
// the row untouched; downgrade-schema is not routed through the procedure
// (its own heartbeat check, nothing applied); booker-erasure applies nothing.
func TestPAC116_AC14_DirtyAndUnroutedCommands(t *testing.T) {
	latest, _ := storage.LatestMigrationVersion()
	p := newP116(t, latest-1)
	p.exec(`UPDATE schema_migrations SET dirty = true`)
	dirtyText := fmt.Sprintf("Dirty database version %d. Fix and force version.", latest-1)
	for _, c := range []struct {
		name string
		run  func() (int, string, string)
	}{
		{"convert", func() (int, string, string) { return p.enc(nil, p116Convert...) }},
		{"status", func() (int, string, string) { return p.enc(nil, "status") }},
		{"advance-all", func() (int, string, string) { return p.op(nil, p116Advance...) }},
	} {
		if code, out, stderr := c.run(); code != exitFailed || !strings.Contains(stderr, dirtyText) || strings.Contains(out+stderr, "being applied") {
			t.Fatalf("%s on a dirty database: exit %d %s %s", c.name, code, out, stderr)
		}
	}
	p.wantVersion(latest-1, true)

	p = newP116(t, latest-1)
	p.heartbeat(0)
	code, _, stderr := p.enc(nil, "downgrade-schema", "--operator", "o", "--reason", "r")
	if code != exitBackends || !strings.Contains(stderr, `"Encryption key - stop the backend first"`) || strings.Contains(stderr, "were not applied") {
		t.Fatalf("downgrade-schema: %d %s", code, stderr)
	}
	if code, out, stderr := p.erasureRun("status"); code != exitOK || !strings.Contains(out, "booker-erasure status at=") {
		t.Fatalf("booker-erasure: %d %s %s", code, out, stderr)
	}
	p.wantVersion(latest-1, false)
}

func (p *p116) erasureRun(args ...string) (int, string, string) {
	p.t.Helper()
	var out, errw bytes.Buffer
	code := runBookerErasure(args, p.envWith(nil), &out, &errw, time.Now)
	p.settle()
	return code, out.String(), errw.String()
}

// holdMigrator runs the procedure for caller in the background and returns
// once it is held by the seam that install installs; release lets it go and
// returns its result.
func holdMigrator(t *testing.T, dsn, caller string, install func(release <-chan struct{}, held chan<- struct{})) (release func() (storage.MigrateResult, error)) {
	t.Helper()
	held := make(chan struct{})
	rel := make(chan struct{})
	install(rel, held)
	type result struct {
		res storage.MigrateResult
		err error
	}
	done := make(chan result, 1)
	go func() {
		res, err := storage.MigrateWhenStopped(context.Background(), dsn, storage.MigrateOptions{Caller: caller})
		done <- result{res, err}
	}()
	select {
	case <-held:
	case r := <-done:
		t.Fatalf("the migrator was not held: %+v %v", r.res, r.err)
	case <-time.After(30 * time.Second):
		t.Fatal("the migrator was not held within 30 s")
	}
	var once sync.Once
	release = func() (storage.MigrateResult, error) {
		once.Do(func() { close(rel) })
		r := <-done
		return r.res, r.err
	}
	t.Cleanup(func() { once.Do(func() { close(rel) }) })
	return release
}

// AC-19: a migrator inside a migration file (version dirty, lock held):
// convert exits 5, advance-all 1, status 5, all naming the holder, none
// saying "Dirty database version"; once it ends each runs as today.
func TestPAC116_AC19_DirtyDuringMigration(t *testing.T) {
	latest, _ := storage.LatestMigrationVersion()
	p := newP116(t, latest-1)
	release := holdMigrator(t, p.dsn, storage.AppBackend, func(rel <-chan struct{}, held chan<- struct{}) {
		var once sync.Once
		storage.InstallInsideMigrationFile(t, func(uint) {
			once.Do(func() { close(held) })
			<-rel
		})
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, dirty := p.version(); dirty {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the version was never marked dirty")
		}
		time.Sleep(10 * time.Millisecond)
	}
	holder := "being applied by paceday-migrate: paceday-backend (pid "
	for _, c := range []struct {
		name string
		code int
		run  func() (int, string, string)
		text string
	}{
		{"convert", exitLock, func() (int, string, string) { return p.enc(nil, p116Convert...) }, holder},
		{"advance-all", 1, func() (int, string, string) { return p.op(nil, p116Advance...) }, holder},
		{"status", exitLock, func() (int, string, string) { return p.enc(nil, "status") }, "encryption status: migrations are being applied by paceday-migrate: paceday-backend (pid "},
	} {
		code, out, stderr := c.run()
		if code != c.code || !strings.Contains(stderr, c.text) || strings.Contains(out+stderr, "Dirty database version") {
			t.Fatalf("%s during a migration: exit %d\n%s\n%s", c.name, code, out, stderr)
		}
	}
	if res, err := release(); err != nil || !res.Applied {
		t.Fatalf("the held migrator: %+v %v", res, err)
	}
	p.settle()
	p.wantVersion(latest, false)
	for _, run := range []func() (int, string, string){
		func() (int, string, string) { return p.enc(nil, p116Convert...) },
		func() (int, string, string) { return p.op(nil, p116Advance...) },
		func() (int, string, string) { return p.enc(nil, "status") },
	} {
		if code, out, stderr := run(); code != exitOK {
			t.Fatalf("after the migration: exit %d %s %s", code, out, stderr)
		}
	}
}

// AC-20: a version the binary has no file for (above the highest, and the
// 033 gap) stops convert, advance-all and status with golang-migrate's
// "no migration found for version N"; exit 1; nothing written.
func TestPAC116_AC20_UnknownVersion(t *testing.T) {
	latest, _ := storage.LatestMigrationVersion()
	for _, v := range []uint{latest + 1, 33} {
		p := newP116(t, 0)
		u := p.user()
		p.exec(`UPDATE schema_migrations SET version = $1`, v)
		text := fmt.Sprintf("no migration found for version %d", v)
		for _, c := range []struct {
			name string
			run  func() (int, string, string)
		}{
			{"convert", func() (int, string, string) { return p.enc(nil, p116Convert...) }},
			{"advance-all", func() (int, string, string) { return p.op(nil, p116Advance...) }},
			{"status", func() (int, string, string) { return p.enc(nil, "status") }},
		} {
			if code, out, stderr := c.run(); code != exitFailed || !strings.Contains(stderr, text) {
				t.Fatalf("%s at version %d: exit %d %s %s", c.name, v, code, out, stderr)
			}
		}
		p.wantVersion(v, false)
		if p.audits() != 0 || p.generation(u) != 0 {
			t.Fatalf("version %d: something was written", v)
		}
	}
}

// AC-21 (operator half): two advance-all processes with the same pending
// migrations, released together at a barrier after reading the version, 20
// times on fresh databases: exactly one applies, the other gets "lock held"
// or nothing to do, none refuses as serving, never dirty. Run with -race.
func TestPAC116_AC21_TwoAdvanceAllExactlyOneApplies(t *testing.T) {
	latest, _ := storage.LatestMigrationVersion()
	for run := 0; run < 20; run++ {
		p := newP116(t, latest-1)
		arrived := make(chan struct{}, 2)
		gate := make(chan struct{})
		var opened sync.Once
		uninstall := storage.InstallAfterMigrateStateRead(t, func(string) {
			arrived <- struct{}{}
			if len(arrived) == 2 {
				opened.Do(func() { close(gate) })
			}
			select {
			case <-gate:
			case <-time.After(30 * time.Second):
			}
		})
		type result struct {
			code      int
			out, errw string
		}
		results := make([]result, 2)
		var wg sync.WaitGroup
		for i := range results {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				var out, errw bytes.Buffer
				code := runPAC71(p116Advance, p.envWith(nil), &out, &errw)
				results[i] = result{code, out.String(), errw.String()}
			}(i)
		}
		wg.Wait()
		uninstall()
		p.settle()
		applied := 0
		for _, r := range results {
			if strings.Contains(r.errw, "the app is serving") {
				t.Fatalf("run %d: a migrator refused as serving:\n%s", run, r.errw)
			}
			switch {
			case strings.Contains(r.errw, "migrations applied"):
				applied++
				if r.code != 0 {
					t.Fatalf("run %d: the applier failed: %+v", run, r)
				}
			case r.code == 1:
				// The holder is named unless it finished between the
				// loser's lock attempt and its holder read.
				if !strings.Contains(r.errw, "were not applied: the maintenance lock is held by ") {
					t.Fatalf("run %d: loser: %+v", run, r)
				}
			case r.code != 0:
				t.Fatalf("run %d: %+v", run, r)
			}
		}
		if applied != 1 {
			t.Fatalf("run %d: %d processes applied the migrations: %+v", run, applied, results)
		}
		p.wantVersion(latest, false)
	}
}

// AC-22: status is a probe: pending plus a live heartbeat and an app session
// applies nothing, prints the pending range and the serving state, then its
// usual report; exit 0, 3 under --check with plain values; nothing pending:
// no pending line.
func TestPAC116_AC22_StatusIsAProbe(t *testing.T) {
	latest, _ := storage.LatestMigrationVersion()
	p := newP116(t, latest-1)
	u := p.user()
	p.exec(`INSERT INTO settings (user_id, webcal_url) VALUES ($1, 'https://p116.example/x.ics')`, u)
	inst := p.heartbeat(0)
	pid := p.session(storage.AppBackend)
	code, out, stderr := p.enc(nil, "status")
	for _, want := range []string{
		fmt.Sprintf("encryption status: schema at version %d; this release's migrations up to %d are pending, not applied", latest-1, latest),
		"serving: 1 live backend heartbeat(s), 1 other database session(s)",
		"live backend heartbeat: instance " + inst.String(),
		fmt.Sprintf("other database session: pid %d, application_name %q", pid, storage.AppBackend),
		"plain values: 1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("status lacks %q: exit %d\n%s\n%s", want, code, out, stderr)
		}
	}
	if code != exitOK {
		t.Fatalf("status: exit %d %s", code, stderr)
	}
	if code, _, _ := p.enc(nil, "status", "--check"); code != exitPlain {
		t.Fatalf("status --check: %d", code)
	}
	p.wantVersion(latest-1, false)

	p = newP116(t, 0)
	if code, out, _ := p.enc(nil, "status"); code != exitOK || strings.Contains(out, "pending") || strings.Contains(out, "serving:") {
		t.Fatalf("status with nothing pending: %d %s", code, out)
	}
}

// AC-23: a migrator holds the lock (after its check); advance-all with
// nothing pending for its own version opens its working connection, sees the
// migrating holder and exits 1, locking no account. booker-erasure status
// (challenge note of revision 3) refuses the same way.
func TestPAC116_AC23_OperatorAfterNothingToDo(t *testing.T) {
	latest, _ := storage.LatestMigrationVersion()
	p := newP116(t, latest-1)
	u := p.user()
	release := holdMigrator(t, p.dsn, storage.AppBackend, func(rel <-chan struct{}, held chan<- struct{}) {
		storage.InstallAfterMigrateCheck(t, func(string) {
			close(held)
			<-rel
		})
	})
	// This binary's version is the database's: nothing pending for it.
	restore := storage.SetMigrationCeilingForTest(latest - 1)
	code, out, stderr := p.op(nil, p116Advance...)
	if code != 1 || !strings.Contains(stderr, "paceday-operator advance-all: migrations are being applied by paceday-migrate: paceday-backend (pid ") || out != "" {
		t.Fatalf("advance-all under a migrator: %d %s %s", code, out, stderr)
	}
	code, out, stderr = p.erasureRun("status")
	if code != exitFailed || !strings.Contains(stderr, "booker-erasure status: migrations are being applied by paceday-migrate: paceday-backend (pid ") || out != "" {
		t.Fatalf("booker-erasure status under a migrator: %d %s %s", code, out, stderr)
	}
	restore()
	if res, err := release(); err != nil || !res.Applied {
		t.Fatalf("the held migrator: %+v %v", res, err)
	}
	p.settle()
	if p.generation(u) != 0 || p.audits() != 0 {
		t.Fatal("the refused advance-all locked or changed an account")
	}
	p.wantVersion(latest, false)
}
