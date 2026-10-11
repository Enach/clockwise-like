package calendar

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"syscall"
	"time"

	"github.com/Enach/paceday/backend/config"
)

// ErrPeerRefused is returned, wrapped, when the connection guard refuses the
// address a calendar connection was about to use.
var ErrPeerRefused = errors.New("calendar: peer address refused by the connection guard")

// dialHooks are the two test seams of plan §1.1. Both are nil in every
// production build.
type dialHooks struct {
	// resolver replaces the system resolver.
	resolver *net.Resolver
	// record is called after the guard, with each address the guard let
	// through, immediately before connect.
	record func(network, address string)
}

// checkPeer judges the literal ip:port a connection is about to use. Anything
// that cannot be judged is refused.
func checkPeer(address string) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%w: cannot judge %q: %v", ErrPeerRefused, address, err)
	}
	if !config.PermittedPeer(ap.Addr()) {
		return fmt.Errorf("%w: %s is outside the permitted ranges", ErrPeerRefused, ap.Addr())
	}
	return nil
}

// newDialer returns the dialler every calendar connection goes through. It
// keeps the default dialler's connect timeout and keep-alive.
//
// When guard is on, Control judges the peer address. Go calls Control after
// resolution and immediately before the connect syscall, with the address
// actually being used, so the judgement is made per connection, on every hop,
// from the answer that connection is using: a name that resolves to a
// permitted address now and a public one later is refused later. In
// production guard is off and, with no test hook, Control is nil.
func newDialer(guard bool, h dialHooks) *net.Dialer {
	d := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Resolver:  h.resolver,
	}
	if !guard && h.record == nil {
		return d
	}
	d.Control = func(network, address string, _ syscall.RawConn) error {
		if guard {
			if err := checkPeer(address); err != nil {
				return err
			}
		}
		if h.record != nil {
			h.record(network, address)
		}
		return nil
	}
	return d
}
