package config

import (
	"strings"
	"testing"
)

// PAC-20: cookies carry Secure unless the operator explicitly opts out for
// plain-HTTP development or e2e. Anything but an explicit "true" keeps them
// Secure (fail closed).
func TestDecideSecureCookies(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		wantSecure bool
		wantNotice string // substring; "" means no notice
	}{
		{name: "unset is production: Secure", raw: "", wantSecure: true},
		{name: "explicit false: Secure", raw: "false", wantSecure: true},
		{name: "explicit true: not Secure, warned", raw: "true", wantSecure: false, wantNotice: "without the Secure attribute"},
		{name: "case and spaces tolerated", raw: "  TRUE ", wantSecure: false, wantNotice: "without the Secure attribute"},
		{name: "1 is not an opt-out: Secure, warned", raw: "1", wantSecure: true, wantNotice: "not recognised"},
		{name: "garbage stays Secure, warned", raw: "yes please", wantSecure: true, wantNotice: "not recognised"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(k string) string {
				if k != EnvInsecureCookies {
					t.Fatalf("read unexpected variable %q", k)
				}
				return tc.raw
			}
			secure, notice := DecideSecureCookies(getenv)
			if secure != tc.wantSecure {
				t.Fatalf("secure = %v, want %v", secure, tc.wantSecure)
			}
			if tc.wantNotice == "" && notice != "" {
				t.Fatalf("unexpected notice %q", notice)
			}
			if tc.wantNotice != "" && !strings.Contains(notice, tc.wantNotice) {
				t.Fatalf("notice %q does not contain %q", notice, tc.wantNotice)
			}
			if tc.wantNotice != "" && !strings.Contains(notice, EnvInsecureCookies) {
				t.Fatalf("notice %q does not name %s", notice, EnvInsecureCookies)
			}
		})
	}
}
