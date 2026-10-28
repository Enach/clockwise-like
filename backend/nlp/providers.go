package nlp

// NEW-llm-bring-your-own: the five providers Paceday supports. Each is reached
// only with a key the user entered for it (spec D-2, D-3, D-4); Ollama and
// every ambient cloud identity are gone (owner decisions O-3, O-4).
const (
	ProviderOpenAI      = "openai"
	ProviderAnthropic   = "anthropic"
	ProviderAzureOpenAI = "azure_openai"
	ProviderBedrock     = "bedrock"
	ProviderVertex      = "vertex"
)

// KeyProviders lists the supported providers in the contract's order.
var KeyProviders = []string{ProviderOpenAI, ProviderAnthropic, ProviderAzureOpenAI, ProviderBedrock, ProviderVertex}

// IsKeyProvider reports whether p is one of the five supported providers.
func IsKeyProvider(p string) bool {
	for _, k := range KeyProviders {
		if p == k {
			return true
		}
	}
	return false
}

// displayNames are the names used in sentences for the user.
var displayNames = map[string]string{
	ProviderOpenAI:      "OpenAI",
	ProviderAnthropic:   "Anthropic",
	ProviderAzureOpenAI: "Azure OpenAI",
	ProviderBedrock:     "AWS Bedrock",
	ProviderVertex:      "Google Vertex AI",
}

// Defaults applied when a stored value is empty.
const (
	defaultOpenAIModel     = "gpt-4o-mini"
	defaultAnthropicModel  = "claude-haiku-4-5-20251001"
	defaultAzureAPIVersion = "2024-02-01"
	defaultBedrockRegion   = "us-east-1"
	defaultBedrockModel    = "anthropic.claude-sonnet-4-5-20251001-v1:0"
	defaultVertexModel     = "gemini-2.0-flash-001"
	vertexExpressHost      = "aiplatform.googleapis.com"
)
