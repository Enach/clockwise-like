package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()

	healthHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %q, want ok", body["status"])
	}
	if body["version"] == "" {
		t.Error("version should not be empty")
	}
}

func TestCORSMiddleware_Options(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := corsMiddleware(next)

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("OPTIONS should return 204, got %d", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("missing CORS origin header")
	}
}

func TestCORSMiddleware_GET(t *testing.T) {
	var called bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := corsMiddleware(next)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Error("next handler should have been called")
	}
	if w.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("missing CORS methods header")
	}
}

func TestLoggingMiddleware(t *testing.T) {
	var called bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := loggingMiddleware(next)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Error("next handler should have been called")
	}
}

func TestApiStartOfWeek(t *testing.T) {
	cases := []struct {
		name string
		in   time.Time
		want time.Weekday
	}{
		{"Wednesday", time.Date(2025, 1, 8, 15, 0, 0, 0, time.UTC), time.Monday},
		{"Monday", time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC), time.Monday},
		{"Sunday", time.Date(2025, 1, 12, 0, 0, 0, 0, time.UTC), time.Monday},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := startOfWeek(tc.in)
			if got.Weekday() != tc.want {
				t.Errorf("startOfWeek(%v) = %v, want %v", tc.in, got.Weekday(), tc.want)
			}
			if got.Hour() != 0 {
				t.Errorf("startOfWeek should return midnight, got hour=%d", got.Hour())
			}
		})
	}
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, "something went wrong", http.StatusBadRequest)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Error("Content-Type should be application/json")
	}

	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["error"] != "something went wrong" {
		t.Errorf("error = %q, want something went wrong", body["error"])
	}
}
