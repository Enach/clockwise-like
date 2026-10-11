// Package config turns deployment configuration into decisions. It reads
// nothing itself: main.go hands it raw strings, so every rule here is a pure
// function that a table test can pin down.
package config

import (
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

const (
	// EnvAllowStandins is the test-mode declaration. It counts as declared only
	// when strconv.ParseBool returns true. On its own it grants nothing.
	EnvAllowStandins = "PACEDAY_ALLOW_STANDINS"
	// EnvCalendarBaseURL redirects calendar-provider traffic to a stand-in. It
	// is refused unless EnvAllowStandins is declared.
	EnvCalendarBaseURL = "GOOGLE_CALENDAR_API_BASE_URL"

	// CalendarAPIRoot is the path every calendar address must end in. Requests
	// are resolved relative to the address, so without the trailing slash the
	// v3 segment is silently dropped.
	CalendarAPIRoot = "/calendar/v3/"
	// PublishedCalendarEndpoint is the provider's own address: the library's
	// basePath, and what every production deployment uses.
	PublishedCalendarEndpoint = "https://www.googleapis.com" + CalendarAPIRoot
)

// CalendarEndpoint is the calendar-provider decision for a deployment. Its
// zero value is not a decision; use ProductionCalendarEndpoint.
type CalendarEndpoint struct {
	// BaseURL is the address calendar requests are resolved against.
	BaseURL string
	// Guard turns on the connection-time peer-address guard. It is on exactly
	// when the deployment is redirected to a stand-in.
	Guard bool
	// Notice, when non-empty, must be printed at startup.
	Notice string
}

// ProductionCalendarEndpoint is the decision for a deployment that redirects
// nothing: the published address, guard off, no notice.
func ProductionCalendarEndpoint() CalendarEndpoint {
	return CalendarEndpoint{BaseURL: PublishedCalendarEndpoint}
}

// permittedPrefixes is the fixed set of non-routable ranges a guarded
// deployment may connect to (spec AC-7, OQ-2): loopback, RFC 1918 and IPv6
// unique-local. It is deliberately not configurable; if a stand-in lives
// elsewhere, move the stand-in.
var permittedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("fc00::/7"),
}

// PermittedPeer reports whether a is inside the permitted set. An IPv4-mapped
// IPv6 address is judged as the address it maps to. A zone-scoped address is
// never permitted. Link-local, unspecified and every public address are
// refused. This is the predicate the calendar connection guard applies to
// every connection; it lives here, in the leaf package, so the startup check
// and the guard cannot disagree about the set.
func PermittedPeer(a netip.Addr) bool {
	if !a.IsValid() || a.Zone() != "" {
		return false
	}
	a = a.Unmap()
	for _, p := range permittedPrefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// DecideCalendarEndpoint evaluates the two raw settings.
//
// This is not the security control. It refuses values that cannot be
// understood and literal addresses outside the permitted set, so a confused
// operator learns at startup rather than at the first calendar call. It
// classifies no host by its shape: anything that does not parse as an address
// literal is a name and is accepted, whatever it looks like. Names are judged
// at connection time by the guard in backend/calendar, which is the control.
func DecideCalendarEndpoint(baseURL, allowStandins string) (CalendarEndpoint, error) {
	declared, _ := strconv.ParseBool(allowStandins)
	if baseURL == "" {
		return ProductionCalendarEndpoint(), nil
	}
	if !declared {
		return CalendarEndpoint{}, fmt.Errorf(
			"%s is set but %s is not true: a calendar redirection is only allowed in a deployment that declares test mode; unset %s, or set %s=true if this is not production",
			EnvCalendarBaseURL, EnvAllowStandins, EnvCalendarBaseURL, EnvAllowStandins)
	}

	u, err := url.Parse(baseURL)
	if err != nil {
		return CalendarEndpoint{}, refuse(baseURL, "it cannot be parsed as a URL")
	}
	shown := baseURL
	if u.User != nil {
		// Name the value, but never echo a password into the logs.
		shown = u.Redacted()
		return CalendarEndpoint{}, refuse(shown, "it carries user information; remove it rather than expecting it to be stripped")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return CalendarEndpoint{}, refuse(shown, "the scheme must be http or https")
	}
	if u.Hostname() == "" {
		return CalendarEndpoint{}, refuse(shown, "it has no host")
	}
	if err := checkLiteralHost(u); err != nil {
		return CalendarEndpoint{}, refuse(shown, err.Error())
	}
	if !strings.HasSuffix(u.Path, CalendarAPIRoot) {
		return CalendarEndpoint{}, refuse(shown, fmt.Sprintf("its path must end in %q, trailing slash included", CalendarAPIRoot))
	}

	return CalendarEndpoint{
		BaseURL: baseURL,
		Guard:   true,
		Notice: fmt.Sprintf(
			"calendar traffic is redirected to %s (%s=true): this is NOT a production deployment",
			baseURL, EnvAllowStandins),
	}, nil
}

// checkLiteralHost refuses a host that is an address literal outside the
// permitted set, or a zone-scoped literal. A host that is not a literal is a
// name and passes.
func checkLiteralHost(u *url.URL) error {
	host := u.Hostname()
	bracketed := strings.HasPrefix(u.Host, "[")
	a, err := netip.ParseAddr(host)
	if err != nil {
		if !bracketed && strings.Contains(host, ":") {
			return fmt.Errorf("the host %q is malformed", host)
		}
		if bracketed {
			return fmt.Errorf("%q is in brackets but is not an IPv6 address", host)
		}
		return nil // a name: judged at connection time
	}
	if a.Zone() != "" {
		return fmt.Errorf("the zone-scoped address %q cannot be understood here", host)
	}
	if !PermittedPeer(a) {
		return fmt.Errorf("the address %s is outside the permitted ranges (loopback, RFC 1918, IPv6 unique-local)", host)
	}
	return nil
}

func refuse(value, why string) error {
	return fmt.Errorf("%s=%q refused: %s", EnvCalendarBaseURL, value, why)
}
