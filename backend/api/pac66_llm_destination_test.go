package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/nlp"
	"github.com/Enach/paceday/backend/nlp/nlptest"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// PAC-66: the deployment's cloud identity goes only to hosts the operator
// approved, on every path that sends it (probe, parse, meeting-brief
// refresh), and the settings write refuses unapproved destinations. Every
// test runs with the spec's sentinel credential and recording transport
// (nlptest.Ambient), so the pre-PAC-66 code would have sent each refused
// request.

const (
	pac66Azure    = "my-resource.openai.azure.com"
	pac66VertexEU = "europe-west4-aiplatform.googleapis.com"
	pac66Attacker = "attacker.example"
)

type pac66 struct {
	t    *testing.T
	user uuid.UUID
	rec  *nlptest.Recorder
}

func newPAC66(t *testing.T, email string, approved map[string][]string, stored func(*storage.Settings)) *pac66 {
	t.Helper()
	p := &pac66{t: t, user: createTestUser(t, email), rec: nlptest.Ambient(t, approved)}
	if stored != nil {
		p.store(stored)
	}
	return p
}

// store writes the row directly, as a value stored before PAC-66 would be.
func (p *pac66) store(mutate func(*storage.Settings)) {
	p.t.Helper()
	db := openTestDB(p.t)
	s, err := storage.GetOrCreateSettingsByUser(db, p.user)
	if err != nil {
		p.t.Fatal(err)
	}
	mutate(s)
	if err := storage.SaveSettingsForUser(db, p.user, s); err != nil {
		p.t.Fatal(err)
	}
}

func (p *pac66) settings() *storage.Settings {
	p.t.Helper()
	s, err := storage.GetOrCreateSettingsByUser(openTestDB(p.t), p.user)
	if err != nil {
		p.t.Fatal(err)
	}
	return s
}

func (p *pac66) probe(body string) *httptest.ResponseRecorder {
	p.t.Helper()
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/llm/test", strings.NewReader(body)), p.user)
	w := httptest.NewRecorder()
	(&llmHandlers{db: openTestDB(p.t)}).testLLM(w, req)
	return w
}

func (p *pac66) patch(body string) *httptest.ResponseRecorder {
	p.t.Helper()
	req := withUser(httptest.NewRequest(http.MethodPatch, "/api/settings", strings.NewReader(body)), p.user)
	w := httptest.NewRecorder()
	(&settingsHandlers{db: openTestDB(p.t)}).patchSettings(w, req)
	return w
}

func (p *pac66) parse() *httptest.ResponseRecorder {
	p.t.Helper()
	db := openTestDB(p.t)
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/nlp/parse", strings.NewReader(`{"text":"meet bob tomorrow"}`)), p.user)
	w := httptest.NewRecorder()
	(&nlpHandlers{svc: &nlp.NLPService{DB: db}, smart: &mockScheduler{}, db: db}).parse(w, req)
	return w
}

func (p *pac66) brief(method, eventID string) *httptest.ResponseRecorder {
	p.t.Helper()
	req := withUser(httptest.NewRequest(method, "/api/meetings/"+eventID+"/brief", nil), p.user)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("event_id", eventID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	h := &meetingBriefHandlers{db: openTestDB(p.t)}
	if method == http.MethodGet {
		h.getBrief(w, req)
	} else {
		h.refreshBrief(w, req)
	}
	return w
}

func (p *pac66) noRequests(what string) {
	p.t.Helper()
	if got := p.rec.Requests(); len(got) != 0 {
		p.t.Errorf("%s: recorded %d outbound requests, want zero: %+v", what, len(got), got)
	}
}

// noLeak is AC-11: no sentinel credential, and a non-empty error sentence.
func noLeak(t *testing.T, what, body, sentence string) {
	t.Helper()
	if leaks := nlptest.Leaks(body); len(leaks) > 0 {
		t.Errorf("%s: the body carries the deployment credential %v: %s", what, leaks, body)
	}
	if strings.TrimSpace(sentence) == "" {
		t.Errorf("%s: empty error sentence: %s", what, body)
	}
}

// refused422 asserts the probe's PAC-66 refusal.
func (p *pac66) refused422(w *httptest.ResponseRecorder, reason, property string) {
	p.t.Helper()
	if w.Code != http.StatusUnprocessableEntity {
		p.t.Fatalf("probe status = %d, want 422; body %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		p.t.Errorf("Content-Type = %q, want JSON", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		p.t.Fatalf("422 body is not JSON: %s", w.Body.String())
	}
	for k := range body {
		if k != "error" && k != "reason" && k != "property" {
			p.t.Errorf("422 body has %q, which LLMDestinationRefusal does not allow", k)
		}
	}
	if body["reason"] != reason {
		p.t.Errorf("reason = %v, want %s", body["reason"], reason)
	}
	got, _ := body["property"].(string)
	if got != property {
		p.t.Errorf("property = %q, want %q", got, property)
	}
	msg, _ := body["error"].(string)
	noLeak(p.t, "probe 422", w.Body.String(), msg)
}

func TestPAC66_ProbeRefusesAnUnapprovedStoredEndpoint(t *testing.T) { // AC-1, AC-9, AC-11
	p := newPAC66(t, "pac66-ac1@example.com", map[string][]string{"azure_openai": {pac66Azure}},
		func(s *storage.Settings) { s.LLMProvider, s.AzureEndpoint = "azure_openai", "https://"+pac66Attacker })
	w := p.probe(`{}`)
	p.refused422(w, nlp.ReasonDestinationNotApproved, "azureEndpoint")
	if !strings.Contains(w.Body.String(), "not an approved") {
		t.Errorf("message does not say the endpoint is not approved: %s", w.Body.String())
	}
	p.noRequests("probe")
}

func TestPAC66_ProbeRefusesAnAuthorityChangingVertexLocation(t *testing.T) { // AC-2
	p := newPAC66(t, "pac66-ac2@example.com", map[string][]string{"vertex": {pac66VertexEU}},
		func(s *storage.Settings) { s.LLMProvider, s.GCPLocation = "vertex", pac66Attacker+"/x" })
	p.refused422(p.probe(`{}`), nlp.ReasonDestinationNotApproved, "gcpLocation")
	p.noRequests("probe")
}

func TestPAC66_ProbeRefusesAProviderWithNothingApproved(t *testing.T) { // AC-4
	for _, provider := range []string{"azure_openai", "vertex", "bedrock"} {
		t.Run(provider, func(t *testing.T) {
			p := newPAC66(t, "pac66-ac4-"+provider+"@example.com", nil, func(s *storage.Settings) {
				s.LLMProvider, s.AzureEndpoint, s.GCPLocation, s.AWSRegion = provider, "https://"+pac66Azure, "europe-west4", "eu-west-1"
			})
			w := p.probe(`{}`)
			p.refused422(w, nlp.ReasonProviderNotEnabled, "")
			if !strings.Contains(w.Body.String(), "not enabled on this deployment") {
				t.Errorf("message does not say the provider is not enabled: %s", w.Body.String())
			}
			p.noRequests("probe")
		})
	}
}

func TestPAC66_SettingsWriteRefusesUnapprovedDestinations(t *testing.T) { // AC-5, AC-7
	p := newPAC66(t, "pac66-ac5@example.com", map[string][]string{"azure_openai": {pac66Azure}, "vertex": {pac66VertexEU}},
		func(s *storage.Settings) {
			s.LLMProvider, s.AzureEndpoint, s.GCPLocation, s.AWSRegion = "openai", "https://"+pac66Azure, "europe-west4", "eu-west-1"
		})
	before := p.settings()
	for body, msg := range map[string]string{
		`{"azureEndpoint":"https://` + pac66Attacker + `"}`:                            nlp.MsgAzureEndpointNotApproved,
		`{"azureEndpoint":"http://` + pac66Azure + `"}`:                                nlp.MsgAzureEndpointNotApproved,
		`{"azureEndpoint":"https://` + pac66Azure + `:8443"}`:                          nlp.MsgAzureEndpointNotApproved,
		`{"azureEndpoint":"https://x@` + pac66Azure + `"}`:                             nlp.MsgAzureEndpointNotApproved,
		`{"gcpLocation":"attacker.example/x"}`:                                         nlp.MsgGCPLocationNotApproved,
		`{"gcpLocation":"us-central1"}`:                                                nlp.MsgGCPLocationNotApproved,
		`{"awsRegion":"attacker.example#"}`:                                            nlp.MsgAWSRegionInvalid,
		`{"workStart":"10:00","azureEndpoint":"https://` + pac66Attacker + `/openai"}`: nlp.MsgAzureEndpointNotApproved,
	} {
		w := p.patch(body)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), msg) {
			t.Errorf("PATCH %s: %d %s, want 400 %q", body, w.Code, w.Body.String(), msg)
		}
	}
	after := p.settings()
	if after.AzureEndpoint != before.AzureEndpoint || after.GCPLocation != before.GCPLocation ||
		after.AWSRegion != before.AWSRegion || after.WorkStart != before.WorkStart {
		t.Errorf("a refused write changed the stored settings: before %+v after %+v", before, after)
	}
	p.noRequests("settings write")
}

func TestPAC66_SettingsWriteWithNothingApproved(t *testing.T) { // AC-5 (not enabled), AC-15
	p := newPAC66(t, "pac66-ac15@example.com", nil, func(s *storage.Settings) {
		s.AzureEndpoint, s.GCPLocation, s.AWSRegion = "https://"+pac66Attacker, pac66Attacker+"/x", "attacker.example#"
	})
	for body, msg := range map[string]string{
		`{"azureEndpoint":"https://` + pac66Azure + `"}`: nlp.MsgAzureEndpointNotEnabled,
		`{"gcpLocation":"europe-west4"}`:                 nlp.MsgGCPLocationNotEnabled,
	} {
		if w := p.patch(body); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), msg) {
			t.Errorf("PATCH %s: %d %s, want 400 %q", body, w.Code, w.Body.String(), msg)
		}
	}
	// Clearing is always accepted, so a user can undo a bad value (D-3).
	if w := p.patch(`{"azureEndpoint":"","gcpLocation":"","awsRegion":""}`); w.Code != http.StatusOK {
		t.Fatalf("clearing: %d %s, want 200", w.Code, w.Body.String())
	}
	if s := p.settings(); s.AzureEndpoint != "" || s.GCPLocation != "" || s.AWSRegion != "" {
		t.Errorf("after clearing: %q %q %q, want all empty", s.AzureEndpoint, s.GCPLocation, s.AWSRegion)
	}
	p.noRequests("settings write")
}

func TestPAC66_ApprovedWriteThenProbeSendsOnce(t *testing.T) { // AC-6
	p := newPAC66(t, "pac66-ac6@example.com", map[string][]string{"azure_openai": {pac66Azure}}, nil)
	if w := p.patch(`{"llmProvider":"azure_openai","azureEndpoint":"https://` + pac66Azure + `","azureDeployment":"gpt"}`); w.Code != http.StatusOK {
		t.Fatalf("PATCH approved endpoint: %d %s", w.Code, w.Body.String())
	}
	w := p.probe(`{}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("probe = %d %s, want 200 ok", w.Code, w.Body.String())
	}
	got := p.rec.Requests()
	if len(got) != 1 || got[0].Host != pac66Azure || got[0].Scheme != "https" {
		t.Fatalf("requests = %+v, want exactly one https request to %s", got, pac66Azure)
	}
	noLeak(t, "probe 200", w.Body.String(), "ok")
}

func TestPAC66_ProbeRefusesAnInsecureStoredEndpoint(t *testing.T) { // AC-7
	p := newPAC66(t, "pac66-ac7@example.com", map[string][]string{"azure_openai": {pac66Azure}},
		func(s *storage.Settings) { s.LLMProvider, s.AzureEndpoint = "azure_openai", "http://"+pac66Azure })
	p.refused422(p.probe(`{}`), nlp.ReasonDestinationNotApproved, "azureEndpoint")
	p.noRequests("probe")
}

func TestPAC66_ProbeReportsARedirectAsAProviderFailure(t *testing.T) { // AC-8
	p := newPAC66(t, "pac66-ac8@example.com", map[string][]string{"azure_openai": {pac66Azure}},
		func(s *storage.Settings) { s.LLMProvider, s.AzureEndpoint = "azure_openai", "https://"+pac66Azure })
	p.rec.Answer = func(r *http.Request) *http.Response {
		return nlptest.Response(r, http.StatusFound, "", http.Header{"Location": {"https://sub." + pac66Azure + "/steal"}})
	}
	w := p.probe(`{}`)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("probe = %d %s, want 502", w.Code, w.Body.String())
	}
	got := p.rec.Requests()
	if len(got) != 1 || got[0].Host != pac66Azure {
		t.Errorf("requests = %+v, want only the original request to the approved host", got)
	}
	noLeak(t, "probe 502", w.Body.String(), w.Body.String())
}

func TestPAC66_EveryPathRefusesAStoredUnapprovedDestination(t *testing.T) { // AC-3, AC-9, AC-13, AC-14
	for _, tc := range []struct {
		provider string
		stored   func(*storage.Settings)
	}{
		{"azure_openai", func(s *storage.Settings) { s.AzureEndpoint = "https://" + pac66Attacker }},
		{"vertex", func(s *storage.Settings) { s.GCPLocation = pac66Attacker + "#" }},
		{"bedrock", func(s *storage.Settings) { s.AWSRegion = "eu-west-1" }},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			approved := map[string][]string{
				"azure_openai": {pac66Azure}, "vertex": {pac66VertexEU}, "bedrock": {"bedrock-runtime.us-east-1.amazonaws.com"},
			}
			p := newPAC66(t, "pac66-paths-"+tc.provider+"@example.com", approved, func(s *storage.Settings) {
				s.LLMProvider = tc.provider
				tc.stored(s)
			})

			// Parse: 200 JSON, intent unknown, an error sentence (AC-13).
			w := p.parse()
			if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
				t.Fatalf("parse = %d %q %s, want 200 JSON", w.Code, w.Header().Get("Content-Type"), w.Body.String())
			}
			var parsed struct{ Intent, Error string }
			_ = json.Unmarshal(w.Body.Bytes(), &parsed)
			if parsed.Intent != "unknown" || !strings.Contains(parsed.Error, "approved") {
				t.Errorf("parse body = %s, want intent unknown and a not-approved sentence", w.Body.String())
			}
			noLeak(t, "parse", w.Body.String(), parsed.Error)

			// Brief refresh: 200, failed, the sentence; stored the same (AC-14).
			w = p.brief(http.MethodPost, "pac66-event-"+tc.provider)
			var brief struct {
				Status    string `json:"status"`
				BriefText string `json:"brief_text"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &brief)
			if w.Code != http.StatusOK || brief.Status != "failed" || !strings.Contains(brief.BriefText, "approved") {
				t.Errorf("refresh = %d %s, want 200 failed with a not-approved sentence", w.Code, w.Body.String())
			}
			noLeak(t, "brief refresh", w.Body.String(), brief.BriefText)
			w = p.brief(http.MethodGet, "pac66-event-"+tc.provider)
			var read struct {
				Status    string `json:"status"`
				BriefText string `json:"brief_text"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &read)
			if read != brief {
				t.Errorf("stored brief = %+v, want the refreshed one %+v", read, brief)
			}

			// Probe: 422 (AC-9).
			p.refused422(p.probe(`{}`), nlp.ReasonDestinationNotApproved, map[string]string{
				"azure_openai": "azureEndpoint", "vertex": "gcpLocation", "bedrock": "awsRegion"}[tc.provider])

			p.noRequests("parse, brief refresh and probe")

			// An unrelated save still succeeds (PAC-24 AC-17, AC-9 second half).
			if w := p.patch(`{"workStart":"08:30"}`); w.Code != http.StatusOK {
				t.Errorf("unrelated save = %d %s, want 200", w.Code, w.Body.String())
			}
		})
	}
}

func TestPAC66_ParseAndBriefSayNotEnabled(t *testing.T) { // AC-13, AC-14 (nothing approved)
	p := newPAC66(t, "pac66-notenabled@example.com", nil,
		func(s *storage.Settings) { s.LLMProvider, s.AzureEndpoint = "azure_openai", "https://"+pac66Azure })
	w := p.parse()
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "not enabled on this deployment") {
		t.Errorf("parse = %d %s, want 200 with a not-enabled sentence", w.Code, w.Body.String())
	}
	w = p.brief(http.MethodPost, "pac66-event-notenabled")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "not enabled on this deployment") {
		t.Errorf("refresh = %d %s, want 200 with a not-enabled sentence", w.Code, w.Body.String())
	}
	p.noRequests("parse and brief refresh")
}

func TestPAC66_ProviderSwitchIsAcceptedAndRefusedAtSendTime(t *testing.T) { // AC-17
	p := newPAC66(t, "pac66-ac17@example.com", map[string][]string{"azure_openai": {pac66Azure}},
		func(s *storage.Settings) { s.LLMProvider, s.AzureEndpoint = "openai", "https://"+pac66Attacker })
	if w := p.patch(`{"llmProvider":"azure_openai"}`); w.Code != http.StatusOK {
		t.Fatalf("provider-only switch = %d %s, want 200", w.Code, w.Body.String())
	}
	p.refused422(p.probe(`{}`), nlp.ReasonDestinationNotApproved, "azureEndpoint")
	p.noRequests("probe")
}

func TestPAC66_ProbeChecksTheProviderItUses(t *testing.T) { // AC-18
	type stored = func(*storage.Settings)
	for _, tc := range []struct {
		name, provider string
		approved       map[string][]string
		stored         stored
		reason, prop   string
	}{
		{"a/azure", "azure_openai", map[string][]string{"azure_openai": {pac66Azure}},
			func(s *storage.Settings) { s.AzureEndpoint = "https://" + pac66Attacker }, nlp.ReasonDestinationNotApproved, "azureEndpoint"},
		{"a/vertex", "vertex", map[string][]string{"vertex": {pac66VertexEU}},
			func(s *storage.Settings) { s.GCPLocation = pac66Attacker + "/x" }, nlp.ReasonDestinationNotApproved, "gcpLocation"},
		{"a/bedrock", "bedrock", map[string][]string{"bedrock": {"bedrock-runtime.us-east-1.amazonaws.com"}},
			func(s *storage.Settings) { s.AWSRegion = "eu-west-1" }, nlp.ReasonDestinationNotApproved, "awsRegion"},
		{"b/azure", "azure_openai", map[string][]string{"vertex": {pac66Azure}},
			func(s *storage.Settings) { s.AzureEndpoint = "https://" + pac66Azure }, nlp.ReasonProviderNotEnabled, ""},
		{"b/vertex", "vertex", nil, nil, nlp.ReasonProviderNotEnabled, ""},
		{"b/bedrock", "bedrock", nil, nil, nlp.ReasonProviderNotEnabled, ""},
		{"c/azure", "azure_openai", map[string][]string{"azure_openai": {pac66Azure}, "vertex": {pac66VertexEU}},
			func(s *storage.Settings) { s.AzureEndpoint = "https://" + pac66VertexEU }, nlp.ReasonDestinationNotApproved, "azureEndpoint"},
	} {
		for _, saved := range []string{"openai", "vertex", "azure_openai"} {
			if saved == tc.provider {
				continue
			}
			t.Run(tc.name+"/saved-"+saved, func(t *testing.T) {
				p := newPAC66(t, "pac66-ac18-"+strings.ReplaceAll(tc.name, "/", "-")+"-"+saved+"@example.com", tc.approved,
					func(s *storage.Settings) {
						s.LLMProvider, s.LLMAPIKey = saved, "sk-user-key"
						if tc.stored != nil {
							tc.stored(s)
						}
					})
				p.refused422(p.probe(`{"llmProvider":"`+tc.provider+`"}`), tc.reason, tc.prop)
				p.noRequests("probe")
				if got := p.settings().LLMProvider; got != saved {
					t.Errorf("saved provider = %q after the probe, want %q", got, saved)
				}
			})
		}
	}
}

func TestPAC66_ProbeEmptyDefaultsResolveToTheDefaultRegion(t *testing.T) { // AC-16, AC-19 at the probe
	p := newPAC66(t, "pac66-ac16@example.com", map[string][]string{"vertex": {pac66VertexEU}},
		func(s *storage.Settings) { s.LLMProvider = "vertex" })
	p.refused422(p.probe(`{}`), nlp.ReasonDestinationNotApproved, "gcpLocation")

	q := newPAC66(t, "pac66-ac19@example.com", map[string][]string{"bedrock": {"bedrock-runtime.eu-west-1.amazonaws.com"}},
		func(s *storage.Settings) { s.LLMProvider = "bedrock" })
	t.Setenv("AWS_REGION", "eu-west-1")
	q.refused422(q.probe(`{}`), nlp.ReasonDestinationNotApproved, "awsRegion")
	p.noRequests("probe")
	q.noRequests("probe")
}

func TestPAC66_ProbeEmptyAzureEndpointIsNotSet(t *testing.T) { // AC-20
	for name, tc := range map[string]struct{ saved, body string }{
		"saved":   {"azure_openai", `{}`},
		"in body": {"openai", `{"llmProvider":"azure_openai"}`},
	} {
		t.Run(name, func(t *testing.T) {
			p := newPAC66(t, "pac66-ac20-"+strings.ReplaceAll(name, " ", "")+"@example.com", map[string][]string{"azure_openai": {pac66Azure}},
				func(s *storage.Settings) { s.LLMProvider, s.AzureEndpoint = tc.saved, "" })
			w := p.probe(tc.body)
			p.refused422(w, nlp.ReasonDestinationNotSet, "azureEndpoint")
			if !strings.Contains(w.Body.String(), "not set") {
				t.Errorf("message does not say the endpoint is not set: %s", w.Body.String())
			}
			p.noRequests("probe")
		})
	}
}

// §6: the user-key and keyless providers behave as before. The spec asks for a
// probe-level test of the Anthropic and Ollama branches.
func TestPAC66_UserKeyAndKeylessProbesUnchanged(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/messages":
			_, _ = w.Write([]byte(`{"content":[{"text":"hi from anthropic"}]}`))
		case "/api/chat":
			_, _ = w.Write([]byte(`{"message":{"content":"hi from ollama"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	for name, stored := range map[string]func(*storage.Settings){
		"anthropic": func(s *storage.Settings) {
			s.LLMProvider, s.LLMAPIKey, s.LLMBaseURL = "anthropic", "sk-ant", provider.URL
		},
		"ollama": func(s *storage.Settings) { s.LLMProvider, s.OllamaBaseURL = "ollama", provider.URL },
	} {
		t.Run(name, func(t *testing.T) {
			p := newPAC66(t, "pac66-userkey-"+name+"@example.com", nil, stored)
			w := p.probe(`{}`)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "hi from "+name) {
				t.Errorf("probe = %d %s, want 200 with the provider's answer", w.Code, w.Body.String())
			}
			p.noRequests("ambient transport")
		})
	}
}

// The probe body still cannot carry a destination (PAC-24), ambient or not.
func TestPAC66_ProbeBodyStillCannotCarryADestination(t *testing.T) {
	p := newPAC66(t, "pac66-body@example.com", map[string][]string{"azure_openai": {pac66Azure}}, nil)
	for _, body := range []string{
		`{"llmProvider":"azure_openai","azureEndpoint":"https://` + pac66Attacker + `"}`,
		`{"llmProvider":"vertex","gcpLocation":"x"}`,
		`{"llmProvider":"bedrock","awsRegion":"eu-west-1"}`,
	} {
		if w := p.probe(body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", body, w.Code, w.Body.String())
		}
	}
	p.noRequests("probe")
	_ = bytes.MinRead
}
