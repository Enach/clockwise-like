// Package budget is the per-request handler budget (PAC-81 Part A, AC-A2).
//
// Every request runs its handler on a context whose deadline is the budget
// (90 s by default). When the handler has not started its response by then,
// the middleware answers for it: the shared JSON 503
// {"error":"request timed out"}, or the answer the handler registered with
// OnExpiry (the sign-in callbacks' failure redirect). A handler that has
// committed (Commit) is waited for instead: once a booking insert is sent,
// only the insert's own result decides the answer (AC-A19).
//
// The handler goroutine may outlive the response (a database call made
// without a context is not interrupted); the middleware drops anything it
// writes after the budget answered, and Track lets the shutdown sequence
// count it as an abandoned unit.
package budget

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// Default is the handler budget (spec section 3, D-4).
const Default = 90 * time.Second

// Message is the fixed error of the budget 503 (contract RequestBudgetExpiredError).
const Message = "request timed out"

// ErrExpired is returned by work that stopped because the budget ran out, or
// would run out before the work could finish (per-item loops, AC-A17/A18).
var ErrExpired = errors.New("request budget expired")

// WriteExpired writes the shared budget 503.
func WriteExpired(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`{"error":"` + Message + `"}` + "\n"))
}

type ctxKey struct{}

// state is shared between the middleware and the handler goroutine.
type state struct {
	mu        sync.Mutex
	expired   bool // the middleware answered; handler writes are dropped
	committed bool // the handler owns the answer whatever the budget
	wrote     bool // the handler started its response
	onExpire  func(w http.ResponseWriter, r *http.Request)
}

func from(ctx context.Context) *state {
	s, _ := ctx.Value(ctxKey{}).(*state)
	return s
}

// Commit marks the point after which the budget no longer answers for the
// handler: the middleware waits for the handler's own answer. It reports
// false when the budget has already answered (or the context is done
// without a budget), in which case the caller must not start the work.
func Commit(ctx context.Context) bool {
	s := from(ctx)
	if s == nil {
		return ctx.Err() == nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.expired {
		return false
	}
	s.committed = true
	return true
}

// OnExpiry registers the answer the middleware writes if the budget runs out
// before the handler answers (replacing any earlier registration). Without
// one, the answer is WriteExpired.
func OnExpiry(ctx context.Context, fn func(w http.ResponseWriter, r *http.Request)) {
	if s := from(ctx); s != nil {
		s.mu.Lock()
		s.onExpire = fn
		s.mu.Unlock()
	}
}

// Expired reports whether the middleware has already answered for this request.
func Expired(ctx context.Context) bool {
	s := from(ctx)
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.expired
}

// Tracker registers a handler goroutine with the shutdown group; the returned
// function is called when the goroutine returns.
type Tracker func(r *http.Request) (done func())

// Middleware returns the budget middleware.
func Middleware(budget time.Duration, track Tracker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return &handler{next: next, budget: budget, track: track}
	}
}

type handler struct {
	next   http.Handler
	budget time.Duration
	track  Tracker
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.budget)
	defer cancel()
	st := &state{}
	ctx = context.WithValue(ctx, ctxKey{}, st)
	r = r.WithContext(ctx)

	bw := &writer{dst: w, st: st, h: w.Header().Clone()}
	done := make(chan struct{})
	panicked := make(chan any, 1)
	var release func()
	if h.track != nil {
		release = h.track(r)
	}
	go func() {
		defer func() {
			if release != nil {
				release()
			}
		}()
		defer close(done)
		defer func() {
			if p := recover(); p != nil {
				st.mu.Lock()
				answered := st.expired
				st.mu.Unlock()
				if answered {
					// Nobody is left to re-raise it to: log it.
					log.Printf("budget: %s %s panicked after the budget answered: %v", r.Method, r.URL.Path, p)
					return
				}
				panicked <- p
			}
		}()
		h.next.ServeHTTP(bw, r)
	}()

	waitHandler := func() {
		<-done
		select {
		case p := <-panicked:
			panic(p)
		default:
		}
	}

	select {
	case <-done:
		select {
		case p := <-panicked:
			panic(p)
		default:
		}
		return
	case <-ctx.Done():
	}

	st.mu.Lock()
	if st.wrote || st.committed {
		st.mu.Unlock()
		waitHandler()
		return
	}
	st.expired = true
	respond := st.onExpire
	st.mu.Unlock()
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		// The client went away (or the server is cancelling requests at the
		// end of the drain): nobody is waiting for an answer.
		return
	}
	if respond != nil {
		respond(w, r)
		return
	}
	WriteExpired(w)
}

// writer gives the handler its own header map and drops its writes once the
// middleware has answered.
type writer struct {
	dst http.ResponseWriter
	st  *state
	h   http.Header
}

func (w *writer) Header() http.Header { return w.h }

// begin copies the handler's headers to the real writer the first time the
// handler writes. It reports false when the middleware already answered.
func (w *writer) begin() bool {
	if w.st.expired {
		return false
	}
	if !w.st.wrote {
		w.st.wrote = true
		dh := w.dst.Header()
		for k := range dh {
			delete(dh, k)
		}
		for k, v := range w.h {
			dh[k] = v
		}
	}
	return true
}

func (w *writer) WriteHeader(code int) {
	w.st.mu.Lock()
	defer w.st.mu.Unlock()
	if w.begin() {
		w.dst.WriteHeader(code)
	}
}

func (w *writer) Write(b []byte) (int, error) {
	w.st.mu.Lock()
	defer w.st.mu.Unlock()
	if !w.begin() {
		return 0, fmt.Errorf("budget: %w: response already sent", ErrExpired)
	}
	return w.dst.Write(b)
}

// Unwrap lets http.ResponseController reach the connection.
func (w *writer) Unwrap() http.ResponseWriter { return w.dst }
