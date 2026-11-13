package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
)

// PAC-50 contract revisions 8 and 8.1: getSsoStepupOptions (AC-26), the
// merged create order with PAC-22's checks (spec §4.2 steps 2-6, §9 D-14,
// AC-3c as extended), null handling (N1) and the grant purpose (N5, AC-27).

func noSecretBody(domain, issuer string) string {
	return `{"domain":"` + domain + `","provider_name":"Okta","provider_type":"oidc","oidc_issuer":"` + issuer + `","oidc_client_id":"cid"}`
}

// AC-26: the options are read-only and the same for every org member.
func TestPAC50_StepupOptionsAreReadOnly(t *testing.T) {
	e := newSSOEnv(t)
	u, org := e.member(t, "opts")
	g := issueGrant(t, e, u.ID, org.ID, e.clock.Now())
	sess := session(t, u, time.Now())

	rec := e.do(t, http.MethodGet, "/api/admin/sso/stepup/options", "", sess)
	want := strings.Replace(bothProvidersStepupBody, `"error":"sso_write_requires_admin_stepup",`, "", 1)
	got, _ := json.Marshal(errorBody(t, rec))
	if rec.Code != http.StatusOK || string(got) != want || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("options = %d %q %s, want 200 %s", rec.Code, rec.Header().Get("Content-Type"), got, want)
	}
	if rec.Header().Get("Set-Cookie") != "" {
		t.Fatalf("options set a cookie: %q", rec.Header().Get("Set-Cookie"))
	}
	if grantConsumed(t, e, g.ID) {
		t.Fatal("options consumed the caller's grant")
	}
	if ev := e.events(t, u.ID); len(ev) != 0 {
		t.Fatalf("options recorded %v", ev)
	}

	// A step-up state cookie sent along (its Path covers this path) is ignored.
	start := e.do(t, http.MethodGet, "/api/admin/sso/stepup?provider=google", "", sess)
	rec = e.do(t, http.MethodGet, "/api/admin/sso/stepup/options", "", sess, stateCookie(start))
	if got2, _ := json.Marshal(errorBody(t, rec)); rec.Code != http.StatusOK || string(got2) != want {
		t.Fatalf("options with a state cookie = %d %s", rec.Code, got2)
	}

	// A break-glass operator gets the same body.
	bg := newSSOEnv(t, u.Email)
	rec = bg.do(t, http.MethodGet, "/api/admin/sso/stepup/options", "", session(t, u, time.Now()))
	if got3, _ := json.Marshal(errorBody(t, rec)); rec.Code != http.StatusOK || string(got3) != want {
		t.Fatalf("break-glass options = %d %s", rec.Code, got3)
	}
}

// AC-26: no org, no options; the body is SsoWriteMembershipRequired.
func TestPAC50_StepupOptionsRefuseOrglessCaller(t *testing.T) {
	e := newSSOEnv(t)
	u, org := e.memberOf(t, "pac50-opts-orgless-"+time.Now().Format("150405.000000")+"@gmail.com")
	if org != nil {
		t.Fatal("gmail user has an org")
	}
	rec := e.do(t, http.MethodGet, "/api/admin/sso/stepup/options", "", session(t, u, time.Now()))
	if rec.Code != http.StatusForbidden || strings.TrimSpace(rec.Body.String()) != `{"error":"org membership required"}` {
		t.Fatalf("orgless options = %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, http.MethodGet, "/api/admin/sso/stepup/options", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session = %d, want 401", rec.Code)
	}
}

// Spec §4.2 step 3 / AC-3c (revision 8.1): every body 400 is decided before
// the consume, in PAC-22's order with unknown property after the SAML
// refusals and before the required-field check; null is empty (N1).
func TestPAC50_BodyRefusalsLeaveTheGrantLive(t *testing.T) {
	e := newSSOEnv(t)
	u, org := e.member(t, "body400")
	g := issueGrant(t, e, u.ID, org.ID, e.clock.Now())
	sess := session(t, u, time.Now())
	d := org.Domain
	for _, c := range []struct{ body, want string }{
		{`[]`, "invalid JSON"},
		{`{"domain":5}`, "invalid JSON"},
		// A SAML property is named, never reported as unknown.
		{`{"saml_cert":null,"zzz":1}`, "saml_cert is not supported"},
		// An unknown property wins over a missing required field.
		{`{"org_id":"x"}`, "unknown property: org_id"},
		{`{"OIDC_CLIENT_SECRET":"s","domain":"` + d + `"}`, "unknown property: OIDC_CLIENT_SECRET"},
		// N1: null is empty, not a type error.
		{`{"domain":null,"provider_name":"x","provider_type":"oidc","oidc_issuer":"https://i","oidc_client_id":"c","oidc_client_secret":"s"}`,
			"domain, provider_name and provider_type are required"},
		{`{"domain":"` + d + `","provider_name":"x","provider_type":"oidc","oidc_issuer":"https://i","oidc_client_id":"c","oidc_client_secret":null}`,
			"oidc_client_secret must not be empty"},
		{`{"domain":"` + d + `","provider_name":"x","provider_type":"oidc","oidc_issuer":null,"oidc_client_id":"c"}`,
			"oidc_issuer is required"},
		{`{"domain":"` + d + `","provider_name":"x","provider_type":"saml","oidc_issuer":"https://i","oidc_client_id":"c"}`,
			"provider_type 'saml' is not supported"},
	} {
		rec := e.do(t, http.MethodPost, "/api/admin/sso", c.body, sess)
		if rec.Code != http.StatusBadRequest || errorBody(t, rec)["error"] != c.want {
			t.Fatalf("%s = %d %s, want 400 %q", c.body, rec.Code, rec.Body.String(), c.want)
		}
	}
	if grantConsumed(t, e, g.ID) {
		t.Fatal("a body 400 consumed the grant")
	}
	if n, _ := e.providerRow(t, d); n != 0 {
		t.Fatal("a refused body wrote a row")
	}
}

// AC-3c (revision 8.1): the stored-row 400 (spec §4.2 step 4b) leaves the
// grant live; the store-time 400 (§9 D-14(2)) spends it.
func TestPAC50_StoredRowRefusalsAndTheGrant(t *testing.T) {
	e := newSSOEnv(t)
	u, org := e.member(t, "check6")
	sess := session(t, u, time.Now())
	if _, err := storage.UpsertSSOProvider(e.db, &storage.SSOProvider{Domain: org.Domain, ProviderName: "Seed", ProviderType: "oidc",
		Enabled: true, OIDCIssuer: "https://i1", OIDCClientID: "cid", OIDCClientSecret: "kept"}); err != nil {
		t.Fatal(err)
	}

	// Check 6 before authorization: a changed issuer with no secret is a 400
	// and the grant is still live.
	g := issueGrant(t, e, u.ID, org.ID, e.clock.Now())
	rec := e.do(t, http.MethodPost, "/api/admin/sso", noSecretBody(org.Domain, "https://typo"), sess)
	if rec.Code != http.StatusBadRequest || errorBody(t, rec)["error"] != "oidc_client_secret is required when oidc_issuer or oidc_client_id changes" {
		t.Fatalf("check 6 = %d %s", rec.Code, rec.Body.String())
	}
	if grantConsumed(t, e, g.ID) {
		t.Fatal("the stored-row 400 consumed the grant")
	}
	// Repeated: still the 400, and still no grant spent.
	if rec := e.do(t, http.MethodPost, "/api/admin/sso", noSecretBody(org.Domain, "https://typo"), sess); rec.Code != http.StatusBadRequest || grantConsumed(t, e, g.ID) {
		t.Fatalf("check 6 repeated = %d", rec.Code)
	}

	// Store time: another write changes the issuer between check 6 and the
	// store, so the kept secret no longer applies. 400, nothing stored, and
	// the grant consumed.
	ssoBeforeStoreHook = func() {
		ssoBeforeStoreHook = nil
		if _, err := e.db.Exec(`UPDATE sso_providers SET oidc_issuer = 'https://i2' WHERE domain = $1`, org.Domain); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { ssoBeforeStoreHook = nil })
	rec = e.do(t, http.MethodPost, "/api/admin/sso", noSecretBody(org.Domain, "https://i1"), sess)
	if rec.Code != http.StatusBadRequest || errorBody(t, rec)["error"] != "oidc_client_secret is required when oidc_issuer or oidc_client_id changes" {
		t.Fatalf("store-time refusal = %d %s", rec.Code, rec.Body.String())
	}
	if !grantConsumed(t, e, g.ID) {
		t.Fatal("the store-time 400 did not spend the grant")
	}
	if ev := e.events(t, u.ID); len(ev) != 0 {
		t.Fatalf("a refused store was recorded: %v", ev)
	}
	rec = e.do(t, http.MethodPost, "/api/admin/sso", noSecretBody(org.Domain, "https://i2"), sess)
	wantStepupRequired(t, rec, rec.Code, errorBody(t, rec))

	// With a new grant, a matching replace keeps the secret and is recorded.
	issueGrant(t, e, u.ID, org.ID, e.clock.Now())
	rec = e.do(t, http.MethodPost, "/api/admin/sso", noSecretBody(org.Domain, "https://i2"), sess)
	if rec.Code != http.StatusCreated || errorBody(t, rec)["has_oidc_client_secret"] != true {
		t.Fatalf("matching replace = %d %s", rec.Code, rec.Body.String())
	}
	if p, _ := storage.GetSSOProviderByDomainAnyState(e.db, org.Domain); p == nil || p.OIDCClientSecret != "kept" {
		t.Fatalf("kept secret = %+v", p)
	}
	if ev := e.events(t, u.ID); len(ev) != 1 || ev[0].Details["op"] != "update" || ev[0].Details["via"] != "stepup" {
		t.Fatalf("events = %v", ev)
	}
}

// AC-27 (N5): only an sso_write grant authorizes an SSO write.
func TestPAC50_GrantOfAnotherPurposeDoesNotAuthorize(t *testing.T) {
	e := newSSOEnv(t)
	u, org := e.member(t, "purpose")
	sess := session(t, u, time.Now())
	other, err := storage.IssueSSOStepupGrant(context.Background(), e.db, u.ID, org.ID, "google", "llm_org_key", e.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, http.MethodPost, "/api/admin/sso", createBody(org.Domain), sess)
	wantStepupRequired(t, rec, rec.Code, errorBody(t, rec))
	rec = e.do(t, http.MethodDelete, "/api/admin/sso/"+org.Domain, "", sess)
	wantStepupRequired(t, rec, rec.Code, errorBody(t, rec))
	if grantConsumed(t, e, other.ID) {
		t.Fatal("an SSO write spent a grant of another purpose")
	}

	sso := issueGrant(t, e, u.ID, org.ID, e.clock.Now())
	if rec := e.do(t, http.MethodPost, "/api/admin/sso", createBody(org.Domain), sess); rec.Code != http.StatusCreated {
		t.Fatalf("write with an sso_write grant = %d %s", rec.Code, rec.Body.String())
	}
	if !grantConsumed(t, e, sso.ID) || grantConsumed(t, e, other.ID) {
		t.Fatal("the write did not spend exactly the sso_write grant")
	}
}

// N4: the start's membership 403 is SsoWriteMembershipRequired's bytes.
func TestPAC50_StartMembershipRefusalBody(t *testing.T) {
	e := newSSOEnv(t)
	u, _ := e.memberOf(t, "pac50-start-orgless-"+time.Now().Format("150405.000000")+"@gmail.com")
	rec := e.do(t, http.MethodGet, "/api/admin/sso/stepup?provider=google", "", session(t, u, time.Now()))
	if rec.Code != http.StatusForbidden || strings.TrimSpace(rec.Body.String()) != `{"error":"org membership required"}` {
		t.Fatalf("start = %d %s", rec.Code, rec.Body.String())
	}
}
