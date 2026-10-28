package nlp

import (
	"context"
	"fmt"
	"net/url"
)

// VertexClient calls Vertex AI in express mode with the user's API key in the
// x-goog-api-key header, never in the URL (spec D-4, AC-5). Express mode uses
// only the global host and no project or location; the Application Default
// Credentials mode is removed (owner decision O-3).
type VertexClient struct {
	APIKey  string
	ModelID string
}

// requestURL is the express-mode inference URL. The model ID is ONE path
// segment (url.PathEscape); the ':' before generateContent is the method
// separator.
func (c *VertexClient) requestURL() string {
	return "https://" + vertexExpressHost + "/v1/publishers/google/models/" + url.PathEscape(c.ModelID) + ":generateContent"
}

func (c *VertexClient) Complete(ctx context.Context, system, user string) (string, error) {
	if err := c.CheckDestination(ctx); err != nil {
		return "", err
	}
	payload := map[string]interface{}{
		"contents": []map[string]interface{}{
			{"role": "user", "parts": []map[string]string{{"text": user}}},
		},
		"systemInstruction": map[string]interface{}{
			"parts": []map[string]string{{"text": system}},
		},
	}
	return postJSONWith(ctx, cloudHTTPClient(ProviderVertex), c.requestURL(),
		map[string]string{"x-goog-api-key": c.APIKey},
		payload,
		func(body []byte) (string, error) {
			var resp struct {
				Candidates []struct {
					Content struct {
						Parts []struct {
							Text string `json:"text"`
						} `json:"parts"`
					} `json:"content"`
				} `json:"candidates"`
			}
			if err := unmarshalLLMResponse(body, &resp); err != nil || len(resp.Candidates) == 0 {
				return "", fmt.Errorf("vertex: unexpected response")
			}
			parts := resp.Candidates[0].Content.Parts
			if len(parts) == 0 {
				return "", fmt.Errorf("vertex: empty parts")
			}
			return parts[0].Text, nil
		})
}
