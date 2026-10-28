package nlp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/storage"
)

type countingTransport struct{ n int }

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n++
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: r}, nil
}

// The transport is the last line: whatever built the URL, an unapproved
// destination is refused at the moment of sending (spec §3).
func TestApprovedOnlyTransport_RefusesAtTheMomentOfSending(t *testing.T) {
	inner := &countingTransport{}
	t.Cleanup(SetApprovedHosts(config.LLMApprovedHosts{ProviderVertex: {vertexExpressHost: true}}))
	t.Cleanup(SetCloudDeps(CloudDeps{Transport: inner}))
	c := cloudHTTPClient(ProviderVertex)
	send := func(target, host string) error {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, target, strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		if host != "" {
			req.Host = host
		}
		resp, err := c.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
		return err
	}

	for _, target := range []string{
		"https://attacker.example/x",
		"http://aiplatform.googleapis.com/x",
		"https://aiplatform.googleapis.com:8443/x",
		"https://u@aiplatform.googleapis.com/x",
		"https://europe-west4-aiplatform.googleapis.com/x",
	} {
		var r *DestinationRefusal
		if err := send(target, ""); !errors.As(err, &r) {
			t.Errorf("%s: err = %v, want a refusal", target, err)
		}
	}
	// A Host header that differs from the URL's host is refused too.
	if err := send("https://aiplatform.googleapis.com/x", "attacker.example"); err == nil {
		t.Error("a mismatched Host header was sent")
	}
	if inner.n != 0 {
		t.Fatalf("%d requests reached the network, want zero", inner.n)
	}

	if err := send("https://AIPlatform.googleapis.com/x", ""); err != nil || inner.n != 1 {
		t.Fatalf("approved request: err %v, sent %d, want sent once", err, inner.n)
	}

	// A transport refusal is attributed to the provider the client was made for.
	t.Cleanup(SetApprovedHosts(config.LLMApprovedHosts{ProviderAzureOpenAI: {vertexExpressHost: true}}))
	if err := send("https://aiplatform.googleapis.com/x", ""); err == nil || inner.n != 1 {
		t.Errorf("a host approved only for azure_openai was sent the vertex key (D-8)")
	}
}

// Before main installs a policy, nothing is approved (fail closed, D-1).
func TestNoPolicyApprovesNothing(t *testing.T) {
	t.Cleanup(SetApprovedHosts(nil))
	for _, c := range []LLMClient{
		&AzureOpenAIClient{APIKey: "k", Endpoint: "https://my-resource.openai.azure.com"},
		&VertexClient{APIKey: "k", ModelID: "m"},
		&BedrockClient{APIKey: "k", Region: "us-east-1", ModelID: "m"},
	} {
		var r *DestinationRefusal
		if err := CheckDestination(context.Background(), c); !errors.As(err, &r) || r.Reason != ReasonProviderNotEnabled {
			t.Errorf("%T: err = %v, want provider_not_enabled", c, err)
		}
	}
}

// NEW-llm-bring-your-own spec §3: the decision table, one state, one reason.
// Replaces TestRefusalDoesNotDependOnTheCredential: there is no credential
// acquisition step any more; a provider with no key is decided "needs a key"
// before any destination is looked at.
func TestDecideSaved_OneStateOneReason(t *testing.T) {
	for _, tc := range []struct {
		name             string
		s                *settingsForTest
		inOrg            bool
		reason, provider string
		enablers         string
	}{
		{"off", &settingsForTest{provider: ""}, true, ReasonOff, "", "user"},
		{"openai no key", &settingsForTest{provider: "openai"}, true, ReasonNeedsKey, "openai", "user"},
		{"bedrock no key, unbound key", &settingsForTest{provider: "bedrock", unbound: "k"}, true, ReasonNeedsKey, "bedrock", "user"},
		{"organization, member", &settingsForTest{provider: "organization"}, true, ReasonNothingConfigured, "", "user"},
		{"organization, no org", &settingsForTest{provider: "organization"}, false, ReasonNothingConfigured, "", "user"},
		{"ollama, member", &settingsForTest{provider: "unsupported", stored: "ollama"}, true, ReasonProviderRemoved, "", "user"},
		{"ollama, no org", &settingsForTest{provider: "unsupported", stored: "ollama"}, false, ReasonProviderRemoved, "", "user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := DecideSaved(tc.s.settings(), tc.inOrg)
			nc := d.NotConfigured
			if nc == nil || d.Client != nil {
				t.Fatalf("decision = %+v, want not configured", d)
			}
			if nc.Reason != tc.reason || nc.Provider != tc.provider || strings.Join(nc.Enablers, ",") != tc.enablers {
				t.Errorf("got %s/%s/%v, want %s/%s/%s", nc.Reason, nc.Provider, nc.Enablers, tc.reason, tc.provider, tc.enablers)
			}
			if !strings.HasPrefix(nc.Message, "AI features are off: ") {
				t.Errorf("the saved choice's message %q does not say AI is off", nc.Message)
			}
			if tc.s.stored == "ollama" && !strings.Contains(nc.Message, "Ollama is no longer supported") {
				t.Errorf("message %q does not name Ollama", nc.Message)
			}
			// Spec revision 2 (Blocking 1): until Part 2 no admin can configure
			// AI, so no Part 1 answer names one, in or out of an organisation.
			if strings.Contains(nc.Message, "admin") {
				t.Errorf("message %q names an organisation admin; in Part 1 only the user can fix it", nc.Message)
			}
			st := Status(tc.s.settings(), tc.inOrg)
			if st.Configured || st.Provider != "" || st.NotConfigured == nil || st.NotConfigured.Reason != tc.reason || st.InOrganization != tc.inOrg {
				t.Errorf("Status = %+v", st)
			}
		})
	}
	s := (&settingsForTest{provider: "anthropic"}).settings()
	s.LLMAPIKeys.Set("anthropic", "k")
	d := DecideSaved(s, false)
	if c, ok := d.Client.(*AnthropicClient); !ok || c.APIKey != "k" || c.Model != defaultAnthropicModel || d.NotConfigured != nil {
		t.Errorf("configured anthropic = %+v", d)
	}
	if st := Status(s, false); !st.Configured || st.Provider != "anthropic" || st.NotConfigured != nil {
		t.Errorf("Status = %+v, want configured anthropic", st)
	}
}

// Defaults applied to empty stored values, per provider.
func TestDecideSaved_Defaults(t *testing.T) {
	for _, p := range KeyProviders {
		s := (&settingsForTest{provider: p}).settings()
		s.LLMAPIKeys.Set(p, "k")
		switch c := DecideSaved(s, false).Client.(type) {
		case *OpenAIClient:
			if c.Model != defaultOpenAIModel {
				t.Errorf("openai model %q", c.Model)
			}
		case *AnthropicClient:
			if c.Model != defaultAnthropicModel {
				t.Errorf("anthropic model %q", c.Model)
			}
		case *AzureOpenAIClient:
			if c.APIVersion != defaultAzureAPIVersion || c.APIKey != "k" {
				t.Errorf("azure %+v", c)
			}
		case *BedrockClient:
			if c.Region != defaultBedrockRegion || c.ModelID != defaultBedrockModel || c.APIKey != "k" {
				t.Errorf("bedrock %+v", c)
			}
		case *VertexClient:
			if c.ModelID != defaultVertexModel || c.APIKey != "k" {
				t.Errorf("vertex %+v", c)
			}
		default:
			t.Errorf("%s: client %T", p, c)
		}
	}
}

// spec D-19, AC-37, contract testLLMConnection "Which configuration is probed".
func TestDecideProbe(t *testing.T) {
	str := func(v string) *string { return &v }
	s := (&settingsForTest{provider: "openai"}).settings()
	s.LLMAPIKeys.Set("openai", "sk-openai")
	s.LLMModel = "gpt-x"

	for _, tc := range []struct {
		name      string
		req       ProbeRequest
		bad       string
		reason    string
		saidOff   bool
		key, mdl  string
		clientFor string
	}{
		{name: "saved", req: ProbeRequest{}, key: "sk-openai", mdl: "gpt-x", clientFor: "openai"},
		{name: "other provider no key", req: ProbeRequest{Provider: str("anthropic")}, reason: ReasonNeedsKey},
		{name: "other provider with key", req: ProbeRequest{Provider: str("anthropic"), Key: str("sk-sub")}, key: "sk-sub", mdl: defaultAnthropicModel, clientFor: "anthropic"},
		{name: "empty key", req: ProbeRequest{Key: str("")}, reason: ReasonNeedsKey},
		{name: "model override", req: ProbeRequest{Model: str("gpt-y")}, key: "sk-openai", mdl: "gpt-y", clientFor: "openai"},
		{name: "off", req: ProbeRequest{Provider: str("")}, reason: ReasonOff},
		{name: "organization", req: ProbeRequest{Provider: str("organization"), Key: str("sk-x")}, reason: ReasonNothingConfigured},
		{name: "unsupported not saved", req: ProbeRequest{Provider: str("unsupported")}, bad: MsgProbeUnsupportedNotSaved},
		{name: "ollama", req: ProbeRequest{Provider: str("ollama")}, bad: MsgProbeProviderInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, bad := DecideProbe(s, false, tc.req)
			if bad != tc.bad {
				t.Fatalf("bad = %q, want %q", bad, tc.bad)
			}
			if tc.bad != "" {
				return
			}
			if tc.reason != "" {
				if d.NotConfigured == nil || d.NotConfigured.Reason != tc.reason {
					t.Fatalf("decision = %+v, want %s", d, tc.reason)
				}
				if strings.Contains(d.NotConfigured.Message, "AI features are off") {
					t.Errorf("a probe of another choice or with a key says AI is off: %q (C-3)", d.NotConfigured.Message)
				}
				return
			}
			switch c := d.Client.(type) {
			case *OpenAIClient:
				if tc.clientFor != "openai" || c.APIKey != tc.key || c.Model != tc.mdl {
					t.Errorf("client %+v", c)
				}
			case *AnthropicClient:
				if tc.clientFor != "anthropic" || c.APIKey != tc.key || c.Model != tc.mdl {
					t.Errorf("client %+v", c)
				}
			default:
				t.Errorf("client %T", c)
			}
		})
	}

	ollama := (&settingsForTest{provider: "unsupported", stored: "ollama", unbound: "sk-legacy"}).settings()
	for _, req := range []ProbeRequest{{}, {Provider: str("unsupported")}, {Provider: str("unsupported"), Key: str("sk-x")}} {
		d, bad := DecideProbe(ollama, true, req)
		if bad != "" || d.NotConfigured == nil || d.NotConfigured.Reason != ReasonProviderRemoved {
			t.Errorf("ollama probe %+v = %+v %q, want provider_removed", req, d, bad)
		}
	}
}

// A stored value that is neither Ollama nor any known choice (a direct
// database edit) is a removed provider too, named generically.
func TestDecideSaved_UnknownStoredProviderIsRemoved(t *testing.T) {
	d := DecideSaved((&settingsForTest{provider: "unsupported", stored: "skynet"}).settings(), false)
	if d.NotConfigured == nil || d.NotConfigured.Reason != ReasonProviderRemoved ||
		d.NotConfigured.Message != "AI features are off: Your previous AI provider is no longer supported. Choose another provider in Settings." {
		t.Errorf("decision = %+v", d.NotConfigured)
	}
}

type configuredSettings = storage.Settings

type settingsForTest struct{ provider, stored, unbound string }

func (f *settingsForTest) settings() *configuredSettings {
	s := &configuredSettings{LLMProvider: f.provider, StoredLLMProvider: f.stored, LLMUnboundAPIKey: f.unbound}
	if s.StoredLLMProvider == "" {
		s.StoredLLMProvider = f.provider
	}
	return s
}
