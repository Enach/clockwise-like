package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/secretbox"
	"github.com/Enach/paceday/backend/internal/secretbox/secretboxtest"
	"github.com/Enach/paceday/backend/internal/testdb"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// PAC-116 (contracts/features/PAC-116.md section 4): the backend at start
// applies pending migrations only while the app is not serving, refuses to
// start while a migrator holds the maintenance lock, and a running backend
// shuts down when it sees one.

type b116 struct {
	t      *testing.T
	dsn    string
	name   string
	latest uint
	kept   map[int]bool
	// running: application names of backends the test runs on purpose,
	// whose pools settle does not wait for.
	running map[string]bool
	key     string // one PACEDAY_ENCRYPTION_KEY for every backend on this database
}

func newB116(t *testing.T, at uint) *b116 {
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
	b := &b116{t: t, dsn: dsn, name: strings.TrimPrefix(u.Path, "/"), latest: latest, kept: map[int]bool{}, running: map[string]bool{},
		key: "pac116:" + base64.StdEncoding.EncodeToString(secretboxtest.RandomKey())}
	b.settle()
	return b
}

// settle waits until the only client sessions left on the database are the
// ones the test keeps on purpose, or a migrator's.
func (b *b116) settle() {
	b.t.Helper()
	admin, err := sql.Open("pgx", testdb.AdminDSN())
	if err != nil {
		b.t.Fatal(err)
	}
	defer admin.Close()
	deadline := time.Now().Add(10 * time.Second)
	for {
		rows, err := admin.Query(`SELECT pid, application_name FROM pg_stat_activity WHERE datname = $1 AND backend_type = 'client backend'
			AND application_name NOT LIKE 'paceday-migrate:%'`, b.name)
		if err != nil {
			b.t.Fatal(err)
		}
		extra := 0
		for rows.Next() {
			var pid int
			var app string
			_ = rows.Scan(&pid, &app)
			if !b.kept[pid] && !b.running[app] {
				extra++
			}
		}
		rows.Close()
		if extra == 0 {
			return
		}
		if time.Now().After(deadline) {
			b.t.Fatalf("%d session(s) still on %s", extra, b.name)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// migratorSessions counts the sessions named paceday-migrate: on the
// database, read from the admin database.
func (b *b116) migratorSessions() int {
	b.t.Helper()
	admin, err := sql.Open("pgx", testdb.AdminDSN())
	if err != nil {
		b.t.Fatal(err)
	}
	defer admin.Close()
	var n int
	if err := admin.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE datname = $1 AND application_name LIKE 'paceday-migrate:%'`, b.name).Scan(&n); err != nil {
		b.t.Fatal(err)
	}
	return n
}

func (b *b116) observe(fn func(conn *sql.Conn)) {
	b.t.Helper()
	db, err := storage.OpenNoMigrate(b.dsn, "pac116-test-observer")
	if err != nil {
		b.t.Fatal(err)
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		b.t.Fatal(err)
	}
	fn(conn)
	conn.Close()
	db.Close()
	b.settle()
}

func (b *b116) exec(q string, args ...any) {
	b.t.Helper()
	b.observe(func(conn *sql.Conn) {
		if _, err := conn.ExecContext(context.Background(), q, args...); err != nil {
			b.t.Fatal(err)
		}
	})
}

func (b *b116) count(q string, args ...any) (n int) {
	b.t.Helper()
	b.observe(func(conn *sql.Conn) {
		if err := conn.QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
			b.t.Fatal(err)
		}
	})
	return n
}

func (b *b116) wantVersion(v uint, dirty bool) {
	b.t.Helper()
	var got uint
	var d bool
	b.observe(func(conn *sql.Conn) {
		var err error
		if got, d, _, err = storage.SchemaVersion(context.Background(), conn); err != nil {
			b.t.Fatal(err)
		}
	})
	if got != v || d != dirty {
		b.t.Fatalf("schema version %d dirty=%v, want %d dirty=%v", got, d, v, dirty)
	}
}

func (b *b116) heartbeat(age time.Duration) uuid.UUID {
	b.t.Helper()
	id := uuid.New()
	b.exec(`INSERT INTO encryption_backend_heartbeats (instance_id, hostname, seen_at) VALUES ($1, 'web-1', now() - make_interval(secs => $2))`, id, age.Seconds())
	return id
}

func (b *b116) session(app string) int {
	b.t.Helper()
	cfg, err := pgx.ParseConfig(b.dsn)
	if err != nil {
		b.t.Fatal(err)
	}
	cfg.RuntimeParams["application_name"] = app
	db := stdlib.OpenDB(*cfg)
	conn, err := db.Conn(context.Background())
	if err != nil {
		b.t.Fatal(err)
	}
	var pid int
	if err := conn.QueryRowContext(context.Background(), `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		b.t.Fatal(err)
	}
	b.kept[pid] = true
	b.t.Cleanup(func() { conn.Close(); db.Close() })
	return pid
}

func (b *b116) servingText(s string) bool {
	return strings.Contains(s, fmt.Sprintf("refusing to start: pending migrations from version %d to %d were not applied: the app is serving.", b.latest-1, b.latest)) &&
		strings.Contains(s, "Stop every backend first")
}

// started is one in-process backend start (steps 3 and 4).
type started struct {
	db  *sql.DB
	enc *encryptionRuntime
}

func (s *started) stop() {
	if s == nil {
		return
	}
	s.enc.removeHeartbeat()
	s.db.Close()
}

// start runs the backend's start steps 3 (openDatabase) and 4 (the key
// load) in this process.
func (b *b116) start(cfg secretbox.KeyConfig) (*started, error) {
	b.t.Helper()
	prev := secretbox.Current()
	b.t.Cleanup(func() { secretbox.Install(prev); secretbox.SetSealGate(nil); secretbox.SetReporter(nil) })
	ctx := context.Background()
	db, err := openDatabase(ctx, b.dsn)
	if err != nil {
		return nil, err
	}
	enc, err := startEncryption(ctx, db, cfg)
	if err != nil {
		db.Close()
		return nil, err
	}
	s := &started{db: db, enc: enc}
	b.t.Cleanup(s.stop)
	return s, nil
}

// holdMigrator: see cmd/paceday-operator/pac116_test.go.
func holdMigrator116(t *testing.T, dsn, caller string, install func(release <-chan struct{}, held chan<- struct{})) (release func() (storage.MigrateResult, error)) {
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

// --- the shipped binary (AC-6, AC-7, AC-13) -------------------------------

var b116Bin struct {
	once sync.Once
	dir  string
	path string
	err  error
}

// b116Binary builds this package's binary once per test process.
func b116Binary(t *testing.T) string {
	t.Helper()
	b116Bin.once.Do(func() {
		b116Bin.dir, b116Bin.err = os.MkdirTemp("", "pac116-backend")
		if b116Bin.err != nil {
			return
		}
		b116Bin.path = filepath.Join(b116Bin.dir, "paceday")
		if runtime.GOOS == "windows" {
			b116Bin.path += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", b116Bin.path, ".")
		if out, err := cmd.CombinedOutput(); err != nil {
			b116Bin.err = fmt.Errorf("go build: %v\n%s", err, out)
		}
	})
	if b116Bin.err != nil {
		t.Fatal(b116Bin.err)
	}
	return b116Bin.path
}

func TestMain(m *testing.M) {
	code := m.Run()
	if b116Bin.dir != "" {
		_ = os.RemoveAll(b116Bin.dir)
	}
	os.Exit(code)
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return fmt.Sprint(l.Addr().(*net.TCPAddr).Port)
}

// proc is a running backend binary.
type proc struct {
	cmd  *exec.Cmd
	port string
	out  *syncBuffer
	done chan error
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// launch starts the backend binary on dsn with a fresh key and port; extra
// variables are added last.
func (b *b116) launch(extra map[string]string) *proc {
	b.t.Helper()
	bin := b116Binary(b.t)
	port := freePort(b.t)
	env := make([]string, 0)
	for _, kv := range os.Environ() {
		k := strings.SplitN(kv, "=", 2)[0]
		if strings.HasPrefix(k, "PACEDAY_") || strings.HasPrefix(k, "MIGRATE") || k == "DATABASE_URL" || k == "PORT" || k == "JWT_SECRET" || strings.HasPrefix(k, "SENTRY_") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "DATABASE_URL="+b.dsn, "PORT="+port, "JWT_SECRET=pac116-test-jwt-secret",
		"PACEDAY_ENCRYPTION_KEY="+b.key)
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+extra[k])
	}
	p := &proc{port: port, out: &syncBuffer{}, done: make(chan error, 1)}
	p.cmd = exec.Command(bin)
	p.cmd.Env = env
	p.cmd.Stdout, p.cmd.Stderr = p.out, p.out
	if err := p.cmd.Start(); err != nil {
		b.t.Fatal(err)
	}
	go func() { p.done <- p.cmd.Wait() }()
	b.t.Cleanup(func() {
		_ = p.cmd.Process.Kill()
		<-p.done
		b.settle()
	})
	return p
}

// exit waits for the process to end on its own.
func (p *proc) exit(t *testing.T) int {
	t.Helper()
	select {
	case err := <-p.done:
		p.done <- err
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		if err != nil {
			t.Fatal(err)
		}
		return 0
	case <-time.After(60 * time.Second):
		t.Fatalf("the backend did not exit:\n%s", p.out.String())
	}
	return -1
}

// ready waits for GET /api/ready to answer 200.
func (p *proc) ready(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-p.done:
			p.done <- err
			t.Fatalf("the backend exited (%v):\n%s", err, p.out.String())
		default:
		}
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://127.0.0.1:"+p.port+"/api/ready", nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("/api/ready never answered 200:\n%s", p.out.String())
}

// AC-6: pending plus a live heartbeat, or plus an app session: the backend
// exits non-zero before listening with the serving text; nothing applied.
func TestPAC116_AC6_BackendRefusesWhileServing(t *testing.T) {
	latest, _ := storage.LatestMigrationVersion()
	b := newB116(t, latest-1)
	inst := b.heartbeat(0)
	p := b.launch(nil)
	if code := p.exit(t); code == 0 || !b.servingText(p.out.String()) || !strings.Contains(p.out.String(), inst.String()) || strings.Contains(p.out.String(), "server listening") {
		t.Fatalf("live heartbeat: exit %d\n%s", code, p.out.String())
	}
	b.wantVersion(latest-1, false)

	b = newB116(t, latest-1)
	pid := b.session(storage.AppBackend)
	p = b.launch(nil)
	if code := p.exit(t); code == 0 || !b.servingText(p.out.String()) || !strings.Contains(p.out.String(), fmt.Sprintf("pid %d, application_name %q", pid, storage.AppBackend)) || strings.Contains(p.out.String(), "server listening") {
		t.Fatalf("app session: exit %d\n%s", code, p.out.String())
	}
	b.wantVersion(latest-1, false)
}

// AC-7: pending and nothing else: the backend migrates and answers
// /api/ready 200; with nothing pending and another backend live, a second
// one starts as today. Once ready, no paceday-migrate: session remains.
func TestPAC116_AC7_BackendMigratesThenServes(t *testing.T) {
	latest, _ := storage.LatestMigrationVersion()
	b := newB116(t, latest-1)
	first := b.launch(nil)
	first.ready(t)
	b.running[storage.AppBackend] = true
	if !strings.Contains(first.out.String(), fmt.Sprintf("database: migrations applied, schema version %d -> %d", latest-1, latest)) {
		t.Fatalf("no apply line:\n%s", first.out.String())
	}
	if n := b.migratorSessions(); n != 0 {
		t.Fatalf("%d paceday-migrate: session(s) left after the first backend is ready", n)
	}
	b.wantVersion(latest, false)
	if b.count(`SELECT count(*) FROM encryption_backend_heartbeats WHERE seen_at > now() - interval '90 seconds'`) != 1 {
		t.Fatal("the first backend has no live heartbeat")
	}
	second := b.launch(nil)
	second.ready(t)
	if strings.Contains(second.out.String(), "migrations applied") || strings.Contains(second.out.String(), "refusing to start") {
		t.Fatalf("second backend:\n%s", second.out.String())
	}
	if n := b.migratorSessions(); n != 0 {
		t.Fatalf("%d paceday-migrate: session(s) left after the second backend is ready", n)
	}
}

// AC-13 (backend half): AC-6 with every PACEDAY_* / MIGRATE* name found in
// non-test backend source set to "true" still refuses as serving.
func TestPAC116_AC13_BackendNoEnvironmentOverride(t *testing.T) {
	names := map[string]bool{}
	re := regexp.MustCompile(`"((?:PACEDAY_|MIGRATE)[A-Z0-9_]*)"`)
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			names[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !names["PACEDAY_ENCRYPTION_KEY"] || len(names) < 5 {
		t.Fatalf("the source scan found too little: %v", names)
	}
	extra := map[string]string{}
	for n := range names {
		switch n {
		case "PACEDAY_ENCRYPTION_KEY", "PACEDAY_ENCRYPTION_KEY_PREVIOUS":
			// Key material is parsed before step 3: "true" is no key and
			// refuses for that reason, which would prove nothing here.
			continue
		}
		extra[n] = "true"
	}
	// A variable whose value is validated before step 3 (a URL or an
	// address) refuses earlier with a text naming it: that start applies
	// nothing either, and the variable is dropped and the run repeated.
	latest, _ := storage.LatestMigrationVersion()
	early := regexp.MustCompile(`refusing to start: ((?:PACEDAY_|MIGRATE)[A-Z0-9_]*)="true"`)
	for len(extra) > 0 {
		b := newB116(t, latest-1)
		b.heartbeat(0)
		p := b.launch(extra)
		code := p.exit(t)
		out := p.out.String()
		b.wantVersion(latest-1, false)
		if code == 0 {
			t.Fatalf("with %v: the backend started:\n%s", extra, out)
		}
		if m := early.FindStringSubmatch(out); m != nil && extra[m[1]] != "" {
			t.Logf("%s=true is refused before step 3 (nothing applied); repeating without it", m[1])
			delete(extra, m[1])
			continue
		}
		if !b.servingText(out) {
			t.Fatalf("with %v: exit %d\n%s", extra, code, out)
		}
		t.Logf("refused as serving with %d variables set to true", len(extra))
		return
	}
	t.Fatal("every variable was refused before step 3")
}

// --- in process -----------------------------------------------------------

// AC-8: a migrator is held after its serving check; a backend with nothing
// pending for its own version writes its heartbeat, sees the migrating
// holder, deletes its heartbeat and refuses; after the migrator ends it
// starts.
func TestPAC116_AC8_BackendRefusesDuringMigration(t *testing.T) {
	latest, _ := storage.LatestMigrationVersion()
	b := newB116(t, latest-1)
	cfg, _ := pac84Keys(t, "ac8")
	release := holdMigrator116(t, b.dsn, "paceday-operator advance-all", func(rel <-chan struct{}, held chan<- struct{}) {
		storage.InstallAfterMigrateCheck(t, func(string) { close(held); <-rel })
	})
	restore := storage.SetMigrationCeilingForTest(latest - 1) // this backend's version
	defer restore()
	_, err := b.start(cfg)
	if err == nil || !regexp.MustCompile(`^refusing to start: migrations are being applied by paceday-migrate: paceday-operator advance-all \(pid \d+\); start the backend after it ends`).MatchString(err.Error()) {
		t.Fatalf("start during a migration: %v", err)
	}
	if n := b.count(`SELECT count(*) FROM encryption_backend_heartbeats`); n != 0 {
		t.Fatalf("the refused backend left %d heartbeat(s)", n)
	}
	if res, err := release(); err != nil || !res.Applied {
		t.Fatalf("the held migrator: %+v %v", res, err)
	}
	b.settle()
	s, err := b.start(cfg)
	if err != nil {
		t.Fatalf("start after the migration: %v", err)
	}
	s.stop()
	b.settle()
	b.wantVersion(latest, false)
}

// AC-9: a backend is held after writing its heartbeat, before its lock
// check; an operator with pending migrations takes the lock, sees the
// heartbeat and refuses; nothing is applied; the backend then starts.
func TestPAC116_AC9_MigratorRefusesDuringBackendStart(t *testing.T) {
	latest, _ := storage.LatestMigrationVersion()
	b := newB116(t, latest-1)
	cfg, _ := pac84Keys(t, "ac9")
	held := make(chan storage.Instance, 1)
	rel := make(chan struct{})
	var once sync.Once
	storage.InstallAfterHeartbeatWrite(t, func(inst storage.Instance) {
		once.Do(func() { held <- inst; <-rel })
	})
	restore := storage.SetMigrationCeilingForTest(latest - 1) // the backend's version
	type result struct {
		s   *started
		err error
	}
	done := make(chan result, 1)
	go func() {
		s, err := b.start(cfg)
		done <- result{s, err}
	}()
	var inst storage.Instance
	select {
	case inst = <-held:
	case r := <-done:
		t.Fatalf("the backend was not held: %v", r.err)
	case <-time.After(30 * time.Second):
		t.Fatal("the backend was not held")
	}
	restore() // the operator's version: something pending
	_, err := storage.MigrateWhenStopped(context.Background(), b.dsn, storage.MigrateOptions{Caller: storage.OperatorApp("convert")})
	var refusal *storage.MigrationRefusal
	if !errors.As(err, &refusal) || !refusal.Serving() || !strings.Contains(err.Error(), "live backend heartbeat: instance "+inst.ID.String()) {
		t.Fatalf("operator during a backend start: %v", err)
	}
	close(rel)
	r := <-done
	if r.err != nil {
		t.Fatalf("the backend after the refusal: %v", r.err)
	}
	r.s.stop()
	b.settle()
	b.wantVersion(latest-1, false)
}

// AC-10: an operator and a starting backend with the same pending
// migrations, released together after reading the version: exactly one
// applies; the other gets "lock held" naming the holder, or nothing to do;
// neither refuses as serving; applied once, never dirty.
func TestPAC116_AC10_OperatorAndBackendExactlyOneApplies(t *testing.T) {
	latest, _ := storage.LatestMigrationVersion()
	cfg, _ := pac84Keys(t, "ac10")
	for run := 0; run < 5; run++ {
		b := newB116(t, latest-1)
		logs := captureLog(t)
		uninstall := installBarrier(t, 2)
		var wg sync.WaitGroup
		var opRes storage.MigrateResult
		var opErr, startErr error
		var s *started
		wg.Add(2)
		go func() {
			defer wg.Done()
			opRes, opErr = storage.MigrateWhenStopped(context.Background(), b.dsn, storage.MigrateOptions{Caller: storage.OperatorApp("convert")})
		}()
		go func() {
			defer wg.Done()
			s, startErr = b.start(cfg)
		}()
		wg.Wait()
		uninstall()
		backendApplied := strings.Contains(logs.String(), "database: migrations applied")
		for _, err := range []error{opErr, startErr} {
			if err != nil && strings.Contains(err.Error(), "the app is serving") {
				t.Fatalf("run %d: refused as serving: %v", run, err)
			}
		}
		if opRes.Applied == backendApplied {
			t.Fatalf("run %d: operator applied=%v backend applied=%v (%v / %v)", run, opRes.Applied, backendApplied, opErr, startErr)
		}
		if opErr != nil && !errors.Is(opErr, storage.ErrMigrationLockHeld) {
			t.Fatalf("run %d: operator: %v", run, opErr)
		}
		if startErr != nil && !strings.Contains(startErr.Error(), "the maintenance lock is held by ") {
			t.Fatalf("run %d: backend: %v", run, startErr)
		}
		s.stop()
		b.settle()
		b.wantVersion(latest, false)
	}
}

// installBarrier holds every migrator after it read the version until n of
// them have arrived.
func installBarrier(t *testing.T, n int) (uninstall func()) {
	arrived := make(chan struct{}, n)
	gate := make(chan struct{})
	var opened sync.Once
	return storage.InstallAfterMigrateStateRead(t, func(string) {
		arrived <- struct{}{}
		if len(arrived) == n {
			opened.Do(func() { close(gate) })
		}
		select {
		case <-gate:
		case <-time.After(30 * time.Second):
		}
	})
}

// AC-11: a running backend whose heartbeat went stale and whose pool held no
// connection has its refresher tick while a migrator holds the lock: it
// begins the graceful shutdown with a line naming the migrator.
func TestPAC116_AC11_RunningBackendShutsDown(t *testing.T) {
	latest, _ := storage.LatestMigrationVersion()
	b := newB116(t, latest-1)
	cfg, _ := pac84Keys(t, "ac11")
	restore := storage.SetMigrationCeilingForTest(latest - 1)
	s, err := b.start(cfg)
	restore()
	if err != nil {
		t.Fatal(err)
	}
	logs := captureLog(t)
	s.db.SetMaxIdleConns(0) // no connection open at the migrator's check
	b.exec(`DELETE FROM encryption_backend_heartbeats`)
	release := holdMigrator116(t, b.dsn, "paceday-operator encryption convert", func(rel <-chan struct{}, held chan<- struct{}) {
		storage.InstallAfterMigrateCheck(t, func(string) { close(held); <-rel })
	})
	if s.enc.refresher.Tick(context.Background()) {
		t.Fatal("the refresher kept running under a migrator")
	}
	var reason string
	select {
	case reason = <-s.enc.fatal:
	default:
		t.Fatal("no shutdown requested")
	}
	want := regexp.MustCompile(`^encryption: migrations are being applied by paceday-migrate: paceday-operator encryption convert \(pid \d+\); shutting down$`)
	if !want.MatchString(reason) || !strings.Contains(logs.String(), reason) {
		t.Fatalf("shutdown reason %q, log:\n%s", reason, logs.String())
	}
	if res, err := release(); err != nil || !res.Applied {
		t.Fatalf("the held migrator: %+v %v", res, err)
	}
}

// AC-14 (backend half): dirty with no migrating holder: today's text, the
// row untouched.
func TestPAC116_AC14_BackendDirty(t *testing.T) {
	latest, _ := storage.LatestMigrationVersion()
	b := newB116(t, latest-1)
	b.exec(`UPDATE schema_migrations SET dirty = true`)
	_, err := openDatabase(context.Background(), b.dsn)
	if err == nil || err.Error() != fmt.Sprintf("failed to open database: run migrations: migrate up: Dirty database version %d. Fix and force version.", latest-1) {
		t.Fatalf("dirty: %v", err)
	}
	b.settle()
	b.wantVersion(latest-1, true)
}

// AC-19 (backend half): a migrator inside a migration file: the backend
// refuses naming it, never with "Dirty database version"; after, it starts.
func TestPAC116_AC19_BackendDuringMigrationFile(t *testing.T) {
	latest, _ := storage.LatestMigrationVersion()
	b := newB116(t, latest-1)
	cfg, _ := pac84Keys(t, "ac19")
	release := holdMigrator116(t, b.dsn, "paceday-operator advance-all", func(rel <-chan struct{}, held chan<- struct{}) {
		var once sync.Once
		storage.InstallInsideMigrationFile(t, func(uint) { once.Do(func() { close(held) }); <-rel })
	})
	deadline := time.Now().Add(10 * time.Second)
	for b.count(`SELECT CASE WHEN dirty THEN 1 ELSE 0 END FROM schema_migrations`) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("never dirty")
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, err := b.start(cfg)
	if err == nil || !strings.HasPrefix(err.Error(), "refusing to start: ") ||
		!strings.Contains(err.Error(), "being applied by paceday-migrate: paceday-operator advance-all (pid ") || strings.Contains(err.Error(), "Dirty database version") {
		t.Fatalf("start inside a migration file: %v", err)
	}
	if res, err := release(); err != nil || !res.Applied {
		t.Fatalf("the held migrator: %+v %v", res, err)
	}
	b.settle()
	s, err := b.start(cfg)
	if err != nil {
		t.Fatalf("start after: %v", err)
	}
	s.stop()
}

// AC-20 (backend half): a version the binary has no file for (above the
// highest, and the 033 gap) stops the start with golang-migrate's text
// before anything is opened or written.
func TestPAC116_AC20_BackendUnknownVersion(t *testing.T) {
	latest, _ := storage.LatestMigrationVersion()
	for _, v := range []uint{latest + 1, 33} {
		b := newB116(t, 0)
		b.exec(`UPDATE schema_migrations SET version = $1`, v)
		_, err := openDatabase(context.Background(), b.dsn)
		if err == nil || !strings.HasPrefix(err.Error(), fmt.Sprintf("failed to open database: run migrations: migrate up: no migration found for version %d", v)) {
			t.Fatalf("version %d: %v", v, err)
		}
		b.settle()
		b.wantVersion(v, false)
	}
}

// AC-21 (backend half): two backends with the same pending migrations,
// released together after reading the version, 20 times on fresh databases:
// exactly one applies, the other gets "lock held" or nothing to do, none
// refuses as serving, never dirty. Run with -race.
func TestPAC116_AC21_TwoBackendsExactlyOneApplies(t *testing.T) {
	latest, _ := storage.LatestMigrationVersion()
	for run := 0; run < 20; run++ {
		b := newB116(t, latest-1)
		logs := captureLog(t)
		uninstall := installBarrier(t, 2)
		var wg sync.WaitGroup
		dbs := make([]*sql.DB, 2)
		errs := make([]error, 2)
		for i := range dbs {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				dbs[i], errs[i] = openDatabase(context.Background(), b.dsn)
			}(i)
		}
		wg.Wait()
		uninstall()
		if n := strings.Count(logs.String(), "database: migrations applied"); n != 1 {
			t.Fatalf("run %d: %d backends applied: %v\n%s", run, n, errs, logs.String())
		}
		for i, err := range errs {
			switch {
			case err == nil:
				dbs[i].Close()
			case strings.Contains(err.Error(), "the app is serving"):
				t.Fatalf("run %d: refused as serving: %v", run, err)
			case !strings.Contains(err.Error(), "refusing to start: pending migrations from version") || !strings.Contains(err.Error(), "the maintenance lock is held by "):
				t.Fatalf("run %d: %v", run, err)
			}
		}
		b.settle()
		b.wantVersion(latest, false)
	}
}
