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

// PAC-66, as changed by NEW-llm-bring-your-own: the user's key for Azure
// OpenAI, Vertex and Bedrock goes only to hosts the operator approved, and no
// identity of the machine Paceday runs on is used. Every test runs with
// ambient AWS, Azure and Google credentials in the environment
// (nlptest.Ambient) and a sentinel user key bound to the provider, so a
// request carrying anything but that key would be seen.

const (
	azureHost    = "my-resource.openai.azure.com"
	vertexHost   = "aiplatform.googleapis.com"
	vertexEU     = "europe-west4-aiplatform.googleapis.com"
	bedrockUS    = "bedrock-runtime.us-east-1.amazonaws.com"
	bedrockEU    = "bedrock-runtime.eu-west-1.amazonaws.com"
	attackerHost = "attacker.example"
)

// client builds the client the saved choice in s decides on, binding the
// sentinel user key to the provider when s has none.
func client(t *testing.T, s storage.Settings) nlp.LLMClient {
	t.Helper()
	if s.LLMAPIKeys.For(s.LLMProvider) == "" {
		s.LLMAPIKeys.Set(s.LLMProvider, nlptest.UserKey)
	}
	d := nlp.DecideSaved(&s, false)
	if d.NotConfigured != nil {
		t.Fatalf("construct %s: not configured: %s", s.LLMProvider, d.NotConfigured.Message)
	}
	return d.Client
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
		if strings.TrimSpace(r.Error()) == "" || len(nlptest.Leaks(r.Error())) > 0 || strings.Contains(r.Error(), nlptest.UserKey) {
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

// sentOnce asserts Complete succeeds with exactly one https request to host,
// carrying no ambient credential.
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
	for k, v := range got[0].Header {
		if leaks := nlptest.Leaks(strings.Join(v, " ")); len(leaks) > 0 {
			t.Errorf("header %s carries an ambient credential %v", k, leaks)
		}
	}
	return got[0]
}

func TestAzure_UnapprovedEndpointSendsNothing(t *testing.T) { // AC-1, AC-9
	rec := nlptest.Ambient(t, map[string][]string{"azure_openai": {azureHost}})
	refusal(t, rec, client(t, storage.Settings{LLMProvider: "azure_openai", AzureEndpoint: "https://" + attackerHost}),
		nlp.ReasonDestinationNotApproved, "azureEndpoint")
}

func TestAzure_ApprovedEndpointSendsOnceWithTheKeyOnly(t *testing.T) { // AC-6, NEW-llm-bring-your-own AC-3
	rec := nlptest.Ambient(t, map[string][]string{"azure_openai": {azureHost}})
	req := sentOnce(t, rec, client(t, storage.Settings{LLMProvider: "azure_openai",
		AzureEndpoint: "https://My-Resource.openai.azure.com/", AzureDeployment: "gpt"}), azureHost)
	if req.Header.Get("api-key") != nlptest.UserKey || req.Header.Get("Authorization") != "" {
		t.Errorf("headers = %v, want api-key = the user's key and no Authorization", req.Header)
	}
	if req.Path != "/openai/deployments/gpt/chat/completions" || req.Query != "api-version=2024-02-01" {
		t.Errorf("request = %s?%s", req.Path, req.Query)
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

func TestCloud_NothingApprovedMeansNotEnabled(t *testing.T) { // AC-4, D-1
	for _, s := range []storage.Settings{
		{LLMProvider: "azure_openai", AzureEndpoint: "https://" + azureHost},
		{LLMProvider: "azure_openai"},
		{LLMProvider: "vertex"},
		{LLMProvider: "bedrock", AWSRegion: "eu-west-1"},
		{LLMProvider: "bedrock"},
	} {
		t.Run(s.LLMProvider+"/"+s.AzureEndpoint+s.AWSRegion, func(t *testing.T) {
			// A host approved for ANOTHER provider never enables this one (D-8).
			others := map[string][]string{"azure_openai": {vertexHost, bedrockEU}, "vertex": {azureHost}, "bedrock": {azureHost, vertexHost}}
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

func TestCloud_HostApprovedOnlyForAnotherProvider(t *testing.T) { // AC-18 (c), D-8
	rec := nlptest.Ambient(t, map[string][]string{"azure_openai": {azureHost}, "vertex": {vertexHost, attackerHost}})
	refusal(t, rec, client(t, storage.Settings{LLMProvider: "azure_openai", AzureEndpoint: "https://" + vertexHost}),
		nlp.ReasonDestinationNotApproved, "azureEndpoint")
	refusal(t, rec, client(t, storage.Settings{LLMProvider: "azure_openai", AzureEndpoint: "https://" + attackerHost}),
		nlp.ReasonDestinationNotApproved, "azureEndpoint")
}

// NEW-llm-bring-your-own D-4: Vertex express mode has one destination, the
// global host, and uses no project or location. A regional host the operator
// approved for the removed ADC mode approves nothing. Replaces the regional
// location tests (TestVertex_LocationThatMovesTheAuthoritySendsNothing,
// TestVertex_EmptyLocationMeansTheDefaultRegion).
func TestVertex_ExpressModeGlobalHostOnly(t *testing.T) { // AC-5
	rec := nlptest.Ambient(t, map[string][]string{"vertex": {vertexEU}})
	refusal(t, rec, client(t, storage.Settings{LLMProvider: "vertex", GCPProject: "p", GCPLocation: "europe-west4"}),
		nlp.ReasonProviderNotEnabled, "")

	rec = nlptest.Ambient(t, map[string][]string{"vertex": {vertexHost}})
	req := sentOnce(t, rec, client(t, storage.Settings{LLMProvider: "vertex", GCPProject: "p", GCPLocation: "europe-west4", VertexModel: "gemini-2.5-flash"}), vertexHost)
	if req.Header.Get("x-goog-api-key") != nlptest.UserKey || req.Header.Get("Authorization") != "" {
		t.Errorf("headers = %v, want x-goog-api-key = the user's key and no Authorization", req.Header)
	}
	if req.Path != "/v1/publishers/google/models/gemini-2.5-flash:generateContent" || req.Query != "" {
		t.Errorf("request = %s?%s, want the express path with no key in the URL and no project or location", req.Path, req.Query)
	}
}

func TestBedrock_EmptyRegionMeansUSEast1NotTheDeploymentRegion(t *testing.T) { // AC-19, D-7
	rec := nlptest.Ambient(t, map[string][]string{"bedrock": {bedrockEU}})
	t.Setenv("AWS_REGION", "eu-west-1")
	refusal(t, rec, client(t, storage.Settings{LLMProvider: "bedrock"}), nlp.ReasonDestinationNotApproved, "awsRegion")

	rec = nlptest.Ambient(t, map[string][]string{"bedrock": {bedrockUS}})
	t.Setenv("AWS_REGION", "eu-west-1")
	req := sentOnce(t, rec, client(t, storage.Settings{LLMProvider: "bedrock"}), bedrockUS)
	if req.Header.Get("Authorization") != "Bearer "+nlptest.UserKey {
		t.Errorf("Authorization = %q, want the Bedrock API key as a bearer token (AC-4)", req.Header.Get("Authorization"))
	}
	if req.EscapedPath != "/model/anthropic.claude-sonnet-4-5-20251001-v1:0/converse" {
		t.Errorf("path = %q, want the Converse path with the default model as one segment", req.EscapedPath)
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

// Was TestBedrock_OperatorEndpointOverrideIsCheckedToo: with the AWS SDK gone,
// no AWS environment variable or profile can move the destination at all.
func TestBedrock_AWSEnvironmentCannotMoveTheDestination(t *testing.T) {
	rec := nlptest.Ambient(t, map[string][]string{"bedrock": {bedrockUS}})
	t.Setenv("AWS_ENDPOINT_URL_BEDROCK_RUNTIME", "https://"+attackerHost)
	t.Setenv("AWS_ENDPOINT_URL", "https://"+attackerHost)
	t.Setenv("AWS_PROFILE", "attacker")
	sentOnce(t, rec, client(t, storage.Settings{LLMProvider: "bedrock", AWSRegion: "us-east-1", AWSProfile: "attacker"}), bedrockUS)
}

func TestCloud_RedirectIsNeverFollowedWithTheKey(t *testing.T) { // AC-8
	cases := map[string]storage.Settings{
		azureHost:  {LLMProvider: "azure_openai", AzureEndpoint: "https://" + azureHost},
		vertexHost: {LLMProvider: "vertex"},
		bedrockUS:  {LLMProvider: "bedrock", AWSRegion: "us-east-1"},
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
				if strings.Contains(err.Error(), nlptest.UserKey) {
					t.Errorf("error carries the key: %v", err)
				}
			})
		}
	}
}

// Part A §6 left openai and anthropic unchecked. PAC-66-B supersedes it
// (§B6): AC-B1 (openai at http://10.0.0.1) and AC-B2 / AC-B6 (anthropic at
// http://attacker.example) are refused.
func TestUserKeyProviders_AreCheckedSincePAC66B(t *testing.T) {
	_ = nlptest.Ambient(t, nil)
	rec := nlptest.UserDestinations(t, "", "")
	for _, s := range []storage.Settings{
		{LLMProvider: "openai", LLMBaseURL: "http://10.0.0.1"},
		{LLMProvider: "anthropic", LLMBaseURL: "http://attacker.example"},
	} {
		refusal(t, rec, client(t, s), nlp.ReasonDestinationNotApproved, "llmBaseUrl")
	}
}

func TestWriteChecks(t *testing.T) { // AC-5, AC-7, AC-15 (write side)
	_ = nlptest.Ambient(t, map[string][]string{"azure_openai": {azureHost}})
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
	if got := nlp.AzureEndpointWriteProblem(""); got != "" {
		t.Errorf("nothing approved: \"\" refused: %q (AC-15)", got)
	}
}
