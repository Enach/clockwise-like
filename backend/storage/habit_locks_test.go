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
	if err := db.QueryRow(`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND classid = $1`, uint32(HabitLockClass)).Scan(&held); err != nil || held != 0 {
		t.Fatalf("%d locks of the class left, %v; want none", held, err)
	}
}
