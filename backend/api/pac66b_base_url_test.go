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

// PAC-66-B: a user-set OpenAI or Anthropic base URL goes only to a
// destination approved for the provider in use, on every path (probe, parse,
// meeting-brief refresh), and the settings write refuses a base URL approved
// for no provider that reads it. Every test runs with a sentinel user key and
// a recording transport (nlptest.UserDestinations), so the pre-PAC-66-B code
// would have sent each refused request.
//
// NEW-llm-bring-your-own: Ollama is removed, keys are bound to providers, and
// a caller with no key is "AI not configured" (409) before any destination is
// checked (contract §6). The Ollama cases are replaced by the byok_test.go
// cases named beside each removal; none is skipped.

const (
	pac66bGateway  = "gateway.example"
	pac66bOllama   = "http://ollama.internal:11434"
	pac66bInternal = "http://169.254.169.254"
	pac66bDatabase = "http://postgres:5432"
)

func newPAC66B(t *testing.T, email, openai, anthropic string, stored func(*storage.Settings)) *pac66 {
	t.Helper()
	p := &pac66{t: t, user: createTestUser(t, email), rec: nlptest.UserDestinations(t, openai, anthropic)}
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
			p := newPAC66B(t, "pac66b-paths-"+provider+"@example.com", "", "", func(s *storage.Settings) {
				s.LLMProvider, s.LLMBaseURL = provider, pac66bInternal
				s.LLMAPIKeys.Set(provider, nlptest.UserKey)
			})
			p.refusedUser422(p.probe(`{}`), nlp.ReasonDestinationNotApproved, "llmBaseUrl")
			p.refusedUser422(p.probe(`{"llmApiKey":"sk-submitted-pac66b"}`), nlp.ReasonDestinationNotApproved, "llmBaseUrl")
			p.parseAndBriefRefuse("pac66b-event-"+provider, "not an approved")
			p.noRequests("probe, parse and brief refresh")

			// An unrelated save still succeeds; clearing is accepted and reads back "".
			if w := p.patch(`{"workStart":"08:30"}`); w.Code != http.StatusOK {
				t.Errorf("unrelated save = %d %s, want 200", w.Code, w.Body.String())
			}
			if w := p.patch(`{"llmBaseUrl":""}`); w.Code != http.StatusOK {
				t.Fatalf("clearing = %d %s, want 200", w.Code, w.Body.String())
			}
			if s := p.settings(); s.LLMBaseURL != "" {
				t.Errorf("after clearing: %q, want empty", s.LLMBaseURL)
			}
		})
	}
}

// Was TestPAC66B_RefusalDoesNotDependOnTheKey (a refused destination with no
// key was the 422). NEW-llm-bring-your-own contract §6: with no key the
// caller is "AI not configured", decided before the destination, so the
// answer is the 409 needs_key on the probe and brief refresh and the parse
// not_configured; still nothing is sent. The 422 for the same destination is
// pinned with a key on file by TestPAC66B_EveryPathRefusesAStoredUnapprovedBaseURL.
func TestPAC66B_NoKeyIsNotConfiguredBeforeTheDestination(t *testing.T) { // contract §6
	for _, provider := range []string{"openai", "anthropic"} {
		p := newPAC66B(t, "pac66b-nokey-"+provider+"@example.com", "", "", func(s *storage.Settings) {
			s.LLMProvider, s.LLMBaseURL = provider, pac66bInternal
		})
		conflict409(t, "probe", p.probe(`{}`), "needs_key", provider, "user")
		conflict409(t, "brief refresh", p.brief(http.MethodPost, "pac66b-nokey-"+provider), "needs_key", provider, "user")
		w := p.parse()
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"not_configured"`) {
			t.Errorf("parse = %d %s, want 200 with not_configured", w.Code, w.Body.String())
		}
		p.noRequests("no key")
	}
}

func TestPAC66B_SettingsWriteRefusesUnapprovedBaseURLs(t *testing.T) { // AC-B3, AC-B4, AC-B6, AC-B18, AC-B19
	for _, provider := range []string{"openai", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			p := newPAC66B(t, "pac66b-write-"+provider+"@example.com", pac66bGateway, "", func(s *storage.Settings) {
				s.LLMProvider, s.LLMBaseURL = provider, "https://"+pac66bGateway
			})
			before := p.settings()
			for body, msg := range map[string]string{
				`{"llmBaseUrl":"` + pac66bInternal + `"}`:                        nlp.MsgLLMBaseURLNotApproved,
				`{"llmBaseUrl":"` + pac66bDatabase + `"}`:                        nlp.MsgLLMBaseURLNotApproved,
				`{"llmBaseUrl":"http://` + pac66bGateway + `"}`:                  nlp.MsgLLMBaseURLNotApproved,
				`{"llmBaseUrl":"https://` + pac66bGateway + `:8443"}`:            nlp.MsgLLMBaseURLNotApproved,
				`{"llmBaseUrl":"https://api.openai.com/v1"}`:                     nlp.MsgLLMBaseURLNotApproved,
				`{"llmBaseUrl":"https://` + pac66bGateway + `/x"}`:               nlp.MsgLLMBaseURLNotApproved,
				`{"llmBaseUrl":"` + pac66bOllama + `"}`:                          nlp.MsgLLMBaseURLNotApproved,
				`{"llmBaseUrl":"` + pac66bOllama + `/api/pull?x="}`:              nlp.MsgLLMBaseURLNotApproved,
				`{"llmBaseUrl":"` + pac66bOllama + `#"}`:                         nlp.MsgLLMBaseURLNotApproved,
				`{"workStart":"10:00","llmBaseUrl":"` + pac66bInternal + `"}`:    nlp.MsgLLMBaseURLNotApproved,
				`{"llmProvider":"openai","llmBaseUrl":"` + pac66bInternal + `"}`: nlp.MsgLLMBaseURLNotApproved,
			} {
				w := p.patch(body)
				if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), msg) {
					t.Errorf("PATCH %s: %d %s, want 400 %q", body, w.Code, w.Body.String(), msg)
				}
			}
			after := p.settings()
			if after.LLMBaseURL != before.LLMBaseURL || after.WorkStart != before.WorkStart || after.LLMProvider != before.LLMProvider {
				t.Errorf("a refused write changed the stored settings: before %+v after %+v", before, after)
			}
			// Accepted: approved for at least one provider that reads it (D-B5).
			for _, body := range []string{
				`{"llmBaseUrl":"https://api.anthropic.com"}`,
				`{"llmBaseUrl":"https://` + pac66bGateway + `/"}`,
				`{"llmBaseUrl":"https://` + pac66bGateway + `"}`,
			} {
				if w := p.patch(body); w.Code != http.StatusOK {
					t.Errorf("PATCH %s: %d %s, want 200", body, w.Code, w.Body.String())
				}
			}
			p.noRequests("settings write")
		})
	}
}

// Was TestPAC66B_SettingsWriteWithOllamaNotEnabled: the ollamaBaseUrl half is
// replaced by TestBYOK_OllamaCannotBeChosen (the property is gone: 400
// unknown property); the provider-only half is kept here.
func TestPAC66B_ProviderOnlyChangeIsNotRefused(t *testing.T) { // AC-B14
	p := newPAC66B(t, "pac66b-write-provideronly@example.com", "", "", func(s *storage.Settings) {
		s.LLMProvider, s.LLMBaseURL = "ollama", pac66bInternal
	})
	if w := p.patch(`{"llmProvider":"openai"}`); w.Code != http.StatusOK {
		t.Errorf("provider-only change = %d %s, want 200", w.Code, w.Body.String())
	}
	p.noRequests("settings write")
}

// TestPAC66B_OllamaNotEnabledOnEveryPath is replaced by
// TestBYOK_EveryNotConfiguredRowGivesOneReason/row_8 (an Ollama account is
// "AI not configured: Ollama is no longer supported" on every path).

func TestPAC66B_ProbeChecksTheProviderItUses(t *testing.T) { // AC-B11, NEW-llm-bring-your-own AC-16
	// A former Ollama account whose stored key is now bound to no provider
	// and whose general base URL holds an Ollama origin (spec D-14).
	p := newPAC66B(t, "pac66b-ac11@example.com", "", "", func(s *storage.Settings) {
		s.LLMProvider, s.LLMUnboundAPIKey, s.LLMBaseURL = "ollama", nlptest.UserKey, pac66bOllama
	})
	// The unbound key is never used: naming a provider with no key bound to
	// it is "needs a key", and nothing is sent.
	conflict409(t, "probe openai", p.probe(`{"llmProvider":"openai"}`), "needs_key", "openai", "user")
	conflict409(t, "probe anthropic", p.probe(`{"llmProvider":"anthropic"}`), "needs_key", "anthropic", "user")
	conflict409(t, "probe saved", p.probe(`{}`), "provider_removed", "", "user")
	// With a key, the destination is checked for the provider named: the
	// Ollama origin is approved for no remaining provider.
	p.refusedUser422(p.probe(`{"llmProvider":"openai","llmApiKey":"sk-submitted-pac66b"}`), nlp.ReasonDestinationNotApproved, "llmBaseUrl")
	p.store(func(s *storage.Settings) { s.LLMAPIKeys.Set("anthropic", nlptest.UserKey) })
	p.refusedUser422(p.probe(`{"llmProvider":"anthropic"}`), nlp.ReasonDestinationNotApproved, "llmBaseUrl")
	p.noRequests("probe naming a user-key provider")
}

func TestPAC66B_ApprovedWriteThenProbeSendsOnce(t *testing.T) { // AC-B5
	p := newPAC66B(t, "pac66b-ac5@example.com", pac66bGateway, "", nil)
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
}

// Was TestPAC66B_WebFieldDoesNotRedirectOllama (the web's Ollama field wrote
// llmBaseUrl). NEW-llm-bring-your-own D-14 / AC-39: such a value is kept,
// can be written back unchanged and cleared, and is never sent: an OpenAI
// request with it is refused before anything is sent.
func TestPAC66B_FormerOllamaBaseURLIsKeptAndNeverSent(t *testing.T) { // D-14, AC-39 (unit half)
	p := newPAC66B(t, "pac66b-webfield@example.com", "", "", func(s *storage.Settings) {
		s.LLMProvider, s.LLMBaseURL = "ollama", pac66bOllama
	})
	if w := p.patch(`{"llmProvider":"openai","llmApiKey":"` + nlptest.UserKey + `","llmBaseUrl":"` + pac66bOllama + `"}`); w.Code != http.StatusOK {
		t.Fatalf("save OpenAI with the unchanged base URL = %d %s, want 200", w.Code, w.Body.String())
	}
	p.refusedUser422(p.probe(`{}`), nlp.ReasonDestinationNotApproved, "llmBaseUrl")
	p.noRequests("refused")
	if w := p.patch(`{"llmBaseUrl":""}`); w.Code != http.StatusOK {
		t.Fatalf("clearing = %d %s", w.Code, w.Body.String())
	}
	if w := p.probe(`{}`); w.Code != http.StatusOK {
		t.Fatalf("probe after clearing = %d %s, want 200", w.Code, w.Body.String())
	}
	if got := p.rec.Requests(); len(got) != 1 || got[0].Host != "api.openai.com" {
		t.Errorf("requests = %+v, want one to the public host", got)
	}
}

func TestPAC66B_ProbeReportsARedirectAsAProviderFailure(t *testing.T) { // AC-B7 at the probe
	p := newPAC66B(t, "pac66b-ac7@example.com", "", "", func(s *storage.Settings) {
		s.LLMProvider, s.LLMBaseURL = "anthropic", ""
		s.LLMAPIKeys.Set("anthropic", nlptest.UserKey)
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
