package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Enach/paceday/backend/internal/secretbox"
	"github.com/google/uuid"
)

// PAC-84 contract sections 4 and 5.3: data keys, backend heartbeats, the
// advisory locks, the key load at start and the keyring refresher.

// Advisory lock class and ids (contract 4.3).
const (
	lockClass       = 8400
	lockMaintenance = 1
	lockKeySetup    = 2
)

// AppBackend is the backend pool's application_name (contract 4.3).
const AppBackend = "paceday-backend"

// OperatorApp is the CLI's application_name for an encryption subcommand.
func OperatorApp(subcommand string) string { return "paceday-operator encryption " + subcommand }

// Destructive subcommands: a backend refuses to start while one holds the
// maintenance lock, and a running backend shuts down when it sees one.
var destructiveApps = map[string]string{
	OperatorApp("decrypt-all"):    "decrypt-all",
	OperatorApp("reset-lost-key"): "reset-lost-key",
}

// Timing of contract 4.3.
const (
	HeartbeatInterval = 30 * time.Second
	HeartbeatLive     = 90 * time.Second
	SealLease         = 60 * time.Second
	heartbeatExpiry   = 24 * time.Hour
)

// SealBound is the number of seals after which a data key is rotated
// automatically (contract 4.1, K-8); SealAdvisory flags "rotate due".
const (
	SealBound    int64 = 1 << 30
	SealAdvisory int64 = 1 << 29
)

// Instance identifies one backend process in encryption_backend_heartbeats.
type Instance struct {
	ID       uuid.UUID
	Hostname string
}

// WriteHeartbeat upserts inst's heartbeat with activeDEK (nil: none yet).
func WriteHeartbeat(ctx context.Context, db DBTX, inst Instance, activeDEK *int32) error {
	var dek sql.NullInt32
	if activeDEK != nil {
		dek = sql.NullInt32{Int32: *activeDEK, Valid: true}
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO encryption_backend_heartbeats (instance_id, hostname, active_dek)
		VALUES ($1, $2, $3)
		ON CONFLICT (instance_id) DO UPDATE SET active_dek = EXCLUDED.active_dek, seen_at = now()`,
		inst.ID, inst.Hostname, dek)
	return err
}

// DeleteHeartbeat removes inst's heartbeat (graceful shutdown).
func DeleteHeartbeat(ctx context.Context, db DBTX, inst Instance) error {
	_, err := db.ExecContext(ctx, `DELETE FROM encryption_backend_heartbeats WHERE instance_id = $1`, inst.ID)
	return err
}

// Heartbeat is one row of encryption_backend_heartbeats.
type Heartbeat struct {
	InstanceID uuid.UUID
	Hostname   string
	ActiveDEK  *int32
	StartedAt  time.Time
	SeenAt     time.Time
}

// LiveHeartbeats returns the heartbeats seen within HeartbeatLive.
func LiveHeartbeats(ctx context.Context, db DBTX) ([]Heartbeat, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT instance_id, hostname, active_dek, started_at, seen_at
		FROM encryption_backend_heartbeats
		WHERE seen_at > now() - make_interval(secs => $1)
		ORDER BY started_at`, HeartbeatLive.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Heartbeat, 0)
	for rows.Next() {
		var h Heartbeat
		var dek sql.NullInt32
		if err := rows.Scan(&h.InstanceID, &h.Hostname, &dek, &h.StartedAt, &h.SeenAt); err != nil {
			return nil, err
		}
		if dek.Valid {
			d := dek.Int32
			h.ActiveDEK = &d
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// LockHolder is the process holding the maintenance lock.
type LockHolder struct {
	AppName      string
	PID          int
	BackendStart time.Time
}

// MaintenanceHolder returns the holder of the maintenance lock, nil when
// none holds it.
func MaintenanceHolder(ctx context.Context, db DBTX) (*LockHolder, error) {
	var h LockHolder
	err := db.QueryRowContext(ctx, `
		SELECT COALESCE(a.application_name, ''), a.pid, COALESCE(a.backend_start, now())
		FROM pg_locks l JOIN pg_stat_activity a ON a.pid = l.pid
		WHERE l.locktype = 'advisory' AND l.classid = $1 AND l.objid = $2 AND l.granted
		LIMIT 1`, lockClass, lockMaintenance).Scan(&h.AppName, &h.PID, &h.BackendStart)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &h, nil
}

// DestructiveSubcommand names the destructive subcommand h runs, "" if none.
func (h *LockHolder) DestructiveSubcommand() string {
	if h == nil {
		return ""
	}
	return destructiveApps[h.AppName]
}

// MaintenanceLock is the session advisory lock (8400,1) held on one
// dedicated connection for a whole run.
type MaintenanceLock struct{ conn *sql.Conn }

// ErrLockHeld: the maintenance lock is held by another process.
var ErrLockHeld = errors.New("maintenance lock held")

// LockHeldError names the holder.
type LockHeldError struct{ Holder *LockHolder }

func (e *LockHeldError) Error() string {
	if e.Holder == nil {
		return "the encryption maintenance lock is held by another process"
	}
	return fmt.Sprintf("the encryption maintenance lock is held by %s (pid %d, started %s)",
		e.Holder.AppName, e.Holder.PID, e.Holder.BackendStart.UTC().Format(time.RFC3339))
}

func (e *LockHeldError) Is(t error) bool { return t == ErrLockHeld }

// TryMaintenanceLock takes the maintenance lock or returns a *LockHeldError.
func TryMaintenanceLock(ctx context.Context, db *sql.DB) (*MaintenanceLock, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	var ok bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1, $2)`, lockClass, lockMaintenance).Scan(&ok); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if !ok {
		_ = conn.Close()
		holder, _ := MaintenanceHolder(ctx, db)
		return nil, &LockHeldError{Holder: holder}
	}
	return &MaintenanceLock{conn: conn}, nil
}

// Release releases the lock and its connection.
func (l *MaintenanceLock) Release() {
	if l == nil || l.conn == nil {
		return
	}
	_, _ = l.conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1, $2)`, lockClass, lockMaintenance)
	_ = l.conn.Close()
	l.conn = nil
}

// keySetupLock takes the transaction lock (8400,2) inside tx.
func keySetupLock(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, lockClass, lockKeySetup)
	return err
}

// LoadDataKeys reads every data key.
func LoadDataKeys(ctx context.Context, q DBTXQuery) ([]secretbox.WrappedDEK, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, state, kek_id, wrapped FROM encryption_data_keys ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]secretbox.WrappedDEK, 0)
	for rows.Next() {
		var w secretbox.WrappedDEK
		if err := rows.Scan(&w.ID, &w.State, &w.KEKID, &w.Wrapped); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// DBTXQuery is the query part of *sql.DB, *sql.Tx and *sql.Conn.
type DBTXQuery interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// insertDataKey creates a new active data key wrapped by kek, in tx.
func insertDataKey(ctx context.Context, tx *sql.Tx, kek secretbox.KEK) (secretbox.WrappedDEK, error) {
	var id int32
	if err := tx.QueryRowContext(ctx, `SELECT nextval('encryption_data_keys_id_seq')::integer`).Scan(&id); err != nil {
		return secretbox.WrappedDEK{}, err
	}
	w, err := secretbox.NewDEK(id, kek)
	if err != nil {
		return secretbox.WrappedDEK{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO encryption_data_keys (id, state, kek_id, wrapped) VALUES ($1, 'active', $2, $3)`,
		w.ID, w.KEKID, w.Wrapped); err != nil {
		return secretbox.WrappedDEK{}, err
	}
	return w, nil
}

// KeyLoad is what LoadKeyring did (contract 9.4 start line).
type KeyLoad struct {
	Keyring   *secretbox.Keyring
	DataKeys  int
	Active    int32
	Created   bool
	Rewrapped int
	// UnusedPrevious are previous key ids wrapping no data key.
	UnusedPrevious []string
}

// ErrOfflineOperation: a destructive operator command holds the
// maintenance lock (contract 4.3).
var ErrOfflineOperation = errors.New("offline encryption operation running")

// PrepareKeyring is the key setup of contract 5.3 step 4 without the
// heartbeat: under lock (8400,2), in one transaction, create the first data
// key if none is active, re-wrap every data key wrapped by a previous key
// under the active key (D-6), unwrap every data key. Any failure changes
// nothing. createIfMissing false (operator reset checks) never creates.
func PrepareKeyring(ctx context.Context, db *sql.DB, cfg secretbox.KeyConfig, createIfMissing bool) (*KeyLoad, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := keySetupLock(ctx, tx); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM encryption_backend_heartbeats WHERE seen_at < now() - make_interval(secs => $1)`, heartbeatExpiry.Seconds()); err != nil {
		return nil, err
	}
	deks, err := LoadDataKeys(ctx, tx)
	if err != nil {
		return nil, err
	}
	load := &KeyLoad{}
	hasActive := false
	for _, w := range deks {
		if w.State == "active" {
			hasActive = true
		}
	}
	if !hasActive && createIfMissing {
		w, err := insertDataKey(ctx, tx, cfg.Active)
		if err != nil {
			return nil, err
		}
		deks = append(deks, w)
		load.Created = true
	}
	for i, w := range deks {
		if w.KEKID == cfg.Active.ID {
			continue
		}
		nw, err := secretbox.Rewrap(w, cfg)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE encryption_data_keys SET kek_id = $2, wrapped = $3, wrapped_at = now() WHERE id = $1`,
			nw.ID, nw.KEKID, nw.Wrapped); err != nil {
			return nil, err
		}
		deks[i] = nw
		load.Rewrapped++
	}
	k, err := secretbox.NewKeyring(cfg, deks)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	load.Keyring, load.DataKeys, load.Active = k, len(deks), k.ActiveDEK()
	load.UnusedPrevious = cfg.PreviousIDs() // every data key is now wrapped by the active key
	return load, nil
}

// LoadKeyring is contract 5.3 step 4 for a backend: the heartbeat first
// (active_dek NULL), then the offline-operation check, then PrepareKeyring,
// then the heartbeat with the active id.
func LoadKeyring(ctx context.Context, db *sql.DB, cfg secretbox.KeyConfig, inst Instance) (*KeyLoad, error) {
	if err := WriteHeartbeat(ctx, db, inst, nil); err != nil {
		return nil, fmt.Errorf("write heartbeat: %w", err)
	}
	holder, err := MaintenanceHolder(ctx, db)
	if err != nil {
		return nil, err
	}
	if sub := holder.DestructiveSubcommand(); sub != "" {
		return nil, fmt.Errorf("%w: paceday-operator encryption %s is running (pid %d); start the backend after it ends",
			ErrOfflineOperation, sub, holder.PID)
	}
	load, err := PrepareKeyring(ctx, db, cfg, true)
	if err != nil {
		return nil, err
	}
	active := load.Active
	if err := WriteHeartbeat(ctx, db, inst, &active); err != nil {
		return nil, fmt.Errorf("write heartbeat: %w", err)
	}
	return load, nil
}

// RotateStep1 is rotate-data-key step 1 (contract 8.2) and the automatic
// step of contract 4.1: under lock (8400,2), in one transaction, if retire
// is the active data key, retire it and create a new active data key
// wrapped by cfg.Active. It reports the new id, or 0 when retire was not
// active (nothing to do). onlyIfBound: the automatic step, which acts only
// while the active key's sealed_count is still >= SealBound.
func RotateStep1(ctx context.Context, db *sql.DB, cfg secretbox.KeyConfig, retire int32, onlyIfBound bool) (int32, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := keySetupLock(ctx, tx); err != nil {
		return 0, err
	}
	var state string
	var count int64
	err = tx.QueryRowContext(ctx, `SELECT state, sealed_count FROM encryption_data_keys WHERE id = $1`, retire).Scan(&state, &count)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("data key %d does not exist", retire)
	}
	if err != nil {
		return 0, err
	}
	if state != "active" || (onlyIfBound && count < SealBound) {
		return 0, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE encryption_data_keys SET state = 'retired', retired_at = now() WHERE id = $1`, retire); err != nil {
		return 0, err
	}
	w, err := insertDataKey(ctx, tx, cfg.Active)
	if err != nil {
		return 0, err
	}
	return w.ID, tx.Commit()
}

// ActiveDataKey returns the active data key's id, kek_id and sealed_count.
func ActiveDataKey(ctx context.Context, q DBTXQuery) (id int32, kekID string, sealed int64, err error) {
	err = q.QueryRowContext(ctx, `SELECT id, kek_id, sealed_count FROM encryption_data_keys WHERE state = 'active'`).Scan(&id, &kekID, &sealed)
	return id, kekID, sealed, err
}

// FlushSealCounts adds counts to sealed_count (contract 4.1).
func FlushSealCounts(ctx context.Context, db DBTX, counts map[int32]int64) error {
	for id, n := range counts {
		if _, err := db.ExecContext(ctx, `UPDATE encryption_data_keys SET sealed_count = sealed_count + $2 WHERE id = $1`, id, n); err != nil {
			return err
		}
	}
	return nil
}

// Lease is a process's seal lease (contract 4.3, K-4): Seal is refused while
// the last heartbeat upsert that succeeded was sent more than SealLease ago.
type Lease struct {
	mu       sync.Mutex
	lastSent time.Time // carries the monotonic clock
	now      func() time.Time
	paused   bool
	Logf     func(format string, args ...any)
}

// NewLease starts a lease renewed at sent.
func NewLease(sent time.Time) *Lease { return &Lease{lastSent: sent, now: time.Now} }

// Renew records a heartbeat sent at sent that succeeded.
func (l *Lease) Renew(sent time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if sent.After(l.lastSent) {
		l.lastSent = sent
	}
	l.paused = false
}

// Check is the seal gate: nil while the lease holds, else ErrSealPaused.
func (l *Lease) Check() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	d := l.now().Sub(l.lastSent)
	if d <= SealLease {
		return nil
	}
	if !l.paused && l.Logf != nil {
		l.Logf("encryption: heartbeat not written for %s; sealing paused", d.Truncate(time.Second))
	}
	l.paused = true
	return secretbox.ErrSealPaused
}

// Refresher is the keyring refresher of contract 5.3 step 7.
type Refresher struct {
	DB    *sql.DB
	Cfg   secretbox.KeyConfig
	Inst  Instance
	Lease *Lease
	Logf  func(format string, args ...any)
	// Shutdown is called (once) when the process must stop: its active data
	// key no longer exists, or a destructive subcommand holds the
	// maintenance lock (contract 4.3).
	Shutdown func(reason string)

	shutdownOnce sync.Once
}

func (r *Refresher) logf(format string, args ...any) {
	if r.Logf != nil {
		r.Logf(format, args...)
	}
}

func (r *Refresher) stop(reason string) {
	r.shutdownOnce.Do(func() {
		secretbox.SetSealGate(func() error { return secretbox.ErrSealPaused })
		r.logf("%s", reason)
		if r.Shutdown != nil {
			r.Shutdown(reason)
		}
	})
}

// Tick runs one refresher step: reload the data keys, unwrap only new ones,
// Install the new keyring, flush sealed_count, apply the automatic rotation
// step, then upsert the heartbeat with the active id (Install before the
// heartbeat, K-5). It returns false when the process must shut down.
func (r *Refresher) Tick(ctx context.Context) bool {
	cur := secretbox.Current()
	if cur == nil {
		return true
	}
	holder, err := MaintenanceHolder(ctx, r.DB)
	if err == nil {
		if sub := holder.DestructiveSubcommand(); sub != "" {
			r.stop(fmt.Sprintf("encryption: data key %d no longer exists (or %s holds the maintenance lock); shutting down", cur.ActiveDEK(), sub))
			return false
		}
	}
	deks, err := LoadDataKeys(ctx, r.DB)
	if err != nil {
		r.logf("encryption: refresher: read data keys: %v", err)
		r.beat(ctx, cur.ActiveDEK())
		return true
	}
	found := false
	for _, w := range deks {
		if w.ID == cur.ActiveDEK() {
			found = true
		}
	}
	if !found {
		r.stop(fmt.Sprintf("encryption: data key %d no longer exists (or decrypt-all/reset-lost-key holds the maintenance lock); shutting down", cur.ActiveDEK()))
		return false
	}
	next, err := cur.With(r.Cfg, deks)
	if err != nil {
		// A data key this process cannot unwrap: keep the current keyring
		// and do not advance active_dek (contract 5.3, K-6).
		r.logf("%s", "encryption: "+keyErrorText(err)+" "+secretbox.RunbookRef)
	} else if next.ActiveDEK() != cur.ActiveDEK() || len(next.IDs()) != len(cur.IDs()) {
		secretbox.Install(next)
		cur = next
	}
	if counts := cur.TakeSealCounts(); len(counts) > 0 {
		if err := FlushSealCounts(ctx, r.DB, counts); err != nil {
			cur.RestoreSealCounts(counts)
			r.logf("encryption: refresher: flush sealed_count: %v", err)
		}
	}
	if _, kek, sealed, err := ActiveDataKey(ctx, r.DB); err == nil && sealed >= SealBound && kek == r.Cfg.Active.ID {
		old := cur.ActiveDEK()
		if id, err := RotateStep1(ctx, r.DB, r.Cfg, old, true); err != nil {
			r.logf("encryption: automatic data key rotation: %v", err)
		} else if id != 0 {
			r.logf("encryption: data key %d reached %d seals; data key %d is now active", old, sealed, id)
			if deks, err := LoadDataKeys(ctx, r.DB); err == nil {
				if n, err := cur.With(r.Cfg, deks); err == nil {
					secretbox.Install(n)
					cur = n
				}
			}
		}
	}
	r.beat(ctx, cur.ActiveDEK())
	return true
}

func (r *Refresher) beat(ctx context.Context, active int32) {
	sent := time.Now()
	if err := WriteHeartbeat(ctx, r.DB, r.Inst, &active); err != nil {
		r.logf("encryption: heartbeat: %v", err)
		return
	}
	if r.Lease != nil {
		r.Lease.Renew(sent)
	}
}

// keyErrorText strips a sentinel prefix from a key error.
func keyErrorText(err error) string {
	msg := err.Error()
	for _, s := range []error{secretbox.ErrWrongKey, secretbox.ErrUnknownKEK, secretbox.ErrNoActiveDEK} {
		if p := s.Error() + ": "; strings.HasPrefix(msg, p) {
			return msg[len(p):]
		}
	}
	return msg
}
