package api

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// PAC-163: Retry-After is not CORS-safelisted, so the public booking 429 was
// unreadable to a page on another origin even though the server always sets
// the header (allowPublicBooking) and the contract declares it required. The
// CORS middleware now sends Access-Control-Expose-Headers: Retry-After.

// exposesRetryAfter reports whether the response's
// Access-Control-Expose-Headers names Retry-After. It parses the list rather
// than comparing the whole value, so adding another legitimately-needed
// header later does not break these tests.
func exposesRetryAfter(h http.Header) bool {
	for _, v := range h.Values("Access-Control-Expose-Headers") {
		for _, name := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(name), "Retry-After") {
				return true
			}
		}
	}
	return false
}

// TestPAC163_CORSExposesRetryAfterInBothShapes pins the header onto both CORS
// shapes, and pins that the shapes themselves are untouched: wildcard origin
// without credentials, or exactly the configured origin with credentials.
func TestPAC163_CORSExposesRetryAfterInBothShapes(t *testing.T) {
	const configured = "https://app.example"
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	cases := []struct {
		name            string
		allowedOrigin   string
		requestOrigin   string
		method          string
		wantAllowOrigin string
		wantCredentials string
	}{
		// The wildcard shape: any origin, never credentials.
		{"wildcard star", "*", "https://anywhere.example", http.MethodGet, "*", ""},
		{"wildcard empty config", "", "https://anywhere.example", http.MethodGet, "*", ""},
		{"wildcard preflight", "*", "https://anywhere.example", http.MethodOptions, "*", ""},
		// The exact-origin shape: the configured origin, with credentials.
		{"exact origin", configured, configured, http.MethodGet, configured, "true"},
		{"exact origin preflight", configured, configured, http.MethodOptions, configured, "true"},
		// A non-matching origin gets no Access-Control-Allow-Origin, so the
		// browser blocks the response whatever else it carries. Unchanged.
		{"other origin refused", configured, "https://evil.example", http.MethodGet, "", "true"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, "/api/book/whatever", nil)
			req.Header.Set("Origin", c.requestOrigin)
			w := httptest.NewRecorder()
			corsMiddleware(c.allowedOrigin)(ok).ServeHTTP(w, req)

			if !exposesRetryAfter(w.Header()) {
				t.Errorf("Access-Control-Expose-Headers = %q, want it to name Retry-After",
					w.Header().Get("Access-Control-Expose-Headers"))
			}
			if got := w.Header().Get("Access-Control-Allow-Origin"); got != c.wantAllowOrigin {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, c.wantAllowOrigin)
			}
			if got := w.Header().Get("Access-Control-Allow-Credentials"); got != c.wantCredentials {
				t.Errorf("Access-Control-Allow-Credentials = %q, want %q", got, c.wantCredentials)
			}
		})
	}
}

// exhaustBookingBudget leaves the limiter with no budget for key, so the next
// request from that client is the refusal. The production limits are restored
// by resetBookingLimiters' cleanup.
func exhaustBookingBudget(t *testing.T, l *fixedWindowLimiter, key string) {
	t.Helper()
	l.setLimit(1)
	if ok, _ := l.take(key); !ok {
		t.Fatalf("the limiter refused the first request for %s", key)
	}
}

// TestPAC163_CrossOrigin429ExposesRetryAfter is the end of the problem: on
// each of the three rate-limited public booking routes, a cross-origin caller
// now gets a 429 whose Retry-After it may actually read.
//
// The limit is the first thing each handler does, before the link lookup and
// before the body (429 precedes every other status), so a refused request
// never reaches the database and this test needs none.
func TestPAC163_CrossOrigin429ExposesRetryAfter(t *testing.T) {
	const webOrigin = "https://app.example"
	routes := []struct {
		name    string
		method  string
		path    string
		limiter *fixedWindowLimiter
		maxWait int // the contract's bound for this response
	}{
		{"getPublicLinkInfo", http.MethodGet, "/api/book/some-slug", bookingSlotsLimiter, 60},
		{"getPublicBookingSlots", http.MethodGet, "/api/book/some-slug/slots", bookingSlotsLimiter, 60},
		{"createPublicBooking", http.MethodPost, "/api/book/some-slug", bookingCreateLimiter, 600},
	}
	// Both CORS deployments a cross-origin caller can meet.
	shapes := []struct {
		name            string
		allowedOrigin   string
		wantAllowOrigin string
	}{
		{"exact origin with credentials", webOrigin, webOrigin},
		{"wildcard without credentials", "*", "*"},
	}

	for _, shape := range shapes {
		for _, route := range routes {
			t.Run(shape.name+"/"+route.name, func(t *testing.T) {
				resetBookingLimiters(t)
				// A nil database is never read: the 429 short-circuits first.
				h := newBookingHandlersWithEngine(nil, nil)
				mux := chi.NewRouter()
				mux.Get("/api/book/{slug}", h.getLinkInfo)
				mux.Get("/api/book/{slug}/slots", h.getSlots)
				mux.Post("/api/book/{slug}", h.createBooking)
				handler := corsMiddleware(shape.allowedOrigin)(mux)

				exhaustBookingBudget(t, route.limiter, "203.0.113.10")

				req := httptest.NewRequest(route.method, route.path, nil)
				req.RemoteAddr = peerA
				req.Header.Set("Origin", webOrigin)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)

				if w.Code != http.StatusTooManyRequests {
					t.Fatalf("status = %d, want 429; body: %s", w.Code, w.Body.String())
				}
				// The value is there, in the contract's bounds...
				secs, err := strconv.Atoi(w.Header().Get("Retry-After"))
				if err != nil {
					t.Fatalf("Retry-After = %q, want an integer", w.Header().Get("Retry-After"))
				}
				if secs < 1 || secs > route.maxWait {
					t.Fatalf("Retry-After = %d, want 1..%d", secs, route.maxWait)
				}
				// ...and the cross-origin caller is allowed to read it.
				if !exposesRetryAfter(w.Header()) {
					t.Fatalf("429 does not expose Retry-After: Access-Control-Expose-Headers = %q",
						w.Header().Get("Access-Control-Expose-Headers"))
				}
				if got := w.Header().Get("Access-Control-Allow-Origin"); got != shape.wantAllowOrigin {
					t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, shape.wantAllowOrigin)
				}
			})
		}
	}
}

// TestPAC163_SameOrigin429Unchanged: a same-origin page never needed the
// exposed list, and its 429 is what it was — 429, the JSON envelope and the
// header, with the wildcard CORS an unconfigured deployment sends.
func TestPAC163_SameOrigin429Unchanged(t *testing.T) {
	resetBookingLimiters(t)
	h := newBookingHandlersWithEngine(nil, nil)
	mux := chi.NewRouter()
	mux.Get("/api/book/{slug}", h.getLinkInfo)
	handler := corsMiddleware("*")(mux)

	exhaustBookingBudget(t, bookingSlotsLimiter, "203.0.113.10")

	// No Origin header at all: the same-origin case.
	req := httptest.NewRequest(http.MethodGet, "/api/book/some-slug", nil)
	req.RemoteAddr = peerA
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := strings.TrimSpace(w.Body.String()); got != `{"error":"rate limit exceeded"}` {
		t.Errorf("body = %s, want the rate-limit envelope", got)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("Retry-After is missing")
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want none with the wildcard origin", got)
	}
}
