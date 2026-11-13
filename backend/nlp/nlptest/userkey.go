package nlptest

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/nlp"
)

// UserKey is the sentinel key of the user-key providers (PAC-66-B AC-B16).
const UserKey = "sk-sentinel-user-key-pac66b"

// UserDestinations installs the operator's approved destinations for openai
// and anthropic, given as the raw values of the two variables (PAC-66-B,
// gateway prefixes since NEW-llm-bring-your-own), and a fresh Recorder as the
// transport of those two clients, for the duration of t. A malformed value
// fails the test: a test Given must say exactly what it approves.
func UserDestinations(t testing.TB, openai, anthropic string) *Recorder {
	t.Helper()
	d, notices := config.DecideLLMUserDestinations(func(k string) string {
		return map[string]string{
			config.EnvLLMApprovedHostsOpenAI:    openai,
			config.EnvLLMApprovedHostsAnthropic: anthropic,
		}[k]
	})
	if len(notices) > 0 {
		t.Fatalf("malformed test configuration: %v", notices)
	}
	rec := &Recorder{}
	t.Cleanup(nlp.SetUserDestinations(d))
	t.Cleanup(nlp.SetUserTransport(rec))
	return rec
}

// ForwardTo is a transport that delivers every request to srv, keeping its
// path and query. It is the spec's public-host Given (PAC-66-B §B6): the
// destination the rule sees is the approved public host, while the answer
// comes from an in-process server. Nothing in the product approves srv.
func ForwardTo(srv *httptest.Server) http.RoundTripper {
	target, _ := url.Parse(srv.URL)
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		out := req.Clone(req.Context())
		out.URL.Scheme, out.URL.Host, out.Host = target.Scheme, target.Host, ""
		return http.DefaultTransport.RoundTrip(out)
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
