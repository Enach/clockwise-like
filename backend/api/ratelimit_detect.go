package api

import (
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Rate limiter for POST /api/auth/detect (fixed 1-minute window per client IP).
//
// PAC-39 / API-062: the client IP is the connection's RemoteAddr. X-Real-IP is
// believed only when RemoteAddr is a proxy listed in TRUSTED_PROXIES (comma-
// separated IPs or CIDRs; empty means trust nobody). X-Forwarded-For is never
// read: its left-most entry is whatever the client wrote. The bundled nginx
// sets X-Real-IP to $remote_addr, overwriting any client-supplied value.
//
// The limiter is still per process: 20/min is per replica (API-062, not fixed
// here).

type ipBucket struct {
	count     int
	windowEnd time.Time
}

var (
	detectLimiterMu sync.Mutex
	detectLimiter   = make(map[string]*ipBucket)
	lastDetectSweep time.Time

	trustedProxies = parseTrustedProxies(os.Getenv("TRUSTED_PROXIES"))
)

const detectRateLimit = 20

func allowDetect(ip string) bool {
	detectLimiterMu.Lock()
	defer detectLimiterMu.Unlock()

	now := time.Now()
	// Drop expired buckets at most once a window so the map cannot grow
	// without bound (API-062).
	if now.Sub(lastDetectSweep) >= time.Minute {
		for k, b := range detectLimiter {
			if now.After(b.windowEnd) {
				delete(detectLimiter, k)
			}
		}
		lastDetectSweep = now
	}

	b, ok := detectLimiter[ip]
	if !ok || now.After(b.windowEnd) {
		detectLimiter[ip] = &ipBucket{count: 1, windowEnd: now.Add(time.Minute)}
		return true
	}
	if b.count >= detectRateLimit {
		return false
	}
	b.count++
	return true
}

// parseTrustedProxies parses a comma-separated list of IPs and CIDRs.
// Unparseable entries are ignored, so a typo trusts less, never more.
func parseTrustedProxies(v string) []*net.IPNet {
	nets := make([]*net.IPNet, 0)
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !strings.Contains(part, "/") {
			ip := net.ParseIP(part)
			if ip == nil {
				continue
			}
			bits := 128
			if ip.To4() != nil {
				ip, bits = ip.To4(), 32
			}
			nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		if _, n, err := net.ParseCIDR(part); err == nil {
			nets = append(nets, n)
		}
	}
	return nets
}

// clientIP returns the IP the limiter keys on: RemoteAddr, or X-Real-IP when
// RemoteAddr is a trusted proxy and the header holds a valid IP.
func clientIP(r *http.Request, trusted []*net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(host)
	if peer == nil {
		return host
	}
	for _, n := range trusted {
		if n.Contains(peer) {
			if xri := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); xri != nil {
				return xri.String()
			}
			break
		}
	}
	return peer.String()
}
