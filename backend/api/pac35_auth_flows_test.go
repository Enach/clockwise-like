package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// PAC-35: sign-in and sign-out flows that fail, mislead or log the user out.
// Spec docs/specs/PAC-35.md, contract contracts/features/PAC-35.md.

const pac35Frontend = "http://frontend.test"
const pac35FailureURL = pac35Frontend + "/?error=auth_failed"

// ---- shared helpers --------------------------------------------------------

// setCookieNamed returns every Set-Cookie header of rec that names cookie.
func setCookieNamed(rec *httptest.ResponseRecorder, cookie string) []string {
	var out []string
	for _, h := range rec.Result().Header.Values("Set-Cookie") {
		if strings.HasPrefix(h, cookie+"=") {
			out = append(out, h)
		}
	}
	return out
}

// assertSignInFailed checks the failure state of the contract's 302: Location
// is exactly the fixed failure URL and no auth_token cookie is set or expired.
func assertSignInFailed(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != pac35FailureURL {
		t.Fatalf("Location = %q, want %q", loc, pac35FailureURL)
	}
	if got := setCookieNamed(rec, "auth_token"); len(got) != 0 {
		t.Errorf("the failure redirect touched auth_token: %v", got)
	}
}

func assertSignedIn(t *testing.T, rec *httptest.ResponseRecorder, wantLocation string) {
	t.Helper()
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != wantLocation {
		t.Fatalf("Location = %q, want %q", loc, wantLocation)
	}
	if authCookie(rec) == nil {
		t.Fatal("no auth_token cookie on a successful sign-in")
	}
}

// failingTokenGenerator swaps the session-token seam for one that fails, the
// "token generation fails with a non-empty secret" case of AC-1..3.
func failingTokenGenerator(t *testing.T) {
	t.Helper()
	prev := generateSessionToken
	generateSessionToken = func(uuid.UUID, string, string) (string, error) {
		return "", errors.New("signing failed")
	}
	t.Cleanup(func() { generateSessionToken = prev })
}

// closedDB is a database handle whose every query fails.
func closedDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", "postgres://pac35:pac35@127.0.0.1:1/none")
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	return db
}

// fakeOAuthProvider is a token endpoint plus a profile endpoint, standing in
// for Google or Microsoft after the user consented.
type fakeOAuthProvider struct {
	srv     *httptest.Server
	profile func(w http.ResponseWriter)
}

func newFakeOAuthProvider(t *testing.T) *fakeOAuthProvider {
	t.Helper()
	p := &fakeOAuthProvider{}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at-" + uuid.NewString(), "refresh_token": "rt", "token_type": "Bearer", "expires_in": 3600,
		})
	})
	mux.HandleFunc("/profile", func(w http.ResponseWriter, _ *http.Request) { p.profile(w) })
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func (p *fakeOAuthProvider) endpoint() oauth2.Endpoint {
	return oauth2.Endpoint{AuthURL: p.srv.URL + "/authorize", TokenURL: p.srv.URL + "/token", AuthStyle: oauth2.AuthStyleInParams}
}

func jsonProfile(v any) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
}

// ---- AC-1: Google callback -------------------------------------------------

func runGoogleCallback(t *testing.T, ah *authHandlers, p *fakeOAuthProvider) *httptest.ResponseRecorder {
	t.Helper()
	prev := googleUserInfoURL
	googleUserInfoURL = p.srv.URL + "/profile"
	t.Cleanup(func() { googleUserInfoURL = prev })
	ah.oauthConfig = &oauth2.Config{ClientID: "g", ClientSecret: "s", RedirectURL: "http://localhost/cb", Endpoint: p.endpoint()}
	ah.frontendURL = pac35Frontend
	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?state=s1&code=c1", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "s1"})
	rec := httptest.NewRecorder()
	ah.callback(rec, req)
	return rec
}

func TestPAC35_GoogleCallback_EmptySecretGoesToFailureState(t *testing.T) {
	db := openTestDB(t)
	p := newFakeOAuthProvider(t)
	p.profile = jsonProfile(map[string]string{"id": "g-ac1", "email": "pac35-ac1-google@example.com", "name": "G", "picture": "https://img.test/g.png"})

	rec := runGoogleCallback(t, &authHandlers{db: db, jwtSecret: ""}, p)
	assertSignInFailed(t, rec)

	// The user and token written before the session step are kept.
	u, err := storage.GetUserByEmail(db, "pac35-ac1-google@example.com")
	if err != nil || u == nil {
		t.Fatalf("user not kept: %v", err)
	}
	if tok, _ := auth.LoadUserToken(db, u.ID); tok == nil {
		t.Error("Google token not kept on the failure path")
	}
}

func TestPAC35_GoogleCallback_TokenGenerationFailureGoesToFailureState(t *testing.T) {
	failingTokenGenerator(t)
	p := newFakeOAuthProvider(t)
	p.profile = jsonProfile(map[string]string{"id": "g-ac1b", "email": "pac35-ac1b-google@example.com", "name": "G"})
	assertSignInFailed(t, runGoogleCallback(t, &authHandlers{db: openTestDB(t), jwtSecret: "secret"}, p))
}

func TestPAC35_GoogleCallback_SuccessStillLandsOnCompletionPage(t *testing.T) {
	p := newFakeOAuthProvider(t)
	p.profile = jsonProfile(map[string]string{"id": "g-ok", "email": "pac35-ok-google@example.com", "name": "G"})
	rec := runGoogleCallback(t, &authHandlers{db: openTestDB(t), jwtSecret: "secret"}, p)
	assertSignedIn(t, rec, pac35Frontend+"/auth/callback")
}

// ---- AC-2, AC-4: Microsoft callback ----------------------------------------

func runMicrosoftCallback(t *testing.T, ah *authHandlers, p *fakeOAuthProvider) *httptest.ResponseRecorder {
	t.Helper()
	t.Setenv("MICROSOFT_CLIENT_ID", "ms-client")
	t.Setenv("MICROSOFT_CLIENT_SECRET", "ms-secret")
	t.Setenv("MICROSOFT_REDIRECT_URL", "http://localhost/ms/cb")
	prevEndpoint, prevGraph := microsoftOAuthEndpoint, microsoftGraphMeURL
	microsoftOAuthEndpoint = p.endpoint()
	microsoftGraphMeURL = p.srv.URL + "/profile"
	t.Cleanup(func() { microsoftOAuthEndpoint, microsoftGraphMeURL = prevEndpoint, prevGraph })
	ah.frontendURL = pac35Frontend
	req := httptest.NewRequest(http.MethodGet, "/api/auth/microsoft/callback?state=s1&code=c1", nil)
	req.AddCookie(&http.Cookie{Name: "ms_oauth_state", Value: "s1"})
	rec := httptest.NewRecorder()
	ah.microsoftCallback(rec, req)
	return rec
}

func TestPAC35_MicrosoftCallback_EmptySecretGoesToFailureState(t *testing.T) {
	db := openTestDB(t)
	p := newFakeOAuthProvider(t)
	p.profile = jsonProfile(map[string]string{"id": "ms-ac2", "mail": "pac35-ac2-ms@example.com", "displayName": "M"})
	assertSignInFailed(t, runMicrosoftCallback(t, &authHandlers{db: db, jwtSecret: ""}, p))

	u, err := storage.GetUserByEmail(db, "pac35-ac2-ms@example.com")
	if err != nil || u == nil {
		t.Fatalf("user not kept: %v", err)
	}
	if tok, _ := auth.LoadMicrosoftToken(db, u.ID); tok == nil {
		t.Error("Microsoft token not kept on the failure path")
	}
}

func TestPAC35_MicrosoftCallback_TokenGenerationFailureGoesToFailureState(t *testing.T) {
	failingTokenGenerator(t)
	p := newFakeOAuthProvider(t)
	p.profile = jsonProfile(map[string]string{"id": "ms-ac2b", "mail": "pac35-ac2b-ms@example.com", "displayName": "M"})
	assertSignInFailed(t, runMicrosoftCallback(t, &authHandlers{db: openTestDB(t), jwtSecret: "secret"}, p))
}

func TestPAC35_MicrosoftCallback_ProfileUnreadableGoesToFailureState(t *testing.T) {
	cases := map[string]func(http.ResponseWriter){
		"profile endpoint fails": func(w http.ResponseWriter) { http.Error(w, "boom", http.StatusInternalServerError) },
		"profile has no email":   jsonProfile(map[string]string{"id": "ms-noemail", "displayName": "M"}),
	}
	for name, profile := range cases {
		t.Run(name, func(t *testing.T) {
			p := newFakeOAuthProvider(t)
			p.profile = profile
			rec := runMicrosoftCallback(t, &authHandlers{db: openTestDB(t), jwtSecret: "secret"}, p)
			assertSignInFailed(t, rec)
			if rec.Body.Len() > 0 && strings.Contains(rec.Body.String(), "Microsoft") {
				t.Errorf("failure redirect carries server detail: %q", rec.Body.String())
			}
		})
	}
}

func TestPAC35_MicrosoftCallback_UpsertFailureGoesToFailureState(t *testing.T) {
	p := newFakeOAuthProvider(t)
	p.profile = jsonProfile(map[string]string{"id": "ms-up", "mail": "pac35-upsert-ms@example.com", "displayName": "M"})
	assertSignInFailed(t, runMicrosoftCallback(t, &authHandlers{db: closedDB(t), jwtSecret: "secret"}, p))
}

func TestPAC35_MicrosoftCallback_SuccessStillLandsOnCompletionPage(t *testing.T) {
	p := newFakeOAuthProvider(t)
	p.profile = jsonProfile(map[string]string{"id": "ms-ok", "mail": "pac35-ok-ms@example.com", "displayName": "M"})
	rec := runMicrosoftCallback(t, &authHandlers{db: openTestDB(t), jwtSecret: "secret"}, p)
	assertSignedIn(t, rec, pac35Frontend+"/auth/callback?provider=outlook")
}

func TestPAC35_MicrosoftCallback_InvalidStateStays400(t *testing.T) {
	ah := &authHandlers{db: openTestDB(t), jwtSecret: "secret", frontendURL: pac35Frontend}
	req := httptest.NewRequest(http.MethodGet, "/api/auth/microsoft/callback?state=a&code=c", nil)
	req.AddCookie(&http.Cookie{Name: "ms_oauth_state", Value: "b"})
	rec := httptest.NewRecorder()
	ah.microsoftCallback(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// ---- AC-3, AC-18: SSO (OIDC) callback --------------------------------------

func seedOIDCProvider(t *testing.T, d string, idp *fakeIdP) {
	t.Helper()
	if _, err := storage.UpsertSSOProvider(openTestDB(t), &storage.SSOProvider{
		Domain: d, ProviderName: "Acme IdP", ProviderType: "oidc", Enabled: true,
		OIDCIssuer: idp.srv.URL, OIDCClientID: idp.clientID, OIDCClientSecret: "secret",
	}); err != nil {
		t.Fatalf("UpsertSSOProvider: %v", err)
	}
}

func ssoRouter(ah *authHandlers) *chi.Mux {
	h := &ssoHandlers{ah: ah}
	r := chi.NewRouter()
	r.Post("/api/auth/detect", h.detect)
	r.Get("/api/auth/sso/{domain}", h.startSSO)
	r.Get("/api/auth/callback/oidc/{domain}", h.oidcCallback)
	return r
}

func oidcCallbackRequest(d string, cookie *http.Cookie) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback/oidc/"+d+"?state=s1&code=c1", nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	return req
}

func TestPAC35_OIDCCallback_EveryDocumentedStatus(t *testing.T) {
	db := openTestDB(t)
	const d = "pac35-oidc.test"
	idp := newFakeIdP(t, "client-"+d)
	seedOIDCProvider(t, d, idp)
	okCookie := &http.Cookie{Name: "sso_state", Value: "s1|" + d}

	serve := func(ah *authHandlers, req *http.Request) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		ssoRouter(ah).ServeHTTP(rec, req)
		return rec
	}
	withSecret := &authHandlers{db: db, jwtSecret: "secret", frontendURL: pac35Frontend}

	t.Run("302 success", func(t *testing.T) {
		idp.email = "alice@" + d
		assertSignedIn(t, serve(withSecret, oidcCallbackRequest(d, okCookie)), pac35Frontend+"/auth/callback?provider=sso")
	})
	t.Run("302 failure redirect, empty secret (AC-3)", func(t *testing.T) {
		idp.email = "bob@" + d
		assertSignInFailed(t, serve(&authHandlers{db: db, jwtSecret: "", frontendURL: pac35Frontend}, oidcCallbackRequest(d, okCookie)))
		if u, _ := storage.GetUserByEmail(db, "bob@"+d); u == nil {
			t.Error("saved user row not kept on the failure path")
		}
	})
	t.Run("302 failure redirect, token generation fails (AC-3)", func(t *testing.T) {
		failingTokenGenerator(t)
		idp.email = "carol@" + d
		assertSignInFailed(t, serve(withSecret, oidcCallbackRequest(d, okCookie)))
	})
	plain := func(t *testing.T, rec *httptest.ResponseRecorder, code int, body string) {
		t.Helper()
		if rec.Code != code {
			t.Fatalf("status = %d, want %d; body %q", rec.Code, code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Errorf("Content-Type = %q, want text/plain", ct)
		}
		if got := strings.TrimSpace(rec.Body.String()); got != body {
			t.Errorf("body = %q, want %q", got, body)
		}
		if len(setCookieNamed(rec, "auth_token")) != 0 {
			t.Error("auth_token touched on an error")
		}
	}
	t.Run("400 missing state cookie", func(t *testing.T) {
		plain(t, serve(withSecret, oidcCallbackRequest(d, nil)), http.StatusBadRequest, "missing state cookie")
	})
	t.Run("400 invalid state", func(t *testing.T) {
		plain(t, serve(withSecret, oidcCallbackRequest(d, &http.Cookie{Name: "sso_state", Value: "other|" + d})), http.StatusBadRequest, "invalid state")
	})
	t.Run("400 no email", func(t *testing.T) {
		idp.email = ""
		plain(t, serve(withSecret, oidcCallbackRequest(d, okCookie)), http.StatusBadRequest, "no email in OIDC token claims")
	})
	t.Run("403 email outside domain", func(t *testing.T) {
		idp.email = "mallory@elsewhere.test"
		plain(t, serve(withSecret, oidcCallbackRequest(d, okCookie)), http.StatusForbidden, "email domain does not match SSO provider domain")
	})
	t.Run("404 no provider", func(t *testing.T) {
		const unknown = "pac35-unknown.test"
		rec := serve(withSecret, oidcCallbackRequest(unknown, &http.Cookie{Name: "sso_state", Value: "s1|" + unknown}))
		assertJSONError(t, rec, http.StatusNotFound, "SSO not configured for this domain")
	})
	t.Run("500 OIDC setup fails", func(t *testing.T) {
		const broken = "pac35-broken-issuer.test"
		seedBrokenOIDCProvider(t, broken)
		rec := serve(withSecret, oidcCallbackRequest(broken, &http.Cookie{Name: "sso_state", Value: "s1|" + broken}))
		assertJSONError(t, rec, http.StatusInternalServerError, "internal error")
	})
}

func seedBrokenOIDCProvider(t *testing.T, d string) {
	t.Helper()
	dead := httptest.NewServer(http.NotFoundHandler())
	issuer := dead.URL
	dead.Close()
	if _, err := storage.UpsertSSOProvider(openTestDB(t), &storage.SSOProvider{
		Domain: d, ProviderName: "Broken", ProviderType: "oidc", Enabled: true,
		OIDCIssuer: issuer, OIDCClientID: "c", OIDCClientSecret: "s",
	}); err != nil {
		t.Fatalf("UpsertSSOProvider: %v", err)
	}
}

// assertJSONError checks a status and an exact {"error": msg} JSON body with
// no other member.
func assertJSONError(t *testing.T, rec *httptest.ResponseRecorder, code int, msg string) {
	t.Helper()
	if rec.Code != code {
		t.Fatalf("status = %d, want %d; body %q", rec.Code, code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body struct {
		Error string `json:"error"`
	}
	dec := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		t.Fatalf("body %q is not {error}: %v", rec.Body.String(), err)
	}
	if body.Error != msg {
		t.Errorf("error = %q, want %q", body.Error, msg)
	}
}

// ---- AC-8, AC-10, AC-18: SSO start -----------------------------------------

func TestPAC35_SSOStart_EveryDocumentedStatusAndHint(t *testing.T) {
	const d = "pac35-start.test"
	idp := newFakeIdP(t, "client-"+d)
	seedOIDCProvider(t, d, idp)
	ah := &authHandlers{db: openTestDB(t), jwtSecret: "secret", frontendURL: pac35Frontend}

	start := func(target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		ssoRouter(ah).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec
	}
	idpQuery := func(t *testing.T, rec *httptest.ResponseRecorder) url.Values {
		t.Helper()
		if rec.Code != http.StatusFound {
			t.Fatalf("status = %d, want 302; body %q", rec.Code, rec.Body.String())
		}
		loc, err := url.Parse(rec.Header().Get("Location"))
		if err != nil || !strings.HasPrefix(loc.String(), idp.srv.URL+"/authorize") {
			t.Fatalf("Location = %q, want the IdP authorize endpoint", rec.Header().Get("Location"))
		}
		if len(setCookieNamed(rec, "sso_state")) != 1 {
			t.Error("sso_state cookie not set")
		}
		return loc.Query()
	}
	base := idpQuery(t, start("/api/auth/sso/"+d))
	if _, ok := base["login_hint"]; ok {
		t.Fatalf("no hint was given but one was sent: %v", base)
	}

	t.Run("302 with a valid hint forwards it (AC-8)", func(t *testing.T) {
		q := idpQuery(t, start("/api/auth/sso/"+d+"?login_hint="+url.QueryEscape("alice@"+d)))
		if got := q.Get("login_hint"); got != "alice@"+d {
			t.Errorf("login_hint = %q, want alice@%s", got, d)
		}
	})
	t.Run("302 with a hint in another domain drops it (D-10)", func(t *testing.T) {
		q := idpQuery(t, start("/api/auth/sso/"+d+"?login_hint="+url.QueryEscape("alice@elsewhere.test")))
		if _, ok := q["login_hint"]; ok {
			t.Errorf("other-domain hint forwarded: %v", q)
		}
	})
	t.Run("302 with an invalid hint drops it and adds nothing (AC-10)", func(t *testing.T) {
		for _, hint := range []string{"alice@" + d + "&prompt=none", "alice&prompt=none@" + d, "not-an-email"} {
			q := idpQuery(t, start("/api/auth/sso/"+d+"?login_hint="+url.QueryEscape(hint)+"&prompt=none"))
			assertSameKeys(t, base, q)
			if q.Get("prompt") != "" {
				t.Errorf("hint %q: prompt reached the IdP: %v", hint, q)
			}
		}
	})
	t.Run("404 unknown domain", func(t *testing.T) {
		assertJSONError(t, start("/api/auth/sso/pac35-nobody.test"), http.StatusNotFound, "SSO not configured for this domain")
	})
	t.Run("501 SAML", func(t *testing.T) {
		if _, err := storage.UpsertSSOProvider(openTestDB(t), &storage.SSOProvider{
			Domain: "pac35-saml.test", ProviderName: "SAML", ProviderType: "saml", Enabled: true,
		}); err != nil {
			t.Fatal(err)
		}
		assertJSONError(t, start("/api/auth/sso/pac35-saml.test"), http.StatusNotImplemented, "SAML not yet supported")
	})
	t.Run("500 setup failure", func(t *testing.T) {
		seedBrokenOIDCProvider(t, "pac35-start-broken.test")
		assertJSONError(t, start("/api/auth/sso/pac35-start-broken.test"), http.StatusInternalServerError, "internal error")
	})
}

// assertSameKeys fails when got carries a query key want does not (or the
// reverse); state is random per request, so only keys are compared.
func assertSameKeys(t *testing.T, want, got url.Values) {
	t.Helper()
	keys := func(v url.Values) string {
		var k []string
		for key := range v {
			k = append(k, key)
		}
		sort.Strings(k)
		return strings.Join(k, ",")
	}
	if keys(want) != keys(got) {
		t.Errorf("query keys = %s, want %s", keys(got), keys(want))
	}
}

// ---- AC-9, AC-10: Google and Microsoft start -------------------------------

func TestPAC35_GoogleAndMicrosoftStart_ForwardOnlyValidHints(t *testing.T) {
	t.Setenv("MICROSOFT_CLIENT_ID", "ms-client")
	ah := &authHandlers{
		oauthConfig: &oauth2.Config{ClientID: "g", ClientSecret: "s", RedirectURL: "http://localhost/cb",
			Endpoint: oauth2.Endpoint{AuthURL: "https://accounts.example/auth", TokenURL: "https://accounts.example/token"}},
		db: openTestDB(t),
	}
	routes := map[string]http.HandlerFunc{"google": ah.startOAuth, "microsoft": ah.startMicrosoftOAuth}
	for name, h := range routes {
		t.Run(name, func(t *testing.T) {
			start := func(query string) url.Values {
				t.Helper()
				rec := httptest.NewRecorder()
				h(rec, httptest.NewRequest(http.MethodGet, "/api/auth/"+name+query, nil))
				if rec.Code != http.StatusFound {
					t.Fatalf("status = %d, want 302", rec.Code)
				}
				loc, err := url.Parse(rec.Header().Get("Location"))
				if err != nil {
					t.Fatal(err)
				}
				return loc.Query()
			}
			base := start("")
			if _, ok := base["login_hint"]; ok {
				t.Fatalf("hint sent without one given: %v", base)
			}
			for _, hint := range []string{"alice@acme.com", "o'brien@acme.com"} {
				if got := start("?login_hint=" + url.QueryEscape(hint)).Get("login_hint"); got != hint {
					t.Errorf("login_hint = %q, want %q", got, hint)
				}
			}
			for _, hint := range []string{"alice@acme.com&prompt=none", "alice&prompt=none@acme.com", "alice@acme.com?x=y", "alice@localhost", ""} {
				q := start("?login_hint=" + url.QueryEscape(hint) + "&prompt=none&x=y")
				assertSameKeys(t, base, q)
				if q.Get("prompt") != base.Get("prompt") {
					t.Errorf("hint %q: prompt = %q, want %q", hint, q.Get("prompt"), base.Get("prompt"))
				}
			}
			// A repeated hint: only the first value counts.
			if got := start("?login_hint=bad&login_hint=alice@acme.com"); got.Get("login_hint") != "" {
				t.Errorf("a later repeated value was forwarded: %v", got)
			}
		})
	}
}

// ---- AC-11, AC-17, D-9: disconnect -----------------------------------------

func pac35Session(t *testing.T, secret string, user uuid.UUID) *http.Cookie {
	t.Helper()
	tok, err := auth.GenerateToken(user, "x@example.com", secret)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: "auth_token", Value: tok}
}

func pac35Router(db *sql.DB, secret string) *chi.Mux {
	ah := &authHandlers{oauthConfig: &oauth2.Config{}, db: db, jwtSecret: secret}
	mh := &meHandlers{db: db}
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(requireAuth(secret))
		r.Get("/api/auth/status", ah.status)
		r.Delete("/api/auth/disconnect", ah.disconnect)
		r.Get("/api/auth/me", mh.me)
		r.Post("/api/auth/logout", mh.logout)
	})
	return r
}

func pac35Do(r http.Handler, method, target string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func calendarStatus(t *testing.T, r http.Handler, cookie *http.Cookie) map[string]any {
	t.Helper()
	rec := pac35Do(r, http.MethodGet, "/api/auth/status", cookie)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("status body %q: %v", rec.Body.String(), err)
	}
	return body
}

func TestPAC35_Disconnect_GoogleUserStaysSignedIn(t *testing.T) {
	db := openTestDB(t)
	const secret = "pac35-secret"
	user := createTestUser(t, "pac35-ac11@example.com")
	if err := auth.UpsertUserToken(db, user, &oauth2.Token{AccessToken: "a", RefreshToken: "r", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	r := pac35Router(db, secret)
	session := pac35Session(t, secret, user)
	if s := calendarStatus(t, r, session); s["connected"] != true || s["provider"] != "google" {
		t.Fatalf("precondition: status = %v", s)
	}

	rec := pac35Do(r, http.MethodDelete, "/api/auth/disconnect", session)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body %q", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("204 has a body: %q", rec.Body.String())
	}
	if h := rec.Result().Header.Values("Set-Cookie"); len(h) != 0 {
		t.Errorf("disconnect set a cookie: %v", h)
	}
	if tok, _ := auth.LoadUserToken(db, user); tok != nil {
		t.Error("the Google token was not deleted")
	}
	if s := calendarStatus(t, r, session); s["connected"] != false {
		t.Errorf("status after disconnect = %v, want not connected", s)
	}
	if me := pac35Do(r, http.MethodGet, "/api/auth/me", session); me.Code != http.StatusOK {
		t.Errorf("/api/auth/me after disconnect = %d, want 200 (still signed in)", me.Code)
	}
}

func conferencingState(t *testing.T, user uuid.UUID) map[string]bool {
	t.Helper()
	rec := httptest.NewRecorder()
	setupConferencingRoutes(t, nil, nil).ServeHTTP(rec, withUser(httptest.NewRequest(http.MethodGet, "/api/conference/providers", nil), user))
	var list []conferenceProviderStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("providers %q: %v", rec.Body.String(), err)
	}
	out := map[string]bool{}
	for _, p := range list {
		out[p.Provider] = p.Connected
	}
	return out
}

func TestPAC35_Disconnect_OutlookAndWebcalUsersKeepEverything(t *testing.T) {
	for _, key := range []string{"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "MICROSOFT_CLIENT_ID", "MICROSOFT_CLIENT_SECRET"} {
		t.Setenv(key, "configured-for-test")
	}
	t.Setenv("GOOGLE_REDIRECT_URL", "http://localhost:8080/api/auth/callback")
	t.Setenv("MICROSOFT_REDIRECT_URL", "http://localhost:8080/api/auth/microsoft/callback")
	db := openTestDB(t)
	const secret = "pac35-secret"
	r := pac35Router(db, secret)

	for _, provider := range []string{"outlook", "webcal"} {
		t.Run(provider, func(t *testing.T) {
			user := createTestUser(t, "pac35-ac17-"+provider+"@example.com")
			googleTok := &oauth2.Token{AccessToken: "g-" + provider, RefreshToken: "g-refresh-" + provider, Expiry: time.Now().Add(time.Hour).Truncate(time.Second)}
			if err := auth.UpsertUserToken(db, user, googleTok); err != nil {
				t.Fatal(err)
			}
			if _, err := storage.GetOrCreateSettingsByUser(db, user); err != nil {
				t.Fatal(err)
			}
			if provider == "outlook" {
				if err := auth.SaveMicrosoftToken(db, user, &oauth2.Token{AccessToken: "m", RefreshToken: "ms-refresh", Expiry: time.Now().Add(time.Hour)}); err != nil {
					t.Fatal(err)
				}
			} else if _, err := db.Exec(`UPDATE settings SET calendar_provider='webcal', webcal_url='https://cal.example/feed.ics' WHERE user_id=$1`, user); err != nil {
				t.Fatal(err)
			}
			session := pac35Session(t, secret, user)
			statusBefore := calendarStatus(t, r, session)
			if statusBefore["provider"] != provider || statusBefore["connected"] != true {
				t.Fatalf("precondition: status = %v", statusBefore)
			}
			confBefore := conferencingState(t, user)
			if !confBefore["google_meet"] {
				t.Fatalf("precondition: google_meet not connected: %v", confBefore)
			}

			rec := pac35Do(r, http.MethodDelete, "/api/auth/disconnect", session)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409; body %q", rec.Code, rec.Body.String())
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q", ct)
			}
			var body struct {
				Error    string `json:"error"`
				Provider string `json:"provider"`
			}
			dec := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&body); err != nil {
				t.Fatalf("409 body %q: %v", rec.Body.String(), err)
			}
			if body.Error != "disconnecting this kind of calendar is not available yet" || body.Provider != provider {
				t.Errorf("409 body = %+v", body)
			}
			if h := rec.Result().Header.Values("Set-Cookie"); len(h) != 0 {
				t.Errorf("disconnect set a cookie: %v", h)
			}

			tok, err := auth.LoadUserToken(db, user)
			if err != nil || tok == nil || tok.AccessToken != googleTok.AccessToken || tok.RefreshToken != googleTok.RefreshToken {
				t.Errorf("Google token changed: %+v (err %v)", tok, err)
			}
			if provider == "outlook" {
				if ms, _ := auth.LoadMicrosoftToken(db, user); ms == nil || ms.RefreshToken != "ms-refresh" {
					t.Errorf("Microsoft token changed: %+v", ms)
				}
			} else {
				s, _ := storage.GetOrCreateSettingsByUser(db, user)
				if s == nil || s.WebcalURL != "https://cal.example/feed.ics" {
					t.Errorf("webcal link changed: %+v", s)
				}
			}
			statusAfter := calendarStatus(t, r, session)
			if statusAfter["provider"] != statusBefore["provider"] || statusAfter["connected"] != statusBefore["connected"] {
				t.Errorf("status changed: before %v, after %v", statusBefore, statusAfter)
			}
			confAfter := conferencingState(t, user)
			if confAfter["google_meet"] != confBefore["google_meet"] || confAfter["teams"] != confBefore["teams"] {
				t.Errorf("conferencing changed: before %v, after %v", confBefore, confAfter)
			}
			if me := pac35Do(r, http.MethodGet, "/api/auth/me", session); me.Code != http.StatusOK {
				t.Errorf("/api/auth/me after disconnect = %d, want 200", me.Code)
			}
		})
	}
}

func TestPAC35_Disconnect_UnreadableSettingsDeletesNothing(t *testing.T) {
	// A caller with no users row: the settings get-or-create fails on the
	// foreign key, which is the "settings cannot be read" branch (D-9).
	req := withUser(httptest.NewRequest(http.MethodDelete, "/api/auth/disconnect", nil), uuid.New())
	rec := httptest.NewRecorder()
	(&authHandlers{db: openTestDB(t)}).disconnect(rec, req)
	assertJSONError(t, rec, http.StatusInternalServerError, "internal error")
	if h := rec.Result().Header.Values("Set-Cookie"); len(h) != 0 {
		t.Errorf("disconnect set a cookie: %v", h)
	}
}

// ---- AC-12, AC-18: logout and current user ---------------------------------

func TestPAC35_Logout_204NoBodyExpiresSession(t *testing.T) {
	db := openTestDB(t)
	const secret = "pac35-secret"
	user := createTestUser(t, "pac35-logout@example.com")
	r := pac35Router(db, secret)

	rec := pac35Do(r, http.MethodPost, "/api/auth/logout", pac35Session(t, secret, user))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want none", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "" {
		t.Errorf("Content-Type = %q, want none", ct)
	}
	cookies := setCookieNamed(rec, "auth_token")
	if len(cookies) != 1 || !strings.Contains(cookies[0], "Max-Age=0") {
		t.Errorf("auth_token not expired: %v", cookies)
	}

	t.Run("401 signed out", func(t *testing.T) {
		rec := pac35Do(r, http.MethodPost, "/api/auth/logout", nil)
		if rec.Code != http.StatusUnauthorized || strings.TrimSpace(rec.Body.String()) != `{"error":"unauthorized"}` {
			t.Errorf("status = %d body %q, want 401 unauthorized", rec.Code, rec.Body.String())
		}
	})
}

func TestPAC35_CurrentUser_EveryDocumentedStatus(t *testing.T) {
	db := openTestDB(t)
	const secret = "pac35-secret"
	r := pac35Router(db, secret)
	u, err := storage.UpsertUser(db, "pac35-me@example.com", "Me", "https://img.test/me.png", "google", "g-me")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("200", func(t *testing.T) {
		rec := pac35Do(r, http.MethodGet, "/api/auth/me", pac35Session(t, secret, u.ID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		var body struct {
			ID        string `json:"id"`
			Email     string `json:"email"`
			Name      string `json:"name"`
			AvatarURL string `json:"avatarUrl"`
			Provider  string `json:"provider"`
			CreatedAt string `json:"createdAt"`
		}
		dec := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			t.Fatalf("body %q: %v", rec.Body.String(), err)
		}
		if body.AvatarURL != "https://img.test/me.png" || body.Provider != "google" || body.ID != u.ID.String() {
			t.Errorf("body = %+v", body)
		}
	})
	t.Run("401 signed out", func(t *testing.T) {
		rec := pac35Do(r, http.MethodGet, "/api/auth/me", nil)
		if rec.Code != http.StatusUnauthorized || strings.TrimSpace(rec.Body.String()) != `{"error":"unauthorized"}` {
			t.Errorf("status = %d body %q", rec.Code, rec.Body.String())
		}
	})
	t.Run("404 stale user", func(t *testing.T) {
		rec := pac35Do(r, http.MethodGet, "/api/auth/me", pac35Session(t, secret, uuid.New()))
		if rec.Code != http.StatusNotFound || strings.TrimSpace(rec.Body.String()) != `{"error":"user not found"}` {
			t.Errorf("status = %d body %q", rec.Code, rec.Body.String())
		}
	})
}

// ---- AC-18: detection ------------------------------------------------------

func TestPAC35_Detect_EveryDocumentedStatus(t *testing.T) {
	const d = "pac35-detect.test"
	idp := newFakeIdP(t, "client-"+d)
	seedOIDCProvider(t, d, idp)
	ah := &authHandlers{db: openTestDB(t)}

	detect := func(ah *authHandlers, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/detect", strings.NewReader(body))
		req.RemoteAddr = "192.0.2.35:1000"
		ssoRouter(ah).ServeHTTP(rec, req)
		return rec
	}
	decode := func(t *testing.T, rec *httptest.ResponseRecorder) auth.DetectResult {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body %q", rec.Code, rec.Body.String())
		}
		var res auth.DetectResult
		dec := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&res); err != nil {
			t.Fatalf("body %q: %v", rec.Body.String(), err)
		}
		return res
	}

	t.Run("200 sso", func(t *testing.T) {
		resetDetectLimiter(t, nil)
		res := decode(t, detect(ah, `{"email":"alice@`+d+`"}`))
		if res.Type != "sso" || res.ProviderName != "Acme IdP" || res.RedirectURL != "/api/auth/sso/"+d {
			t.Errorf("result = %+v", res)
		}
	})
	t.Run("200 not sso", func(t *testing.T) {
		resetDetectLimiter(t, nil)
		rec := detect(ah, `{"email":"alice@gmail.com"}`)
		res := decode(t, rec)
		if res.Type != "generic" || res.RedirectURL != "" || strings.Contains(rec.Body.String(), "redirect_url") {
			t.Errorf("result = %+v (%s)", res, rec.Body.String())
		}
	})
	t.Run("400 no email", func(t *testing.T) {
		resetDetectLimiter(t, nil)
		assertJSONError(t, detect(ah, `{}`), http.StatusBadRequest, "email required")
	})
	t.Run("429 throttled", func(t *testing.T) {
		resetDetectLimiter(t, nil)
		var rec *httptest.ResponseRecorder
		for i := 0; i <= detectRateLimit; i++ {
			rec = detect(ah, `{}`)
		}
		assertJSONError(t, rec, http.StatusTooManyRequests, "rate limit exceeded")
	})
	t.Run("500 lookup fails", func(t *testing.T) {
		resetDetectLimiter(t, nil)
		assertJSONError(t, detect(&authHandlers{db: closedDB(t)}, `{"email":"alice@`+d+`"}`), http.StatusInternalServerError, "internal error")
	})
}
