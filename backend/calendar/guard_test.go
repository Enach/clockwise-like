package calendar

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/Enach/paceday/backend/config"
)

const (
	standInName  = "stand-in.pac43.test"
	publicAddr   = "142.250.185.78"
	metadataAddr = "169.254.169.254"
)

// TestPermittedPeer_Table is AC-7: the connection guard's predicate, judged on
// the literal ip:port the dialler is about to connect to.
func TestPermittedPeer_Table(t *testing.T) {
	cases := []struct {
		ip       string
		permit   bool
		whyNoted string
	}{
		{"127.0.0.1", true, "loopback"},
		{"127.0.0.2", true, "loopback, not just .1"},
		{"127.1.2.3", true, "anywhere in 127/8"},
		{"::1", true, "IPv6 loopback"},
		{"::ffff:127.0.0.1", true, "IPv4-mapped loopback is judged as what it maps to"},
		{"10.1.2.3", true, "RFC 1918 10/8"},
		{"172.16.0.1", true, "RFC 1918 172.16/12, low edge"},
		{"172.31.255.254", true, "RFC 1918 172.16/12, high edge"},
		{"192.168.1.1", true, "RFC 1918 192.168/16"},
		{"fd00::1", true, "IPv6 unique-local"},
		{"169.254.169.254", false, "link-local: instance metadata"},
		{"fe80::1", false, "IPv6 link-local"},
		{"0.0.0.0", false, "unspecified"},
		{"::", false, "IPv6 unspecified"},
		{"172.32.0.1", false, "neighbours 172.16/12 and is public"},
		{"8.8.8.8", false, "public"},
		{"142.250.185.78", false, "public (a Google address)"},
		{"::ffff:8.8.8.8", false, "IPv4-mapped public is judged as what it maps to"},
	}
	for _, tc := range cases {
		t.Run(tc.ip, func(t *testing.T) {
			err := checkPeer(net.JoinHostPort(tc.ip, "443"))
			if tc.permit && err != nil {
				t.Fatalf("%s (%s) refused, want permitted: %v", tc.ip, tc.whyNoted, err)
			}
			if !tc.permit {
				if err == nil {
					t.Fatalf("%s (%s) permitted, want refused", tc.ip, tc.whyNoted)
				}
				if !errors.Is(err, ErrPeerRefused) {
					t.Fatalf("refusal of %s is not ErrPeerRefused: %v", tc.ip, err)
				}
			}
		})
	}
	t.Run("unparseable address is refused", func(t *testing.T) {
		if err := checkPeer("not-an-address"); err == nil {
			t.Fatal("an address the guard cannot judge was permitted")
		}
	})
}

// listEvents runs one calendar operation through the real entry point with a
// bounded context, so a defect that lets a dial through cannot hang the suite.
func listEvents(t *testing.T) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "T"}))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = c.ListEvents(ctx, "primary", time.Now(), time.Now().Add(time.Hour))
	return err
}

// TestGuard_RefusesNonPermittedResolution is AC-8.
func TestGuard_RefusesNonPermittedResolution(t *testing.T) {
	for _, answer := range []string{publicAddr, metadataAddr} {
		t.Run(answer, func(t *testing.T) {
			dns := newDNSDouble(t)
			dns.set(standInName, answer)
			rec := &attemptRecorder{}
			configureForTest(t, guardedStandIn("http://"+standInName+":8080/calendar/v3/"),
				dialHooks{resolver: dns.resolver(), record: rec.record})

			err := listEvents(t)
			if err == nil {
				t.Fatal("calendar operation succeeded against a non-permitted peer")
			}
			if !strings.Contains(err.Error(), answer) || !strings.Contains(err.Error(), "refused") {
				t.Errorf("error does not say the peer %s was refused: %v", answer, err)
			}
			if rec.sawHost(answer) {
				t.Errorf("a connection was attempted to %s: %v", answer, rec.all())
			}
		})
	}
}

// TestGuard_JudgesEveryConnection is AC-9: the same name resolves to a
// permitted address on the first call and a public one on the second.
func TestGuard_JudgesEveryConnection(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	// A pooled connection is not a new connection: without this, call 2 could
	// reuse call 1's connection and pass without the guard being consulted.
	srv.Config.SetKeepAlivesEnabled(false)
	srv.Start()
	defer srv.Close()
	port := srv.Listener.Addr().(*net.TCPAddr).Port

	dns := newDNSDouble(t)
	dns.set(standInName, "127.0.0.1")
	rec := &attemptRecorder{}
	configureForTest(t, guardedStandIn(fmt.Sprintf("http://%s:%d/calendar/v3/", standInName, port)),
		dialHooks{resolver: dns.resolver(), record: rec.record})

	if err := listEvents(t); err != nil {
		t.Fatalf("call 1 (permitted answer) failed: %v", err)
	}
	if hits.Load() != 1 || !rec.saw(fmt.Sprintf("127.0.0.1:%d", port)) {
		t.Fatalf("positive control: listener hits=%d, attempts=%v", hits.Load(), rec.all())
	}

	dns.set(standInName, publicAddr)
	err := listEvents(t)
	if err == nil {
		t.Fatal("call 2 (public answer) succeeded")
	}
	if !errors.Is(err, ErrPeerRefused) && !strings.Contains(err.Error(), "refused") {
		t.Errorf("call 2 failed for the wrong reason: %v", err)
	}
	if rec.sawHost(publicAddr) {
		t.Errorf("a connection was attempted to %s: %v", publicAddr, rec.all())
	}
	if hits.Load() != 1 {
		t.Errorf("listener hits = %d after call 2, want 1", hits.Load())
	}
}

// TestClient_DoesNotFollowRedirect is AC-10: a 3xx is never followed, inside
// the permitted set or outside it.
func TestClient_DoesNotFollowRedirect(t *testing.T) {
	var bHits atomic.Int32
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		bHits.Add(1)
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer b.Close()
	bURL, _ := url.Parse(b.URL)

	cases := []struct {
		name     string
		location string
		notDial  func(*attemptRecorder) bool
	}{
		{"to loopback, inside the permitted set", b.URL + "/calendar/v3/calendars/primary/events",
			func(r *attemptRecorder) bool { return r.saw(bURL.Host) }},
		{"to a name resolving to a public address", "http://elsewhere.pac43.test:8080/calendar/v3/calendars/primary/events",
			func(r *attemptRecorder) bool { return r.sawHost(publicAddr) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bHits.Store(0)
			a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, tc.location, http.StatusFound)
			}))
			defer a.Close()

			dns := newDNSDouble(t)
			dns.set("elsewhere.pac43.test", publicAddr)
			rec := &attemptRecorder{}
			configureForTest(t, guardedStandIn(a.URL+"/calendar/v3/"),
				dialHooks{resolver: dns.resolver(), record: rec.record})

			err := listEvents(t)
			if err == nil {
				t.Fatal("operation succeeded; the redirect was followed")
			}
			if !errors.Is(err, ErrRedirectRefused) {
				t.Errorf("error is not the named redirect refusal: %v", err)
			}
			if bHits.Load() != 0 {
				t.Errorf("redirect target was entered %d time(s)", bHits.Load())
			}
			if tc.notDial(rec) {
				t.Errorf("a second dial was attempted: %v", rec.all())
			}
			if n := len(rec.all()); n != 1 {
				t.Errorf("dial attempts = %d (%v), want exactly 1 (to A)", n, rec.all())
			}
		})
	}
}

// TestClient_GuardOnStillSendsBearer is the in-process half of AC-21 for the
// guarded configuration.
func TestClient_GuardOnStillSendsBearer(t *testing.T) {
	var got atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Store(r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer srv.Close()
	configureForTest(t, guardedStandIn(srv.URL+"/calendar/v3/"), dialHooks{})

	if err := listEvents(t); err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if v, _ := got.Load().(string); v != "Bearer T" {
		t.Errorf("Authorization = %q, want %q", v, "Bearer T")
	}
}

// TestGuard_NameRowsNeverTrusted is AC-6's second half: every host the startup
// check accepted as a name is judged at connect time, so a public resolution
// answer gets no connection attempt.
func TestGuard_NameRowsNeverTrusted(t *testing.T) {
	names := []string{
		"2130706433", "0x7f000001", "017700000001", "127.1", "0x7f.0.0.1",
		"fake-google", "localhost", "evil.example.com",
	}
	for _, host := range names {
		t.Run(host, func(t *testing.T) {
			d, err := config.DecideCalendarEndpoint("http://"+host+":8080/calendar/v3/", "true")
			if err != nil {
				t.Fatalf("startup check refused %q, want accepted as a name: %v", host, err)
			}
			if !d.Guard {
				t.Fatalf("decision for %q has the guard off", host)
			}
			dns := newDNSDouble(t)
			dns.setFallback(publicAddr)
			rec := &attemptRecorder{}
			configureForTest(t, d, dialHooks{resolver: dns.resolver(), record: rec.record})

			err = listEvents(t)
			if err == nil {
				t.Fatal("calendar operation succeeded")
			}
			// It must fail closed before any byte is written: either the
			// guard refused the peer, or the resolver refused the name
			// (all-numeric labels are not domain names to Go's resolver).
			// A server's answer, such as a 401, means a connection was made.
			var dnsErr *net.DNSError
			if !errors.Is(err, ErrPeerRefused) && !errors.As(err, &dnsErr) {
				t.Errorf("failed for the wrong reason (not a guard or resolver refusal): %v", err)
			}
			if rec.sawHost(publicAddr) {
				t.Errorf("a connection was attempted to %s: %v", publicAddr, rec.all())
			}
			// Record what the name actually resolved to, for the PR (plan §7 item 4).
			t.Logf("%q: attempts %v; error: %v", host, rec.all(), err)
		})
	}
}
