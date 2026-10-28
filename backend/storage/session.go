package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/google/uuid"
)

// PAC-71 session generation (spec docs/specs/PAC-71.md §3 "Sessions", D-20;
// contract contracts/features/PAC-71.md §5; plan docs/plans/PAC-71.md §3).
//
// Every account has a session generation. A session carries the generation of
// its account when it was issued; a request whose session generation is not
// the account's current one is unauthenticated. Two mechanisms enforce it:
//
//   - the request LEASE (BeginSessionLease): requireAuth holds a shared
//     advisory lock on the account for the whole request, and every generation
//     advance (a binding, an operator step) takes the same lock exclusively.
//     So no advance can commit while a request authenticated under the
//     previous generation is still running, and a request that arrives while
//     an advance waits queues behind it and then reads the new generation;
//   - the IN-REQUEST RE-CHECK (WithSessionGeneration): a load or write of the
//     session account's data runs in one transaction whose first statement
//     re-reads the generation under a row lock, so no advance can commit
//     between the comparison and the load or write. A mismatch marks the
//     request's guard stale; requireAuth then answers the 401.

// Querier is what both a *sql.DB and a *sql.Tx offer, so a storage function
// that takes one can run inside a caller's transaction.
type Querier interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// ErrStaleSession means the session's generation is no longer the account's
// current one, or the account no longer exists. The request is answered as
// unauthenticated (401).
var ErrStaleSession = errors.New("session generation is not the account's current one")

// ErrGenerationUnavailable means the account's current generation could not
// be read. It is a server error (500), never a 401: the web app treats a 401
// as "signed out", and an outage must not look like one (spec AC-48).
var ErrGenerationUnavailable = errors.New("session generation could not be read")

// accountLockClass namespaces PAC-71's per-account advisory locks
// (pg_advisory_xact_lock*(class, hashtext(user id))). A hash collision between
// two accounts only makes one wait for the other; it never lets anything
// through.
const accountLockClass = 71071

// SessionGuard is the request's session as requireAuth validated it: the
// account and the generation its token carries. It records whether an
// in-request re-check found the generation moved (stale) or unreadable.
type SessionGuard struct {
	UserID      uuid.UUID
	Gen         int64
	stale       atomic.Bool
	unavailable atomic.Bool
}

// MarkStale records that the session is no longer current.
func (g *SessionGuard) MarkStale() { g.stale.Store(true) }

// MarkUnavailable records that the generation could not be read.
func (g *SessionGuard) MarkUnavailable() { g.unavailable.Store(true) }

// Stale reports whether an in-request re-check found the session stale.
func (g *SessionGuard) Stale() bool { return g != nil && g.stale.Load() }

// Unavailable reports whether an in-request re-check could not read the
// generation.
func (g *SessionGuard) Unavailable() bool { return g != nil && g.unavailable.Load() }

type sessionGuardKey struct{}

// WithSessionGuard returns ctx carrying g.
func WithSessionGuard(ctx context.Context, g *SessionGuard) context.Context {
	return context.WithValue(ctx, sessionGuardKey{}, g)
}

// SessionGuardFrom returns the request's guard, or nil when the context
// carries no authenticated session (scheduled jobs, public pages).
func SessionGuardFrom(ctx context.Context) *SessionGuard {
	g, _ := ctx.Value(sessionGuardKey{}).(*SessionGuard)
	return g
}

// SessionLease is the request lease of the package comment. Release it when
// the request ends.
type SessionLease struct{ tx *sql.Tx }

// Release ends the lease. It is safe on a nil lease and idempotent.
func (l *SessionLease) Release() {
	if l != nil && l.tx != nil {
		_ = l.tx.Rollback()
		l.tx = nil
	}
}

// BeginSessionLease takes userID's lease and reads its current generation,
// uncached. found is false when no account row exists (the lease is then
// already released). Any failure wraps ErrGenerationUnavailable.
//
// The lease is not tied to ctx's cancellation: a client that disconnects must
// not end it while the handler still runs, or an advance could commit in the
// middle of the request.
func BeginSessionLease(ctx context.Context, db *sql.DB, userID uuid.UUID) (*SessionLease, int64, bool, error) {
	tx, err := db.BeginTx(context.WithoutCancel(ctx), nil)
	if err != nil {
		return nil, 0, false, fmt.Errorf("%w: %w", ErrGenerationUnavailable, err)
	}
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock_shared($1, hashtext($2))`, accountLockClass, userID.String()); err != nil {
		_ = tx.Rollback()
		return nil, 0, false, fmt.Errorf("%w: %w", ErrGenerationUnavailable, err)
	}
	var gen int64
	err = tx.QueryRow(`SELECT session_generation FROM users WHERE id = $1`, userID).Scan(&gen)
	if errors.Is(err, sql.ErrNoRows) {
		_ = tx.Rollback()
		return nil, 0, false, nil
	}
	if err != nil {
		_ = tx.Rollback()
		return nil, 0, false, fmt.Errorf("%w: %w", ErrGenerationUnavailable, err)
	}
	return &SessionLease{tx: tx}, gen, true, nil
}

// lockAccountExclusive is the first step of every generation advance: it waits
// for every request lease on the account to end, then locks the account row.
// It must be the first lock the transaction takes.
func lockAccountExclusive(tx *sql.Tx, userID uuid.UUID) error {
	if _, err := tx.Exec(`SET LOCAL lock_timeout = '10s'`); err != nil {
		return err
	}
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1, hashtext($2))`, accountLockClass, userID.String()); err != nil {
		return err
	}
	return nil
}

// CurrentGeneration reads userID's generation, uncached. ok is false when no
// account row exists.
func CurrentGeneration(q Querier, userID uuid.UUID) (gen int64, ok bool, err error) {
	err = q.QueryRow(`SELECT session_generation FROM users WHERE id = $1`, userID).Scan(&gen)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return gen, true, nil
}

// WithSessionGeneration runs fn for the request in ctx. When ctx carries a
// session guard, fn runs inside one transaction whose first statement
// re-confirms, under a row lock, that the guard's account still exists and is
// still at the guard's generation (the in-request re-check). On a mismatch fn
// does not run, the guard is marked stale and ErrStaleSession is returned; if
// the generation cannot be read the guard is marked unavailable. Without a
// guard (no session: scheduled jobs, public pages, other accounts' data) fn
// runs directly against db.
//
// The re-check uses a row lock (FOR KEY SHARE), not the advisory lock: it
// runs while this request already holds its lease, and an advisory request
// would queue behind an advance that is itself waiting for that lease.
func WithSessionGeneration(ctx context.Context, db *sql.DB, fn func(q Querier) error) error {
	g := SessionGuardFrom(ctx)
	if g == nil {
		return fn(db)
	}
	return withGenerationTx(ctx, db, g.UserID, g.Gen, g, fn)
}

// WithGeneration runs fn in one transaction that first re-confirms, under a
// row lock, that userID is still at generation gen. It is the store step of a
// connect (contract §4 step 6, spec AC-47): ErrStaleSession when it moved.
func WithGeneration(ctx context.Context, db *sql.DB, userID uuid.UUID, gen int64, fn func(q Querier) error) error {
	return withGenerationTx(ctx, db, userID, gen, nil, fn)
}

func withGenerationTx(ctx context.Context, db *sql.DB, userID uuid.UUID, gen int64, g *SessionGuard, fn func(q Querier) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		if g != nil {
			g.MarkUnavailable()
		}
		return fmt.Errorf("%w: %w", ErrGenerationUnavailable, err)
	}
	defer func() { _ = tx.Rollback() }()
	var cur int64
	err = tx.QueryRow(`SELECT session_generation FROM users WHERE id = $1 FOR KEY SHARE`, userID).Scan(&cur)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if g != nil {
			g.MarkStale()
		}
		return ErrStaleSession
	case err != nil:
		if g != nil {
			g.MarkUnavailable()
		}
		return fmt.Errorf("%w: %w", ErrGenerationUnavailable, err)
	case cur != gen:
		if g != nil {
			g.MarkStale()
		}
		return ErrStaleSession
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
