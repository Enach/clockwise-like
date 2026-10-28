package nlp

import (
	"encoding/json"
	"fmt"

	"github.com/Enach/paceday/backend/storage"
)

// NEW-llm-bring-your-own (spec §3): which AI configuration a request runs on.
// Rows of the spec's table, evaluated top to bottom; only row 2 (and, after
// Part 2, row 4) sends anything:
//
//	1 off                       -> not configured, "off"
//	2 a provider with its key   -> configured
//	3 a provider without key    -> not configured, "needs_key"
//	6 organization              -> not configured, "nothing_configured" (Part 1)
//	8 a removed provider        -> not configured, "provider_removed"
//
// Every AI-backed operation (parse, brief refresh, probe) and the settings
// read decide through here, so one state gives one reason everywhere
// (AC-40).

// Not-configured reasons and enablers, as the contract's AINotConfigured.
const (
	ReasonOff               = "off"
	ReasonNeedsKey          = "needs_key"
	ReasonNothingConfigured = "nothing_configured"
	ReasonProviderRemoved   = "provider_removed"

	EnablerUser              = "user"
	EnablerOrganizationAdmin = "organization_admin"
)

// Decision is the outcome for one request: a client for Provider, or why
// there is none. Exactly one of Client and NotConfigured is set.
type Decision struct {
	Provider      string
	Client        LLMClient
	NotConfigured *storage.AINotConfigured
}

// DecideSaved decides the caller's saved choice, as parse and brief refresh
// use it. inOrg says whether the caller belongs to an organisation (it only
// changes who can fix a not-configured answer).
func DecideSaved(s *storage.Settings, inOrg bool) Decision {
	return decide(s, inOrg, s.LLMProvider, nil, nil, true)
}

// Status is the settings read's aiStatus for the caller (AC-8, AC-8b).
func Status(s *storage.Settings, inOrg bool) storage.AIStatus {
	d := DecideSaved(s, inOrg)
	if d.NotConfigured != nil {
		return storage.AIStatus{InOrganization: inOrg, NotConfigured: d.NotConfigured}
	}
	return storage.AIStatus{Configured: true, Provider: d.Provider, InOrganization: inOrg}
}

// ProbeRequest is the body of POST /api/llm/test; nil means absent.
type ProbeRequest struct {
	Provider *string
	Model    *string
	Key      *string
}

// Probe-only 400 messages, as documented on testLLMConnection.
const (
	MsgProbeProviderInvalid     = `llmProvider must be one of: "", organization, unsupported, openai, anthropic, azure_openai, bedrock, vertex`
	MsgProbeUnsupportedNotSaved = "llmProvider unsupported can be probed only when it is your saved choice"
)

// DecideProbe decides what a probe tests (spec D-19, contract
// testLLMConnection "Which configuration is probed"). A probe naming a
// provider uses only the submitted key or the key bound to THAT provider,
// with the caller's own stored destination values for it. It returns a
// non-empty badRequest when the body names a value the probe refuses.
func DecideProbe(s *storage.Settings, inOrg bool, req ProbeRequest) (d Decision, badRequest string) {
	choice := s.LLMProvider
	if req.Provider != nil {
		switch p := *req.Provider; {
		case p == storage.LLMChoiceUnsupported:
			if s.LLMProvider != storage.LLMChoiceUnsupported {
				return Decision{}, MsgProbeUnsupportedNotSaved
			}
		case p == storage.LLMChoiceOff, p == storage.LLMChoiceOrganization, IsKeyProvider(p):
		default:
			return Decision{}, MsgProbeProviderInvalid
		}
		choice = *req.Provider
	}
	// The sentence says "AI features are off" only for the caller's saved
	// choice probed as saved: no other choice named and no key submitted.
	saved := choice == s.LLMProvider && req.Key == nil
	model := req.Model
	if model == nil && choice == s.LLMProvider {
		model = &s.LLMModel
	}
	return decide(s, inOrg, choice, req.Key, model, saved), ""
}

// decide applies the table to choice. key, when not nil, replaces the key
// bound to the provider (a probe's submitted key, "" meaning none); model,
// when not nil, is the openai/anthropic model.
func decide(s *storage.Settings, inOrg bool, choice string, key, model *string, saved bool) Decision {
	switch {
	case choice == storage.LLMChoiceOff:
		return notConfigured(ReasonOff, "", inOrg, saved, s)
	case choice == storage.LLMChoiceOrganization:
		// Part 1: no organisation configuration exists (row 6).
		return notConfigured(ReasonNothingConfigured, "", inOrg, saved, s)
	case IsKeyProvider(choice):
		k := s.LLMAPIKeys.For(choice)
		if key != nil {
			k = *key
		}
		if k == "" {
			return notConfigured(ReasonNeedsKey, choice, inOrg, saved, s)
		}
		m := ""
		if model != nil {
			m = *model
		}
		return Decision{Provider: choice, Client: newProviderClient(choice, s, k, m)}
	default:
		return notConfigured(ReasonProviderRemoved, "", inOrg, saved, s)
	}
}

// newProviderClient builds the client for a key-based provider with key,
// reading every destination value from the caller's own stored settings.
func newProviderClient(provider string, s *storage.Settings, key, model string) LLMClient {
	or := func(v, def string) string {
		if v == "" {
			return def
		}
		return v
	}
	switch provider {
	case ProviderOpenAI:
		return &OpenAIClient{APIKey: key, Model: or(model, defaultOpenAIModel), BaseURL: s.LLMBaseURL}
	case ProviderAnthropic:
		return &AnthropicClient{APIKey: key, Model: or(model, defaultAnthropicModel), BaseURL: s.LLMBaseURL}
	case ProviderAzureOpenAI:
		return &AzureOpenAIClient{APIKey: key, Endpoint: s.AzureEndpoint, Deployment: s.AzureDeployment,
			APIVersion: or(s.AzureAPIVersion, defaultAzureAPIVersion)}
	case ProviderBedrock:
		return &BedrockClient{APIKey: key, Region: or(s.AWSRegion, defaultBedrockRegion), ModelID: or(s.BedrockModel, defaultBedrockModel)}
	case ProviderVertex:
		return &VertexClient{APIKey: key, ModelID: or(s.VertexModel, defaultVertexModel)}
	}
	return nil
}

func notConfigured(reason, provider string, inOrg, saved bool, s *storage.Settings) Decision {
	enablers := []string{EnablerUser}
	if inOrg && (reason == ReasonNothingConfigured || reason == ReasonProviderRemoved) {
		enablers = append(enablers, EnablerOrganizationAdmin)
	}
	return Decision{Provider: provider, NotConfigured: &storage.AINotConfigured{
		Reason: reason, Provider: provider, Enablers: enablers,
		Message: notConfiguredMessage(reason, provider, inOrg, saved, s),
	}}
}

// notConfiguredMessage is the sentence for the user. For the saved choice it
// says AI features are off; for another probed choice it describes only that
// choice (contract AINotConfigured.message). It never carries a key.
func notConfiguredMessage(reason, provider string, inOrg, saved bool, s *storage.Settings) string {
	orAdmin := ""
	if inOrg {
		orAdmin = ", or ask your organisation's admin"
	}
	name := displayNames[provider]
	if saved {
		switch reason {
		case ReasonOff:
			return "AI features are off: you turned them off in Settings. Choose a provider in Settings to turn them on."
		case ReasonNeedsKey:
			return fmt.Sprintf("AI features are off: %s needs an API key. Add one in Settings.", name)
		case ReasonNothingConfigured:
			return "AI features are off: no AI provider is set up. Add your own API key in Settings" + orAdmin + "."
		default:
			return "AI features are off: " + removedName(s) + " is no longer supported. Choose another provider in Settings" + orAdmin + "."
		}
	}
	switch reason {
	case ReasonOff:
		return "Off is not a provider, so there is nothing to test. Choose a provider to test it."
	case ReasonNeedsKey:
		return fmt.Sprintf("%s needs an API key. Enter one to test it.", name)
	case ReasonNothingConfigured:
		if inOrg {
			return "Your organisation has no AI provider set up, so there is nothing to test. Use your own key, or ask your organisation's admin."
		}
		return "You do not belong to an organisation, so there is no organisation AI to test. Choose a provider and enter your own key."
	default:
		return removedName(s) + " is no longer supported, so it cannot be tested. Choose another provider."
	}
}

// removedName names the removed provider a row is stored on.
func removedName(s *storage.Settings) string {
	if s != nil && s.StoredLLMProvider == "ollama" {
		return "Ollama"
	}
	return "Your previous AI provider"
}

func unmarshalLLMResponse(body []byte, v interface{}) error {
	return json.Unmarshal(body, v)
}
