package nlp

import (
	"encoding/json"
	"fmt"

	"github.com/Enach/clockwise-like/backend/storage"
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
			return nil, fmt.Errorf("LLM not configured — set API key in Settings")
		}
		model := s.LLMModel
		if model == "" {
			model = "gpt-4o-mini"
		}
		return &OpenAIClient{APIKey: s.LLMAPIKey, Model: model, BaseURL: s.LLMBaseURL}, nil
	case "anthropic":
		if s.LLMAPIKey == "" {
			return nil, fmt.Errorf("LLM not configured — set API key in Settings")
		}
		model := s.LLMModel
		if model == "" {
			model = "claude-haiku-4-5-20251001"
		}
		return &AnthropicClient{APIKey: s.LLMAPIKey, Model: model, BaseURL: s.LLMBaseURL}, nil
	case "ollama":
		baseURL := s.OllamaBaseURL
		if baseURL == "" {
			baseURL = s.LLMBaseURL // fallback to legacy field
		}
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		model := s.OllamaModel
		if model == "" {
			model = s.LLMModel
		}
		if model == "" {
			model = "llama3.2"
		}
		return &OllamaClient{BaseURL: baseURL, Model: model}, nil
	default:
		return nil, fmt.Errorf("LLM not configured — set API key in Settings")
	}
}

func unmarshalLLMResponse(body []byte, v interface{}) error {
	return json.Unmarshal(body, v)
}
