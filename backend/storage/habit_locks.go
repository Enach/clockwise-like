package storage

// PAC-136: one habit reoptimize per user at a time. Habit create, update and
// the reoptimize endpoint each start a run; two that overlapped each placed
// an event for the same (habit, day), and the row kept only one of them.
//
// The lock is a session-level advisory lock held on one dedicated connection
// for the whole run, not a transaction lock: a run makes Google calls for up
// to 14 days, and holding a transaction open across them would keep a
// connection idle in transaction (holding back vacuum) for nothing, since the
// run's writes go through the pool. A session lock still dies with its
// connection, so a crashed replica cannot leave it held, and Postgres orders
// every replica's runs, not just this process's.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// HabitLockClass is the advisory lock class of PAC-136 (two-integer form,
// pg_locks objsubid = 2). Disjoint from PAC-82's 8201-8203 and PAC-84's 8400.
const HabitLockClass int32 = 13601

// habitLockKey is the objid of a user's reoptimize lock: the first 4 bytes of
// SHA-256 of the user id, big-endian, as a signed int32. Two users that share
// a key only queue behind each other.
func habitLockKey(userID uuid.UUID) int32 {
	sum := sha256.Sum256([]byte("habit-reoptimize:" + userID.String()))
	return int32(binary.BigEndian.Uint32(sum[:4]))
}

// habitUnlockTimeout bounds the unlock, which runs on its own context so a
// cancelled run still releases its lock.
const habitUnlockTimeout = 10 * time.Second

// LockHabitReoptimize waits for the user's reoptimize lock and returns the
// function that releases it. Waiting ends with ctx. The release unlocks and
// returns the connection; when the unlock cannot be confirmed the connection
// is discarded instead of returned to the pool, which ends its session and so
// the lock.
func LockHabitReoptimize(ctx context.Context, db *sql.DB, userID uuid.UUID) (release func() error, err error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("habit lock: connection: %w", err)
	}
	key := habitLockKey(userID)
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1, $2)`, HabitLockClass, key); err != nil {
		return nil, errors.Join(fmt.Errorf("habit lock: %w", err), discardConn(conn))
	}
	return func() error {
		uctx, cancel := context.WithTimeout(context.Background(), habitUnlockTimeout)
		defer cancel()
		var released bool
		err := conn.QueryRowContext(uctx, `SELECT pg_advisory_unlock($1, $2)`, HabitLockClass, key).Scan(&released)
		if err == nil && !released {
			err = errors.New("lock was not held")
		}
		if err != nil {
			return errors.Join(fmt.Errorf("habit unlock: %w", err), discardConn(conn))
		}
		return conn.Close()
	}, nil
}

// discardConn closes conn without returning its session to the pool: a
// session that may still hold (or be waiting for) the lock must end.
// After Raw returns driver.ErrBadConn, database/sql has already closed the
// connection, so Close's sql.ErrConnDone is expected and not a failure.
func discardConn(conn *sql.Conn) error {
	rawErr := conn.Raw(func(any) error { return driver.ErrBadConn })
	closeErr := conn.Close()
	if errors.Is(closeErr, sql.ErrConnDone) {
		closeErr = nil
	}
	if rawErr != nil && !errors.Is(rawErr, driver.ErrBadConn) {
		return errors.Join(rawErr, closeErr)
	}
	return closeErr
}
