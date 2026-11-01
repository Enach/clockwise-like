package api

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// PAC-50 test harness: a stand-in for Google and for Microsoft that signs ID
// tokens with a test key served from its own JWKS address (spec §5, last
// paragraph), a mutable clock, and a router mounting the SSO admin routes
// behind the real session middleware.

const pac50Secret = "pac50-test-secret"

type pac50Clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *pac50Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *pac50Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

// stepupIdP is one identity provider stand-in. Every field is read at
// request time, so a test changes the answer before driving the callback.
type stepupIdP struct {
	t        *testing.T
	kind     string // "google" | "microsoft"
	srv      *httptest.Server
	key      *rsa.PrivateKey
	clientID string

	mu     sync.Mutex
	claims map[string]any // ID-token claims; a nil value omits the claim
	nonce  string
	// Google Directory answer.
	dirStatus             int
	isAdmin, isDelegAdmin bool
	// Microsoft Graph answers.
	orgStatus   int
	orgID       string
	verified    []string
	rolesStatus int
	roles       []string
	tokenStatus int
}

func newStepupIdP(t *testing.T, kind string) *stepupIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	idp := &stepupIdP{t: t, kind: kind, key: key, clientID: kind + "-stepup-client",
		claims: map[string]any{}, dirStatus: 200, orgStatus: 200, rolesStatus: 200, tokenStatus: 200}
	jwks := func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "k1",
			"n": b64url(key.N.Bytes()), "e": b64url(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}
	token := func(w http.ResponseWriter, r *http.Request) {
		idp.mu.Lock()
		status := idp.tokenStatus
		idp.mu.Unlock()
		if status != 200 {
			http.Error(w, `{"error":"invalid_grant"}`, status)
			return
		}
		if err := r.ParseForm(); err != nil || r.PostForm.Get("code") == "" {
			http.Error(w, `{"error":"invalid_request"}`, 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "stepup-access-" + kind, "token_type": "Bearer", "expires_in": 3600,
			"refresh_token": "stepup-refresh-" + kind, "id_token": idp.sign(),
		})
	}
	mux := http.NewServeMux()
	if kind == "google" {
		mux.HandleFunc("/oauth2/v3/certs", jwks)
		mux.HandleFunc("/token", token)
		mux.HandleFunc("/admin/directory/v1/users/", func(w http.ResponseWriter, r *http.Request) {
			idp.mu.Lock()
			defer idp.mu.Unlock()
			if r.Header.Get("Authorization") != "Bearer stepup-access-google" {
				http.Error(w, "no bearer", 401)
				return
			}
			if idp.dirStatus != 200 {
				http.Error(w, `{"error":{"code":403}}`, idp.dirStatus)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"primaryEmail": strings.TrimPrefix(r.URL.Path, "/admin/directory/v1/users/"),
				"isAdmin":      idp.isAdmin, "isDelegatedAdmin": idp.isDelegAdmin,
			})
		})
	} else {
		mux.HandleFunc("/common/discovery/v2.0/keys", jwks)
		mux.HandleFunc("/organizations/oauth2/v2.0/token", token)
		mux.HandleFunc("/v1.0/organization", func(w http.ResponseWriter, r *http.Request) {
			idp.mu.Lock()
			defer idp.mu.Unlock()
			if r.Header.Get("Authorization") != "Bearer stepup-access-microsoft" || idp.orgStatus != 200 {
				http.Error(w, `{"error":{}}`, 403)
				return
			}
			doms := make([]map[string]any, 0, len(idp.verified))
			for _, d := range idp.verified {
				doms = append(doms, map[string]any{"name": d, "isDefault": false})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"value": []map[string]any{{"id": idp.orgID, "verifiedDomains": doms}}})
		})
		mux.HandleFunc("/v1.0/me/memberOf/microsoft.graph.directoryRole", func(w http.ResponseWriter, r *http.Request) {
			idp.mu.Lock()
			defer idp.mu.Unlock()
			if r.Header.Get("Authorization") != "Bearer stepup-access-microsoft" || idp.rolesStatus != 200 {
				http.Error(w, `{"error":{}}`, 403)
				return
			}
			vals := make([]map[string]any, 0, len(idp.roles))
			for _, id := range idp.roles {
				vals = append(vals, map[string]any{"roleTemplateId": id})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"value": vals})
		})
	}
	idp.srv = httptest.NewServer(mux)
	t.Cleanup(idp.srv.Close)
	return idp
}

func (idp *stepupIdP) endpoints() config.IdPEndpoint {
	if idp.kind == "google" {
		return config.GoogleIdPAt(idp.srv.URL)
	}
	return config.MicrosoftIdPAt(idp.srv.URL)
}

func (idp *stepupIdP) set(f func(*stepupIdP)) {
	idp.mu.Lock()
	defer idp.mu.Unlock()
	f(idp)
}

func (idp *stepupIdP) sign() string {
	idp.mu.Lock()
	defer idp.mu.Unlock()
	now := time.Now()
	c := map[string]any{
		"sub": "idp-subject", "aud": idp.clientID, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
		"nonce": idp.nonce,
	}
	if idp.kind == "google" {
		c["iss"] = idp.srv.URL
	}
	for k, v := range idp.claims {
		c[k] = v
	}
	if idp.kind == "microsoft" {
		if tid, ok := c["tid"].(string); ok {
			c["iss"] = idp.srv.URL + "/" + tid + "/v2.0"
		}
	}
	for k, v := range c {
		if v == nil {
			delete(c, k)
		}
	}
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "k1"})
	body, _ := json.Marshal(c)
	in := b64url(header) + "." + b64url(body)
	sum := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(rand.Reader, idp.key, crypto.SHA256, sum[:])
	if err != nil {
		idp.t.Fatalf("sign: %v", err)
	}
	return in + "." + b64url(sig)
}

// ssoEnv is one handler set over the shared test database.
type ssoEnv struct {
	db     *sql.DB
	h      *ssoHandlers
	router http.Handler
	clock  *pac50Clock
	google *stepupIdP
	ms     *stepupIdP
}

const pac50Frontend = "http://frontend.test"
const pac50AppURL = "http://api.test"

func newSSOEnv(t *testing.T, breakGlass ...string) *ssoEnv {
	t.Helper()
	db := openTestDB(t)
	e := &ssoEnv{db: db, clock: &pac50Clock{t: time.Now().UTC()},
		google: newStepupIdP(t, "google"), ms: newStepupIdP(t, "microsoft")}
	ah := &authHandlers{db: db, jwtSecret: pac50Secret, frontendURL: pac50Frontend}
	e.h = newSSOHandlers(ah, ssoOptions{
		Clock:      e.clock,
		BreakGlass: strings.Join(breakGlass, ","),
		Google:     auth.NewGoogleStepup(e.google.clientID, "google-secret", pac50AppURL+stepupCallbackPath, e.google.endpoints()),
		Microsoft:  auth.NewMicrosoftStepup(e.ms.clientID, "ms-secret", pac50AppURL+stepupCallbackPath, e.ms.endpoints()),
	})
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(requireAuth(pac50Secret))
		r.Route("/api/admin/sso", e.h.mountAdmin)
	})
	e.router = r
	return e
}

// member creates a user whose org is a fresh domain and returns both.
func (e *ssoEnv) member(t *testing.T, label string) (*storage.User, *storage.Org) {
	t.Helper()
	d := "pac50-" + label + "-" + uuid.NewString()[:8] + ".test"
	return e.memberOf(t, "admin@"+d)
}

func (e *ssoEnv) memberOf(t *testing.T, email string) (*storage.User, *storage.Org) {
	t.Helper()
	u, err := storage.UpsertUser(e.db, email, "Member", "", "google", "sub-"+email)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.AssociateUserWithOrg(e.db, u.ID, email); err != nil {
		t.Fatal(err)
	}
	u, err = storage.GetUserByID(e.db, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if u.OrgID == nil {
		return u, nil
	}
	o, err := storage.GetOrgByID(e.db, *u.OrgID)
	if err != nil {
		t.Fatal(err)
	}
	return u, o
}

// session mints a session JWT for u issued at iat (spec OQ-7, AC-14).
func session(t *testing.T, u *storage.User, iat time.Time) string {
	t.Helper()
	c := auth.Claims{UserID: u.ID.String(), Email: u.Email, RegisteredClaims: jwt.RegisteredClaims{
		IssuedAt: jwt.NewNumericDate(iat), ExpiresAt: jwt.NewNumericDate(iat.Add(7 * 24 * time.Hour)),
	}}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(pac50Secret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (e *ssoEnv) do(t *testing.T, method, path, body, sess string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if sess != "" {
		req.AddCookie(&http.Cookie{Name: "auth_token", Value: sess})
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func createBody(domain string) string {
	return `{"domain":"` + domain + `","provider_name":"Okta","provider_type":"oidc","oidc_issuer":"https://idp.example","oidc_client_id":"cid","oidc_client_secret":"sec"}`
}

func stateCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == "sso_stepup_state" {
			return c
		}
	}
	return nil
}

// stepup drives the whole round-trip as the browser would: start, then the
// provider's redirect back to the callback with the state and a code. The
// stand-in echoes the nonce the start sent, as a real provider does.
func (e *ssoEnv) stepup(t *testing.T, provider, sess string) *httptest.ResponseRecorder {
	t.Helper()
	start := e.do(t, http.MethodGet, "/api/admin/sso/stepup?provider="+provider, "", sess)
	if start.Code != http.StatusFound {
		t.Fatalf("start = %d %s", start.Code, start.Body.String())
	}
	loc, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	idp := e.google
	if provider == "microsoft" {
		idp = e.ms
	}
	idp.set(func(i *stepupIdP) { i.nonce = loc.Query().Get("nonce") })
	return e.do(t, http.MethodGet, stepupCallbackPath+"?code=c1&state="+url.QueryEscape(loc.Query().Get("state")), "", sess, stateCookie(start))
}

type ssoEventRow struct {
	Kind    string
	Details map[string]any
}

func (e *ssoEnv) events(t *testing.T, u uuid.UUID) []ssoEventRow {
	t.Helper()
	rows, err := e.db.Query(`SELECT kind, details FROM sso_events WHERE user_id = $1 ORDER BY created_at, id`, u)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []ssoEventRow
	for rows.Next() {
		var k string
		var raw []byte
		if err := rows.Scan(&k, &raw); err != nil {
			t.Fatal(err)
		}
		var d map[string]any
		if err := json.Unmarshal(raw, &d); err != nil {
			t.Fatalf("details are not JSON: %v (%s)", err, raw)
		}
		out = append(out, ssoEventRow{Kind: k, Details: d})
	}
	return out
}

func (e *ssoEnv) liveGrants(t *testing.T, u uuid.UUID) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT count(*) FROM sso_stepup_grants WHERE user_id = $1 AND consumed_at IS NULL`, u).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *ssoEnv) providerRow(t *testing.T, d string) (int, time.Time) {
	t.Helper()
	var n int
	var updated sql.NullTime
	if err := e.db.QueryRow(`SELECT count(*), max(updated_at) FROM sso_providers WHERE lower(domain) = lower($1)`, d).Scan(&n, &updated); err != nil {
		t.Fatal(err)
	}
	return n, updated.Time
}

func errorBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("body is not JSON: %v (%q)", err, rec.Body.String())
	}
	return m
}

// bothProvidersStepupBody is the step-up 403 when Google and Microsoft are
// both configured (newSSOEnv), keys in encoding/json's sorted order.
const bothProvidersStepupBody = `{"error":"sso_write_requires_admin_stepup",` +
	`"stepup_scopes":{"google":["https://www.googleapis.com/auth/admin.directory.user.readonly"],` +
	`"microsoft":["Organization.Read.All","RoleManagement.Read.Directory"]},` +
	`"stepup_url":"/api/admin/sso/stepup","without_stepup":["sso_configuration_writes"]}`

// wantDomainMismatch asserts the owner's 403 for a provider domain that is
// not exactly the caller's org domain (spec §9 D-10): status 403 and the
// body exactly {"error":"provider_domain_mismatch"}.
func wantDomainMismatch(t *testing.T, what string, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusForbidden || strings.TrimSpace(rec.Body.String()) != `{"error":"provider_domain_mismatch"}` {
		t.Fatalf("%s = %d %s, want 403 {\"error\":\"provider_domain_mismatch\"}", what, rec.Code, rec.Body.String())
	}
}
