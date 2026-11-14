package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/testdb"
	"github.com/golang-migrate/migrate/v4"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// PAC-116 (contract contracts/features/PAC-116.md): the migrate-when-stopped
// procedure at the storage level. The callers' halves (backend start,
// paceday-operator) are in main_pac116_test.go and
// cmd/paceday-operator/pac116_test.go.

// --- unit: no database ---------------------------------------------------

func TestPAC116_EmbeddedVersions(t *testing.T) {
	e, err := embeddedVersions()
	if err != nil {
		t.Fatal(err)
	}
	latest, _ := LatestMigrationVersion()
	if latest != e.highest || latest < 39 {
		t.Fatalf("highest %d / %d", latest, e.highest)
	}
	if e.known[33] || !e.known[32] || !e.known[34] || !e.known[latest] || e.known[latest+1] {
		t.Fatalf("known set wrong: 33=%v 32=%v 34=%v", e.known[33], e.known[32], e.known[34])
	}
	for _, c := range []struct{ ceiling, want uint }{{0, latest}, {36, 36}, {latest + 10, latest}} {
		if got := e.highestFor(c.ceiling); got != c.want {
			t.Errorf("highestFor(%d) = %d, want %d", c.ceiling, got, c.want)
		}
	}
}

func TestPAC116_MigratorNameAndHolder(t *testing.T) {
	if got := MigratorName(AppBackend); got != "paceday-migrate: paceday-backend" {
		t.Fatal(got)
	}
	var none *LockHolder
	for _, c := range []struct {
		h    *LockHolder
		want bool
	}{
		{none, false},
		{&LockHolder{AppName: "paceday-migrate: paceday-operator advance-all"}, true},
		{&LockHolder{AppName: "paceday-migrate:x"}, true},
		{&LockHolder{AppName: OperatorApp("decrypt-all")}, false},
		{&LockHolder{AppName: "psql paceday-migrate:"}, false},
	} {
		if c.h.Migrating() != c.want {
			t.Errorf("%+v: Migrating = %v", c.h, !c.want)
		}
	}
	h := &LockHolder{AppName: "paceday-migrate: paceday-backend", PID: 42, BackendStart: time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)}
	if got := MigratingHolderText(h); got != "migrations are being applied by paceday-migrate: paceday-backend (pid 42, started 2026-11-14T09:00:00Z); run again after it ends. Nothing was changed." {
		t.Fatal(got)
	}
}

// Contract 5: every field of the serving and lock-held texts; hidden fields
// read "?"; the refusals match their sentinels.
func TestPAC116_RefusalTexts(t *testing.T) {
	started := time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC)
	inst := uuid.MustParse("6f1c2b9a-8e7d-4c3b-2a19-000000000001")
	serving := &MigrationRefusal{kind: refusalServing, From: 38, FromPresent: true, To: 39,
		Heartbeats: []LiveBeat{{InstanceID: inst, Hostname: "web-1", Age: 5*time.Second + 300*time.Millisecond}},
		Sessions: []OtherSession{
			{PID: 101, AppName: "paceday-backend", User: "clockwise", Client: "10.0.0.5", Started: &started, State: "idle"},
			{PID: 102, AppName: "", User: "monitor", Client: "?", State: "?", Hidden: true},
		}}
	text := serving.Error()
	for _, want := range []string{
		"pending migrations from version 38 to 39 were not applied: the app is serving.",
		`live backend heartbeat: instance 6f1c2b9a-8e7d-4c3b-2a19-000000000001 on "web-1", seen 5s ago`,
		`other database session: pid 101, application_name "paceday-backend", user clockwise, client 10.0.0.5, started 2026-11-14T08:00:00Z, state idle`,
		`other database session: pid 102, application_name "", user monitor, client ?, started ?, state ?`,
		"Stop every backend first", "close or terminate the sessions listed", "Nothing was changed.",
		`docs/runbook.md, "Applying migrations: stop, migrate, start"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("serving text lacks %q:\n%s", want, text)
		}
	}
	if !errors.Is(serving, ErrServing) || errors.Is(serving, ErrMigrationLockHeld) || !serving.Serving() {
		t.Fatal("serving sentinel")
	}
	holder := &LockHolder{AppName: "paceday-migrate: paceday-backend", PID: 7, BackendStart: started}
	held := &MigrationRefusal{kind: refusalLockHeld, To: 39, Holder: holder}
	if got := held.Error(); got != "pending migrations from an empty schema to version 39 were not applied: the maintenance lock is held by paceday-migrate: paceday-backend (pid 7, started 2026-11-14T08:00:00Z); run again after it ends. Nothing was changed." {
		t.Fatal(got)
	}
	inProgress := &MigrationRefusal{kind: refusalInProgress, From: 39, FromPresent: true, To: 39, Holder: holder}
	if got := inProgress.Error(); !strings.Contains(got, "being applied by paceday-migrate: paceday-backend (pid 7") || strings.Contains(got, "Dirty") {
		t.Fatal(got)
	}
	for _, r := range []*MigrationRefusal{held, inProgress} {
		if !errors.Is(r, ErrMigrationLockHeld) || errors.Is(r, ErrServing) || r.Serving() {
			t.Fatalf("lock-held sentinel: %v", r)
		}
	}
	if got := (&MigrationRefusal{kind: refusalLockHeld, From: 1, FromPresent: true, To: 2}).Error(); !strings.Contains(got, "held by another process") {
		t.Fatal(got)
	}
	unknown := NewUnknownVersionError(33)
	if !strings.HasPrefix(unknown.Error(), "no migration found for version 33: ") || !errors.Is(unknown, fs.ErrNotExist) {
		t.Fatalf("unknown: %v", unknown)
	}
}

// --- integration ---------------------------------------------------------

// pac116DB is a database of its own migrated to version below (0: every
// migration), with no session left on it.
func pac116DB(t *testing.T, below uint) string {
	t.Helper()
	dsn, cleanup, err := testdb.Create()
	if err != nil {
		t.Skipf("PostgreSQL unavailable (PAC-116 storage tests need it; scripts/test-backend.sh provides it): %v", err)
	}
	t.Cleanup(func() { _ = cleanup() })
	if below > 0 {
		restore := SetMigrationCeilingForTest(below)
		defer restore()
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	pac116Idle(t, dsn)
	return dsn
}

func pac116Idle(t *testing.T, dsn string) {
	t.Helper()
	if err := testdb.WaitNoSessions(dsn, 10*time.Second); err != nil {
		t.Fatal(err)
	}
}

// pac116Conn opens one connection named app on dsn (as user, when set) and
// keeps it open until the test ends; it returns the connection and its pid.
func pac116Conn(t *testing.T, dsn, app string) (*sql.Conn, int) {
	t.Helper()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["application_name"] = app
	db := stdlib.OpenDB(*cfg)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	if err := conn.QueryRowContext(context.Background(), `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(); db.Close() })
	return conn, pid
}

// pac116Observe runs fn on one short-lived connection to dsn, then waits
// until that connection's server process has ended, so it is never counted
// as another session by a later run of the procedure.
func pac116Observe(t *testing.T, dsn string, fn func(conn *sql.Conn)) {
	t.Helper()
	db, err := OpenNoMigrate(dsn, "pac116-test-observer")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	if err := conn.QueryRowContext(context.Background(), `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	fn(conn)
	conn.Close()
	db.Close()
	if err := testdb.WaitPIDGone(pid, 10*time.Second); err != nil {
		t.Fatal(err)
	}
}

// pac116Version reads the version on a short-lived connection.
func pac116Version(t *testing.T, dsn string) (v uint, dirty bool) {
	t.Helper()
	pac116Observe(t, dsn, func(conn *sql.Conn) {
		var err error
		if v, dirty, _, err = SchemaVersion(context.Background(), conn); err != nil {
			t.Fatal(err)
		}
	})
	return v, dirty
}

// AC-4 (storage half), AC-12, AC-21 (exemption): pending with nothing else
// connected applies, then a second run has nothing to do; a session named
// paceday-migrate: with no lock is not counted; any other session is.
func TestPAC116_ProcedureAppliesOnlyWhenStopped(t *testing.T) {
	latest, _ := LatestMigrationVersion()
	ctx := context.Background()

	dsn := pac116DB(t, latest-1)
	_, migPID := pac116Conn(t, dsn, "paceday-migrate: test")
	res, err := MigrateWhenStopped(ctx, dsn, MigrateOptions{Caller: "pac116 test"})
	if err != nil || !res.Applied || res.From != latest-1 || !res.FromPresent || res.To != latest {
		t.Fatalf("pending, only a paceday-migrate: session (pid %d): %+v %v", migPID, res, err)
	}
	if v, dirty := pac116Version(t, dsn); v != latest || dirty {
		t.Fatalf("after apply: %d dirty=%v", v, dirty)
	}
	res, err = MigrateWhenStopped(ctx, dsn, MigrateOptions{Caller: "pac116 test"})
	if err != nil || res.Applied || res.From != latest {
		t.Fatalf("second run: %+v %v", res, err)
	}

	dsn = pac116DB(t, latest-1)
	_, pid := pac116Conn(t, dsn, AppBackend)
	_, err = MigrateWhenStopped(ctx, dsn, MigrateOptions{Caller: "pac116 test"})
	var refusal *MigrationRefusal
	if !errors.As(err, &refusal) || !refusal.Serving() || len(refusal.Sessions) != 1 || refusal.Sessions[0].PID != pid ||
		refusal.Sessions[0].AppName != AppBackend || refusal.Sessions[0].Hidden || refusal.Sessions[0].State != "idle" {
		t.Fatalf("pending with a paceday-backend session: %v", err)
	}
	if v, dirty := pac116Version(t, dsn); v != latest-1 || dirty {
		t.Fatalf("a refusal changed the version: %d dirty=%v", v, dirty)
	}
}

// AC-14, AC-19, AC-20 (storage half): the version states of contract 2.
func TestPAC116_VersionStates(t *testing.T) {
	latest, _ := LatestMigrationVersion()
	ctx := context.Background()
	dsn := pac116DB(t, latest-1)
	set := func(v uint, dirty bool) {
		t.Helper()
		pac116Observe(t, dsn, func(conn *sql.Conn) {
			if _, err := conn.ExecContext(ctx, `UPDATE schema_migrations SET version = $1, dirty = $2`, v, dirty); err != nil {
				t.Fatal(err)
			}
		})
	}
	probe := func() (st VersionState) {
		t.Helper()
		pac116Observe(t, dsn, func(conn *sql.Conn) {
			var err error
			if st, err = ProbeVersionState(ctx, conn); err != nil {
				t.Fatal(err)
			}
		})
		return st
	}

	if st := probe(); st.Kind != VersionPending || st.Version != latest-1 || st.Highest != latest {
		t.Fatalf("pending: %+v", st)
	}
	// Unknown, above the highest and in the gap (033): golang-migrate's text,
	// before any lock or check, nothing written.
	for _, v := range []uint{latest + 1, 33} {
		set(v, false)
		if st := probe(); st.Kind != VersionUnknown || st.Version != v {
			t.Fatalf("version %d: %+v", v, st)
		}
		_, err := MigrateWhenStopped(ctx, dsn, MigrateOptions{Caller: "pac116 test"})
		var unknown *UnknownVersionError
		if !errors.As(err, &unknown) || !strings.Contains(err.Error(), fmt.Sprintf("no migration found for version %d", v)) {
			t.Fatalf("version %d: %v", v, err)
		}
		if got, dirty := pac116Version(t, dsn); got != v || dirty {
			t.Fatalf("version %d changed to %d dirty=%v", v, got, dirty)
		}
	}
	// Dirty, no migrating holder: today's dirty error, decided under the
	// lock; the row is left as it was.
	set(latest-1, true)
	if st := probe(); st.Kind != VersionDirty {
		t.Fatalf("dirty: %+v", st)
	}
	_, err := MigrateWhenStopped(ctx, dsn, MigrateOptions{Caller: "pac116 test"})
	if !errors.As(err, new(migrate.ErrDirty)) || !strings.Contains(err.Error(), fmt.Sprintf("Dirty database version %d. Fix and force version.", latest-1)) {
		t.Fatalf("dirty: %v", err)
	}
	if v, dirty := pac116Version(t, dsn); v != latest-1 || !dirty {
		t.Fatal("dirty row touched")
	}
	// Dirty while a migrator holds (8400,1): in progress, never "dirty".
	holder, holderPID := pac116Conn(t, dsn, "paceday-migrate: pac116 holder")
	if _, err := holder.ExecContext(ctx, `SELECT pg_advisory_lock(8400, 1)`); err != nil {
		t.Fatal(err)
	}
	if st := probe(); st.Kind != VersionInProgress || st.Holder == nil || st.Holder.PID != holderPID {
		t.Fatalf("in progress: %+v", st)
	}
	_, err = MigrateWhenStopped(ctx, dsn, MigrateOptions{Caller: "pac116 test"})
	if !errors.Is(err, ErrMigrationLockHeld) || !strings.Contains(err.Error(), fmt.Sprintf("being applied by paceday-migrate: pac116 holder (pid %d", holderPID)) || strings.Contains(err.Error(), "Dirty") {
		t.Fatalf("in progress: %v", err)
	}
	if _, err := holder.ExecContext(ctx, `SELECT pg_advisory_unlock(8400, 1)`); err != nil {
		t.Fatal(err)
	}
	// Clean at the highest: nothing to do, no lock and no check (a live
	// session does not matter).
	set(latest, false)
	if st := probe(); st.Kind != VersionCurrent {
		t.Fatalf("current: %+v", st)
	}
	_, _ = pac116Conn(t, dsn, AppBackend)
	if res, err := MigrateWhenStopped(ctx, dsn, MigrateOptions{Caller: "pac116 test"}); err != nil || res.Applied {
		t.Fatalf("current with a session: %+v %v", res, err)
	}
}

// Contract 3 step 3: a pending run with the lock held by any process refuses
// as "lock held", naming the holder; below the floor nothing is attempted.
func TestPAC116_LockHeldAndFloor(t *testing.T) {
	latest, _ := LatestMigrationVersion()
	ctx := context.Background()
	dsn := pac116DB(t, latest-1)
	holder, pid := pac116Conn(t, dsn, "paceday-migrate: other")
	if _, err := holder.ExecContext(ctx, `SELECT pg_advisory_lock(8400, 1)`); err != nil {
		t.Fatal(err)
	}
	_, err := MigrateWhenStopped(ctx, dsn, MigrateOptions{Caller: "pac116 test"})
	var refusal *MigrationRefusal
	if !errors.As(err, &refusal) || refusal.Serving() || refusal.Holder == nil || refusal.Holder.PID != pid ||
		!strings.Contains(err.Error(), fmt.Sprintf("pending migrations from version %d to %d were not applied: the maintenance lock is held by paceday-migrate: other (pid %d", latest-1, latest, pid)) {
		t.Fatalf("lock held: %v", err)
	}
	if _, err := MigrateWhenStopped(ctx, dsn, MigrateOptions{Caller: "pac116 test", Floor: latest}); !errors.Is(err, ErrBelowFloor) {
		t.Fatalf("below the floor: %v", err)
	}
}

// AC-18 (challenge S1): a session of another role, hidden from a migrator
// that is neither superuser nor in pg_read_all_stats, still counts, listed
// by pid with "?" fields; a superuser migrator sees its fields.
func TestPAC116_HiddenSessionOfAnotherRoleCounts(t *testing.T) {
	latest, _ := LatestMigrationVersion()
	ctx := context.Background()
	dsn, cleanup, err := testdb.Create()
	if err != nil {
		t.Skipf("PostgreSQL unavailable: %v", err)
	}
	admin, err := sql.Open("pgx", testdb.AdminDSN())
	if err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	mig, mon := "pac116_mig_"+suffix, "pac116_mon_"+suffix
	// Cleanups run last-in first-out: the database goes before its roles.
	t.Cleanup(func() {
		for _, r := range []string{mig, mon} {
			if _, err := admin.Exec(`DROP ROLE IF EXISTS ` + r); err != nil {
				t.Errorf("drop role %s: %v", r, err)
			}
		}
		admin.Close()
	})
	t.Cleanup(func() { _ = cleanup() })
	name := strings.TrimPrefix(mustURL(t, dsn).Path, "/")
	for _, stmt := range []string{
		`CREATE ROLE ` + mig + ` LOGIN NOSUPERUSER PASSWORD 'pac116'`,
		`CREATE ROLE ` + mon + ` LOGIN NOSUPERUSER PASSWORD 'pac116'`,
		// Setup only: with pg_read_all_stats the migrator sees every session,
		// so an autovacuum worker cannot refuse the setup migration.
		`GRANT pg_read_all_stats TO ` + mig,
		`ALTER DATABASE "` + name + `" OWNER TO ` + mig,
	} {
		if _, err := admin.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	pac116Idle(t, dsn)
	asRole := func(role string) string {
		u := mustURL(t, dsn)
		u.User = url.UserPassword(role, "pac116")
		return u.String()
	}
	restore := SetMigrationCeilingForTest(latest - 1)
	_, err = MigrateWhenStopped(ctx, asRole(mig), MigrateOptions{Caller: "pac116 setup"})
	restore()
	if err != nil {
		t.Fatalf("setup migration as %s: %v", mig, err)
	}
	if _, err := admin.Exec(`REVOKE pg_read_all_stats FROM ` + mig); err != nil {
		t.Fatal(err)
	}
	pac116Idle(t, dsn)

	_, monPID := pac116Conn(t, asRole(mon), "")
	_, err = MigrateWhenStopped(ctx, asRole(mig), MigrateOptions{Caller: "pac116 test"})
	var refusal *MigrationRefusal
	if !errors.As(err, &refusal) || !refusal.Serving() {
		t.Fatalf("hidden session, non-superuser migrator: %v", err)
	}
	found := false
	for _, s := range refusal.Sessions {
		if s.PID == monPID {
			found = s.Hidden && s.State == "?" && s.Client == "?" && s.Started == nil
		}
	}
	if !found || !strings.Contains(err.Error(), fmt.Sprintf("pid %d, application_name \"\", user %s, client ?, started ?, state ?", monPID, mon)) {
		t.Fatalf("the hidden session is not listed with '?' fields: %v", err)
	}
	if v, dirty := pac116Version(t, dsn); v != latest-1 || dirty {
		t.Fatalf("version changed: %d %v", v, dirty)
	}

	_, err = MigrateWhenStopped(ctx, dsn, MigrateOptions{Caller: "pac116 test superuser"})
	if !errors.As(err, &refusal) || !refusal.Serving() {
		t.Fatalf("superuser migrator: %v", err)
	}
	found = false
	for _, s := range refusal.Sessions {
		if s.PID == monPID {
			found = !s.Hidden && s.State == "idle" && s.Started != nil && s.User == mon
		}
	}
	if !found {
		t.Fatalf("superuser does not see the session's fields: %+v", refusal.Sessions)
	}
}

func mustURL(t *testing.T, dsn string) *url.URL {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// pac116Set writes schema_migrations on a short-lived connection.
func pac116Set(t *testing.T, dsn string, v uint, dirty bool) {
	t.Helper()
	pac116Observe(t, dsn, func(conn *sql.Conn) {
		if _, err := conn.ExecContext(context.Background(), `UPDATE schema_migrations SET version = $1, dirty = $2`, v, dirty); err != nil {
			t.Fatal(err)
		}
	})
}

// Contract 2 "Read order", review B1: a dirty row read with no lock and no
// migrating holder is never final. A migrator that writes clean and unlocks
// right after the step-1 read must not turn into today's dirty error; step 4
// decides under the lock.
func TestPAC116_LockFreeDirtyIsNeverFinal(t *testing.T) {
	latest, _ := LatestMigrationVersion()
	ctx := context.Background()
	for _, after := range []struct {
		name    string
		v       uint
		applied bool
	}{{"finished: current", latest, false}, {"finished: still pending", latest - 1, true}} {
		dsn := pac116DB(t, latest-1)
		pac116Set(t, dsn, latest-1, true)
		var once sync.Once
		uninstall := InstallAfterMigrateStateRead(t, func(string) {
			once.Do(func() { pac116Set(t, dsn, after.v, false) })
		})
		res, err := MigrateWhenStopped(ctx, dsn, MigrateOptions{Caller: "pac116 test"})
		uninstall()
		if err != nil || res.Applied != after.applied {
			t.Fatalf("%s: dirty at step 1, clean before step 4: %+v %v", after.name, res, err)
		}
		if v, dirty := pac116Version(t, dsn); v != latest || dirty {
			t.Fatalf("%s: %d dirty=%v", after.name, v, dirty)
		}
	}
}

// Contract 5.1 / review B1 (M3c): status's lock-free probe re-reads once; a
// migration that finishes between the two reads is reported as finished,
// never as dirty.
func TestPAC116_ProbeRereadsDirty(t *testing.T) {
	latest, _ := LatestMigrationVersion()
	ctx := context.Background()
	dsn := pac116DB(t, latest-1)
	pac116Set(t, dsn, latest-1, true)
	fired := false
	InstallAfterProbeDirtyRead(t, func() {
		fired = true
		pac116Set(t, dsn, latest, false)
	})
	var st VersionState
	pac116Observe(t, dsn, func(conn *sql.Conn) {
		var err error
		if st, err = ProbeVersionState(ctx, conn); err != nil {
			t.Fatal(err)
		}
	})
	if !fired || st.Kind != VersionCurrent || st.Version != latest {
		t.Fatalf("probe after a migration finished between its reads: fired=%v %+v", fired, st)
	}
}

// Challenge S4 / review N1: the loser's paceday-migrate: connection stays
// open across the winner's serving check (the loser is held after its lock
// attempt failed until the winner returned): exactly one applies, the other
// gets "lock held", neither refuses as serving.
func TestPAC116_LoserSessionDuringWinnersCheck(t *testing.T) {
	latest, _ := LatestMigrationVersion()
	dsn := pac116DB(t, latest-1)
	winnerDone := make(chan struct{})
	var closeOnce sync.Once
	loserHeld := make(chan struct{}, 2)
	InstallAfterMigrateLockRefused(t, func(string) {
		loserHeld <- struct{}{}
		select {
		case <-winnerDone:
		case <-time.After(30 * time.Second):
		}
	})
	// Both read the version (pending) before either tries the lock.
	arrived := make(chan struct{}, 2)
	gate := make(chan struct{})
	var opened sync.Once
	InstallAfterMigrateStateRead(t, func(string) {
		arrived <- struct{}{}
		if len(arrived) == 2 {
			opened.Do(func() { close(gate) })
		}
		select {
		case <-gate:
		case <-time.After(30 * time.Second):
		}
	})
	type out struct {
		res MigrateResult
		err error
	}
	results := make(chan out, 2)
	for _, caller := range []string{"pac116 A", "pac116 B"} {
		go func(caller string) {
			res, err := MigrateWhenStopped(context.Background(), dsn, MigrateOptions{Caller: caller})
			if err == nil {
				closeOnce.Do(func() { close(winnerDone) })
			}
			results <- out{res, err}
		}(caller)
	}
	applied, held := 0, 0
	for i := 0; i < 2; i++ {
		r := <-results
		switch {
		case r.err == nil && r.res.Applied:
			applied++
		case errors.Is(r.err, ErrMigrationLockHeld):
			held++
		default:
			t.Errorf("unexpected outcome: %+v %v", r.res, r.err)
		}
		if errors.Is(r.err, ErrServing) {
			t.Errorf("a migrator refused as serving: %v", r.err)
		}
	}
	closeOnce.Do(func() { close(winnerDone) })
	if applied != 1 || held != 1 || len(loserHeld) != 1 {
		t.Fatalf("applied %d, lock held %d, losers held across the check %d", applied, held, len(loserHeld))
	}
	if v, dirty := pac116Version(t, dsn); v != latest || dirty {
		t.Fatalf("%d dirty=%v", v, dirty)
	}
}

// Review N3: a dirty row at a version the binary has no file for is
// "unknown" (golang-migrate's "no migration found"), not "dirty".
func TestPAC116_DirtyUnknownVersionIsUnknown(t *testing.T) {
	latest, _ := LatestMigrationVersion()
	ctx := context.Background()
	dsn := pac116DB(t, latest-1)
	pac116Set(t, dsn, latest+1, true)
	var st VersionState
	pac116Observe(t, dsn, func(conn *sql.Conn) {
		var err error
		if st, err = ProbeVersionState(ctx, conn); err != nil {
			t.Fatal(err)
		}
	})
	if st.Kind != VersionUnknown {
		t.Fatalf("probe: %+v", st)
	}
	_, err := MigrateWhenStopped(ctx, dsn, MigrateOptions{Caller: "pac116 test"})
	var unknown *UnknownVersionError
	if !errors.As(err, &unknown) || errors.As(err, new(migrate.ErrDirty)) || !strings.Contains(err.Error(), fmt.Sprintf("no migration found for version %d", latest+1)) {
		t.Fatalf("dirty unknown version: %v", err)
	}
	if v, dirty := pac116Version(t, dsn); v != latest+1 || !dirty {
		t.Fatal("row touched")
	}
}
