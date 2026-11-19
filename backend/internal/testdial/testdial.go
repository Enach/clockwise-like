// Package testdial keeps test binaries off real hosts (PAC-145).
//
// The calendar transport (backend/calendar) and the identity-provider client
// (backend/auth, step-up) set Proxy=nil on purpose, so the PAC-139 deny-all
// proxy that scripts/test-backend-in-container.sh points HTTP(S)_PROXY at
// never sees their connections. Their own connection guards only apply when a
// deployment declares stand-ins, and they permit private ranges. Without this
// package, a test that reached either client unconfigured would dial the
// production provider directly.
//
// Both dialers call Check from their net.Dialer Control function, after
// resolution and immediately before connect, with the literal ip:port about
// to be used. Inside a binary built by `go test` (testing.Testing), Check
// refuses every address that is not loopback. In any other binary it returns
// nil and the dialers install no Control function for it at all, so production
// connections are unchanged.
//
// testing.Testing is reported by the linker, not by the environment or a build
// tag: plain `go test` in any package turns the check on with no flag, script
// or variable to remember, and nothing an operator sets can turn it on (or
// off) in a production binary.
package testdial

import (
	"errors"
	"fmt"
	"net/netip"
	"testing"
)

// ErrRealHost is returned, wrapped, when a test binary tries to connect to an
// address that is not loopback.
var ErrRealHost = errors.New("testdial: a test binary may only connect to loopback")

// active is fixed for the life of the process.
var active = testing.Testing()

// Active reports whether this binary was built by `go test`. The dialers use
// it to decide whether to install a Control function for Check.
func Active() bool { return active }

// Check judges the literal ip:port a connection is about to use. It returns
// nil outside a test binary.
func Check(address string) error {
	return check(active, address)
}

func check(on bool, address string) error {
	if !on {
		return nil
	}
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%w: cannot judge %q: %v", ErrRealHost, address, err)
	}
	if !ap.Addr().Unmap().IsLoopback() {
		return fmt.Errorf("%w: refused %s", ErrRealHost, address)
	}
	return nil
}
