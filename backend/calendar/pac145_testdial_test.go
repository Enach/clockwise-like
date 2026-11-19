package calendar

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/internal/testdial"
)

// PAC-145: inside a test binary, every calendar dialler refuses a peer that is
// not loopback, guard on or off, configured or not. 192.0.2.1 (TEST-NET-1,
// RFC 5737) is never routed, so if the refusal were missing the dial would
// time out rather than reach anything, and the test would still fail.
func TestPAC145DiallerRefusesNonLoopbackInTests(t *testing.T) {
	const peer = "192.0.2.1:443"
	for name, s := range map[string]*setup{
		"production decision, guard off": newSetup(config.ProductionCalendarEndpoint(), dialHooks{}),
		"stand-in decision, guard on":    newSetup(config.CalendarEndpoint{BaseURL: "http://127.0.0.1:1", Guard: true}, dialHooks{}),
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			conn, err := s.base.DialContext(ctx, "tcp", peer)
			if conn != nil {
				conn.Close()
			}
			if s.guard {
				// The production guard judges first and permits nothing public.
				if !errors.Is(err, ErrPeerRefused) {
					t.Fatalf("dial %s = %v, want ErrPeerRefused", peer, err)
				}
				return
			}
			if !errors.Is(err, testdial.ErrRealHost) {
				t.Fatalf("dial %s = %v, want testdial.ErrRealHost", peer, err)
			}
		})
	}
}

// A private peer the production guard permits is still refused in a test
// binary. Control is called directly, with no socket, so nothing is ever sent
// to 10.255.255.1, whatever the guard does: a real LAN host there is never
// dialled.
func TestPAC145PrivatePeerRefusedInTests(t *testing.T) {
	d := newDialer(true, dialHooks{})
	if d.Control == nil {
		t.Fatal("guarded dialler has no Control")
	}
	if err := d.Control("tcp4", "10.255.255.1:443", nil); !errors.Is(err, testdial.ErrRealHost) {
		t.Fatalf("Control(10.255.255.1:443) = %v, want testdial.ErrRealHost", err)
	}
	if err := d.Control("tcp4", "127.0.0.1:443", nil); err != nil {
		t.Fatalf("Control(127.0.0.1:443) = %v, want nil: loopback stand-ins must still work", err)
	}
}
