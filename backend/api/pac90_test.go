package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/internal/budget"
	"github.com/go-chi/chi/v5"
)

// PAC-90 items 2-4: the access log's implicit 200, flush through the
// production chain, and the step-up record step's reservation.

// --- Item 2: access log ------------------------------------------------------

// pac90Lines returns the captured access-log lines for route.
func pac90Lines(l *logLines, route string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, line := range l.lines {
		if strings.Contains(line, " "+route+" ") {
			out = append(out, line)
		}
	}
	return out
}

// AC-3: production chain, a live request whose handler writes nothing:
// exactly one access line, status 200 (net/http's implicit 200), not "-".
func TestPAC90_AccessLogUnwrittenLiveRequestIs200(t *testing.T) {
	l := captureAccessLog(t)
	r := fullRouter(t)
	r.Get("/api/pac90/silent", func(http.ResponseWriter, *http.Request) {})
	rec := serveAt(r, http.MethodGet, "/api/pac90/silent", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("client got %d", rec.Code)
	}
	lines := pac90Lines(l, "/api/pac90/silent")
	if len(lines) != 1 || !strings.Contains(lines[0], "GET /api/pac90/silent 200 ") {
		t.Fatalf("lines %v", lines)
	}
}

// --- Item 4: flush through the production chain -----------------------------

func pac90Server(t *testing.T, path string, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	r := fullRouter(t)
	r.Get(path, h)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// AC-7: a handler writes a chunk, flushes, and waits for the client to
// confirm it received it: the chunk arrives within 2 s, before the handler
// returns.
func TestPAC90_FlushReachesClientThroughProductionChain(t *testing.T) {
	received := make(chan struct{})
	var returned atomic.Bool
	srv := pac90Server(t, "/api/pac90/stream", func(w http.ResponseWriter, _ *http.Request) {
		defer returned.Store(true)
		_, _ = w.Write([]byte("chunk1"))
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("flush: %v", err)
		}
		select {
		case <-received:
		case <-time.After(5 * time.Second):
		}
		_, _ = w.Write([]byte("end"))
	})
	got := make(chan string, 1)
	go func() {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/api/pac90/stream", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			got <- "error: " + err.Error()
			return
		}
		defer resp.Body.Close()
		buf := make([]byte, len("chunk1"))
		if _, err := io.ReadFull(resp.Body, buf); err != nil {
			got <- "error: " + err.Error()
			return
		}
		got <- string(buf)
		_, _ = io.Copy(io.Discard, resp.Body)
	}()
	select {
	case s := <-got:
		if s != "chunk1" {
			t.Fatalf("client read %q", s)
		}
		if returned.Load() {
			t.Fatal("the chunk arrived only after the handler returned")
		}
		close(received)
	case <-time.After(2 * time.Second):
		close(received)
		t.Fatal("the flushed chunk did not reach the client within 2 s")
	}
}

// AC-8: a flush before any write sends the 200; the client then goes away.
// The access log records 200 from the flush, not "-" from the ended context.
func TestPAC90_FlushBeforeWriteLogs200(t *testing.T) {
	l := captureAccessLog(t)
	handlerDone := make(chan struct{})
	srv := pac90Server(t, "/api/pac90/flush-first", func(w http.ResponseWriter, r *http.Request) {
		defer close(handlerDone)
		_ = http.NewResponseController(w).Flush()
		<-r.Context().Done() // until the client has gone
	})
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/pac90/flush-first", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("client got %d", resp.StatusCode)
	}
	cancel()
	_ = resp.Body.Close()
	select {
	case <-handlerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not see the client leave")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		lines := pac90Lines(l, "/api/pac90/flush-first")
		if len(lines) == 1 {
			if !strings.Contains(lines[0], "GET /api/pac90/flush-first 200 ") {
				t.Fatalf("line %q, want status 200", lines[0])
			}
			return
		}
		if len(lines) > 1 || time.Now().After(deadline) {
			t.Fatalf("lines %v", lines)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// --- Item 3: the step-up record step's reservation ---------------------------

const pac90StepupBudget = 400 * time.Millisecond

type pac90Stepup struct {
	e        *ssoEnv
	pool     *sql.DB
	returned chan struct{} // closed when the callback's handler goroutine returns
}

// newPAC90Stepup is the PAC-50 Google step-up fixture with the handlers on a
// pool capped at 1, behind a short request budget, and a Directory stand-in
// that runs dirHook (when set) before answering "admin".
func newPAC90Stepup(t *testing.T, dirHook func()) *pac90Stepup {
	t.Helper()
	e := newSSOEnv(t)
	s := &pac90Stepup{e: e, pool: capOnePool(t), returned: make(chan struct{})}
	dir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer stepup-access-google" {
			http.Error(w, "no bearer", http.StatusUnauthorized)
			return
		}
		if dirHook != nil {
			dirHook()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"isAdmin": true, "isDelegatedAdmin": false})
	}))
	t.Cleanup(dir.Close)
	ep := e.google.endpoints()
	ep.APIBase = dir.URL
	ah := &authHandlers{db: s.pool, jwtSecret: pac50Secret, frontendURL: pac50Frontend}
	e.h = newSSOHandlers(ah, ssoOptions{
		Clock:     e.clock,
		Google:    auth.NewGoogleStepup(e.google.clientID, "google-secret", pac50AppURL+stepupCallbackPath, ep),
		Microsoft: auth.NewMicrosoftStepup(e.ms.clientID, "ms-secret", pac50AppURL+stepupCallbackPath, e.ms.endpoints()),
	})
	r := chi.NewRouter()
	r.Use(budget.Middleware(pac90StepupBudget, nil))
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Path == stepupCallbackPath {
				defer close(s.returned)
			}
			next.ServeHTTP(w, req)
		})
	})
	r.Group(func(r chi.Router) {
		r.Use(requireAuth(s.pool, pac50Secret))
		r.Route("/api/admin/sso", e.h.mountAdmin)
	})
	e.router = r
	return s
}

// run drives the Google step-up for a fresh admin member and returns the
// callback's answer, when it came, and when the handler goroutine returned.
func (s *pac90Stepup) run(t *testing.T, label string) (*httptest.ResponseRecorder, time.Duration, time.Duration, func() (int, int)) {
	t.Helper()
	u, org := s.e.member(t, label)
	s.e.google.set(googleAdmin(u.Email, org.Domain))
	sess := session(t, u, time.Now())
	start := time.Now()
	rec := s.e.stepup(t, "google", sess)
	answered := time.Since(start)
	var returned time.Duration
	select {
	case <-s.returned:
		returned = time.Since(start)
	case <-time.After(pac90StepupBudget + 2*time.Second):
		returned = time.Hour
	}
	stored := func() (int, int) {
		n := 0
		for _, ev := range s.e.events(t, u.ID) {
			if ev.Kind == "sso_stepup" {
				n++
			}
		}
		return n, s.e.liveGrants(t, u.ID)
	}
	return rec, answered, returned, stored
}

func pac90WantDenied(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), pac50Frontend+"/settings?stepup=denied") {
		t.Fatalf("got %d Location %q, want the budget's denied redirect", rec.Code, rec.Header().Get("Location"))
	}
}

// AC-5: the checks pass and the only connection is taken during the last
// check (the Directory call): the budget's denied redirect within budget +
// 1 s, the handler goroutine returns within budget + 1 s, and once the
// connection is released nothing was stored for the user.
func TestPAC90_StepupPoolHeldAtRecordIsBudgetDenied(t *testing.T) {
	var held atomic.Pointer[sql.Conn]
	var s *pac90Stepup
	s = newPAC90Stepup(t, func() {
		c, err := s.pool.Conn(context.Background())
		if err == nil {
			held.Store(c)
		}
	})
	rec, answered, returned, stored := s.run(t, "pac90-held")
	if c := held.Load(); c != nil {
		_ = c.Close()
	} else {
		t.Fatal("the Directory stand-in did not take the connection")
	}
	pac90WantDenied(t, rec)
	if limit := pac90StepupBudget + time.Second; answered > limit || returned > limit {
		t.Fatalf("answered after %s, handler returned after %s; want both within %s", answered, returned, limit)
	}
	if events, grants := stored(); events != 0 || grants != 0 {
		t.Fatalf("%d step-up outcomes and %d grants stored after the budget said denied", events, grants)
	}
}

// AC-5b: the record's connection is reserved, then the budget answers before
// budget.Commit: the denied redirect, the handler returns within budget +
// 1 s, the reserved connection is released within 1 s, nothing is stored.
// Fails if finishStepup's `!budget.Commit` return is removed.
func TestPAC90_StepupBudgetBetweenReserveAndCommitStoresNothing(t *testing.T) {
	s := newPAC90Stepup(t, nil)
	stepupBeforeCommit = func(ctx context.Context) { <-ctx.Done() } // the budget answers here
	t.Cleanup(func() { stepupBeforeCommit = nil })
	rec, _, returned, stored := s.run(t, "pac90-commit")
	stepupBeforeCommit = nil
	pac90WantDenied(t, rec)
	if limit := pac90StepupBudget + time.Second; returned > limit {
		t.Fatalf("handler returned after %s, want within %s", returned, limit)
	}
	deadline := time.Now().Add(time.Second)
	for s.pool.Stats().InUse != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("reserved connection still in use 1 s after the handler returned")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if events, grants := stored(); events != 0 || grants != 0 {
		t.Fatalf("%d step-up outcomes and %d grants stored after the budget said denied", events, grants)
	}
}

// AC-6: the control case: same fixture, pool free: the outcome is recorded
// and the answer is the granted redirect.
func TestPAC90_StepupPoolFreeAtRecordIsGranted(t *testing.T) {
	s := newPAC90Stepup(t, nil)
	rec, _, _, stored := s.run(t, "pac90-free")
	wantOutcome(t, rec, rec.Code, "stepup=granted")
	if events, grants := stored(); events != 1 || grants != 1 {
		t.Fatalf("%d step-up outcomes and %d grants, want 1 and 1", events, grants)
	}
}
