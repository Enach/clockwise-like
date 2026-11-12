package budget

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// PAC-90 item 4: budget.writer.Flush forwards outward through the whole
// remaining chain (writers that only expose Unwrap included), and stays a
// no-op once the budget answered.

// flushCounter is a destination that records flushes.
type flushCounter struct {
	*httptest.ResponseRecorder
	flushes int
}

func (f *flushCounter) Flush() {
	f.flushes++
	f.ResponseRecorder.Flush()
}

// unwrapOnly stands for an outer wrapper without Flush (contract §3.1).
type unwrapOnly struct{ http.ResponseWriter }

func (u unwrapOnly) Unwrap() http.ResponseWriter { return u.ResponseWriter }

func TestPAC90_FlushForwardsThroughUnwrapOnlyWriter(t *testing.T) {
	dst := &flushCounter{ResponseRecorder: httptest.NewRecorder()}
	h := Middleware(time.Second, nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Handler", "1")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("flush: %v", err)
		}
		if dst.flushes != 1 {
			t.Errorf("flush not forwarded before the handler returned: %d", dst.flushes)
		}
	}))
	h.ServeHTTP(unwrapOnly{dst}, httptest.NewRequest(http.MethodGet, "/x", nil))
	if dst.flushes != 1 || dst.Code != http.StatusOK || dst.Header().Get("X-Handler") != "1" {
		t.Fatalf("flushes %d code %d header %q", dst.flushes, dst.Code, dst.Header().Get("X-Handler"))
	}
}

// AC-8 (budget half): after the budget answered, a flush through the
// Flusher interface and through http.ResponseController sends nothing and
// does not panic; the writer still has no Unwrap and Hijack is refused.
func TestPAC90_FlushAfterBudgetAnsweredIsNoop(t *testing.T) {
	dst := &flushCounter{ResponseRecorder: httptest.NewRecorder()}
	handlerDone := make(chan struct{})
	var unwrappable bool
	var hijackErr, ctrlErr error
	h := Middleware(30*time.Millisecond, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(handlerDone)
		<-r.Context().Done() // the budget answered and cancelled the context
		w.Header().Set("X-Late", "1")
		w.(http.Flusher).Flush()
		ctrlErr = http.NewResponseController(w).Flush()
		_, unwrappable = w.(interface{ Unwrap() http.ResponseWriter })
		_, _, hijackErr = w.(http.Hijacker).Hijack()
	}))
	h.ServeHTTP(dst, httptest.NewRequest(http.MethodGet, "/x", nil))
	<-handlerDone
	if dst.Code != http.StatusServiceUnavailable || dst.Body.String() != `{"error":"`+Message+`"}`+"\n" {
		t.Fatalf("answer %d %q", dst.Code, dst.Body.String())
	}
	if dst.flushes != 0 || dst.Header().Get("X-Late") != "" {
		t.Fatalf("a late flush reached the client: %d flushes, X-Late %q", dst.flushes, dst.Header().Get("X-Late"))
	}
	if ctrlErr != nil {
		t.Fatalf("ResponseController flush: %v", ctrlErr)
	}
	if unwrappable {
		t.Fatal("budget.writer exposes Unwrap")
	}
	if !errors.Is(hijackErr, ErrHijackRefused) {
		t.Fatalf("hijack: %v", hijackErr)
	}
}
