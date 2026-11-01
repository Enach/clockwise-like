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

// approveGateways is the operator's configuration for the test: openai and
// anthropic entries as the raw variable values.
func approveGateways(t *testing.T, openai, anthropic string) {
	t.Helper()
	d, notices := config.DecideLLMUserDestinations(func(k string) string {
		return map[string]string{config.EnvLLMApprovedHostsOpenAI: openai, config.EnvLLMApprovedHostsAnthropic: anthropic}[k]
	})
	if len(notices) > 0 {
		t.Fatalf("malformed test configuration: %v", notices)
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
// that is not exactly the provider's chat operation, under an approved path
// prefix, on an approved destination is refused at the moment of sending.
func TestUserOnlyTransport_RefusesAtTheMomentOfSending(t *testing.T) {
	inner := &countingTransport{}
	approveGateways(t, "https://gw.example/openai", "")
	t.Cleanup(SetUserTransport(inner))
	for _, tc := range []struct {
		provider, target, host string
		ok                     bool
	}{
		{ProviderOpenAI, "https://api.openai.com/v1/chat/completions", "", true},
		{ProviderAnthropic, "https://api.anthropic.com/v1/messages", "", true},
		{ProviderOpenAI, "https://gw.example/openai/v1/chat/completions", "", true},
		{ProviderOpenAI, "https://gw.example/v1/chat/completions", "", false},
		{ProviderOpenAI, "https://gw.example/openai/x/v1/chat/completions", "", false},
		{ProviderOpenAI, "https://gw.example/OPENAI/v1/chat/completions", "", false},
		{ProviderAnthropic, "https://gw.example/openai/v1/messages", "", false},
		{ProviderOpenAI, "https://api.openai.com/v1/models", "", false},
		{ProviderOpenAI, "https://api.openai.com/v1/chat/completions?x=1", "", false},
		{ProviderOpenAI, "http://api.openai.com/v1/chat/completions", "", false},
		{ProviderOpenAI, "https://api.anthropic.com/v1/chat/completions", "", false},
		{ProviderOpenAI, "https://api.openai.com/v1/chat/completions", "attacker.example", false},
		{ProviderAnthropic, "https://u:p@api.anthropic.com/v1/messages", "", false},
		{"ollama", "http://ollama:11434/api/chat", "", false},
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
// approved (fail closed, D-B1).
func TestNoUserConfigurationApprovesOnlyPublicHosts(t *testing.T) {
	t.Cleanup(SetUserDestinations(config.LLMUserDestinations{}))
	if err := (&OpenAIClient{APIKey: "k"}).CheckDestination(context.Background()); err != nil {
		t.Errorf("openai public host refused with no configuration: %v", err)
	}
	if err := (&AnthropicClient{APIKey: "k"}).CheckDestination(context.Background()); err != nil {
		t.Errorf("anthropic public host refused with no configuration: %v", err)
	}
	var r *DestinationRefusal
	if err := (&OpenAIClient{APIKey: "k", BaseURL: "https://gw.example"}).CheckDestination(context.Background()); !errors.As(err, &r) {
		t.Errorf("a gateway with no configuration: %v, want a refusal", err)
	}
}
