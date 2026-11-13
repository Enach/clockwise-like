package config

import (
	"errors"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/internal/secretbox"
	"github.com/Enach/paceday/backend/internal/secretbox/secretboxtest"
)

const (
	e2eKey  = "e2e-test:cGFjZWRheS1lMmUtdGVzdC1rZXktTk9ULVNFQ1JFVCE="
	e2eKey2 = "e2e-test-2:cGFjZWRheS1lMmUtdGVzdC1rZXktMi1OT1RTRUNSRVQ="
)

func encEnv(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// AC-30 (K-12): either committed e2e key, under any id, as active or
// previous, is refused unless PACEDAY_ENCRYPTION_ALLOW_TEST_KEY is exactly
// "true".
func TestPAC84_CommittedTestKeyRefused(t *testing.T) {
	real := secretboxtest.Key(t)
	renamed := "prod-2026:" + strings.SplitN(e2eKey2, ":", 2)[1]
	for _, c := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"active", map[string]string{"PACEDAY_ENCRYPTION_KEY": e2eKey},
			`PACEDAY_ENCRYPTION_KEY is the committed e2e test key "e2e-test"; generate a real key`},
		{"renamed", map[string]string{"PACEDAY_ENCRYPTION_KEY": renamed},
			`PACEDAY_ENCRYPTION_KEY is the committed e2e test key "prod-2026"; generate a real key`},
		{"previous", map[string]string{"PACEDAY_ENCRYPTION_KEY": real, "PACEDAY_ENCRYPTION_KEY_PREVIOUS": e2eKey2},
			`PACEDAY_ENCRYPTION_KEY_PREVIOUS is the committed e2e test key "e2e-test-2"`},
		{"allow not exactly true", map[string]string{"PACEDAY_ENCRYPTION_KEY": e2eKey, "PACEDAY_ENCRYPTION_ALLOW_TEST_KEY": "TRUE"},
			"committed e2e test key"},
	} {
		_, err := DecideEncryptionKeys(encEnv(c.env))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: %v, want %q", c.name, err, c.want)
		}
		line := EncryptionRefusal("refusing to start: ", err)
		if !strings.HasPrefix(line, "refusing to start: PACEDAY_ENCRYPTION_KEY") || !strings.HasSuffix(line, `(docs/runbook.md, "Encryption key")`) {
			t.Fatalf("%s: refusal line %q", c.name, line)
		}
		if strings.Contains(line, "cGFjZWRheS1lMmUt") {
			t.Fatalf("%s: line echoes key material: %q", c.name, line)
		}
	}
	cfg, err := DecideEncryptionKeys(encEnv(map[string]string{"PACEDAY_ENCRYPTION_KEY": e2eKey, "PACEDAY_ENCRYPTION_KEY_PREVIOUS": e2eKey2, "PACEDAY_ENCRYPTION_ALLOW_TEST_KEY": "true"}))
	if err != nil || cfg.Active.ID != "e2e-test" {
		t.Fatalf("allowed test key: %v", err)
	}
	if _, err := DecideEncryptionKeys(encEnv(map[string]string{"PACEDAY_ENCRYPTION_KEY": real})); err != nil {
		t.Fatalf("real key refused: %v", err)
	}
}

// AC-8, AC-9: the missing and malformed refusals name the variable and the
// runbook section, never key material.
func TestPAC84_RefusalLines(t *testing.T) {
	_, err := DecideEncryptionKeys(encEnv(nil))
	if !errors.Is(err, secretbox.ErrKeyMissing) {
		t.Fatal(err)
	}
	line := EncryptionRefusal("refusing to start: ", err)
	if !strings.HasPrefix(line, "refusing to start: PACEDAY_ENCRYPTION_KEY is not set") || !strings.Contains(line, `docs/runbook.md, "Encryption key"`) {
		t.Fatalf("missing: %q", line)
	}
	_, err = DecideEncryptionKeys(encEnv(map[string]string{"PACEDAY_ENCRYPTION_KEY": "k:c2hvcnQ="}))
	line = EncryptionRefusal("encryption: ", err)
	if line != `encryption: PACEDAY_ENCRYPTION_KEY is malformed: decodes to 5 bytes, need 32 (key "k") (docs/runbook.md, "Encryption key")` {
		t.Fatalf("malformed: %q", line)
	}
}
