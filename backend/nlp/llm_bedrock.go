package nlp

import (
	"context"
	"fmt"
	"net/url"
)

// BedrockClient calls the Bedrock Runtime Converse operation with an Amazon
// Bedrock API key sent as a bearer token (spec D-3, AC-4). The SigV4 mode
// (AWS default credential chain, shared profiles) is removed (owner decision
// O-3): no AWS SDK, no AWS credential and no AWS_BEARER_TOKEN_BEDROCK from the
// server's environment is ever read.
type BedrockClient struct {
	APIKey  string
	Region  string
	ModelID string
}

// host is the regional runtime host, computed from the stored region only.
func (c *BedrockClient) host() string {
	return "bedrock-runtime." + c.Region + ".amazonaws.com"
}

// requestURL is the Converse URL. The model ID is ONE path segment,
// percent-encoded with url.PathEscape: ':' stays literal, '/' becomes %2F.
func (c *BedrockClient) requestURL() string {
	return "https://" + c.host() + "/model/" + url.PathEscape(c.ModelID) + "/converse"
}

func (c *BedrockClient) Complete(ctx context.Context, system, user string) (string, error) {
	if err := c.CheckDestination(ctx); err != nil {
		return "", err
	}
	payload := map[string]interface{}{
		"system": []map[string]string{{"text": system}},
		"messages": []map[string]interface{}{
			{"role": "user", "content": []map[string]string{{"text": user}}},
		},
		"inferenceConfig": map[string]int{"maxTokens": 1024},
	}
	return postJSONWith(ctx, cloudHTTPClient(ProviderBedrock), c.requestURL(),
		map[string]string{"Authorization": "Bearer " + c.APIKey},
		payload,
		func(body []byte) (string, error) {
			var resp struct {
				Output struct {
					Message struct {
						Content []struct {
							Text string `json:"text"`
						} `json:"content"`
					} `json:"message"`
				} `json:"output"`
			}
			if err := unmarshalLLMResponse(body, &resp); err != nil || len(resp.Output.Message.Content) == 0 {
				return "", fmt.Errorf("bedrock: unexpected response")
			}
			return resp.Output.Message.Content[0].Text, nil
		})
}
