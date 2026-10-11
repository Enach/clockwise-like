package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/storage"
	"golang.org/x/oauth2"
)

// recordingTransport fails every request and remembers its URL, so a test can
// prove a handler made no outbound call.
type recordingTransport struct {
	mu   sync.Mutex
	urls []string
}

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.urls = append(r.urls, req.URL.String())
	r.mu.Unlock()
	return nil, errors.New("outbound call made from a test")
}

// PAC-65: /api/auth/status runs on every dashboard load. It must answer from
// what the server already knows: no outbound call, and above all not one that
// carries the user's Google access token to Google's userinfo endpoint.
func TestAuthStatus_GoogleConnectedMakesNoOutboundCall(t *testing.T) {
	db := openTestDB(t)
	user, err := storage.UpsertUser(db, "status-user@example.com", "Status User", "", "google", "g-status")
	if err != nil {
		t.Fatalf("upsert user: %v", err)
	}
	if err := auth.UpsertUserToken(db, user.ID, &oauth2.Token{
		AccessToken: "access-token-must-not-leave", RefreshToken: "refresh", Expiry: time.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("store token: %v", err)
	}

	rec := &recordingTransport{}
	orig := http.DefaultTransport
	http.DefaultTransport = rec
	t.Cleanup(func() { http.DefaultTransport = orig })

	h := &authHandlers{oauthConfig: &oauth2.Config{}, db: db}
	req := httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.UserIDKey, user.ID))
	w := httptest.NewRecorder()
	h.status(w, req)

	if len(rec.urls) != 0 {
		t.Fatalf("status made outbound call(s): %v", rec.urls)
	}
	var body struct {
		Connected bool   `json:"connected"`
		Provider  string `json:"provider"`
		Email     string `json:"email"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Connected || body.Provider != "google" || body.Email != "status-user@example.com" {
		t.Fatalf("status = %+v, want connected google with the user's own email", body)
	}
}
