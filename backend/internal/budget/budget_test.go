package budget

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func serve(t *testing.T, b time.Duration, h http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	Middleware(b, nil)(h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	return rec
}

func TestFastHandlerAnswersNormally(t *testing.T) {
	rec := serve(t, time.Second, func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Context().Deadline(); !ok {
			t.Error("handler context has no budget deadline")
		}
		w.Header().Set("X-Test", "1")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	})
	if rec.Code != http.StatusCreated || rec.Body.String() != "ok" || rec.Header().Get("X-Test") != "1" {
		t.Fatalf("got %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
}

// AC-A2: a handler still running at the budget gets a complete JSON 503, its
// context is cancelled, and its late writes are dropped (never a 2xx).
func TestExpiryAnswers503JSON(t *testing.T) {
	cancelled := make(chan struct{})
	late := make(chan error, 1)
	start := time.Now()
	rec := serve(t, 50*time.Millisecond, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		close(cancelled)
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(`{"partial":true}`))
		late <- err
	})
	if took := time.Since(start); took > time.Second {
		t.Fatalf("answered after %s", took)
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] != Message || len(body) != 1 {
		t.Fatalf("body %q (%v)", rec.Body.String(), err)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type %q", ct)
	}
	<-cancelled
	if err := <-late; !errors.Is(err, ErrExpired) {
		t.Fatalf("late write error %v, want ErrExpired", err)
	}
	if rec.Body.String() != `{"error":"request timed out"}`+"\n" {
		t.Fatalf("late write reached the client: %q", rec.Body.String())
	}
}

// AC-A2: a browser-navigation route registers its failure redirect.
func TestExpiryCustomRedirect(t *testing.T) {
	rec := serve(t, 30*time.Millisecond, func(w http.ResponseWriter, r *http.Request) {
		OnExpiry(r.Context(), func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://app.test/?error=auth_failed", http.StatusFound)
		})
		<-r.Context().Done()
	})
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "https://app.test/?error=auth_failed" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

// AC-A19: once committed, the handler's own answer is waited for, even past
// the budget.
func TestCommittedHandlerIsWaitedFor(t *testing.T) {
	rec := serve(t, 30*time.Millisecond, func(w http.ResponseWriter, r *http.Request) {
		if !Commit(r.Context()) {
			t.Error("commit refused before the budget")
		}
		time.Sleep(100 * time.Millisecond) // the "insert" outlives the budget
		w.WriteHeader(http.StatusCreated)
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201", rec.Code)
	}
}

func TestCommitAfterExpiryIsRefused(t *testing.T) {
	refused := make(chan bool, 1)
	rec := serve(t, 20*time.Millisecond, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		for !Expired(r.Context()) {
			time.Sleep(time.Millisecond)
		}
		refused <- !Commit(r.Context())
	})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", rec.Code)
	}
	if !<-refused {
		t.Fatal("commit accepted after the budget answered")
	}
}

// A handler that has started its response finishes it.
func TestStartedResponseIsNotReplaced(t *testing.T) {
	rec := serve(t, 20*time.Millisecond, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		time.Sleep(60 * time.Millisecond)
		_, _ = w.Write([]byte("done"))
	})
	if rec.Code != http.StatusOK || rec.Body.String() != "done" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
}

func TestPanicIsReraisedOnServingGoroutine(t *testing.T) {
	defer func() {
		if p := recover(); p != "boom" {
			t.Fatalf("recovered %v, want boom", p)
		}
	}()
	serve(t, time.Second, func(http.ResponseWriter, *http.Request) { panic("boom") })
	t.Fatal("no panic")
}

func TestTrackerSeesHandlerGoroutine(t *testing.T) {
	var live atomic.Int32
	track := func(*http.Request) func() { live.Add(1); return func() { live.Add(-1) } }
	release := make(chan struct{})
	rec := httptest.NewRecorder()
	Middleware(20*time.Millisecond, track)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release // a context-free call still blocked after the response
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable || live.Load() != 1 {
		t.Fatalf("status %d live %d", rec.Code, live.Load())
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for live.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if live.Load() != 0 {
		t.Fatal("tracked goroutine never released")
	}
}

func TestClientGoneWritesNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	rec := httptest.NewRecorder()
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	Middleware(time.Second, nil)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
	if rec.Body.Len() != 0 {
		t.Fatalf("wrote %q for a gone client", rec.Body.String())
	}
}

func TestHelpersWithoutBudget(t *testing.T) {
	ctx := context.Background()
	if !Commit(ctx) || Expired(ctx) {
		t.Fatal("no-budget context must commit and never be expired")
	}
	OnExpiry(ctx, nil) // no-op
	c, cancel := context.WithCancel(ctx)
	cancel()
	if Commit(c) {
		t.Fatal("a done context without a budget must not commit")
	}
}

// A handler whose work fails because the budget cancelled it never beats the
// budget's own answer (the cancellation follows the answer).
func TestCancelledWorkDoesNotRaceTheAnswer(t *testing.T) {
	for i := 0; i < 50; i++ {
		rec := serve(t, 5*time.Millisecond, func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
			w.WriteHeader(http.StatusInternalServerError)
		})
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("iteration %d: %d, want the budget's 503", i, rec.Code)
		}
	}
}

// A panic after the budget answered is logged, not lost and not re-raised.
func TestPanicAfterExpiryIsLogged(t *testing.T) {
	logged := &syncBuf{}
	prev := log.Writer()
	log.SetOutput(logged)
	defer log.SetOutput(prev)
	done := make(chan struct{})
	rec := serve(t, 10*time.Millisecond, func(_ http.ResponseWriter, r *http.Request) {
		defer close(done)
		<-r.Context().Done()
		for !Expired(r.Context()) {
			time.Sleep(time.Millisecond)
		}
		panic("late")
	})
	<-done
	for i := 0; i < 100 && !strings.Contains(logged.String(), "late"); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(logged.String(), "panicked after the budget answered: late") {
		t.Fatalf("%d %q", rec.Code, logged.String())
	}
}

type syncBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
