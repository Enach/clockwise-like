package api

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/internal/lifecycle"
	"github.com/Enach/paceday/backend/internal/lifecycle/lifecycletest"
	"github.com/google/uuid"
)

// PAC-122 SF-096: the detached units started by habits, analytics and
// manager requests no longer discard their errors: each reaches the
// lifecycle reporter with the unit's kind and id. The responses are
// unchanged.

// pac122ClosedDB is a pool on the test database that is already closed:
// every query on it fails.
func pac122ClosedDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", sharedTestDSN)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	return db
}

func pac122Group(t *testing.T) *lifecycle.Group {
	t.Helper()
	g := lifecycle.NewGroup()
	t.Cleanup(lifecycle.SetDefault(g))
	return g
}

func TestPAC122_HabitReoptimizeErrorReported(t *testing.T) {
	rec := lifecycletest.Record(t)
	g := pac122Group(t)
	user := uuid.New()
	h := &habitsHandlers{eng: &engine.HabitsEngine{DB: pac122ClosedDB(t)}}
	w := httptest.NewRecorder()
	h.reoptimize(w, withUser(httptest.NewRequest(http.MethodPost, "/api/habits/reoptimize", nil), user))
	g.Wait(context.Background())
	if w.Code != http.StatusOK {
		t.Fatalf("status %d (behaviour changed)", w.Code)
	}
	got := rec.Matching("habit-reoptimize", "list habits")
	if len(got) != 1 || got[0].Unit.ID != user.String() {
		t.Fatalf("reports %+v", rec.All())
	}
}

func TestPAC122_AnalyticsRecomputeErrorReported(t *testing.T) {
	rec := lifecycletest.Record(t)
	g := pac122Group(t)
	user := uuid.New()
	h := &analyticsHandlers{eng: &engine.AnalyticsEngine{DB: pac122ClosedDB(t)}}
	w := httptest.NewRecorder()
	h.recompute(w, withUser(httptest.NewRequest(http.MethodPost, "/api/analytics/recompute", nil), user))
	g.Wait(context.Background())
	if w.Code != http.StatusAccepted {
		t.Fatalf("status %d (behaviour changed)", w.Code)
	}
	got := rec.Matching("analytics-recompute", "get settings")
	if len(got) != 1 || got[0].Unit.ID != user.String() {
		t.Fatalf("reports %+v", rec.All())
	}
}

type pac122FailingDetect struct{ fakeManagerWorkflow }

func (*pac122FailingDetect) DetectTeam(context.Context, uuid.UUID) (*engine.DetectResult, error) {
	return nil, fmt.Errorf("calendar not connected: %w", errors.New("attendee alex.report@example.com"))
}

func TestPAC122_ManagerDetectionErrorReported(t *testing.T) {
	rec := lifecycletest.Record(t)
	var logs bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(prev) })
	g := pac122Group(t)
	r := setupManagerRoutes(t, &pac122FailingDetect{})
	user := createTestUser(t, "pac122-manager@example.com")
	w := managerRequest(t, r, http.MethodPost, "/api/manager/profile", user, map[string]any{"is_manager": true})
	g.Wait(context.Background())
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s (behaviour changed)", w.Code, w.Body.String())
	}
	got := rec.Matching("manager-detection", "calendar not connected")
	if len(got) != 1 || got[0].Unit.ID != user.String() {
		t.Fatalf("reports %+v", rec.All())
	}
	// The unit's error carries an address: neither the report nor the log
	// may (PAC-122 review blocker 1).
	if strings.Contains(got[0].Err.Error(), "alex.report") || strings.Contains(logs.String(), "alex.report") {
		t.Fatalf("an address leaked: report %q, log %q", got[0].Err, logs.String())
	}
	if !strings.Contains(logs.String(), "manager-detection "+user.String()+" failed") {
		t.Fatalf("failure not logged: %q", logs.String())
	}
}
