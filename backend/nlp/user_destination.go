package nlp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/internal/outbound"
)

// PAC-66-B, as changed by NEW-llm-bring-your-own: a user-set base URL for
// openai or anthropic is sent to only if it names a destination approved for
// the provider in use (optionally with an operator-approved path prefix,
// owner decision O-10), and only that provider's chat operation is called on
// it, with no query and no redirect followed. The check is made on every send
// by every path (probe, parse, meeting briefs), and again by the transport at
// the moment of sending. Ollama is removed (owner decision O-4).

// MsgLLMBaseURLNotApproved is the settings-write message for a changed
// llmBaseUrl approved for neither provider, as catalogued in the contract's
// SettingsValidationError.
const MsgLLMBaseURLNotApproved = "llmBaseUrl must be an OpenAI or Anthropic destination approved on this deployment"

// chatPaths is the one operation each provider may call.
var chatPaths = map[string]string{
	ProviderOpenAI:    "/v1/chat/completions",
	ProviderAnthropic: "/v1/messages",
}

var publicHosts = map[string]string{
	ProviderOpenAI:    config.OpenAIPublicHost,
	ProviderAnthropic: config.AnthropicPublicHost,
}

var (
	userDestinations config.LLMUserDestinations
	userTransport    http.RoundTripper
)

// SetUserDestinations installs the operator's approved destinations for
// openai and anthropic and returns a function restoring the previous set.
// main calls it once at startup; until then only the vendors' public hosts
// are approved.
func SetUserDestinations(d config.LLMUserDestinations) (restore func()) {
	policyMu.Lock()
	prev := userDestinations
	userDestinations = d
	policyMu.Unlock()
	return func() { SetUserDestinations(prev) }
}

// SetUserTransport installs the transport that carries openai and anthropic
// requests (nil means http.DefaultTransport) and returns a function restoring
// the previous one. It is a test seam, separate from the cloud providers'.
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

// baseURLApproved is the matching rule: https on the default port, and a host
// and path prefix approved for provider.
func baseURLApproved(provider string, b config.BaseURL) bool {
	return b.Scheme == "https" && (b.Port == "" || b.Port == "443") &&
		currentUserDestinations().Approved(provider, config.Gateway{Host: b.Host, Prefix: b.Prefix})
}

func refuseBaseURL(provider string) *DestinationRefusal {
	return &DestinationRefusal{
		Provider: provider, Reason: ReasonDestinationNotApproved, Property: "llmBaseUrl",
		Message: fmt.Sprintf("llmBaseUrl is not an approved %s destination on this deployment; clear it in Settings to use %s",
			displayNames[provider], publicHosts[provider]),
	}
}

// userRequestURL returns the one URL a request for provider may go to, given
// its stored base URL ("" means the vendor's public host), or the refusal.
// It sends nothing.
func userRequestURL(provider, base string) (string, *DestinationRefusal) {
	if base == "" {
		base = "https://" + publicHosts[provider]
	}
	b, ok := config.ParseBaseURL(base)
	if !ok || !baseURLApproved(provider, b) {
		return "", refuseBaseURL(provider)
	}
	return "https://" + b.Host + b.Prefix + chatPaths[provider], nil
}

func (c *OpenAIClient) requestURL() (string, *DestinationRefusal) {
	return userRequestURL(ProviderOpenAI, c.BaseURL)
}

func (c *AnthropicClient) requestURL() (string, *DestinationRefusal) {
	return userRequestURL(ProviderAnthropic, c.BaseURL)
}

// CheckDestination is the OpenAI send-time check.
func (c *OpenAIClient) CheckDestination(context.Context) error {
	if _, r := c.requestURL(); r != nil {
		return r
	}
	return nil
}

// CheckDestination is the Anthropic send-time check.
func (c *AnthropicClient) CheckDestination(context.Context) error {
	if _, r := c.requestURL(); r != nil {
		return r
	}
	return nil
}

// errUserRedirect is why a redirect is never followed for these providers:
// the request that drew it counts as failed.
var errUserRedirect = errors.New("the provider answered with a redirect; redirects are never followed for this provider")

// userHTTPClient is the HTTP client of openai and anthropic. It follows no
// redirect, and its transport refuses any request that is not exactly the
// provider's chat operation under an approved prefix on a destination
// approved for that provider, so the rule holds at the moment of sending
// whatever built the URL.
func userHTTPClient(provider string) *http.Client {
	inner := currentUserTransport()
	if inner == nil {
		inner = http.DefaultTransport
	}
	return &http.Client{
		Timeout:       outbound.Current().LLM, // PAC-81 AC-A3: 30 s per LLM call
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
	chat := chatPaths[t.provider]
	ok := u != nil && chat != "" && u.User == nil && u.Opaque == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" &&
		strings.HasSuffix(u.Path, chat) && (u.RawPath == "" || u.RawPath == u.Path) &&
		(req.Host == "" || strings.EqualFold(req.Host, u.Host))
	if ok {
		b := config.BaseURL{Scheme: strings.ToLower(u.Scheme), Host: strings.ToLower(u.Hostname()), Port: u.Port(),
			Prefix: strings.TrimSuffix(u.Path, chat)}
		ok = b.Host != "" && baseURLApproved(t.provider, b)
	}
	if !ok {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, &DestinationRefusal{
			Provider: t.provider, Reason: ReasonDestinationNotApproved,
			Message: fmt.Sprintf("refused to send a %s request to a destination this deployment has not approved", displayNames[t.provider]),
		}
	}
	return t.inner.RoundTrip(req)
}

// LLMBaseURLWriteProblem is the settings-write check for a CHANGED
// llmBaseUrl: "" when acceptable, otherwise the catalogue message. The value
// is read by openai and anthropic, so it must be approved for at least one of
// them; "" is always acceptable.
func LLMBaseURLWriteProblem(v string) string {
	if v == "" {
		return ""
	}
	if b, ok := config.ParseBaseURL(v); ok {
		for _, p := range []string{ProviderOpenAI, ProviderAnthropic} {
			if baseURLApproved(p, b) {
				return ""
			}
		}
	}
	return MsgLLMBaseURLNotApproved
}

// OrgBaseURLWriteProblem is patchOrganizationAI's check for a CHANGED,
// non-empty organisation baseUrl (PAC-73 Part 2, spec P2-D18): exactly an
// origin approved for provider followed by exactly that entry's path
// prefix, with no query, fragment or credentials. "" when acceptable.
func OrgBaseURLWriteProblem(provider, v string) string {
	if v == "" {
		return ""
	}
	if b, ok := config.ParseBaseURL(v); ok && (provider == ProviderOpenAI || provider == ProviderAnthropic) && baseURLApproved(provider, b) {
		return ""
	}
	return fmt.Sprintf("baseUrl must be an approved %s destination on this deployment: an approved origin and exactly its path prefix, with no query, fragment or credentials", displayNames[provider])
}

// OrgAzureEndpointShapeProblem is patchOrganizationAI's shape check for a
// non-empty organisation azureEndpoint (spec P2-D18, contract shape 400
// (8)): https://<host> with at most one trailing "/" and no port, path,
// query, fragment or credentials, because every member reads it.
func OrgAzureEndpointShapeProblem(v string) string {
	const msg = "azureEndpoint must be https://<host> with no path, query, fragment or credentials"
	if v == "" {
		return ""
	}
	u, err := url.Parse(v)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Host == "" ||
		u.Port() != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(v, "#") ||
		(u.Path != "" && u.Path != "/") || u.RawPath != "" {
		return msg
	}
	if v != "https://"+u.Host && v != "https://"+u.Host+"/" {
		return msg
	}
	return ""
}
