package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// PAC-50 spec §4.3: the admin step-up start and callback.

const (
	globalAdmin    = "62e90394-69f5-4237-9190-012177145e10"
	ordinaryMember = "b79fbf4d-3ef9-4689-8143-76b194e85509"
)

func wantOutcome(t *testing.T, rec interface{ Header() http.Header }, code int, want string) {
	t.Helper()
	if code != http.StatusFound {
		t.Fatalf("callback status = %d, want 302", code)
	}
	got := rec.Header().Get("Location")
	if got != pac50Frontend+"/settings?"+want {
		t.Fatalf("Location = %q, want %q", got, pac50Frontend+"/settings?"+want)
	}
}

// wantOneStepupEvent checks AC-12 for a step-up: exactly one sso_stepup row,
// attributed to the caller, whose details parse into the §4.6 shape.
func wantOneStepupEvent(t *testing.T, e *ssoEnv, u *storage.User, org *storage.Org, provider, outcome, reason string) {
	t.Helper()
	var got []ssoEventRow
	for _, ev := range e.events(t, u.ID) {
		if ev.Kind == "sso_stepup" {
			got = append(got, ev)
		}
	}
	if len(got) != 1 {
		t.Fatalf("%d sso_stepup events, want 1: %v", len(got), got)
	}
	d := got[0].Details
	var wantReason any
	if reason != "" {
		wantReason = reason
	}
	if d["org_id"] != org.ID.String() || d["provider"] != provider || d["outcome"] != outcome || d["reason"] != wantReason || len(d) != 4 {
		t.Fatalf("details = %v, want {org_id: %s, provider: %s, outcome: %s, reason: %v}", d, org.ID, provider, outcome, wantReason)
	}
}

func googleAdmin(email, hd string) func(*stepupIdP) {
	return func(i *stepupIdP) {
		i.claims = map[string]any{"email": email, "email_verified": true, "hd": hd}
		i.isAdmin, i.isDelegAdmin, i.dirStatus = true, false, 200
	}
}

func microsoftAdmin(email, tid, domain string) func(*stepupIdP) {
	return func(i *stepupIdP) {
		i.claims = map[string]any{"tid": tid, "preferred_username": email, "wids": []string{ordinaryMember, globalAdmin}}
		i.orgID, i.verified, i.orgStatus = tid, []string{"other.onmicrosoft.com", strings.ToUpper(domain)}, 200
		i.roles, i.rolesStatus = nil, 200
	}
}

// AC-21a / AC-21b, D-2.
func TestStepupStart(t *testing.T) {
	e := newSSOEnv(t)
	u, org := e.member(t, "ac21")
	sess := session(t, u, time.Now())

	for _, c := range []struct {
		provider, authPrefix string
		scopes               []string
		absent               string
	}{
		{"google", e.google.srv.URL + "/o/oauth2/v2/auth?", []string{"openid", "email", "https://www.googleapis.com/auth/admin.directory.user.readonly"}, "calendar"},
		{"microsoft", e.ms.srv.URL + "/organizations/oauth2/v2.0/authorize?", []string{"openid", "Organization.Read.All", "RoleManagement.Read.Directory"}, "Calendars"},
	} {
		rec := e.do(t, http.MethodGet, "/api/admin/sso/stepup?provider="+c.provider, "", sess)
		loc := rec.Header().Get("Location")
		if rec.Code != http.StatusFound || !strings.HasPrefix(loc, c.authPrefix) {
			t.Fatalf("%s: %d Location %q", c.provider, rec.Code, loc)
		}
		q, _ := url.Parse(loc)
		v := q.Query()
		if v.Get("redirect_uri") != pac50AppURL+"/api/admin/sso/stepup/callback" || v.Get("state") == "" || v.Get("nonce") == "" || v.Get("response_type") != "code" {
			t.Fatalf("%s: query = %v", c.provider, v)
		}
		for _, s := range c.scopes {
			if !strings.Contains(" "+v.Get("scope")+" ", " "+s+" ") {
				t.Errorf("%s: scope %q missing from %q", c.provider, s, v.Get("scope"))
			}
		}
		if strings.Contains(v.Get("scope"), c.absent) {
			t.Errorf("%s: the step-up widens the calendar consent: %q", c.provider, v.Get("scope"))
		}
		ck := stateCookie(rec)
		if ck == nil || ck.Value == "" || ck.Path != "/api/admin/sso/stepup" || ck.MaxAge != 600 || !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode {
			t.Fatalf("%s: state cookie = %+v", c.provider, ck)
		}
		if strings.Contains(ck.Value, u.Email) {
			t.Error("the state cookie carries the email in clear")
		}
	}
	if v, _ := url.Parse(e.do(t, http.MethodGet, "/api/admin/sso/stepup?provider=google", "", sess).Header().Get("Location")); v.Query().Get("hd") != org.Domain || v.Query().Get("login_hint") != u.Email {
		t.Errorf("google hints = %v", v.Query())
	}

	// The missing/empty provider is TestPAC50_StartProviderValidation's.
	for _, q := range []string{"?provider=okta", "?provider=Google"} {
		rec := e.do(t, http.MethodGet, "/api/admin/sso/stepup"+q, "", sess)
		if rec.Code != http.StatusBadRequest || rec.Header().Get("Content-Type") != "application/json" ||
			errorBody(t, rec)["error"] != "provider must be 'google' or 'microsoft'" || stateCookie(rec) != nil {
			t.Fatalf("start%s = %d %s cookie=%v", q, rec.Code, rec.Body.String(), stateCookie(rec))
		}
	}

	e.h.stepup.microsoft = nil
	rec := e.do(t, http.MethodGet, "/api/admin/sso/stepup?provider=microsoft", "", sess)
	if rec.Code != http.StatusServiceUnavailable || errorBody(t, rec)["error"] != "microsoft step-up not configured" || stateCookie(rec) != nil {
		t.Fatalf("unconfigured = %d %s", rec.Code, rec.Body.String())
	}
}

// AC-5, AC-6, AC-7, AC-13a and D-3 for Google.
func TestStepupGoogle_Denials(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(i *stepupIdP, email, domain string)
		reason string
	}{
		{"no hd (AC-5)", func(i *stepupIdP, email, d string) { googleAdmin(email, d)(i); i.claims["hd"] = nil }, "no_hd"},
		{"hd mismatch (AC-6)", func(i *stepupIdP, email, d string) { googleAdmin(email, d)(i); i.claims["hd"] = "sub." + d }, "hd_mismatch"},
		{"not admin (AC-7)", func(i *stepupIdP, email, d string) { googleAdmin(email, d)(i); i.isAdmin = false }, "not_admin"},
		{"directory error (AC-7)", func(i *stepupIdP, email, d string) { googleAdmin(email, d)(i); i.dirStatus = 403 }, "not_admin"},
		{"email mismatch (AC-13a)", func(i *stepupIdP, email, d string) { googleAdmin("other@"+d, d)(i) }, "email_mismatch"},
		{"email not verified (D-3)", func(i *stepupIdP, email, d string) { googleAdmin(email, d)(i); i.claims["email_verified"] = false }, "email_mismatch"},
		{"token exchange fails (D-3)", func(i *stepupIdP, email, d string) { googleAdmin(email, d)(i); i.tokenStatus = 400 }, "token_invalid"},
		{"wrong audience (D-3)", func(i *stepupIdP, email, d string) { googleAdmin(email, d)(i); i.claims["aud"] = "someone-else" }, "token_invalid"},
		{"wrong nonce (D-3)", func(i *stepupIdP, email, d string) { googleAdmin(email, d)(i); i.claims["nonce"] = "replayed" }, "token_invalid"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newSSOEnv(t)
			u, org := e.member(t, "g-deny")
			e.google.set(func(i *stepupIdP) { c.mutate(i, u.Email, org.Domain) })
			rec := e.stepup(t, "google", session(t, u, time.Now()))
			wantOutcome(t, rec, rec.Code, "stepup=denied&reason="+c.reason)
			if e.liveGrants(t, u.ID) != 0 {
				t.Fatal("a denied step-up issued a grant")
			}
			wantOneStepupEvent(t, e, u, org, "google", "denied", c.reason)
			if ck := stateCookie(rec); ck == nil || ck.MaxAge >= 0 {
				t.Fatalf("state cookie not cleared: %+v", ck)
			}
		})
	}
}

// AC-8a / AC-8b and the write half of AC-12.
func TestStepupGoogle_AdminOrDelegatedAdminGetsAGrant(t *testing.T) {
	for _, delegated := range []bool{false, true} {
		e := newSSOEnv(t)
		u, org := e.member(t, "g-grant")
		e.google.set(googleAdmin(strings.ToUpper(u.Email), strings.ToUpper(org.Domain)))
		if delegated {
			e.google.set(func(i *stepupIdP) { i.isAdmin, i.isDelegAdmin = false, true })
		}
		sess := session(t, u, time.Now())
		rec := e.stepup(t, "google", sess)
		wantOutcome(t, rec, rec.Code, "stepup=granted")
		if e.liveGrants(t, u.ID) != 1 {
			t.Fatal("no grant")
		}
		wantOneStepupEvent(t, e, u, org, "google", "granted", "")
		if rec := e.do(t, http.MethodPost, "/api/admin/sso", createBody(org.Domain), sess); rec.Code != http.StatusCreated {
			t.Fatalf("write after a granted step-up = %d %s", rec.Code, rec.Body.String())
		}
		ev := e.events(t, u.ID)
		if len(ev) != 2 || ev[1].Kind != "sso_provider_written" || ev[1].Details["via"] != "stepup" || ev[1].Details["org_id"] != org.ID.String() || ev[1].Details["provider_id"] == "" {
			t.Fatalf("events = %v", ev)
		}
	}
}

// AC-9a, AC-9c, AC-10, AC-13a and D-3 for Microsoft.
func TestStepupMicrosoft_Denials(t *testing.T) {
	cases := []struct {
		name   string
		pin    string
		mutate func(i *stepupIdP, email, domain string)
		reason string
	}{
		{"domain not verified (AC-9a)", "", func(i *stepupIdP, email, d string) { microsoftAdmin(email, "t1", d)(i); i.verified = []string{"sub." + d} }, "tid_mismatch"},
		{"organisation id differs from tid", "", func(i *stepupIdP, email, d string) { microsoftAdmin(email, "t1", d)(i); i.orgID = "t9" }, "tid_mismatch"},
		{"organisation lookup fails (D-3)", "", func(i *stepupIdP, email, d string) { microsoftAdmin(email, "t1", d)(i); i.orgStatus = 403 }, "tid_mismatch"},
		{"pinned to another tenant (AC-9c)", "t1", func(i *stepupIdP, email, d string) { microsoftAdmin(email, "t2", d)(i) }, "tid_changed"},
		{"ordinary member only (AC-10)", "", func(i *stepupIdP, email, d string) {
			microsoftAdmin(email, "t1", d)(i)
			i.claims["wids"] = []string{ordinaryMember}
		}, "not_admin"},
		{"no wids, role lookup fails (D-3)", "", func(i *stepupIdP, email, d string) {
			microsoftAdmin(email, "t1", d)(i)
			i.claims["wids"] = nil
			i.rolesStatus = 403
		}, "not_admin"},
		{"email mismatch (AC-13a)", "", func(i *stepupIdP, email, d string) { microsoftAdmin("other@"+d, "t1", d)(i) }, "email_mismatch"},
		{"provider error (D-3)", "", nil, "token_invalid"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newSSOEnv(t)
			u, org := e.member(t, "m-deny")
			if c.pin != "" {
				if _, err := e.db.Exec(`UPDATE organizations SET ms_tenant_id = $2 WHERE id = $1`, org.ID, c.pin); err != nil {
					t.Fatal(err)
				}
			}
			sess := session(t, u, time.Now())
			var rec interface {
				Header() http.Header
			}
			code := 0
			if c.mutate == nil {
				start := e.do(t, http.MethodGet, "/api/admin/sso/stepup?provider=microsoft", "", sess)
				loc, _ := url.Parse(start.Header().Get("Location"))
				r := e.do(t, http.MethodGet, stepupCallbackPath+"?error=access_denied&state="+loc.Query().Get("state"), "", sess, stateCookie(start))
				rec, code = r, r.Code
			} else {
				e.ms.set(func(i *stepupIdP) { c.mutate(i, u.Email, org.Domain) })
				r := e.stepup(t, "microsoft", sess)
				rec, code = r, r.Code
			}
			wantOutcome(t, rec, code, "stepup=denied&reason="+c.reason)
			if e.liveGrants(t, u.ID) != 0 {
				t.Fatal("a denied step-up issued a grant")
			}
			wantOneStepupEvent(t, e, u, org, "microsoft", "denied", c.reason)
			tid, _ := storage.GetOrgMSTenantID(context.Background(), e.db, org.ID)
			if tid != c.pin {
				t.Fatalf("tenant pin = %q, want %q", tid, c.pin)
			}
		})
	}
}

// AC-9b, AC-11a, AC-11b.
func TestStepupMicrosoft_AdminGetsAGrantAndPinsTenant(t *testing.T) {
	for _, viaGraph := range []bool{false, true} {
		e := newSSOEnv(t)
		u, org := e.member(t, "m-grant")
		e.ms.set(microsoftAdmin(u.Email, "tenant-abc", org.Domain))
		if viaGraph {
			e.ms.set(func(i *stepupIdP) { i.claims["wids"] = nil; i.roles = []string{ordinaryMember, globalAdmin} })
		}
		sess := session(t, u, time.Now())
		rec := e.stepup(t, "microsoft", sess)
		wantOutcome(t, rec, rec.Code, "stepup=granted")
		if e.liveGrants(t, u.ID) != 1 {
			t.Fatal("no grant")
		}
		if tid, _ := storage.GetOrgMSTenantID(context.Background(), e.db, org.ID); tid != "tenant-abc" {
			t.Fatalf("pin = %q, want tenant-abc", tid)
		}
		wantOneStepupEvent(t, e, u, org, "microsoft", "granted", "")
		if rec := e.do(t, http.MethodDelete, "/api/admin/sso/"+org.Domain, "", sess); rec.Code != http.StatusNotFound {
			t.Fatalf("delete after grant = %d %s (D-5: 404, grant used)", rec.Code, rec.Body.String())
		}
		if e.liveGrants(t, u.ID) != 0 {
			t.Fatal("D-5: a 404 delete left the grant live")
		}
	}
}

// AC-13b, AC-13c and the state checks of D-3.
func TestStepupCallback_StateAndSession(t *testing.T) {
	e := newSSOEnv(t)
	u, org := e.member(t, "state")
	v, _ := e.memberOf(t, "colleague@"+org.Domain)
	e.google.set(googleAdmin(u.Email, org.Domain))
	sessU := session(t, u, time.Now())

	start := e.do(t, http.MethodGet, "/api/admin/sso/stepup?provider=google", "", sessU)
	loc, _ := url.Parse(start.Header().Get("Location"))
	state := loc.Query().Get("state")
	e.google.set(func(i *stepupIdP) { i.nonce = loc.Query().Get("nonce") })
	cb := stepupCallbackPath + "?code=c1&state=" + url.QueryEscape(state)

	// AC-13c: no session.
	if rec := e.do(t, http.MethodGet, cb, "", "", stateCookie(start)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session = %d", rec.Code)
	}
	if len(e.events(t, u.ID)) != 0 || e.liveGrants(t, u.ID) != 0 {
		t.Fatal("a sessionless callback recorded or issued")
	}

	// AC-13b: U's state cookie presented with V's session.
	rec := e.do(t, http.MethodGet, cb, "", session(t, v, time.Now()), stateCookie(start))
	wantOutcome(t, rec, rec.Code, "stepup=denied&reason=state_mismatch")
	if e.liveGrants(t, u.ID)+e.liveGrants(t, v.ID) != 0 {
		t.Fatal("a mismatched state issued a grant")
	}
	if ev := e.events(t, v.ID); len(ev) != 1 || ev[0].Details["reason"] != "state_mismatch" {
		t.Fatalf("events for V = %v", ev)
	}

	tampered := *stateCookie(start)
	tampered.Value = strings.Replace(tampered.Value, ".", "x.", 1)
	for name, r := range map[string]func() int{
		"no cookie":       func() int { return e.do(t, http.MethodGet, cb, "", sessU).Code },
		"tampered cookie": func() int { return e.do(t, http.MethodGet, cb, "", sessU, &tampered).Code },
		"state differs": func() int {
			return e.do(t, http.MethodGet, stepupCallbackPath+"?code=c1&state=other", "", sessU, stateCookie(start)).Code
		},
		"expired cookie": func() int {
			e.clock.Set(e.clock.Now().Add(11 * time.Minute))
			defer e.clock.Set(e.clock.Now().Add(-11 * time.Minute))
			return e.do(t, http.MethodGet, cb, "", sessU, stateCookie(start)).Code
		},
	} {
		if code := r(); code != http.StatusFound {
			t.Fatalf("%s = %d", name, code)
		}
	}
	if e.liveGrants(t, u.ID) != 0 {
		t.Fatal("a bad state issued a grant")
	}
	for _, ev := range e.events(t, u.ID) {
		if ev.Details["reason"] != "state_mismatch" {
			t.Fatalf("event = %v, want state_mismatch", ev)
		}
	}

	// The cookie is bound to the org: U moves org, the old state is refused.
	_, other := e.member(t, "state-moved")
	if err := storage.SetUserOrg(e.db, u.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	rec = e.do(t, http.MethodGet, cb, "", sessU, stateCookie(start))
	wantOutcome(t, rec, rec.Code, "stepup=denied&reason=state_mismatch")
}

// D-4: a storage failure in the callback is a 500 and leaves no grant.
func TestStepupCallback_StorageFailureIs500(t *testing.T) {
	e := newSSOEnv(t)
	u, org := e.member(t, "d4")
	e.google.set(googleAdmin(u.Email, org.Domain))
	e.h.store = &failingSSOStore{ssoStore: e.h.store, failRecord: true}
	rec := e.stepup(t, "google", session(t, u, time.Now()))
	if rec.Code != http.StatusInternalServerError || rec.Header().Get("Location") != "" {
		t.Fatalf("= %d Location %q", rec.Code, rec.Header().Get("Location"))
	}
	_ = errorBody(t, rec)
	if e.liveGrants(t, u.ID) != 0 {
		t.Fatal("a failed callback left a grant")
	}
}

// AC-17: a successful step-up leaves settings and the OAuth-token row alone.
func TestStepup_DoesNotTouchSettingsOrTokens(t *testing.T) {
	e := newSSOEnv(t)
	u, org := e.member(t, "ac17")
	if _, err := storage.GetOrCreateSettingsByUser(e.db, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := auth.UpsertUserToken(e.db, u.ID, &oauth2.Token{AccessToken: "cal-at", RefreshToken: "cal-rt", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		var s, tok string
		if err := e.db.QueryRow(`SELECT row_to_json(s)::text FROM settings s WHERE user_id = $1`, u.ID).Scan(&s); err != nil {
			t.Fatal(err)
		}
		if err := e.db.QueryRow(`SELECT row_to_json(o)::text FROM oauth_tokens o WHERE user_id = $1`, u.ID).Scan(&tok); err != nil {
			t.Fatal(err)
		}
		return s + "\n" + tok
	}
	before := snapshot()
	sess := session(t, u, time.Now())
	e.google.set(googleAdmin(u.Email, org.Domain))
	e.ms.set(microsoftAdmin(u.Email, "tenant-17", org.Domain))
	for _, p := range []string{"google", "microsoft"} {
		rec := e.stepup(t, p, sess)
		wantOutcome(t, rec, rec.Code, "stepup=granted")
		if c := authCookie(rec); c != nil {
			t.Fatalf("%s step-up touched the session", p)
		}
	}
	if after := snapshot(); after != before {
		t.Fatalf("rows changed:\nbefore %s\nafter  %s", before, after)
	}
}

func TestHasQualifyingEntraRole(t *testing.T) {
	for _, id := range []string{globalAdmin, "e8611ab8-c189-46e8-94e1-60213ab1f814", "9b895d92-2cd3-44c7-9d02-a6ac2d5ea5c3", "158c047a-c907-4556-b7ef-446551a6b5f7"} {
		if !auth.HasQualifyingEntraRole([]string{ordinaryMember, strings.ToUpper(id)}) {
			t.Errorf("%s does not qualify", id)
		}
	}
	if auth.HasQualifyingEntraRole([]string{ordinaryMember, uuid.NewString()}) || auth.HasQualifyingEntraRole(nil) {
		t.Error("a non-admin set qualifies")
	}
}
