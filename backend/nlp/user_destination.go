package nlp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Enach/paceday/backend/config"
)

// PAC-66-B: a user-set base URL for openai, anthropic (which carry the user's
// own key) or ollama (which carries none) is sent to only if it names a
// destination approved for the provider in use, and only that provider's chat
// operation is called on it, with no query and no redirect followed. The
// check is made on every send by every path (probe, parse, meeting briefs),
// and again by the transport at the moment of sending.

// The user-key and keyless providers.
const (
	ProviderOpenAI    = "openai"
	ProviderAnthropic = "anthropic"
	ProviderOllama    = "ollama"
)

// Settings-write messages, as catalogued in the contract's
// SettingsValidationError. Each names the property first.
const (
	MsgLLMBaseURLNotApproved    = "llmBaseUrl must be the scheme, host and port of an OpenAI, Anthropic or Ollama destination approved on this deployment"
	MsgOllamaBaseURLNotApproved = "ollamaBaseUrl must be the scheme, host and port of an Ollama server approved on this deployment"
	MsgOllamaBaseURLNotEnabled  = "ollamaBaseUrl cannot be set: ollama is not enabled on this deployment"
)

// ollamaDefaultBaseURL is Ollama's built-in fallback when both base URLs are
// empty (spec §B4; fallback order unchanged, D-B9).
const ollamaDefaultBaseURL = "http://localhost:11434"

// chatPaths is the one operation each provider may call (spec D-B8).
var chatPaths = map[string]string{
	ProviderOpenAI:    "/v1/chat/completions",
	ProviderAnthropic: "/v1/messages",
	ProviderOllama:    "/api/chat",
}

var userProviderNames = map[string]string{
	ProviderOpenAI:    "OpenAI",
	ProviderAnthropic: "Anthropic",
	ProviderOllama:    "Ollama",
}

var (
	userDestinations config.LLMUserDestinations
	userTransport    http.RoundTripper
)

// SetUserDestinations installs the operator's approved destinations for
// openai, anthropic and ollama and returns a function restoring the previous
// set. main calls it once at startup; until then only the vendors' public
// hosts are approved and ollama is not enabled (fail closed, D-B1).
func SetUserDestinations(d config.LLMUserDestinations) (restore func()) {
	policyMu.Lock()
	prev := userDestinations
	userDestinations = d
	policyMu.Unlock()
	return func() { SetUserDestinations(prev) }
}

// SetUserTransport installs the transport that carries openai, anthropic and
// ollama requests (nil means http.DefaultTransport) and returns a function
// restoring the previous one. It is a test seam, separate from the ambient
// providers' transport.
func SetUserTransport(rt http.RoundTripper) (restore func()) {
	policyMu.Lock()
	prev := userTransport
	userTransport = rt
	policyMu.Unlock()
	return func() { SetUserTransport(prev) }
}

func currentUserDestinations() config.LLMUserDestinations {
	policyMu.RLock()
	defer policyMu.RUnlock()
	return userDestinations
}

func currentUserTransport() http.RoundTripper {
	policyMu.RLock()
	defer policyMu.RUnlock()
	return userTransport
}

// userOriginApproved is the matching rule (contract operatorConfiguration):
// openai and anthropic over https on the default port to an approved host;
// ollama to an approved origin.
func userOriginApproved(provider string, o config.Origin) bool {
	d := currentUserDestinations()
	switch provider {
	case ProviderOpenAI, ProviderAnthropic:
		return o.Scheme == "https" && (o.Port == "" || o.Port == "443") && d.HostApproved(provider, o.Host)
	case ProviderOllama:
		return (o.Scheme == "http" || o.Scheme == "https") && d.OllamaApproved(o)
	}
	return false
}

func refuseOllamaNotEnabled() *DestinationRefusal {
	return &DestinationRefusal{
		Provider: ProviderOllama, Reason: ReasonProviderNotEnabled,
		Message: "ollama is not enabled on this deployment; ask your administrator to approve an Ollama server",
	}
}

func refuseBaseURL(provider, property string) *DestinationRefusal {
	msg := fmt.Sprintf("%s is not an approved Ollama server on this deployment; use only the scheme, host and port of a server your administrator approved", property)
	switch provider {
	case ProviderOpenAI:
		msg = property + " is not an approved OpenAI destination on this deployment; use only the scheme, host and port of an approved server, or leave it empty for " + config.OpenAIPublicHost
	case ProviderAnthropic:
		msg = property + " is not an approved Anthropic destination on this deployment; use only the scheme, host and port of an approved server, or leave it empty for " + config.AnthropicPublicHost
	}
	return &DestinationRefusal{Provider: provider, Reason: ReasonDestinationNotApproved, Property: property, Message: msg}
}

// userRequestURL returns the one URL a request for provider may go to, given
// its effective base URL and the stored property that base came from, or the
// refusal. It sends nothing.
func userRequestURL(provider, base, property string) (string, *DestinationRefusal) {
	if provider == ProviderOllama && !currentUserDestinations().OllamaEnabled() {
		return "", refuseOllamaNotEnabled()
	}
	o, ok := config.ParseOrigin(base)
	if !ok || !userOriginApproved(provider, o) {
		return "", refuseBaseURL(provider, property)
	}
	if c := (config.Origin{Scheme: o.Scheme}).Canonical(); o.Port == c.Port {
		o.Port = "" // an explicit default port is sent as the plain origin
	}
	return o.URL() + chatPaths[provider], nil
}

func (c *OpenAIClient) requestURL() (string, *DestinationRefusal) {
	base := c.BaseURL
	if base == "" {
		base = "https://" + config.OpenAIPublicHost
	}
	return userRequestURL(ProviderOpenAI, base, "llmBaseUrl")
}

func (c *AnthropicClient) requestURL() (string, *DestinationRefusal) {
	base := c.BaseURL
	if base == "" {
		base = "https://" + config.AnthropicPublicHost
	}
	return userRequestURL(ProviderAnthropic, base, "llmBaseUrl")
}

func (c *OllamaClient) requestURL() (string, *DestinationRefusal) {
	base := c.BaseURL
	if base == "" {
		base = "http://host.docker.internal:11434"
	}
	property := c.baseURLProperty
	if property == "" {
		property = "ollamaBaseUrl"
	}
	return userRequestURL(ProviderOllama, base, property)
}

// CheckDestination is the OpenAI send-time check (spec AC-B1, AC-B5, AC-B6).
func (c *OpenAIClient) CheckDestination(context.Context) error {
	if _, r := c.requestURL(); r != nil {
		return r
	}
	return nil
}

// CheckDestination is the Anthropic send-time check (spec AC-B2).
func (c *AnthropicClient) CheckDestination(context.Context) error {
	if _, r := c.requestURL(); r != nil {
		return r
	}
	return nil
}

// CheckDestination is the Ollama send-time check (spec AC-B8 to AC-B10).
func (c *OllamaClient) CheckDestination(context.Context) error {
	if _, r := c.requestURL(); r != nil {
		return r
	}
	return nil
}

// errUserRedirect is why a redirect is never followed for these providers
// (spec D-B6): the request that drew it counts as failed.
var errUserRedirect = errors.New("the provider answered with a redirect; redirects are never followed for this provider")

// userHTTPClient is the HTTP client of openai, anthropic and ollama. It
// follows no redirect (AC-B7), and its transport refuses any request that is
// not exactly the provider's chat operation on a destination approved for
// that provider, so the rule holds at the moment of sending whatever built
// the URL.
func userHTTPClient(provider string) *http.Client {
	inner := currentUserTransport()
	if inner == nil {
		inner = http.DefaultTransport
	}
	return &http.Client{
		Transport:     &userOnlyTransport{provider: provider, inner: inner},
		CheckRedirect: func(*http.Request, []*http.Request) error { return errUserRedirect },
	}
}

type userOnlyTransport struct {
	provider string
	inner    http.RoundTripper
}

func (t *userOnlyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u := req.URL
	ok := u != nil && u.User == nil && u.Opaque == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" &&
		u.Path == chatPaths[t.provider] && (u.RawPath == "" || u.RawPath == u.Path) &&
		(req.Host == "" || strings.EqualFold(req.Host, u.Host))
	if ok {
		o := config.Origin{Scheme: strings.ToLower(u.Scheme), Host: strings.ToLower(u.Hostname()), Port: u.Port()}
		ok = o.Host != "" && userOriginApproved(t.provider, o)
	}
	if !ok {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, &DestinationRefusal{
			Provider: t.provider, Reason: ReasonDestinationNotApproved,
			Message: fmt.Sprintf("refused to send a %s request to a destination this deployment has not approved", userProviderNames[t.provider]),
		}
	}
	return t.inner.RoundTrip(req)
}

// LLMBaseURLWriteProblem is the settings-write check for a CHANGED
// llmBaseUrl: "" when acceptable, otherwise the catalogue message. The value
// is read by openai, anthropic and ollama, so it must be approved for at least
// one of them (D-B5); "" is always acceptable (AC-B14).
func LLMBaseURLWriteProblem(v string) string {
	if v == "" {
		return ""
	}
	if o, ok := config.ParseOrigin(v); ok {
		for _, p := range []string{ProviderOpenAI, ProviderAnthropic, ProviderOllama} {
			if userOriginApproved(p, o) {
				return ""
			}
		}
	}
	return MsgLLMBaseURLNotApproved
}

// OllamaBaseURLWriteProblem is the settings-write check for a CHANGED
// ollamaBaseUrl (see LLMBaseURLWriteProblem).
func OllamaBaseURLWriteProblem(v string) string {
	if v == "" {
		return ""
	}
	if !currentUserDestinations().OllamaEnabled() {
		return MsgOllamaBaseURLNotEnabled
	}
	if o, ok := config.ParseOrigin(v); ok && userOriginApproved(ProviderOllama, o) {
		return ""
	}
	return MsgOllamaBaseURLNotApproved
}
