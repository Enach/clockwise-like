package calendar

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/Enach/paceday/backend/config"
)

// dnsDouble is an in-process DNS responder for the PAC-43 guard tests. Its
// answer per name can be swapped between queries, which is what the rebinding
// test (AC-9) needs. Names use the .test TLD so no hosts file entry and no
// search-domain expansion can supply an answer behind the test's back.
type dnsDouble struct {
	conn *net.UDPConn

	mu       sync.Mutex
	answers  map[string][]netip.Addr // keyed by lower-case FQDN, trailing dot included
	fallback []netip.Addr            // answer for any other name; nil means NXDOMAIN
}

func newDNSDouble(t *testing.T) *dnsDouble {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("dns double: listen: %v", err)
	}
	d := &dnsDouble{conn: conn, answers: map[string][]netip.Addr{}}
	go d.serve()
	t.Cleanup(func() { _ = conn.Close() })
	return d
}

// set installs the answer for name. Calling it again replaces the answer.
func (d *dnsDouble) set(name string, addrs ...string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.answers[fqdn(name)] = parseAddrs(addrs)
}

// setFallback installs the answer for every name that has no answer of its own.
func (d *dnsDouble) setFallback(addrs ...string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fallback = parseAddrs(addrs)
}

// resolver returns a pure-Go resolver whose every query reaches this double.
func (d *dnsDouble) resolver() *net.Resolver {
	addr := d.conn.LocalAddr().String()
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dl net.Dialer
			return dl.DialContext(ctx, "udp", addr)
		},
	}
}

func (d *dnsDouble) lookup(name string) ([]netip.Addr, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if a, ok := d.answers[strings.ToLower(name)]; ok {
		return a, true
	}
	if d.fallback != nil {
		return d.fallback, true
	}
	return nil, false
}

func (d *dnsDouble) serve() {
	buf := make([]byte, 1500)
	for {
		n, peer, err := d.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if resp, ok := d.answer(buf[:n]); ok {
			_, _ = d.conn.WriteToUDP(resp, peer)
		}
	}
}

func (d *dnsDouble) answer(query []byte) ([]byte, bool) {
	var p dnsmessage.Parser
	hdr, err := p.Start(query)
	if err != nil {
		return nil, false
	}
	q, err := p.Question()
	if err != nil {
		return nil, false
	}
	addrs, known := d.lookup(q.Name.String())
	rcode := dnsmessage.RCodeSuccess
	if !known {
		rcode = dnsmessage.RCodeNameError
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{
		ID: hdr.ID, Response: true, Authoritative: true, RecursionDesired: hdr.RecursionDesired,
		RecursionAvailable: true, RCode: rcode,
	})
	b.EnableCompression()
	if err := b.StartQuestions(); err != nil {
		return nil, false
	}
	if err := b.Question(q); err != nil {
		return nil, false
	}
	if err := b.StartAnswers(); err != nil {
		return nil, false
	}
	rh := dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 0}
	for _, a := range addrs {
		switch {
		case q.Type == dnsmessage.TypeA && a.Is4():
			rh.Type = dnsmessage.TypeA
			if err := b.AResource(rh, dnsmessage.AResource{A: a.As4()}); err != nil {
				return nil, false
			}
		case q.Type == dnsmessage.TypeAAAA && a.Is6():
			rh.Type = dnsmessage.TypeAAAA
			if err := b.AAAAResource(rh, dnsmessage.AAAAResource{AAAA: a.As16()}); err != nil {
				return nil, false
			}
		}
	}
	out, err := b.Finish()
	if err != nil {
		return nil, false
	}
	return out, true
}

func fqdn(name string) string {
	name = strings.ToLower(name)
	if !strings.HasSuffix(name, ".") {
		name += "."
	}
	return name
}

func parseAddrs(addrs []string) []netip.Addr {
	out := make([]netip.Addr, 0, len(addrs))
	for _, s := range addrs {
		out = append(out, netip.MustParseAddr(s))
	}
	return out
}

// attemptRecorder is the dial hook chained after the guard (plan §1.1). It sees
// only the addresses the guard let through, at the last point before connect,
// so "the recorder saw no attempt to X" is the observable form of "no
// connection is attempted to X".
type attemptRecorder struct {
	mu       sync.Mutex
	attempts []string // "ip:port"
}

func (r *attemptRecorder) record(_, address string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempts = append(r.attempts, address)
}

func (r *attemptRecorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.attempts...)
}

// sawHost reports whether any attempt went to ip (any port).
func (r *attemptRecorder) sawHost(ip string) bool {
	want := netip.MustParseAddr(ip).Unmap()
	for _, a := range r.all() {
		ap, err := netip.ParseAddrPort(a)
		if err == nil && ap.Addr().Unmap() == want {
			return true
		}
	}
	return false
}

// saw reports whether any attempt went to exactly address ("ip:port").
func (r *attemptRecorder) saw(address string) bool {
	for _, a := range r.all() {
		if a == address {
			return true
		}
	}
	return false
}

// configureForTest installs decision d with the test seams for the duration of
// t, and restores whatever was active before. Tests that call it must not run
// in parallel with each other.
func configureForTest(t *testing.T, d config.CalendarEndpoint, h dialHooks) {
	t.Helper()
	prev := active.Load()
	configure(d, h)
	t.Cleanup(func() { active.Store(prev) })
}

// guardedStandIn is the decision a redirected deployment gets: the configured
// address with the guard on.
func guardedStandIn(baseURL string) config.CalendarEndpoint {
	return config.CalendarEndpoint{BaseURL: baseURL, Guard: true}
}
