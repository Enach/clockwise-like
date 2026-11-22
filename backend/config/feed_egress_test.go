package config

import (
	"net/netip"
	"strings"
	"testing"
)

// Calendar-feed egress (security audit of 2026-11-22: SSRF via the stored
// webcal feed URL). PublicFeedPeer is the predicate the egress guard applies
// to every connection, and CheckFeedURL the write check both write paths
// share; both are pure, so a table pins them down.

func TestPublicFeedPeer_Table(t *testing.T) {
	refused := []string{
		// Loopback, in every spelling.
		"127.0.0.1", "127.8.9.10", "::1", "::ffff:127.0.0.1",
		// RFC 1918 and IPv6 unique-local.
		"10.0.0.5", "172.16.0.1", "172.31.255.255", "192.168.1.1", "fd00::1", "fc00::1",
		// An IPv4-mapped private address is judged as what it maps to.
		"::ffff:10.0.0.5", "::ffff:192.168.0.1",
		// Link-local: the cloud metadata address and its IPv6 equivalents.
		"169.254.169.254", "169.254.0.1", "fe80::1", "ff02::1",
		// Unspecified, multicast, broadcast.
		"0.0.0.0", "::", "224.0.0.1", "239.255.255.250", "ff00::1", "ff01::1", "255.255.255.255",
		// Ranges that are not a feed's home even though they are unicast.
		"100.64.0.1", "192.0.0.170", "198.18.0.1", "192.0.2.1", "198.51.100.1", "203.0.113.1",
		// IPv6 transition ranges, each carrying an IPv4 destination.
		"64:ff9b::a00:5", "64:ff9b:1::1", "2002:c0a8:101::1", "2001::1", "100::1",
		// Documentation.
		"2001:db8::1",
	}
	for _, s := range refused {
		a, err := netip.ParseAddr(s)
		if err != nil {
			t.Fatalf("test address %q: %v", s, err)
		}
		if PublicFeedPeer(a) {
			t.Errorf("PublicFeedPeer(%s) = true, want false", s)
		}
	}
	for _, s := range []string{"142.250.185.78", "93.184.216.34", "8.8.8.8", "2a00:1450:4007:80e::200a", "2606:4700::1"} {
		a, err := netip.ParseAddr(s)
		if err != nil {
			t.Fatalf("test address %q: %v", s, err)
		}
		if !PublicFeedPeer(a) {
			t.Errorf("PublicFeedPeer(%s) = false, want true", s)
		}
	}
	// A zone-scoped address and an invalid one cannot be judged.
	zoned, err := netip.ParseAddr("fe80::1%eth0")
	if err != nil {
		t.Fatal(err)
	}
	if PublicFeedPeer(zoned) {
		t.Error("PublicFeedPeer accepts a zone-scoped address")
	}
	if PublicFeedPeer(netip.Addr{}) {
		t.Error("PublicFeedPeer accepts the zero Addr")
	}
}

// PermittedPeer (the stand-in guard of PAC-43) and PublicFeedPeer are
// deliberately opposite: a feed may go exactly where a stand-in may not.
// Nothing may be in both sets, which is what makes "fail closed" mean the
// same thing in both guards.
func TestFeedAndStandInSetsDoNotOverlap(t *testing.T) {
	for _, s := range []string{
		"127.0.0.1", "::1", "10.0.0.5", "172.16.0.1", "192.168.1.1", "fd00::1",
		"142.250.185.78", "8.8.8.8", "169.254.169.254", "0.0.0.0",
	} {
		a := netip.MustParseAddr(s)
		if PermittedPeer(a) && PublicFeedPeer(a) {
			t.Errorf("%s is both a permitted stand-in peer and a public feed peer", s)
		}
	}
}

func TestCheckFeedURL_Table(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want FeedURLFault
	}{
		// Acceptable: a public host, under any scheme a feed is published
		// with. A name is accepted whatever it points at — the guard judges
		// it at connection time.
		{"", FeedURLOK},
		{"https://calendar.google.com/calendar/ical/x/basic.ics", FeedURLOK},
		{"http://feeds.example.com/team.ics", FeedURLOK},
		{"webcal://example.com/family.ics", FeedURLOK},
		{"webcals://example.com/family.ics", FeedURLOK},
		{"WEBCAL://Example.COM/family.ics", FeedURLOK},
		{"https://feeds.example.com.:8443/team.ics", FeedURLOK}, // fully qualified, explicit port
		{"https://142.250.185.78/team.ics", FeedURLOK},          // a public literal
		{"https://xn--bcher-kva.example/f.ics", FeedURLOK},      // punycode

		// Not fetchable: the scheme, credentials, or nothing to resolve.
		{"file:///etc/passwd", FeedURLNotFetchable},
		{"gopher://10.0.0.5:9200/_", FeedURLNotFetchable},
		{"ftp://example.com/f.ics", FeedURLNotFetchable},
		{"jar:https://example.com/f.ics", FeedURLNotFetchable},
		{"https://user:pw@example.com/f.ics", FeedURLNotFetchable},
		{"http://feeds.example.com@10.0.0.5/feed.ics", FeedURLNotFetchable},
		{"https:///f.ics", FeedURLNotFetchable},
		{"/feed.ics", FeedURLNotFetchable},
		{"not a url at all", FeedURLNotFetchable},
		{"http://10.0.0.5\x00.example.com/f.ics", FeedURLNotFetchable},
		{"http://[10.0.0.5]/f.ics", FeedURLNotFetchable}, // bracketed, not IPv6
		{"http://[::1/f.ics", FeedURLNotFetchable},       // unclosed bracket

		// Not public: the host is, or can only be, somewhere this deployment
		// must not reach.
		{"http://10.0.0.5:9200/_cluster/health", FeedURLNotPublic},
		{"http://169.254.169.254/latest/meta-data/", FeedURLNotPublic},
		{"http://127.0.0.1:8080/feed.ics", FeedURLNotPublic},
		{"https://[::1]/feed.ics", FeedURLNotPublic},
		{"http://[fd00::1]/feed.ics", FeedURLNotPublic},
		{"http://[::ffff:10.0.0.5]/feed.ics", FeedURLNotPublic},
		{"webcal://100.64.0.1/feed.ics", FeedURLNotPublic},
		{"http://0.0.0.0/feed.ics", FeedURLNotPublic},
		{"http://[fe80::1%25eth0]/feed.ics", FeedURLNotPublic},
		{"http://localhost:8080/feed.ics", FeedURLNotPublic},
		{"http://LOCALHOST/feed.ics", FeedURLNotPublic},
		{"http://feeds.localhost/feed.ics", FeedURLNotPublic},
		{"http://intranet/feed.ics", FeedURLNotPublic},        // single label
		{"http://2130706433/feed.ics", FeedURLNotPublic},      // 127.0.0.1 as a decimal integer
		{"http://192.168.1/feed.ics", FeedURLNotPublic},       // numeric final label
		{"http://-feeds.example.com/f.ics", FeedURLNotPublic}, // not a valid label
	} {
		if got := CheckFeedURL(tc.raw, false); got != tc.want {
			t.Errorf("CheckFeedURL(%q, false) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

// With the test-mode declaration the address and name-shape rules relax, so a
// test stack can point a feed at a container; the scheme and credential rules
// do not.
func TestCheckFeedURL_TestMode(t *testing.T) {
	for _, raw := range []string{
		"http://10.143.43.10:8080/feed.ics",
		"http://127.0.0.1:8080/feed.ics",
		"http://fake-webcal:8080/feed.ics",
		"http://localhost:8080/feed.ics",
	} {
		if got := CheckFeedURL(raw, true); got != FeedURLOK {
			t.Errorf("CheckFeedURL(%q, true) = %v, want FeedURLOK", raw, got)
		}
	}
	for _, tc := range []struct {
		raw  string
		want FeedURLFault
	}{
		{"file:///etc/passwd", FeedURLNotFetchable},
		{"https://user:pw@10.0.0.5/f.ics", FeedURLNotFetchable},
		{"http://[fe80::1%25eth0]/feed.ics", FeedURLNotPublic},
	} {
		if got := CheckFeedURL(tc.raw, true); got != tc.want {
			t.Errorf("CheckFeedURL(%q, true) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

// DecideFeedEgress: production unless test mode is declared, and never
// silently.
func TestDecideFeedEgress(t *testing.T) {
	for _, raw := range []string{"", "false", "0", "no", "TRUE-ish", "1 "} {
		if d := DecideFeedEgress(raw); d.AllowPrivate || d.Notice != "" {
			t.Errorf("DecideFeedEgress(%q) = %+v, want production", raw, d)
		}
	}
	for _, raw := range []string{"true", "TRUE", "1", "t"} {
		d := DecideFeedEgress(raw)
		if !d.AllowPrivate {
			t.Errorf("DecideFeedEgress(%q) does not allow private addresses", raw)
		}
		if !strings.Contains(d.Notice, EnvAllowStandins) || !strings.Contains(d.Notice, "NOT a production deployment") {
			t.Errorf("DecideFeedEgress(%q).Notice = %q", raw, d.Notice)
		}
	}
	if (FeedEgress{}).AllowPrivate {
		t.Error("the zero FeedEgress allows private addresses")
	}
}
