package api

// PAC-22: GET/POST /api/admin/sso never return the OIDC client secret or the
// SAML signing certificate; the response is the described SsoProvider
// projection; createSsoProvider validates at entry and keeps an omitted client
// secret only where it would be sent (spec v3.4, contract revision 1.3).
//
// setSsoProviderState is not here: the contract says the transition is not
// implementable before PAC-50 merges (spec D-5, D-6), so AC-9, AC-10, AC-12's
// server half, AC-15, AC-16, AC-17 and AC-19 to AC-21 wait for it. A
// suspended configuration is arranged through storage where a criterion
// needs one (AC-11, AC-28).
//
// PAC-50 (contract revision 8.1, N2): every SSO write is now behind PAC-50's
// gate, so the handler is built through newSSOHandlers and every write this
// file sends is made by a caller holding a live sso_write step-up grant
// (pac22Do). The PAC-50 gate itself is tested in pac50_*_test.go; what is
// asserted here is PAC-22's: the projection, the 400s and their order, the
// kept-secret rules and the interleaving.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// Long, test-unique secrets, as spec AC-1 requires for the byte search to be
// meaningful. The certificate carries line breaks so its JSON-escaped form
// differs from its raw form.
func pac22Secret(t *testing.T) string {
	t.Helper()
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return "pac22-client-secret-" + hex.EncodeToString(b)
}

func pac22Cert(t *testing.T) string {
	t.Helper()
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return "-----BEGIN CERTIFICATE-----\nPAC22" + hex.EncodeToString(b) + "\nMIIC\"quoted\"/slash\n-----END CERTIFICATE-----"
}

type pac22Org struct {
	domain string
	userID uuid.UUID
}

// pac22NewOrg makes a fresh organisation with one member. The shared test
// database is never reset, so every test uses its own domain.
func pac22NewOrg(t *testing.T) pac22Org {
	t.Helper()
	db := openTestDB(t)
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	d := "pac22-" + hex.EncodeToString(b) + ".test"
	u, err := storage.UpsertUser(db, "admin@"+d, "Admin", "", "google", "pac22-"+d)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := storage.AssociateUserWithOrg(db, u.ID, u.Email); err != nil {
		t.Fatalf("associate org: %v", err)
	}
	return pac22Org{domain: d, userID: u.ID}
}

func pac22Router(t *testing.T) http.Handler {
	t.Helper()
	h := newSSOHandlers(&authHandlers{db: openTestDB(t), jwtSecret: "test-secret", frontendURL: "http://frontend.test"}, ssoOptions{})
	r := chi.NewRouter()
	r.Post("/api/admin/sso", h.createSSOProvider)
	r.Get("/api/admin/sso", h.listSSOProviders)
	r.Delete("/api/admin/sso/{domain}", h.deleteSSOProvider)
	r.Get("/api/auth/callback/oidc/{domain}", h.oidcCallback)
	return r
}

func pac22Do(t *testing.T, method, path string, user uuid.UUID, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rd *strings.Reader
	if body != "" {
		rd = strings.NewReader(body)
	} else {
		rd = strings.NewReader("")
	}
	if method == http.MethodPost || method == http.MethodDelete {
		pac22GrantFor(t, user)
	}
	req := httptest.NewRequest(method, path, rd)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	pac22Router(t).ServeHTTP(rec, req)
	return rec
}

// pac22GrantFor gives an org member a live sso_write step-up grant, as a
// successful PAC-50 step-up would, so the write that follows passes PAC-50's
// authorization step. A caller with no org gets none (the membership 403
// comes first anyway).
func pac22GrantFor(t *testing.T, user uuid.UUID) {
	t.Helper()
	db := openTestDB(t)
	u, err := storage.GetUserByID(db, user)
	if err != nil || u == nil || u.OrgID == nil {
		return
	}
	if _, err := storage.IssueSSOStepupGrant(context.Background(), db, u.ID, *u.OrgID, "google", storage.SSOGrantPurposeSSOWrite, time.Now()); err != nil {
		t.Fatalf("issue step-up grant: %v", err)
	}
}

func pac22JSON(t *testing.T, v map[string]any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// pac22AssertNoSecret is the raw byte search of spec AC-1: neither the value
// nor its JSON-escaped form occurs anywhere in the body or the headers, in any
// letter case.
func pac22AssertNoSecret(t *testing.T, rec *httptest.ResponseRecorder, secrets ...string) {
	t.Helper()
	body := strings.ToLower(rec.Body.String())
	var hdr strings.Builder
	for k, vs := range rec.Header() {
		hdr.WriteString(k + ": " + strings.Join(vs, ",") + "\n")
	}
	headers := strings.ToLower(hdr.String())
	for _, s := range secrets {
		if s == "" {
			continue
		}
		escaped, _ := json.Marshal(s)
		forms := []string{s, strings.Trim(string(escaped), `"`)}
		for _, f := range forms {
			f = strings.ToLower(f)
			if strings.Contains(body, f) {
				t.Errorf("response body contains a secret (%d bytes): %s", len(s), rec.Body.String())
			}
			if strings.Contains(headers, f) {
				t.Errorf("response headers contain a secret: %s", hdr.String())
			}
		}
	}
}

var pac22ProviderKeys = []string{
	"created_at", "domain", "has_oidc_client_secret", "has_saml_cert", "id",
	"oidc_client_id", "oidc_issuer", "provider_name", "provider_type",
	"saml_entry_point", "saml_issuer", "state", "updated_at",
}

// pac22DecodeProvider validates one SsoProvider against the described shape:
// exactly the described keys, every one present, and nothing else.
func pac22DecodeProvider(t *testing.T, raw json.RawMessage) gen.SsoProvider {
	t.Helper()
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatalf("provider is not an object: %v (%s)", err, raw)
	}
	got := make([]string, 0, len(keys))
	for k := range keys {
		got = append(got, k)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(pac22ProviderKeys, ",") {
		t.Fatalf("provider keys = %v, want exactly %v", got, pac22ProviderKeys)
	}
	var p gen.SsoProvider
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		t.Fatalf("provider does not decode as SsoProvider: %v", err)
	}
	if p.State != gen.Active && p.State != gen.Suspended {
		t.Errorf("state = %q, want active or suspended", p.State)
	}
	if p.ProviderType != gen.SsoProviderProviderTypeOidc && p.ProviderType != gen.SsoProviderProviderTypeSaml {
		t.Errorf("provider_type = %q", p.ProviderType)
	}
	return p
}

func pac22DecodeList(t *testing.T, rec *httptest.ResponseRecorder) []gen.SsoProvider {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &top); err != nil {
		t.Fatalf("list body is not a JSON object (a bare array is not the described shape): %v; %s", err, rec.Body.String())
	}
	if len(top) != 1 || top["providers"] == nil {
		t.Fatalf("list top level = %s, want exactly {\"providers\": [...]}", rec.Body.String())
	}
	var items []json.RawMessage
	if err := json.Unmarshal(top["providers"], &items); err != nil || items == nil {
		t.Fatalf("providers is not an array: %s", top["providers"])
	}
	out := make([]gen.SsoProvider, 0, len(items))
	for _, it := range items {
		out = append(out, pac22DecodeProvider(t, it))
	}
	return out
}

func pac22DecodeCreated(t *testing.T, rec *httptest.ResponseRecorder) gen.SsoProvider {
	t.Helper()
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body %s", rec.Code, rec.Body.String())
	}
	return pac22DecodeProvider(t, rec.Body.Bytes())
}

func pac22Stored(t *testing.T, d string) *storage.SSOProvider {
	t.Helper()
	p, err := storage.GetSSOProviderByDomainAnyState(openTestDB(t), d)
	if err != nil {
		t.Fatalf("read stored configuration: %v", err)
	}
	return p
}

func pac22Store(t *testing.T, p *storage.SSOProvider) {
	t.Helper()
	if _, err := storage.UpsertSSOProvider(openTestDB(t), p); err != nil {
		t.Fatalf("arrange configuration through storage: %v", err)
	}
}

func pac22SetEnabled(t *testing.T, d string, enabled bool) {
	t.Helper()
	if _, err := openTestDB(t).Exec(`UPDATE sso_providers SET enabled = $2 WHERE domain = $1`, d, enabled); err != nil {
		t.Fatalf("arrange active state through storage: %v", err)
	}
}

func pac22Body(d, issuer, clientID string, extra map[string]any) map[string]any {
	m := map[string]any{
		"domain":         d,
		"provider_name":  "Okta",
		"provider_type":  "oidc",
		"oidc_issuer":    issuer,
		"oidc_client_id": clientID,
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func pac22AssertError(t *testing.T, rec *httptest.ResponseRecorder, code int, msg string) {
	t.Helper()
	if rec.Code != code {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, code, rec.Body.String())
	}
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("error body is not JSON: %s", rec.Body.String())
	}
	if e.Error != msg {
		t.Errorf("error = %q, want %q", e.Error, msg)
	}
}

func pac22AssertUnchanged(t *testing.T, before, after *storage.SSOProvider) {
	t.Helper()
	if before == nil {
		if after != nil {
			t.Fatalf("a configuration was stored: %+v", after.Domain)
		}
		return
	}
	if after == nil {
		t.Fatal("the stored configuration was removed")
	}
	if *before != *after {
		t.Errorf("stored configuration changed:\n before %+v\n after  %+v", *before, *after)
	}
}

// pac22SignIn drives the OIDC callback for d as a browser returning from the
// IdP would, and reports whether a session was issued.
func pac22SignIn(t *testing.T, d string, idp *fakeIdP) bool {
	t.Helper()
	idp.email = "someone@" + d
	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback/oidc/"+d+"?state=s1&code=c1", nil)
	req.AddCookie(&http.Cookie{Name: "sso_state", Value: "s1|" + d})
	rec := httptest.NewRecorder()
	pac22Router(t).ServeHTTP(rec, req)
	return rec.Code == http.StatusFound && authCookie(rec) != nil
}

// --- AC-1, AC-3, AC-5, AC-6: the list ---------------------------------------

func TestPAC22_ListNeverReturnsEitherSecret(t *testing.T) {
	org := pac22NewOrg(t)
	secret, cert := pac22Secret(t), pac22Cert(t)
	// A configuration stored before PAC-22 can hold both (AC-1 arranges it
	// through storage: no API write can store a certificate any more).
	pac22Store(t, &storage.SSOProvider{
		Domain: org.domain, ProviderName: "Legacy", ProviderType: "saml", Enabled: true,
		OIDCClientSecret: secret, SAMLEntryPoint: "https://idp.example/sso",
		SAMLIssuer: "urn:idp", SAMLCert: cert,
	})

	rec := pac22Do(t, http.MethodGet, "/api/admin/sso", org.userID, "")
	pac22AssertNoSecret(t, rec, secret, cert)
	items := pac22DecodeList(t, rec)
	if len(items) != 1 {
		t.Fatalf("providers = %d, want 1", len(items))
	}
	p := items[0]
	if !p.HasOidcClientSecret || !p.HasSamlCert {
		t.Errorf("has_oidc_client_secret = %v, has_saml_cert = %v; want both true", p.HasOidcClientSecret, p.HasSamlCert)
	}
	if p.ProviderType != gen.SsoProviderProviderTypeSaml || p.SamlEntryPoint != "https://idp.example/sso" || p.SamlIssuer != "urn:idp" {
		t.Errorf("non-secret SAML values not projected: %+v", p)
	}
	if p.Domain != org.domain || p.State != gen.Active {
		t.Errorf("domain/state = %q/%q", p.Domain, p.State)
	}
}

func TestPAC22_EachIndicatorTracksOnlyItsOwnSecret(t *testing.T) {
	// AC-3: each property checked once with its own secret present and the
	// other secret absent.
	withSecret := pac22NewOrg(t)
	pac22Store(t, &storage.SSOProvider{
		Domain: withSecret.domain, ProviderName: "A", ProviderType: "oidc", Enabled: true,
		OIDCIssuer: "https://a.example", OIDCClientID: "a", OIDCClientSecret: pac22Secret(t),
	})
	withCert := pac22NewOrg(t)
	pac22Store(t, &storage.SSOProvider{
		Domain: withCert.domain, ProviderName: "B", ProviderType: "saml", Enabled: true,
		SAMLEntryPoint: "https://b.example", SAMLCert: pac22Cert(t),
	})

	a := pac22DecodeList(t, pac22Do(t, http.MethodGet, "/api/admin/sso", withSecret.userID, ""))
	if len(a) != 1 || !a[0].HasOidcClientSecret || a[0].HasSamlCert {
		t.Errorf("secret only: %+v, want has_oidc_client_secret true, has_saml_cert false", a)
	}
	b := pac22DecodeList(t, pac22Do(t, http.MethodGet, "/api/admin/sso", withCert.userID, ""))
	if len(b) != 1 || b[0].HasOidcClientSecret || !b[0].HasSamlCert {
		t.Errorf("cert only: %+v, want has_oidc_client_secret false, has_saml_cert true", b)
	}
}

func TestPAC22_EmptyLegacyRowSerialisesWithFalseIndicators(t *testing.T) {
	// AC-5: an OIDC row stored before PAC-22 with every value empty.
	org := pac22NewOrg(t)
	pac22Store(t, &storage.SSOProvider{Domain: org.domain, ProviderName: "Empty", ProviderType: "oidc", Enabled: true})

	items := pac22DecodeList(t, pac22Do(t, http.MethodGet, "/api/admin/sso", org.userID, ""))
	if len(items) != 1 {
		t.Fatalf("providers = %d, want 1", len(items))
	}
	p := items[0]
	if p.HasOidcClientSecret || p.HasSamlCert || p.OidcIssuer != "" || p.OidcClientId != "" {
		t.Errorf("empty legacy row = %+v", p)
	}
}

func TestPAC22_ListIsANamedContainer(t *testing.T) {
	// AC-6, none and one through the API. "More than one" cannot be arranged
	// (both domains are unique and the list joins exactly); the contract
	// manifest covers it by validating a constructed container of two.
	org := pac22NewOrg(t)
	if got := pac22DecodeList(t, pac22Do(t, http.MethodGet, "/api/admin/sso", org.userID, "")); len(got) != 0 {
		t.Fatalf("providers = %v, want []", got)
	}
	rec := pac22Do(t, http.MethodGet, "/api/admin/sso", org.userID, "")
	if strings.TrimSpace(rec.Body.String()) != `{"providers":[]}` {
		t.Errorf("empty list body = %s, want {\"providers\":[]}", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	pac22Store(t, &storage.SSOProvider{Domain: org.domain, ProviderName: "One", ProviderType: "oidc", Enabled: true,
		OIDCIssuer: "https://one.example", OIDCClientID: "one", OIDCClientSecret: pac22Secret(t)})
	if got := pac22DecodeList(t, pac22Do(t, http.MethodGet, "/api/admin/sso", org.userID, "")); len(got) != 1 {
		t.Fatalf("providers = %d, want 1", len(got))
	}

	two := []byte(`{"providers":[` + string(mustJSON(t, gen.SsoProvider{State: gen.Active, ProviderType: gen.SsoProviderProviderTypeOidc})) +
		`,` + string(mustJSON(t, gen.SsoProvider{State: gen.Suspended, ProviderType: gen.SsoProviderProviderTypeSaml})) + `]}`)
	rr := httptest.NewRecorder()
	rr.WriteHeader(http.StatusOK)
	_, _ = rr.Write(two)
	if got := pac22DecodeList(t, rr); len(got) != 2 {
		t.Fatalf("constructed container of two decoded to %d", len(got))
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPAC22_ListSuspendedConfigurationReportsSuspended(t *testing.T) {
	org := pac22NewOrg(t)
	pac22Store(t, &storage.SSOProvider{Domain: org.domain, ProviderName: "S", ProviderType: "oidc", Enabled: true,
		OIDCIssuer: "https://s.example", OIDCClientID: "s", OIDCClientSecret: pac22Secret(t)})
	pac22SetEnabled(t, org.domain, false)
	items := pac22DecodeList(t, pac22Do(t, http.MethodGet, "/api/admin/sso", org.userID, ""))
	if len(items) != 1 || items[0].State != gen.Suspended {
		t.Fatalf("items = %+v, want one suspended", items)
	}
}

func TestPAC22_ListRefusesCallerWithoutOrg(t *testing.T) {
	// API-092, the listSsoProviders share: the 403 is asserted.
	u, err := storage.UpsertUser(openTestDB(t), "pac22-noorg-"+uuid.NewString()+"@gmail.com", "N", "", "google", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	pac22AssertError(t, pac22Do(t, http.MethodGet, "/api/admin/sso", u.ID, ""), http.StatusForbidden, "org membership required")
}

// --- AC-2: the create response ------------------------------------------------

func TestPAC22_CreateResponseNeverReturnsTheSecret(t *testing.T) {
	org := pac22NewOrg(t)
	secret := pac22Secret(t)
	rec := pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID,
		pac22JSON(t, pac22Body(org.domain, "https://issuer.example/"+org.domain, "client-1", map[string]any{"oidc_client_secret": secret})))
	pac22AssertNoSecret(t, rec, secret)
	p := pac22DecodeCreated(t, rec)
	if !p.HasOidcClientSecret || p.HasSamlCert || p.State != gen.Active || p.ProviderType != gen.SsoProviderProviderTypeOidc {
		t.Errorf("created = %+v", p)
	}
	if stored := pac22Stored(t, org.domain); stored == nil || stored.OIDCClientSecret != secret {
		t.Fatal("the supplied secret was not stored")
	}

	// The same submission also carrying a certificate is refused, and the
	// refusal carries neither secret, nor the stored one.
	cert := pac22Cert(t)
	rec = pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID,
		pac22JSON(t, pac22Body(org.domain, "https://issuer.example/"+org.domain, "client-1",
			map[string]any{"oidc_client_secret": pac22Secret(t), "saml_cert": cert})))
	pac22AssertError(t, rec, http.StatusBadRequest, "saml_cert is not supported")
	pac22AssertNoSecret(t, rec, secret, cert)

	// A member of another organisation: refused, and no secret in the body.
	other := pac22NewOrg(t)
	attempt := pac22Secret(t)
	rec = pac22Do(t, http.MethodPost, "/api/admin/sso", other.userID,
		pac22JSON(t, pac22Body(org.domain, "https://evil.example", "c", map[string]any{"oidc_client_secret": attempt})))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign write status = %d, want 403", rec.Code)
	}
	pac22AssertNoSecret(t, rec, secret, attempt)
}

// --- AC-7, AC-8, AC-18, AC-26: validation at entry ---------------------------

func TestPAC22_CreateValidation(t *testing.T) {
	cases := []struct {
		name string
		body func(d string) string
		msg  string
	}{
		{"not JSON", func(string) string { return "{" }, "invalid JSON"},
		{"not an object", func(string) string { return "[]" }, "invalid JSON"},
		{"null body", func(string) string { return "null" }, "invalid JSON"},
		{"wrong type", func(d string) string {
			return pac22JSONNoT(pac22Body(d, "https://i", "c", map[string]any{"provider_name": 5, "oidc_client_secret": "x"}))
		}, "invalid JSON"},
		{"saml_entry_point", func(d string) string {
			return pac22JSONNoT(pac22Body(d, "https://i", "c", map[string]any{"oidc_client_secret": "x", "saml_entry_point": "https://e"}))
		}, "saml_entry_point is not supported"},
		{"saml_issuer null", func(d string) string {
			return pac22JSONNoT(pac22Body(d, "https://i", "c", map[string]any{"oidc_client_secret": "x", "saml_issuer": nil}))
		}, "saml_issuer is not supported"},
		{"saml_cert empty", func(d string) string {
			return pac22JSONNoT(pac22Body(d, "https://i", "c", map[string]any{"oidc_client_secret": "x", "saml_cert": ""}))
		}, "saml_cert is not supported"},
		{"saml_entry_point named first", func(d string) string {
			return pac22JSONNoT(pac22Body(d, "https://i", "c", map[string]any{"saml_cert": "c", "saml_entry_point": "e", "provider_type": "saml"}))
		}, "saml_entry_point is not supported"},
		{"missing domain", func(d string) string {
			b := pac22Body(d, "https://i", "c", map[string]any{"oidc_client_secret": "x"})
			delete(b, "domain")
			return pac22JSONNoT(b)
		}, "domain, provider_name and provider_type are required"},
		{"missing provider_name", func(d string) string {
			return pac22JSONNoT(pac22Body(d, "https://i", "c", map[string]any{"oidc_client_secret": "x", "provider_name": ""}))
		}, "domain, provider_name and provider_type are required"},
		{"saml type", func(d string) string {
			return pac22JSONNoT(pac22Body(d, "https://i", "c", map[string]any{"oidc_client_secret": "x", "provider_type": "saml"}))
		}, "provider_type 'saml' is not supported"},
		{"other type", func(d string) string {
			return pac22JSONNoT(pac22Body(d, "https://i", "c", map[string]any{"oidc_client_secret": "x", "provider_type": "ldap"}))
		}, "provider_type must be 'oidc'"},
		{"missing issuer", func(d string) string {
			b := pac22Body(d, "", "c", map[string]any{"oidc_client_secret": "x"})
			delete(b, "oidc_issuer")
			return pac22JSONNoT(b)
		}, "oidc_issuer is required"},
		{"null issuer", func(d string) string {
			return pac22JSONNoT(pac22Body(d, "", "c", map[string]any{"oidc_client_secret": "x", "oidc_issuer": nil}))
		}, "oidc_issuer is required"},
		{"empty client id", func(d string) string {
			return pac22JSONNoT(pac22Body(d, "https://i", "", map[string]any{"oidc_client_secret": "x"}))
		}, "oidc_client_id is required"},
		{"empty secret", func(d string) string {
			return pac22JSONNoT(pac22Body(d, "https://i", "c", map[string]any{"oidc_client_secret": ""}))
		}, "oidc_client_secret must not be empty"},
		{"null secret", func(d string) string {
			return pac22JSONNoT(pac22Body(d, "https://i", "c", map[string]any{"oidc_client_secret": nil}))
		}, "oidc_client_secret must not be empty"},
		{"absent secret, nothing stored", func(d string) string {
			return pac22JSONNoT(pac22Body(d, "https://i", "c", nil))
		}, "oidc_client_secret is required"},
	}

	for _, c := range cases {
		t.Run(c.name+"/no configuration", func(t *testing.T) {
			org := pac22NewOrg(t)
			pac22AssertError(t, pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID, c.body(org.domain)), http.StatusBadRequest, c.msg)
			pac22AssertUnchanged(t, nil, pac22Stored(t, org.domain))
		})
		if c.name == "absent secret, nothing stored" {
			continue
		}
		t.Run(c.name+"/stored configuration", func(t *testing.T) {
			org := pac22NewOrg(t)
			pac22Store(t, &storage.SSOProvider{Domain: org.domain, ProviderName: "Working", ProviderType: "oidc", Enabled: true,
				OIDCIssuer: "https://i", OIDCClientID: "c", OIDCClientSecret: pac22Secret(t)})
			before := pac22Stored(t, org.domain)
			rec := pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID, c.body(org.domain))
			pac22AssertError(t, rec, http.StatusBadRequest, c.msg)
			pac22AssertNoSecret(t, rec, before.OIDCClientSecret)
			pac22AssertUnchanged(t, before, pac22Stored(t, org.domain))
		})
	}
}

func pac22JSONNoT(v map[string]any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestPAC22_MembershipIsCheckedBeforeTheBody(t *testing.T) {
	u, err := storage.UpsertUser(openTestDB(t), "pac22-noorg-"+uuid.NewString()+"@gmail.com", "N", "", "google", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	pac22AssertError(t, pac22Do(t, http.MethodPost, "/api/admin/sso", u.ID, "{"), http.StatusForbidden, "org membership required")
}

func TestPAC22_ValidationRunsBeforeTheDomainMatch(t *testing.T) {
	// Checks 1-5 come before the domain 403 (PAC-50: provider_domain_mismatch,
	// which replaced "can only configure SSO for your own domain").
	org := pac22NewOrg(t)
	rec := pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID,
		pac22JSON(t, pac22Body("someone-else.test", "https://i", "c", map[string]any{"oidc_client_secret": ""})))
	pac22AssertError(t, rec, http.StatusBadRequest, "oidc_client_secret must not be empty")
	rec = pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID,
		pac22JSON(t, pac22Body("someone-else.test", "https://i", "c", nil)))
	pac22AssertError(t, rec, http.StatusForbidden, "provider_domain_mismatch")
}

func TestPAC22_StoredSAMLRowStaysReadableAndRemovable(t *testing.T) {
	// AC-8, second half (retrieve and remove; suspend and resume wait for
	// setSsoProviderState).
	org := pac22NewOrg(t)
	pac22Store(t, &storage.SSOProvider{Domain: org.domain, ProviderName: "SAML", ProviderType: "saml", Enabled: true,
		SAMLEntryPoint: "https://e", SAMLIssuer: "urn:i", SAMLCert: pac22Cert(t)})
	if got := pac22DecodeList(t, pac22Do(t, http.MethodGet, "/api/admin/sso", org.userID, "")); len(got) != 1 {
		t.Fatalf("providers = %d, want 1", len(got))
	}
	if rec := pac22Do(t, http.MethodDelete, "/api/admin/sso/"+org.domain, org.userID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", rec.Code)
	}
	if pac22Stored(t, org.domain) != nil {
		t.Fatal("SAML row not removed")
	}
}

func TestPAC22_ReplaceOfSAMLRowStoresNoSAMLValues(t *testing.T) {
	// D-8: an accepted write stores the unsupported kind's values as absent.
	org := pac22NewOrg(t)
	pac22Store(t, &storage.SSOProvider{Domain: org.domain, ProviderName: "SAML", ProviderType: "saml", Enabled: true,
		SAMLEntryPoint: "https://e", SAMLIssuer: "urn:i", SAMLCert: pac22Cert(t)})
	p := pac22DecodeCreated(t, pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID,
		pac22JSON(t, pac22Body(org.domain, "https://i", "c", map[string]any{"oidc_client_secret": pac22Secret(t)}))))
	if p.HasSamlCert || p.SamlEntryPoint != "" || p.SamlIssuer != "" || p.ProviderType != gen.SsoProviderProviderTypeOidc {
		t.Errorf("replaced = %+v", p)
	}
}

// --- AC-4: the indicator tracks the secret and nothing else ------------------

func TestPAC22_IndicatorSurvivesANameChange(t *testing.T) {
	org := pac22NewOrg(t)
	secret := pac22Secret(t)
	first := pac22DecodeCreated(t, pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID,
		pac22JSON(t, pac22Body(org.domain, "https://i", "c", map[string]any{"oidc_client_secret": secret}))))
	second := pac22DecodeCreated(t, pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID,
		pac22JSON(t, pac22Body(org.domain, "https://i", "c", map[string]any{"oidc_client_secret": secret, "provider_name": "Renamed"}))))
	if !second.HasOidcClientSecret || second.ProviderName != "Renamed" {
		t.Fatalf("after rename: %+v", second)
	}
	first.ProviderName, first.UpdatedAt = second.ProviderName, second.UpdatedAt
	if first != second {
		t.Errorf("a described value other than the name and updated_at changed:\n %+v\n %+v", first, second)
	}
	pac22AssertError(t, pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID,
		pac22JSON(t, pac22Body(org.domain, "https://i", "c", map[string]any{"oidc_client_secret": ""}))),
		http.StatusBadRequest, "oidc_client_secret must not be empty")
	items := pac22DecodeList(t, pac22Do(t, http.MethodGet, "/api/admin/sso", org.userID, ""))
	if len(items) != 1 || !items[0].HasOidcClientSecret {
		t.Errorf("indicator after refused empty write: %+v", items)
	}
}

// --- AC-22, AC-23, AC-26, AC-14: the kept secret is the one sign-in uses -----

func TestPAC22_OmittedSecretIsKeptAndSignInStillWorks(t *testing.T) {
	org := pac22NewOrg(t)
	s1 := pac22Secret(t)
	idp := newFakeIdP(t, "client-"+org.domain)
	idp.acceptSecret = s1
	pac22DecodeCreated(t, pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID,
		pac22JSON(t, pac22Body(org.domain, idp.srv.URL, idp.clientID, map[string]any{"oidc_client_secret": s1}))))
	if !pac22SignIn(t, org.domain, idp) {
		t.Fatal("sign-in with the supplied secret failed (AC-14 precondition)")
	}

	rec := pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID,
		pac22JSON(t, pac22Body(org.domain, idp.srv.URL, idp.clientID, map[string]any{"provider_name": "Renamed"})))
	pac22AssertNoSecret(t, rec, s1)
	p := pac22DecodeCreated(t, rec)
	if !p.HasOidcClientSecret || p.ProviderName != "Renamed" {
		t.Errorf("after a replace leaving the secret out: %+v", p)
	}
	if !pac22SignIn(t, org.domain, idp) {
		t.Fatal("sign-in after a replace that left the secret out failed: the stored secret was not kept")
	}

	// AC-26: "" and null are refused and the secret is unchanged.
	for _, v := range []any{"", nil} {
		pac22AssertError(t, pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID,
			pac22JSON(t, pac22Body(org.domain, idp.srv.URL, idp.clientID, map[string]any{"oidc_client_secret": v}))),
			http.StatusBadRequest, "oidc_client_secret must not be empty")
		if !pac22SignIn(t, org.domain, idp) {
			t.Fatalf("sign-in after a refused %v secret failed", v)
		}
	}
}

// JSON property names are case sensitive and the contract declares only
// "oidc_client_secret". A case variant ("OIDC_CLIENT_SECRET") is an undeclared
// property, ignored like any other today, so it can neither clear, replace nor
// supply the secret (AC-7, AC-26; review finding on PR #237: Go's struct
// decoding matches field names case-insensitively, which made "" under a case
// variant store an empty secret).
func TestPAC22_CaseVariantSecretKeyIsNotTheSecret(t *testing.T) {
	org := pac22NewOrg(t)
	s1 := pac22Secret(t)
	idp := newFakeIdP(t, "client-"+org.domain)
	idp.acceptSecret = s1
	pac22DecodeCreated(t, pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID,
		pac22JSON(t, pac22Body(org.domain, idp.srv.URL, idp.clientID, map[string]any{"oidc_client_secret": s1}))))

	base := pac22JSON(t, pac22Body(org.domain, idp.srv.URL, idp.clientID, nil))
	withTail := func(tail string) string { return strings.TrimSuffix(base, "}") + "," + tail + "}" }
	// PAC-50 (contract revision 8, createSsoProvider step 2c): a case variant
	// of a property name is an undeclared property, so it is now refused with
	// 400 `unknown property: <name>` instead of being ignored. PAC-22's
	// guarantee holds more strictly: a variant never becomes the secret, the
	// stored secret is unchanged, and sign-in still works.
	cases := map[string]struct{ body, name string }{
		"upper case, empty":           {withTail(`"OIDC_CLIENT_SECRET":""`), "OIDC_CLIENT_SECRET"},
		"upper case, null":            {withTail(`"OIDC_CLIENT_SECRET":null`), "OIDC_CLIENT_SECRET"},
		"mixed case, other value":     {withTail(`"Oidc_Client_Secret":"` + pac22Secret(t) + `"`), "Oidc_Client_Secret"},
		"exact then variant, empty":   {withTail(`"oidc_client_secret":"` + s1 + `","OIDC_CLIENT_SECRET":""`), "OIDC_CLIENT_SECRET"},
		"exact then variant, a value": {withTail(`"oidc_client_secret":"` + s1 + `","OIDC_CLIENT_SECRET":"` + pac22Secret(t) + `"`), "OIDC_CLIENT_SECRET"},
	}
	for name, c := range cases {
		rec := pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID, c.body)
		pac22AssertNoSecret(t, rec, s1)
		pac22AssertError(t, rec, http.StatusBadRequest, "unknown property: "+c.name)
		if stored := pac22Stored(t, org.domain); stored == nil || stored.OIDCClientSecret != s1 {
			t.Errorf("%s: the stored secret changed", name)
		}
		if !pac22SignIn(t, org.domain, idp) {
			t.Fatalf("%s: sign-in failed after the write", name)
		}
	}

	// With nothing stored, a case variant does not supply a secret either.
	other := pac22NewOrg(t)
	body := pac22JSON(t, pac22Body(other.domain, "https://i", "c", nil))
	body = strings.TrimSuffix(body, "}") + `,"OIDC_CLIENT_SECRET":"` + pac22Secret(t) + `"}`
	pac22AssertError(t, pac22Do(t, http.MethodPost, "/api/admin/sso", other.userID, body),
		http.StatusBadRequest, "unknown property: OIDC_CLIENT_SECRET")
	pac22AssertUnchanged(t, nil, pac22Stored(t, other.domain))
}

func TestPAC22_SuppliedSecretReplacesTheStoredOne(t *testing.T) {
	org := pac22NewOrg(t)
	s1, s2 := pac22Secret(t), pac22Secret(t)
	idp := newFakeIdP(t, "client-"+org.domain)
	pac22DecodeCreated(t, pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID,
		pac22JSON(t, pac22Body(org.domain, idp.srv.URL, idp.clientID, map[string]any{"oidc_client_secret": s1}))))

	pac22DecodeCreated(t, pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID,
		pac22JSON(t, pac22Body(org.domain, idp.srv.URL, idp.clientID, map[string]any{"oidc_client_secret": s2}))))
	idp.acceptSecret = s2
	if !pac22SignIn(t, org.domain, idp) {
		t.Fatal("sign-in with the IdP accepting only the new secret failed")
	}
	idp.acceptSecret = s1
	if pac22SignIn(t, org.domain, idp) {
		t.Fatal("sign-in succeeded with the IdP accepting only the old secret: the supplied secret was ignored")
	}
}

// --- AC-24, AC-25: a kept secret is bound to where it is sent ----------------

func TestPAC22_ChangedIssuerOrClientIDRequiresTheSecret(t *testing.T) {
	const msg = "oidc_client_secret is required when oidc_issuer or oidc_client_id changes"
	for _, change := range []string{"issuer", "client id", "issuer trailing slash"} {
		t.Run(change, func(t *testing.T) {
			org := pac22NewOrg(t)
			s1 := pac22Secret(t)
			pac22Store(t, &storage.SSOProvider{Domain: org.domain, ProviderName: "Okta", ProviderType: "oidc", Enabled: true,
				OIDCIssuer: "https://good.example", OIDCClientID: "c1", OIDCClientSecret: s1})
			before := pac22Stored(t, org.domain)
			issuer, client := "https://good.example", "c1"
			switch change {
			case "issuer":
				issuer = "https://evil.example"
			case "client id":
				client = "c2"
			default:
				issuer = "https://good.example/"
			}
			rec := pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID, pac22JSON(t, pac22Body(org.domain, issuer, client, nil)))
			pac22AssertError(t, rec, http.StatusBadRequest, msg)
			pac22AssertNoSecret(t, rec, s1)
			pac22AssertUnchanged(t, before, pac22Stored(t, org.domain))

			s2 := pac22Secret(t)
			pac22DecodeCreated(t, pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID,
				pac22JSON(t, pac22Body(org.domain, issuer, client, map[string]any{"oidc_client_secret": s2}))))
			if got := pac22Stored(t, org.domain); got.OIDCClientSecret != s2 || got.OIDCIssuer != issuer || got.OIDCClientID != client {
				t.Errorf("with the secret supplied the write did not store it")
			}
		})
	}
}

func TestPAC22_NothingToKeepIsRefused(t *testing.T) {
	const msg = "oidc_client_secret is required"
	t.Run("legacy row with an empty secret", func(t *testing.T) {
		org := pac22NewOrg(t)
		pac22Store(t, &storage.SSOProvider{Domain: org.domain, ProviderName: "Old", ProviderType: "oidc", Enabled: true,
			OIDCIssuer: "https://i", OIDCClientID: "c"})
		before := pac22Stored(t, org.domain)
		pac22AssertError(t, pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID, pac22JSON(t, pac22Body(org.domain, "https://i", "c", nil))),
			http.StatusBadRequest, msg)
		pac22AssertUnchanged(t, before, pac22Stored(t, org.domain))
	})
	t.Run("SAML row holding a client secret", func(t *testing.T) {
		org := pac22NewOrg(t)
		s := pac22Secret(t)
		pac22Store(t, &storage.SSOProvider{Domain: org.domain, ProviderName: "Old", ProviderType: "saml", Enabled: true,
			OIDCIssuer: "https://i", OIDCClientID: "c", OIDCClientSecret: s, SAMLEntryPoint: "https://e"})
		before := pac22Stored(t, org.domain)
		items := pac22DecodeList(t, pac22Do(t, http.MethodGet, "/api/admin/sso", org.userID, ""))
		if len(items) != 1 || !items[0].HasOidcClientSecret {
			t.Fatalf("indicator on a SAML row with a secret = %+v, want true", items)
		}
		rec := pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID, pac22JSON(t, pac22Body(org.domain, "https://i", "c", nil)))
		pac22AssertError(t, rec, http.StatusBadRequest, msg)
		pac22AssertNoSecret(t, rec, s)
		pac22AssertUnchanged(t, before, pac22Stored(t, org.domain))
	})
	t.Run("the key is the domain the write is stored under", func(t *testing.T) {
		// Check 6 reads the row the write would be stored under. Since PAC-50
		// that is the row under the lower-cased domain (contract revision 8,
		// createSsoProvider step 4, which PAC-22's own text anticipated): an
		// upper-case domain is a replace of that one row and keeps its secret;
		// no second row exists under another casing.
		org := pac22NewOrg(t)
		s := pac22Secret(t)
		pac22Store(t, &storage.SSOProvider{Domain: org.domain, ProviderName: "Okta", ProviderType: "oidc", Enabled: true,
			OIDCIssuer: "https://i", OIDCClientID: "c", OIDCClientSecret: s})
		upper := strings.ToUpper(org.domain)
		rec := pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID, pac22JSON(t, pac22Body(upper, "https://i", "c", nil)))
		got := pac22DecodeCreated(t, rec)
		pac22AssertNoSecret(t, rec, s)
		if got.Domain != org.domain || !got.HasOidcClientSecret {
			t.Fatalf("replace under the upper-case domain = %+v; want the lower-case row with its secret kept", got)
		}
		var n int
		if err := openTestDB(t).QueryRow(`SELECT count(*) FROM sso_providers WHERE lower(domain) = $1`, org.domain).Scan(&n); err != nil || n != 1 {
			t.Fatalf("rows under any casing = %d, %v; want 1", n, err)
		}
		if stored := pac22Stored(t, org.domain); stored.OIDCClientSecret != s {
			t.Fatal("the kept secret changed")
		}
	})
}

// --- AC-11, AC-28: the active state is never written by a replace ------------

func TestPAC22_ReplaceKeepsTheActiveState(t *testing.T) {
	org := pac22NewOrg(t)
	created := pac22DecodeCreated(t, pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID,
		pac22JSON(t, pac22Body(org.domain, "https://i", "c", map[string]any{"oidc_client_secret": pac22Secret(t)}))))
	if created.State != gen.Active {
		t.Fatalf("a create starts %q, want active", created.State)
	}
	replaced := pac22DecodeCreated(t, pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID,
		pac22JSON(t, pac22Body(org.domain, "https://i", "c", map[string]any{"provider_name": "Again"}))))
	if replaced.State != gen.Active {
		t.Fatalf("active did not stay active: %q", replaced.State)
	}
	pac22SetEnabled(t, org.domain, false)
	for _, extra := range []map[string]any{nil, {"oidc_client_secret": pac22Secret(t)}} {
		p := pac22DecodeCreated(t, pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID,
			pac22JSON(t, pac22Body(org.domain, "https://i", "c", extra))))
		if p.State != gen.Suspended {
			t.Fatalf("suspended did not stay suspended (secret supplied: %v): %q", extra != nil, p.State)
		}
	}
}

func TestPAC22_SuspensionStoredDuringAReplaceIsNotUndone(t *testing.T) {
	for _, supply := range []bool{false, true} {
		for _, target := range []bool{false, true} { // false: suspend, true: resume
			org := pac22NewOrg(t)
			pac22Store(t, &storage.SSOProvider{Domain: org.domain, ProviderName: "Okta", ProviderType: "oidc", Enabled: !target,
				OIDCIssuer: "https://i", OIDCClientID: "c", OIDCClientSecret: pac22Secret(t)})
			extra := map[string]any{"provider_name": "New name"}
			if supply {
				extra["oidc_client_secret"] = pac22Secret(t)
			}
			ssoBeforeStoreHook = func() { pac22SetEnabled(t, org.domain, target) }
			rec := pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID, pac22JSON(t, pac22Body(org.domain, "https://i", "c", extra)))
			ssoBeforeStoreHook = nil
			p := pac22DecodeCreated(t, rec)
			stored := pac22Stored(t, org.domain)
			if stored.Enabled != target || p.ProviderName != "New name" || stored.ProviderName != "New name" {
				t.Errorf("secret supplied %v, transition to enabled=%v: stored enabled=%v name=%q", supply, target, stored.Enabled, stored.ProviderName)
			}
		}
	}
}

// --- AC-27: interleaved writes ------------------------------------------------

func TestPAC22_InterleavedReplaceNeverPairsAnIssuerWithAnotherSecret(t *testing.T) {
	const msg = "oidc_client_secret is required when oidc_issuer or oidc_client_id changes"
	org := pac22NewOrg(t)
	s1, s2 := pac22Secret(t), pac22Secret(t)
	idp2 := newFakeIdP(t, "c")
	idp2.acceptSecret = s2
	pac22Store(t, &storage.SSOProvider{Domain: org.domain, ProviderName: "Okta", ProviderType: "oidc", Enabled: true,
		OIDCIssuer: "https://i1.example", OIDCClientID: "c", OIDCClientSecret: s1})

	// B's replace (I2, C, S2) is stored between A's check and A's store.
	ssoBeforeStoreHook = func() {
		ssoBeforeStoreHook = nil
		pac22DecodeCreated(t, pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID,
			pac22JSON(t, pac22Body(org.domain, idp2.srv.URL, "c", map[string]any{"oidc_client_secret": s2}))))
	}
	rec := pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID, pac22JSON(t, pac22Body(org.domain, "https://i1.example", "c", nil)))
	ssoBeforeStoreHook = nil
	pac22AssertError(t, rec, http.StatusBadRequest, msg)
	pac22AssertNoSecret(t, rec, s1, s2)
	if got := pac22Stored(t, org.domain); got.OIDCIssuer != idp2.srv.URL || got.OIDCClientSecret != s2 {
		t.Fatalf("stored issuer %q with secret of length %d; want I2 with S2", got.OIDCIssuer, len(got.OIDCClientSecret))
	}
	if !pac22SignIn(t, org.domain, idp2) {
		t.Fatal("sign-in through I2 with S2 failed")
	}

	// Sequential: B then A refuses A, the final state is I2 with S2.
	pac22AssertError(t, pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID, pac22JSON(t, pac22Body(org.domain, "https://i1.example", "c", nil))),
		http.StatusBadRequest, msg)
	if got := pac22Stored(t, org.domain); got.OIDCIssuer != idp2.srv.URL || got.OIDCClientSecret != s2 {
		t.Fatal("B then A did not end with I2 and S2")
	}
}

func TestPAC22_SequentialAThenBEndsWithB(t *testing.T) {
	org := pac22NewOrg(t)
	s1, s2 := pac22Secret(t), pac22Secret(t)
	pac22Store(t, &storage.SSOProvider{Domain: org.domain, ProviderName: "Okta", ProviderType: "oidc", Enabled: true,
		OIDCIssuer: "https://i1.example", OIDCClientID: "c", OIDCClientSecret: s1})
	pac22DecodeCreated(t, pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID, pac22JSON(t, pac22Body(org.domain, "https://i1.example", "c", nil))))
	pac22DecodeCreated(t, pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID,
		pac22JSON(t, pac22Body(org.domain, "https://i2.example", "c", map[string]any{"oidc_client_secret": s2}))))
	if got := pac22Stored(t, org.domain); got.OIDCIssuer != "https://i2.example" || got.OIDCClientSecret != s2 {
		t.Fatal("A then B did not end with I2 and S2")
	}
}

func TestPAC22_RemovalDuringAReplaceIsNotUndone(t *testing.T) {
	org := pac22NewOrg(t)
	pac22Store(t, &storage.SSOProvider{Domain: org.domain, ProviderName: "Okta", ProviderType: "oidc", Enabled: true,
		OIDCIssuer: "https://i1.example", OIDCClientID: "c", OIDCClientSecret: pac22Secret(t)})
	ssoBeforeStoreHook = func() {
		ssoBeforeStoreHook = nil
		if err := storage.DeleteSSOProvider(openTestDB(t), org.domain); err != nil {
			t.Fatal(err)
		}
	}
	rec := pac22Do(t, http.MethodPost, "/api/admin/sso", org.userID, pac22JSON(t, pac22Body(org.domain, "https://i1.example", "c", nil)))
	ssoBeforeStoreHook = nil
	pac22AssertError(t, rec, http.StatusBadRequest, "oidc_client_secret is required")
	if pac22Stored(t, org.domain) != nil {
		t.Fatal("a configuration was recreated with an empty secret")
	}
}

func TestPAC22_StorageRefusalsMapToFixedMessages(t *testing.T) {
	for err, want := range map[error]string{
		storage.ErrSSOClientSecretEmpty:         "oidc_client_secret must not be empty",
		storage.ErrSSOClientSecretRequired:      "oidc_client_secret is required",
		storage.ErrSSOClientSecretTargetChanged: "oidc_client_secret is required when oidc_issuer or oidc_client_id changes",
		errors.New("connection reset"):          "",
		nil:                                     "",
	} {
		if got := ssoKeepRefusal(err); got != want {
			t.Errorf("ssoKeepRefusal(%v) = %q, want %q", err, got, want)
		}
	}
}
