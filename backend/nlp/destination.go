package nlp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/internal/outbound"
)

// PAC-66, as changed by NEW-llm-bring-your-own: the user's key for Azure
// OpenAI, Bedrock and Vertex is sent only to inference hosts the operator
// approved for that provider. The check is made on every send, by every path
// (probe, parse, meeting briefs), and again by the transport at the moment of
// sending. No identity of the machine Paceday runs on is used any more.

// Refusal reasons, as the contract's LLMDestinationRefusal.reason enum.
const (
	ReasonProviderNotEnabled     = "provider_not_enabled"
	ReasonDestinationNotApproved = "destination_not_approved"
	ReasonDestinationNotSet      = "destination_not_set"
)

// Settings-write messages, as catalogued in the contract's
// SettingsValidationError. Each names the property first.
const (
	MsgAzureEndpointNotApproved = "azureEndpoint must be an https URL whose host is approved for azure_openai on this deployment"
	MsgAzureEndpointNotEnabled  = "azureEndpoint cannot be set: azure_openai is not enabled on this deployment"
	MsgAWSRegionInvalid         = "awsRegion must be a valid AWS region name"
)

// CloudDeps are the process-wide seams of the three cloud providers. The zero
// value means production: the real network.
type CloudDeps struct {
	// Transport carries inference requests. nil means http.DefaultTransport.
	Transport http.RoundTripper
}

var (
	policyMu sync.RWMutex
	approved config.LLMApprovedHosts
	deps     CloudDeps
)

// SetApprovedHosts installs the operator's approved inference hosts and
// returns a function restoring the previous set. main calls it once at
// startup; until then nothing is approved (fail closed).
func SetApprovedHosts(h config.LLMApprovedHosts) (restore func()) {
	policyMu.Lock()
	prev := approved
	approved = h
	policyMu.Unlock()
	return func() { SetApprovedHosts(prev) }
}

// SetCloudDeps installs d and returns a function restoring the previous one.
func SetCloudDeps(d CloudDeps) (restore func()) {
	policyMu.Lock()
	prev := deps
	deps = d
	policyMu.Unlock()
	return func() { SetCloudDeps(prev) }
}

func currentDeps() CloudDeps {
	policyMu.RLock()
	defer policyMu.RUnlock()
	return deps
}

func currentApproved() config.LLMApprovedHosts {
	policyMu.RLock()
	defer policyMu.RUnlock()
	return approved
}

// DestinationRefusal is a send-time refusal: the request would carry the
// user's key to a destination not approved for Provider, so nothing was sent.
// Message is a sentence for the user and never carries a key.
type DestinationRefusal struct {
	Provider string
	Reason   string
	// Property is the stored setting to correct; empty for
	// ReasonProviderNotEnabled.
	Property string
	Message  string
}

func (r *DestinationRefusal) Error() string { return r.Message }

func refuseNotEnabled(provider string) *DestinationRefusal {
	what := "an endpoint for " + displayNames[provider]
	if provider == ProviderVertex {
		what = vertexExpressHost
	}
	return &DestinationRefusal{
		Provider: provider, Reason: ReasonProviderNotEnabled,
		Message: fmt.Sprintf("%s is not enabled on this deployment; ask your administrator to approve %s", provider, what),
	}
}

func refuseNotApproved(provider, property string) *DestinationRefusal {
	verb := "does not lead to"
	if property == "azureEndpoint" {
		verb = "is not"
	}
	return &DestinationRefusal{
		Provider: provider, Reason: ReasonDestinationNotApproved, Property: property,
		Message: fmt.Sprintf("%s %s an approved %s endpoint on this deployment", property, verb, displayNames[provider]),
	}
}

func refuseAzureNotSet() *DestinationRefusal {
	return &DestinationRefusal{
		Provider: ProviderAzureOpenAI, Reason: ReasonDestinationNotSet, Property: "azureEndpoint",
		Message: "the Azure endpoint is not set; save an approved endpoint in Settings first",
	}
}

// approvedURL is the matching rule (contracts/features/PAC-66.md §4): scheme
// https, port absent or 443, no userinfo, and a host equal to an entry for
// that provider.
func approvedURL(provider string, u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" {
		return false
	}
	if p := u.Port(); p != "" && p != "443" {
		return false
	}
	return currentApproved().Approved(provider, u.Hostname())
}

func approvedRawURL(provider, raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && approvedURL(provider, u)
}

// CheckDestination reports whether c may send now: a *DestinationRefusal when
// c would carry the key to a destination the operator did not approve, nil
// otherwise. It sends nothing.
func CheckDestination(ctx context.Context, c LLMClient) error {
	if dc, ok := c.(interface{ CheckDestination(context.Context) error }); ok {
		return dc.CheckDestination(ctx)
	}
	return nil
}

// CheckDestination is the Azure OpenAI send-time check.
func (c *AzureOpenAIClient) CheckDestination(context.Context) error {
	if !currentApproved().Enabled(ProviderAzureOpenAI) {
		return refuseNotEnabled(ProviderAzureOpenAI)
	}
	if c.Endpoint == "" {
		return refuseAzureNotSet()
	}
	if !approvedRawURL(ProviderAzureOpenAI, c.requestURL()) {
		return refuseNotApproved(ProviderAzureOpenAI, "azureEndpoint")
	}
	return nil
}

// CheckDestination is the Vertex send-time check. Express mode has exactly
// one destination, so the provider is enabled exactly when that host is
// approved.
func (c *VertexClient) CheckDestination(context.Context) error {
	if !currentApproved().Approved(ProviderVertex, vertexExpressHost) {
		return refuseNotEnabled(ProviderVertex)
	}
	return nil
}

// CheckDestination is the Bedrock send-time check: the region must be one
// host label and its runtime host must be approved. No AWS SDK endpoint
// resolution is involved, so nothing from the machine's AWS configuration can
// move the destination.
func (c *BedrockClient) CheckDestination(context.Context) error {
	if !currentApproved().Enabled(ProviderBedrock) {
		return refuseNotEnabled(ProviderBedrock)
	}
	if !config.ValidDNSLabel(c.Region) || !approvedRawURL(ProviderBedrock, "https://"+c.host()+"/") {
		return refuseNotApproved(ProviderBedrock, "awsRegion")
	}
	return nil
}

// errRedirect is why a redirect is never followed with the key.
var errRedirect = errors.New("the provider answered with a redirect; the key is never sent to a redirect target")

// cloudHTTPClient is the HTTP client of the three cloud providers. It follows
// no redirect, and its transport refuses any request whose destination is not
// approved for provider, so the rule holds at the moment of sending whatever
// built the URL.
func cloudHTTPClient(provider string) *http.Client {
	inner := currentDeps().Transport
	if inner == nil {
		inner = http.DefaultTransport
	}
	return &http.Client{
		Timeout:       outbound.Current().LLM, // PAC-81 AC-A3: 30 s per LLM call
		Transport:     &approvedOnlyTransport{provider: provider, inner: inner},
		CheckRedirect: func(*http.Request, []*http.Request) error { return errRedirect },
	}
}

type approvedOnlyTransport struct {
	provider string
	inner    http.RoundTripper
}

func (t *approvedOnlyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !approvedURL(t.provider, req.URL) || (req.Host != "" && !strings.EqualFold(req.Host, req.URL.Host)) {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, &DestinationRefusal{
			Provider: t.provider, Reason: ReasonDestinationNotApproved,
			Message: fmt.Sprintf("refused to send the %s key to a host this deployment has not approved", displayNames[t.provider]),
		}
	}
	return t.inner.RoundTrip(req)
}

// AzureEndpointWriteProblem is the settings-write check for a CHANGED
// azureEndpoint: "" when acceptable, otherwise the catalogue message. "" is
// always acceptable.
func AzureEndpointWriteProblem(v string) string {
	if v == "" {
		return ""
	}
	err := (&AzureOpenAIClient{Endpoint: v, Deployment: "d", APIVersion: "v"}).CheckDestination(context.Background())
	var r *DestinationRefusal
	switch {
	case err == nil:
		return ""
	case errors.As(err, &r) && r.Reason == ReasonProviderNotEnabled:
		return MsgAzureEndpointNotEnabled
	default:
		return MsgAzureEndpointNotApproved
	}
}

// AWSRegionWriteProblem is the settings-write check for a CHANGED awsRegion:
// one host label. The Bedrock host itself is checked at send time.
func AWSRegionWriteProblem(v string) string {
	if v == "" || config.ValidDNSLabel(v) {
		return ""
	}
	return MsgAWSRegionInvalid
}
