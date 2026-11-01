package nlp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// NEW-llm-bring-your-own (spec §3, Part 2 spec
// docs/specs/NEW-llm-bring-your-own-part2.md §3.1): which AI configuration a
// request runs on. Rows of the table, evaluated in this order; only rows 2
// and 4 send anything:
//
//	1  off                                      -> "off" [user]
//	10 own/removed provider; forbids; complete  -> "personal_keys_forbidden" [user]
//	11 own/removed provider; forbids; not compl -> "personal_keys_forbidden" [organization_admin]
//	2  a provider with its key                  -> configured: own
//	3  a provider without key                   -> "needs_key" [user]
//	4  organization; complete; confirmed        -> configured: organisation
//	5  organization; complete; not confirmed    -> "organization_unconfirmed" [user]
//	6  organization; no org or not complete     -> "nothing_configured" [user (+admin in org) | admin when forbids]
//	7  removed provider; org complete           -> "provider_removed_organization_available" [user]
//	8  removed provider; otherwise              -> "provider_removed" [user (+admin in org)]
//
// Row 9 is reserved (spec P2-D2's current default, OQ-P2-2) and never
// answered. Every AI-backed operation (parse, brief refresh, probe) and the
// settings read decide through here, so one state gives one reason
// everywhere (P2-AC-29).

// Not-configured reasons and enablers, as the contract's AINotConfigured.
const (
	ReasonOff                                  = "off"
	ReasonNeedsKey                             = "needs_key"
	ReasonOrganizationUnconfirmed              = "organization_unconfirmed"
	ReasonNothingConfigured                    = "nothing_configured"
	ReasonProviderRemovedOrganizationAvailable = "provider_removed_organization_available"
	ReasonProviderRemoved                      = "provider_removed"
	ReasonPersonalKeysForbidden                = "personal_keys_forbidden"

	EnablerUser              = "user"
	EnablerOrganizationAdmin = "organization_admin"

	// ConfigurationOwn and ConfigurationOrganization say whose configuration
	// a configured decision runs on (contract AIStatus.configuration,
	// LLMTestResult.configuration).
	ConfigurationOwn          = "own"
	ConfigurationOrganization = "organization"
)

// AIState is everything a decision reads: the caller's settings and one
// consistent read of their organisation, its configuration and their
// confirmation (storage.ReadOrgAIState).
type AIState struct {
	Settings *storage.Settings
	Org      storage.OrgAIState
}

// Decision is the outcome for one request: a client for Provider, or why
// there is none. Exactly one of Client and NotConfigured is set.
type Decision struct {
	Provider      string
	Client        LLMClient
	NotConfigured *storage.AINotConfigured
	// Configuration is ConfigurationOwn or ConfigurationOrganization when
	// Client is set.
	Configuration string
	// Secrets are the keys this request would send and the caller's own
	// keys (stored and submitted): every string Redact replaces (spec
	// §3.1a).
	Secrets []string
}

// Organization reports whether the decision runs on the organisation's
// configuration (row 4).
func (d Decision) Organization() bool { return d.Configuration == ConfigurationOrganization }

// Redact replaces every occurrence of every secret of d in msg with
// "[redacted]" (exact strings, longest first). It is applied to provider
// text before it is logged, reported, stored or returned.
func (d Decision) Redact(msg string) string { return RedactSecrets(msg, d.Secrets) }

// RedactSecrets replaces every occurrence of each non-empty secret in msg
// with "[redacted]", longest first so a key that contains another is
// replaced whole.
func RedactSecrets(msg string, secrets []string) string {
	keys := make([]string, 0, len(secrets))
	for _, s := range secrets {
		if s != "" {
			keys = append(keys, s)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, k := range keys {
		msg = strings.ReplaceAll(msg, k, "[redacted]")
	}
	return msg
}

// stateFor is the state of a caller with no organisation configuration:
// Part 1's view, kept for callers and tests that know only membership.
func stateFor(s *storage.Settings, inOrg bool) AIState {
	st := AIState{Settings: s, Org: storage.OrgAIState{Config: storage.OrgAIConfig{PersonalKeysAllowed: true}}}
	if inOrg {
		id := uuid.Nil
		st.Org.OrgID = &id
	}
	return st
}

// DecideSaved decides the caller's saved choice with no organisation
// configuration (see DecideState).
func DecideSaved(s *storage.Settings, inOrg bool) Decision { return DecideState(stateFor(s, inOrg)) }

// DecideState decides the caller's saved choice, as parse and brief refresh
// use it.
func DecideState(st AIState) Decision {
	s := st.Settings
	return decide(st, s.LLMProvider, nil, nil, true, s.LLMAPIKeys.All())
}

// Status is the settings read's aiStatus with no organisation configuration.
func Status(s *storage.Settings, inOrg bool) storage.AIStatus { return StatusOf(stateFor(s, inOrg)) }

// StatusOf is the settings read's aiStatus for the caller (AC-8; Part 2
// adds configuration).
func StatusOf(st AIState) storage.AIStatus {
	d := DecideState(st)
	if d.NotConfigured != nil {
		return storage.AIStatus{InOrganization: st.Org.InOrg(), NotConfigured: d.NotConfigured}
	}
	return storage.AIStatus{Configured: true, Provider: d.Provider, Configuration: d.Configuration, InOrganization: st.Org.InOrg()}
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
	MsgProbeOrganizationNoModel = "llmModel and llmApiKey cannot be used with your organisation's AI configuration"
)

// DecideProbe decides a probe with no organisation configuration (see
// DecideProbeState).
func DecideProbe(s *storage.Settings, inOrg bool, req ProbeRequest) (Decision, string) {
	return DecideProbeState(stateFor(s, inOrg), req)
}

// DecideProbeState decides what a probe tests (spec D-19, Part 2 §3.5;
// contract testLLMConnection "Order of decisions"). A probe naming a
// provider uses only the submitted key or the key bound to THAT provider,
// with the caller's own stored destination values for it; naming a provider
// never selects the organisation's key (P2-D6). It returns a non-empty
// badRequest when the body names a value the probe refuses.
func DecideProbeState(st AIState, req ProbeRequest) (d Decision, badRequest string) {
	s := st.Settings
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
	// Step 1a (Part 2, owner O-12): while personal keys are forbidden, a
	// probe naming a provider or carrying a key sends nothing, whatever
	// else it says.
	if st.Org.Forbids() && ((req.Provider != nil && IsKeyProvider(*req.Provider)) || req.Key != nil) {
		what := "the key you entered"
		if req.Provider != nil && IsKeyProvider(*req.Provider) {
			what = displayNames[*req.Provider]
		}
		msg := "Your organisation does not allow personal API keys, so " + what +
			" cannot be tested. An administrator of your organisation can change this."
		return Decision{NotConfigured: &storage.AINotConfigured{Reason: ReasonPersonalKeysForbidden,
			Enablers: []string{EnablerOrganizationAdmin}, Message: msg}}, ""
	}
	// The sentence says "AI features are off" only for the caller's saved
	// choice probed as saved: no other choice named and no key submitted.
	saved := choice == s.LLMProvider && req.Key == nil
	model := req.Model
	if model == nil && choice == s.LLMProvider {
		model = &s.LLMModel
	}
	own := s.LLMAPIKeys.All()
	if req.Key != nil {
		own = append(own, *req.Key)
	}
	d = decide(st, choice, req.Key, model, saved, own)
	// Step 2a: a model or key is never combined with the organisation's
	// configuration (parent D-18).
	if d.Organization() && (req.Model != nil || req.Key != nil) {
		return Decision{}, MsgProbeOrganizationNoModel
	}
	return d, ""
}

// decide applies the table to choice. key, when not nil, replaces the key
// bound to the provider (a probe's submitted key, "" meaning none); model,
// when not nil, is the caller's openai/anthropic model. own are the
// caller's keys, for redaction.
func decide(st AIState, choice string, key, model *string, saved bool, own []string) Decision {
	s := st.Settings
	org := st.Org
	secrets := append(append([]string{}, own...), s.LLMUnboundAPIKey)
	complete := org.InOrg() && org.Config.Complete()
	switch {
	case choice == storage.LLMChoiceOff:
		return notConfigured(ReasonOff, "", saved, s, st, EnablerUser)
	case org.Forbids() && (IsKeyProvider(choice) || choice == storage.LLMChoiceUnsupported):
		if complete {
			return notConfigured(ReasonPersonalKeysForbidden, "", saved, s, st, EnablerUser) // row 10
		}
		return notConfigured(ReasonPersonalKeysForbidden, "", saved, s, st, EnablerOrganizationAdmin) // row 11
	case IsKeyProvider(choice):
		k := s.LLMAPIKeys.For(choice)
		if key != nil {
			k = *key
		}
		if k == "" {
			return notConfigured(ReasonNeedsKey, choice, saved, s, st, EnablerUser)
		}
		m := ""
		if model != nil {
			m = *model
		}
		return Decision{Provider: choice, Client: newProviderClient(choice, s, k, m),
			Configuration: ConfigurationOwn, Secrets: append(secrets, k)}
	case choice == storage.LLMChoiceOrganization:
		switch {
		case complete && org.Confirmed():
			c := org.Config
			return Decision{Provider: c.Provider, Client: newOrgClient(c),
				Configuration: ConfigurationOrganization, Secrets: append(secrets, c.APIKey)}
		case complete:
			return notConfigured(ReasonOrganizationUnconfirmed, "", saved, s, st, EnablerUser)
		case org.Forbids():
			return notConfigured(ReasonNothingConfigured, "", saved, s, st, EnablerOrganizationAdmin)
		case org.InOrg():
			return notConfigured(ReasonNothingConfigured, "", saved, s, st, EnablerUser, EnablerOrganizationAdmin)
		default:
			return notConfigured(ReasonNothingConfigured, "", saved, s, st, EnablerUser)
		}
	default:
		if complete {
			return notConfigured(ReasonProviderRemovedOrganizationAvailable, "", saved, s, st, EnablerUser)
		}
		if org.InOrg() {
			return notConfigured(ReasonProviderRemoved, "", saved, s, st, EnablerUser, EnablerOrganizationAdmin)
		}
		return notConfigured(ReasonProviderRemoved, "", saved, s, st, EnablerUser)
	}
}

// newProviderClient builds the client for a key-based provider with key,
// reading every destination value from the caller's own stored settings.
func newProviderClient(provider string, s *storage.Settings, key, model string) LLMClient {
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

// newOrgClient builds the client for the organisation's configuration (row
// 4): every value is the organisation's, nothing is the caller's (parent
// D-18, P2-AC-12).
func newOrgClient(c storage.OrgAIConfig) LLMClient {
	switch c.Provider {
	case ProviderOpenAI:
		return &OpenAIClient{APIKey: c.APIKey, Model: or(c.Model, defaultOpenAIModel), BaseURL: c.BaseURL}
	case ProviderAnthropic:
		return &AnthropicClient{APIKey: c.APIKey, Model: or(c.Model, defaultAnthropicModel), BaseURL: c.BaseURL}
	case ProviderAzureOpenAI:
		return &AzureOpenAIClient{APIKey: c.APIKey, Endpoint: c.AzureEndpoint, Deployment: c.AzureDeployment,
			APIVersion: or(c.AzureAPIVersion, defaultAzureAPIVersion)}
	case ProviderBedrock:
		return &BedrockClient{APIKey: c.APIKey, Region: or(c.AWSRegion, defaultBedrockRegion), ModelID: or(c.Model, defaultBedrockModel)}
	case ProviderVertex:
		return &VertexClient{APIKey: c.APIKey, ModelID: or(c.Model, defaultVertexModel)}
	}
	return nil
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// notConfigured builds a not-configured answer with the enablers of its row.
func notConfigured(reason, provider string, saved bool, s *storage.Settings, st AIState, enablers ...string) Decision {
	return Decision{Provider: provider, NotConfigured: &storage.AINotConfigured{
		Reason: reason, Provider: provider, Enablers: enablers,
		Message: notConfiguredMessage(reason, provider, saved, s, st, enablers),
	}}
}

// adminFixes reports whether the organisation's administrator is among the
// enablers.
func adminFixes(enablers []string) bool {
	for _, e := range enablers {
		if e == EnablerOrganizationAdmin {
			return true
		}
	}
	return false
}

// O-13's notice (owner, 2026-10-28): the organisation's provider account's
// administrators can ACCESS the data. No sentence here says "read", "reads"
// or "see" (P2-AC-26).
const consentSentence = "Your text and meeting details (titles, attendee emails, Slack and Notion excerpts) are sent to your organisation's AI provider account, which its administrators can access outside Paceday."

// notConfiguredMessage is the sentence for the user. For the saved choice it
// says AI features are off; for another probed choice it describes only that
// choice (contract AINotConfigured.message). It names the organisation's
// administrator exactly when the enablers do. It never carries a key.
func notConfiguredMessage(reason, provider string, saved bool, s *storage.Settings, st AIState, enablers []string) string {
	name := displayNames[provider]
	orgName := displayNames[st.Org.Config.Provider]
	admin := adminFixes(enablers)
	adminOnly := admin && len(enablers) == 1
	prefix := ""
	if saved {
		prefix = "AI features are off: "
	}
	switch reason {
	case ReasonOff:
		if saved {
			return "AI features are off: you turned them off in Settings. Choose a provider in Settings to turn them on."
		}
		return "Off is not a provider, so there is nothing to test. Choose a provider to test it."
	case ReasonNeedsKey:
		if saved {
			return fmt.Sprintf("AI features are off: %s needs an API key. Add one in Settings.", name)
		}
		return fmt.Sprintf("%s needs an API key. Enter one to test it.", name)
	case ReasonOrganizationUnconfirmed:
		if saved {
			return "AI features are off until you confirm: your organisation provides AI through " + orgName + ". " +
				consentSentence + " Confirm in Paceday to turn AI on."
		}
		return "Your organisation provides AI through " + orgName + ". " + consentSentence + " Confirm in Paceday to test it."
	case ReasonNothingConfigured:
		switch {
		case adminOnly:
			return prefix + upperFirst(saved, "your organisation does not allow personal API keys and has not finished setting up its AI. An administrator of your organisation can set it up.")
		case admin && saved:
			return "AI features are off: no AI provider is set up. Add your own API key in Settings, or ask an administrator of your organisation to set up its AI."
		case admin:
			return "Your organisation has not set up AI, so there is nothing to test. Add your own key in Settings, or ask an administrator of your organisation to set up its AI."
		case saved:
			return "AI features are off: no AI provider is set up. Add your own API key in Settings."
		default:
			return "There is no AI provider to test. Choose a provider and add your key in Settings."
		}
	case ReasonPersonalKeysForbidden:
		if adminOnly {
			return prefix + upperFirst(saved, "your organisation does not allow personal API keys and has not finished setting up its AI. An administrator of your organisation can set it up.")
		}
		return prefix + upperFirst(saved, "your organisation does not allow personal API keys, so your own choice ("+choiceName(s)+") and any key you saved for it are kept but not used. Choose \"Use my organisation's AI\" in Settings to use your organisation's AI.")
	case ReasonProviderRemovedOrganizationAvailable:
		if saved {
			return "AI features are off: " + removedName(s) + " is no longer supported. Your organisation provides AI: choose \"Use my organisation's AI\" in Settings."
		}
		return removedName(s) + " is no longer supported, so it cannot be tested. Your organisation provides AI: choose \"Use my organisation's AI\" in Settings."
	default:
		tail := "Choose another provider in Settings."
		if admin {
			tail = "Choose another provider in Settings, or ask an administrator of your organisation to set up its AI."
		}
		if saved {
			return "AI features are off: " + removedName(s) + " is no longer supported. " + tail
		}
		return removedName(s) + " is no longer supported, so it cannot be tested. " + tail
	}
}

// upperFirst capitalises the sentence when it is not after "AI features are
// off: ".
func upperFirst(saved bool, sentence string) string {
	if saved || sentence == "" {
		return sentence
	}
	return strings.ToUpper(sentence[:1]) + sentence[1:]
}

// choiceName names the caller's own stored choice in row 10's sentence.
func choiceName(s *storage.Settings) string {
	if n, ok := displayNames[s.LLMProvider]; ok {
		return n
	}
	if s.StoredLLMProvider == "ollama" {
		return "Ollama"
	}
	return "a provider Paceday no longer supports"
}

// removedName names the removed provider a row is stored on.
func removedName(s *storage.Settings) string {
	if s != nil && s.StoredLLMProvider == "ollama" {
		return "Ollama"
	}
	return "Your previous AI provider"
}

// OrganizationDestinationRefusal is the refusal of the organisation's
// destination (row 4, P2-AC-41): the same reason and property, with a
// sentence that names the organisation's AI destination and its
// administrator, never the caller's own Settings.
func OrganizationDestinationRefusal(r *DestinationRefusal) *DestinationRefusal {
	out := *r
	out.Message = "Your organisation's AI destination is not approved on this deployment. An administrator of your organisation can fix this."
	return &out
}

func unmarshalLLMResponse(body []byte, v interface{}) error {
	return json.Unmarshal(body, v)
}
