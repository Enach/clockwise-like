package storage

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// PAC-136: a user's habit reoptimize lock is exclusive per user, waiting
// ends with the context, other users are not held up, and the release frees
// it for the next run.
func TestPAC136_HabitReoptimizeLock(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	alice, bob := uuid.New(), uuid.New()

	release, err := LockHabitReoptimize(ctx, db, alice)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}

	short, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if again, err := LockHabitReoptimize(short, db, alice); err == nil {
		if relErr := again(); relErr != nil {
			t.Errorf("release: %v", relErr)
		}
		t.Fatal("a second lock of the same user was granted while the first is held")
	}

	if habitLockKey(alice) != habitLockKey(bob) {
		releaseBob, err := LockHabitReoptimize(ctx, db, bob)
		if err != nil {
			t.Fatalf("another user's lock: %v", err)
		}
		if err := releaseBob(); err != nil {
			t.Fatalf("release another user's lock: %v", err)
		}
	}

	if err := release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	next, err := LockHabitReoptimize(ctx, db, alice)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	if err := next(); err != nil {
		t.Fatalf("release: %v", err)
	}

	var held int
	if err := db.QueryRow(`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND classid = $1 AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`, uint32(HabitLockClass)).Scan(&held); err != nil || held != 0 {
		t.Fatalf("%d locks of the class left, %v; want none", held, err)
	}
}

// PAC-136 review L1: discarding a connection ends its session (it is not
// returned to the pool) and is not reported as a failure.
func TestPAC136_DiscardConnEndsTheSession(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	if err := conn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if err := discardConn(conn); err != nil {
		t.Fatalf("discardConn = %v, want nil", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var alive int
		if err := db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE pid = $1`, pid).Scan(&alive); err != nil {
			t.Fatal(err)
		}
		if alive == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the discarded session is still open")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
