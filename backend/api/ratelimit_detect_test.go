package api

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// PAC-39 / API-062: the /api/auth/detect limiter keys on the connection's
// RemoteAddr, and only believes X-Real-IP when that RemoteAddr is a configured
// trusted proxy. X-Forwarded-For is never used.

// resetDetectLimiter isolates each test from the package-level limiter state.
func resetDetectLimiter(t *testing.T, trusted []*net.IPNet) {
	t.Helper()
	detectLimiterMu.Lock()
	detectLimiter = make(map[string]*ipBucket)
	detectLimiterMu.Unlock()
	prev := trustedProxies
	trustedProxies = trusted
	t.Cleanup(func() {
		trustedProxies = prev
		detectLimiterMu.Lock()
		detectLimiter = make(map[string]*ipBucket)
		detectLimiterMu.Unlock()
	})
}

// detectStatus posts an empty body: the limiter runs before the body is read,
// so an allowed request is a 400 and a throttled one is a 429. No DB needed.
func detectStatus(remoteAddr string, headers map[string]string) int {
	h := &ssoHandlers{}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/detect", strings.NewReader("{}"))
	req.RemoteAddr = remoteAddr
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.detect(w, req)
	return w.Code
}

func TestDetectLimiter_RotatingXForwardedFor_StillThrottled(t *testing.T) {
	resetDetectLimiter(t, nil)
	for i := 0; i < detectRateLimit; i++ {
		code := detectStatus("203.0.113.7:4000", map[string]string{
			"X-Forwarded-For": fmt.Sprintf("198.51.100.%d", i+1),
		})
		if code == http.StatusTooManyRequests {
			t.Fatalf("request %d throttled early", i+1)
		}
	}
	code := detectStatus("203.0.113.7:4001", map[string]string{"X-Forwarded-For": "198.51.100.250"})
	if code != http.StatusTooManyRequests {
		t.Fatalf("request %d status = %d, want 429", detectRateLimit+1, code)
	}
}

func TestDetectLimiter_RotatingXRealIPFromUntrustedPeer_StillThrottled(t *testing.T) {
	resetDetectLimiter(t, nil)
	for i := 0; i < detectRateLimit; i++ {
		detectStatus("203.0.113.7:4000", map[string]string{"X-Real-IP": fmt.Sprintf("198.51.100.%d", i+1)})
	}
	if code := detectStatus("203.0.113.7:4000", map[string]string{"X-Real-IP": "198.51.100.250"}); code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", code)
	}
}

func TestDetectLimiter_DistinctRemoteAddrs_IndependentBuckets(t *testing.T) {
	resetDetectLimiter(t, nil)
	for i := 0; i < detectRateLimit; i++ {
		detectStatus("203.0.113.7:4000", nil)
	}
	if code := detectStatus("203.0.113.8:4000", nil); code == http.StatusTooManyRequests {
		t.Fatal("a different client was throttled by another client's bucket")
	}
}

func TestDetectLimiter_TrustedProxy_KeysOnXRealIP(t *testing.T) {
	resetDetectLimiter(t, parseTrustedProxies("10.0.0.0/8"))
	proxy := "10.1.2.3:5555"
	for i := 0; i < detectRateLimit; i++ {
		detectStatus(proxy, map[string]string{"X-Real-IP": "198.51.100.1"})
	}
	if code := detectStatus(proxy, map[string]string{"X-Real-IP": "198.51.100.1"}); code != http.StatusTooManyRequests {
		t.Fatalf("same client via proxy: status = %d, want 429", code)
	}
	// A different end user behind the same proxy has their own bucket.
	if code := detectStatus(proxy, map[string]string{"X-Real-IP": "198.51.100.2"}); code == http.StatusTooManyRequests {
		t.Fatal("distinct client behind trusted proxy was throttled")
	}
}

func TestDetectLimiter_TrustedProxy_IgnoresXForwardedFor(t *testing.T) {
	resetDetectLimiter(t, parseTrustedProxies("10.0.0.0/8"))
	for i := 0; i < detectRateLimit; i++ {
		detectStatus("10.1.2.3:5555", map[string]string{
			"X-Real-IP":       "198.51.100.1",
			"X-Forwarded-For": fmt.Sprintf("192.0.2.%d, 198.51.100.1", i+1),
		})
	}
	code := detectStatus("10.1.2.3:5555", map[string]string{
		"X-Real-IP": "198.51.100.1", "X-Forwarded-For": "192.0.2.99, 198.51.100.1",
	})
	if code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", code)
	}
}

func TestClientIP(t *testing.T) {
	trusted := parseTrustedProxies("10.0.0.0/8, 127.0.0.1 ,::1,garbage")
	cases := []struct {
		name, remote, xri, want string
	}{
		{"untrusted peer ignores header", "203.0.113.7:80", "1.2.3.4", "203.0.113.7"},
		{"trusted peer uses X-Real-IP", "10.9.9.9:80", "1.2.3.4", "1.2.3.4"},
		{"trusted single IP", "127.0.0.1:80", "1.2.3.4", "1.2.3.4"},
		{"trusted IPv6 single IP", "[::1]:80", "2001:db8::1", "2001:db8::1"},
		{"trusted peer, missing header", "10.9.9.9:80", "", "10.9.9.9"},
		{"trusted peer, unparseable header", "10.9.9.9:80", "not-an-ip", "10.9.9.9"},
		{"remote without port", "203.0.113.7", "", "203.0.113.7"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = c.remote
			if c.xri != "" {
				req.Header.Set("X-Real-IP", c.xri)
			}
			if got := clientIP(req, trusted); got != c.want {
				t.Fatalf("clientIP = %q, want %q", got, c.want)
			}
		})
	}
}

func TestParseTrustedProxies_EmptyMeansNone(t *testing.T) {
	if got := parseTrustedProxies(""); len(got) != 0 {
		t.Fatalf("got %d networks, want 0", len(got))
	}
	if got := parseTrustedProxies(" , garbage "); len(got) != 0 {
		t.Fatalf("got %d networks, want 0", len(got))
	}
}

func TestDetectLimiter_ExpiredBucketsAreSwept(t *testing.T) {
	resetDetectLimiter(t, nil)
	past := time.Now().Add(-2 * time.Minute)
	detectLimiterMu.Lock()
	for i := 0; i < 50; i++ {
		detectLimiter[fmt.Sprintf("192.0.2.%d", i)] = &ipBucket{count: 5, windowEnd: past}
	}
	lastDetectSweep = past
	detectLimiterMu.Unlock()

	if !allowDetect("198.51.100.1") {
		t.Fatal("fresh client denied")
	}
	detectLimiterMu.Lock()
	n := len(detectLimiter)
	detectLimiterMu.Unlock()
	if n != 1 {
		t.Fatalf("limiter holds %d buckets after sweep, want 1", n)
	}
}
