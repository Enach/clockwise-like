package config

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// PAC-81 AC-A14: .env.example ships an empty SENTRY_DSN and says that empty
// means monitoring off; AC-A9: it lists the pool settings, empty (defaults).
func TestEnvExamplePAC81(t *testing.T) {
	b, err := os.ReadFile("../../.env.example")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	if !regexp.MustCompile(`(?m)^SENTRY_DSN=$`).MatchString(src) {
		t.Error("SENTRY_DSN is not empty in .env.example")
	}
	if !strings.Contains(src, "monitoring is off until SENTRY_DSN is set") || !strings.Contains(src, `"monitoring disabled"`) {
		t.Error(".env.example does not say that an empty DSN turns monitoring off")
	}
	for _, name := range []string{EnvDBMaxOpenConns, EnvDBMaxIdleConns, EnvDBConnMaxLifetime, EnvDBConnMaxIdleTime} {
		if !regexp.MustCompile(`(?m)^` + name + `=$`).MatchString(src) {
			t.Errorf("%s missing or not empty in .env.example", name)
		}
	}
}
