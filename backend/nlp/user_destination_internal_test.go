package nlp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/config"
)

// approveOllamaOrigin is the spec's Ollama Given (PAC-66-B §B6): the
// operator's configuration for the test approves origin for ollama only.
func approveOllamaOrigin(t *testing.T, origin string) {
	t.Helper()
	d, notices := config.DecideLLMUserDestinations(func(k string) string {
		if k == config.EnvLLMApprovedOriginsOllama {
			return origin
		}
		return ""
	})
	if len(notices) > 0 {
		t.Fatalf("malformed test origin: %v", notices)
	}
	t.Cleanup(SetUserDestinations(d))
}

// forwardUserRequestsTo is the spec's public-host Given (§B6): the destination
// the rule sees is the vendor's public host, and the request is delivered to
// the in-process srv instead of the network.
func forwardUserRequestsTo(t *testing.T, srv *httptest.Server) {
	t.Helper()
	target, _ := url.Parse(srv.URL)
	t.Cleanup(SetUserTransport(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		out := req.Clone(req.Context())
		out.URL.Scheme, out.URL.Host, out.Host = target.Scheme, target.Host, ""
		return http.DefaultTransport.RoundTrip(out)
	})))
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// The user-key transport is the last line: whatever built the URL, a request
// that is not exactly the provider's chat operation on an approved
// destination is refused at the moment of sending (spec §B3).
func TestUserOnlyTransport_RefusesAtTheMomentOfSending(t *testing.T) {
	inner := &countingTransport{}
	approveOllamaOrigin(t, "http://ollama:11434")
	t.Cleanup(SetUserTransport(inner))
	for _, tc := range []struct {
		provider, target, host string
		ok                     bool
	}{
		{ProviderOpenAI, "https://api.openai.com/v1/chat/completions", "", true},
		{ProviderAnthropic, "https://api.anthropic.com/v1/messages", "", true},
		{ProviderOllama, "http://ollama:11434/api/chat", "", true},
		{ProviderOpenAI, "https://api.openai.com/v1/models", "", false},
		{ProviderOpenAI, "https://api.openai.com/v1/chat/completions?x=1", "", false},
		{ProviderOpenAI, "http://api.openai.com/v1/chat/completions", "", false},
		{ProviderOpenAI, "https://api.anthropic.com/v1/chat/completions", "", false},
		{ProviderOpenAI, "https://api.openai.com/v1/chat/completions", "attacker.example", false},
		{ProviderAnthropic, "https://u:p@api.anthropic.com/v1/messages", "", false},
		{ProviderOllama, "http://ollama:11434/api/pull", "", false},
		{ProviderOllama, "http://ollama:11435/api/chat", "", false},
		{ProviderOllama, "https://api.openai.com/api/chat", "", false},
	} {
		before := inner.n
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, tc.target, strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		if tc.host != "" {
			req.Host = tc.host
		}
		resp, err := userHTTPClient(tc.provider).Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
		var r *DestinationRefusal
		if tc.ok && (err != nil || inner.n != before+1) {
			t.Errorf("%s %s: err %v, sent %d; want sent once", tc.provider, tc.target, err, inner.n-before)
		}
		if !tc.ok && (!errors.As(err, &r) || inner.n != before) {
			t.Errorf("%s %s (Host %q): err %v, sent %d; want a refusal and nothing sent", tc.provider, tc.target, tc.host, err, inner.n-before)
		}
	}
}

// Before main installs a configuration, only the vendors' public hosts are
// approved and ollama is not enabled (fail closed, D-B1).
func TestNoUserConfigurationApprovesOnlyPublicHosts(t *testing.T) {
	t.Cleanup(SetUserDestinations(config.LLMUserDestinations{}))
	if err := (&OpenAIClient{APIKey: "k"}).CheckDestination(context.Background()); err != nil {
		t.Errorf("openai public host refused with no configuration: %v", err)
	}
	if err := (&AnthropicClient{APIKey: "k"}).CheckDestination(context.Background()); err != nil {
		t.Errorf("anthropic public host refused with no configuration: %v", err)
	}
	var r *DestinationRefusal
	if err := (&OllamaClient{BaseURL: "http://localhost:11434"}).CheckDestination(context.Background()); !errors.As(err, &r) || r.Reason != ReasonProviderNotEnabled {
		t.Errorf("ollama with no configuration: %v, want provider_not_enabled", err)
	}
}
