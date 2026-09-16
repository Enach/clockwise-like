package api

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
)

// fakeIdP is an OIDC provider that signs an id_token asserting whatever email
// the test sets — the same power an attacker has over an IdP they run.
type fakeIdP struct {
	srv      *httptest.Server
	key      *rsa.PrivateKey
	clientID string
	email    string
}

func newFakeIdP(t *testing.T, clientID string) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	idp := &fakeIdP{key: key, clientID: clientID}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                idp.srv.URL,
			"authorization_endpoint":                idp.srv.URL + "/authorize",
			"token_endpoint":                        idp.srv.URL + "/token",
			"jwks_uri":                              idp.srv.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{
				"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "test",
				"n": b64url(key.N.Bytes()),
				"e": b64url(big.NewInt(int64(key.E)).Bytes()),
			}},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"id_token":     idp.signIDToken(t),
		})
	})
	idp.srv = httptest.NewServer(mux)
	t.Cleanup(idp.srv.Close)
	return idp
}

func (idp *fakeIdP) signIDToken(t *testing.T) string {
	t.Helper()
	now := time.Now()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "test"})
	claims, _ := json.Marshal(map[string]any{
		"iss":   idp.srv.URL,
		"sub":   "idp-subject",
		"aud":   idp.clientID,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
		"email": idp.email,
		"name":  "Attacker",
	})
	signingInput := b64url(header) + "." + b64url(claims)
	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, idp.key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("sign id_token: %v", err)
	}
	return signingInput + "." + b64url(sig)
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// runOIDCCallback configures an SSO provider for providerDomain backed by a
// fake IdP asserting assertedEmail, then drives the callback as the browser
// returning from that IdP would.
func runOIDCCallback(t *testing.T, providerDomain, assertedEmail string) *httptest.ResponseRecorder {
	t.Helper()
	db := openTestDB(t)

	idp := newFakeIdP(t, "client-"+providerDomain)
	idp.email = assertedEmail
	if _, err := storage.UpsertSSOProvider(db, &storage.SSOProvider{
		Domain:           providerDomain,
		ProviderName:     "Attacker IdP",
		ProviderType:     "oidc",
		Enabled:          true,
		OIDCIssuer:       idp.srv.URL,
		OIDCClientID:     idp.clientID,
		OIDCClientSecret: "secret",
	}); err != nil {
		t.Fatalf("UpsertSSOProvider: %v", err)
	}

	h := &ssoHandlers{ah: &authHandlers{db: db, jwtSecret: "test-secret", frontendURL: "http://frontend.test"}}
	r := chi.NewRouter()
	r.Get("/api/auth/callback/oidc/{domain}", h.oidcCallback)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback/oidc/"+providerDomain+"?state=s1&code=c1", nil)
	req.AddCookie(&http.Cookie{Name: "sso_state", Value: "s1|" + providerDomain})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func authCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == "auth_token" && c.Value != "" {
			return c
		}
	}
	return nil
}

// PAC-45: an org member points their own domain's provider row at an IdP they
// run, and that IdP asserts a victim's email from a different domain.
func TestOIDCCallback_RejectsEmailOutsideProviderDomain(t *testing.T) {
	db := openTestDB(t)
	victim, err := storage.UpsertUser(db, "victim@pac45-victim.test", "Victim", "", "google", "google-victim")
	if err != nil {
		t.Fatalf("seed victim: %v", err)
	}

	rec := runOIDCCallback(t, "pac45-attacker.test", "victim@pac45-victim.test")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", rec.Code, rec.Body.String())
	}
	if c := authCookie(rec); c != nil {
		t.Fatalf("auth_token cookie issued for a cross-domain assertion")
	}
	after, err := storage.GetUserByEmail(db, victim.Email)
	if err != nil || after == nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if after.Name != "Victim" {
		t.Errorf("victim row was overwritten: name = %q", after.Name)
	}
}

func TestOIDCCallback_RejectsSubdomainEmail(t *testing.T) {
	rec := runOIDCCallback(t, "pac45-parent.test", "alice@eu.pac45-parent.test")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", rec.Code, rec.Body.String())
	}
	if authCookie(rec) != nil {
		t.Fatal("auth_token cookie issued for a subdomain assertion")
	}
}

func TestOIDCCallback_AcceptsEmailInProviderDomain(t *testing.T) {
	rec := runOIDCCallback(t, "pac45-legit.test", "Alice@PAC45-legit.test")
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.HasPrefix(rec.Header().Get("Location"), "http://frontend.test/auth/callback") {
		t.Errorf("Location = %q", rec.Header().Get("Location"))
	}
	if authCookie(rec) == nil {
		t.Fatal("expected auth_token cookie for a same-domain assertion")
	}
}
