package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// PAC-71 connect step (spec §3 "Connecting a calendar", AC-7 last sentence,
// AC-12 to AC-14, AC-31, AC-47 connect half; contract §4, CONTRACT-2, -3,
// -7, -10).

// connectProvider stands in for Google's and Microsoft's token endpoint and
// Graph, counting every call.
type connectProvider struct {
	srv                         *httptest.Server
	tokenCalls, meCalls, orgCal atomic.Int64
	access                      string
	failToken                   bool
}

func newConnectProvider(t *testing.T) *connectProvider {
	t.Helper()
	p := &connectProvider{access: "connect-" + uuid.NewString()[:8]}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		p.tokenCalls.Add(1)
		if p.failToken {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": p.access, "refresh_token": "r-" + p.access, "token_type": "Bearer", "expires_in": 3600})
	})
	mux.HandleFunc("/me", func(w http.ResponseWriter, _ *http.Request) {
		p.meCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": uuid.NewString(), "mail": "h.work@other.org"})
	})
	mux.HandleFunc("/organization", func(w http.ResponseWriter, _ *http.Request) {
		p.orgCal.Add(1)
		http.Error(w, "personal", http.StatusForbidden)
	})
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	t.Setenv("MICROSOFT_CLIENT_ID", "ms-client")
	t.Setenv("MICROSOFT_CLIENT_SECRET", "ms-secret")
	t.Setenv("MICROSOFT_REDIRECT_URL", "https://app.test/api/auth/microsoft/callback")
	prevEP, prevMe, prevOrg := microsoftOAuthEndpoint, microsoftGraphMeURL, microsoftGraphOrganizationURL
	microsoftOAuthEndpoint = oauth2.Endpoint{AuthURL: p.srv.URL + "/authorize", TokenURL: p.srv.URL + "/token", AuthStyle: oauth2.AuthStyleInParams}
	microsoftGraphMeURL, microsoftGraphOrganizationURL = p.srv.URL+"/me", p.srv.URL+"/organization"
	t.Cleanup(func() {
		microsoftOAuthEndpoint, microsoftGraphMeURL, microsoftGraphOrganizationURL = prevEP, prevMe, prevOrg
	})
	return p
}

func (p *connectProvider) googleConfig() *oauth2.Config {
	c := auth.NewGoogleOAuthConfig("g-client", "g-secret", "https://app.test/api/auth/callback")
	c.Endpoint = oauth2.Endpoint{AuthURL: p.srv.URL + "/authorize", TokenURL: p.srv.URL + "/token", AuthStyle: oauth2.AuthStyleInParams}
	return c
}

// pac71Router is the production route table.
func pac71Router(t *testing.T, cfg *oauth2.Config) *chi.Mux {
	t.Helper()
	r := chi.NewRouter()
	RegisterRoutes(r, openTestDB(t), cfg, pac71Secret, "", pac35Frontend)
	return r
}

func sessionCookie(t *testing.T, id uuid.UUID, gen int64) *http.Cookie {
	t.Helper()
	tok, err := auth.GenerateSessionToken(id, "x@example.com", gen, pac71Secret)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: "auth_token", Value: tok}
}

// startConnect POSTs a connect start as the web app does and returns the
// state cookie it set.
func startConnect(t *testing.T, r http.Handler, path string, session *http.Cookie) (*httptest.ResponseRecorder, *http.Cookie) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, nil)
	if session != nil {
		req.AddCookie(session)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if strings.HasSuffix(c.Name, "_connect_state") {
			return rec, c
		}
	}
	return rec, nil
}

// finishConnect is the provider's return to the callback in the same browser.
func finishConnect(t *testing.T, r http.Handler, callback string, state *http.Cookie, session *http.Cookie, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, callback+"?state="+url.QueryEscape(state.Value)+"&"+query, nil)
	req.AddCookie(state)
	if session != nil {
		req.AddCookie(session)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func assertNotCredited(t *testing.T, rec *httptest.ResponseRecorder, cookie string) {
	t.Helper()
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != pac35Frontend+connectNotCreditedPath {
		t.Fatalf("= %d %q, want 302 not_credited; body %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	assertConnectHygiene(t, rec, cookie)
}

// assertConnectHygiene is CONTRACT-3's connect half: auth_token never set or
// expired, the connect cookie always expired.
func assertConnectHygiene(t *testing.T, rec *httptest.ResponseRecorder, cookie string) {
	t.Helper()
	if got := setCookieNamed(rec, "auth_token"); len(got) != 0 {
		t.Fatalf("a connect touched auth_token: %v", got)
	}
	got := setCookieNamed(rec, cookie)
	if len(got) != 1 || !strings.Contains(got[0], "Max-Age=0") {
		t.Fatalf("connect cookie not expired: %v", got)
	}
}

func TestPAC71_CONTRACT2_ConnectStartsAnswerJSONAndAStateCookie(t *testing.T) {
	p := newConnectProvider(t)
	r := pac71Router(t, p.googleConfig())
	u := createTestUser(t, "pac71-start-"+uuid.NewString()[:8]+"@example.com")
	for _, c := range []struct{ path, cookie, scopes string }{
		{"/api/auth/google/connect", googleConnectCookie, "https://www.googleapis.com/auth/calendar https://www.googleapis.com/auth/userinfo.email"},
		{"/api/auth/microsoft/connect", msConnectCookie, "Calendars.ReadWrite User.Read offline_access"},
	} {
		t.Run(c.path, func(t *testing.T) {
			rec, state := startConnect(t, r, c.path, sessionCookie(t, u, 0))
			if rec.Code != http.StatusOK || state == nil {
				t.Fatalf("= %d %s", rec.Code, rec.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body) != 1 {
				t.Fatalf("body %s", rec.Body.String())
			}
			au, err := url.Parse(body["authorize_url"].(string))
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Fields(au.Query().Get("scope"))
			sort.Strings(got)
			want := strings.Fields(c.scopes)
			sort.Strings(want)
			if strings.Join(got, " ") != strings.Join(want, " ") || au.Query().Get("login_hint") != "" || au.Query().Get("state") != state.Value {
				t.Fatalf("authorize_url %s", au)
			}
			sc := rec.Result().Header.Values("Set-Cookie")
			if len(sc) != 1 || !strings.Contains(sc[0], "HttpOnly") || !strings.Contains(sc[0], "Max-Age=300") || !strings.Contains(sc[0], "SameSite=Lax") {
				t.Fatalf("Set-Cookie %v", sc)
			}
			// AC-14 (d): a GET is not a connect start.
			get := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, c.path, nil)
			req.AddCookie(sessionCookie(t, u, 0))
			r.ServeHTTP(get, req)
			if get.Code != http.StatusMethodNotAllowed || len(get.Result().Header.Values("Set-Cookie")) != 0 {
				t.Fatalf("GET = %d %v", get.Code, get.Result().Header.Values("Set-Cookie"))
			}
			// Unauthenticated: requireAuth's 401, no cookie.
			unauth, st := startConnect(t, r, c.path, nil)
			if unauth.Code != http.StatusUnauthorized || st != nil {
				t.Fatalf("no session = %d", unauth.Code)
			}
		})
	}
	t.Run("Microsoft unconfigured is 503 without a cookie", func(t *testing.T) {
		t.Setenv("MICROSOFT_CLIENT_ID", "")
		rec, st := startConnect(t, r, "/api/auth/microsoft/connect", sessionCookie(t, u, 0))
		if rec.Code != http.StatusServiceUnavailable || st != nil {
			t.Fatalf("= %d", rec.Code)
		}
	})
}

// AC-12: Henry connects Outlook after choosing it in Settings; the
// Microsoft account's identity and address are never used.
func TestPAC71_AC12_OutlookConnectAttachesTheCredentialToHenryOnly(t *testing.T) {
	p := newConnectProvider(t)
	r := pac71Router(t, p.googleConfig())
	d := pac71Domain()
	henry := pac71Bound(t, "henry@"+d, googleIdentity(pac71GoogleID()), storage.AddressDomainConfirmed)
	execT(t, `INSERT INTO settings (user_id, calendar_provider) VALUES ($1, 'outlook')`, henry) // his explicit choice first
	before := readAccount(t, henry)
	s := sessionCookie(t, henry, 0)
	_, state := startConnect(t, r, "/api/auth/microsoft/connect", s)
	rec := finishConnect(t, r, "/api/auth/microsoft/callback", state, s, "code=c1")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != pac35Frontend+"/auth/callback?provider=outlook&connect=credited" {
		t.Fatalf("= %d %q", rec.Code, rec.Header().Get("Location"))
	}
	assertConnectHygiene(t, rec, msConnectCookie)
	after := readAccount(t, henry)
	if !strings.Contains(after.msTokens, p.access) || after.calendarProvider != "outlook" || after.gen != before.gen || after.identity != before.identity || after.standing != before.standing {
		t.Fatalf("Henry = %+v", after)
	}
	if p.meCalls.Load() != 0 || p.orgCal.Load() != 0 {
		t.Fatal("CONTRACT-7: the connect read the Microsoft identity")
	}
	if carriersCount(t, "h.work@other.org") != 0 {
		t.Fatal("an account was created for the Microsoft address")
	}
}

// AC-12b and AC-31: a Google connect by a Microsoft-bound Henry binds
// nothing and never changes the work calendar; G7 is refused at sign-in.
func TestPAC71_AC12b_AC31_GoogleConnectNeverBindsOrChangesTheWorkCalendar(t *testing.T) {
	p := newConnectProvider(t)
	r := pac71Router(t, p.googleConfig())
	d := pac71Domain()
	henry := pac71Bound(t, "henry@"+d, msIdentity(pac71GUID(), pac71GUID()), storage.AddressDomainConfirmed)
	execT(t, `INSERT INTO settings (user_id, calendar_provider) VALUES ($1, 'outlook')`, henry)
	s := sessionCookie(t, henry, 0)
	_, state := startConnect(t, r, "/api/auth/google/connect", s)
	rec := finishConnect(t, r, "/api/auth/callback", state, s, "code=c1")
	if rec.Header().Get("Location") != pac35Frontend+"/auth/callback?provider=google&connect=credited" {
		t.Fatalf("= %d %q", rec.Code, rec.Header().Get("Location"))
	}
	a := readAccount(t, henry)
	if a.googleAccess != p.access || a.calendarProvider != "outlook" || !strings.HasPrefix(a.identity, "microsoft:") {
		t.Fatalf("Henry = %+v", a)
	}
	assertRefused(t, pac71Google(t, googleProfile(pac71GoogleID(), "henry@"+d, true, d)))

	t.Run("AC-31: Outlook connect without choosing it keeps Google", func(t *testing.T) {
		g := pac71Bound(t, "g@"+pac71Domain(), googleIdentity(pac71GoogleID()), storage.AddressDomainConfirmed)
		execT(t, `INSERT INTO settings (user_id) VALUES ($1)`, g)
		sg := sessionCookie(t, g, 0)
		_, st := startConnect(t, r, "/api/auth/microsoft/connect", sg)
		finishConnect(t, r, "/api/auth/microsoft/callback", st, sg, "code=c1")
		if a := readAccount(t, g); a.calendarProvider != "google" || a.msTokens == "" {
			t.Fatalf("= %+v", a)
		}
	})
}

// AC-13 and AC-7 (last sentence): B connects with an identity bound to A.
func TestPAC71_AC13_ConnectWithAnotherAccountsIdentityChangesOnlyTheConnector(t *testing.T) {
	p := newConnectProvider(t)
	r := pac71Router(t, p.googleConfig())
	a := pac71Bound(t, "a@"+pac71Domain(), msIdentity(pac71GUID(), pac71GUID()), storage.AddressDomainConfirmed)
	before := readAccount(t, a)
	b := createTestUser(t, "b-"+uuid.NewString()[:8]+"@example.com")
	s := sessionCookie(t, b, 0)
	_, state := startConnect(t, r, "/api/auth/microsoft/connect", s)
	rec := finishConnect(t, r, "/api/auth/microsoft/callback", state, s, "code=c1")
	if !strings.HasSuffix(rec.Header().Get("Location"), "connect=credited") {
		t.Fatalf("= %q", rec.Header().Get("Location"))
	}
	if after := readAccount(t, a); after != before {
		t.Fatal("A changed")
	}
	if readAccount(t, b).msTokens == "" {
		t.Fatal("B does not hold the credential")
	}
}

// AC-14 (a)-(c), CONTRACT-10 and the connect 500: credited to nobody.
func TestPAC71_AC14_ConnectCreditedToNobody(t *testing.T) {
	p := newConnectProvider(t)
	r := pac71Router(t, p.googleConfig())
	a := createTestUser(t, "a-"+uuid.NewString()[:8]+"@example.com")
	b := createTestUser(t, "b-"+uuid.NewString()[:8]+"@example.com")
	start := func() *http.Cookie {
		_, st := startConnect(t, r, "/api/auth/google/connect", sessionCookie(t, a, 0))
		return st
	}
	cases := map[string]func() *httptest.ResponseRecorder{
		"(a) no session": func() *httptest.ResponseRecorder {
			return finishConnect(t, r, "/api/auth/callback", start(), nil, "code=c1")
		},
		"(b) another user's session": func() *httptest.ResponseRecorder {
			return finishConnect(t, r, "/api/auth/callback", start(), sessionCookie(t, b, 0), "code=c1")
		},
		"CONTRACT-10 cancelled at the provider": func() *httptest.ResponseRecorder {
			calls := p.tokenCalls.Load()
			rec := finishConnect(t, r, "/api/auth/callback", start(), sessionCookie(t, a, 0), "error=access_denied")
			if p.tokenCalls.Load() != calls {
				t.Fatal("a code-less return was exchanged")
			}
			return rec
		},
		"a forged state": func() *httptest.ResponseRecorder {
			forged := &http.Cookie{Name: googleConnectCookie, Value: "eyJ1IjoiMCJ9.AAAA"}
			return finishConnect(t, r, "/api/auth/callback", forged, sessionCookie(t, a, 0), "code=c1")
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			assertNotCredited(t, run(), googleConnectCookie)
			if readAccount(t, a).googleAccess != "" || readAccount(t, b).googleAccess != "" {
				t.Fatal("a credential was stored")
			}
		})
	}
	t.Run("(c) the generation advanced after the start", func(t *testing.T) {
		seedGoogleToken(t, a, "a-own")
		st := start()
		execT(t, `UPDATE users SET session_generation = session_generation + 1 WHERE id = $1`, a)
		for _, s := range []*http.Cookie{sessionCookie(t, a, 0), sessionCookie(t, a, 1)} {
			rec := finishConnect(t, r, "/api/auth/callback", st, s, "code=c1")
			assertNotCredited(t, rec, googleConnectCookie)
		}
		if readAccount(t, a).googleAccess != "a-own" {
			t.Fatal("A's credential changed")
		}
	})
	t.Run("exchange failure is a 500 that still expires the state", func(t *testing.T) {
		p.failToken = true
		defer func() { p.failToken = false }()
		// a is at generation 1 since (c): start at that generation.
		_, st := startConnect(t, r, "/api/auth/google/connect", sessionCookie(t, a, 1))
		rec := finishConnect(t, r, "/api/auth/callback", st, sessionCookie(t, a, 1), "code=c1")
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("= %d", rec.Code)
		}
		assertConnectHygiene(t, rec, googleConnectCookie)
	})
}

// AC-14 (d): within the spec's cross-site model nothing another site can
// make the browser send starts a connect that can store a credential.
func TestPAC71_AC14d_CrossSiteRequestsStartNothingThatStores(t *testing.T) {
	p := newConnectProvider(t)
	r := pac71Router(t, p.googleConfig())
	a := createTestUser(t, "a-"+uuid.NewString()[:8]+"@example.com")
	s := sessionCookie(t, a, 0)
	for _, kind := range []string{"top-level navigation", "GET form", "subresource"} {
		t.Run(kind, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/auth/google/connect?x=1", nil)
			req.AddCookie(s)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusMethodNotAllowed || len(rec.Result().Header.Values("Set-Cookie")) != 0 {
				t.Fatalf("= %d", rec.Code)
			}
		})
	}
	t.Run("POST form: the browser gets a state the attacker cannot read or replay", func(t *testing.T) {
		rec, victimState := startConnect(t, r, "/api/auth/google/connect", s)
		if rec.Code != http.StatusOK || victimState == nil {
			t.Fatal("start failed")
		}
		// The attacking page cannot read the body, so the provider step never
		// happens for this state. The attacker's own state, sent to the
		// victim's browser without its cookie, is an invalid state.
		attacker := createTestUser(t, "x-"+uuid.NewString()[:8]+"@example.com")
		_, attackerState := startConnect(t, r, "/api/auth/google/connect", sessionCookie(t, attacker, 0))
		req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?state="+url.QueryEscape(attackerState.Value)+"&code=c1", nil)
		req.AddCookie(s)
		req.AddCookie(victimState)
		out := httptest.NewRecorder()
		r.ServeHTTP(out, req)
		if out.Code != http.StatusBadRequest {
			t.Fatalf("= %d", out.Code)
		}
		if readAccount(t, a).googleAccess != "" || readAccount(t, attacker).googleAccess != "" {
			t.Fatal("stored")
		}
	})
	t.Run("an ordinary sign-in in a browser holding A's session never binds or stores on A", func(t *testing.T) {
		email := "x@" + pac71Domain()
		p2 := newFakeOAuthProvider(t)
		p2.profile = jsonProfile(googleProfile(pac71GoogleID(), email, true, ""))
		ah := pac71Handlers(t)
		prev := googleUserInfoURL
		googleUserInfoURL = p2.srv.URL + "/profile"
		t.Cleanup(func() { googleUserInfoURL = prev })
		ah.oauthConfig = &oauth2.Config{ClientID: "g", Endpoint: p2.endpoint()}
		req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?state=s1&code=c1", nil)
		req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "s1"})
		req.AddCookie(s)
		rec := httptest.NewRecorder()
		ah.callback(rec, req)
		uid, _ := sessionOf(t, rec)
		if uid == a || readAccount(t, a).identity != "" || readAccount(t, a).googleAccess != "" {
			t.Fatal("the sign-in reached A")
		}
	})
}

// AC-47 (connect half): the completion passes its generation check, then a
// rule (b) binding of the account commits, then the store is attempted.
func TestPAC71_AC47_ConnectStoreAfterABindingIsCreditedToNobody(t *testing.T) {
	for _, c := range []struct{ start, callback, cookie string }{
		{"/api/auth/google/connect", "/api/auth/callback", googleConnectCookie},
		{"/api/auth/microsoft/connect", "/api/auth/microsoft/callback", msConnectCookie},
	} {
		t.Run(c.start, func(t *testing.T) {
			p := newConnectProvider(t)
			r := pac71Router(t, p.googleConfig())
			d := pac71Domain()
			a := pac71Legacy(t, "henry@"+d, "google", "")
			s0 := sessionCookie(t, a, 0)
			_, state := startConnect(t, r, c.start, s0)
			var henryAccess string
			connectBeforeStoreHook = func() {
				connectBeforeStoreHook = nil
				uid, gen := sessionOf(t, pac71Google(t, googleProfile(pac71GoogleID(), "henry@"+d, true, d)))
				if uid != a || gen != 1 {
					t.Fatalf("binding reached %s gen %d", uid, gen)
				}
				henryAccess = readAccount(t, a).googleAccess
			}
			t.Cleanup(func() { connectBeforeStoreHook = nil })
			rec := finishConnect(t, r, c.callback, state, s0, "code=c1")
			assertNotCredited(t, rec, c.cookie)
			st := readAccount(t, a)
			if henryAccess == "" || st.googleAccess != henryAccess || st.msTokens != "" {
				t.Fatalf("A = %+v, want Henry's Google credential only", st)
			}
		})
	}
}
