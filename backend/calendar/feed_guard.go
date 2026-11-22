package calendar

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/internal/outbound"
	"github.com/Enach/paceday/backend/internal/testdial"
)

// Calendar-feed egress (security audit of 2026-11-22: SSRF via the stored
// webcal feed URL). A feed URL is user-supplied, stored, and fetched again by
// the personal-blocker cron every 30 minutes, so an unguarded fetch is a
// persistent internal scanner rather than a one-shot probe. Everything a feed
// fetch goes through is here: the deployment's decision, the write check both
// write paths share, and the client the fetch uses.
//
// guard.go does the same job for calendar-PROVIDER traffic, and its
// polarity is the opposite one: there, a guarded deployment may reach ONLY
// private addresses, because the thing it is guarding is a stand-in. Here the
// destination is chosen by a user, so a production deployment may reach only
// public addresses. The two share config.EnvAllowStandins and nothing else.

var (
	// ErrFeedPeerRefused is returned, wrapped, when the egress guard refuses
	// the address a feed connection was about to use.
	ErrFeedPeerRefused = errors.New("calendar: feed peer address refused by the egress guard")
	// ErrFeedRedirectRefused is returned, wrapped, when a feed answers with a
	// redirection. Redirects are never followed: the guard judges the address
	// of every hop, but a redirect is also how a feed that passed the write
	// check sends the fetch somewhere else entirely, so there is nothing to
	// gain by following one.
	ErrFeedRedirectRefused = errors.New("calendar: feed redirect refused")
)

// feedEgress is the installed decision. nil means Configure was never called.
var feedEgress atomic.Pointer[config.FeedEgress]

// ConfigureFeeds installs the feed egress decision for this process. main.go
// calls it once, before any cron or route exists, in every deployment. The
// returned function restores the previous decision; main.go ignores it.
func ConfigureFeeds(d config.FeedEgress) (restore func()) {
	prev := feedEgress.Swap(&d)
	return func() { feedEgress.Store(prev) }
}

// currentFeedEgress returns the installed decision, or the production one
// when ConfigureFeeds was never called: nothing private, fail closed. That
// holds inside a test binary too, so the write check below is tested as a
// deployment runs it.
func currentFeedEgress() config.FeedEgress {
	if d := feedEgress.Load(); d != nil {
		return *d
	}
	return config.FeedEgress{}
}

// Write-check messages. Each names the property first, so a client can attach
// it to the field, in the shape of nlp.MsgAzureEndpointNotApproved and its
// neighbours.
const (
	msgFeedURLNotFetchable = " must be a webcal, webcals, http or https URL with no credentials"
	msgFeedURLNotPublic    = " must not point at a loopback, link-local or private address"
)

// WebcalURLProblem is the write check for a feed URL: "" when the value is
// acceptable, otherwise a message naming property first. "" is always
// acceptable (no feed is configured).
//
// Both write paths go through this one function — the personal-calendars
// handler (POST and PATCH) and validateChangedSettings' webcalUrl case — so
// they cannot drift apart. It is not the control: a name is judged for real
// at connection time by the egress guard below.
func WebcalURLProblem(property, raw string) string {
	switch config.CheckFeedURL(raw, currentFeedEgress().AllowPrivate) {
	case config.FeedURLNotFetchable:
		return property + msgFeedURLNotFetchable
	case config.FeedURLNotPublic:
		return property + msgFeedURLNotPublic
	default:
		return ""
	}
}

// feedResolver replaces the system resolver for feed connections while
// installed. Production installs none (test_seams.go).
var feedResolver seam[*net.Resolver]

// feedTransport is the transport under every feed fetch, built once and
// shared by every fetch for connection reuse.
var feedTransport = newFeedTransport()

func newFeedTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	// No ambient proxy (as in newBaseTransport): with one, the peer the guard
	// judges would be the proxy, and the address the request is actually
	// delivered to would never be judged at all.
	t.Proxy = nil
	t.DialContext = dialFeed
	t.MaxIdleConnsPerHost = 100
	t.HTTP2 = &http.HTTP2Config{SendPingTimeout: 31 * time.Second}
	return t
}

// dialFeed builds the dialler per connection, so the installed resolver and
// decision are read at dial time rather than frozen into the transport.
func dialFeed(ctx context.Context, network, address string) (net.Conn, error) {
	r, _ := feedResolver.get()
	d := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Resolver:  r,
		Control:   controlFeedPeer,
	}
	return d.DialContext(ctx, network, address)
}

// controlFeedPeer judges the peer of every feed connection. Go calls Control
// after resolution and immediately before the connect syscall, with the
// literal address that connection is about to use, so the judgement is made
// per connection and from the answer that connection is using: a name that
// resolved to a public address when the URL was saved, or on an earlier
// fetch, and to a private one now (DNS rebinding) is refused now. This is why
// the check belongs in the dialler and not only on write.
//
// Inside a test binary (and only there) two things change. Private addresses
// are allowed, so a test's httptest feed on loopback is reachable without
// every such test declaring a decision — a test that needs the production
// judgement installs it with ConfigureFeeds. And testdial.Check applies last,
// so no test can connect to anything but loopback through this transport,
// whatever was or was not configured (PAC-145). The switch is
// testing.Testing, reported by the linker: nothing an operator sets can turn
// it on in a production binary.
func controlFeedPeer(_, address string, _ syscall.RawConn) error {
	allowPrivate := currentFeedEgress().AllowPrivate ||
		(testdial.Active() && feedEgress.Load() == nil)
	if err := checkFeedPeer(allowPrivate, address); err != nil {
		return err
	}
	if testdial.Active() {
		return testdial.Check(address)
	}
	return nil
}

// checkFeedPeer judges the literal ip:port a feed connection is about to use.
// Anything that cannot be judged is refused.
func checkFeedPeer(allowPrivate bool, address string) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%w: cannot judge %q: %v", ErrFeedPeerRefused, address, err)
	}
	if allowPrivate {
		return nil
	}
	if !config.PublicFeedPeer(ap.Addr()) {
		return fmt.Errorf("%w: %s is not a public address", ErrFeedPeerRefused, ap.Addr())
	}
	return nil
}

// feedClient is the client every feed fetch uses: bounded by the ICS limit
// (PAC-81 AC-A3), over the guarded transport, following no redirect.
func feedClient() *http.Client {
	return &http.Client{
		Timeout:       outbound.Current().ICS,
		Transport:     feedTransport,
		CheckRedirect: refuseFeedRedirect,
	}
}

// refuseFeedRedirect names the host the feed pointed to, never the feed URL
// itself (a webcal URL is a secret and may carry an address; see withoutURL).
func refuseFeedRedirect(req *http.Request, _ []*http.Request) error {
	return fmt.Errorf("%w: the feed pointed to %s", ErrFeedRedirectRefused, req.URL.Host)
}
