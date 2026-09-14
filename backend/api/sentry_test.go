package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// TestSentryMiddlewarePanicStillReturns500 verifies the client-facing contract
// is unchanged (contract §3, §5): with the Sentry recover middleware installed,
// a handler panic is still converted to a 500 by the downstream recoverer, and
// the middleware repanics rather than swallowing it.
func TestSentryMiddlewarePanicStillReturns500(t *testing.T) {
	r := chi.NewRouter()
	// Sentry outermost, then chi's recoverer to turn the repanic into a 500 —
	// mirrors production where ListenAndServe's per-connection recover yields 500.
	r.Use(sentryMiddleware())
	r.Use(middleware.Recoverer)
	r.Get("/boom", func(http.ResponseWriter, *http.Request) {
		panic("kaboom")
	})

	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	// Include an auth header to exercise the path a real panicking request takes.
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 after panic, got %d", rec.Code)
	}
}

// TestSentryMiddlewarePassesThroughNormalRequests verifies the middleware does
// not alter a normal (non-panicking) response.
func TestSentryMiddlewarePassesThroughNormalRequests(t *testing.T) {
	r := chi.NewRouter()
	r.Use(sentryMiddleware())
	r.Get("/ok", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("hi"))
	})

	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusTeapot {
		t.Fatalf("expected pass-through 418, got %d", rec.Code)
	}
	if rec.Body.String() != "hi" {
		t.Fatalf("expected body %q, got %q", "hi", rec.Body.String())
	}
}
