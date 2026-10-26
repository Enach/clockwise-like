// Package nlptest is test support for the deployment-credential LLM providers
// (PAC-66). It installs the spec's two shared Givens: a sentinel credential
// that is always obtainable, and a recording transport that records every
// inference request and lets none reach the network.
package nlptest

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/nlp"
	"github.com/aws/aws-sdk-go-v2/aws"
)

// Sentinel credential values. AC-11 checks that none appears in a response.
const (
	AzureToken   = "sentinel-azure-token-pac66"
	VertexToken  = "sentinel-vertex-token-pac66"
	AWSAccessKey = "AKIASENTINELPAC66XXXX"
	AWSSecretKey = "sentinel-aws-secret-pac66"
)

// Sentinels lists every sentinel value, for leak checks.
var Sentinels = []string{AzureToken, VertexToken, AWSAccessKey, AWSSecretKey}

// SuccessBody answers an inference request successfully for all three
// providers at once (spec shared Given "Successful answer").
const SuccessBody = `{"choices":[{"message":{"content":"hello"}}],` +
	`"candidates":[{"content":{"parts":[{"text":"hello"}]}}],` +
	`"content":[{"text":"hello"}]}`

// Request is one recorded outbound request.
type Request struct {
	Method string
	Scheme string
	Host   string
	Path   string
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
		Path: req.URL.Path, Header: req.Header.Clone(),
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

// Ambient installs approved (provider -> hosts), the sentinel credential and a
// fresh Recorder for the duration of t, and isolates the AWS SDK from the
// machine's own configuration. It uses t.Setenv, so t must not be parallel.
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
	t.Cleanup(nlp.SetAmbientDeps(nlp.AmbientDeps{
		Transport:   rec,
		AzureToken:  func(context.Context) (string, error) { return AzureToken, nil },
		VertexToken: func(context.Context) (string, error) { return VertexToken, nil },
		AWSCredentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: AWSAccessKey, SecretAccessKey: AWSSecretKey, Source: "nlptest"}, nil
		}),
	}))
	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	for _, k := range []string{"AWS_REGION", "AWS_DEFAULT_REGION", "AWS_PROFILE", "AWS_ENDPOINT_URL",
		"AWS_ENDPOINT_URL_BEDROCK_RUNTIME", "AWS_USE_FIPS_ENDPOINT", "AWS_USE_DUALSTACK_ENDPOINT"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
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
