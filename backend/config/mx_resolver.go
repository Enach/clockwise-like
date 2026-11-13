package config

import (
	"fmt"
	"net/netip"
	"strconv"
)

// PAC-97 (spec docs/specs/PAC-97.md D-9, AC-28): sign-in detection reads the
// domain's MX records through the system resolver. A test deployment may send
// those queries to a DNS stand-in instead, under the same rule as every other
// stand-in: only with the test-mode declaration, only to a permitted address,
// and never silently.

// EnvMXResolver names the DNS stand-in (a literal ip:port) that sign-in
// detection queries instead of the system resolver. It is refused unless
// EnvAllowStandins is declared.
const EnvMXResolver = "PACEDAY_MX_RESOLVER"

// MXResolverDecision is the sign-in detection resolver for a deployment. Its
// zero value is production: the system resolver, no notice.
type MXResolverDecision struct {
	// Address is the stand-in's ip:port; empty means the system resolver.
	Address string
	// Notice, when non-empty, must be printed at startup.
	Notice string
}

// DecideMXResolver evaluates the raw settings. Unset is production. Set, it
// is refused without the test-mode declaration, unless it is a literal
// ip:port (a name would itself need resolving), and unless the address is
// inside the permitted set (PermittedPeer).
func DecideMXResolver(addr, allowStandins string) (MXResolverDecision, error) {
	if addr == "" {
		return MXResolverDecision{}, nil
	}
	if declared, _ := strconv.ParseBool(allowStandins); !declared {
		return MXResolverDecision{}, fmt.Errorf(
			"%s is set but %s is not true: a DNS stand-in for sign-in detection is only allowed in a deployment that declares test mode; unset %s, or set %s=true if this is not production",
			EnvMXResolver, EnvAllowStandins, EnvMXResolver, EnvAllowStandins)
	}
	ap, err := netip.ParseAddrPort(addr)
	if err != nil {
		return MXResolverDecision{}, fmt.Errorf("%s=%q refused: it must be a literal ip:port", EnvMXResolver, addr)
	}
	if ap.Port() == 0 {
		return MXResolverDecision{}, fmt.Errorf("%s=%q refused: the port must not be 0", EnvMXResolver, addr)
	}
	if !PermittedPeer(ap.Addr()) {
		return MXResolverDecision{}, fmt.Errorf(
			"%s=%q refused: the address %s is outside the permitted ranges (loopback, RFC 1918, IPv6 unique-local)",
			EnvMXResolver, addr, ap.Addr())
	}
	return MXResolverDecision{
		Address: ap.String(),
		Notice: fmt.Sprintf(
			"sign-in detection queries MX records at the DNS stand-in %s (%s=true): this is NOT a production deployment",
			ap.String(), EnvAllowStandins),
	}, nil
}
