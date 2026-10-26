package nlp_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/nlp"
	"github.com/Enach/paceday/backend/nlp/nlptest"
	"github.com/Enach/paceday/backend/storage"
)

// PAC-66: the deployment's cloud identity goes only to hosts the operator
// approved. Every test here runs with the spec's sentinel credential, so the
// pre-PAC-66 code would have sent each refused request.

const (
	azureHost    = "my-resource.openai.azure.com"
	vertexEU     = "europe-west4-aiplatform.googleapis.com"
	vertexUS     = "us-central1-aiplatform.googleapis.com"
	bedrockUS    = "bedrock-runtime.us-east-1.amazonaws.com"
	bedrockEU    = "bedrock-runtime.eu-west-1.amazonaws.com"
	attackerHost = "attacker.example"
)

func client(t *testing.T, s storage.Settings) nlp.LLMClient {
	t.Helper()
	c, err := nlp.NewLLMClientFromSettings(&s)
	if err != nil {
		t.Fatalf("construct %s: %v", s.LLMProvider, err)
	}
	return c
}

// refusal asserts that both the pre-check and Complete refuse with reason and
// property, and that nothing was recorded.
func refusal(t *testing.T, rec *nlptest.Recorder, c nlp.LLMClient, reason, property string) {
	t.Helper()
	for name, err := range map[string]error{
		"CheckDestination": nlp.CheckDestination(context.Background(), c),
		"Complete":         completeErr(c),
	} {
		var r *nlp.DestinationRefusal
		if !errors.As(err, &r) {
			t.Fatalf("%s: err = %v, want a DestinationRefusal", name, err)
		}
		if r.Reason != reason || r.Property != property {
			t.Errorf("%s: reason %q property %q, want %q %q", name, r.Reason, r.Property, reason, property)
		}
		if strings.TrimSpace(r.Error()) == "" || len(nlptest.Leaks(r.Error())) > 0 {
			t.Errorf("%s: message %q is empty or carries a credential (AC-11)", name, r.Error())
		}
	}
	if got := rec.Requests(); len(got) != 0 {
		t.Errorf("recorded %d requests, want zero: %+v", len(got), got)
	}
}

func completeErr(c nlp.LLMClient) error {
	_, err := c.Complete(context.Background(), "sys", "user")
	return err
}

// sentOnce asserts Complete succeeds with exactly one https request to host.
func sentOnce(t *testing.T, rec *nlptest.Recorder, c nlp.LLMClient, host string) nlptest.Request {
	t.Helper()
	if err := nlp.CheckDestination(context.Background(), c); err != nil {
		t.Fatalf("CheckDestination: %v", err)
	}
	out, err := c.Complete(context.Background(), "sys", "user")
	if err != nil || out != "hello" {
		t.Fatalf("Complete = %q, %v", out, err)
	}
	got := rec.Requests()
	if len(got) != 1 || !strings.EqualFold(got[0].Host, host) || got[0].Scheme != "https" {
		t.Fatalf("requests = %+v, want exactly one https request to %s", got, host)
	}
	return got[0]
}

func TestAzure_UnapprovedEndpointSendsNothing(t *testing.T) { // AC-1, AC-9
	rec := nlptest.Ambient(t, map[string][]string{"azure_openai": {azureHost}})
	refusal(t, rec, client(t, storage.Settings{LLMProvider: "azure_openai", AzureEndpoint: "https://" + attackerHost}),
		nlp.ReasonDestinationNotApproved, "azureEndpoint")
}

func TestAzure_ApprovedEndpointSendsOnceWithTheCredential(t *testing.T) { // AC-6
	rec := nlptest.Ambient(t, map[string][]string{"azure_openai": {azureHost}})
	req := sentOnce(t, rec, client(t, storage.Settings{LLMProvider: "azure_openai",
		AzureEndpoint: "https://My-Resource.openai.azure.com/", AzureDeployment: "gpt"}), azureHost)
	if req.Header.Get("Authorization") != "Bearer "+nlptest.AzureToken {
		t.Errorf("Authorization = %q, want the sentinel bearer token", req.Header.Get("Authorization"))
	}
}

func TestAzure_InsecureOrDisguisedEndpointsSendNothing(t *testing.T) { // AC-7, D-9
	for _, ep := range []string{
		"http://" + azureHost,
		"https://" + azureHost + ":8443",
		"https://evil@" + azureHost,
		"https://evil.example%2F@" + azureHost,
		"https://" + azureHost + ".",
		"https://x." + azureHost,
		"https://" + attackerHost + "/" + azureHost,
		"https://" + attackerHost + "#" + azureHost,
		azureHost,
		"//" + azureHost,
		"https:///x",
		"  ",
	} {
		t.Run(ep, func(t *testing.T) {
			rec := nlptest.Ambient(t, map[string][]string{"azure_openai": {azureHost}})
			refusal(t, rec, client(t, storage.Settings{LLMProvider: "azure_openai", AzureEndpoint: ep}),
				nlp.ReasonDestinationNotApproved, "azureEndpoint")
		})
	}
}

func TestAzure_EmptyEndpointIsNotSet(t *testing.T) { // AC-20
	rec := nlptest.Ambient(t, map[string][]string{"azure_openai": {azureHost}})
	c := client(t, storage.Settings{LLMProvider: "azure_openai"})
	refusal(t, rec, c, nlp.ReasonDestinationNotSet, "azureEndpoint")
	if err := nlp.CheckDestination(context.Background(), c); !strings.Contains(err.Error(), "not set") {
		t.Errorf("message %q does not say the endpoint is not set", err)
	}
}

func TestAmbient_NothingApprovedMeansNotEnabled(t *testing.T) { // AC-4, D-1
	for _, s := range []storage.Settings{
		{LLMProvider: "azure_openai", AzureEndpoint: "https://" + azureHost},
		{LLMProvider: "azure_openai"},
		{LLMProvider: "vertex", GCPLocation: "europe-west4"},
		{LLMProvider: "vertex"},
		{LLMProvider: "bedrock", AWSRegion: "eu-west-1"},
		{LLMProvider: "bedrock"},
	} {
		t.Run(s.LLMProvider+"/"+s.AzureEndpoint+s.GCPLocation+s.AWSRegion, func(t *testing.T) {
			// A host approved for ANOTHER provider never enables this one (D-8).
			others := map[string][]string{"azure_openai": {vertexEU, bedrockEU}, "vertex": {azureHost}, "bedrock": {azureHost, vertexEU}}
			delete(others, s.LLMProvider)
			rec := nlptest.Ambient(t, others)
			c := client(t, s)
			refusal(t, rec, c, nlp.ReasonProviderNotEnabled, "")
			if err := nlp.CheckDestination(context.Background(), c); !strings.Contains(err.Error(), "not enabled on this deployment") {
				t.Errorf("message %q does not say the provider is not enabled", err)
			}
		})
	}
}

func TestAmbient_HostApprovedOnlyForAnotherProvider(t *testing.T) { // AC-18 (c), D-8
	rec := nlptest.Ambient(t, map[string][]string{"azure_openai": {azureHost}, "vertex": {vertexEU, attackerHost}})
	refusal(t, rec, client(t, storage.Settings{LLMProvider: "azure_openai", AzureEndpoint: "https://" + vertexEU}),
		nlp.ReasonDestinationNotApproved, "azureEndpoint")
	refusal(t, rec, client(t, storage.Settings{LLMProvider: "azure_openai", AzureEndpoint: "https://" + attackerHost}),
		nlp.ReasonDestinationNotApproved, "azureEndpoint")
}

func TestVertex_LocationThatMovesTheAuthoritySendsNothing(t *testing.T) { // AC-2
	for _, loc := range []string{"attacker.example/x", "attacker.example#", "attacker.example:443/", "x@attacker.example/", "europe-west4.attacker.example/"} {
		t.Run(loc, func(t *testing.T) {
			rec := nlptest.Ambient(t, map[string][]string{"vertex": {vertexEU}})
			refusal(t, rec, client(t, storage.Settings{LLMProvider: "vertex", GCPLocation: loc}),
				nlp.ReasonDestinationNotApproved, "gcpLocation")
		})
	}
}

func TestVertex_ApprovedRegionSendsOnce(t *testing.T) { // AC-6
	rec := nlptest.Ambient(t, map[string][]string{"vertex": {vertexEU}})
	req := sentOnce(t, rec, client(t, storage.Settings{LLMProvider: "vertex", GCPLocation: "europe-west4", GCPProject: "p"}), vertexEU)
	if req.Header.Get("Authorization") != "Bearer "+nlptest.VertexToken {
		t.Errorf("Authorization = %q, want the sentinel bearer token", req.Header.Get("Authorization"))
	}
}

func TestVertex_EmptyLocationMeansTheDefaultRegion(t *testing.T) { // AC-16
	rec := nlptest.Ambient(t, map[string][]string{"vertex": {vertexEU}})
	refusal(t, rec, client(t, storage.Settings{LLMProvider: "vertex"}), nlp.ReasonDestinationNotApproved, "gcpLocation")

	rec = nlptest.Ambient(t, map[string][]string{"vertex": {vertexUS}})
	sentOnce(t, rec, client(t, storage.Settings{LLMProvider: "vertex"}), vertexUS)
}

func TestBedrock_EmptyRegionMeansUSEast1NotTheDeploymentRegion(t *testing.T) { // AC-19, D-7
	rec := nlptest.Ambient(t, map[string][]string{"bedrock": {bedrockEU}})
	t.Setenv("AWS_REGION", "eu-west-1")
	refusal(t, rec, client(t, storage.Settings{LLMProvider: "bedrock"}), nlp.ReasonDestinationNotApproved, "awsRegion")

	rec = nlptest.Ambient(t, map[string][]string{"bedrock": {bedrockUS}})
	t.Setenv("AWS_REGION", "eu-west-1")
	req := sentOnce(t, rec, client(t, storage.Settings{LLMProvider: "bedrock"}), bedrockUS)
	if !strings.Contains(req.Header.Get("Authorization"), nlptest.AWSAccessKey) {
		t.Errorf("Authorization = %q, want a SigV4 signature by the sentinel key", req.Header.Get("Authorization"))
	}
}

func TestBedrock_UnapprovedOrInvalidRegionSendsNothing(t *testing.T) { // AC-1, AC-9 for bedrock
	for _, region := range []string{"eu-west-1", "attacker.example#", "attacker.example/x", "us-east-1.attacker.example"} {
		t.Run(region, func(t *testing.T) {
			rec := nlptest.Ambient(t, map[string][]string{"bedrock": {bedrockUS}})
			refusal(t, rec, client(t, storage.Settings{LLMProvider: "bedrock", AWSRegion: region}),
				nlp.ReasonDestinationNotApproved, "awsRegion")
		})
	}
}

func TestBedrock_OperatorEndpointOverrideIsCheckedToo(t *testing.T) {
	rec := nlptest.Ambient(t, map[string][]string{"bedrock": {bedrockUS}})
	t.Setenv("AWS_ENDPOINT_URL_BEDROCK_RUNTIME", "https://"+attackerHost)
	refusal(t, rec, client(t, storage.Settings{LLMProvider: "bedrock", AWSRegion: "us-east-1"}),
		nlp.ReasonDestinationNotApproved, "awsRegion")
}

func TestAmbient_RedirectIsNeverFollowedWithTheCredential(t *testing.T) { // AC-8
	cases := map[string]storage.Settings{
		azureHost: {LLMProvider: "azure_openai", AzureEndpoint: "https://" + azureHost},
		vertexEU:  {LLMProvider: "vertex", GCPLocation: "europe-west4"},
	}
	for host, s := range cases {
		for _, target := range []string{"https://" + attackerHost + "/x", "https://sub." + host + "/x", "http://" + host + "/x"} {
			t.Run(s.LLMProvider+" to "+target, func(t *testing.T) {
				rec := nlptest.Ambient(t, map[string][]string{s.LLMProvider: {host}})
				rec.Answer = func(r *http.Request) *http.Response {
					if r.URL.Host == host && r.URL.Scheme == "https" && !strings.HasSuffix(r.URL.Path, "/x") {
						return nlptest.Response(r, http.StatusTemporaryRedirect, "", http.Header{"Location": {target}})
					}
					return nil
				}
				err := completeErr(client(t, s))
				if err == nil {
					t.Fatal("Complete succeeded after a redirect; want failure")
				}
				var r *nlp.DestinationRefusal
				if errors.As(err, &r) {
					t.Errorf("a refused redirect is reported as a destination refusal (422); want a provider failure (502): %v", err)
				}
				got := rec.Requests()
				if len(got) != 1 || got[0].Host != host || got[0].Scheme != "https" {
					t.Errorf("requests = %+v, want only the original request to %s", got, host)
				}
				if len(nlptest.Leaks(err.Error())) > 0 {
					t.Errorf("error carries the credential: %v", err)
				}
			})
		}
	}
}

// Part A §6 left openai, anthropic and ollama unchecked. PAC-66-B supersedes
// it (§B6): AC-B1 (openai at http://10.0.0.1), AC-B2 and AC-B6 (anthropic at
// http://attacker.example) and AC-B8 (ollama with nothing stored, nothing
// approved) are now refused.
func TestUserKeyProviders_AreCheckedSincePAC66B(t *testing.T) {
	_ = nlptest.Ambient(t, nil)
	rec := nlptest.UserDestinations(t, "", "", "")
	for _, tc := range []struct {
		s                storage.Settings
		reason, property string
	}{
		{storage.Settings{LLMProvider: "openai", LLMAPIKey: "k", LLMBaseURL: "http://10.0.0.1"}, nlp.ReasonDestinationNotApproved, "llmBaseUrl"},
		{storage.Settings{LLMProvider: "anthropic", LLMAPIKey: "k", LLMBaseURL: "http://attacker.example"}, nlp.ReasonDestinationNotApproved, "llmBaseUrl"},
		{storage.Settings{LLMProvider: "ollama"}, nlp.ReasonProviderNotEnabled, ""},
	} {
		refusal(t, rec, client(t, tc.s), tc.reason, tc.property)
	}
}

func TestWriteChecks(t *testing.T) { // AC-5, AC-7, AC-15 (write side)
	_ = nlptest.Ambient(t, map[string][]string{"azure_openai": {azureHost}, "vertex": {vertexEU}})
	for v, want := range map[string]string{
		"":                               "",
		"https://" + azureHost:           "",
		"https://" + azureHost + "/":     "",
		"https://" + attackerHost:        nlp.MsgAzureEndpointNotApproved,
		"http://" + azureHost:            nlp.MsgAzureEndpointNotApproved,
		"https://" + azureHost + ":8443": nlp.MsgAzureEndpointNotApproved,
		"not a url":                      nlp.MsgAzureEndpointNotApproved,
	} {
		if got := nlp.AzureEndpointWriteProblem(v); got != want {
			t.Errorf("azureEndpoint %q: %q, want %q", v, got, want)
		}
	}
	for v, want := range map[string]string{
		"":                   "",
		"europe-west4":       "",
		"us-central1":        nlp.MsgGCPLocationNotApproved,
		"attacker.example/x": nlp.MsgGCPLocationNotApproved,
	} {
		if got := nlp.GCPLocationWriteProblem(v); got != want {
			t.Errorf("gcpLocation %q: %q, want %q", v, got, want)
		}
	}
	for v, want := range map[string]string{
		"": "", "us-east-1": "", "eu-west-1": "",
		"attacker.example": nlp.MsgAWSRegionInvalid, "us-east-1#": nlp.MsgAWSRegionInvalid, "-x": nlp.MsgAWSRegionInvalid,
	} {
		if got := nlp.AWSRegionWriteProblem(v); got != want {
			t.Errorf("awsRegion %q: %q, want %q", v, got, want)
		}
	}

	_ = nlptest.Ambient(t, nil)
	if got := nlp.AzureEndpointWriteProblem("https://" + azureHost); got != nlp.MsgAzureEndpointNotEnabled {
		t.Errorf("nothing approved: azureEndpoint %q, want %q", got, nlp.MsgAzureEndpointNotEnabled)
	}
	if got := nlp.GCPLocationWriteProblem("europe-west4"); got != nlp.MsgGCPLocationNotEnabled {
		t.Errorf("nothing approved: gcpLocation %q, want %q", got, nlp.MsgGCPLocationNotEnabled)
	}
	if got := nlp.AzureEndpointWriteProblem(""); got != "" {
		t.Errorf("nothing approved: \"\" refused: %q (AC-15)", got)
	}
}
