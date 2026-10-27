package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/api"
	"github.com/getsentry/sentry-go"
	"github.com/go-chi/chi/v5"
	"golang.org/x/oauth2"
)

// eventRecorder is a sentry.Transport that keeps every event in memory so a
// test can inspect exactly what would have been sent to Sentry.
type eventRecorder struct {
	mu     sync.Mutex
	events []*sentry.Event
}

func (r *eventRecorder) Configure(sentry.ClientOptions) {}
func (r *eventRecorder) SendEvent(e *sentry.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}
func (r *eventRecorder) Flush(time.Duration) bool                  { return true }
func (r *eventRecorder) FlushWithContext(context.Context) bool { return true }
func (r *eventRecorder) Close()                                    {}

// TestScrubSensitiveDropsQueryString proves the BeforeSend hook clears the
// request query string: on OAuth callbacks it carries the authorization code
// and state (PAC-14 §2, PAC-21 review finding).
func TestScrubSensitiveDropsQueryString(t *testing.T) {
	event := sentry.NewEvent()
	event.Request = &sentry.Request{
		URL:         "https://api.example.test/api/auth/callback",
		Method:      http.MethodGet,
		QueryString: "code=4%2F0AUTHCODE-SECRET&state=S1",
	}
	got := scrubSensitive(event, nil)
	if got.Request.QueryString != "" {
		t.Errorf("query string was not cleared: %q", got.Request.QueryString)
	}
	if strings.Contains(got.Request.URL, "code=") {
		t.Errorf("request URL carries the code: %q", got.Request.URL)
	}
}

// TestOAuthCallbackFailureEventCarriesNoCode drives the real router with the
// production BeforeSend hook: a failed Google token exchange must reach Sentry
// without the authorization code or state anywhere in the request.
func TestOAuthCallbackFailureEventCarriesNoCode(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer tokenSrv.Close()

	rec := &eventRecorder{}
	client, err := sentry.NewClient(sentry.ClientOptions{
		Dsn:        "https://public@sentry.example.test/1",
		Transport:  rec,
		BeforeSend: scrubSensitive,
	})
	if err != nil {
		t.Fatalf("sentry client: %v", err)
	}
	hub := sentry.CurrentHub()
	prev := hub.Client()
	hub.BindClient(client)
	t.Cleanup(func() { hub.BindClient(prev) })

	cfg := &oauth2.Config{
		ClientID:     "id",
		ClientSecret: "secret",
		Endpoint:     oauth2.Endpoint{AuthURL: tokenSrv.URL + "/auth", TokenURL: tokenSrv.URL + "/token"},
		RedirectURL:  "http://localhost/api/auth/callback",
	}
	r := chi.NewRouter()
	api.RegisterRoutes(r, nil, cfg, "jwt-secret", "http://localhost", "http://localhost")

	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?code=4%2F0AUTHCODE-SECRET&state=S1", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "S1"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body %q)", w.Code, w.Body.String())
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.events) == 0 {
		t.Fatal("no Sentry event captured for the failed callback")
	}
	for _, e := range rec.events {
		if e.Request == nil {
			continue
		}
		for _, s := range []string{e.Request.QueryString, e.Request.URL} {
			if strings.Contains(s, "code=") || strings.Contains(s, "AUTHCODE-SECRET") || strings.Contains(s, "state=") {
				t.Errorf("Sentry event leaks OAuth query: %q", s)
			}
		}
	}
}
