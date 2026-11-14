package storage

// PAC-116 (contract contracts/features/PAC-116.md sections 2-5): pending
// migrations are applied only while the app is not serving. MigrateWhenStopped
// is the one procedure every caller uses (the backend at start, the mutating
// paceday-operator encryption subcommands, PAC-71's subcommands); nothing
// else in non-test code applies an up migration.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// migratorPrefix starts the application_name of a migrator's connection
// (contract 2, "Migrator name"); a maintenance-lock holder whose name starts
// with it is a migrating holder.
const migratorPrefix = "paceday-migrate:"

// MigratorName is the application_name of caller's migrator connection.
func MigratorName(caller string) string { return migratorPrefix + " " + caller }

// Migrating reports whether h is a migrator (contract 2).
func (h *LockHolder) Migrating() bool {
	return h != nil && strings.HasPrefix(h.AppName, migratorPrefix)
}

// MigratingHolderText is the lock-held text for a process that found nothing
// to apply but sees a migrator at work (contract 4.5, 5).
func MigratingHolderText(h *LockHolder) string {
	return fmt.Sprintf("migrations are being applied by %s (pid %d, started %s); run again after it ends. Nothing was changed.",
		h.AppName, h.PID, h.BackendStart.UTC().Format(time.RFC3339))
}

// VersionKind classifies schema_migrations against the embedded migrations
// (contract 2, "Version state", read in this order).
type VersionKind int

const (
	// VersionCurrent: clean and at the binary's highest version.
	VersionCurrent VersionKind = iota
	// VersionPending: absent, empty, or clean below the highest version.
	VersionPending
	// VersionDirty: dirty with no migrating holder of the maintenance lock.
	VersionDirty
	// VersionInProgress: dirty while a migrator holds the maintenance lock.
	VersionInProgress
	// VersionUnknown: a version the binary has no file for.
	VersionUnknown
)

// VersionState is one reading of the schema version.
type VersionState struct {
	Kind    VersionKind
	Version uint // meaningful when Present
	Present bool
	Highest uint        // the binary's highest version (the test ceiling, if set)
	Holder  *LockHolder // the migrating holder, for VersionInProgress
}

// embedded is the set of embedded up-migration versions.
type embedded struct {
	known   map[uint]bool
	highest uint
}

var (
	embeddedOnce sync.Once
	embeddedSet  embedded
	embeddedErr  error
)

func embeddedVersions() (embedded, error) {
	embeddedOnce.Do(func() {
		entries, err := fs.ReadDir(migrationFiles, "migrations")
		if err != nil {
			embeddedErr = err
			return
		}
		embeddedSet.known = map[uint]bool{}
		for _, e := range entries {
			n := e.Name()
			if !strings.HasSuffix(n, ".up.sql") {
				continue
			}
			v, err := strconv.ParseUint(strings.SplitN(n, "_", 2)[0], 10, 32)
			if err != nil {
				embeddedErr = fmt.Errorf("migration file %s: %w", n, err)
				return
			}
			embeddedSet.known[uint(v)] = true
			if uint(v) > embeddedSet.highest {
				embeddedSet.highest = uint(v)
			}
		}
	})
	return embeddedSet, embeddedErr
}

// highestFor is the version a run applies up to: every embedded migration,
// or the test ceiling (SetMigrationCeilingForTest) when one is set.
func (e embedded) highestFor(ceiling uint) uint {
	if ceiling != 0 && ceiling < e.highest {
		return ceiling
	}
	return e.highest
}

// readVersion reads schema_migrations; present is false when the table is
// absent or empty.
func readVersion(ctx context.Context, q DBTXQuery) (version uint, dirty, present bool, err error) {
	var exists bool
	if err = q.QueryRowContext(ctx, `SELECT to_regclass('public.schema_migrations') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		return 0, false, false, err
	}
	var v int64
	err = q.QueryRowContext(ctx, `SELECT version, dirty FROM schema_migrations LIMIT 1`).Scan(&v, &dirty)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, false, nil
	}
	if err != nil {
		return 0, false, false, err
	}
	return uint(v), dirty, true, nil
}

// readState reads and classifies the version, without looking at the lock:
// a dirty row is VersionDirty here; the callers decide about its holder.
func readState(ctx context.Context, q DBTXQuery, e embedded, highest uint) (VersionState, error) {
	v, dirty, present, err := readVersion(ctx, q)
	if err != nil {
		return VersionState{}, err
	}
	st := VersionState{Version: v, Present: present, Highest: highest}
	switch {
	case !present:
		st.Kind = VersionPending
	case !e.known[v]:
		st.Kind = VersionUnknown
	case dirty:
		st.Kind = VersionDirty
	case v < highest:
		st.Kind = VersionPending
	default:
		st.Kind = VersionCurrent
	}
	return st, nil
}

// ProbeVersionState is the lock-free reading of contract 2 for a reader that
// takes no lock (encryption status, 5.1): the version, then the holder; a
// dirty version with no migrating holder is re-read once and reported dirty
// only if it is still dirty with still no migrating holder ("Read order").
func ProbeVersionState(ctx context.Context, q DBTXQuery) (VersionState, error) {
	e, err := embeddedVersions()
	if err != nil {
		return VersionState{}, err
	}
	highest := e.highestFor(uint(migrationCeiling.Load()))
	var st VersionState
	for attempt := 0; attempt < 2; attempt++ {
		st, err = readState(ctx, q, e, highest)
		if err != nil || st.Kind != VersionDirty {
			return st, err
		}
		h, err := MaintenanceHolder(ctx, q)
		if err != nil {
			return st, err
		}
		if h.Migrating() {
			st.Kind, st.Holder = VersionInProgress, h
			return st, nil
		}
		if attempt == 0 {
			if fn, ok := afterProbeDirtyRead.get(); ok {
				fn()
			}
		}
	}
	return st, nil
}

// UnknownVersionError is golang-migrate's own refusal of a schema version the
// binary has no file for (contract 2, "Unknown"; "no migration found for
// version N", as Up returned it through readUp/versionExists).
type UnknownVersionError struct {
	Version uint
	err     error
}

func (e *UnknownVersionError) Error() string {
	return fmt.Sprintf("no migration found for version %d: %v", e.Version, e.err)
}

func (e *UnknownVersionError) Unwrap() error { return e.err }

// NewUnknownVersionError is the error for version v, built from the
// embedded source as golang-migrate builds it.
func NewUnknownVersionError(v uint) *UnknownVersionError {
	err := error(fs.ErrNotExist)
	if src, serr := iofs.New(migrationFiles, "migrations"); serr == nil {
		if r, _, rerr := src.ReadDown(v); rerr == nil {
			_ = r.Close()
		} else {
			err = rerr
		}
		_ = src.Close()
	}
	return &UnknownVersionError{Version: v, err: err}
}

// ErrBelowFloor: the schema is below MigrateOptions.Floor; nothing was
// applied and no lock was taken (PAC-84 review #3, kept by contract 5.1).
var ErrBelowFloor = errors.New("schema below the caller's floor; no migration applied")

// Sentinels for the two refusals (contract 5).
var (
	ErrServing           = errors.New("the app is serving")
	ErrMigrationLockHeld = errors.New("the maintenance lock is held")
)

// LiveBeat is a live heartbeat as a migrator saw it.
type LiveBeat struct {
	InstanceID uuid.UUID
	Hostname   string
	Age        time.Duration
}

// OtherSession is one other session on the database (contract 2). Hidden is
// true when PostgreSQL hides its details from the reading role.
type OtherSession struct {
	PID     int
	AppName string
	User    string
	Client  string
	Started *time.Time
	State   string
	Hidden  bool
}

type refusalKind int

const (
	refusalServing refusalKind = iota
	refusalLockHeld
	refusalInProgress
)

// MigrationRefusal is a refusal of contract 5: nothing was applied and the
// version was not touched. Error() is the text after the caller's prefix.
type MigrationRefusal struct {
	kind        refusalKind
	From        uint
	FromPresent bool
	To          uint
	Holder      *LockHolder
	Heartbeats  []LiveBeat
	Sessions    []OtherSession
}

// Serving reports a "serving" refusal (else "lock held" or in progress).
func (r *MigrationRefusal) Serving() bool { return r.kind == refusalServing }

func (r *MigrationRefusal) Is(t error) bool {
	if r.kind == refusalServing {
		return t == ErrServing
	}
	return t == ErrMigrationLockHeld
}

func (r *MigrationRefusal) pending() string {
	if !r.FromPresent {
		return fmt.Sprintf("pending migrations from an empty schema to version %d", r.To)
	}
	return fmt.Sprintf("pending migrations from version %d to %d", r.From, r.To)
}

func (r *MigrationRefusal) Error() string {
	holder := "another process"
	if h := r.Holder; h != nil {
		holder = fmt.Sprintf("%s (pid %d, started %s)", h.AppName, h.PID, h.BackendStart.UTC().Format(time.RFC3339))
	}
	switch r.kind {
	case refusalLockHeld:
		return fmt.Sprintf("%s were not applied: the maintenance lock is held by %s; run again after it ends. Nothing was changed.", r.pending(), holder)
	case refusalInProgress:
		return fmt.Sprintf("migrations up to version %d were not applied: being applied by %s; run again after it ends. Nothing was changed.", r.To, holder)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s were not applied: the app is serving.\n", r.pending())
	for _, h := range r.Heartbeats {
		fmt.Fprintf(&b, "  live backend heartbeat: instance %s on %q, seen %ds ago\n", h.InstanceID, h.Hostname, int64(h.Age/time.Second))
	}
	for _, s := range r.Sessions {
		started := "?"
		if s.Started != nil {
			started = s.Started.UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(&b, "  other database session: pid %d, application_name %q, user %s, client %s, started %s, state %s\n",
			s.PID, s.AppName, s.User, s.Client, started, s.State)
	}
	b.WriteString("Stop every backend first (a graceful stop removes its heartbeat; a killed one stops counting 90 s after it was last seen), close or terminate the sessions listed, then run again. Nothing was changed. See docs/runbook.md, \"Applying migrations: stop, migrate, start\".")
	return b.String()
}

// ServingState reads the live heartbeats (none when the table does not exist
// yet, below 034) and the other sessions of contract 2 on q's connection.
func ServingState(ctx context.Context, q DBTXQuery) ([]LiveBeat, []OtherSession, error) {
	beats := make([]LiveBeat, 0)
	var hasTable bool
	if err := q.QueryRowContext(ctx, `SELECT to_regclass('public.encryption_backend_heartbeats') IS NOT NULL`).Scan(&hasTable); err != nil {
		return nil, nil, err
	}
	if hasTable {
		rows, err := q.QueryContext(ctx, `
			SELECT instance_id, hostname, GREATEST(0, extract(epoch FROM now() - seen_at))::float8
			FROM encryption_backend_heartbeats
			WHERE seen_at > now() - make_interval(secs => $1)
			ORDER BY started_at`, HeartbeatLive.Seconds())
		if err != nil {
			return nil, nil, err
		}
		for rows.Next() {
			var b LiveBeat
			var age float64
			if err := rows.Scan(&b.InstanceID, &b.Hostname, &age); err != nil {
				rows.Close()
				return nil, nil, err
			}
			b.Age = time.Duration(age * float64(time.Second))
			beats = append(beats, b)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, nil, err
		}
	}
	// A NULL backend_type is a session whose details PostgreSQL hides from
	// this role: it counts (fail closed, contract 2, challenge S1).
	rows, err := q.QueryContext(ctx, `
		SELECT pid, COALESCE(application_name, ''), usename, host(client_addr), backend_start, state, backend_type IS NULL
		FROM pg_stat_activity
		WHERE datname = current_database() AND pid <> pg_backend_pid()
		  AND (backend_type = 'client backend' OR backend_type IS NULL)
		  AND COALESCE(application_name, '') NOT LIKE $1
		ORDER BY pid`, migratorPrefix+"%")
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	sessions := make([]OtherSession, 0)
	for rows.Next() {
		var s OtherSession
		var user, client, state sql.NullString
		var started sql.NullTime
		if err := rows.Scan(&s.PID, &s.AppName, &user, &client, &started, &state, &s.Hidden); err != nil {
			return nil, nil, err
		}
		s.User, s.Client, s.State = "?", "?", "?"
		if user.Valid {
			s.User = user.String
		}
		if !s.Hidden {
			s.Client = "local"
			if client.Valid {
				s.Client = client.String
			}
			if state.Valid {
				s.State = state.String
			}
			if started.Valid {
				t := started.Time
				s.Started = &t
			}
		}
		sessions = append(sessions, s)
	}
	return beats, sessions, rows.Err()
}

// MigrateOptions names the caller (application_name "paceday-migrate:
// <Caller>") and, for the encryption subcommands, a floor below which the
// procedure applies nothing (ErrBelowFloor).
type MigrateOptions struct {
	Caller string
	Floor  uint
}

// MigrateResult reports what the procedure did: Applied false is "nothing to
// do". From is the version it found (FromPresent false: none), To the
// version the binary applies up to.
type MigrateResult struct {
	Applied     bool
	From        uint
	FromPresent bool
	To          uint
}

// MigrateWhenStopped is the migrate-when-stopped procedure (contract 3). It
// opens its own one-connection handle named MigratorName(opt.Caller); the
// caller must hold no other open connection to the database (step 2). It
// returns a *MigrationRefusal when the app is serving or the maintenance lock
// is held, an *UnknownVersionError for a version the binary has no file for,
// golang-migrate's dirty error for a dirty version with no migrating holder,
// and ErrBelowFloor below opt.Floor.
func MigrateWhenStopped(ctx context.Context, dsn string, opt MigrateOptions) (MigrateResult, error) {
	ceiling := uint(migrationCeiling.Load()) // one reading per run
	e, err := embeddedVersions()
	if err != nil {
		return MigrateResult{}, err
	}
	highest := e.highestFor(ceiling)
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return MigrateResult{}, fmt.Errorf("open postgres: %w", err)
	}
	cfg.RuntimeParams["application_name"] = MigratorName(opt.Caller)
	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(1)
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return MigrateResult{}, fmt.Errorf("ping postgres: %w", err)
	}
	defer conn.Close()

	// Step 1: the version state, before any lock or check.
	st, err := readState(ctx, conn, e, highest)
	if err != nil {
		return MigrateResult{}, err
	}
	res := MigrateResult{From: st.Version, FromPresent: st.Present, To: highest}
	switch st.Kind {
	case VersionUnknown:
		return res, fmt.Errorf("migrate up: %w", NewUnknownVersionError(st.Version))
	case VersionCurrent:
		return res, nil
	case VersionDirty:
		// Dirty with a migrating holder is a migration in progress; with
		// none it is decided under the lock (step 4).
		h, err := MaintenanceHolder(ctx, conn)
		if err != nil {
			return res, err
		}
		if h.Migrating() {
			return res, &MigrationRefusal{kind: refusalInProgress, From: st.Version, FromPresent: true, To: highest, Holder: h}
		}
	case VersionPending:
		if opt.Floor != 0 && st.Present && st.Version < opt.Floor {
			return res, ErrBelowFloor
		}
	}
	fireMigrateHook(&afterMigrateStateRead, opt.Caller)

	// Step 3: the maintenance lock, on this connection.
	var ok bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1, $2)`, lockClass, lockMaintenance).Scan(&ok); err != nil {
		return res, err
	}
	if !ok {
		fireMigrateHook(&afterMigrateLockRefused, opt.Caller)
		h, _ := MaintenanceHolder(ctx, conn)
		if h.Migrating() && st.Kind == VersionDirty {
			return res, &MigrationRefusal{kind: refusalInProgress, From: st.Version, FromPresent: true, To: highest, Holder: h}
		}
		return res, &MigrationRefusal{kind: refusalLockHeld, From: st.Version, FromPresent: st.Present, To: highest, Holder: h}
	}
	locked := true
	unlock := func() {
		if locked {
			locked = false
			// Bounded: if the server does not answer, closing the
			// handle ends the session, which releases the lock anyway.
			uctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = conn.ExecContext(uctx, `SELECT pg_advisory_unlock($1, $2)`, lockClass, lockMaintenance)
		}
	}
	defer unlock()

	// Step 4: the version again, under the lock.
	st, err = readState(ctx, conn, e, highest)
	if err != nil {
		return res, err
	}
	res.From, res.FromPresent = st.Version, st.Present
	switch st.Kind {
	case VersionUnknown:
		return res, fmt.Errorf("migrate up: %w", NewUnknownVersionError(st.Version))
	case VersionDirty:
		return res, fmt.Errorf("migrate up: %w", migrate.ErrDirty{Version: int(st.Version)})
	case VersionCurrent:
		return res, nil
	}
	if opt.Floor != 0 && st.Present && st.Version < opt.Floor {
		return res, ErrBelowFloor
	}

	// Step 5: is the app serving?
	beats, sessions, err := ServingState(ctx, conn)
	if err != nil {
		return res, err
	}
	if len(beats) > 0 || len(sessions) > 0 {
		return res, &MigrationRefusal{kind: refusalServing, From: st.Version, FromPresent: st.Present, To: highest, Heartbeats: beats, Sessions: sessions}
	}
	fireMigrateHook(&afterMigrateCheck, opt.Caller)

	// Step 6: golang-migrate on this same connection; step 7: unlock.
	if err := applyUp(ctx, conn, ceiling, highest, unlock); err != nil {
		return res, fmt.Errorf("migrate up: %w", err)
	}
	res.Applied = true
	return res, nil
}

// applyUp runs golang-migrate up to highest on conn and calls unlock before
// the driver releases conn.
func applyUp(ctx context.Context, conn *sql.Conn, ceiling, highest uint, unlock func()) error {
	src, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}
	driver, err := postgres.WithConnection(ctx, conn, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("create migration driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", seamSource{src}, "postgres", driver)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	// m.Close closes the driver, which releases conn: unlock first.
	defer m.Close()
	defer unlock()
	if ceiling != 0 {
		err = m.Migrate(highest)
	} else {
		err = m.Up()
	}
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

// seamSource fires the insideMigrationFile seam when golang-migrate reads an
// up file's body: by then the version is marked dirty and the file runs as
// soon as its body is read (PAC-116 AC-19).
type seamSource struct{ source.Driver }

func (s seamSource) ReadUp(version uint) (io.ReadCloser, string, error) {
	r, id, err := s.Driver.ReadUp(version)
	if err != nil {
		return r, id, err
	}
	if fn, ok := insideMigrationFile.get(); ok {
		return &hookedBody{ReadCloser: r, fire: func() { fn(version) }}, id, nil
	}
	return r, id, nil
}

type hookedBody struct {
	io.ReadCloser
	once sync.Once
	fire func()
}

func (b *hookedBody) Read(p []byte) (int, error) {
	b.once.Do(b.fire)
	return b.ReadCloser.Read(p)
}

// LatestMigrationVersion is the highest embedded up migration's version.
func LatestMigrationVersion() (uint, error) {
	e, err := embeddedVersions()
	return e.highest, err
}
