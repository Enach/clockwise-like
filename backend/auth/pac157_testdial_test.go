package auth

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/testdial"
)

// PAC-157: a Detector built without a resolver does not get the system
// resolver inside a test binary. It gets one that queries nothing and refuses
// every name with an error wrapping testdial.ErrRealHost, so a test that
// reaches DNS unconfigured fails loudly instead of reading a live reply.
func TestPAC157DefaultResolverRefusesInTests(t *testing.T) {
	d := NewDetector(DetectorOptions{})
	if _, ok := d.resolver.(refusingMXResolver); !ok {
		t.Fatalf("resolver = %T, want refusingMXResolver: the PAC-157 guard is off", d.resolver)
	}
	_, err := d.resolver.LookupMX(context.Background(), "example.com.")
	if !errors.Is(err, testdial.ErrRealHost) {
		t.Errorf("LookupMX = %v, want testdial.ErrRealHost", err)
	}
	// The refusal names the query, so a run that trips the guard says which
	// lookup was unconfigured.
	if err == nil || !strings.Contains(err.Error(), "example.com.") {
		t.Errorf("LookupMX error %v does not name the query", err)
	}
}

// Outside a test binary the default is unchanged: production keeps the system
// resolver, as PAC-97 AC-15 requires.
func TestPAC157ProductionDefaultResolverUnchanged(t *testing.T) {
	if got := defaultMXResolver(false); got != MXResolver(net.DefaultResolver) {
		t.Errorf("defaultMXResolver(false) = %T %v, want net.DefaultResolver", got, got)
	}
	if _, ok := defaultMXResolver(true).(refusingMXResolver); !ok {
		t.Errorf("defaultMXResolver(true) = %T, want refusingMXResolver", defaultMXResolver(true))
	}
}

// The refusal travels the documented failure path: the answer is choose, and
// because the failure is not definite it is cached for MXFailureTTL only. No
// query leaves the process, so no domain can be answered google or microsoft
// out of real DNS.
func TestPAC157RefusedLookupAnswersChooseAsAFailure(t *testing.T) {
	clk := newFakeClock()
	d := NewDetector(DetectorOptions{Now: clk.now})

	got := d.mailProvider(context.Background(), "corp.example")
	if got != DetectChoose {
		t.Fatalf("mailProvider = %q, want %q", got, DetectChoose)
	}
	if d.cached() != 1 {
		t.Fatalf("cached = %d, want 1", d.cached())
	}
	// Just before MXFailureTTL the cached failure still answers; just after it
	// has expired, which is what distinguishes it from a definite answer.
	clk.advance(MXFailureTTL - time.Second)
	if got := d.mailProvider(context.Background(), "corp.example"); got != DetectChoose {
		t.Errorf("within the failure TTL: %q, want %q", got, DetectChoose)
	}
	clk.advance(2 * time.Second)
	d.mu.Lock()
	e := d.cache["corp.example"]
	expired := e == nil || !clk.now().Before(e.expires)
	d.mu.Unlock()
	if !expired {
		t.Error("the refusal was cached as a definite answer, not as a failure")
	}
}

// Detect answers choose end to end for a queryable domain with no SSO row,
// with nothing dialled and nothing resolved.
func TestPAC157DetectUnconfiguredIsChoose(t *testing.T) {
	d := NewDetector(DetectorOptions{})
	res, err := d.detect(context.Background(), noSSO, "someone@corp.example")
	if err != nil {
		t.Fatalf("detect = %v", err)
	}
	if res.Type != DetectChoose {
		t.Errorf("type = %q, want %q", res.Type, DetectChoose)
	}
}

// StandInResolver was the package's last raw dialler. Inside a test binary its
// Control refuses a stand-in address that is not loopback; the e2e stand-in,
// which is loopback or a permitted peer, is unaffected. Control is called
// directly, with no socket, so nothing is sent to the addresses below.
func TestPAC157StandInResolverRefusesNonLoopbackInTests(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r := StandInResolver("192.0.2.1:53")
	conn, err := r.Dial(ctx, "udp", "")
	if conn != nil {
		conn.Close()
	}
	if !errors.Is(err, testdial.ErrRealHost) {
		t.Errorf("stand-in dial to 192.0.2.1:53 = %v, want testdial.ErrRealHost", err)
	}

	// A loopback stand-in is still dialled: PAC-97's stand-in test depends on
	// it. The listener makes the connect succeed without any network.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	conn, err = StandInResolver(ln.Addr().String()).Dial(ctx, "tcp", "")
	if err != nil {
		t.Errorf("stand-in dial to loopback = %v, want nil", err)
	}
	if conn != nil {
		conn.Close()
	}
}
