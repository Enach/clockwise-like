package auth

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// mxDouble is an in-process UDP DNS responder answering MX questions, in the
// manner of backend/calendar/dnsdouble_test.go. It is the AC-28 proof that a
// detector built on StandInResolver queries the stand-in and nothing else.
type mxDouble struct {
	conn *net.UDPConn

	mu      sync.Mutex
	answers map[string][]*net.MX // lower-case FQDN
	seen    []string
}

func newMXDouble(t *testing.T) *mxDouble {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("mx double: listen: %v", err)
	}
	d := &mxDouble{conn: conn, answers: map[string][]*net.MX{}}
	go d.serve()
	t.Cleanup(func() { _ = conn.Close() })
	return d
}

func (d *mxDouble) addr() string { return d.conn.LocalAddr().String() }

func (d *mxDouble) set(name string, records ...*net.MX) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.answers[strings.ToLower(name)] = records
}

func (d *mxDouble) queries() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.seen...)
}

func (d *mxDouble) serve() {
	buf := make([]byte, 1500)
	for {
		n, from, err := d.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		var p dnsmessage.Parser
		h, err := p.Start(buf[:n])
		if err != nil {
			continue
		}
		q, err := p.Question()
		if err != nil {
			continue
		}
		name := strings.ToLower(q.Name.String())
		d.mu.Lock()
		d.seen = append(d.seen, name)
		records, ok := d.answers[name]
		d.mu.Unlock()

		rh := dnsmessage.Header{ID: h.ID, Response: true, Authoritative: true, RCode: dnsmessage.RCodeSuccess}
		if !ok {
			rh.RCode = dnsmessage.RCodeNameError
		}
		b := dnsmessage.NewBuilder(nil, rh)
		b.EnableCompression()
		_ = b.StartQuestions()
		_ = b.Question(q)
		_ = b.StartAnswers()
		if q.Type == dnsmessage.TypeMX {
			for _, r := range records {
				_ = b.MXResource(
					dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 60},
					dnsmessage.MXResource{Pref: r.Pref, MX: dnsmessage.MustNewName(r.Host)},
				)
			}
		}
		out, err := b.Finish()
		if err != nil {
			continue
		}
		_, _ = d.conn.WriteToUDP(out, from)
	}
}

// AC-28 (the detection half; the startup decision is
// backend/config/mx_resolver_test.go): a detector on the stand-in resolver
// answers from the stand-in, and the stand-in sees every query.
func TestPAC97_AC28_StandInResolverQueriesOnlyTheStandIn(t *testing.T) {
	dbl := newMXDouble(t)
	dbl.set("gorgias-e2e.test.", &net.MX{Host: "aspmx.l.google.com.", Pref: 1}, &net.MX{Host: "alt1.aspmx.l.google.com.", Pref: 5})
	dbl.set("gateway-e2e.test.", &net.MX{Host: "eu-smtp-inbound-1.mimecast.com.", Pref: 10})

	d := NewDetector(DetectorOptions{Resolver: StandInResolver(dbl.addr())})
	if got := detectType(t, d, context.Background(), "alice@gorgias-e2e.test"); got != DetectGoogle {
		t.Errorf("gorgias-e2e.test = %q, want google", got)
	}
	if got := detectType(t, d, context.Background(), "alice@gateway-e2e.test"); got != DetectChoose {
		t.Errorf("gateway-e2e.test = %q, want choose", got)
	}
	if got := detectType(t, d, context.Background(), "alice@unknown-e2e.test"); got != DetectChoose {
		t.Errorf("unknown-e2e.test (NXDOMAIN) = %q, want choose", got)
	}
	seen := map[string]bool{}
	for _, q := range dbl.queries() {
		seen[q] = true
	}
	for _, want := range []string{"gorgias-e2e.test.", "gateway-e2e.test.", "unknown-e2e.test."} {
		if !seen[want] {
			t.Errorf("the stand-in did not see %q (saw %v)", want, dbl.queries())
		}
	}
	// NXDOMAIN from a real reply is a definite answer, cached for an hour.
	d.mu.Lock()
	e := d.cache["unknown-e2e.test"]
	d.mu.Unlock()
	if e == nil || e.expires.Sub(d.now()) < MXDefiniteTTL-time.Minute {
		t.Errorf("NXDOMAIN cache entry = %+v, want a definite (1 h) entry", e)
	}
}
