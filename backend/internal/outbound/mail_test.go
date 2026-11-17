package outbound

import (
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// PAC-122 SF-066: email off, or invite links defaulting to paceday.app, is
// said once at startup; a full configuration says nothing.
func TestPAC122_SF066_MailNotices(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want []string // substrings of the single notice; nil means none
	}{
		{"nothing set", map[string]string{}, []string{"email is off", "SMTP_HOST and SMTP_FROM not set"}},
		{"host only", map[string]string{"SMTP_HOST": "smtp.example.test"}, []string{"email is off", "(SMTP_FROM not set)"}},
		{"from only", map[string]string{"SMTP_FROM": "noreply@example.test"}, []string{"email is off", "(SMTP_HOST not set)"}},
		{"on, no BASE_URL", map[string]string{"SMTP_HOST": "h", "SMTP_FROM": "f"}, []string{"BASE_URL is not set", "https://paceday.app"}},
		{"fully configured", map[string]string{"SMTP_HOST": "h", "SMTP_FROM": "f", "BASE_URL": "https://app.example.test"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MailNotices(env(tc.env))
			if tc.want == nil {
				if len(got) != 0 {
					t.Fatalf("notices %q, want none", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("notices %q, want one", got)
			}
			for _, w := range tc.want {
				if !strings.Contains(got[0], w) {
					t.Fatalf("notice %q lacks %q", got[0], w)
				}
			}
			if MailConfigured(env(tc.env)) != (tc.env["SMTP_HOST"] != "" && tc.env["SMTP_FROM"] != "") {
				t.Fatal("MailConfigured disagrees with the senders' test")
			}
		})
	}
}
