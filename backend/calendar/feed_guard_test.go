package calendar

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/internal/outbound"
	"github.com/Enach/paceday/backend/internal/testdial"
)

// The calendar-feed egress guard (security audit of 2026-11-22: SSRF via the
// stored webcal feed URL). A feed URL is user-supplied and re-fetched by the
// personal-blocker cron, so these tests are about what the fetch may reach,
// not about what the feed says.

// feedName is a name the write check accepts (two labels, a letter in the
// last) and whose answer the DNS double controls. The .test TLD means no
// hosts file and no search domain can answer behind the test's back.
const feedName = "feed.ssrf.test"

const minimalICS = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nEND:VCALENDAR\r\n"

// productionFeedEgress installs the decision of a deployment that declares
// nothing: a feed may be fetched only from a public address.
func productionFeedEgress(t *testing.T) {
	t.Helper()
	t.Cleanup(ConfigureFeeds(config.FeedEgress{}))
}

// testModeFeedEgress installs the decision of a stack that declares test mode
// (PACEDAY_ALLOW_STANDINS=true), as the e2e stack does.
func testModeFeedEgress(t *testing.T) {
	t.Helper()
	t.Cleanup(ConfigureFeeds(config.DecideFeedEgress("true")))
}

func fetchFeed(t *testing.T, rawURL string) error {
	t.Helper()
	// A fresh client every time: WebcalClient caches a successful fetch for
	// 15 minutes, and each of these tests is about one connection.
	_, err := NewWebcalClient(rawURL).ListEvents(context.Background(), time.Now(), time.Now().Add(time.Hour))
	return err
}

// The finding itself: an authenticated user stores a URL inside the
// deployment network and the backend fetches it. With the production decision
// the connection is refused before the syscall, and the listener is never
// touched.
func TestFeedGuard_RefusesPrivatePeer(t *testing.T) {
	productionFeedEgress(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(minimalICS))
	}))
	defer srv.Close()

	err := fetchFeed(t, srv.URL)
	if !errors.Is(err, ErrFeedPeerRefused) {
		t.Fatalf("fetch of %s = %v, want ErrFeedPeerRefused", srv.URL, err)
	}
	if hits.Load() != 0 {
		t.Errorf("the feed was reached %d time(s)", hits.Load())
	}
	// The refusal names the address, never the feed URL: a webcal URL is a
	// secret and may carry an address (withoutURL).
	if strings.Contains(err.Error(), srv.URL) {
		t.Errorf("the error carries the feed URL: %v", err)
	}
}

// The rebinding case, which is why the check belongs in the dialler and not
// only on write: the name passes the write check and resolves to a private
// address only when the fetch happens. The second half is the positive
// control — the same name answered with a public address gets past the feed
// guard (and is then stopped by testdial, which is what proves the guard let
// it through).
func TestFeedGuard_RefusesRebindingAfterTheWriteCheck(t *testing.T) {
	productionFeedEgress(t)
	if msg := WebcalURLProblem("url", "http://"+feedName+"/feed.ics"); msg != "" {
		t.Fatalf("the write check refuses %s on its own: %q — this test would prove nothing", feedName, msg)
	}

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(minimalICS))
	}))
	defer srv.Close()
	port := srv.Listener.Addr().(*net.TCPAddr).Port
	rawURL := fmt.Sprintf("http://%s:%d/feed.ics", feedName, port)

	dns := newDNSDouble(t)
	dns.set(feedName, "127.0.0.1")
	feedResolver.install(t, dns.resolver())

	err := fetchFeed(t, rawURL)
	if !errors.Is(err, ErrFeedPeerRefused) {
		t.Fatalf("fetch of a name resolving to loopback = %v, want ErrFeedPeerRefused", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("the feed was reached %d time(s)", hits.Load())
	}

	dns.set(feedName, publicAddr)
	err = fetchFeed(t, rawURL)
	if err == nil {
		t.Fatal("fetch of a name resolving to a public address succeeded inside a test binary")
	}
	if errors.Is(err, ErrFeedPeerRefused) {
		t.Errorf("the feed guard refused a public address: %v", err)
	}
	if !errors.Is(err, testdial.ErrRealHost) {
		t.Errorf("fetch = %v, want it stopped by testdial (so the feed guard let it through)", err)
	}
}

// A redirect is never followed: it is the other way a URL that passed the
// write check sends the fetch somewhere else.
func TestFeedGuard_RefusesRedirect(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
		_, _ = w.Write([]byte(minimalICS))
	}))
	defer target.Close()
	for _, code := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL+"/feed.ics", code)
		}))
		err := fetchFeed(t, feed.URL)
		feed.Close()
		if !errors.Is(err, ErrFeedRedirectRefused) {
			t.Errorf("%d: fetch = %v, want ErrFeedRedirectRefused", code, err)
		}
		if targetHits.Load() != 0 {
			t.Fatalf("%d: the redirect target was reached", code)
		}
	}
}

// The e2e and test stacks fetch from containers on private addresses. A
// deployment that declares test mode still can — both at the write check and
// at the dialler — and says so at startup.
func TestFeedGuard_TestModeReachesPrivateAddresses(t *testing.T) {
	testModeFeedEgress(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(minimalICS))
	}))
	defer srv.Close()
	if err := fetchFeed(t, srv.URL); err != nil {
		t.Fatalf("test-mode fetch from loopback: %v", err)
	}
	// A test binary can only connect to loopback (PAC-145), so the container
	// case itself is asserted on the predicate the dialler applies.
	for _, addr := range []string{"10.143.43.10:8080", "172.18.0.4:80", "[fd00::2]:8080"} {
		if err := checkFeedPeer(true, addr); err != nil {
			t.Errorf("test mode refuses the container address %s: %v", addr, err)
		}
	}
	for _, raw := range []string{"http://10.143.43.10:8080/feed.ics", "http://fake-webcal:8080/feed.ics"} {
		if msg := WebcalURLProblem("url", raw); msg != "" {
			t.Errorf("test mode refuses %s on write: %q", raw, msg)
		}
	}
	if d := config.DecideFeedEgress("true"); d.Notice == "" {
		t.Error("a test-mode deployment prints no notice")
	}
}

// Fail closed: a process that never called ConfigureFeeds — any production
// binary that somehow skipped the wiring — judges as production, not as
// permissive.
func TestFeedGuard_UnconfiguredIsProduction(t *testing.T) {
	t.Cleanup(ConfigureFeeds(config.FeedEgress{AllowPrivate: true}))
	feedEgress.Store(nil)
	if d := currentFeedEgress(); d.AllowPrivate {
		t.Fatal("an unconfigured process allows private addresses")
	}
	if msg := WebcalURLProblem("url", "http://10.0.0.5:9200/_cluster/health"); msg == "" {
		t.Error("an unconfigured process accepts a private feed URL on write")
	}
}

// checkFeedPeer is what Control applies, on the literal ip:port.
func TestCheckFeedPeer_Table(t *testing.T) {
	refused := []string{
		"127.0.0.1:80", "[::1]:443", "[::ffff:127.0.0.1]:80", // loopback
		"10.0.0.5:9200", "172.16.0.1:80", "192.168.1.1:80", // RFC 1918
		"[fd00::1]:80",          // IPv6 unique-local
		"169.254.169.254:80",    // link-local: the metadata address
		"[fe80::1]:80",          // IPv6 link-local
		"0.0.0.0:80", "[::]:80", // unspecified
		"100.64.0.1:80",          // RFC 6598
		"224.0.0.1:80",           // multicast
		"255.255.255.255:80",     // broadcast
		"[64:ff9b::a00:5]:80",    // NAT64-wrapped 10.0.0.5
		"[::ffff:10.0.0.5]:9200", // IPv4-mapped private
		"[fe80::1%eth0]:80",      // zone-scoped
		feedName + ":80",         // unresolved: cannot be judged
		"garbage",
	}
	for _, addr := range refused {
		if err := checkFeedPeer(false, addr); !errors.Is(err, ErrFeedPeerRefused) {
			t.Errorf("checkFeedPeer(false, %q) = %v, want ErrFeedPeerRefused", addr, err)
		}
	}
	for _, addr := range []string{publicAddr + ":443", "[2a00:1450:4007:80e::200a]:443", "93.184.216.34:80"} {
		if err := checkFeedPeer(false, addr); err != nil {
			t.Errorf("checkFeedPeer(false, %q) = %v, want nil", addr, err)
		}
	}
	// Test mode relaxes the address set, not the ability to judge.
	if err := checkFeedPeer(true, "garbage"); !errors.Is(err, ErrFeedPeerRefused) {
		t.Errorf("checkFeedPeer(true, garbage) = %v, want ErrFeedPeerRefused", err)
	}
}

// The write check both handlers share. The address set is config's
// (TestCheckFeedURL_Table); this is about the message.
func TestWebcalURLProblem(t *testing.T) {
	productionFeedEgress(t)
	for _, tc := range []struct {
		raw  string
		want string
	}{
		{"", ""},
		{"https://feeds.example.com/team.ics", ""},
		{"webcal://example.com/family.ics", ""},
		{"http://10.0.0.5:9200/_cluster/health", "url" + msgFeedURLNotPublic},
		{"http://169.254.169.254/latest/meta-data/", "url" + msgFeedURLNotPublic},
		{"http://localhost/feed.ics", "url" + msgFeedURLNotPublic},
		{"file:///etc/passwd", "url" + msgFeedURLNotFetchable},
		{"http://user:pw@example.com/f.ics", "url" + msgFeedURLNotFetchable},
	} {
		if got := WebcalURLProblem("url", tc.raw); got != tc.want {
			t.Errorf("WebcalURLProblem(url, %q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
	// The property is the caller's, so one function serves both sinks.
	if got := WebcalURLProblem("webcalUrl", "http://10.0.0.5/f.ics"); !strings.HasPrefix(got, "webcalUrl ") {
		t.Errorf("WebcalURLProblem(webcalUrl, …) = %q, want it to name webcalUrl first", got)
	}
}

// The feed client keeps PAC-81's ICS bound and refuses redirects, over the
// guarded transport and no other.
func TestFeedClient_BoundsAndTransport(t *testing.T) {
	l := outbound.Default
	l.ICS = 3 * time.Second
	defer outbound.SetForTest(l)()
	c := feedClient()
	if c.Timeout != 3*time.Second {
		t.Errorf("feedClient().Timeout = %s, want the ICS limit", c.Timeout)
	}
	if c.Transport != http.RoundTripper(feedTransport) {
		t.Error("feedClient() does not use the guarded transport")
	}
	if c.CheckRedirect == nil {
		t.Error("feedClient() follows redirects")
	}
	if feedTransport.Proxy != nil {
		t.Error("the feed transport honours an ambient proxy, so the guard would judge the proxy")
	}
}
