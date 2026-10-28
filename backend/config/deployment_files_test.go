package config

import (
	"os"
	"strings"
	"testing"
)

// NEW-llm-bring-your-own AC-20: the deployment files neither mention Ollama
// nor any ambient cloud identity, and .env.example says that Paceday supplies
// no LLM credential. The files are read from the repository root, which
// scripts/test-backend.sh mounts (backend/ is the working directory).
func TestDeploymentFilesOfferNoOllamaAndNoAmbientIdentity(t *testing.T) { // AC-20
	forbidden := []string{
		"ollama",
		"cloud identity",
		"managed identity",
		"instance role",
		"service account",
		"defaultazurecredential",
		"application default credentials",
		"google_application_credentials",
		"aws_profile",
		"aws_access_key_id",
		"aws_bearer_token_bedrock=",
		"openai_api_key=",
		"anthropic_api_key=",
	}
	files := map[string]string{}
	for _, f := range []string{"../../docker-compose.yml", "../../docker-compose.prod.yml", "../../e2e/docker-compose.e2e.yml", "../../.env.example"} {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		files[f] = string(raw)
		for i, line := range strings.Split(string(raw), "\n") {
			l := strings.ToLower(line)
			for _, word := range forbidden {
				if strings.Contains(l, word) {
					t.Errorf("%s:%d mentions %q: %s", f, i+1, word, strings.TrimSpace(line))
				}
			}
		}
	}
	if !strings.Contains(files["../../.env.example"], "PACEDAY SUPPLIES NO LLM CREDENTIAL") {
		t.Error(".env.example does not state that Paceday supplies no LLM credential")
	}
}
