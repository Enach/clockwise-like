// Package nlptest is test support for the LLM providers. It installs the
// shared Givens: the operator's approved destinations, a recording transport
// that records every inference request and lets none reach the network, and
// (NEW-llm-bring-your-own) ambient cloud credentials in the environment that
// must never be read for an AI request.
package nlptest

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/nlp"
)

// Ambient credential values placed in the environment by Ambient. None may
// appear in a request or a response (spec AC-2: no identity of the machine
// Paceday runs on is used).
const (
	AWSAccessKey      = "AKIASENTINELPAC66XXXX"
	AWSSecretKey      = "sentinel-aws-secret-pac66"
	AWSBearerToken    = "sentinel-aws-bearer-token-byok"
	AzureClientSecret = "sentinel-azure-client-secret-byok"
	// OpenAIEnvKey and AnthropicEnvKey are the providers' own SDK environment
	// variables, set on the server: a deployment-level key that must never be
	// sent (O-2, spec AC-42).
	OpenAIEnvKey    = "sk-sentinel-openai-env-byok"
	AnthropicEnvKey = "sk-ant-sentinel-anthropic-env-byok"
)

// Sentinels lists every ambient sentinel value, for leak checks.
var Sentinels = []string{AWSAccessKey, AWSSecretKey, AWSBearerToken, AzureClientSecret, OpenAIEnvKey, AnthropicEnvKey}

// SuccessBody answers an inference request successfully for every provider
// at once (OpenAI and Azure choices, Vertex candidates, Anthropic content,
// Bedrock Converse output).
const SuccessBody = `{"choices":[{"message":{"content":"hello"}}],` +
	`"candidates":[{"content":{"parts":[{"text":"hello"}]}}],` +
	`"content":[{"text":"hello"}],"message":{"content":"hello"},` +
	`"output":{"message":{"content":[{"text":"hello"}]}}}`

// Request is one recorded outbound request.
type Request struct {
	Method string
	Scheme string
	Host   string
	Path   string
	// EscapedPath is the path as sent on the wire.
	EscapedPath string
	// Query is the raw query (PAC-66-B AC-B18, AC-B19).
	Query  string
	Header http.Header
}

// Recorder is the recording transport. Answer, when set, chooses the response;
// otherwise every request gets a 200 with SuccessBody.
type Recorder struct {
	mu     sync.Mutex
	reqs   []Request
	Answer func(*http.Request) *http.Response
}

// RoundTrip records req and answers without touching the network.
func (r *Recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
	}
	r.mu.Lock()
	r.reqs = append(r.reqs, Request{
		Method: req.Method, Scheme: req.URL.Scheme, Host: req.URL.Host,
		Path: req.URL.Path, EscapedPath: req.URL.EscapedPath(), Query: req.URL.RawQuery, Header: req.Header.Clone(),
	})
	answer := r.Answer
	r.mu.Unlock()
	if answer != nil {
		if resp := answer(req); resp != nil {
			resp.Request = req
			return resp, nil
		}
	}
	return Response(req, http.StatusOK, SuccessBody, nil), nil
}

// Requests returns what has been recorded so far.
func (r *Recorder) Requests() []Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Request(nil), r.reqs...)
}

// Response builds a response for req.
func Response(req *http.Request, status int, body string, header http.Header) *http.Response {
	h := http.Header{"Content-Type": []string{"application/json"}}
	for k, v := range header {
		h[k] = v
	}
	return &http.Response{
		StatusCode: status, Status: http.StatusText(status),
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: h, Body: io.NopCloser(bytes.NewBufferString(body)),
		ContentLength: int64(len(body)), Request: req,
	}
}

// Ambient installs approved (provider -> hosts) for the three cloud providers
// and a fresh Recorder as their transport for the duration of t, and puts
// ambient AWS, Azure and Google credentials into the environment, as a cloud
// VM would have them. It uses t.Setenv, so t must not be parallel.
func Ambient(t testing.TB, approved map[string][]string) *Recorder {
	t.Helper()
	hosts := config.LLMApprovedHosts{}
	for provider, list := range approved {
		set := map[string]bool{}
		for _, h := range list {
			set[strings.ToLower(h)] = true
		}
		hosts[provider] = set
	}
	rec := &Recorder{}
	t.Cleanup(nlp.SetApprovedHosts(hosts))
	t.Cleanup(nlp.SetCloudDeps(nlp.CloudDeps{Transport: rec}))
	dir := t.TempDir()
	adc := filepath.Join(dir, "adc.json")
	_ = os.WriteFile(adc, []byte(`{"type":"authorized_user","client_id":"x","client_secret":"`+AzureClientSecret+`","refresh_token":"r"}`), 0o600)
	t.Setenv("AWS_ACCESS_KEY_ID", AWSAccessKey)
	t.Setenv("AWS_SECRET_ACCESS_KEY", AWSSecretKey)
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", AWSBearerToken)
	t.Setenv("AWS_REGION", "eu-west-1")
	t.Setenv("AZURE_CLIENT_ID", "sentinel-client")
	t.Setenv("AZURE_TENANT_ID", "sentinel-tenant")
	t.Setenv("AZURE_CLIENT_SECRET", AzureClientSecret)
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", adc)
	t.Setenv("OPENAI_API_KEY", OpenAIEnvKey)
	t.Setenv("ANTHROPIC_API_KEY", AnthropicEnvKey)
	return rec
}

// Leaks returns the sentinel values that appear in s.
func Leaks(s string) []string {
	var out []string
	for _, v := range Sentinels {
		if strings.Contains(s, v) {
			out = append(out, v)
		}
	}
	return out
}
