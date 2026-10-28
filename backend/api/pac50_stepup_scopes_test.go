package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// PAC-50 contract revision 6, AC-24 (owner, 2026-10-27, spec §9 D-11): the
// step-up 403 tells the client what the optional step-up would request and
// what is lost without it. stepup_scopes has a key for exactly the providers
// whose step-up is configured, each equal to what that start requests beyond
// the identity scopes; without_stepup is ["sso_configuration_writes"].
func TestPAC50_StepupBodyNamesOnlyConfiguredProviders(t *testing.T) {
	cases := []struct {
		name              string
		google, microsoft bool
		want              string
	}{
		{"both configured", true, true, bothProvidersStepupBody},
		{"google only (the e2e stack)", true, false, `{"error":"sso_write_requires_admin_stepup",` +
			`"stepup_scopes":{"google":["https://www.googleapis.com/auth/admin.directory.user.readonly"]},` +
			`"stepup_url":"/api/admin/sso/stepup","without_stepup":["sso_configuration_writes"]}`},
		{"microsoft only", false, true, `{"error":"sso_write_requires_admin_stepup",` +
			`"stepup_scopes":{"microsoft":["Organization.Read.All","RoleManagement.Read.Directory"]},` +
			`"stepup_url":"/api/admin/sso/stepup","without_stepup":["sso_configuration_writes"]}`},
		{"neither configured (or APP_URL unset)", false, false, `{"error":"sso_write_requires_admin_stepup",` +
			`"stepup_scopes":{},"stepup_url":"/api/admin/sso/stepup","without_stepup":["sso_configuration_writes"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newSSOEnv(t)
			if !tc.google {
				e.h.stepup.google = nil
			}
			if !tc.microsoft {
				e.h.stepup.microsoft = nil
			}
			u, org := e.member(t, "scopes")
			sess := session(t, u, time.Now())
			for _, rec := range []*httptest.ResponseRecorder{
				e.do(t, http.MethodPost, "/api/admin/sso", createBody(org.Domain), sess),
				e.do(t, http.MethodDelete, "/api/admin/sso/"+org.Domain, "", sess),
			} {
				got := strings.TrimSpace(rec.Body.String())
				if rec.Code != http.StatusForbidden || got != tc.want {
					t.Fatalf("= %d %s, want 403 %s", rec.Code, got, tc.want)
				}
			}
			// The key set is the configured set: each listed provider's start
			// answers 302, each absent one 503.
			for name, on := range map[string]bool{"google": tc.google, "microsoft": tc.microsoft} {
				rec := e.do(t, http.MethodGet, "/api/admin/sso/stepup?provider="+name, "", sess)
				if on && rec.Code != http.StatusFound || !on && rec.Code != http.StatusServiceUnavailable {
					t.Fatalf("start %s = %d, configured=%v", name, rec.Code, on)
				}
			}
		})
	}
}

// AC-24, second half: each stepup_scopes list is exactly what the start puts
// in its authorize URL beyond the identity scopes.
func TestPAC50_StepupScopesAreWhatTheStartRequests(t *testing.T) {
	e := newSSOEnv(t)
	u, org := e.member(t, "scopelist")
	sess := session(t, u, time.Now())
	var body struct {
		Scopes map[string][]string `json:"stepup_scopes"`
	}
	rec := e.do(t, http.MethodPost, "/api/admin/sso", createBody(org.Domain), sess)
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	identity := map[string]bool{"openid": true, "profile": true, "email": true, "User.Read": true}
	for _, name := range []string{"google", "microsoft"} {
		start := e.do(t, http.MethodGet, "/api/admin/sso/stepup?provider="+name, "", sess)
		loc, err := url.Parse(start.Header().Get("Location"))
		if err != nil || start.Code != http.StatusFound {
			t.Fatalf("start %s = %d %v", name, start.Code, err)
		}
		var extra []string
		for _, s := range strings.Fields(loc.Query().Get("scope")) {
			if !identity[s] {
				extra = append(extra, s)
			}
		}
		if len(extra) == 0 || strings.Join(extra, " ") != strings.Join(body.Scopes[name], " ") {
			t.Fatalf("%s: start requests %v beyond identity, 403 lists %v", name, extra, body.Scopes[name])
		}
	}
}
