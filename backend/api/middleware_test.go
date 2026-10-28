package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Enach/paceday/backend/auth"
	"github.com/google/uuid"
)

// PAC-39 / API-008: requireAuth prefers the Authorization header over the
// auth_token cookie, so a stale cookie can no longer shadow a valid bearer token.

const mwTestSecret = "middleware-test-secret"

func mwToken(t *testing.T, user uuid.UUID, secret string) string {
	t.Helper()
	tok, err := auth.GenerateToken(user, user.String()+"@example.com", secret)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	return tok
}

// whoami runs requireAuth around a handler that echoes the authenticated user id.
func whoami(t *testing.T, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	h := requireAuth(openTestDB(t), mwTestSecret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(userIDFromCtx(r.Context()).String()))
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestRequireAuth_StaleCookieAndValidBearer_UsesBearer(t *testing.T) {
	bearerUser := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	// A cookie signed with another secret is what a stale session looks like
	// after a secret rotation; an expired token fails validation the same way.
	req.AddCookie(&http.Cookie{Name: "auth_token", Value: mwToken(t, uuid.New(), "old-secret")})
	req.Header.Set("Authorization", "Bearer "+mwToken(t, bearerUser, mwTestSecret))

	w := whoami(t, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %q", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != bearerUser.String() {
		t.Fatalf("authenticated as %q, want bearer user %q", got, bearerUser)
	}
}

func TestRequireAuth_ValidCookieAndValidBearer_BearerIdentityWins(t *testing.T) {
	cookieUser, bearerUser := uuid.New(), uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.AddCookie(&http.Cookie{Name: "auth_token", Value: mwToken(t, cookieUser, mwTestSecret)})
	req.Header.Set("Authorization", "Bearer "+mwToken(t, bearerUser, mwTestSecret))

	w := whoami(t, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %q", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != bearerUser.String() {
		t.Fatalf("authenticated as %q, want bearer user %q", got, bearerUser)
	}
}

// An explicit but invalid bearer token is rejected rather than silently
// falling back to the cookie: the client asked to be that identity.
func TestRequireAuth_InvalidBearerAndValidCookie_Rejected(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.AddCookie(&http.Cookie{Name: "auth_token", Value: mwToken(t, uuid.New(), mwTestSecret)})
	req.Header.Set("Authorization", "Bearer not-a-jwt")

	if w := whoami(t, req); w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestRequireAuth_CookieOnly_StillAccepted(t *testing.T) {
	user := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.AddCookie(&http.Cookie{Name: "auth_token", Value: mwToken(t, user, mwTestSecret)})

	w := whoami(t, req)
	if w.Code != http.StatusOK || w.Body.String() != user.String() {
		t.Fatalf("status = %d body %q, want 200 %q", w.Code, w.Body.String(), user)
	}
}

func TestRequireAuth_NonBearerAuthorizationFallsBackToCookie(t *testing.T) {
	user := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.AddCookie(&http.Cookie{Name: "auth_token", Value: mwToken(t, user, mwTestSecret)})
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")

	w := whoami(t, req)
	if w.Code != http.StatusOK || w.Body.String() != user.String() {
		t.Fatalf("status = %d body %q, want 200 %q", w.Code, w.Body.String(), user)
	}
}

func TestRequireAuth_NoCredentials_Unauthorized(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if w := whoami(t, req); w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}
