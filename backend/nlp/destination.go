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
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

// PAC-66: the deployment's own cloud identity (azure_openai, vertex, bedrock)
// is sent only to inference hosts the operator approved for that provider.
// The check is made on every send, by every path (probe, parse, meeting
// briefs), and again by the transport at the moment of sending.

// The three providers that send the deployment's own cloud identity.
const (
	ProviderAzureOpenAI = "azure_openai"
	ProviderVertex      = "vertex"
	ProviderBedrock     = "bedrock"
)

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
	MsgGCPLocationNotApproved   = "gcpLocation must be a region whose Vertex host is approved on this deployment"
	MsgGCPLocationNotEnabled    = "gcpLocation cannot be set: vertex is not enabled on this deployment"
	MsgAWSRegionInvalid         = "awsRegion must be a valid AWS region name"
)

var providerNames = map[string]string{
	ProviderAzureOpenAI: "Azure OpenAI",
	ProviderVertex:      "Vertex AI",
	ProviderBedrock:     "Bedrock",
}

// AmbientDeps are the process-wide seams of the three providers that send the
// deployment's own cloud identity. The zero value means production: the real
// network and the real credential chains.
type AmbientDeps struct {
	// Transport carries inference requests. nil means http.DefaultTransport.
	Transport http.RoundTripper
	// AzureToken and VertexToken mint the bearer token. nil means the SDK's
	// default credential chain.
	AzureToken  func(context.Context) (string, error)
	VertexToken func(context.Context) (string, error)
	// AWSCredentials signs Bedrock requests. nil means the default chain.
	AWSCredentials aws.CredentialsProvider
}

var (
	policyMu sync.RWMutex
	approved config.LLMApprovedHosts
	deps     AmbientDeps
)

// SetApprovedHosts installs the operator's approved inference hosts and
// returns a function restoring the previous set. main calls it once at
// startup; until then nothing is approved (fail closed, spec D-1).
func SetApprovedHosts(h config.LLMApprovedHosts) (restore func()) {
	policyMu.Lock()
	prev := approved
	approved = h
	policyMu.Unlock()
	return func() { SetApprovedHosts(prev) }
}

// SetAmbientDeps installs d and returns a function restoring the previous one.
func SetAmbientDeps(d AmbientDeps) (restore func()) {
	policyMu.Lock()
	prev := deps
	deps = d
	policyMu.Unlock()
	return func() { SetAmbientDeps(prev) }
}

func currentDeps() AmbientDeps {
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
// deployment's cloud credential to a destination the operator did not approve
// for Provider, so nothing was sent. Message is a sentence for the user and
// never carries a credential.
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
	return &DestinationRefusal{
		Provider: provider, Reason: ReasonProviderNotEnabled,
		Message: fmt.Sprintf("%s is not enabled on this deployment; ask your administrator to approve an endpoint for %s",
			provider, providerNames[provider]),
	}
}

func refuseNotApproved(provider, property string) *DestinationRefusal {
	verb := "does not lead to"
	if property == "azureEndpoint" {
		verb = "is not"
	}
	return &DestinationRefusal{
		Provider: provider, Reason: ReasonDestinationNotApproved, Property: property,
		Message: fmt.Sprintf("%s %s an approved %s endpoint on this deployment", property, verb, providerNames[provider]),
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

// CheckDestination reports whether c may send now. It returns a
// *DestinationRefusal when c would carry the deployment credential to a
// destination the operator did not approve, another error when the
// destination cannot be worked out for a reason that is not a refusal (a
// Bedrock configuration that does not load), and nil otherwise. Since
// PAC-66-B the clients that carry the caller's own key or none (openai,
// anthropic, ollama) are checked too (user_destination.go). It sends nothing
// and acquires no credential, so a refusal is reported even where no
// credential could be obtained.
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

// CheckDestination is the Vertex send-time check. The effective host is the
// host of the URL the server builds, as parsed, so a location that changes the
// authority is refused.
func (c *VertexClient) CheckDestination(context.Context) error {
	if !currentApproved().Enabled(ProviderVertex) {
		return refuseNotEnabled(ProviderVertex)
	}
	if !approvedRawURL(ProviderVertex, c.requestURL()) {
		return refuseNotApproved(ProviderVertex, "gcpLocation")
	}
	return nil
}

// CheckDestination is the Bedrock send-time check.
func (c *BedrockClient) CheckDestination(ctx context.Context) error {
	_, err := c.prepare(ctx)
	return err
}

// prepare loads the AWS configuration and checks the host the SDK will
// resolve for it: the explicit region, any endpoint override the operator set
// (environment, or the selected profile's endpoint_url), FIPS and dual-stack.
func (c *BedrockClient) prepare(ctx context.Context) (aws.Config, error) {
	if !currentApproved().Enabled(ProviderBedrock) {
		return aws.Config{}, refuseNotEnabled(ProviderBedrock)
	}
	if !config.ValidDNSLabel(c.Region) {
		return aws.Config{}, refuseNotApproved(ProviderBedrock, "awsRegion")
	}
	cfg, err := c.loadConfig(ctx)
	if err != nil {
		return aws.Config{}, err
	}
	o := bedrockruntime.NewFromConfig(cfg).Options()
	property := "awsRegion"
	if o.BaseEndpoint != nil && c.Profile != "" {
		property = "awsProfile"
	}
	ep, err := o.EndpointResolverV2.ResolveEndpoint(ctx, bedrockruntime.EndpointParameters{
		Region:       aws.String(o.Region),
		UseFIPS:      aws.Bool(o.EndpointOptions.UseFIPSEndpoint == aws.FIPSEndpointStateEnabled),
		UseDualStack: aws.Bool(o.EndpointOptions.UseDualStackEndpoint == aws.DualStackEndpointStateEnabled),
		Endpoint:     o.BaseEndpoint,
	})
	if err != nil || !approvedURL(ProviderBedrock, &ep.URI) {
		return aws.Config{}, refuseNotApproved(ProviderBedrock, property)
	}
	return cfg, nil
}

// errRedirect is why a redirect is never followed with the credential.
var errRedirect = errors.New("the provider answered with a redirect; the deployment credential is never sent to a redirect target")

// ambientHTTPClient is the HTTP client of the deployment-credential providers.
// It follows no redirect (spec AC-8), and its transport refuses any request
// whose destination is not approved for provider, so the rule holds at the
// moment of sending whatever built the URL.
func ambientHTTPClient(provider string) *http.Client {
	inner := currentDeps().Transport
	if inner == nil {
		inner = http.DefaultTransport
	}
	return &http.Client{
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
			Message: fmt.Sprintf("refused to send the %s credential to a host this deployment has not approved", providerNames[t.provider]),
		}
	}
	return t.inner.RoundTrip(req)
}

// AzureEndpointWriteProblem is the settings-write check for a CHANGED
// azureEndpoint: "" when acceptable, otherwise the catalogue message. "" is
// always acceptable (spec AC-15, D-3).
func AzureEndpointWriteProblem(v string) string {
	if v == "" {
		return ""
	}
	err := (&AzureOpenAIClient{Endpoint: v, Deployment: "d", APIVersion: "v"}).CheckDestination(context.Background())
	return writeProblem(err, MsgAzureEndpointNotEnabled, MsgAzureEndpointNotApproved)
}

// GCPLocationWriteProblem is the settings-write check for a CHANGED
// gcpLocation (see AzureEndpointWriteProblem).
func GCPLocationWriteProblem(v string) string {
	if v == "" {
		return ""
	}
	err := (&VertexClient{Project: "p", Location: v, ModelID: "m"}).CheckDestination(context.Background())
	return writeProblem(err, MsgGCPLocationNotEnabled, MsgGCPLocationNotApproved)
}

// AWSRegionWriteProblem is the settings-write check for a CHANGED awsRegion:
// one host label. The Bedrock host itself is checked at send time.
func AWSRegionWriteProblem(v string) string {
	if v == "" || config.ValidDNSLabel(v) {
		return ""
	}
	return MsgAWSRegionInvalid
}

func writeProblem(err error, notEnabled, notApproved string) string {
	var r *DestinationRefusal
	switch {
	case err == nil:
		return ""
	case errors.As(err, &r) && r.Reason == ReasonProviderNotEnabled:
		return notEnabled
	default:
		return notApproved
	}
}
