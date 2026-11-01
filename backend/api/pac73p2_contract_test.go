package api

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// PAC-73 Part 2, P2-AC-34: the assembled contract documents the three new
// operations with every refusal, the purpose on the three step-up
// operations, the personal-keys reason, and no readable organisation key.
// It also checks that every status this branch's handlers answer is one the
// contract documents.

// schemaBlock is the text of one component schema in the bundle.
func schemaBlock(t *testing.T, raw, name string) string {
	t.Helper()
	start := strings.Index(raw, "\n    "+name+":\n")
	if start < 0 {
		t.Fatalf("schema %s is not in the bundle", name)
	}
	rest := raw[start+1:]
	next := regexp.MustCompile(`\n    [A-Za-z0-9_]+:\n`).FindStringIndex(rest[len(name)+6:])
	if next == nil {
		return rest
	}
	return rest[:len(name)+6+next[0]]
}

func TestPAC73P2_ContractDocumentsPart2(t *testing.T) {
	b := parseBundle(t, pac21Bundle)
	for op, codes := range map[[2]string][]string{
		{"/api/organization/ai", "get"}:                    {"200", "401", "403", "500"},
		{"/api/organization/ai", "patch"}:                  {"200", "400", "401", "403", "409", "500"},
		{"/api/organization/ai/consent", "post"}:           {"200", "400", "401", "403", "409", "500"},
		{"/api/admin/sso/stepup/options", "get"}:           {"200", "400", "401", "403"},
		{"/api/admin/sso/stepup", "get"}:                   {"302", "400", "403", "503"},
		{"/api/llm/test", "post"}:                          {"200", "400", "409", "422", "500", "502"},
		{"/api/meetings/{event_id}/brief/refresh", "post"}: {"200", "409"},
	} {
		responses := b.get("paths", op[0], op[1], "responses")
		if responses == nil {
			t.Errorf("%s %s has no responses", op[1], op[0])
			continue
		}
		for _, c := range codes {
			if responses.get(c) == nil {
				t.Errorf("%s %s does not document %s", op[1], op[0], c)
			}
		}
	}
	rawBytes, err := os.ReadFile(pac21Bundle)
	if err != nil {
		t.Fatal(err)
	}
	raw := strings.ReplaceAll(string(rawBytes), "\r\n", "\n")
	if !strings.Contains(schemaBlock(t, raw, "AINotConfigured"), "- personal_keys_forbidden") {
		t.Error("AINotConfigured.reason does not list personal_keys_forbidden")
	}
	if !strings.Contains(schemaBlock(t, raw, "SsoStepupPurpose"), "- llm_org_key_write") {
		t.Error("SsoStepupPurpose does not list llm_org_key_write")
	}
	if n := strings.Count(raw, "      - name: purpose\n"); n != 2 {
		t.Errorf("purpose is a parameter of %d operations, want 2 (start and options)", n)
	}
	if !strings.Contains(raw, `(&purpose=llm_org_key_write)?$`) {
		t.Error("the callback's Location pattern does not carry the purpose")
	}
	for _, name := range []string{"OrganizationAIConfiguration", "OrganizationAIConsentConflict", "AIStatus", "Settings"} {
		if strings.Contains(schemaBlock(t, raw, name), "\n        apiKey:") {
			t.Errorf("readable schema %s carries an apiKey property", name)
		}
	}
	upd := schemaBlock(t, raw, "OrganizationAIConfigurationUpdate")
	if i := strings.Index(upd, "\n        apiKey:"); i < 0 || !strings.Contains(upd[i:i+200], "writeOnly: true") {
		t.Error("OrganizationAIConfigurationUpdate.apiKey is not writeOnly")
	}
	if !strings.Contains(raw, "DELETES the caller's stored confirmation") {
		t.Error("patchSettings does not document that storing off or a provider ends the confirmation")
	}
}
