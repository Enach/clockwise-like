package nlp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// NEW-llm-bring-your-own AC-1 (owner decisions O-2, O-3): no AI-provider code
// uses an identity of the machine Paceday runs on. The scan covers every
// non-test Go file of backend/nlp; it fails on any use of the Azure default
// credential chain, the AWS default configuration loader, Google Application
// Default Credentials, or the SDK packages that provide them.
func TestNoAmbientIdentityInAIProviderCode(t *testing.T) {
	forbidden := []string{
		"NewDefaultAzureCredential",
		"LoadDefaultConfig",
		"FindDefaultCredentials",
		"azidentity",
		"aws-sdk-go-v2/config",
		"golang.org/x/oauth2/google",
		"AWS_BEARER_TOKEN_BEDROCK",
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		scanned++
		for i, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, word := range forbidden {
				if strings.Contains(line, word) {
					t.Errorf("%s:%d uses %s: %s", f, i+1, word, strings.TrimSpace(line))
				}
			}
		}
	}
	if scanned < 5 {
		t.Fatalf("scanned %d files; the scan is not looking at backend/nlp", scanned)
	}
}
