package storage

import (
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

// NEW-llm-bring-your-own (spec §3, D-5, D-10; contract §4, §5): the AI choice
// as the contract reads it, keys bound to the provider they were entered for,
// and the read-only status of the caller's AI.

// LLM choice values as the contract's Settings.llmProvider reads them.
const (
	LLMChoiceOff          = ""
	LLMChoiceOrganization = "organization"
	LLMChoiceUnsupported  = "unsupported"
)

// readableProviders are the stored values read as themselves. Any other
// stored value (ollama, or anything a direct database edit left) reads as
// "unsupported".
var readableProviders = map[string]bool{
	LLMChoiceOff: true, LLMChoiceOrganization: true,
	"openai": true, "anthropic": true, "azure_openai": true, "bedrock": true, "vertex": true,
}

// ReadLLMProvider maps a stored llm_provider to the value the contract reads.
func ReadLLMProvider(stored string) string {
	if readableProviders[stored] {
		return stored
	}
	return LLMChoiceUnsupported
}

// storedProvider is the llm_provider value a save writes: the stored value
// itself while the document still reads "unsupported" (it is never
// rewritten, so a rollback restores it), else the chosen value.
func (s *Settings) storedProvider() string {
	if s.LLMProvider == LLMChoiceUnsupported && s.StoredLLMProvider != "" {
		return s.StoredLLMProvider
	}
	return s.LLMProvider
}

// LLMAPIKeys is one write-only key per provider.
type LLMAPIKeys struct {
	OpenAI      string
	Anthropic   string
	AzureOpenAI string
	Bedrock     string
	Vertex      string
}

func (k *LLMAPIKeys) slot(provider string) *string {
	switch provider {
	case "openai":
		return &k.OpenAI
	case "anthropic":
		return &k.Anthropic
	case "azure_openai":
		return &k.AzureOpenAI
	case "bedrock":
		return &k.Bedrock
	case "vertex":
		return &k.Vertex
	}
	return nil
}

// For returns the key bound to provider, "" when none or provider is not one
// of the five.
func (k LLMAPIKeys) For(provider string) string {
	if p := k.slot(provider); p != nil {
		return *p
	}
	return ""
}

// Set binds key to provider and reports whether provider is one of the five.
func (k *LLMAPIKeys) Set(provider, key string) bool {
	p := k.slot(provider)
	if p == nil {
		return false
	}
	*p = key
	return true
}

// All returns every non-empty key, for redaction.
func (k LLMAPIKeys) All() []string {
	var out []string
	for _, v := range []string{k.OpenAI, k.Anthropic, k.AzureOpenAI, k.Bedrock, k.Vertex} {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// LLMAPIKeysSet is the read-only presence flag per provider (contract
// LLMApiKeysSet).
type LLMAPIKeysSet struct {
	OpenAI      bool `json:"openai"`
	Anthropic   bool `json:"anthropic"`
	AzureOpenAI bool `json:"azure_openai"`
	Bedrock     bool `json:"bedrock"`
	Vertex      bool `json:"vertex"`
}

// RefreshKeyFlags recomputes the read-only key presence flags from the keys.
func (s *Settings) RefreshKeyFlags() {
	k := s.LLMAPIKeys
	s.LLMAPIKeysSet = LLMAPIKeysSet{
		OpenAI: k.OpenAI != "", Anthropic: k.Anthropic != "", AzureOpenAI: k.AzureOpenAI != "",
		Bedrock: k.Bedrock != "", Vertex: k.Vertex != "",
	}
	s.LLMAPIKeySet = k.For(s.LLMProvider) != ""
	s.LLMUnboundAPIKeySet = s.LLMUnboundAPIKey != ""
}

// AINotConfigured is why AI is not configured for a caller and who can change
// that (contract AINotConfigured). It never carries a key.
type AINotConfigured struct {
	Reason   string   `json:"reason"`
	Provider string   `json:"provider,omitempty"`
	Enablers []string `json:"enablers"`
	Message  string   `json:"message"`
}

// AIStatus is the read-only summary on the settings document (contract
// AIStatus): exactly one of Provider and NotConfigured is set.
type AIStatus struct {
	Configured     bool             `json:"configured"`
	Provider       string           `json:"provider,omitempty"`
	// Configuration is "own" (row 2) or "organization" (row 4), present
	// exactly when Configured (PAC-73 Part 2).
	Configuration  string           `json:"configuration,omitempty"`
	InOrganization bool             `json:"inOrganization"`
	NotConfigured  *AINotConfigured `json:"notConfigured,omitempty"`
}

// UserInOrganization reports whether userID belongs to an organisation.
func UserInOrganization(db DBTX, userID uuid.UUID) (bool, error) {
	var in bool
	err := db.QueryRow(`SELECT org_id IS NOT NULL FROM users WHERE id = $1`, userID).Scan(&in)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("UserInOrganization: %w", err)
	}
	return in, nil
}
