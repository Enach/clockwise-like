package api

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// PAC-50 contract revision 4 (contracts/features/PAC-50.yaml mergeConditions,
// spec §9 D-8 and D-9): what the stage-4 branch must answer once it sits on
// a main that has PAC-20 (one cookie helper) and PAC-21 (no error text in a
// 5xx body).

// wantInternalError asserts the PAC-21 body on a 500: exactly
// {"error":"internal error"}, JSON, no detail.
func wantInternalError(t *testing.T, what string, rec interface{ Header() http.Header }, code int, body string) {
	t.Helper()
	if code != http.StatusInternalServerError {
		t.Fatalf("%s: status = %d %s, want 500", what, code, body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("%s: Content-Type = %q, want application/json", what, ct)
	}
	if strings.TrimSpace(body) != `{"error":"internal error"}` {
		t.Errorf("%s: body = %s, want {\"error\":\"internal error\"}", what, body)
	}
}

// mergeConditions: every PAC-50 500, and listSsoProviders, answers the PAC-21
// body, never the branch's `save failed: ...`, err.Error(), `grant check
// failed`, `state generation failed` or `step-up could not be recorded`.
func TestPAC50_EveryServerErrorIsTheGenericBody(t *testing.T) {
	t.Run("create: save fails after the gate", func(t *testing.T) {
		e := newSSOEnv(t)
		u, org := e.member(t, "r4save")
		issueGrant(t, e, u.ID, org.ID, e.clock.Now())
		e.h.store = &failingSSOStore{ssoStore: e.h.store, failWrite: true}
		rec := e.do(t, http.MethodPost, "/api/admin/sso", createBody(org.Domain), session(t, u, time.Now()))
		wantInternalError(t, "create", rec, rec.Code, rec.Body.String())
	})
	t.Run("delete: delete fails after the gate", func(t *testing.T) {
		e := newSSOEnv(t)
		u, org := e.member(t, "r4del")
		issueGrant(t, e, u.ID, org.ID, e.clock.Now())
		e.h.store = &failingSSOStore{ssoStore: e.h.store, failDelete: true}
		rec := e.do(t, http.MethodDelete, "/api/admin/sso/"+org.Domain, "", session(t, u, time.Now()))
		wantInternalError(t, "delete", rec, rec.Code, rec.Body.String())
	})
	t.Run("create and delete: the grant lookup fails", func(t *testing.T) {
		e := newSSOEnv(t)
		u, org := e.member(t, "r4grant")
		e.h.store = &failingSSOStore{ssoStore: e.h.store, failConsume: true}
		sess := session(t, u, time.Now())
		rec := e.do(t, http.MethodPost, "/api/admin/sso", createBody(org.Domain), sess)
		wantInternalError(t, "create grant lookup", rec, rec.Code, rec.Body.String())
		rec = e.do(t, http.MethodDelete, "/api/admin/sso/"+org.Domain, "", sess)
		wantInternalError(t, "delete grant lookup", rec, rec.Code, rec.Body.String())
	})
	t.Run("list: the query fails", func(t *testing.T) {
		e := newSSOEnv(t)
		u, _ := e.member(t, "r4list")
		e.h.store = &failingSSOStore{ssoStore: e.h.store, failList: true}
		rec := e.do(t, http.MethodGet, "/api/admin/sso", "", session(t, u, time.Now()))
		wantInternalError(t, "list", rec, rec.Code, rec.Body.String())
	})
	t.Run("start: the state cannot be generated", func(t *testing.T) {
		e := newSSOEnv(t)
		u, _ := e.member(t, "r4rand")
		prev := stepupRandRead
		stepupRandRead = func([]byte) (int, error) { return 0, errors.New("injected entropy failure") }
		t.Cleanup(func() { stepupRandRead = prev })
		rec := e.do(t, http.MethodGet, "/api/admin/sso/stepup?provider=google", "", session(t, u, time.Now()))
		wantInternalError(t, "start", rec, rec.Code, rec.Body.String())
		if stateCookie(rec) != nil {
			t.Error("a failed start set the state cookie")
		}
	})
	t.Run("callback: the outcome cannot be recorded", func(t *testing.T) {
		e := newSSOEnv(t)
		u, org := e.member(t, "r4rec")
		e.google.set(googleAdmin(u.Email, org.Domain))
		e.h.store = &failingSSOStore{ssoStore: e.h.store, failRecord: true}
		rec := e.stepup(t, "google", session(t, u, time.Now()))
		wantInternalError(t, "callback", rec, rec.Code, rec.Body.String())
	})
}

// D-9 / D-10 (owner, 2026-10-27): the exact-domain rule compares the value
// as sent. Surrounding whitespace is a different domain: 403
// provider_domain_mismatch, nothing written, no grant consumed.
func TestPAC50_DomainIsNotTrimmed(t *testing.T) {
	e := newSSOEnv(t)
	u, org := e.member(t, "r4trim")
	g := issueGrant(t, e, u.ID, org.ID, e.clock.Now())
	sess := session(t, u, time.Now())

	rec := e.do(t, http.MethodPost, "/api/admin/sso", createBody(" "+org.Domain+" "), sess)
	wantDomainMismatch(t, "create ' domain '", rec)
	rec = e.do(t, http.MethodDelete, "/api/admin/sso/%20"+org.Domain, "", sess)
	wantDomainMismatch(t, "delete '%20domain'", rec)
	if n, _ := e.providerRow(t, org.Domain); n != 0 {
		t.Fatal("a padded domain wrote a row")
	}
	if grantConsumed(t, e, g.ID) {
		t.Fatal("a domain mismatch consumed the grant")
	}
}

// D-8 / PAC-20: the state cookie goes through setCookie, so Secure is the
// deployment's decision (config.DecideSecureCookies), set and cleared, and the
// clearing cookie is Max-Age=0.
func TestPAC50_StateCookieFollowsPAC20(t *testing.T) {
	for _, secure := range []bool{true, false} {
		withSecureCookies(t, secure)
		e := newSSOEnv(t)
		u, org := e.member(t, "r4cookie")
		e.google.set(googleAdmin(u.Email, org.Domain))
		sess := session(t, u, time.Now())

		start := e.do(t, http.MethodGet, "/api/admin/sso/stepup?provider=google", "", sess)
		raw := start.Header().Get("Set-Cookie")
		if !strings.HasPrefix(raw, "sso_stepup_state=") || hasAttr(raw, "Secure") != secure {
			t.Fatalf("secure=%v: start Set-Cookie = %q", secure, raw)
		}

		loc, _ := url.Parse(start.Header().Get("Location"))
		e.google.set(func(i *stepupIdP) { i.nonce = loc.Query().Get("nonce") })
		cb := e.do(t, http.MethodGet, stepupCallbackPath+"?code=c1&state="+url.QueryEscape(loc.Query().Get("state")), "", sess, stateCookie(start))
		clear := cb.Header().Get("Set-Cookie")
		if !strings.HasPrefix(clear, "sso_stepup_state=;") || !hasAttr(clear, "Max-Age=0") ||
			!hasAttr(clear, "Path=/api/admin/sso/stepup") || hasAttr(clear, "Secure") != secure {
			t.Fatalf("secure=%v: callback Set-Cookie = %q", secure, clear)
		}
	}
}

// D-9 and the start's 400: a missing or empty provider is the generated
// wrapper's text/plain RequiredParamError; an unknown one is the JSON 400.
func TestPAC50_StartProviderValidation(t *testing.T) {
	e := newSSOEnv(t)
	u, _ := e.member(t, "r4prov")
	sess := session(t, u, time.Now())
	for _, q := range []string{"", "?provider="} {
		rec := e.do(t, http.MethodGet, "/api/admin/sso/stepup"+q, "", sess)
		if rec.Code != http.StatusBadRequest || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") ||
			strings.TrimSpace(rec.Body.String()) != "Query argument provider is required, but not found" || stateCookie(rec) != nil {
			t.Fatalf("start%s = %d %q %s", q, rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
		}
	}
	for _, q := range []string{"?provider=okta", "?provider=Google"} {
		rec := e.do(t, http.MethodGet, "/api/admin/sso/stepup"+q, "", sess)
		if rec.Code != http.StatusBadRequest || rec.Header().Get("Content-Type") != "application/json" ||
			errorBody(t, rec)["error"] != "provider must be 'google' or 'microsoft'" || stateCookie(rec) != nil {
			t.Fatalf("start%s = %d %s", q, rec.Code, rec.Body.String())
		}
	}
}

// D-9: with APP_URL unset no provider is configured, whatever client is set,
// so every start answers D-2's 503.
func TestPAC50_NoAppURLMeansEveryProviderIs503(t *testing.T) {
	t.Setenv("APP_URL", "")
	t.Setenv("GOOGLE_CLIENT_ID", "gid")
	t.Setenv("GOOGLE_CLIENT_SECRET", "gsec")
	t.Setenv("MICROSOFT_CLIENT_ID", "mid")
	t.Setenv("MICROSOFT_CLIENT_SECRET", "msec")
	o := ssoOptionsFromEnv()
	if o.Google != nil || o.Microsoft != nil {
		t.Fatalf("APP_URL unset but providers configured: google=%v microsoft=%v", o.Google != nil, o.Microsoft != nil)
	}

	e := newSSOEnv(t)
	e.h.stepup.google, e.h.stepup.microsoft = o.Google, o.Microsoft
	u, _ := e.member(t, "r4appurl")
	for _, p := range []string{"google", "microsoft"} {
		rec := e.do(t, http.MethodGet, "/api/admin/sso/stepup?provider="+p, "", session(t, u, time.Now()))
		if rec.Code != http.StatusServiceUnavailable || errorBody(t, rec)["error"] != p+" step-up not configured" || stateCookie(rec) != nil {
			t.Fatalf("%s = %d %s", p, rec.Code, rec.Body.String())
		}
	}
}

// D-9: a valid session whose user row is gone gets the JSON 401 at the
// callback, with no record and no redirect.
func TestPAC50_CallbackUserGoneIsJSON401(t *testing.T) {
	e := newSSOEnv(t)
	ghost := &storage.User{ID: uuid.New(), Email: "ghost@nowhere.test"}
	rec := e.do(t, http.MethodGet, stepupCallbackPath+"?code=c&state=s", "", session(t, ghost, time.Now()))
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("Content-Type") != "application/json" ||
		errorBody(t, rec)["error"] != "unauthorized" || rec.Header().Get("Location") != "" {
		t.Fatalf("= %d %q %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
}

// compile-time check that the test double still satisfies the store.
var _ ssoStore = (*failingSSOStore)(nil)
