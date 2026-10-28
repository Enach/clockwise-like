package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/budget"
	"github.com/Enach/paceday/backend/internal/lifecycle"
	"github.com/go-chi/chi/v5"
)

// --- AC-A12: body cap ------------------------------------------------------

func capped(ran *bool) http.Handler {
	return bodyCapMiddleware(maxRequestBody)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*ran = true
		b, _ := io.ReadAll(r.Body)
		_, _ = fmt.Fprintf(w, "%d", len(b))
	}))
}

// The 413 is byte-identical to nginx's (Part B pins the same bytes).
func TestPAC81_BodyCapExactBytes(t *testing.T) {
	var ran bool
	rec := httptest.NewRecorder()
	capped(&ran).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/x", bytes.NewReader(make([]byte, maxRequestBody+1))))
	if rec.Code != http.StatusRequestEntityTooLarge || ran {
		t.Fatalf("status %d, handler ran %v", rec.Code, ran)
	}
	if got := rec.Body.String(); got != "{\"error\":\"request body too large\"}\n" {
		t.Fatalf("body %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type %q", ct)
	}
}

func TestPAC81_BodyCapExactlyAtCapProcessed(t *testing.T) {
	var ran bool
	rec := httptest.NewRecorder()
	capped(&ran).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/x", bytes.NewReader(make([]byte, maxRequestBody))))
	if rec.Code != http.StatusOK || !ran || rec.Body.String() != fmt.Sprint(maxRequestBody) {
		t.Fatalf("status %d ran %v body %q", rec.Code, ran, rec.Body.String())
	}
}

// A body without Content-Length (chunked) that runs past the cap.
func TestPAC81_BodyCapChunkedOver(t *testing.T) {
	var ran bool
	req := httptest.NewRequest(http.MethodPost, "/api/x", io.MultiReader(bytes.NewReader(make([]byte, maxRequestBody)), strings.NewReader("x")))
	req.ContentLength = -1
	rec := httptest.NewRecorder()
	capped(&ran).ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge || ran {
		t.Fatalf("status %d ran %v", rec.Code, ran)
	}
}

// A declared Content-Length over the cap is refused before anything is read.
func TestPAC81_BodyCapDeclaredLength(t *testing.T) {
	var ran bool
	req := httptest.NewRequest(http.MethodPost, "/api/x", strings.NewReader("{}"))
	req.ContentLength = maxRequestBody + 10
	rec := httptest.NewRecorder()
	capped(&ran).ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge || ran {
		t.Fatalf("status %d ran %v", rec.Code, ran)
	}
}

// --- AC-A13: access log ----------------------------------------------------

type logLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *logLines) printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func captureAccessLog(t *testing.T) *logLines {
	t.Helper()
	l := &logLines{}
	prev := accessLogf
	accessLogf = l.printf
	t.Cleanup(func() { accessLogf = prev })
	return l
}

// replacingMiddleware stands for PAC-71's guarded writer: it replaces the
// handler's status with 409.
func replacingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&replaceWriter{ResponseWriter: w}, r)
	})
}

type replaceWriter struct{ http.ResponseWriter }

func (w *replaceWriter) WriteHeader(int) { w.ResponseWriter.WriteHeader(http.StatusConflict) }

func TestPAC81_AccessLogOneLineStatusPathClient(t *testing.T) {
	l := captureAccessLog(t)
	h := loggingMiddleware(replacingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?code=SECRETCODE&state=s", nil)
	req.RemoteAddr = "203.0.113.7:5555"
	h.ServeHTTP(httptest.NewRecorder(), req)
	if len(l.lines) != 1 {
		t.Fatalf("lines %v", l.lines)
	}
	line := l.lines[0]
	for _, want := range []string{"GET /api/auth/callback 409 ", "client=203.0.113.7"} {
		if !strings.Contains(line, want) {
			t.Fatalf("line %q lacks %q", line, want)
		}
	}
	if strings.Contains(line, "SECRETCODE") || strings.Contains(line, "?") {
		t.Fatalf("query string logged: %q", line)
	}
}

func TestPAC81_AccessLogTrustedProxyClient(t *testing.T) {
	l := captureAccessLog(t)
	prev := trustedProxies
	trustedProxies = parseTrustedProxies("10.0.0.0/8")
	defer func() { trustedProxies = prev }()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.RemoteAddr = "10.1.2.3:4444"
	req.Header.Set("X-Real-IP", "198.51.100.9")
	loggingMiddleware(http.HandlerFunc(healthHandler)).ServeHTTP(httptest.NewRecorder(), req)
	if len(l.lines) != 1 || !strings.Contains(l.lines[0], "GET /api/health 200 ") || !strings.Contains(l.lines[0], "client=198.51.100.9") {
		t.Fatalf("lines %v", l.lines)
	}
}

func TestPAC81_AccessLogBudget503(t *testing.T) {
	l := captureAccessLog(t)
	release := make(chan struct{})
	defer close(release)
	h := loggingMiddleware(budget.Middleware(30*time.Millisecond, nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	})))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/nlp/parse", nil))
	if rec.Code != http.StatusServiceUnavailable || len(l.lines) != 1 || !strings.Contains(l.lines[0], "POST /api/nlp/parse 503 ") {
		t.Fatalf("code %d lines %v", rec.Code, l.lines)
	}
}

// --- AC-A11: readiness -----------------------------------------------------

func serveReadiness(rd *readiness) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	readinessHandler(rd)(rec, httptest.NewRequest(http.MethodGet, "/api/ready", nil))
	return rec
}

func TestPAC81_ReadinessReadyAndNotReady(t *testing.T) {
	shutting := false
	pingErr := error(nil)
	rd := &readiness{
		ping:         func(context.Context) error { return pingErr },
		shuttingDown: func() bool { return shutting },
	}
	if rec := serveReadiness(rd); rec.Code != 200 || rec.Body.String() != "{\"status\":\"ready\"}\n" {
		t.Fatalf("ready: %d %q", rec.Code, rec.Body.String())
	}
	pingErr = errors.New("connection refused")
	if rec := serveReadiness(rd); rec.Code != 503 || rec.Body.String() != "{\"error\":\"not ready\"}\n" {
		t.Fatalf("db down: %d %q", rec.Code, rec.Body.String())
	}
	pingErr = nil
	shutting = true
	if rec := serveReadiness(rd); rec.Code != 503 {
		t.Fatalf("shutting down: %d", rec.Code)
	}
}

// The shutdown flag is read before a ping starts: no ping once shutting down.
func TestPAC81_ReadinessNoPingWhenShuttingDown(t *testing.T) {
	var pings atomic.Int32
	rd := &readiness{
		ping:         func(context.Context) error { pings.Add(1); return nil },
		shuttingDown: func() bool { return true },
	}
	serveReadiness(rd)
	if pings.Load() != 0 {
		t.Fatal("pinged while shutting down")
	}
}

// A ping that does not answer: 503 within the readiness timeout + 1 s.
func TestPAC81_ReadinessSlowDatabase(t *testing.T) {
	prev := readinessTimeout
	readinessTimeout = 200 * time.Millisecond
	defer func() { readinessTimeout = prev }()
	rd := &readiness{
		ping:         func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
		shuttingDown: func() bool { return false },
	}
	start := time.Now()
	if rec := serveReadiness(rd); rec.Code != 503 || time.Since(start) > readinessTimeout+time.Second {
		t.Fatalf("%d after %s", rec.Code, time.Since(start))
	}
}

// AC-A11 rev 2 (B2): 50 concurrent calls against a ping that counts and
// blocks: at most one ping in flight at any moment, every call gets the
// shared result.
func TestPAC81_Readiness50ConcurrentSingleFlight(t *testing.T) {
	var inFlight, peak, total atomic.Int32
	release := make(chan struct{})
	rd := &readiness{
		ping: func(context.Context) error {
			n := inFlight.Add(1)
			total.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			<-release
			inFlight.Add(-1)
			return nil
		},
		shuttingDown: func() bool { return false },
	}
	var wg sync.WaitGroup
	codes := make(chan int, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- serveReadiness(rd).Code
		}()
	}
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()
	close(codes)
	for c := range codes {
		if c != 200 {
			t.Fatalf("a call got %d", c)
		}
	}
	if peak.Load() != 1 {
		t.Fatalf("%d pings in flight at once", peak.Load())
	}
	if total.Load() != 1 {
		t.Fatalf("%d pings for 50 calls that all arrived while one was in flight", total.Load())
	}
}

// Through RegisterRoutes: /api/ready and /api/health are public; health keeps
// its bytes; readiness answers 503 while shutting down while a normal request
// is still served (AC-A6 observation).
func TestPAC81_ReadinessRouteAndHealthUnchanged(t *testing.T) {
	db := openTestDB(t)
	r := chi.NewRouter()
	RegisterRoutes(r, db, nil, "secret", "http://app.test", "http://app.test")
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}
	if rec := get("/api/ready"); rec.Code != 200 || rec.Body.String() != "{\"status\":\"ready\"}\n" {
		t.Fatalf("ready: %d %q", rec.Code, rec.Body.String())
	}
	if rec := get("/api/health"); rec.Code != 200 || rec.Body.String() != "{\"status\":\"ok\",\"version\":\"0.1.0\"}\n" {
		t.Fatalf("health: %d %q", rec.Code, rec.Body.String())
	}
	g := lifecycle.NewGroup()
	defer lifecycle.SetDefault(g)()
	g.BeginShutdown()
	if rec := get("/api/ready"); rec.Code != 503 {
		t.Fatalf("ready while shutting down: %d", rec.Code)
	}
	if rec := get("/api/health"); rec.Code != 200 {
		t.Fatalf("a normal request during the notice: %d", rec.Code)
	}
}
