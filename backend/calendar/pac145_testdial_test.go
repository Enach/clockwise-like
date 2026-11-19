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

// The guard does not get in the way of the loopback stand-ins every calendar
// test uses: a guarded private (non-loopback) peer the production guard
// permits is still refused in a test binary.
func TestPAC145PrivatePeerRefusedInTests(t *testing.T) {
	s := newSetup(config.CalendarEndpoint{BaseURL: "http://127.0.0.1:1", Guard: true}, dialHooks{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := s.base.DialContext(ctx, "tcp", "10.255.255.1:443")
	if conn != nil {
		conn.Close()
	}
	if !errors.Is(err, testdial.ErrRealHost) {
		t.Fatalf("dial 10.255.255.1:443 = %v, want testdial.ErrRealHost", err)
	}
}
