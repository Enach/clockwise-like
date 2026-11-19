package testdial

import (
	"errors"
	"testing"
)

func TestActiveInTestBinary(t *testing.T) {
	if !Active() {
		t.Fatal("Active() = false inside a go test binary; the PAC-145 guard would be off")
	}
}

func TestCheck(t *testing.T) {
	for _, tc := range []struct {
		addr string
		ok   bool
	}{
		{"127.0.0.1:443", true},
		{"127.8.9.10:80", true},
		{"[::1]:443", true},
		{"[::ffff:127.0.0.1]:443", true},
		{"142.250.74.106:443", false}, // a public address (Google)
		{"[2a00:1450:4007:80e::200a]:443", false},
		{"10.0.0.5:443", false},    // private, but not loopback
		{"192.168.1.1:443", false}, // private, but not loopback
		{"[fe80::1%eth0]:443", false},
		{"0.0.0.0:443", false},
		{"www.googleapis.com:443", false}, // unresolved: cannot be judged
		{"garbage", false},
	} {
		err := Check(tc.addr)
		if tc.ok && err != nil {
			t.Errorf("Check(%q) = %v, want nil", tc.addr, err)
		}
		if !tc.ok && !errors.Is(err, ErrRealHost) {
			t.Errorf("Check(%q) = %v, want ErrRealHost", tc.addr, err)
		}
	}
}

// Outside a test binary the check is a no-op: production behaviour does not
// change.
func TestCheckOffOutsideTests(t *testing.T) {
	for _, addr := range []string{"142.250.74.106:443", "garbage", "10.0.0.5:443"} {
		if err := check(false, addr); err != nil {
			t.Errorf("check(false, %q) = %v, want nil", addr, err)
		}
	}
}
