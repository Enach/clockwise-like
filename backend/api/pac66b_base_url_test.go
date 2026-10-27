package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/nlp"
	"github.com/Enach/paceday/backend/nlp/nlptest"
	"github.com/Enach/paceday/backend/storage"
)

// PAC-66-B: a user-set OpenAI, Anthropic or Ollama base URL goes only to a
// destination approved for the provider in use, on every path (probe, parse,
// meeting-brief refresh), and the settings write refuses a base URL approved
// for no provider that reads it. Every test runs with a sentinel user key and
// a recording transport (nlptest.UserDestinations), so the pre-PAC-66-B code
// would have sent each refused request.

const (
	pac66bGateway  = "gateway.example"
	pac66bOllama   = "http://ollama.internal:11434"
	pac66bInternal = "http://169.254.169.254"
	pac66bDatabase = "http://postgres:5432"
)

func newPAC66B(t *testing.T, email, openai, anthropic, ollama string, stored func(*storage.Settings)) *pac66 {
	t.Helper()
	p := &pac66{t: t, user: createTestUser(t, email), rec: nlptest.UserDestinations(t, openai, anthropic, ollama)}
	if stored != nil {
		p.store(stored)
	}
	return p
}

// noUserKey is AC-B16: no stored or submitted key in body, and a non-empty sentence.
func noUserKey(t *testing.T, what, body, sentence string) {
	t.Helper()
	for _, key := range []string{nlptest.UserKey, "sk-submitted-pac66b"} {
		if strings.Contains(body, key) {
			t.Errorf("%s: the body carries a key: %s", what, body)
		}
	}
	if strings.TrimSpace(sentence) == "" {
		t.Errorf("%s: empty error sentence: %s", what, body)
	}
}

func (p *pac66) refusedUser422(w *httptest.ResponseRecorder, reason, property string) {
	p.t.Helper()
	p.refused422(w, reason, property)
	var body struct{ Error string }
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	noUserKey(p.t, "probe 422", w.Body.String(), body.Error)
}

// parseAndBriefRefuse asserts AC-B12 on both non-probe paths: parse 200
// intent unknown with the sentence; brief refresh 200 failed with the
// sentence, stored the same.
func (p *pac66) parseAndBriefRefuse(eventID, sentence string) {
	p.t.Helper()
	w := p.parse()
	var parsed struct{ Intent, Error string }
	_ = json.Unmarshal(w.Body.Bytes(), &parsed)
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") ||
		parsed.Intent != "unknown" || !strings.Contains(parsed.Error, sentence) {
		p.t.Errorf("parse = %d %s, want 200 intent unknown with %q", w.Code, w.Body.String(), sentence)
	}
	noUserKey(p.t, "parse", w.Body.String(), parsed.Error)

	w = p.brief(http.MethodPost, eventID)
	var brief struct {
		Status    string `json:"status"`
		BriefText string `json:"brief_text"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &brief)
	if w.Code != http.StatusOK || brief.Status != "failed" || !strings.Contains(brief.BriefText, sentence) {
		p.t.Errorf("refresh = %d %s, want 200 failed with %q", w.Code, w.Body.String(), sentence)
	}
	noUserKey(p.t, "brief refresh", w.Body.String(), brief.BriefText)
	w = p.brief(http.MethodGet, eventID)
	var read struct {
		Status    string `json:"status"`
		BriefText string `json:"brief_text"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &read)
	if read != brief {
		p.t.Errorf("stored brief = %+v, want the refreshed one %+v", read, brief)
	}
}

func TestPAC66B_EveryPathRefusesAStoredUnapprovedBaseURL(t *testing.T) { // AC-B1, AC-B2, AC-B12, AC-B14, AC-B16
	for _, provider := range []string{"openai", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			p := newPAC66B(t, "pac66b-paths-"+provider+"@example.com", "", "", "", func(s *storage.Settings) {
				s.LLMProvider, s.LLMAPIKey, s.LLMBaseURL = provider, nlptest.UserKey, pac66bInternal
			})
			p.refusedUser422(p.probe(`{}`), nlp.ReasonDestinationNotApproved, "llmBaseUrl")
			p.refusedUser422(p.probe(`{"llmApiKey":"sk-submitted-pac66b"}`), nlp.ReasonDestinationNotApproved, "llmBaseUrl")
			p.parseAndBriefRefuse("pac66b-event-"+provider, "not an approved")
			p.noRequests("probe, parse and brief refresh")

			// An unrelated save still succeeds; clearing is accepted and reads back "".
			if w := p.patch(`{"workStart":"08:30"}`); w.Code != http.StatusOK {
				t.Errorf("unrelated save = %d %s, want 200", w.Code, w.Body.String())
			}
			if w := p.patch(`{"llmBaseUrl":"","ollamaBaseUrl":""}`); w.Code != http.StatusOK {
				t.Fatalf("clearing = %d %s, want 200", w.Code, w.Body.String())
			}
			if s := p.settings(); s.LLMBaseURL != "" || s.OllamaBaseURL != "" {
				t.Errorf("after clearing: %q %q, want both empty", s.LLMBaseURL, s.OllamaBaseURL)
			}
		})
	}
}

func TestPAC66B_RefusalDoesNotDependOnTheKey(t *testing.T) { // §B4, contract 500 description
	for _, provider := range []string{"openai", "anthropic"} {
		p := newPAC66B(t, "pac66b-nokey-"+provider+"@example.com", "", "", "", func(s *storage.Settings) {
			s.LLMProvider, s.LLMAPIKey, s.LLMBaseURL = provider, "", pac66bInternal
		})
		p.refusedUser422(p.probe(`{}`), nlp.ReasonDestinationNotApproved, "llmBaseUrl")
		p.parseAndBriefRefuse("pac66b-nokey-"+provider, "not an approved")
		p.noRequests("probe with no key")
	}
}

func TestPAC66B_SettingsWriteRefusesUnapprovedBaseURLs(t *testing.T) { // AC-B3, AC-B4, AC-B6, AC-B18, AC-B19
	for _, provider := range []string{"openai", "anthropic", "ollama"} {
		t.Run(provider, func(t *testing.T) {
			p := newPAC66B(t, "pac66b-write-"+provider+"@example.com", pac66bGateway, "", pac66bOllama, func(s *storage.Settings) {
				s.LLMProvider, s.LLMBaseURL, s.OllamaBaseURL = provider, "https://"+pac66bGateway, pac66bOllama
			})
			before := p.settings()
			for body, msg := range map[string]string{
				`{"llmBaseUrl":"` + pac66bInternal + `"}`:                        nlp.MsgLLMBaseURLNotApproved,
				`{"llmBaseUrl":"` + pac66bDatabase + `"}`:                        nlp.MsgLLMBaseURLNotApproved,
				`{"llmBaseUrl":"http://` + pac66bGateway + `"}`:                  nlp.MsgLLMBaseURLNotApproved,
				`{"llmBaseUrl":"https://` + pac66bGateway + `:8443"}`:            nlp.MsgLLMBaseURLNotApproved,
				`{"llmBaseUrl":"https://api.openai.com/v1"}`:                     nlp.MsgLLMBaseURLNotApproved,
				`{"llmBaseUrl":"` + pac66bOllama + `/api/pull?x="}`:              nlp.MsgLLMBaseURLNotApproved,
				`{"llmBaseUrl":"` + pac66bOllama + `#"}`:                         nlp.MsgLLMBaseURLNotApproved,
				`{"ollamaBaseUrl":"` + pac66bOllama + `/x/../api/pull"}`:         nlp.MsgOllamaBaseURLNotApproved,
				`{"ollamaBaseUrl":"https://` + pac66bGateway + `"}`:              nlp.MsgOllamaBaseURLNotApproved,
				`{"ollamaBaseUrl":"http://localhost:11434"}`:                     nlp.MsgOllamaBaseURLNotApproved,
				`{"workStart":"10:00","llmBaseUrl":"` + pac66bInternal + `"}`:    nlp.MsgLLMBaseURLNotApproved,
				`{"llmProvider":"openai","llmBaseUrl":"` + pac66bInternal + `"}`: nlp.MsgLLMBaseURLNotApproved,
			} {
				w := p.patch(body)
				if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), msg) {
					t.Errorf("PATCH %s: %d %s, want 400 %q", body, w.Code, w.Body.String(), msg)
				}
			}
			after := p.settings()
			if after.LLMBaseURL != before.LLMBaseURL || after.OllamaBaseURL != before.OllamaBaseURL ||
				after.WorkStart != before.WorkStart || after.LLMProvider != before.LLMProvider {
				t.Errorf("a refused write changed the stored settings: before %+v after %+v", before, after)
			}
			// Accepted: approved for at least one provider that reads it (D-B5).
			for _, body := range []string{
				`{"llmBaseUrl":"https://api.anthropic.com"}`,
				`{"llmBaseUrl":"` + pac66bOllama + `"}`,
				`{"llmBaseUrl":"https://` + pac66bGateway + `/"}`,
				`{"ollamaBaseUrl":"` + pac66bOllama + `/"}`,
			} {
				if w := p.patch(body); w.Code != http.StatusOK {
					t.Errorf("PATCH %s: %d %s, want 200", body, w.Code, w.Body.String())
				}
			}
			p.noRequests("settings write")
		})
	}
}

func TestPAC66B_SettingsWriteWithOllamaNotEnabled(t *testing.T) { // AC-B4 (not enabled), AC-B14
	p := newPAC66B(t, "pac66b-write-notenabled@example.com", "", "", "", func(s *storage.Settings) {
		s.LLMProvider, s.LLMBaseURL, s.OllamaBaseURL = "ollama", pac66bInternal, pac66bInternal
	})
	if w := p.patch(`{"ollamaBaseUrl":"` + pac66bOllama + `"}`); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), nlp.MsgOllamaBaseURLNotEnabled) {
		t.Errorf("PATCH ollamaBaseUrl: %d %s, want 400 %q", w.Code, w.Body.String(), nlp.MsgOllamaBaseURLNotEnabled)
	}
	// A provider-only change is not refused, whatever is stored (AC-B14).
	if w := p.patch(`{"llmProvider":"openai"}`); w.Code != http.StatusOK {
		t.Errorf("provider-only change = %d %s, want 200", w.Code, w.Body.String())
	}
	p.noRequests("settings write")
}

func TestPAC66B_OllamaNotEnabledOnEveryPath(t *testing.T) { // AC-B8, AC-B12, §B7 default accounts
	// No stored row change: the account's starting state (provider ollama,
	// ollamaBaseUrl http://localhost:11434).
	p := newPAC66B(t, "pac66b-default@example.com", "", "", "", nil)
	if s := p.settings(); s.LLMProvider != "ollama" || s.OllamaBaseURL != "http://localhost:11434" || s.LLMBaseURL != "" {
		t.Fatalf("starting state = %q %q %q, want ollama, http://localhost:11434, \"\" (spec §B2.1)", s.LLMProvider, s.OllamaBaseURL, s.LLMBaseURL)
	}
	w := p.probe(`{}`)
	p.refusedUser422(w, nlp.ReasonProviderNotEnabled, "")
	if !strings.Contains(w.Body.String(), "not enabled on this deployment") {
		t.Errorf("message does not say ollama is not enabled: %s", w.Body.String())
	}
	p.parseAndBriefRefuse("pac66b-event-default", "not enabled on this deployment")
	p.noRequests("probe, parse and brief refresh")
}

func TestPAC66B_ProbeChecksTheProviderItUses(t *testing.T) { // AC-B11
	p := newPAC66B(t, "pac66b-ac11@example.com", "", "", pac66bOllama, func(s *storage.Settings) {
		s.LLMProvider, s.LLMAPIKey, s.LLMBaseURL, s.OllamaBaseURL = "ollama", nlptest.UserKey, pac66bOllama, ""
	})
	for _, body := range []string{
		`{"llmProvider":"openai"}`,
		`{"llmProvider":"anthropic"}`,
		`{"llmProvider":"openai","llmApiKey":"sk-submitted-pac66b"}`,
	} {
		p.refusedUser422(p.probe(body), nlp.ReasonDestinationNotApproved, "llmBaseUrl")
	}
	p.noRequests("probe naming a user-key provider")

	// The stored provider itself reaches the ollama origin, with no key.
	w := p.probe(`{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("ollama probe = %d %s, want 200", w.Code, w.Body.String())
	}
	got := p.rec.Requests()
	if len(got) != 1 || got[0].Host != "ollama.internal:11434" || got[0].Path != "/api/chat" || got[0].Query != "" {
		t.Fatalf("requests = %+v, want exactly one POST to the ollama chat operation", got)
	}
	if got[0].Header.Get("Authorization") != "" || got[0].Header.Get("x-api-key") != "" {
		t.Errorf("the ollama request carries a credential: %v", got[0].Header)
	}
}

func TestPAC66B_ApprovedWriteThenProbeSendsOnce(t *testing.T) { // AC-B5, AC-B9
	p := newPAC66B(t, "pac66b-ac5@example.com", pac66bGateway, "", pac66bOllama, nil)
	if w := p.patch(`{"llmProvider":"openai","llmApiKey":"` + nlptest.UserKey + `","llmBaseUrl":"https://` + pac66bGateway + `"}`); w.Code != http.StatusOK {
		t.Fatalf("PATCH approved gateway: %d %s", w.Code, w.Body.String())
	}
	w := p.probe(`{}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("probe = %d %s, want 200 ok", w.Code, w.Body.String())
	}
	got := p.rec.Requests()
	if len(got) != 1 || got[0].Host != pac66bGateway || got[0].Scheme != "https" || got[0].Path != "/v1/chat/completions" ||
		got[0].Header.Get("Authorization") != "Bearer "+nlptest.UserKey {
		t.Fatalf("requests = %+v, want exactly one https request to the gateway with the key", got)
	}
	noUserKey(t, "probe 200", w.Body.String(), "ok")

	p2 := newPAC66B(t, "pac66b-ac9@example.com", "", "", pac66bOllama, nil)
	if w := p2.patch(`{"llmProvider":"ollama","ollamaBaseUrl":"` + pac66bOllama + `"}`); w.Code != http.StatusOK {
		t.Fatalf("PATCH approved ollama origin: %d %s", w.Code, w.Body.String())
	}
	if w := p2.probe(`{}`); w.Code != http.StatusOK {
		t.Fatalf("ollama probe = %d %s, want 200", w.Code, w.Body.String())
	}
	if got := p2.rec.Requests(); len(got) != 1 || got[0].Scheme != "http" || got[0].Host != "ollama.internal:11434" {
		t.Fatalf("requests = %+v, want exactly one request to the approved ollama origin", got)
	}
}

func TestPAC66B_WebFieldDoesNotRedirectOllama(t *testing.T) { // AC-B9 (Revision B2), D-B9
	p := newPAC66B(t, "pac66b-webfield@example.com", "", "", pac66bOllama, nil)
	if w := p.patch(`{"llmBaseUrl":"` + pac66bOllama + `"}`); w.Code != http.StatusOK {
		t.Fatalf("saving the approved origin as llmBaseUrl = %d %s, want 200", w.Code, w.Body.String())
	}
	p.refusedUser422(p.probe(`{}`), nlp.ReasonDestinationNotApproved, "ollamaBaseUrl")
	p.noRequests("probe decided on the stored default")
}

func TestPAC66B_ProbeReportsARedirectAsAProviderFailure(t *testing.T) { // AC-B7 at the probe
	p := newPAC66B(t, "pac66b-ac7@example.com", "", "", "", func(s *storage.Settings) {
		s.LLMProvider, s.LLMAPIKey, s.LLMBaseURL = "anthropic", nlptest.UserKey, ""
	})
	p.rec.Answer = func(r *http.Request) *http.Response {
		return nlptest.Response(r, http.StatusTemporaryRedirect, "", http.Header{"Location": {"https://attacker.example/steal"}})
	}
	w := p.probe(`{}`)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("probe = %d %s, want 502", w.Code, w.Body.String())
	}
	got := p.rec.Requests()
	if len(got) != 1 || got[0].Host != "api.anthropic.com" {
		t.Errorf("requests = %+v, want only the original request to the public host", got)
	}
	noUserKey(t, "probe 502", w.Body.String(), w.Body.String())
}
