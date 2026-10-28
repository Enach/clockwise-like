package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/nlp/nlptest"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// NEW-llm-bring-your-own, Part 1 (docs/specs/NEW-llm-bring-your-own.md,
// contracts/features/NEW-llm-bring-your-own.yaml). Paceday sends an AI
// request only with a key the user entered, bound to the provider it was
// entered for; Ollama and every ambient cloud identity are gone; "AI not
// configured" is an answer (parse 200 + not_configured, probe and brief
// refresh 409), never an error. Every test runs with both recording
// transports, so a request to any provider is seen.

const (
	byokAzure   = "my-resource.openai.azure.com"
	byokBedrock = "bedrock-runtime.us-east-1.amazonaws.com"
	byokVertex  = "aiplatform.googleapis.com"
	byokKey     = "sk-byok-sentinel-key-1"
	byokKey2    = "sk-byok-sentinel-key-2"
	byokLegacy  = "sk-byok-legacy-unbound-key"
)

// byokApproved approves one destination for each cloud provider, so that a
// request a credential could authenticate would be sent.
var byokApproved = map[string][]string{
	"azure_openai": {byokAzure},
	"bedrock":      {byokBedrock},
	"vertex":       {byokVertex},
}

type byok struct {
	*pac66
	usr *nlptest.Recorder
	// escaped records the escaped path of every request the ambient
	// recorder sees (the recorder itself keeps only the decoded path).
	mu      sync.Mutex
	escaped []string
}

func newBYOK(t *testing.T, email string) *byok {
	t.Helper()
	// The machine Paceday runs on has ambient credentials of every kind; none
	// may be read for an AI request (spec AC-2, O-2, O-3).
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIABYOKAMBIENTXXXXX")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "byok-ambient-aws-secret")
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "byok-ambient-bedrock-bearer")
	t.Setenv("AZURE_CLIENT_SECRET", "byok-ambient-azure-secret")
	b := &byok{pac66: &pac66{t: t, user: createTestUser(t, email+"@byok.example"), rec: nlptest.Ambient(t, byokApproved)}}
	b.usr = nlptest.UserDestinations(t, "", "")
	b.rec.Answer = func(r *http.Request) *http.Response {
		b.mu.Lock()
		b.escaped = append(b.escaped, r.URL.EscapedPath())
		b.mu.Unlock()
		return nil
	}
	return b
}

func (b *byok) requests() []nlptest.Request {
	return append(b.rec.Requests(), b.usr.Requests()...)
}

func (b *byok) noRequests(what string) {
	b.t.Helper()
	if got := b.requests(); len(got) != 0 {
		b.t.Errorf("%s: recorded %d outbound requests, want zero: %+v", what, len(got), got)
	}
}

// sql runs a statement on the caller's settings row, as a value stored before
// this change (or by another path) would be.
func (b *byok) sql(set string, args ...any) {
	b.t.Helper()
	db := openTestDB(b.t)
	if _, err := storage.GetOrCreateSettingsByUser(db, b.user); err != nil {
		b.t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE settings SET `+set+` WHERE user_id = $1`, append([]any{b.user}, args...)...); err != nil {
		b.t.Fatalf("seed %q: %v", set, err)
	}
}

func (b *byok) mustPatch(body string) map[string]any {
	b.t.Helper()
	w := b.patch(body)
	if w.Code != http.StatusOK {
		b.t.Fatalf("PATCH %s = %d %s", body, w.Code, w.Body.String())
	}
	var doc map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &doc)
	return doc
}

func (b *byok) get() (*httptest.ResponseRecorder, map[string]any) {
	b.t.Helper()
	req := withUser(httptest.NewRequest(http.MethodGet, "/api/settings", nil), b.user)
	w := httptest.NewRecorder()
	(&settingsHandlers{db: openTestDB(b.t)}).getSettings(w, req)
	if w.Code != http.StatusOK {
		b.t.Fatalf("GET /api/settings = %d %s", w.Code, w.Body.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		b.t.Fatal(err)
	}
	return w, doc
}

// strictDecode decodes body into v rejecting unknown members: the contract's
// schemas are all additionalProperties: false.
func strictDecode(t *testing.T, what string, body []byte, v any) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("%s is not valid against the contract: %v; body %s", what, err, body)
	}
}

type notConfigured struct {
	Reason   string   `json:"reason"`
	Provider string   `json:"provider,omitempty"`
	Enablers []string `json:"enablers"`
	Message  string   `json:"message"`
}

func checkNotConfigured(t *testing.T, what string, nc *notConfigured, reason, provider string, enablers ...string) {
	t.Helper()
	if nc == nil {
		t.Fatalf("%s: no notConfigured", what)
	}
	if nc.Reason != reason || nc.Provider != provider {
		t.Errorf("%s: reason %q provider %q, want %q %q", what, nc.Reason, nc.Provider, reason, provider)
	}
	got := append([]string(nil), nc.Enablers...)
	sort.Strings(got)
	sort.Strings(enablers)
	if !reflect.DeepEqual(got, enablers) {
		t.Errorf("%s: enablers %v, want %v", what, nc.Enablers, enablers)
	}
	if strings.TrimSpace(nc.Message) == "" {
		t.Errorf("%s: empty message", what)
	}
	for _, k := range []string{byokKey, byokKey2, byokLegacy} {
		if strings.Contains(nc.Message, k) {
			t.Errorf("%s: message carries a key", what)
		}
	}
}

// parseNotConfigured asserts the parse 200 not-configured answer (AC-13, AC-35).
func (b *byok) parseNotConfigured(reason, provider string, enablers ...string) {
	b.t.Helper()
	w := b.parse()
	if w.Code != http.StatusOK {
		b.t.Fatalf("parse = %d %s, want 200", w.Code, w.Body.String())
	}
	var res gen.NLPParseResult
	strictDecode(b.t, "parse 200", w.Body.Bytes(), &res)
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(w.Body.Bytes(), &raw)
	var nc notConfigured
	if err := json.Unmarshal(raw["not_configured"], &nc); err != nil {
		b.t.Fatalf("parse: no not_configured: %s", w.Body.String())
	}
	checkNotConfigured(b.t, "parse", &nc, reason, provider, enablers...)
	if res.Intent != "unknown" || res.Error == nil || *res.Error != nc.Message {
		b.t.Errorf("parse: intent/error = %s, want unknown and the not_configured message", w.Body.String())
	}
	for k := range raw {
		if k != "intent" && k != "error" && k != "not_configured" {
			b.t.Errorf("parse: %q present beside not_configured (nothing else may be): %s", k, w.Body.String())
		}
	}
	if strings.Contains(w.Body.String(), "0001-01-01") {
		b.t.Errorf("parse carries the zero date (API-080): %s", w.Body.String())
	}
}

// conflict409 asserts an LLMNotConfiguredResponse 409.
func conflict409(t *testing.T, what string, w *httptest.ResponseRecorder, reason, provider string, enablers ...string) notConfigured {
	t.Helper()
	if w.Code != http.StatusConflict {
		t.Fatalf("%s = %d %s, want 409", what, w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("%s: Content-Type %q", what, ct)
	}
	var body gen.LLMNotConfiguredResponse
	strictDecode(t, what, w.Body.Bytes(), &body)
	var raw struct {
		Error         string        `json:"error"`
		NotConfigured notConfigured `json:"notConfigured"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &raw)
	checkNotConfigured(t, what, &raw.NotConfigured, reason, provider, enablers...)
	if raw.Error != raw.NotConfigured.Message {
		t.Errorf("%s: error %q != notConfigured.message %q", what, raw.Error, raw.NotConfigured.Message)
	}
	return raw.NotConfigured
}

func (b *byok) aiStatus() map[string]any {
	b.t.Helper()
	w, doc := b.get()
	var s gen.Settings
	strictDecode(b.t, "GET /api/settings", w.Body.Bytes(), &s)
	st, _ := doc["aiStatus"].(map[string]any)
	if st == nil {
		b.t.Fatalf("no aiStatus in %s", w.Body.String())
	}
	return st
}

// allThree asserts that parse, brief refresh and the probe {} give exactly one
// row's reason, and that nothing is sent (spec AC-40).
func (b *byok) allThree(reason, provider string, enablers ...string) {
	b.t.Helper()
	b.parseNotConfigured(reason, provider, enablers...)
	conflict409(b.t, "brief refresh", b.brief(http.MethodPost, "evt-"+b.user.String()[:8]), reason, provider, enablers...)
	conflict409(b.t, "probe {}", b.probe(`{}`), reason, provider, enablers...)
	st := b.aiStatus()
	if st["configured"] != false || st["provider"] != nil {
		b.t.Errorf("aiStatus = %v, want configured false and no provider", st)
	}
	raw, _ := json.Marshal(st["notConfigured"])
	var nc notConfigured
	_ = json.Unmarshal(raw, &nc)
	checkNotConfigured(b.t, "aiStatus.notConfigured", &nc, reason, provider, enablers...)
	b.noRequests("not configured")
}

func joinOrg(t *testing.T, user uuid.UUID, domain string) {
	t.Helper()
	db := openTestDB(t)
	org, err := storage.UpsertOrg(db, domain, domain)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.SetUserOrg(db, user, org.ID); err != nil {
		t.Fatal(err)
	}
}

// --------------------------------------------------------------- AC-2, AC-40

func TestBYOK_CloudProvidersWithoutAKeySendNothing(t *testing.T) { // AC-2, AC-40 row 3
	for _, tc := range []struct{ provider, patch string }{
		{"azure_openai", `{"llmProvider":"azure_openai","azureEndpoint":"https://` + byokAzure + `","azureDeployment":"gpt"}`},
		{"bedrock", `{"llmProvider":"bedrock","awsRegion":"us-east-1"}`},
		{"vertex", `{"llmProvider":"vertex"}`},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			b := newBYOK(t, "ac2-"+tc.provider)
			b.mustPatch(tc.patch)
			b.allThree("needs_key", tc.provider, "user")
		})
	}
}

// AC-42 (spec revision 2): API keys in the server's environment under the
// providers' own variable names (OPENAI_API_KEY, ANTHROPIC_API_KEY,
// AWS_BEARER_TOKEN_BEDROCK, set by nlptest.Ambient) are deployment-level
// credentials (O-2). A user with no key of their own gets "needs a key" on
// every operation, nothing is sent, and no response carries those values.
func TestBYOK_EnvironmentKeysAreNeverUsed(t *testing.T) {
	for _, tc := range []struct{ provider, patch string }{
		{"openai", `{"llmProvider":"openai"}`},
		{"anthropic", `{"llmProvider":"anthropic"}`},
		{"bedrock", `{"llmProvider":"bedrock","awsRegion":"us-east-1"}`},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			b := newBYOK(t, "ac42-"+tc.provider)
			b.mustPatch(tc.patch)
			b.allThree("needs_key", tc.provider, "user")
			settingsW, _ := b.get()
			for what, w := range map[string]*httptest.ResponseRecorder{
				"probe {}": b.probe(`{}`),
				"settings": settingsW,
			} {
				if leaks := nlptest.Leaks(w.Body.String()); len(leaks) != 0 {
					t.Errorf("%s response carries server environment credentials %v", what, leaks)
				}
			}
		})
	}
}

func TestBYOK_EveryNotConfiguredRowGivesOneReason(t *testing.T) { // AC-40 (Part 1 rows), AC-8b, AC-11, AC-17
	t.Run("row 1 off", func(t *testing.T) {
		b := newBYOK(t, "row1")
		b.mustPatch(`{"llmProvider":""}`)
		b.allThree("off", "", "user")
	})
	t.Run("row 3 openai without key", func(t *testing.T) {
		b := newBYOK(t, "row3")
		b.mustPatch(`{"llmProvider":"openai"}`)
		b.allThree("needs_key", "openai", "user")
	})
	t.Run("row 6 new account, no organisation", func(t *testing.T) {
		b := newBYOK(t, "row6")
		if _, doc := b.get(); doc["llmProvider"] != "organization" {
			t.Errorf("a new account's llmProvider = %v, want organization", doc["llmProvider"])
		}
		b.allThree("nothing_configured", "", "user")
	})
	t.Run("row 6 new account in an organisation", func(t *testing.T) {
		b := newBYOK(t, "row6org")
		joinOrg(t, b.user, "row6org.byok.example")
		b.allThree("nothing_configured", "", "user") // Part 1: no admin can act yet (spec rev. 2)
		if st := b.aiStatus(); st["inOrganization"] != true {
			t.Errorf("aiStatus.inOrganization = %v, want true", st["inOrganization"])
		}
	})
	t.Run("row 8 ollama", func(t *testing.T) {
		b := newBYOK(t, "row8")
		b.sql(`llm_provider = 'ollama', ollama_base_url = 'http://ollama.internal:11434', ollama_model = 'llama3'`)
		b.allThree("provider_removed", "", "user")
		if _, doc := b.get(); doc["llmProvider"] != "unsupported" {
			t.Errorf("llmProvider = %v, want unsupported", doc["llmProvider"])
		}
	})
	t.Run("row 8 ollama in an organisation", func(t *testing.T) {
		b := newBYOK(t, "row8org")
		joinOrg(t, b.user, "row8org.byok.example")
		b.sql(`llm_provider = 'ollama'`)
		b.allThree("provider_removed", "", "user") // Part 1: no admin can act yet (spec rev. 2)
	})
	t.Run("row 2 configured", func(t *testing.T) {
		b := newBYOK(t, "row2")
		b.mustPatch(`{"llmProvider":"anthropic","llmApiKey":"` + byokKey + `"}`)
		st := b.aiStatus()
		if st["configured"] != true || st["provider"] != "anthropic" || st["notConfigured"] != nil {
			t.Errorf("aiStatus = %v, want configured anthropic and no notConfigured", st)
		}
	})
}

// --------------------------------------------------------------- AC-3, AC-4, AC-5

func TestBYOK_CloudProvidersCarryOnlyTheUsersKey(t *testing.T) { // AC-3, AC-4, AC-5
	t.Run("azure_openai api-key header", func(t *testing.T) {
		b := newBYOK(t, "ac3")
		b.mustPatch(`{"llmProvider":"azure_openai","azureEndpoint":"https://` + byokAzure + `","azureDeployment":"gpt","azureApiVersion":"2024-10-21","llmApiKey":"` + byokKey + `"}`)
		if w := b.probe(`{}`); w.Code != http.StatusOK {
			t.Fatalf("probe = %d %s", w.Code, w.Body.String())
		}
		got := b.requests()
		if len(got) != 1 || got[0].Host != byokAzure || got[0].Path != "/openai/deployments/gpt/chat/completions" || got[0].Query != "api-version=2024-10-21" {
			t.Fatalf("requests = %+v", got)
		}
		if got[0].Header.Get("api-key") != byokKey || got[0].Header.Get("Authorization") != "" {
			t.Errorf("headers = %v, want api-key = the key and no Authorization", got[0].Header)
		}
	})
	t.Run("bedrock bearer key on the Converse path", func(t *testing.T) {
		b := newBYOK(t, "ac4")
		b.mustPatch(`{"llmProvider":"bedrock","awsRegion":"us-east-1","bedrockModel":"anthropic.claude-sonnet-4-5-20251001-v1:0","llmApiKey":"` + byokKey + `"}`)
		if w := b.probe(`{}`); w.Code != http.StatusOK {
			t.Fatalf("probe = %d %s", w.Code, w.Body.String())
		}
		got := b.requests()
		if len(got) != 1 || got[0].Host != byokBedrock || got[0].Scheme != "https" {
			t.Fatalf("requests = %+v", got)
		}
		if len(b.escaped) != 1 || b.escaped[0] != "/model/anthropic.claude-sonnet-4-5-20251001-v1:0/converse" {
			t.Errorf("escaped path = %v, want exactly /model/anthropic.claude-sonnet-4-5-20251001-v1:0/converse", b.escaped)
		}
		if got[0].Header.Get("Authorization") != "Bearer "+byokKey {
			t.Errorf("Authorization = %q, want the Bedrock API key as a bearer token", got[0].Header.Get("Authorization"))
		}
		for k := range got[0].Header {
			if strings.HasPrefix(strings.ToLower(k), "x-amz-") {
				t.Errorf("an AWS signature header was sent: %s", k)
			}
		}
		for _, v := range got[0].Header {
			if strings.Contains(strings.Join(v, " "), "byok-ambient") || strings.Contains(strings.Join(v, " "), "AKIABYOK") {
				t.Errorf("an ambient credential was sent: %v", got[0].Header)
			}
		}
	})
	t.Run("bedrock model with a slash stays one segment", func(t *testing.T) {
		b := newBYOK(t, "ac4slash")
		b.mustPatch(`{"llmProvider":"bedrock","awsRegion":"us-east-1","bedrockModel":"arn:aws:bedrock:us-east-1:1:inference-profile/x","llmApiKey":"` + byokKey + `"}`)
		if w := b.probe(`{}`); w.Code != http.StatusOK {
			t.Fatalf("probe = %d %s", w.Code, w.Body.String())
		}
		if len(b.escaped) != 1 || b.escaped[0] != "/model/arn:aws:bedrock:us-east-1:1:inference-profile%2Fx/converse" {
			t.Errorf("escaped path = %v", b.escaped)
		}
	})
	t.Run("vertex express key header", func(t *testing.T) {
		b := newBYOK(t, "ac5")
		b.mustPatch(`{"llmProvider":"vertex","vertexModel":"gemini-2.0-flash-001","llmApiKey":"` + byokKey + `"}`)
		if w := b.probe(`{}`); w.Code != http.StatusOK {
			t.Fatalf("probe = %d %s", w.Code, w.Body.String())
		}
		got := b.requests()
		if len(got) != 1 || got[0].Host != byokVertex || got[0].Path != "/v1/publishers/google/models/gemini-2.0-flash-001:generateContent" || got[0].Query != "" {
			t.Fatalf("requests = %+v", got)
		}
		if got[0].Header.Get("x-goog-api-key") != byokKey || got[0].Header.Get("Authorization") != "" {
			t.Errorf("headers = %v, want x-goog-api-key = the key and no Authorization", got[0].Header)
		}
	})
}

// --------------------------------------------------------------- AC-6, AC-8, AC-9, AC-10

func TestBYOK_OllamaCannotBeChosen(t *testing.T) { // AC-6
	b := newBYOK(t, "ac6")
	before, _ := b.get()
	w := b.patch(`{"llmProvider":"ollama"}`)
	if w.Code != http.StatusBadRequest || !strings.HasPrefix(errorOf(w), "llmProvider ") {
		t.Fatalf("PATCH ollama = %d %s, want 400 naming llmProvider", w.Code, w.Body.String())
	}
	if after, _ := b.get(); after.Body.String() != before.Body.String() {
		t.Errorf("something was stored: %s -> %s", before.Body.String(), after.Body.String())
	}
	for _, removed := range []string{"ollamaBaseUrl", "ollamaModel", "awsProfile", "gcpProject", "gcpLocation"} {
		if w := b.patch(`{"` + removed + `":"x"}`); w.Code != http.StatusBadRequest || errorOf(w) != "unknown property: "+removed {
			t.Errorf("PATCH %s = %d %s, want 400 unknown property", removed, w.Code, w.Body.String())
		}
	}
}

func errorOf(w *httptest.ResponseRecorder) string {
	var body struct{ Error string }
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return body.Error
}

func TestBYOK_LegacyRowsReadValidAndWriteBackUnchanged(t *testing.T) { // AC-8, AC-9, AC-16
	type stored struct {
		provider, legacyKey, ollamaBase, awsProfile, gcpProject string
	}
	read := func(b *byok) stored {
		var s stored
		if err := openTestDB(t).QueryRow(`SELECT llm_provider, llm_api_key, ollama_base_url, aws_profile, gcp_project FROM settings WHERE user_id = $1`, b.user).
			Scan(&s.provider, &s.legacyKey, &s.ollamaBase, &s.awsProfile, &s.gcpProject); err != nil {
			t.Fatal(err)
		}
		return s
	}
	for _, tc := range []struct {
		name, set, readProvider, reason, provider string
	}{
		{"ollama", `llm_provider = 'ollama', llm_api_key = '` + byokLegacy + `', ollama_base_url = 'http://ollama.internal:11434'`, "unsupported", "provider_removed", ""},
		{"bedrock", `llm_provider = 'bedrock', llm_api_key = '` + byokLegacy + `', aws_profile = 'prod'`, "bedrock", "needs_key", "bedrock"},
		{"azure_openai", `llm_provider = 'azure_openai', llm_api_key = '` + byokLegacy + `'`, "azure_openai", "needs_key", "azure_openai"},
		{"vertex", `llm_provider = 'vertex', llm_api_key = '` + byokLegacy + `', gcp_project = 'proj'`, "vertex", "needs_key", "vertex"},
		// Spec revision 2 (AC-16): an account stored "off" with a key.
		{"off", `llm_provider = '', llm_api_key = '` + byokLegacy + `'`, "", "off", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newBYOK(t, "ac8-"+tc.name)
			b.sql(tc.set)
			before := read(b)
			w, doc := b.get()
			var s gen.Settings
			strictDecode(t, "GET", w.Body.Bytes(), &s)
			if doc["llmProvider"] != tc.readProvider {
				t.Errorf("llmProvider = %v, want %s", doc["llmProvider"], tc.readProvider)
			}
			if doc["llmApiKeySet"] != false || doc["llmUnboundApiKeySet"] != true {
				t.Errorf("llmApiKeySet %v llmUnboundApiKeySet %v, want false true (AC-16)", doc["llmApiKeySet"], doc["llmUnboundApiKeySet"])
			}
			if strings.Contains(w.Body.String(), byokLegacy) {
				t.Fatalf("the read carries the key")
			}
			st := b.aiStatus()
			raw, _ := json.Marshal(st["notConfigured"])
			var nc notConfigured
			_ = json.Unmarshal(raw, &nc)
			checkNotConfigured(t, "aiStatus", &nc, tc.reason, tc.provider, "user")

			// AC-9: write back exactly the document read.
			if w := b.patch(w.Body.String()); w.Code != http.StatusOK {
				t.Fatalf("write-back = %d %s", w.Code, w.Body.String())
			}
			if after := read(b); after != before {
				t.Errorf("write-back changed storage: %+v -> %+v", before, after)
			}

			// AC-10 / AC-16: nothing is ever sent with the unbound key.
			b.parse()
			b.probe(`{}`)
			b.brief(http.MethodPost, "evt-ac8")
			for _, r := range b.requests() {
				for _, v := range r.Header {
					if strings.Contains(strings.Join(v, " "), byokLegacy) {
						t.Errorf("the unbound key was sent to %s", r.Host)
					}
				}
			}
			b.noRequests("legacy row")
		})
	}
}

func TestBYOK_OllamaUserProbeVariants(t *testing.T) { // AC-10, AC-10b
	b := newBYOK(t, "ac10")
	b.sql(`llm_provider = 'ollama', llm_api_key = '` + byokLegacy + `'`)
	for _, body := range []string{`{}`, `{"llmProvider":"unsupported","llmModel":""}`, `{"llmProvider":"unsupported","llmApiKey":"` + byokKey + `"}`} {
		conflict409(t, "probe "+body, b.probe(body), "provider_removed", "", "user")
	}
	b.noRequests("ollama probes")

	for _, tc := range []struct{ name, patch string }{
		{"openai with key", `{"llmProvider":"openai","llmApiKey":"` + byokKey + `"}`},
		{"openai", `{"llmProvider":"openai"}`},
		{"off", `{"llmProvider":""}`},
		{"organization", `{"llmProvider":"organization"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := newBYOK(t, "ac10b-"+strings.ReplaceAll(tc.name, " ", ""))
			o.mustPatch(tc.patch)
			w := o.probe(`{"llmProvider":"unsupported"}`)
			if w.Code != http.StatusBadRequest || errorOf(w) != "llmProvider unsupported can be probed only when it is your saved choice" {
				t.Errorf("probe unsupported = %d %s", w.Code, w.Body.String())
			}
			if w := o.patch(`{"llmProvider":"unsupported"}`); w.Code != http.StatusBadRequest ||
				errorOf(w) != "llmProvider unsupported cannot be chosen: choose off, organization or a key-based provider" {
				t.Errorf("PATCH unsupported = %d %s", w.Code, w.Body.String())
			}
			o.noRequests("unsupported")
		})
	}
	if w := b.probe(`{"llmProvider":"ollama"}`); w.Code != http.StatusBadRequest {
		t.Errorf("probe ollama = %d, want 400", w.Code)
	}
}

// --------------------------------------------------------------- AC-12, C-3, AC-37

func TestBYOK_ProbeSendsAKeyOnlyToItsProvider(t *testing.T) { // AC-12, AC-37, C-3
	b := newBYOK(t, "ac37")
	b.mustPatch(`{"llmProvider":"openai","llmApiKey":"` + byokKey + `"}`)

	nc := conflict409(t, "probe anthropic", b.probe(`{"llmProvider":"anthropic"}`), "needs_key", "anthropic", "user")
	if strings.Contains(nc.Message, "AI features are off") {
		t.Errorf("a probe of an unsaved provider says AI is off: %q (C-3)", nc.Message)
	}
	nc = conflict409(t, "probe empty key", b.probe(`{"llmApiKey":""}`), "needs_key", "openai", "user")
	if strings.Contains(nc.Message, "AI features are off") {
		t.Errorf("a probe with llmApiKey says AI is off: %q (C-3)", nc.Message)
	}
	b.noRequests("needs key")

	if w := b.probe(`{"llmProvider":"anthropic","llmApiKey":"` + byokKey2 + `"}`); w.Code != http.StatusOK {
		t.Fatalf("probe anthropic with key = %d %s", w.Code, w.Body.String())
	}
	got := b.requests()
	if len(got) != 1 || got[0].Host != "api.anthropic.com" || got[0].Header.Get("x-api-key") != byokKey2 {
		t.Fatalf("requests = %+v, want one to Anthropic with the submitted key", got)
	}
	for _, v := range got[0].Header {
		if strings.Contains(strings.Join(v, " "), byokKey) && !strings.Contains(strings.Join(v, " "), byokKey2) {
			t.Errorf("the OpenAI key went to Anthropic")
		}
	}

	if w := b.probe(`{"llmProvider":"openai"}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"provider":"openai"`) {
		t.Fatalf("probe openai = %d %s", w.Code, w.Body.String())
	}
	got = b.requests()
	if len(got) != 2 || got[1].Host != "api.openai.com" || got[1].Header.Get("Authorization") != "Bearer "+byokKey {
		t.Fatalf("requests = %+v, want the OpenAI key to OpenAI", got)
	}

	a := newBYOK(t, "ac12")
	a.mustPatch(`{"llmProvider":"anthropic"}`)
	nc = conflict409(t, "probe saved anthropic without key", a.probe(`{}`), "needs_key", "anthropic", "user")
	if !strings.Contains(nc.Message, "AI features are off") {
		t.Errorf("the saved choice's sentence does not say AI is off: %q (C-3)", nc.Message)
	}
	a.noRequests("AC-12")
}

// --------------------------------------------------------------- AC-15, AC-16b

func TestBYOK_KeysAreBoundToTheirProvider(t *testing.T) { // AC-15
	b := newBYOK(t, "ac15")
	b.mustPatch(`{"llmProvider":"openai","llmApiKey":"` + byokKey + `"}`)
	doc := b.mustPatch(`{"llmProvider":"anthropic"}`)
	keys, _ := doc["llmApiKeysSet"].(map[string]any)
	if doc["llmApiKeySet"] != false || keys["openai"] != true || keys["anthropic"] != false {
		t.Errorf("after switching: llmApiKeySet %v llmApiKeysSet %v", doc["llmApiKeySet"], keys)
	}
	b.parseNotConfigured("needs_key", "anthropic", "user")
	b.noRequests("anthropic without key")

	b.mustPatch(`{"llmProvider":"openai"}`)
	if w := b.probe(`{}`); w.Code != http.StatusOK {
		t.Fatalf("probe after switching back = %d %s", w.Code, w.Body.String())
	}
	if got := b.requests(); len(got) != 1 || got[0].Header.Get("Authorization") != "Bearer "+byokKey {
		t.Errorf("requests = %+v, want the OpenAI key usable again", got)
	}
}

func TestBYOK_UnboundKeyIsRemovedOnlyExplicitly(t *testing.T) { // AC-16b
	b := newBYOK(t, "ac16b")
	b.sql(`llm_provider = 'bedrock', llm_api_key = '` + byokLegacy + `'`)
	legacy := func() string {
		var k string
		_ = openTestDB(t).QueryRow(`SELECT llm_api_key FROM settings WHERE user_id = $1`, b.user).Scan(&k)
		return k
	}
	doc := b.mustPatch(`{"llmProvider":"openai","llmApiKey":"sk-new"}`)
	keys, _ := doc["llmApiKeysSet"].(map[string]any)
	if keys["openai"] != true || doc["llmUnboundApiKeySet"] != true || legacy() != byokLegacy {
		t.Errorf("after adding an OpenAI key: %v unbound %v legacy %q", keys, doc["llmUnboundApiKeySet"], legacy())
	}
	if doc := b.mustPatch(`{"llmApiKey":""}`); doc["llmUnboundApiKeySet"] != true {
		t.Errorf("llmApiKey \"\" removed the unbound key")
	}
	if w := b.patch(`{"llmUnboundApiKey":"x"}`); w.Code != http.StatusBadRequest ||
		errorOf(w) != `llmUnboundApiKey must be "" (it can only remove the key bound to no provider)` {
		t.Errorf("llmUnboundApiKey x = %d %s", w.Code, w.Body.String())
	}
	if w := b.patch(`{"llmUnboundApiKey":1}`); w.Code != http.StatusBadRequest || errorOf(w) != "invalid JSON: llmUnboundApiKey has the wrong type" {
		t.Errorf("llmUnboundApiKey 1 = %d %s", w.Code, w.Body.String())
	}
	b.mustPatch(`{"llmApiKey":"sk-new"}`)
	doc = b.mustPatch(`{"llmUnboundApiKey":""}`)
	keys, _ = doc["llmApiKeysSet"].(map[string]any)
	if doc["llmUnboundApiKeySet"] != false || legacy() != "" || keys["openai"] != true {
		t.Errorf("after llmUnboundApiKey \"\": unbound %v legacy %q keys %v", doc["llmUnboundApiKeySet"], legacy(), keys)
	}
	b.mustPatch(`{"llmUnboundApiKey":""}`) // no-op
	if w := b.patch(`{"llmProvider":"","llmApiKey":"sk-x"}`); w.Code != http.StatusBadRequest ||
		errorOf(w) != "llmApiKey cannot be set: choose openai, anthropic, azure_openai, bedrock or vertex as llmProvider first" {
		t.Errorf("key with provider off = %d %s", w.Code, w.Body.String())
	}
}

// --------------------------------------------------------------- AC-14, C-2

func TestBYOK_BriefRefreshNotConfiguredChangesNothing(t *testing.T) { // AC-14
	for _, state := range []string{"none", "pending", "ready", "failed"} {
		t.Run(state, func(t *testing.T) {
			b := newBYOK(t, "ac14-"+state)
			event := "evt-ac14-" + state
			if state != "none" {
				b.sql(`llm_provider = 'openai'`)
				if err := storage.UpsertMeetingBrief(openTestDB(t), &storage.MeetingBrief{
					CalendarEventID: event, UserID: b.user, Status: state, BriefText: "stored " + state,
				}); err != nil {
					t.Fatal(err)
				}
			}
			before := b.brief(http.MethodGet, event).Body.String()
			conflict409(t, "refresh", b.brief(http.MethodPost, event), map[bool]string{true: "nothing_configured", false: "needs_key"}[state == "none"],
				map[bool]string{true: "", false: "openai"}[state == "none"], "user")
			if after := b.brief(http.MethodGet, event).Body.String(); after != before {
				t.Errorf("brief changed: %s -> %s", before, after)
			}
			b.noRequests("refresh")
		})
	}
}

// --------------------------------------------------------------- C-1

func TestBYOK_KeyedCloudProviderStillRefusesAnUnapprovedDestination(t *testing.T) { // C-1
	for _, tc := range []struct {
		name, patch, force, reason, property string
		approved                             map[string][]string
	}{
		{"azure unapproved", `{"llmProvider":"azure_openai","azureEndpoint":"https://` + byokAzure + `","llmApiKey":"` + byokKey + `"}`,
			`azure_endpoint = 'https://attacker.example'`, "destination_not_approved", "azureEndpoint", byokApproved},
		{"azure empty", `{"llmProvider":"azure_openai","llmApiKey":"` + byokKey + `"}`, ``, "destination_not_set", "azureEndpoint", byokApproved},
		{"bedrock region", `{"llmProvider":"bedrock","awsRegion":"eu-west-1","llmApiKey":"` + byokKey + `"}`, ``, "destination_not_approved", "awsRegion", byokApproved},
		{"bedrock nothing approved", `{"llmProvider":"bedrock","llmApiKey":"` + byokKey + `"}`, ``, "provider_not_enabled", "", map[string][]string{"azure_openai": {byokAzure}}},
		{"vertex not approved", `{"llmProvider":"vertex","llmApiKey":"` + byokKey + `"}`, ``, "provider_not_enabled", "", map[string][]string{"vertex": {"europe-west4-aiplatform.googleapis.com"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newBYOK(t, "c1-"+strings.ReplaceAll(tc.name, " ", ""))
			b.rec = nlptest.Ambient(t, tc.approved)
			b.mustPatch(tc.patch)
			if tc.force != "" {
				b.sql(tc.force)
			}
			b.refused422(b.probe(`{}`), tc.reason, tc.property)
			w := b.parse()
			if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "not_configured") || !strings.Contains(w.Body.String(), `"error"`) {
				t.Errorf("parse = %d %s, want the refusal sentence without not_configured", w.Code, w.Body.String())
			}
			if w := b.brief(http.MethodPost, "evt-c1"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"failed"`) {
				t.Errorf("refresh = %d %s, want 200 failed", w.Code, w.Body.String())
			}
			b.noRequests("refused destination")
			if strings.Contains(b.probe(`{}`).Body.String(), byokKey) {
				t.Errorf("422 carries the key")
			}
		})
	}
}

// --------------------------------------------------------------- AC-18

func TestBYOK_GatewayPathPrefix(t *testing.T) { // AC-18
	b := newBYOK(t, "ac18")
	b.usr = nlptest.UserDestinations(t, "https://gw.example/openai", "")
	b.mustPatch(`{"llmProvider":"openai","llmBaseUrl":"https://gw.example/openai","llmApiKey":"` + byokKey + `"}`)
	if w := b.probe(`{}`); w.Code != http.StatusOK {
		t.Fatalf("probe = %d %s", w.Code, w.Body.String())
	}
	if got := b.usr.Requests(); len(got) != 1 || got[0].Host != "gw.example" || got[0].Path != "/openai/v1/chat/completions" || got[0].Query != "" {
		t.Fatalf("requests = %+v, want exactly /openai/v1/chat/completions on gw.example", got)
	}
	for _, v := range []string{"https://gw.example", "https://gw.example/other", "https://gw.example/openai/x", "https://gw.example/openai?x=1"} {
		if w := b.patch(`{"llmBaseUrl":"` + v + `"}`); w.Code != http.StatusBadRequest ||
			errorOf(w) != "llmBaseUrl must be an OpenAI or Anthropic destination approved on this deployment" {
			t.Errorf("write %s = %d %s", v, w.Code, w.Body.String())
		}
	}
	b.sql(`llm_base_url = 'https://gw.example/other'`)
	b.refused422(b.probe(`{}`), "destination_not_approved", "llmBaseUrl")
	if got := b.usr.Requests(); len(got) != 1 {
		t.Errorf("a refused gateway was sent to: %+v", got)
	}
	if w := b.patch(`{"llmBaseUrl":"https://gw.example/openai/"}`); w.Code != http.StatusOK {
		t.Errorf("trailing slash = %d %s", w.Code, w.Body.String())
	}
}

// --------------------------------------------------------------- AC-35

func TestBYOK_ParseWithoutARangeOmitsIt(t *testing.T) { // AC-35
	b := newBYOK(t, "ac35")
	b.mustPatch(`{"llmProvider":"openai","llmApiKey":"` + byokKey + `"}`)
	b.usr.Answer = func(r *http.Request) *http.Response {
		return nlptest.Response(r, http.StatusOK, `{"choices":[{"message":{"content":"{\"intent\":\"unknown\"}"}}]}`, nil)
	}
	w := b.parse()
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "range_start") || strings.Contains(w.Body.String(), "range_end") ||
		strings.Contains(w.Body.String(), "0001-01-01") || strings.Contains(w.Body.String(), "not_configured") {
		t.Errorf("parse = %d %s, want no range and no not_configured", w.Code, w.Body.String())
	}
}

// --------------------------------------------------------------- AC-7 (generated types mirror the bundle)

func TestBYOK_ContractHasNoRemovedProviderOrSetting(t *testing.T) { // AC-7
	for _, name := range []string{"OllamaBaseUrl", "OllamaModel", "AwsProfile", "GcpProject", "GcpLocation"} {
		if _, ok := reflect.TypeOf(gen.Settings{}).FieldByName(name); ok {
			t.Errorf("gen.Settings still has %s", name)
		}
	}
	raw, err := os.ReadFile(pac21Bundle)
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, "enum:") && strings.Contains(line, "ollama") {
			t.Errorf("bundle line %d still offers ollama: %s", i+1, strings.TrimSpace(line))
		}
		if strings.Contains(line, "PACEDAY_LLM_APPROVED_ORIGINS_OLLAMA") && !strings.Contains(line, "approves nothing") {
			t.Errorf("bundle line %d documents the Ollama approval as live: %s", i+1, strings.TrimSpace(line))
		}
	}
}

// --------------------------------------------------------------- C-2

func TestBYOK_BriefRefreshSettingsLoadFailureWritesNothing(t *testing.T) { // C-2
	for _, state := range []string{"none", "ready"} {
		t.Run(state, func(t *testing.T) {
			b := newBYOK(t, "c2-"+state)
			b.mustPatch(`{"llmProvider":"openai","llmApiKey":"` + byokKey + `"}`)
			event := "evt-c2-" + state
			if state != "none" {
				if err := storage.UpsertMeetingBrief(openTestDB(t), &storage.MeetingBrief{
					CalendarEventID: event, UserID: b.user, Status: state, BriefText: "stored " + state,
				}); err != nil {
					t.Fatal(err)
				}
			}
			// A stored value the settings read cannot decode: the caller's AI
			// configuration cannot be decided.
			b.sql(`lunch_breaks = '"not a map"'::jsonb`)
			before := b.brief(http.MethodGet, event).Body.String()
			w := b.brief(http.MethodPost, event)
			if w.Code != http.StatusInternalServerError || errorOf(w) != "internal error" {
				t.Fatalf("refresh = %d %s, want 500 internal error", w.Code, w.Body.String())
			}
			if after := b.brief(http.MethodGet, event).Body.String(); after != before {
				t.Errorf("brief changed: %s -> %s", before, after)
			}
			b.noRequests("settings load failure")
		})
	}
}
