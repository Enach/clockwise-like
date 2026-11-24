package outbound

import (
	"errors"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/testdial"
)

// PAC-157: the SMTP dial goes through a net.Dialer whose Control hook applies
// testdial.Check, so inside a test binary SendMail refuses every address that
// is not loopback. 192.0.2.1 (TEST-NET-1, RFC 5737) is never routed, so if the
// refusal were missing the dial would time out instead of reaching anything,
// and this test would still fail — but with the wrong error.
func TestPAC157SendMailRefusesNonLoopbackInTests(t *testing.T) {
	restore := SetForTest(Limits{SMTP: 2 * time.Second})
	defer restore()

	for _, addr := range []string{"192.0.2.1:25", "[2001:db8::1]:587"} {
		err := SendMail(addr, nil, "from@x.test", []string{"to@x.test"}, []byte("hi"))
		if !errors.Is(err, testdial.ErrRealHost) {
			t.Errorf("SendMail(%q) = %v, want testdial.ErrRealHost", addr, err)
		}
	}

	// A name, rather than a literal, is refused too: Control sees the resolved
	// ip:port, and an unresolvable name fails the dial before that.
	if err := SendMail("smtp.example.com:587", nil, "f@x.test", []string{"t@x.test"}, nil); err == nil {
		t.Error("SendMail to a public name succeeded inside a test binary")
	}
}

// The dialler itself: the connect timeout is the SMTP limit, and Control is
// installed and judges addresses without opening a socket, so nothing is sent
// to the private address below even on a machine that has a host there.
func TestPAC157SMTPDialerControl(t *testing.T) {
	d := smtpDialer(7 * time.Second)
	if d.Timeout != 7*time.Second {
		t.Errorf("Timeout = %v, want 7s (the SMTP limit)", d.Timeout)
	}
	if d.Control == nil {
		t.Fatal("no Control in a test binary: the PAC-157 guard would be off")
	}
	for _, tc := range []struct {
		addr string
		ok   bool
	}{
		{"127.0.0.1:25", true},
		{"[::1]:587", true},
		{"10.255.255.1:25", false}, // private, but not loopback
		{"142.250.74.108:25", false},
		{"smtp.example.com:587", false}, // unresolved: cannot be judged
	} {
		err := d.Control("tcp", tc.addr, nil)
		if tc.ok && err != nil {
			t.Errorf("Control(%q) = %v, want nil", tc.addr, err)
		}
		if !tc.ok && !errors.Is(err, testdial.ErrRealHost) {
			t.Errorf("Control(%q) = %v, want testdial.ErrRealHost", tc.addr, err)
		}
	}
}

// The guard does not get in the way of a loopback SMTP server: that path is
// TestSendMailDelivers, which exercises a real send over 127.0.0.1 and would
// fail if Control refused it.
