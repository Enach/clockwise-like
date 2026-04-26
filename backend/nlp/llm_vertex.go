package nlp

import (
	"context"
	"fmt"

	"golang.org/x/oauth2/google"
)

// VertexClient calls Google Vertex AI generative models.
// Auth follows Application Default Credentials: GOOGLE_APPLICATION_CREDENTIALS env var →
// ~/.config/gcloud ADC → Workload Identity (GKE/Cloud Run).
type VertexClient struct {
	Project  string
	Location string
	ModelID  string
}

func NewVertexClient(project, location, modelID string) (LLMClient, error) {
	if location == "" {
		location = "us-central1"
	}
	if modelID == "" {
		modelID = "gemini-2.0-flash-001"
	}
	return &VertexClient{Project: project, Location: location, ModelID: modelID}, nil
}

func (c *VertexClient) Complete(ctx context.Context, system, user string) (string, error) {
	creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return "", fmt.Errorf("vertex: find credentials: %w", err)
	}
	tok, err := creds.TokenSource.Token()
	if err != nil {
		return "", fmt.Errorf("vertex: get token: %w", err)
	}

	apiURL := fmt.Sprintf(
		"https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google/models/%s:generateContent",
		c.Location, c.Project, c.Location, c.ModelID,
	)

	payload := map[string]interface{}{
		"contents": []map[string]interface{}{
			{"role": "user", "parts": []map[string]string{{"text": user}}},
		},
		"systemInstruction": map[string]interface{}{
			"parts": []map[string]string{{"text": system}},
		},
	}
	return postJSON(ctx, apiURL,
		map[string]string{"Authorization": "Bearer " + tok.AccessToken},
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
				return "", fmt.Errorf("vertex: unexpected response: %s", string(body))
			}
			parts := resp.Candidates[0].Content.Parts
			if len(parts) == 0 {
				return "", fmt.Errorf("vertex: empty parts")
			}
			return parts[0].Text, nil
		})
}
