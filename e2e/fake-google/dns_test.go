package main

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"sort"
	"testing"
	"time"
)

// TestDNSStandIn_AnswersTheFixtures drives the stand-in with Go's own
// resolver, the one the backend uses against it.
func TestDNSStandIn_AnswersTheFixtures(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// Serve on the socket already bound: no window in which the port is
	// closed and the first query times out.
	t.Cleanup(func() { _ = pc.Close() })
	addr := pc.LocalAddr().String()
	go func() { _ = serveDNSOn(pc) }()

	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}}
	lookup := func(name string) ([]*net.MX, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return r.LookupMX(ctx, name)
	}

	// Go's LookupMX sorts by preference and shuffles records of equal
	// preference (RFC 5321), so the answers are compared as sets.
	for name, want := range map[string][]string{
		"abc.google-mx.pac97.test.": {
			"1 aspmx.l.google.com.", "5 alt1.aspmx.l.google.com.", "5 alt2.aspmx.l.google.com.",
			"10 alt3.aspmx.l.google.com.", "10 alt4.aspmx.l.google.com.",
		},
		"x1.gateway-mx.pac97.test.":   {"10 eu-smtp-inbound-1.mimecast.com.", "10 eu-smtp-inbound-2.mimecast.com."},
		"x1.microsoft-mx.pac97.test.": {"0 pac97-test.mail.protection.outlook.com."},
	} {
		mxs, err := lookup(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := recordSet(mxs); !reflect.DeepEqual(got, sorted(want)) {
			t.Errorf("%s = %v, want %v", name, got, sorted(want))
		}
	}
	for _, name := range []string{"gorgias.com.", "google-mx.pac97.test.", "elsewhere.test."} {
		if _, err := lookup(name); !isNotFound(err) {
			t.Errorf("%s: err = %v, want NXDOMAIN", name, err)
		}
	}
}

// recordSet renders records as sorted "pref host" strings.
func recordSet(mxs []*net.MX) []string {
	out := make([]string, 0, len(mxs))
	for _, m := range mxs {
		out = append(out, fmt.Sprintf("%d %s", m.Pref, m.Host))
	}
	return sorted(out)
}

func sorted(s []string) []string {
	c := append([]string(nil), s...)
	sort.Strings(c)
	return c
}

func isNotFound(err error) bool {
	de, ok := err.(*net.DNSError)
	return ok && de.IsNotFound
}

func TestDNSReply_RefusesGarbage(t *testing.T) {
	if dnsReply([]byte{1, 2, 3}) != nil {
		t.Error("a garbage packet got a reply")
	}
}
