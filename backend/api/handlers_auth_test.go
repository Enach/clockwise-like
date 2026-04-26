package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

func TestAuthStatus_NotConnected(t *testing.T) {
	db := openTestDB(t)
	h := &authHandlers{
		oauthConfig: &oauth2.Config{},
		db:          db,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
	w := httptest.NewRecorder()
	h.status(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if body == "" {
		t.Error("body should not be empty")
	}
}

func TestAuthDisconnect(t *testing.T) {
	db := openTestDB(t)
	h := &authHandlers{
		oauthConfig: &oauth2.Config{},
		db:          db,
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/auth/disconnect", nil)
	w := httptest.NewRecorder()
	h.disconnect(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestStartOAuth(t *testing.T) {
	db := openTestDB(t)
	h := &authHandlers{
		oauthConfig: &oauth2.Config{
			ClientID:     "test-client",
			ClientSecret: "test-secret",
			Endpoint:     google.Endpoint,
			Scopes:       []string{"openid", "email"},
		},
		db: db,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/google", nil)
	w := httptest.NewRecorder()
	h.startOAuth(w, req)

	if w.Code != http.StatusFound {
		t.Errorf("status = %d, want 302", w.Code)
	}
	loc := w.Header().Get("Location")
	if loc == "" {
		t.Error("Location header should not be empty")
	}
}

func TestCallback_InvalidState(t *testing.T) {
	db := openTestDB(t)
	h := &authHandlers{oauthConfig: &oauth2.Config{}, db: db}

	// No state cookie → should return 400
	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?state=wrong&code=code", nil)
	w := httptest.NewRecorder()
	h.callback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}
