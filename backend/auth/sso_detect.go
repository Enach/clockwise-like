package auth

import (
	"container/list"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Enach/paceday/backend/domain"
	"github.com/Enach/paceday/backend/storage"
)

// The answers of POST /api/auth/detect (contract SsoDetectResponse.type).
const (
	DetectGoogle    = "google"
	DetectMicrosoft = "microsoft"
	DetectSSO       = "sso"
	DetectGeneric   = "generic"
	// DetectChoose asks the user to pick a provider (PAC-97 D-4): the mail
	// host gave no strong signal, or could not be read.
	DetectChoose = "choose"
)

// DetectResult is returned by the /api/auth/detect endpoint.
type DetectResult struct {
	Type         string `json:"type"`                    // "google" | "microsoft" | "sso" | "generic" | "choose"
	ProviderName string `json:"provider_name,omitempty"` // only when type == "sso"
	RedirectURL  string `json:"redirect_url,omitempty"`  // only when type == "sso"
}

// MXResolver answers MX queries. *net.Resolver satisfies it; tests inject an
// in-memory fake (PAC-97 D-9).
type MXResolver interface {
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
}

const (
	// MXLookupBudget bounds one shared MX lookup, retries included (D-7). It
	// is the lookup's own budget, never a caller's.
	MXLookupBudget = 2 * time.Second
	// MXDefiniteTTL is how long an answer read from a real DNS reply is kept:
	// google, microsoft, or choose from NXDOMAIN / no MX / null MX /
	// non-matching MX (D-8).
	MXDefiniteTTL = time.Hour
	// MXFailureTTL is how long a failed lookup (timeout, SERVFAIL, refused,
	// partial answer) is kept, so a blip neither sticks nor is hammered (D-8).
	MXFailureTTL = time.Minute
	// MXCacheSize bounds the per-process cache; the oldest entry is evicted
	// first (D-8).
	MXCacheSize = 10000
)

// DetectorOptions configures a Detector. The zero value is production: the
// system resolver, the wall clock, MXLookupBudget and MXCacheSize.
type DetectorOptions struct {
	Resolver  MXResolver
	Now       func() time.Time
	Budget    time.Duration
	CacheSize int
}

// Detector decides the sign-in provider for an email address (PAC-97 D-1):
// a consumer or unparseable address is generic; an SSO row wins; then the
// domain's MX records decide google or microsoft on a strong match only;
// everything else, every failure included, is choose.
type Detector struct {
	resolver MXResolver
	now      func() time.Time
	budget   time.Duration
	size     int

	mu       sync.Mutex
	cache    map[string]*mxEntry
	order    *list.List // domains in insertion order, oldest at the front
	inflight map[string]*mxCall
}

type mxEntry struct {
	answer  string
	expires time.Time
	elem    *list.Element
}

// mxCall is one shared lookup; done is closed once answer is set.
type mxCall struct {
	done   chan struct{}
	answer string
}

// NewDetector builds a Detector. A nil Resolver is the system resolver.
func NewDetector(o DetectorOptions) *Detector {
	d := &Detector{
		resolver: o.Resolver,
		now:      o.Now,
		budget:   o.Budget,
		size:     o.CacheSize,
		cache:    map[string]*mxEntry{},
		order:    list.New(),
		inflight: map[string]*mxCall{},
	}
	if d.resolver == nil {
		d.resolver = net.DefaultResolver
	}
	if d.now == nil {
		d.now = time.Now
	}
	if d.budget <= 0 {
		d.budget = MXLookupBudget
	}
	if d.size <= 0 {
		d.size = MXCacheSize
	}
	return d
}

// StandInResolver is the e2e DNS stand-in (PAC-97 D-9, AC-28): a pure-Go
// resolver that sends every query to addr (a literal ip:port) and nowhere
// else. main.go builds it only from config.DecideMXResolver, which refuses it
// without PACEDAY_ALLOW_STANDINS and outside config.PermittedPeer.
func StandInResolver(addr string) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var dl net.Dialer
			return dl.DialContext(ctx, network, addr)
		},
	}
}

var defaultDetector atomic.Pointer[Detector]

func init() { defaultDetector.Store(NewDetector(DetectorOptions{})) }

// DefaultDetector is the detector the HTTP handler uses.
func DefaultDetector() *Detector { return defaultDetector.Load() }

// SetDefaultDetector replaces the handler's detector (main.go for the e2e
// stand-in, tests for a fake) and returns a function restoring the previous
// one.
func SetDefaultDetector(d *Detector) (restore func()) {
	prev := defaultDetector.Swap(d)
	return func() { defaultDetector.Store(prev) }
}

// DetectAuthProvider inspects an email address with the default detector.
func DetectAuthProvider(ctx context.Context, db *sql.DB, email string) (*DetectResult, error) {
	return DefaultDetector().Detect(ctx, db, email)
}

// Detect inspects an email address and returns the sign-in provider. Only a
// failed SSO-row read is an error; a failed or slow MX lookup is choose.
func (d *Detector) Detect(ctx context.Context, db *sql.DB, email string) (*DetectResult, error) {
	return d.detect(ctx, func(dom string) (*storage.SSOProvider, error) {
		return storage.GetSSOProviderByDomain(db, dom)
	}, email)
}

func (d *Detector) detect(ctx context.Context, ssoRow func(string) (*storage.SSOProvider, error), email string) (*DetectResult, error) {
	dom := domain.ExtractDomain(email)
	if dom == "" || domain.IsGenericDomain(dom) {
		return &DetectResult{Type: DetectGeneric}, nil
	}

	// Custom SSO configured for this domain takes priority.
	provider, err := ssoRow(dom)
	if err != nil {
		return nil, err
	}
	if provider != nil {
		return &DetectResult{
			Type:         DetectSSO,
			ProviderName: provider.ProviderName,
			RedirectURL:  fmt.Sprintf("/api/auth/sso/%s", dom),
		}, nil
	}

	// A name that is not a multi-label ASCII host name is never queried.
	if !domain.IsQueryableDNSName(dom) {
		return &DetectResult{Type: DetectChoose}, nil
	}
	return &DetectResult{Type: d.mailProvider(ctx, dom)}, nil
}

// mailProvider answers google, microsoft or choose for a queryable domain,
// from the cache or from one lookup shared by every concurrent caller. A
// caller whose context ends stops waiting and gets choose; the shared lookup
// carries on under its own budget, and a cancellation is never cached.
func (d *Detector) mailProvider(ctx context.Context, dom string) string {
	if ctx.Err() != nil {
		return DetectChoose
	}
	d.mu.Lock()
	if e, ok := d.cache[dom]; ok && d.now().Before(e.expires) {
		d.mu.Unlock()
		return e.answer
	}
	c, ok := d.inflight[dom]
	if !ok {
		c = &mxCall{done: make(chan struct{})}
		d.inflight[dom] = c
		go d.lookup(dom, c)
	}
	d.mu.Unlock()

	select {
	case <-c.done:
		return c.answer
	case <-ctx.Done():
		return DetectChoose
	}
}

type mxReply struct {
	records []*net.MX
	err     error
}

// lookup runs one MX query for the fully-qualified name (trailing dot: no
// search-list expansion) under the detector's own budget, then caches and
// publishes the answer.
func (d *Detector) lookup(dom string, c *mxCall) {
	lctx, cancel := context.WithTimeout(context.Background(), d.budget)
	defer cancel()

	replies := make(chan mxReply, 1)
	go func() {
		records, err := d.resolver.LookupMX(lctx, dom+".")
		replies <- mxReply{records, err}
	}()
	var r mxReply
	select {
	case r = <-replies:
	case <-lctx.Done():
		// A resolver that ignores its context still cannot hold callers
		// past the budget.
		r = mxReply{err: lctx.Err()}
	}

	answer, definite := classifyMX(r.records, r.err)
	ttl := MXDefiniteTTL
	if !definite {
		ttl = MXFailureTTL
		// The domain only: never the address it came from (AC-12, D-11).
		log.Printf("sign-in detection: MX lookup for %s failed, answering %s: %v", dom, answer, r.err)
	}

	d.mu.Lock()
	d.store(dom, answer, d.now().Add(ttl))
	delete(d.inflight, dom)
	c.answer = answer
	close(c.done)
	d.mu.Unlock()
}

// store caches an answer, evicting the oldest entries beyond the bound. The
// caller holds d.mu.
func (d *Detector) store(dom, answer string, expires time.Time) {
	if old, ok := d.cache[dom]; ok {
		d.order.Remove(old.elem)
		delete(d.cache, dom)
	}
	for len(d.cache) >= d.size {
		front := d.order.Front()
		if front == nil {
			break
		}
		delete(d.cache, front.Value.(string))
		d.order.Remove(front)
	}
	d.cache[dom] = &mxEntry{answer: answer, expires: expires, elem: d.order.PushBack(dom)}
}

// cached reports the number of cache entries (tests).
func (d *Detector) cached() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.cache)
}

// classifyMX applies the MX rule (PAC-97 D-2, D-3, D-7) to one lookup's
// result. definite is false for a failure, which is cached briefly.
//
// The records with the lowest preference are the primary ones. The answer is
// provider P when every primary record matches P and no record at any
// preference matches the other provider; otherwise choose. A null MX
// anywhere, no records, NXDOMAIN, any error and a partial answer returned
// together with an error are choose.
func classifyMX(records []*net.MX, err error) (answer string, definite bool) {
	if err != nil {
		var dnsErr *net.DNSError
		if len(records) == 0 && errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return DetectChoose, true // NXDOMAIN, or no MX records
		}
		return DetectChoose, false // timeout, SERVFAIL, refused, partial
	}
	if len(records) == 0 {
		return DetectChoose, true
	}
	lowest := records[0].Pref
	for _, r := range records {
		if r == nil || isNullMX(r.Host) {
			return DetectChoose, true
		}
		if r.Pref < lowest {
			lowest = r.Pref
		}
	}
	var anyGoogle, anyMicrosoft bool
	primaryGoogle, primaryMicrosoft := true, true
	for _, r := range records {
		g, m := isGoogleMX(r.Host), isMicrosoftMX(r.Host)
		anyGoogle = anyGoogle || g
		anyMicrosoft = anyMicrosoft || m
		if r.Pref == lowest {
			primaryGoogle = primaryGoogle && g
			primaryMicrosoft = primaryMicrosoft && m
		}
	}
	switch {
	case primaryGoogle && !anyMicrosoft:
		return DetectGoogle, true
	case primaryMicrosoft && !anyGoogle:
		return DetectMicrosoft, true
	default:
		return DetectChoose, true
	}
}

// normalizeMXHost lower-cases a host and removes one trailing dot.
func normalizeMXHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
}

// isNullMX reports RFC 7505's "." target.
func isNullMX(h string) bool { return normalizeMXHost(h) == "" }

// googleMXDomains: the host equals one of them or is below one, on a label
// boundary (D-2).
var googleMXDomains = []string{"google.com", "googlemail.com"}

// microsoftMXSuffixes are Exchange Online's global MX names (D-2): the
// classic and legacy names and the DNSSEC/DANE form. The Outlook.com
// consumer host (*.olc.protection.outlook.com, personal accounts until
// PAC-77) and the sovereign clouds (office365.us, partner.outlook.cn,
// outlook.de) are deliberately absent: Paceday signs in only through the
// global endpoints.
var microsoftMXSuffixes = []string{".mail.protection.outlook.com", ".mail.eo.outlook.com", ".mx.microsoft"}

func isGoogleMX(h string) bool {
	h = normalizeMXHost(h)
	for _, g := range googleMXDomains {
		if h == g || strings.HasSuffix(h, "."+g) {
			return true
		}
	}
	return false
}

func isMicrosoftMX(h string) bool {
	h = normalizeMXHost(h)
	for _, s := range microsoftMXSuffixes {
		if strings.HasSuffix(h, s) && len(h) > len(s) {
			return true
		}
	}
	return false
}
