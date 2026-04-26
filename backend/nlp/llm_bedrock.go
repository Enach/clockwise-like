package nlp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

// BedrockClient calls Claude models via AWS Bedrock. Authentication follows the
// standard AWS credential chain: env vars → ~/.aws/credentials → IAM role → SSO session.
type BedrockClient struct {
	Region  string
	Profile string
	ModelID string
}

func NewBedrockClient(region, profile, modelID string) (LLMClient, error) {
	if region == "" {
		region = "us-east-1"
	}
	if modelID == "" {
		modelID = "anthropic.claude-sonnet-4-5-20251001-v1:0"
	}
	return &BedrockClient{Region: region, Profile: profile, ModelID: modelID}, nil
}

func (c *BedrockClient) Complete(ctx context.Context, system, user string) (string, error) {
	opts := []func(*config.LoadOptions) error{
		config.WithRegion(c.Region),
	}
	if c.Profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(c.Profile))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return "", fmt.Errorf("bedrock: load config: %w", err)
	}

	client := bedrockruntime.NewFromConfig(cfg)

	payload := map[string]interface{}{
		"anthropic_version": "bedrock-2023-05-31",
		"max_tokens":        1024,
		"system":            system,
		"messages": []map[string]string{
			{"role": "user", "content": user},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	out, err := client.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
		ModelId:     aws.String(c.ModelID),
		Body:        body,
		ContentType: aws.String("application/json"),
		Accept:      aws.String("application/json"),
	})
	if err != nil {
		return "", fmt.Errorf("bedrock: invoke: %w", err)
	}

	var resp struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(out.Body, &resp); err != nil || len(resp.Content) == 0 {
		return "", fmt.Errorf("bedrock: unexpected response")
	}
	return resp.Content[0].Text, nil
}
