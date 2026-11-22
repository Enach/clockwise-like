package config

import (
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// Calendar-feed egress (security audit of 2026-11-22, SSRF via the stored
// webcal feed URL). A webcal feed URL is supplied by an authenticated user
// and stored, and the backend fetches it: once on demand through
// /api/personal-calendars/{id}/preview, and then every 30 minutes through the
// personal-blocker cron. Until this decision existed, that made any address
// reachable from the deployment network fetchable indefinitely by any user.
//
// Two layers close it, and both read the decision below:
//
//  1. the write check (CheckFeedURL, through calendar.WebcalURLProblem), so a
//     hostile URL never reaches storage;
//  2. the egress guard (the dialler Control in backend/calendar), which judges
//     the literal address of every connection, so a name that resolves to a
//     public address when it is saved and to a private one when it is fetched
//     (DNS rebinding) is still refused.
//
// Layer 1 is a courtesy to the user and a defence in depth; layer 2 is the
// control.

// FeedEgress is a deployment's calendar-feed egress decision. Its zero value
// is production: nothing but a public address may be fetched.
type FeedEgress struct {
	// AllowPrivate lets feeds be fetched from loopback and private
	// addresses. It is true only in a deployment that declares test mode,
	// which is how the e2e and test stacks reach their containers.
	AllowPrivate bool
	// Notice, when non-empty, must be printed at startup.
	Notice string
}

// DecideFeedEgress evaluates the test-mode declaration. Unset, or anything
// strconv.ParseBool does not read as true, is production (fail closed). There
// is no variable of its own: a deployment that may fetch a feed from its own
// network is a test deployment, and that is said once, in one place
// (EnvAllowStandins), for every stand-in.
func DecideFeedEgress(allowStandins string) FeedEgress {
	if declared, _ := strconv.ParseBool(allowStandins); !declared {
		return FeedEgress{}
	}
	return FeedEgress{
		AllowPrivate: true,
		Notice: fmt.Sprintf(
			"calendar feeds may be fetched from loopback and private addresses (%s=true): this is NOT a production deployment",
			EnvAllowStandins),
	}
}

// unroutableFeedPrefixes are ranges a feed fetch must not reach even though
// netip reports them as ordinary unicast. The NAT64, 6to4 and Teredo ranges
// are here because each carries an IPv4 destination inside an IPv6 address,
// so a private address can be reached through one without ever appearing as
// an IPv4 address.
var unroutableFeedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),      // RFC 6598 carrier NAT
	netip.MustParsePrefix("192.0.0.0/24"),       // RFC 6890 IETF protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"),      // RFC 2544 benchmarking
	netip.MustParsePrefix("192.0.2.0/24"),       // RFC 5737 documentation
	netip.MustParsePrefix("198.51.100.0/24"),    // RFC 5737 documentation
	netip.MustParsePrefix("203.0.113.0/24"),     // RFC 5737 documentation
	netip.MustParsePrefix("255.255.255.255/32"), // IPv4 broadcast
	netip.MustParsePrefix("64:ff9b::/96"),       // RFC 6052 NAT64
	netip.MustParsePrefix("64:ff9b:1::/48"),     // RFC 8215 local-use NAT64
	netip.MustParsePrefix("2002::/16"),          // RFC 3056 6to4
	netip.MustParsePrefix("2001::/32"),          // RFC 4380 Teredo
	netip.MustParsePrefix("100::/64"),           // RFC 6666 discard-only
	netip.MustParsePrefix("2001:db8::/32"),      // RFC 3849 documentation
}

// PublicFeedPeer reports whether a is an address a calendar feed may be
// fetched from. It enumerates what is allowed rather than what is not: an
// IPv4-mapped IPv6 address is judged as the address it maps to, and anything
// that cannot be judged — invalid, zone-scoped, loopback, unspecified,
// link-local (169.254.169.254 included), multicast, RFC 1918, IPv6
// unique-local, or inside unroutableFeedPrefixes — is refused.
//
// This is the predicate the feed egress guard applies to every connection.
// It lives here, in the leaf package, so the write check and the guard cannot
// disagree about the set.
func PublicFeedPeer(a netip.Addr) bool {
	if !a.IsValid() || a.Zone() != "" {
		return false
	}
	a = a.Unmap()
	switch {
	case a.IsUnspecified(), a.IsLoopback(), a.IsPrivate(),
		a.IsLinkLocalUnicast(), a.IsLinkLocalMulticast(),
		a.IsMulticast(), a.IsInterfaceLocalMulticast():
		return false
	}
	for _, p := range unroutableFeedPrefixes {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// FeedURLFault is what is wrong with a feed URL. The caller turns it into a
// message naming the property it came from.
type FeedURLFault int

const (
	// FeedURLOK means nothing is wrong.
	FeedURLOK FeedURLFault = iota
	// FeedURLNotFetchable: it does not parse, carries credentials, has no
	// host, or does not name a scheme a feed is served over.
	FeedURLNotFetchable
	// FeedURLNotPublic: its host is, or can only be, an address this
	// deployment must not reach.
	FeedURLNotPublic
)

// feedSchemes are the schemes a calendar feed is published under. webcal and
// webcals are the forms calendar applications hand out and are what the
// frontend offers; they are judged exactly like http and https, so the scheme
// can never be used to slip a host past the host rule.
var feedSchemes = map[string]bool{"http": true, "https": true, "webcal": true, "webcals": true}

// CheckFeedURL judges raw as a calendar-feed URL. "" is always acceptable: it
// means no feed is configured.
//
// A host that is an address literal is judged by PublicFeedPeer. A host that
// is a name is judged by its shape only — at least two labels and a final
// label that contains a letter, which rejects "localhost", a single-label
// intranet name and an address written as a decimal integer, but accepts any
// ordinary name whatever it points at. That is deliberate: a name is judged
// for real at connection time by the egress guard, which is the control. This
// check exists so a hostile value is refused at the point the user can still
// correct it, and so it never reaches storage.
//
// allowPrivate is FeedEgress.AllowPrivate: in a deployment that declares test
// mode any address literal and any name shape is accepted, so a test stack
// can point a feed at a container.
func CheckFeedURL(raw string, allowPrivate bool) FeedURLFault {
	if raw == "" {
		return FeedURLOK
	}
	u, err := url.Parse(raw)
	if err != nil {
		return FeedURLNotFetchable
	}
	if !feedSchemes[strings.ToLower(u.Scheme)] || u.Opaque != "" || u.User != nil {
		return FeedURLNotFetchable
	}
	host := u.Hostname()
	if host == "" {
		return FeedURLNotFetchable
	}
	bracketed := strings.HasPrefix(u.Host, "[")
	a, err := netip.ParseAddr(host)
	if err != nil {
		if bracketed || strings.Contains(host, ":") {
			// In brackets but not an IPv6 address, or a malformed host.
			return FeedURLNotFetchable
		}
		if !feedHostName(host, allowPrivate) {
			return FeedURLNotPublic
		}
		return FeedURLOK
	}
	if a.Zone() != "" {
		return FeedURLNotPublic
	}
	if !allowPrivate && !PublicFeedPeer(a) {
		return FeedURLNotPublic
	}
	return FeedURLOK
}

// feedHostName judges the shape of a host that is a name. With allowPrivate
// a single-label container name ("fake-webcal") is accepted too.
func feedHostName(host string, allowPrivate bool) bool {
	labels := strings.Split(strings.TrimSuffix(strings.ToLower(host), "."), ".")
	for _, l := range labels {
		if !ValidDNSLabel(l) {
			return false
		}
	}
	if allowPrivate {
		return true
	}
	if len(labels) < 2 {
		return false
	}
	last := labels[len(labels)-1]
	// RFC 6761 §6.3: localhost, and any name under it, is loopback by
	// definition. A final label with no letter is not a top-level domain
	// (RFC 3696 §2), which is how "2130706433" — 127.0.0.1 as a decimal
	// integer, resolved as such by getaddrinfo — is refused.
	if last == "localhost" || !strings.ContainsFunc(last, isASCIILetter) {
		return false
	}
	return true
}

func isASCIILetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}
