package nlp

import (
	"context"
	"fmt"
	"strings"
)

// AzureOpenAIClient calls an Azure OpenAI deployment with the user's API key
// in the api-key header (spec D-2, AC-3). The Entra ID / default credential
// chain mode is removed (owner decision O-3): no Authorization header is
// sent and no identity of the machine Paceday runs on is read.
type AzureOpenAIClient struct {
	APIKey     string
	Endpoint   string
	Deployment string
	APIVersion string
}

// requestURL is the inference URL. The appended text starts with "/", so the
// URL's authority is always the stored endpoint's.
func (c *AzureOpenAIClient) requestURL() string {
	return fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=%s",
		strings.TrimRight(c.Endpoint, "/"), c.Deployment, c.APIVersion)
}

func (c *AzureOpenAIClient) Complete(ctx context.Context, system, user string) (string, error) {
	if err := c.CheckDestination(ctx); err != nil {
		return "", err
	}
	payload := map[string]interface{}{
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"max_tokens": 1024,
	}
	return postJSONWith(ctx, cloudHTTPClient(ProviderAzureOpenAI), c.requestURL(),
		map[string]string{"api-key": c.APIKey},
		payload,
		func(body []byte) (string, error) {
			var resp struct {
				Choices []struct {
					Message struct{ Content string } `json:"message"`
				} `json:"choices"`
			}
			if err := unmarshalLLMResponse(body, &resp); err != nil || len(resp.Choices) == 0 {
				return "", fmt.Errorf("azure: unexpected response")
			}
			return resp.Choices[0].Message.Content, nil
		})
}
