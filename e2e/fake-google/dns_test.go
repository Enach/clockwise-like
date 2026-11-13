package main

import (
	"context"
	"net"
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
	addr := pc.LocalAddr().String()
	_ = pc.Close()
	go func() { _ = serveDNS(addr) }()

	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}}
	lookup := func(name string) ([]*net.MX, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var mxs []*net.MX
		var err error
		for i := 0; i < 20; i++ { // the listener may not be up yet
			if mxs, err = r.LookupMX(ctx, name); err == nil || isNotFound(err) {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		return mxs, err
	}

	mxs, err := lookup("abc.google-mx.pac97.test.")
	if err != nil || len(mxs) != 5 || mxs[0].Host != "aspmx.l.google.com." || mxs[0].Pref != 1 {
		t.Fatalf("google-mx = %v, %v", mxs, err)
	}
	mxs, err = lookup("x1.gateway-mx.pac97.test.")
	if err != nil || len(mxs) != 2 || mxs[0].Host != "eu-smtp-inbound-1.mimecast.com." {
		t.Fatalf("gateway-mx = %v, %v", mxs, err)
	}
	mxs, err = lookup("x1.microsoft-mx.pac97.test.")
	if err != nil || len(mxs) != 1 || mxs[0].Host != "pac97-test.mail.protection.outlook.com." {
		t.Fatalf("microsoft-mx = %v, %v", mxs, err)
	}
	for _, name := range []string{"gorgias.com.", "google-mx.pac97.test.", "elsewhere.test."} {
		if _, err := lookup(name); !isNotFound(err) {
			t.Errorf("%s: err = %v, want NXDOMAIN", name, err)
		}
	}
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
