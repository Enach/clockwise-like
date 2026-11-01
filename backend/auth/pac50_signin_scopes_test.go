package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/config"
)

// PAC-50 AC-24 (owner, 2026-10-27, spec §9 D-11; PAC-68 OQ-2): sign-in never
// requests new or admin-consent scopes. Each sign-in start's scope parameter
// is exactly today's set and carries no scope the step-up adds; the step-up's
// authorize URL is the only one that does.
func TestPAC50_SignInAuthorizeURLsCarryNoNewScope(t *testing.T) {
	issuer := oidcDiscoveryStandIn(t)
	oidcClient, err := NewOIDCClient(t.Context(), issuer, "oidc-client", "oidc-secret", "https://app.test/api/auth/callback/oidc/acme.com")
	if err != nil {
		t.Fatal(err)
	}
	signIn := map[string]struct {
		authURL string
		want    []string
	}{
		"google": {GetAuthURL(NewGoogleOAuthConfig("g", "s", "https://app.test/cb"), "st", ""),
			[]string{"https://www.googleapis.com/auth/calendar", "https://www.googleapis.com/auth/userinfo.email"}},
		"microsoft": {GetMicrosoftAuthURL(&MicrosoftConfig{ClientID: "m", ClientSecret: "s", RedirectURL: "https://app.test/cb"}, "st", ""),
			[]string{"Calendars.ReadWrite", "User.Read", "offline_access"}},
		"oidc sso": {oidcClient.AuthURL("st", ""), []string{"openid", "profile", "email"}},
	}

	ep := config.IdPEndpoint{AuthURL: "https://idp.test/auth", TokenURL: "https://idp.test/token", JWKSURL: "https://idp.test/jwks"}
	stepupExtra := map[string]bool{}
	for _, p := range []*StepupProvider{
		NewGoogleStepup("g", "s", "https://app.test/api/admin/sso/stepup/callback", ep),
		NewMicrosoftStepup("m", "s", "https://app.test/api/admin/sso/stepup/callback", ep),
	} {
		if len(p.ExtraScopes()) == 0 {
			t.Fatalf("%s step-up names no extra scope", p.Name)
		}
		got := scopeParam(t, p.AuthURL("st", "n", "", ""))
		for _, s := range p.ExtraScopes() {
			stepupExtra[s] = true
			if !slices.Contains(got, s) {
				t.Fatalf("%s step-up authorize URL lacks its extra scope %q: %v", p.Name, s, got)
			}
		}
	}
	for _, admin := range []string{"https://www.googleapis.com/auth/admin.directory.user.readonly", "Organization.Read.All", "RoleManagement.Read.Directory"} {
		if !stepupExtra[admin] {
			t.Fatalf("the step-up does not request %q; the test premise is broken", admin)
		}
	}

	for name, s := range signIn {
		got := scopeParam(t, s.authURL)
		if strings.Join(got, " ") != strings.Join(s.want, " ") {
			t.Errorf("%s sign-in scope = %v, want exactly today's %v", name, got, s.want)
		}
		for _, sc := range got {
			if stepupExtra[sc] || strings.Contains(sc, "admin.directory") || strings.HasSuffix(sc, ".All") || strings.HasPrefix(sc, "RoleManagement.") {
				t.Errorf("%s sign-in requests the step-up / admin-consent scope %q", name, sc)
			}
		}
	}
}

// The identity scopes are not "extra": ExtraScopes is what the 403's
// stepup_scopes carries (contract: beyond openid, email, profile, User.Read).
func TestPAC50_StepupExtraScopesExcludeIdentityScopes(t *testing.T) {
	ep := config.IdPEndpoint{AuthURL: "https://idp.test/auth", TokenURL: "https://idp.test/token", JWKSURL: "https://idp.test/jwks"}
	g := NewGoogleStepup("g", "s", "https://app.test/cb", ep).ExtraScopes()
	m := NewMicrosoftStepup("m", "s", "https://app.test/cb", ep).ExtraScopes()
	if strings.Join(g, " ") != "https://www.googleapis.com/auth/admin.directory.user.readonly" {
		t.Errorf("google extra = %v", g)
	}
	if strings.Join(m, " ") != "Organization.Read.All RoleManagement.Read.Directory" {
		t.Errorf("microsoft extra = %v", m)
	}
	// A caller cannot change the provider's list through the returned slice.
	g[0] = "mutated"
	if NewGoogleStepup("g", "s", "https://app.test/cb", ep).ExtraScopes()[0] == "mutated" {
		t.Error("ExtraScopes shares its backing array")
	}
}

func scopeParam(t *testing.T, raw string) []string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(u.Query().Get("scope"))
}

// oidcDiscoveryStandIn serves just enough OIDC discovery for NewOIDCClient.
func oidcDiscoveryStandIn(t *testing.T) string {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 srv.URL,
			"authorization_endpoint": srv.URL + "/authorize",
			"token_endpoint":         srv.URL + "/token",
			"jwks_uri":               srv.URL + "/jwks",
		})
	})
	return srv.URL
}
