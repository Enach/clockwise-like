package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/nlp/nlptest"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// PAC-73 Part 2 (spec docs/specs/NEW-llm-bring-your-own-part2.md §3.2-§3.4,
// contract revision 3.1): the three organisation-AI operations and the
// step-up purpose, through the real session middleware and PAC-50's
// step-up stand-ins (pac50_sso_helpers_test.go).

const (
	p2OrgKey   = "sk-p2-org-key-DISTINCTIVE-1"
	p2OrgKey2  = "sk-p2-org-key-DISTINCTIVE-2"
	p2Gateway  = "https://gw.p2.example/openai"
	p2AzureOrg = "p2-org.openai.azure.com"
)

type orgAIEnv struct {
	*ssoEnv
	oa *orgAIHandlers
}

func newOrgAIEnv(t *testing.T, breakGlass ...string) *orgAIEnv {
	t.Helper()
	e := &orgAIEnv{ssoEnv: newSSOEnv(t, breakGlass...)}
	e.oa = newOrgAIHandlers(e.db, e.h)
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(requireAuth(pac50Secret))
		r.Route("/api/admin/sso", e.h.mountAdmin)
		r.Route("/api/organization/ai", e.oa.mount)
	})
	e.router = r
	// The operator's approvals the destination checks read.
	nlptest.UserDestinations(t, p2Gateway, "")
	nlptest.Ambient(t, map[string][]string{"azure_openai": {p2AzureOrg}, "bedrock": {"bedrock-runtime.eu-west-1.amazonaws.com"}})
	return e
}

// stepupFor drives a step-up as the browser would, for purpose ("" = none).
func (e *orgAIEnv) stepupFor(t *testing.T, purpose, sess string) *httptest.ResponseRecorder {
	t.Helper()
	path := "/api/admin/sso/stepup?provider=google"
	if purpose != "" {
		path += "&purpose=" + purpose
	}
	start := e.do(t, http.MethodGet, path, "", sess)
	if start.Code != http.StatusFound {
		t.Fatalf("start = %d %s", start.Code, start.Body.String())
	}
	loc, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	e.google.set(func(i *stepupIdP) { i.nonce = loc.Query().Get("nonce") })
	return e.do(t, http.MethodGet, stepupCallbackPath+"?code=c1&state="+url.QueryEscape(loc.Query().Get("state")), "", sess, stateCookie(start))
}

// admin makes u a proven Google admin of org for the stand-in and returns a
// fresh session.
func (e *orgAIEnv) admin(t *testing.T, u *storage.User, org *storage.Org) string {
	t.Helper()
	e.google.set(googleAdmin(u.Email, org.Domain))
	return session(t, u, e.clock.Now())
}

// grantLLM obtains a live llm_org_key_write grant for u.
func (e *orgAIEnv) grantLLM(t *testing.T, sess string) {
	t.Helper()
	rec := e.stepupFor(t, "llm_org_key_write", sess)
	if got := rec.Header().Get("Location"); got != pac50Frontend+"/settings?stepup=granted&purpose=llm_org_key_write" {
		t.Fatalf("org-AI step-up landing = %d %q", rec.Code, got)
	}
}

func (e *orgAIEnv) getOrg(t *testing.T, sess string) (*httptest.ResponseRecorder, gen.OrganizationAIConfiguration) {
	t.Helper()
	rec := e.do(t, http.MethodGet, "/api/organization/ai", "", sess)
	var c gen.OrganizationAIConfiguration
	if rec.Code == http.StatusOK {
		strictDecode(t, "getOrganizationAI 200", rec.Body.Bytes(), &c)
	}
	return rec, c
}

func (e *orgAIEnv) patchOrg(t *testing.T, sess, body string) *httptest.ResponseRecorder {
	t.Helper()
	return e.do(t, http.MethodPatch, "/api/organization/ai", body, sess)
}

func (e *orgAIEnv) llmGrants(t *testing.T, u uuid.UUID, purpose string) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT count(*) FROM sso_stepup_grants WHERE user_id = $1 AND purpose = $2 AND consumed_at IS NULL`, u, purpose).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func wantStatus(t *testing.T, what string, rec *httptest.ResponseRecorder, code int) {
	t.Helper()
	if rec.Code != code {
		t.Fatalf("%s = %d %s, want %d", what, rec.Code, rec.Body.String(), code)
	}
}

func wantError(t *testing.T, what string, rec *httptest.ResponseRecorder, code int, msg string) {
	t.Helper()
	wantStatus(t, what, rec, code)
	if got := strings.TrimSpace(rec.Body.String()); got != `{"error":`+jsonString(msg)+`}` {
		t.Fatalf("%s body = %s, want {\"error\":%s}", what, got, jsonString(msg))
	}
}

func jsonString(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(s)
	return strings.TrimSpace(buf.String())
}

// P2-AC-1, P2-AC-2, P2-AC-17, PAC-50 AC-27: one proof of this purpose per
// change; an SSO grant is never spent here and the reverse.
func TestPAC73P2_OrgAIChangeNeedsAProofOfThisPurposePerChange(t *testing.T) {
	e := newOrgAIEnv(t)
	u, org := e.member(t, "p2-ac1")
	sess := e.admin(t, u, org)
	_, before := e.getOrg(t, sess)
	beforeRaw, _ := e.getOrg(t, sess)

	// P2-AC-1: no proof. The 403 says how to step up for this purpose.
	rec := e.patchOrg(t, sess, `{"configurationVersion":0,"provider":"openai","apiKey":"`+p2OrgKey+`"}`)
	wantStatus(t, "no proof", rec, http.StatusForbidden)
	want := `{"error":"organization_ai_write_requires_admin_stepup","stepup_purpose":"llm_org_key_write",` +
		`"stepup_scopes":{"google":["https://www.googleapis.com/auth/admin.directory.user.readonly"],` +
		`"microsoft":["Organization.Read.All","RoleManagement.Read.Directory"]},` +
		`"stepup_url":"/api/admin/sso/stepup","without_stepup":["organization_ai_configuration_writes"]}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Fatalf("403 = %s\nwant  %s", got, want)
	}
	var forbidden gen.OrganizationAIWriteStepupRequired
	strictDecode(t, "403", rec.Body.Bytes(), &forbidden)
	if after, _ := e.getOrg(t, sess); after.Body.String() != beforeRaw.Body.String() {
		t.Fatalf("a refused change changed the configuration: %s -> %s", beforeRaw.Body.String(), after.Body.String())
	}
	if before.ConfigurationVersion != 0 || before.Configured || before.KeySet || !before.PersonalKeysAllowed || before.UpdatedAt != nil {
		t.Fatalf("never set = %+v", before)
	}

	// A team owner proves nothing.
	if _, err := storage.CreateTeam(e.db, "P2 team", u.ID); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, "team owner", e.patchOrg(t, sess, `{"configurationVersion":0,"personalKeysAllowed":false}`), http.StatusForbidden)

	// An SSO grant: refused, and still live for an SSO write (AC-27).
	if loc := e.stepupFor(t, "", sess).Header().Get("Location"); loc != pac50Frontend+"/settings?stepup=granted" {
		t.Fatalf("SSO step-up landing = %q (must be PAC-50's, byte-identical)", loc)
	}
	wantStatus(t, "with an sso_write grant", e.patchOrg(t, sess, `{"configurationVersion":0,"personalKeysAllowed":false}`), http.StatusForbidden)
	if n := e.llmGrants(t, u.ID, "sso_write"); n != 1 {
		t.Fatalf("live sso_write grants = %d, want 1 (not consumed)", n)
	}
	if rec := e.do(t, http.MethodPost, "/api/admin/sso", createBody(org.Domain), sess); rec.Code/100 != 2 {
		t.Fatalf("SSO write with the sso_write grant = %d %s", rec.Code, rec.Body.String())
	}

	// An organisation-AI grant: an SSO write is refused and the grant lives.
	e.grantLLM(t, sess)
	rec = e.do(t, http.MethodDelete, "/api/admin/sso/"+org.Domain, "", sess)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "sso_write_requires_admin_stepup") {
		t.Fatalf("SSO write with an llm grant = %d %s", rec.Code, rec.Body.String())
	}
	if n := e.llmGrants(t, u.ID, "llm_org_key_write"); n != 1 {
		t.Fatalf("live llm grants after the SSO write = %d, want 1", n)
	}
	// P2-AC-2: the change succeeds; its answer equals what a member reads.
	rec = e.patchOrg(t, sess, `{"configurationVersion":0,"provider":"openai","model":"gpt-4o","apiKey":"`+p2OrgKey+`","baseUrl":"`+p2Gateway+`"}`)
	wantStatus(t, "with an llm grant", rec, http.StatusOK)
	var c gen.OrganizationAIConfiguration
	strictDecode(t, "patch 200", rec.Body.Bytes(), &c)
	if !c.Configured || !c.KeySet || c.ConfigurationVersion != 1 || *c.Provider != "openai" || *c.BaseUrl != p2Gateway || *c.Model != "gpt-4o" || c.UpdatedAt == nil {
		t.Fatalf("after the change = %s", rec.Body.String())
	}
	if read, _ := e.getOrg(t, sess); read.Body.String() != rec.Body.String() {
		t.Fatalf("patch answer %s != read %s", rec.Body.String(), read.Body.String())
	}
	// A second change needs a new step-up (P2-AC-17: the personal-keys
	// setting too).
	rec = e.patchOrg(t, sess, `{"configurationVersion":1,"personalKeysAllowed":false}`)
	wantStatus(t, "second change", rec, http.StatusForbidden)
	e.grantLLM(t, sess)
	wantStatus(t, "personal keys with a new proof", e.patchOrg(t, sess, `{"configurationVersion":1,"personalKeysAllowed":false}`), http.StatusOK)
	if n := e.llmGrants(t, u.ID, "llm_org_key_write"); n != 0 {
		t.Fatalf("the personal-keys change did not spend its proof: %d live", n)
	}

	// A user with no organisation: no organisation, no step-up offered.
	loner, _ := e.memberOf(t, "p2-loner-"+uuid.NewString()[:8]+"@gmail.com")
	ls := session(t, loner, e.clock.Now())
	wantError(t, "no org patch", e.patchOrg(t, ls, `{"configurationVersion":0,"personalKeysAllowed":false}`), http.StatusForbidden, "org membership required")
	wantError(t, "no org read", e.do(t, http.MethodGet, "/api/organization/ai", "", ls), http.StatusForbidden, "org membership required")
	wantError(t, "no org confirm", e.do(t, http.MethodPost, "/api/organization/ai/consent", `{"configurationVersion":1}`, ls), http.StatusForbidden, "org membership required")
}

// P2-AC-2 break-glass: once per fresh sign-in, under 10 minutes, including
// two at once (contract revision 3).
func TestPAC73P2_OrgAIBreakGlassOneChangePerSignIn(t *testing.T) {
	d := "pac73-bg-" + uuid.NewString()[:8] + ".test"
	e := newOrgAIEnv(t, "ops@"+d)
	u, _ := e.memberOf(t, "ops@"+d)
	now := e.clock.Now()
	wantStatus(t, "9 min", e.patchOrg(t, session(t, u, now.Add(-9*time.Minute)), `{"configurationVersion":0,"personalKeysAllowed":false}`), http.StatusOK)
	for what, iat := range map[string]time.Time{"10 min": now.Add(-10 * time.Minute), "future": now.Add(2 * time.Minute)} {
		wantError(t, what, e.patchOrg(t, session(t, u, iat), `{"configurationVersion":0,"model":"x","provider":"openai","apiKey":"k"}`),
			http.StatusForbidden, "organization_ai_write_requires_fresh_login")
	}
	s2 := session(t, u, now.Add(-2*time.Minute))
	wantStatus(t, "first change", e.patchOrg(t, s2, `{"configurationVersion":0,"provider":"openai","apiKey":"`+p2OrgKey+`"}`), http.StatusOK)
	wantError(t, "second change, same session", e.patchOrg(t, s2, `{"configurationVersion":1,"model":"gpt-x"}`),
		http.StatusForbidden, "organization_ai_write_requires_fresh_login")
	_, c := e.getOrg(t, s2)
	if c.Model == nil || *c.Model != "" || c.ConfigurationVersion != 1 {
		t.Fatalf("after the refused second change = %+v", c)
	}
	wantStatus(t, "after a new sign-in", e.patchOrg(t, session(t, u, now.Add(-time.Minute)), `{"configurationVersion":1,"model":"gpt-x"}`), http.StatusOK)

	// Two at once with one fresh, unused session: the first holds its
	// transaction between its check and its record until the second has
	// made its check; exactly one succeeds.
	s3 := session(t, u, now.Add(-30*time.Second))
	secondAtCheck := make(chan struct{})
	var calls sync.Mutex
	n := 0
	e.oa.hooks = &storage.OrgAIWriteHooks{
		BeforeAuthorize: func() {
			calls.Lock()
			n++
			mine := n
			calls.Unlock()
			if mine == 2 {
				close(secondAtCheck)
			}
		},
		AfterAuthorize: func() error {
			select {
			case <-secondAtCheck:
				time.Sleep(100 * time.Millisecond)
			case <-time.After(5 * time.Second):
			}
			return nil
		},
	}
	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i, body := range []string{`{"configurationVersion":1,"personalKeysAllowed":true}`, `{"configurationVersion":1,"model":"gpt-z"}`} {
		wg.Add(1)
		go func(i int, body string) {
			defer wg.Done()
			codes[i] = e.patchOrg(t, s3, body).Code
		}(i, body)
		time.Sleep(50 * time.Millisecond)
	}
	wg.Wait()
	e.oa.hooks = nil
	if !(codes[0] == http.StatusOK && codes[1] == http.StatusForbidden) {
		t.Fatalf("concurrent = %v, want exactly one 200 and the other 403", codes)
	}
	_, c = e.getOrg(t, s3)
	if !c.PersonalKeysAllowed || *c.Model != "gpt-x" {
		t.Fatalf("after the concurrent pair = %+v, want only the first change", c)
	}
}

// P2-AC-4: every body refusal names the problem, leaves the configuration
// unchanged and the grant live; each bad version gets one message.
func TestPAC73P2_OrgAIBodyRefusalsKeepTheGrant(t *testing.T) {
	e := newOrgAIEnv(t)
	u, org := e.member(t, "p2-ac4")
	sess := e.admin(t, u, org)
	e.grantLLM(t, sess)
	for _, tc := range []struct{ body, msg string }{
		{`[]`, "invalid JSON"},
		{`null`, "invalid JSON"},
		{`{"configurationVersion":0`, "invalid JSON"},
		{`{"configurationVersion":0,"ollamaBaseUrl":"x","model":null}`, "unknown property: ollamaBaseUrl"},
		{`{"model":"m"}`, "configurationVersion is required: send the version you were shown"},
		{`{"configurationVersion":null,"model":null}`, "configurationVersion must not be null"},
		{`{"configurationVersion":"3","model":"x"}`, "invalid JSON: configurationVersion has the wrong type"},
		{`{"configurationVersion":1.5,"model":"x"}`, "invalid JSON: configurationVersion has the wrong type"},
		{`{"configurationVersion":-1,"model":7}`, "configurationVersion must be 0 or more"},
		{`{"configurationVersion":0,"model":null,"apiKey":7}`, "model must not be null"},
		{`{"configurationVersion":0,"apiKey":7,"model":null}`, "invalid JSON: apiKey has the wrong type"},
		{`{"configurationVersion":0,"personalKeysAllowed":"no"}`, "invalid JSON: personalKeysAllowed has the wrong type"},
		{`{"configurationVersion":0}`, "send at least one property to change"},
		{`{"configurationVersion":0,"provider":"ollama","apiKey":"k"}`, `provider must be one of: "", openai, anthropic, azure_openai, bedrock, vertex`},
		{`{"configurationVersion":0,"provider":"","model":"m"}`, `provider "" removes the whole configuration; send no other property with it`},
		{`{"configurationVersion":0,"provider":"azure_openai","apiKey":"k","azureEndpoint":"https://` + p2AzureOrg + `/path"}`, "azureEndpoint must be https://<host> with no path, query, fragment or credentials"},
		{`{"configurationVersion":0,"provider":"azure_openai","apiKey":"k","azureEndpoint":"https://` + p2AzureOrg + `?a=b"}`, "azureEndpoint must be https://<host> with no path, query, fragment or credentials"},
		{`{"configurationVersion":0,"provider":"azure_openai","apiKey":"k","azureEndpoint":"https://` + p2AzureOrg + `#f"}`, "azureEndpoint must be https://<host> with no path, query, fragment or credentials"},
		{`{"configurationVersion":0,"provider":"azure_openai","apiKey":"k","azureEndpoint":"https://u:p@` + p2AzureOrg + `"}`, "azureEndpoint must be https://<host> with no path, query, fragment or credentials"},
		{`{"configurationVersion":0,"provider":"openai"}`, "apiKey is required when provider changes"},
		{`{"configurationVersion":0,"model":"m"}`, "model requires a provider"},
		{`{"configurationVersion":0,"apiKey":"k"}`, "apiKey requires a provider"},
		{`{"configurationVersion":0,"provider":"openai","apiKey":"k","azureEndpoint":"https://` + p2AzureOrg + `"}`, "azureEndpoint does not apply to openai"},
		{`{"configurationVersion":0,"provider":"azure_openai","apiKey":"k","model":"m"}`, "model does not apply to azure_openai"},
		{`{"configurationVersion":0,"provider":"openai","apiKey":"k","baseUrl":"` + p2Gateway + `?token=x"}`, "baseUrl must be an approved OpenAI destination on this deployment: an approved origin and exactly its path prefix, with no query, fragment or credentials"},
		{`{"configurationVersion":0,"provider":"openai","apiKey":"k","baseUrl":"https://gw.p2.example/other"}`, "baseUrl must be an approved OpenAI destination on this deployment: an approved origin and exactly its path prefix, with no query, fragment or credentials"},
		{`{"configurationVersion":0,"provider":"azure_openai","apiKey":"k","azureEndpoint":"https://unapproved.openai.azure.com"}`, "azureEndpoint must be an https URL whose host is approved for azure_openai on this deployment"},
		{`{"configurationVersion":0,"provider":"bedrock","apiKey":"k","awsRegion":"eu west"}`, "awsRegion must be a valid AWS region name"},
	} {
		rec := e.patchOrg(t, sess, tc.body)
		wantError(t, tc.body, rec, http.StatusBadRequest, tc.msg)
		if strings.Contains(rec.Body.String(), `"k"`) {
			t.Errorf("%s: the 400 echoes the key", tc.body)
		}
	}
	if n := e.llmGrants(t, u.ID, "llm_org_key_write"); n != 1 {
		t.Fatalf("live grants after the refusals = %d, want 1", n)
	}
	if _, c := e.getOrg(t, sess); c.ConfigurationVersion != 0 || c.Provider != nil || c.UpdatedAt != nil {
		t.Fatalf("a refused body changed the configuration: %+v", c)
	}
	// The same grant then authorises one valid change.
	wantStatus(t, "valid", e.patchOrg(t, sess, `{"configurationVersion":0,"provider":"azure_openai","apiKey":"k","azureEndpoint":"https://`+p2AzureOrg+`","azureDeployment":"dep"}`), http.StatusOK)
}

// errStore is the real store whose write fails after the proof (P2-AC-5)
// or applies another administrator's change first (P2-AC-40).
type interleavedStore struct {
	orgAIStore
	before func(ctx context.Context)
}

func (s interleavedStore) write(ctx context.Context, w storage.OrgAIWrite) (storage.OrgAIWriteOutcome, error) {
	if s.before != nil {
		s.before(ctx)
	}
	return s.orgAIStore.write(ctx, w)
}

// P2-AC-5: a storage failure inside the write leaves the configuration
// unchanged and the proof unspent; a retry succeeds with it.
func TestPAC73P2_OrgAIStoreFailureKeepsTheProof(t *testing.T) {
	d := "pac73-ac5-" + uuid.NewString()[:8] + ".test"
	e := newOrgAIEnv(t, "ops@"+d)
	u, org := e.memberOf(t, "admin@"+d)
	sess := e.admin(t, u, org)
	e.grantLLM(t, sess)
	e.oa.hooks = &storage.OrgAIWriteHooks{AfterAuthorize: func() error { return errors.New("injected store failure " + p2OrgKey) }}
	body := `{"configurationVersion":0,"provider":"anthropic","apiKey":"` + p2OrgKey + `"}`
	wantError(t, "store failure", e.patchOrg(t, sess, body), http.StatusInternalServerError, "internal error")
	if n := e.llmGrants(t, u.ID, "llm_org_key_write"); n != 1 {
		t.Fatalf("the failure spent the grant: %d live", n)
	}
	if _, c := e.getOrg(t, sess); c.ConfigurationVersion != 0 {
		t.Fatalf("the failure stored %+v", c)
	}
	e.oa.hooks = nil
	wantStatus(t, "retry", e.patchOrg(t, sess, body), http.StatusOK)

	// Break-glass: the session is still unused after a failure.
	ops, _ := e.memberOf(t, "ops@"+d)
	bs := session(t, ops, e.clock.Now().Add(-time.Minute))
	e.oa.hooks = &storage.OrgAIWriteHooks{AfterAuthorize: func() error { return errors.New("injected") }}
	wantStatus(t, "break-glass failure", e.patchOrg(t, bs, `{"configurationVersion":1,"model":"m"}`), http.StatusInternalServerError)
	e.oa.hooks = nil
	wantStatus(t, "break-glass retry, same session", e.patchOrg(t, bs, `{"configurationVersion":1,"model":"m"}`), http.StatusOK)
}

// P2-AC-40: a stale version is a conflict carrying the current
// configuration, decided by the write itself, and the proof stays live.
func TestPAC73P2_OrgAIStaleVersionIsAConflict(t *testing.T) {
	e := newOrgAIEnv(t)
	x, org := e.member(t, "p2-ac40")
	y, _ := e.memberOf(t, "second-admin@"+org.Domain)
	sx, sy := e.admin(t, x, org), session(t, y, e.clock.Now())
	e.grantLLM(t, sx)
	e.google.set(googleAdmin(y.Email, org.Domain))
	e.grantLLM(t, sy)

	// Two first setups naming 0, X's stored between Y's checks and Y's write.
	real := e.oa.store
	e.oa.store = interleavedStore{orgAIStore: real, before: func(ctx context.Context) {
		e.oa.store = real
		e.google.set(googleAdmin(x.Email, org.Domain))
		if rec := e.patchOrg(t, sx, `{"configurationVersion":0,"provider":"openai","apiKey":"`+p2OrgKey+`"}`); rec.Code != http.StatusOK {
			t.Errorf("X's change = %d %s", rec.Code, rec.Body.String())
		}
	}}
	rec := e.patchOrg(t, sy, `{"configurationVersion":0,"provider":"anthropic","apiKey":"`+p2OrgKey2+`"}`)
	e.oa.store = real
	wantStatus(t, "Y, interleaved first setup", rec, http.StatusConflict)
	var conflict gen.OrganizationAIConsentConflict
	strictDecode(t, "409", rec.Body.Bytes(), &conflict)
	if conflict.Reason != gen.ConfigurationChanged || conflict.Organization.ConfigurationVersion != 1 || *conflict.Organization.Provider != "openai" {
		t.Fatalf("409 = %s, want X's result", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), p2OrgKey) || strings.Contains(rec.Body.String(), p2OrgKey2) {
		t.Fatal("the 409 carries a key")
	}
	if n := e.llmGrants(t, y.ID, "llm_org_key_write"); n != 1 {
		t.Fatalf("Y's grant after the conflict: %d live, want 1", n)
	}
	// Y, shown 1 now: a body malformed on its own merits naming a stale
	// version is the 400; a stale body that fails only a state check is the
	// 409; the right version then succeeds with the same grant.
	wantStatus(t, "shape error, stale version", e.patchOrg(t, sy, `{"configurationVersion":0,"provider":"ollama"}`), http.StatusBadRequest)
	wantStatus(t, "state error, stale version", e.patchOrg(t, sy, `{"configurationVersion":0,"azureDeployment":"d"}`), http.StatusConflict)
	wantStatus(t, "state error, current version", e.patchOrg(t, sy, `{"configurationVersion":1,"azureDeployment":"d"}`), http.StatusBadRequest)

	// Interleaved on an existing configuration: X moves to 2 between Y's
	// checks and Y's write.
	e.google.set(googleAdmin(x.Email, org.Domain))
	e.grantLLM(t, sx)
	e.oa.store = interleavedStore{orgAIStore: real, before: func(ctx context.Context) {
		e.oa.store = real
		if rec := e.patchOrg(t, sx, `{"configurationVersion":1,"apiKey":"`+p2OrgKey2+`"}`); rec.Code != http.StatusOK {
			t.Errorf("X's key change = %d %s", rec.Code, rec.Body.String())
		}
	}}
	rec = e.patchOrg(t, sy, `{"configurationVersion":1,"provider":"anthropic","apiKey":"sk-typed-for-anthropic"}`)
	e.oa.store = real
	wantStatus(t, "Y, interleaved", rec, http.StatusConflict)
	cfg, _ := storage.GetOrgAIConfig(context.Background(), e.db, org.ID)
	if cfg.Provider != "openai" || cfg.APIKey != p2OrgKey2 || cfg.Version != 2 {
		t.Fatalf("A = %+v, want exactly X's change", cfg)
	}
	if n := e.llmGrants(t, y.ID, "llm_org_key_write"); n != 1 {
		t.Fatalf("Y's grant: %d live", n)
	}
	wantStatus(t, "Y naming the current version", e.patchOrg(t, sy, `{"configurationVersion":2,"model":"gpt-4o"}`), http.StatusOK)
}

// P2-AC-6: one operator record per change, never the key; the step-up's
// outcome record names this purpose.
func TestPAC73P2_OrgAIOperatorRecords(t *testing.T) {
	d := "pac73-ac6-" + uuid.NewString()[:8] + ".test"
	e := newOrgAIEnv(t, "ops@"+d)
	u, org := e.memberOf(t, "admin@"+d)
	sess := e.admin(t, u, org)
	e.grantLLM(t, sess)
	wantStatus(t, "change", e.patchOrg(t, sess, `{"configurationVersion":0,"provider":"openai","apiKey":"`+p2OrgKey+`"}`), http.StatusOK)
	ops, _ := e.memberOf(t, "ops@"+d)
	wantStatus(t, "break-glass", e.patchOrg(t, session(t, ops, e.clock.Now()), `{"configurationVersion":1,"personalKeysAllowed":false}`), http.StatusOK)
	var written []map[string]any
	var stepups []map[string]any
	for _, who := range []uuid.UUID{u.ID, ops.ID} {
		for _, ev := range e.events(t, who) {
			raw, _ := json.Marshal(ev.Details)
			if strings.Contains(string(raw), p2OrgKey) {
				t.Errorf("record %s carries the key", raw)
			}
			switch ev.Kind {
			case "llm_org_ai_written":
				written = append(written, ev.Details)
			case "sso_stepup":
				stepups = append(stepups, ev.Details)
			}
		}
	}
	if len(written) != 2 || written[0]["via"] != "stepup" || written[1]["via"] != "breakglass" || written[1]["session_iat"] == nil {
		t.Fatalf("records = %v", written)
	}
	if ch, _ := written[1]["changed"].([]any); len(ch) != 1 || ch[0] != "personalKeysAllowed" {
		t.Errorf("break-glass changed = %v", written[1]["changed"])
	}
	if len(stepups) != 1 || stepups[0]["purpose"] != "llm_org_key_write" || stepups[0]["outcome"] != "granted" {
		t.Fatalf("step-up records = %v", stepups)
	}
}

// P2-AC-3: the purpose travels in the state, is issued on the grant and is
// stated on the landing; absent, everything is PAC-50's.
func TestPAC73P2_StepupPurpose(t *testing.T) {
	e := newOrgAIEnv(t)
	u, org := e.member(t, "p2-ac3")
	sess := e.admin(t, u, org)
	for _, q := range []string{"&purpose=x", "&purpose=", "&purpose=sso_write&purpose=sso_write", "&purpose=LLM_ORG_KEY_WRITE"} {
		rec := e.do(t, http.MethodGet, "/api/admin/sso/stepup?provider=google"+q, "", sess)
		wantError(t, "start"+q, rec, http.StatusBadRequest, "purpose must be 'sso_write' or 'llm_org_key_write'")
		if stateCookie(rec) != nil {
			t.Errorf("start%s set the state cookie", q)
		}
		wantError(t, "options"+q, e.do(t, http.MethodGet, "/api/admin/sso/stepup/options?"+strings.TrimPrefix(q, "&"), "", sess),
			http.StatusBadRequest, "purpose must be 'sso_write' or 'llm_org_key_write'")
	}
	// provider is checked first.
	wantError(t, "provider first", e.do(t, http.MethodGet, "/api/admin/sso/stepup?provider=okta&purpose=x", "", sess),
		http.StatusBadRequest, "provider must be 'google' or 'microsoft'")
	// purpose before membership on the options read.
	loner, _ := e.memberOf(t, "p2-ac3-loner-"+uuid.NewString()[:8]+"@gmail.com")
	wantStatus(t, "orgless options, bad purpose", e.do(t, http.MethodGet, "/api/admin/sso/stepup/options?purpose=x", "", session(t, loner, e.clock.Now())), http.StatusBadRequest)

	opts := func(q string) gen.SsoStepupOptions {
		rec := e.do(t, http.MethodGet, "/api/admin/sso/stepup/options"+q, "", sess)
		wantStatus(t, "options"+q, rec, http.StatusOK)
		var o gen.SsoStepupOptions
		strictDecode(t, "options", rec.Body.Bytes(), &o)
		return o
	}
	if o := opts(""); strings.Join(o.WithoutStepup, ",") != "sso_configuration_writes" {
		t.Errorf("options = %v", o.WithoutStepup)
	}
	if o := opts("?purpose=sso_write"); strings.Join(o.WithoutStepup, ",") != "sso_configuration_writes" {
		t.Errorf("options sso_write = %v", o.WithoutStepup)
	}
	if o := opts("?purpose=llm_org_key_write"); strings.Join(o.WithoutStepup, ",") != "organization_ai_configuration_writes" {
		t.Errorf("options llm = %v", o.WithoutStepup)
	}

	// Granted with the purpose: an llm grant, landing with the purpose.
	e.grantLLM(t, sess)
	if e.llmGrants(t, u.ID, "llm_org_key_write") != 1 || e.llmGrants(t, u.ID, "sso_write") != 0 {
		t.Fatal("the org-AI step-up did not issue exactly one llm_org_key_write grant")
	}
	// Denied with the purpose: the landing still says the purpose.
	e.google.set(func(i *stepupIdP) { i.isAdmin = false })
	if loc := e.stepupFor(t, "llm_org_key_write", sess).Header().Get("Location"); loc != pac50Frontend+"/settings?stepup=denied&reason=not_admin&purpose=llm_org_key_write" {
		t.Fatalf("denied landing = %q", loc)
	}
	if loc := e.stepupFor(t, "", sess).Header().Get("Location"); loc != pac50Frontend+"/settings?stepup=denied&reason=not_admin" {
		t.Fatalf("SSO denied landing = %q (PAC-50's, byte-identical)", loc)
	}
	// sso_write named explicitly is PAC-50's flow.
	e.google.set(googleAdmin(u.Email, org.Domain))
	if loc := e.stepupFor(t, "sso_write", sess).Header().Get("Location"); loc != pac50Frontend+"/settings?stepup=granted" {
		t.Fatalf("sso_write landing = %q", loc)
	}
	if e.llmGrants(t, u.ID, "sso_write") != 1 {
		t.Fatal("purpose=sso_write did not issue an sso_write grant")
	}
	// A state signed before the deploy carries no purpose: sso_write.
	st := stepupState{UserID: u.ID, OrgID: org.ID, Provider: "google", Nonce: "n1", Expires: e.clock.Now().Add(time.Minute).Unix()}
	raw, _ := json.Marshal(st)
	if strings.Contains(string(raw), "pp") {
		t.Fatalf("a state with no purpose serialises one: %s", raw)
	}
	if st.purpose() != "sso_write" || (*stepupState)(nil).purpose() != "sso_write" {
		t.Fatal("a state without a purpose does not read as sso_write")
	}
}

// P2-AC-7, P2-AC-8: every member reads the same bytes, never a key; a
// member of B reads B and nothing of A.
func TestPAC73P2_OrgAIReadIsIdenticalAndKeyless(t *testing.T) {
	e := newOrgAIEnv(t)
	a1, orgA := e.member(t, "p2-ac7")
	a2, _ := e.memberOf(t, "member@"+orgA.Domain)
	b1, orgB := e.member(t, "p2-ac8")
	s1, s2, sb := e.admin(t, a1, orgA), session(t, a2, e.clock.Now()), session(t, b1, e.clock.Now())
	e.grantLLM(t, s1)
	written := e.patchOrg(t, s1, `{"configurationVersion":0,"provider":"azure_openai","apiKey":"`+p2OrgKey+`","azureEndpoint":"https://`+p2AzureOrg+`","azureDeployment":"dep"}`)
	wantStatus(t, "write", written, http.StatusOK)
	r1, c1 := e.getOrg(t, s1)
	r2, _ := e.getOrg(t, s2)
	if r1.Body.String() != r2.Body.String() || r1.Body.String() != written.Body.String() {
		t.Fatalf("members read different bytes:\n%s\n%s\n%s", written.Body.String(), r1.Body.String(), r2.Body.String())
	}
	if c1.Model != nil || c1.BaseUrl != nil || c1.AwsRegion != nil || *c1.AzureApiVersion != "" || c1.ChangeableBy != gen.OrganizationAIConfigurationChangeableByOrganizationAdmin {
		t.Fatalf("azure read = %s", r1.Body.String())
	}
	for what, rec := range map[string]*httptest.ResponseRecorder{"write": written, "read": r1} {
		if strings.Contains(rec.Body.String(), p2OrgKey) {
			t.Errorf("%s carries the key", what)
		}
	}
	rb, cb := e.getOrg(t, sb)
	if cb.ConfigurationVersion != 0 || cb.Provider != nil || strings.Contains(rb.Body.String(), p2AzureOrg) {
		t.Fatalf("B reads %s", rb.Body.String())
	}
	_ = orgB
	// B cannot confirm A's version: it confirms B, which has nothing.
	rec := e.do(t, http.MethodPost, "/api/organization/ai/consent", `{"configurationVersion":1}`, sb)
	wantStatus(t, "B confirms", rec, http.StatusConflict)
	if strings.Contains(rec.Body.String(), p2AzureOrg) {
		t.Fatal("B's 409 carries A's configuration")
	}
}
