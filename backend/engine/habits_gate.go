package engine

// PAC-136 (review B1): the in-process gate in front of a habit reoptimize
// run's database lock. A run holds two pool connections while it works: the
// dedicated one that holds the user's advisory lock, and one for its reads
// and writes. Unbounded, enough concurrent runs (one user calling reoptimize
// repeatedly, or many users at once) held every pool connection in the lock
// or its wait, and the holder then waited forever for a connection for its
// reads: the whole backend's database access stopped.
//
// The gate removes that: a run waits here, holding no connection, for
//   - its user's turn: at most one run per user runs and one waits per
//     process; a further request while one waits is dropped (the waiting run
//     reads the latest state when it starts), and
//   - one of K process-wide run slots, K well below half the pool, so the
//     runs of one process hold at most 2K connections.
// The Postgres lock still orders runs across replicas; across replicas a run
// may hold its lock connection while it waits, which the K bound covers.

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/google/uuid"
)

// habitRunTimeout bounds one reoptimize run, waits included, so nothing waits
// forever and a cancelled run frees its connections.
const habitRunTimeout = 10 * time.Minute

// habitRunSlotsMax is K's ceiling.
const habitRunSlotsMax = 4

// habitRunSlots is K for a pool: a quarter of its open-connection limit,
// between 1 and habitRunSlotsMax (habitRunSlotsMax when unlimited), so the
// runs hold at most half the pool (MaxOpen 20 gives K=4, 8 connections).
func habitRunSlots(maxOpen int) int {
	if maxOpen <= 0 {
		return habitRunSlotsMax
	}
	k := maxOpen / 4
	if k < 1 {
		return 1
	}
	if k > habitRunSlotsMax {
		return habitRunSlotsMax
	}
	return k
}

// habitGate is one process's gate for one database (one pool).
type habitGate struct {
	slots chan struct{}

	mu    sync.Mutex
	users map[uuid.UUID]*habitUserTurn
}

// habitUserTurn is a user's turn token and how many runs hold or wait for it.
type habitUserTurn struct {
	token   chan struct{}
	pending int
}

var habitGates sync.Map // *sql.DB -> *habitGate

// habitGateFor returns db's gate, sizing it from db's pool on first use.
func habitGateFor(db *sql.DB) *habitGate {
	if g, ok := habitGates.Load(db); ok {
		return g.(*habitGate)
	}
	g, _ := habitGates.LoadOrStore(db, &habitGate{
		slots: make(chan struct{}, habitRunSlots(db.Stats().MaxOpenConnections)),
		users: map[uuid.UUID]*habitUserTurn{},
	})
	return g.(*habitGate)
}

// enter waits for the user's turn and a run slot. run is false when the
// request was coalesced into the run already waiting for this user. On
// success the caller must call leave.
func (g *habitGate) enter(ctx context.Context, userID uuid.UUID) (leave func(), run bool, err error) {
	g.mu.Lock()
	turn := g.users[userID]
	if turn == nil {
		turn = &habitUserTurn{token: make(chan struct{}, 1)}
		g.users[userID] = turn
	}
	if turn.pending >= 2 {
		g.mu.Unlock()
		return nil, false, nil
	}
	turn.pending++
	g.mu.Unlock()

	done := func() {
		g.mu.Lock()
		turn.pending--
		if turn.pending == 0 {
			delete(g.users, userID)
		}
		g.mu.Unlock()
	}
	select {
	case turn.token <- struct{}{}:
	case <-ctx.Done():
		done()
		return nil, false, ctx.Err()
	}
	select {
	case g.slots <- struct{}{}:
	case <-ctx.Done():
		<-turn.token
		done()
		return nil, false, ctx.Err()
	}
	return func() {
		<-g.slots
		<-turn.token
		done()
	}, true, nil
}

// inUse is how many run slots are held (tests).
func (g *habitGate) inUse() int { return len(g.slots) }
