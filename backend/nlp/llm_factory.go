package nlp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Enach/paceday/backend/storage"
)

// NewLLMClientFromSettings creates the appropriate LLM client based on the configured provider.
// SSO providers (bedrock, azure_openai, vertex) use ambient credentials — no API keys stored.
// API-key providers (openai, anthropic) require a non-empty LLMAPIKey.
func NewLLMClientFromSettings(s *storage.Settings) (LLMClient, error) {
	switch s.LLMProvider {
	case "bedrock":
		return NewBedrockClient(s.AWSRegion, s.AWSProfile, s.BedrockModel)
	case "azure_openai":
		return NewAzureOpenAIClient(s.AzureEndpoint, s.AzureDeployment, s.AzureAPIVersion)
	case "vertex":
		return NewVertexClient(s.GCPProject, s.GCPLocation, s.VertexModel)
	case "openai":
		if s.LLMAPIKey == "" {
			// PAC-66-B: a refused destination is reported whether or not a
			// key is on file (spec §B4).
			if r := (&OpenAIClient{BaseURL: s.LLMBaseURL}).CheckDestination(context.Background()); r != nil {
				return nil, r
			}
			return nil, fmt.Errorf("LLM not configured — set API key in Settings")
		}
		model := s.LLMModel
		if model == "" {
			model = "gpt-4o-mini"
		}
		return &OpenAIClient{APIKey: s.LLMAPIKey, Model: model, BaseURL: s.LLMBaseURL}, nil
	case "anthropic":
		if s.LLMAPIKey == "" {
			if r := (&AnthropicClient{BaseURL: s.LLMBaseURL}).CheckDestination(context.Background()); r != nil {
				return nil, r
			}
			return nil, fmt.Errorf("LLM not configured — set API key in Settings")
		}
		model := s.LLMModel
		if model == "" {
			model = "claude-haiku-4-5-20251001"
		}
		return &AnthropicClient{APIKey: s.LLMAPIKey, Model: model, BaseURL: s.LLMBaseURL}, nil
	case "ollama":
		// Fallback order unchanged (PAC-66-B D-B9); the property a refusal
		// names is the one the effective destination came from.
		baseURL, property := s.OllamaBaseURL, "ollamaBaseUrl"
		if baseURL == "" && s.LLMBaseURL != "" {
			baseURL, property = s.LLMBaseURL, "llmBaseUrl" // fallback to legacy field
		}
		if baseURL == "" {
			baseURL = ollamaDefaultBaseURL
		}
		model := s.OllamaModel
		if model == "" {
			model = s.LLMModel
		}
		if model == "" {
			model = "llama3.2"
		}
		return &OllamaClient{BaseURL: baseURL, Model: model, baseURLProperty: property}, nil
	default:
		return nil, fmt.Errorf("LLM not configured — set API key in Settings")
	}
}

func unmarshalLLMResponse(body []byte, v interface{}) error {
	return json.Unmarshal(body, v)
}
