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

// loadConfig resolves the AWS configuration for this client. The region is
// applied explicitly, so it overrides AWS_REGION and the shared-config region
// (spec D-7). Loading reads the environment and shared config files only; it
// acquires no credential and sends nothing.
//
// The approved-only HTTP client is deliberately NOT set here: aws.Config's
// HTTPClient is also what the credential chain (IMDS, STS web identity and
// assume-role, ECS, SSO) uses, and those must reach their own endpoints. The
// guard belongs on the inference client only; see Complete.
func (c *BedrockClient) loadConfig(ctx context.Context) (aws.Config, error) {
	d := currentDeps()
	opts := []func(*config.LoadOptions) error{
		config.WithRegion(c.Region),
	}
	if c.Profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(c.Profile))
	}
	if d.AWSCredentials != nil {
		opts = append(opts, config.WithCredentialsProvider(d.AWSCredentials))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("bedrock: load config: %w", err)
	}
	return cfg, nil
}

func (c *BedrockClient) Complete(ctx context.Context, system, user string) (string, error) {
	cfg, err := c.prepare(ctx)
	if err != nil {
		return "", err
	}

	// Only the inference request goes through the approved-only transport.
	client := bedrockruntime.NewFromConfig(cfg, func(o *bedrockruntime.Options) {
		o.HTTPClient = ambientHTTPClient(ProviderBedrock)
	})

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
