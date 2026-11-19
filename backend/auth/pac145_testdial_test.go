package auth

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/testdial"
)

// PAC-145: inside a test binary the identity-provider client refuses a peer
// that is not loopback, guard on or off. 192.0.2.1 (TEST-NET-1, RFC 5737) is
// never routed, so if the refusal were missing the dial would time out rather
// than reach anything, and the test would still fail.
func TestPAC145IdPClientRefusesNonLoopbackInTests(t *testing.T) {
	for _, guard := range []bool{false, true} {
		tr := newIdPHTTPClient(guard).Transport.(*http.Transport)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		conn, err := tr.DialContext(ctx, "tcp", "192.0.2.1:443")
		cancel()
		if conn != nil {
			conn.Close()
		}
		want := testdial.ErrRealHost
		if guard {
			want = errPeerRefused // the production guard judges first
		}
		if !errors.Is(err, want) {
			t.Errorf("guard=%v: dial = %v, want %v", guard, err, want)
		}
	}
	// A guarded private peer the production guard permits is still refused.
	// Control is called directly, with no socket, so nothing is ever sent to
	// 10.255.255.1: a real LAN host there is never dialled.
	d := newIdPDialer(true)
	if d.Control == nil {
		t.Fatal("guarded dialler has no Control")
	}
	if err := d.Control("tcp4", "10.255.255.1:443", nil); !errors.Is(err, testdial.ErrRealHost) {
		t.Errorf("guard=true private peer: Control = %v, want testdial.ErrRealHost", err)
	}
	if err := d.Control("tcp4", "127.0.0.1:443", nil); err != nil {
		t.Errorf("guard=true loopback: Control = %v, want nil", err)
	}
}
