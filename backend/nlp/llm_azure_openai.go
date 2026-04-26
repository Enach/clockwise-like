package nlp

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// AzureOpenAIClient calls Azure OpenAI deployments via Azure AD / Entra ID SSO.
// Auth follows the default Azure credential chain: env vars → managed identity → az login.
type AzureOpenAIClient struct {
	Endpoint   string
	Deployment string
	APIVersion string
}

func NewAzureOpenAIClient(endpoint, deployment, apiVersion string) (LLMClient, error) {
	if apiVersion == "" {
		apiVersion = "2024-02-01"
	}
	return &AzureOpenAIClient{Endpoint: endpoint, Deployment: deployment, APIVersion: apiVersion}, nil
}

func (c *AzureOpenAIClient) Complete(ctx context.Context, system, user string) (string, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return "", fmt.Errorf("azure: credential: %w", err)
	}
	tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://cognitiveservices.azure.com/.default"},
	})
	if err != nil {
		return "", fmt.Errorf("azure: get token: %w", err)
	}

	apiURL := fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=%s",
		strings.TrimRight(c.Endpoint, "/"), c.Deployment, c.APIVersion)

	payload := map[string]interface{}{
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"max_tokens": 1024,
	}
	return postJSON(ctx, apiURL,
		map[string]string{"Authorization": "Bearer " + tok.Token},
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
