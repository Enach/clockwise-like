package api

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Rate limits for the public booking routes (NEW-booking-rate-limit).
//
//   - POST /api/book/{slug}:       20 requests per client per fixed 10-minute window
//   - GET  /api/book/{slug}/slots: 120 requests per client per fixed 1-minute window
//
// A client is identified exactly as for POST /api/auth/detect (PAC-39,
// clientIP in ratelimit_detect.go): the socket peer, or X-Real-IP only when the
// peer is in TRUSTED_PROXIES; one exact address, no IPv6 prefix grouping. The
// count is per client across all links, and the two budgets are independent.
// The limit is checked and the request counted under one mutex as the
// handler's first step, so a concurrent burst cannot overshoot it, every
// request counts whatever it would have answered, and 429 precedes 404.
//
// Counts live in process memory: the limits are per backend process (N per
// replica, as API-062's residue) and a restart clears them. Expired clients
// are swept on a request at most once per window, so memory stays bounded.
// The values are assumed by the factory, pending owner confirmation (spec
// D-1). Only the counts can be raised, and only by the e2e stack's test-mode
// configuration (config.DecideBookingRateLimits); the windows never change.

const (
	defaultBookingCreateLimit = 20
	defaultBookingSlotsLimit  = 120
	bookingCreateWindow       = 10 * time.Minute
	bookingSlotsWindow        = time.Minute
)

var (
	bookingCreateLimiter = newFixedWindowLimiter(defaultBookingCreateLimit, bookingCreateWindow)
	bookingSlotsLimiter  = newFixedWindowLimiter(defaultBookingSlotsLimit, bookingSlotsWindow)
)

// fixedWindowLimiter allows limit requests per key per fixed window. A key's
// window starts at its first counted request and is not extended by refusals.
type fixedWindowLimiter struct {
	mu        sync.Mutex
	limit     int
	window    time.Duration
	buckets   map[string]*ipBucket
	lastSweep time.Time
	now       func() time.Time
}

func newFixedWindowLimiter(limit int, window time.Duration) *fixedWindowLimiter {
	return &fixedWindowLimiter{limit: limit, window: window, buckets: make(map[string]*ipBucket), now: time.Now}
}

// take counts one request for key. It reports whether the request is allowed
// and, when it is not, how long until the key's window ends.
func (l *fixedWindowLimiter) take(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	if now.Sub(l.lastSweep) >= l.window {
		for k, b := range l.buckets {
			if now.After(b.windowEnd) {
				delete(l.buckets, k)
			}
		}
		l.lastSweep = now
	}

	b, ok := l.buckets[key]
	if !ok || now.After(b.windowEnd) {
		l.buckets[key] = &ipBucket{count: 1, windowEnd: now.Add(l.window)}
		return true, 0
	}
	if b.count >= l.limit {
		return false, b.windowEnd.Sub(now)
	}
	b.count++
	return true, 0
}

// size is the number of clients the limiter currently remembers.
func (l *fixedWindowLimiter) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// reset forgets every client.
func (l *fixedWindowLimiter) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buckets = make(map[string]*ipBucket)
	l.lastSweep = time.Time{}
}

func (l *fixedWindowLimiter) setLimit(n int) {
	if n <= 0 {
		return
	}
	l.mu.Lock()
	l.limit = n
	l.mu.Unlock()
}

// SetPublicBookingLimits replaces the request counts of the public booking
// limiters; a non-positive value leaves that count unchanged. main.go calls it
// only with what config.DecideBookingRateLimits allows (the e2e stack).
func SetPublicBookingLimits(create, slots int) {
	bookingCreateLimiter.setLimit(create)
	bookingSlotsLimiter.setLimit(slots)
}

// retryAfterSeconds is the Retry-After delay-seconds value: wait rounded up to
// whole seconds, never less than 1 and never more than the window.
func retryAfterSeconds(wait, window time.Duration) int {
	secs := int((wait + time.Second - 1) / time.Second)
	if maxSecs := int(window / time.Second); secs > maxSecs {
		secs = maxSecs
	}
	if secs < 1 {
		secs = 1
	}
	return secs
}

// allowPublicBooking counts r against l. When the client is over the limit it
// writes the 429 (JSON error envelope and Retry-After) and returns false.
func allowPublicBooking(w http.ResponseWriter, r *http.Request, l *fixedWindowLimiter) bool {
	ok, wait := l.take(clientIP(r, trustedProxies))
	if ok {
		return true
	}
	w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(wait, l.window)))
	writeError(w, "rate limit exceeded", http.StatusTooManyRequests)
	return false
}
