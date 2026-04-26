package nlp

import (
	"encoding/json"
	"fmt"

	"github.com/Enach/clockwise-like/backend/storage"
)

// NewLLMClientFromSettings creates the appropriate LLM client based on the configured provider.
// SSO providers (bedrock, azure_openai, vertex) require ambient credentials — no API keys stored.
func NewLLMClientFromSettings(s *storage.Settings) (LLMClient, error) {
	switch s.LLMProvider {
	case "bedrock":
		return NewBedrockClient(s.AWSRegion, s.AWSProfile, s.BedrockModel)
	case "azure_openai":
		return NewAzureOpenAIClient(s.AzureEndpoint, s.AzureDeployment, s.AzureAPIVersion)
	case "vertex":
		return NewVertexClient(s.GCPProject, s.GCPLocation, s.VertexModel)
	case "openai":
		model := s.LLMModel
		if model == "" {
			model = "gpt-4o-mini"
		}
		return &OpenAIClient{APIKey: s.LLMAPIKey, Model: model, BaseURL: s.LLMBaseURL}, nil
	case "anthropic":
		model := s.LLMModel
		if model == "" {
			model = "claude-haiku-4-5-20251001"
		}
		return &AnthropicClient{APIKey: s.LLMAPIKey, Model: model, BaseURL: s.LLMBaseURL}, nil
	default: // "ollama" or empty
		baseURL := s.OllamaBaseURL
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		model := s.OllamaModel
		if model == "" {
			model = "llama3"
		}
		return &OllamaClient{BaseURL: baseURL, Model: model}, nil
	}
}

func unmarshalLLMResponse(body []byte, v interface{}) error {
	return json.Unmarshal(body, v)
}

func newLLMError(provider, msg string) error {
	return fmt.Errorf("%s: %s", provider, msg)
}
