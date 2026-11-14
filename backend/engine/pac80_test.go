package engine

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// PAC-80 item 2 (spec docs/specs/PAC-80.md AC-6, AC-7): team detection
// creates the manager's default profile row under the session re-check when
// it runs for a request, and as before when the scheduled job runs it.

func pac80ProfileCount(t *testing.T, db storage.DBTX, u uuid.UUID) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM user_profiles WHERE user_id = $1`, u).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// AC-6: the session goes stale at the detection's profile step (its second
// re-check, after the credential load and the calendar read).
func TestPAC80_DetectionForAStaleRequestWritesNoProfileRow(t *testing.T) {
	db := openTestDB(t)
	stub := newGoogleStub(t)
	u, _ := pac81User(t, db, "pac80-detect-stale@example.com")
	var calls atomic.Int64
	storage.InstallGuardedCallHook(t, func(context.Context) {
		if calls.Add(1) == 2 {
			if _, err := db.Exec(`UPDATE users SET session_generation = session_generation + 1 WHERE id = $1`, u); err != nil {
				t.Error(err)
			}
		}
	})
	g := &storage.SessionGuard{UserID: u, Gen: 0}
	ctx := storage.WithSessionGuard(context.Background(), g)
	res, err := (&ManagerEngine{DB: db, OAuthConfig: &oauth2.Config{}}).DetectTeam(ctx, u)
	if !errors.Is(err, storage.ErrStaleSession) || res != nil || !g.Stale() {
		t.Fatalf("DetectTeam = %v, %v; want the stale-session error", res, err)
	}
	stub.mu.Lock()
	listed := len(stub.lists)
	stub.mu.Unlock()
	if listed == 0 || calls.Load() != 2 {
		t.Fatalf("the detection stopped before its profile step (calendar reads %d, re-checks %d)", listed, calls.Load())
	}
	if n := pac80ProfileCount(t, db, u); n != 0 {
		t.Fatalf("a stale detection created %d profile rows", n)
	}
}

// AC-7: the scheduled job (no session) creates the default row for a manager
// with none and proceeds as today.
func TestPAC80_ScheduledDetectionCreatesTheDefaultRow(t *testing.T) {
	db := openTestDB(t)
	newGoogleStub(t)
	u, _ := pac81User(t, db, "pac80-detect-job@example.com")
	res, err := (&ManagerEngine{DB: db, OAuthConfig: &oauth2.Config{}}).DetectTeam(context.Background(), u)
	if err != nil || res == nil || res.ScannedAt.IsZero() || res.Candidates == nil {
		t.Fatalf("DetectTeam = %+v, %v", res, err)
	}
	p, err := storage.GetUserProfile(db, u)
	if err != nil {
		t.Fatalf("no default row: %v", err)
	}
	if p.IsManager || p.DetectedAt != nil || !p.AnalyticsSharedWithManager {
		t.Fatalf("row = %+v, want the schema defaults", p)
	}
}
