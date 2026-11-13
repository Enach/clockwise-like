package config

import (
	"strings"
	"testing"
)

// PAC-97 AC-28: the DNS stand-in for sign-in detection follows the stand-in
// rule (refused without PACEDAY_ALLOW_STANDINS, refused outside
// PermittedPeer, a startup notice naming it when accepted).
func TestPAC97_AC28_DecideMXResolver(t *testing.T) {
	t.Run("unset is production", func(t *testing.T) {
		for _, allow := range []string{"", "true", "false"} {
			d, err := DecideMXResolver("", allow)
			if err != nil || d != (MXResolverDecision{}) {
				t.Errorf("allow=%q: (%+v, %v), want the zero decision", allow, d, err)
			}
		}
	})
	t.Run("refused without the test-mode declaration", func(t *testing.T) {
		for _, allow := range []string{"", "false", "0", "yes", "TRUE "} {
			_, err := DecideMXResolver("10.143.43.10:5353", allow)
			if err == nil || !strings.Contains(err.Error(), EnvAllowStandins) {
				t.Errorf("allow=%q: err = %v, want a refusal naming %s", allow, err, EnvAllowStandins)
			}
		}
	})
	t.Run("refused outside the permitted set or unparseable", func(t *testing.T) {
		for _, addr := range []string{
			"8.8.8.8:53",                // public
			"1.1.1.1:53",                // public
			"[2001:4860:4860::8888]:53", // public IPv6
			"169.254.1.1:53",            // link-local
			"0.0.0.0:53",                // unspecified
			"[fe80::1%eth0]:53",         // zone-scoped
			"fake-google:5353",          // a name, not a literal
			"10.143.43.10",              // no port
			"10.143.43.10:0",            // port 0
			"10.143.43.10:99999",        // bad port
		} {
			if d, err := DecideMXResolver(addr, "true"); err == nil {
				t.Errorf("%q accepted: %+v", addr, d)
			} else if !strings.Contains(err.Error(), EnvMXResolver) {
				t.Errorf("%q: refusal %q does not name %s", addr, err, EnvMXResolver)
			}
		}
	})
	t.Run("accepted with both, with a notice naming it", func(t *testing.T) {
		for _, addr := range []string{"10.143.43.10:5353", "127.0.0.1:53", "192.168.1.2:5300", "[::1]:5353", "[fd00::1]:53"} {
			d, err := DecideMXResolver(addr, "true")
			if err != nil {
				t.Fatalf("%q refused: %v", addr, err)
			}
			if d.Address != addr {
				t.Errorf("%q: Address = %q", addr, d.Address)
			}
			if !strings.Contains(d.Notice, addr) || !strings.Contains(d.Notice, "NOT a production deployment") {
				t.Errorf("%q: notice %q does not name the stand-in", addr, d.Notice)
			}
		}
	})
}
