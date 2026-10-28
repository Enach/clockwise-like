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
// the account's current one is unauthenticated. Two checks enforce it:
//
//   - requireAuth reads the generation, uncached, with one plain query (no
//     transaction is held while the handler runs);
//   - the IN-REQUEST RE-CHECK: every read or write of the session account's
//     data (contract §5 D1–D14) runs in one transaction whose first statement
//     re-reads the generation under a row lock (FOR KEY SHARE). Every
//     generation advance (a binding, an operator step) locks the same row FOR
//     UPDATE, so no advance can commit between the comparison and the read or
//     write. A mismatch marks the request's guard stale; requireAuth then
//     answers the 401.

// Querier is what both a *sql.DB and a *sql.Tx offer, so a storage function
// that takes one can run inside a caller's transaction.
type Querier interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ErrStaleSession means the session's generation is no longer the account's
// current one, or the account no longer exists. The request is answered as
// unauthenticated (401).
var ErrStaleSession = errors.New("session generation is not the account's current one")

// ErrGenerationUnavailable means the account's current generation could not
// be read. It is a server error (500), never a 401: the web app treats a 401
// as "signed out", and an outage must not look like one (spec AC-48).
var ErrGenerationUnavailable = errors.New("session generation could not be read")

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

// lockAccountExclusive is the first step of every generation advance: it
// locks the account row FOR UPDATE (waiting at most lock_timeout for a
// re-check or a write in progress on the account), so no in-request
// re-check can pass between the advance's checks and its commit.
func lockAccountExclusive(tx *sql.Tx, userID uuid.UUID) error {
	if _, err := tx.Exec(`SET LOCAL lock_timeout = '10s'`); err != nil {
		return err
	}
	var id uuid.UUID
	err := tx.QueryRow(`SELECT id FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
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

// RecheckSessionGeneration is the in-request re-check run as a statement of
// the caller's own transaction tx: when ctx carries a session guard, it locks
// the guard's account row (FOR KEY SHARE) and confirms its generation is the
// guard's. Storage functions that open their own transaction call it first.
func RecheckSessionGeneration(ctx context.Context, tx Querier) error {
	g := SessionGuardFrom(ctx)
	if g == nil {
		return nil
	}
	return recheck(ctx, tx, g.UserID, g.Gen, g)
}

func recheck(ctx context.Context, tx Querier, userID uuid.UUID, gen int64, g *SessionGuard) error {
	var cur int64
	err := tx.QueryRowContext(ctx, `SELECT session_generation FROM users WHERE id = $1 FOR KEY SHARE`, userID).Scan(&cur)
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
	return nil
}

// WithSessionGeneration runs fn for the request in ctx. When ctx carries a
// session guard, fn runs inside one transaction whose first statement is the
// re-check; on a mismatch fn does not run, the guard is marked stale and
// ErrStaleSession is returned; if the generation cannot be read the guard is
// marked unavailable. Without a guard (no session: scheduled jobs, public
// pages, other accounts' data) fn runs directly against db.
func WithSessionGeneration(ctx context.Context, db *sql.DB, fn func(q Querier) error) error {
	g := SessionGuardFrom(ctx)
	if g == nil {
		return fn(db)
	}
	if GuardedCallHook != nil {
		GuardedCallHook(ctx)
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
	if err := recheck(ctx, tx, userID, gen, g); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// txScope is a transaction a storage function either owns (it was given a
// *sql.DB) or joins (it was given the caller's *sql.Tx, typically the
// in-request re-check's): Commit and Rollback act only on an owned one.
type txScope struct {
	tx    *sql.Tx
	owned bool
}

// beginOn starts a transaction on q, or joins q when it is one.
func beginOn(q Querier) (*sql.Tx, *txScope, error) {
	switch v := q.(type) {
	case *sql.Tx:
		return v, &txScope{tx: v}, nil
	case *sql.DB:
		tx, err := v.Begin()
		if err != nil {
			return nil, nil, err
		}
		return tx, &txScope{tx: tx, owned: true}, nil
	}
	return nil, nil, fmt.Errorf("beginOn: unsupported %T", q)
}

func (s *txScope) Commit() error {
	if s.owned {
		return s.tx.Commit()
	}
	return nil
}

func (s *txScope) Rollback() error {
	if s.owned {
		return s.tx.Rollback()
	}
	return nil
}

// GuardedCallHook is a deterministic test seam (PAC-71 review of #242): it
// runs before every in-request re-check of a request with a session, so a
// test can commit a binding between a handler's first and second storage
// call, or after it spawned background work. Production leaves it nil.
var GuardedCallHook func(ctx context.Context)
