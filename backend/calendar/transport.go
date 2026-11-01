package calendar

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Enach/paceday/backend/internal/outbound"
	"golang.org/x/oauth2"

	"github.com/Enach/paceday/backend/config"
)

// ErrRedirectRefused is returned, wrapped, when the calendar provider (or a
// stand-in) answers with a redirection. Redirects are never followed in any
// deployment: the credential is attached to every request, so following one
// would hand the user's token to wherever the 3xx pointed.
var ErrRedirectRefused = errors.New("calendar: redirect refused")

// setup is one configuration of the shared construction path: where calendar
// requests go, whether the guard is on, and the base transport built from
// them. It is immutable once built.
type setup struct {
	endpoint string
	guard    bool
	base     *http.Transport
}

var (
	// active is the configured setup. nil means "never configured", which is
	// the production decision (see current).
	active atomic.Pointer[setup]

	productionOnce  sync.Once
	productionSetup *setup
)

// Configure installs the calendar decision for this process. main.go calls it
// once, before any cron or route exists, in every deployment. A process that
// never calls it gets the production decision.
func Configure(d config.CalendarEndpoint) {
	configure(d, dialHooks{})
}

func configure(d config.CalendarEndpoint, h dialHooks) {
	active.Store(newSetup(d, h))
}

// current returns the active setup, or the production one when Configure was
// never called.
func current() *setup {
	if s := active.Load(); s != nil {
		return s
	}
	productionOnce.Do(func() {
		productionSetup = newSetup(config.ProductionCalendarEndpoint(), dialHooks{})
	})
	return productionSetup
}

func newSetup(d config.CalendarEndpoint, h dialHooks) *setup {
	endpoint := d.BaseURL
	if endpoint == "" {
		endpoint = config.PublishedCalendarEndpoint
	}
	return &setup{endpoint: endpoint, guard: d.Guard, base: newBaseTransport(d.Guard, h)}
}

// newBaseTransport is the transport under every calendar request, in every
// deployment. It is built once per configuration and shared by every client.
func newBaseTransport(guard bool, h dialHooks) *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	// No ambient proxy, in any deployment (spec §V3-3, E3). The default is
	// http.ProxyFromEnvironment; with it, the peer the guard judges would be
	// the proxy, not the endpoint the request is delivered to.
	t.Proxy = nil
	t.DialContext = newDialer(guard, h).DialContext
	// Parity with the library's default transport, so moving production off
	// it (E1) does not quietly change connection behaviour.
	t.MaxIdleConnsPerHost = 100
	t.HTTP2 = &http.HTTP2Config{SendPingTimeout: 31 * time.Second}
	return t
}

func refuseRedirect(req *http.Request, _ []*http.Request) error {
	return fmt.Errorf("%w: the response pointed to %s", ErrRedirectRefused, req.URL.Host)
}

// newHTTPClient assembles the client for one NewClient call: the caller's
// token source attaches the bearer on a clone of every request, over the
// shared base transport, and redirects are refused.
func newHTTPClient(s *setup, ts oauth2.TokenSource) *http.Client {
	return &http.Client{
		// PAC-81 AC-A3: every calendar API call ends within the provider
		// limit, whatever context the caller passes.
		Timeout:       outbound.Current().Provider,
		Transport:     &oauth2.Transport{Source: ts, Base: s.base},
		CheckRedirect: refuseRedirect,
	}
}
