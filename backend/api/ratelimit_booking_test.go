package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// NEW-booking-rate-limit: per-client limits on POST /api/book/{slug} (20 per
// fixed 10-minute window) and GET /api/book/{slug}/slots (120 per fixed
// 1-minute window). Every backend test shares one httptest peer address, so
// each test that touches the public booking routes resets both limiters
// (spec §6, Q6): setupPublicBookingRoutes calls resetBookingLimiters.

// resetBookingLimiters isolates a test from the package-level limiter state
// and restores the production limits and clock afterwards.
func resetBookingLimiters(t *testing.T) {
	t.Helper()
	restore := func() {
		for _, l := range []*fixedWindowLimiter{bookingCreateLimiter, bookingSlotsLimiter} {
			l.reset()
		}
		bookingCreateLimiter.mu.Lock()
		bookingCreateLimiter.limit = defaultBookingCreateLimit
		bookingCreateLimiter.mu.Unlock()
		bookingSlotsLimiter.mu.Lock()
		bookingSlotsLimiter.limit = defaultBookingSlotsLimit
		bookingSlotsLimiter.mu.Unlock()
	}
	restore()
	t.Cleanup(restore)
}

// fakeClock is a settable clock for the limiters.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// useFakeClock makes both public booking limiters read the returned clock for
// the rest of the test.
func useFakeClock(t *testing.T) *fakeClock {
	t.Helper()
	c := &fakeClock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	for _, l := range []*fixedWindowLimiter{bookingCreateLimiter, bookingSlotsLimiter} {
		l.mu.Lock()
		l.now = c.now
		l.mu.Unlock()
	}
	t.Cleanup(func() {
		for _, l := range []*fixedWindowLimiter{bookingCreateLimiter, bookingSlotsLimiter} {
			l.mu.Lock()
			l.now = time.Now
			l.mu.Unlock()
		}
	})
	return c
}

// countingBookingFlow is a goroutine-safe BookingFlow that counts the bookings
// it confirms (each one stands for a written booking).
type countingBookingFlow struct {
	confirmed atomic.Int64
	slots     []engine.AvailableSlot
}

func (f *countingBookingFlow) CollectiveSlots(_ context.Context, _ *storage.SchedulingLink, _ time.Time, _ int) ([]engine.AvailableSlot, error) {
	return f.slots, nil
}

func (f *countingBookingFlow) ConfirmBooking(_ context.Context, link *storage.SchedulingLink, name, email string, start, end time.Time, notes string) (*storage.Booking, error) {
	f.confirmed.Add(1)
	return &storage.Booking{ID: uuid.New(), LinkID: link.ID, BookerName: name, BookerEmail: email, StartTime: start, EndTime: end, Notes: notes}, nil
}

type limitedResponse struct {
	code       int
	body       []byte
	retryAfter string
	ctype      string
}

func doPublic(t *testing.T, h http.Handler, method, path, body, remoteAddr string, headers map[string]string) limitedResponse {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.RemoteAddr = remoteAddr
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return limitedResponse{code: w.Code, body: w.Body.Bytes(), retryAfter: w.Header().Get("Retry-After"), ctype: w.Header().Get("Content-Type")}
}

func bookingBody(start time.Time) string {
	return fmt.Sprintf(`{"name":"Alice","email":"alice@example.com","start":"%s","duration_minutes":30}`, start.Format(time.RFC3339))
}

// assertRateLimited checks the AC-1/AC-7 refusal: 429, JSON body
// {"error":"rate limit exceeded"} and nothing else, Retry-After an integer in
// [1, maxSeconds].
func assertRateLimited(t *testing.T, got limitedResponse, maxSeconds int) int {
	t.Helper()
	if got.code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body: %s", got.code, got.body)
	}
	if got.ctype != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got.ctype)
	}
	var body map[string]any
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatalf("429 body is not JSON: %v (%s)", err, got.body)
	}
	if len(body) != 1 || body["error"] != "rate limit exceeded" {
		t.Fatalf("429 body = %s, want exactly {\"error\":\"rate limit exceeded\"}", got.body)
	}
	secs, err := strconv.Atoi(got.retryAfter)
	if err != nil {
		t.Fatalf("Retry-After = %q, want an integer", got.retryAfter)
	}
	if secs < 1 || secs > maxSeconds {
		t.Fatalf("Retry-After = %d, want 1..%d", secs, maxSeconds)
	}
	return secs
}

const (
	peerA = "203.0.113.10:5000"
	peerB = "203.0.113.11:5000"
)

// --- the limiter itself -----------------------------------------------------

func TestFixedWindowLimiter_RetryAfterIsSecondsLeftRoundedUp(t *testing.T) {
	c := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	l := newFixedWindowLimiter(2, 10*time.Minute)
	l.now = c.now
	for i := 0; i < 2; i++ {
		if ok, _ := l.take("k"); !ok {
			t.Fatalf("take %d refused", i+1)
		}
	}
	c.advance(90*time.Second + 500*time.Millisecond)
	ok, wait := l.take("k")
	if ok {
		t.Fatal("third take allowed, want refused")
	}
	// 600s - 90.5s = 509.5s, rounded up.
	if got := retryAfterSeconds(wait, l.window); got != 510 {
		t.Fatalf("Retry-After = %d, want 510", got)
	}
	// Refusals do not extend the window.
	c.advance(509 * time.Second)
	if ok, _ := l.take("k"); ok {
		t.Fatal("take before the window ends allowed")
	}
	c.advance(time.Second)
	if ok, _ := l.take("k"); !ok {
		t.Fatal("take after the window ended refused")
	}
}

func TestRetryAfterSeconds_Bounds(t *testing.T) {
	cases := []struct {
		wait, window time.Duration
		want         int
	}{
		{0, time.Minute, 1},
		{-time.Second, time.Minute, 1},
		{time.Millisecond, time.Minute, 1},
		{59*time.Second + time.Millisecond, time.Minute, 60},
		{2 * time.Minute, time.Minute, 60},
		{10 * time.Minute, 10 * time.Minute, 600},
	}
	for _, tc := range cases {
		if got := retryAfterSeconds(tc.wait, tc.window); got != tc.want {
			t.Errorf("retryAfterSeconds(%v, %v) = %d, want %d", tc.wait, tc.window, got, tc.want)
		}
	}
}

func TestPublicBookingLimiters_ProductionValues(t *testing.T) {
	resetBookingLimiters(t)
	if bookingCreateLimiter.limit != 20 || bookingCreateLimiter.window != 10*time.Minute {
		t.Fatalf("booking limiter = %d per %v, want 20 per 10m", bookingCreateLimiter.limit, bookingCreateLimiter.window)
	}
	if bookingSlotsLimiter.limit != 120 || bookingSlotsLimiter.window != time.Minute {
		t.Fatalf("slots limiter = %d per %v, want 120 per 1m", bookingSlotsLimiter.limit, bookingSlotsLimiter.window)
	}
}

func TestSetPublicBookingLimits_RaisesCountsOnly(t *testing.T) {
	resetBookingLimiters(t)
	SetPublicBookingLimits(500, 2000)
	if bookingCreateLimiter.limit != 500 || bookingSlotsLimiter.limit != 2000 {
		t.Fatalf("limits = %d/%d, want 500/2000", bookingCreateLimiter.limit, bookingSlotsLimiter.limit)
	}
	if bookingCreateLimiter.window != 10*time.Minute || bookingSlotsLimiter.window != time.Minute {
		t.Fatal("SetPublicBookingLimits changed a window")
	}
	SetPublicBookingLimits(0, -1)
	if bookingCreateLimiter.limit != 500 || bookingSlotsLimiter.limit != 2000 {
		t.Fatalf("non-positive values changed the limits to %d/%d", bookingCreateLimiter.limit, bookingSlotsLimiter.limit)
	}
}

// AC-8: sweeps run on requests, at most once per window, and drop every
// expired client while keeping live ones. Proven for each limiter.
func TestPublicBookingLimiters_ExpiredClientsAreSwept(t *testing.T) {
	for _, tc := range []struct {
		name string
		l    func() *fixedWindowLimiter
	}{
		{"booking", func() *fixedWindowLimiter { return bookingCreateLimiter }},
		{"slots", func() *fixedWindowLimiter { return bookingSlotsLimiter }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetBookingLimiters(t)
			c := useFakeClock(t)
			l := tc.l()
			for i := 0; i < 1000; i++ {
				l.take(fmt.Sprintf("198.51.100.%d-%d", i%250, i))
			}
			if got := l.size(); got != 1000 {
				t.Fatalf("size = %d, want 1000", got)
			}
			// Half a window later a live client arrives; nothing has expired.
			c.advance(l.window / 2)
			l.take("live")
			// One full window after the first sweep, every early client has
			// expired; "live" has not.
			c.advance(l.window/2 + time.Second)
			l.take("trigger")
			if got := l.size(); got != 2 {
				t.Fatalf("size after sweep = %d, want 2 (live + trigger)", got)
			}
			l.mu.Lock()
			_, liveKept := l.buckets["live"]
			l.mu.Unlock()
			if !liveKept {
				t.Fatal("sweep dropped a live client")
			}
		})
	}
}

// --- the handlers -----------------------------------------------------------

// AC-1, AC-2: the 21st booking in the window is refused with no booking
// written; after the window it is processed again.
func TestCreateBooking_RateLimited(t *testing.T) {
	ownerID := createTestUser(t, "rl-create-owner@example.com")
	link := createPublicBookingLink(t, ownerID, "rl-create", "RL Create", 0, "reusable", nil)
	eng := &countingBookingFlow{}
	r := setupPublicBookingRoutes(t, eng)
	c := useFakeClock(t)

	start := futureBookingStart()
	for i := 0; i < 20; i++ {
		got := doPublic(t, r, http.MethodPost, "/api/book/"+link.Slug, bookingBody(start.Add(time.Duration(i)*time.Hour)), peerA, nil)
		if got.code != http.StatusCreated {
			t.Fatalf("booking %d: status = %d, want 201; body: %s", i+1, got.code, got.body)
		}
	}
	c.advance(4 * time.Minute)
	got := doPublic(t, r, http.MethodPost, "/api/book/"+link.Slug, bookingBody(start.Add(21*time.Hour)), peerA, nil)
	secs := assertRateLimited(t, got, 600)
	if secs != 360 {
		t.Fatalf("Retry-After = %d, want 360 (6 minutes left)", secs)
	}
	if n := eng.confirmed.Load(); n != 20 {
		t.Fatalf("bookings written = %d, want 20 (none for the refused request)", n)
	}

	c.advance(6*time.Minute + time.Second)
	got = doPublic(t, r, http.MethodPost, "/api/book/"+link.Slug, bookingBody(start.Add(21*time.Hour)), peerA, nil)
	if got.code != http.StatusCreated {
		t.Fatalf("after the window: status = %d, want 201; body: %s", got.code, got.body)
	}
}

// AC-1 (D-4): every request counts, whatever its outcome.
func TestCreateBooking_EveryOutcomeCounts(t *testing.T) {
	ownerID := createTestUser(t, "rl-outcomes-owner@example.com")
	link := createPublicBookingLink(t, ownerID, "rl-outcomes", "RL Outcomes", 0, "reusable", nil)
	r := setupPublicBookingRoutes(t, &countingBookingFlow{})

	for i := 0; i < 20; i++ {
		var got limitedResponse
		switch i % 3 {
		case 0:
			got = doPublic(t, r, http.MethodPost, "/api/book/"+link.Slug, `{}`, peerA, nil)
		case 1:
			got = doPublic(t, r, http.MethodPost, "/api/book/does-not-exist", `{}`, peerA, nil)
		default:
			got = doPublic(t, r, http.MethodPost, "/api/book/"+link.Slug, `not json`, peerA, nil)
		}
		if got.code == http.StatusTooManyRequests {
			t.Fatalf("request %d throttled early", i+1)
		}
	}
	assertRateLimited(t, doPublic(t, r, http.MethodPost, "/api/book/"+link.Slug, `{}`, peerA, nil), 600)
}

// AC-3: another client is unaffected.
func TestCreateBooking_OtherClientUnaffected(t *testing.T) {
	ownerID := createTestUser(t, "rl-other-owner@example.com")
	link := createPublicBookingLink(t, ownerID, "rl-other", "RL Other", 0, "reusable", nil)
	r := setupPublicBookingRoutes(t, &countingBookingFlow{})

	for i := 0; i < 20; i++ {
		doPublic(t, r, http.MethodPost, "/api/book/"+link.Slug, `{}`, peerA, nil)
	}
	assertRateLimited(t, doPublic(t, r, http.MethodPost, "/api/book/"+link.Slug, `{}`, peerA, nil), 600)
	got := doPublic(t, r, http.MethodPost, "/api/book/"+link.Slug, bookingBody(futureBookingStart()), peerB, nil)
	if got.code != http.StatusCreated {
		t.Fatalf("client B: status = %d, want 201; body: %s", got.code, got.body)
	}
}

// AC-4: forged forwarding headers from an untrusted peer are ignored; a
// trusted proxy's X-Real-IP is believed.
func TestCreateBooking_ClientIdentification(t *testing.T) {
	ownerID := createTestUser(t, "rl-ident-owner@example.com")
	link := createPublicBookingLink(t, ownerID, "rl-ident", "RL Ident", 0, "reusable", nil)
	r := setupPublicBookingRoutes(t, &countingBookingFlow{})
	path := "/api/book/" + link.Slug

	t.Run("untrusted peer, rotating headers", func(t *testing.T) {
		resetBookingLimiters(t)
		resetDetectLimiter(t, nil)
		for i := 0; i < 20; i++ {
			got := doPublic(t, r, http.MethodPost, path, `{}`, peerA, map[string]string{
				"X-Real-IP":       fmt.Sprintf("198.51.100.%d", i+1),
				"X-Forwarded-For": fmt.Sprintf("192.0.2.%d", i+1),
			})
			if got.code == http.StatusTooManyRequests {
				t.Fatalf("request %d throttled early", i+1)
			}
		}
		assertRateLimited(t, doPublic(t, r, http.MethodPost, path, `{}`, peerA, map[string]string{"X-Real-IP": "198.51.100.250"}), 600)
	})

	t.Run("trusted proxy, X-Real-IP believed", func(t *testing.T) {
		resetBookingLimiters(t)
		_, proxyNet, _ := net.ParseCIDR("10.0.0.0/8")
		resetDetectLimiter(t, []*net.IPNet{proxyNet})
		proxy := "10.1.2.3:7000"
		for i := 0; i < 20; i++ {
			doPublic(t, r, http.MethodPost, path, `{}`, proxy, map[string]string{"X-Real-IP": "198.51.100.7"})
		}
		assertRateLimited(t, doPublic(t, r, http.MethodPost, path, `{}`, proxy, map[string]string{"X-Real-IP": "198.51.100.7"}), 600)
		// A different real client behind the same proxy is its own client.
		got := doPublic(t, r, http.MethodPost, path, `{}`, proxy, map[string]string{"X-Real-IP": "198.51.100.8"})
		if got.code == http.StatusTooManyRequests {
			t.Fatal("a second client behind the trusted proxy was throttled")
		}
	})
}

// AC-5: the limit is checked before the link lookup.
func TestCreateBooking_LimitPrecedesLookup(t *testing.T) {
	r := setupPublicBookingRoutes(t, &countingBookingFlow{})
	for i := 0; i < 20; i++ {
		got := doPublic(t, r, http.MethodPost, "/api/book/rl-no-such-slug", `{}`, peerA, nil)
		if got.code != http.StatusNotFound {
			t.Fatalf("request %d: status = %d, want 404", i+1, got.code)
		}
	}
	assertRateLimited(t, doPublic(t, r, http.MethodPost, "/api/book/rl-no-such-slug", `{}`, peerA, nil), 600)
}

// AC-6: the two budgets are independent.
func TestPublicBooking_BudgetsAreIndependent(t *testing.T) {
	ownerID := createTestUser(t, "rl-budgets-owner@example.com")
	link := createPublicBookingLink(t, ownerID, "rl-budgets", "RL Budgets", 0, "reusable", nil)
	start := futureBookingStart()
	eng := &countingBookingFlow{slots: []engine.AvailableSlot{{Start: start, End: start.Add(30 * time.Minute)}}}
	r := setupPublicBookingRoutes(t, eng)
	slotsPath := fmt.Sprintf("/api/book/%s/slots?date=%s", link.Slug, start.Format(time.DateOnly))

	for i := 0; i < 20; i++ {
		doPublic(t, r, http.MethodPost, "/api/book/"+link.Slug, `{}`, peerA, nil)
	}
	assertRateLimited(t, doPublic(t, r, http.MethodPost, "/api/book/"+link.Slug, `{}`, peerA, nil), 600)
	if got := doPublic(t, r, http.MethodGet, slotsPath, "", peerA, nil); got.code != http.StatusOK {
		t.Fatalf("slots after booking budget spent: status = %d, want 200; body: %s", got.code, got.body)
	}

	for i := 0; i < 120; i++ {
		doPublic(t, r, http.MethodGet, slotsPath, "", peerB, nil)
	}
	assertRateLimited(t, doPublic(t, r, http.MethodGet, slotsPath, "", peerB, nil), 60)
	if got := doPublic(t, r, http.MethodPost, "/api/book/"+link.Slug, bookingBody(start), peerB, nil); got.code != http.StatusCreated {
		t.Fatalf("booking after slot budget spent: status = %d, want 201; body: %s", got.code, got.body)
	}
}

// AC-7: the 121st slot listing in the minute is refused, with or without a
// date, and before the link lookup.
func TestGetSlots_RateLimited(t *testing.T) {
	ownerID := createTestUser(t, "rl-slots-owner@example.com")
	link := createPublicBookingLink(t, ownerID, "rl-slots", "RL Slots", 0, "reusable", nil)
	r := setupPublicBookingRoutes(t, &countingBookingFlow{})
	c := useFakeClock(t)
	day := futureBookingStart().Format(time.DateOnly)

	for i := 0; i < 120; i++ {
		path := "/api/book/" + link.Slug + "/slots"
		if i%2 == 0 {
			path += "?date=" + day
		}
		if got := doPublic(t, r, http.MethodGet, path, "", peerA, nil); got.code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200; body: %s", i+1, got.code, got.body)
		}
	}
	c.advance(15 * time.Second)
	secs := assertRateLimited(t, doPublic(t, r, http.MethodGet, "/api/book/"+link.Slug+"/slots", "", peerA, nil), 60)
	if secs != 45 {
		t.Fatalf("Retry-After = %d, want 45", secs)
	}
	assertRateLimited(t, doPublic(t, r, http.MethodGet, "/api/book/rl-no-such-slug/slots?date="+day, "", peerA, nil), 60)

	c.advance(46 * time.Second)
	if got := doPublic(t, r, http.MethodGet, "/api/book/"+link.Slug+"/slots?date="+day, "", peerA, nil); got.code != http.StatusOK {
		t.Fatalf("after the window: status = %d, want 200", got.code)
	}
}

// AC-14: a concurrent burst cannot overshoot the booking limit.
func TestCreateBooking_ConcurrentBurstCannotOvershoot(t *testing.T) {
	ownerID := createTestUser(t, "rl-burst-owner@example.com")
	link := createPublicBookingLink(t, ownerID, "rl-burst", "RL Burst", 0, "reusable", nil)
	eng := &countingBookingFlow{}
	r := setupPublicBookingRoutes(t, eng)

	const k = 60
	start := futureBookingStart()
	results := make([]limitedResponse, k)
	var wg sync.WaitGroup
	for i := 0; i < k; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = doPublic(t, r, http.MethodPost, "/api/book/"+link.Slug, bookingBody(start.Add(time.Duration(i)*time.Hour)), peerA, nil)
		}(i)
	}
	wg.Wait()

	created, limited, other := 0, 0, 0
	for _, res := range results {
		switch res.code {
		case http.StatusCreated:
			created++
		case http.StatusTooManyRequests:
			limited++
			assertRateLimited(t, res, 600)
		default:
			other++
		}
	}
	if created+other > 20 {
		t.Fatalf("%d requests got past the limiter, want at most 20", created+other)
	}
	if limited < k-20 {
		t.Fatalf("%d answered 429, want at least %d", limited, k-20)
	}
	if n := int(eng.confirmed.Load()); n != created || n > 20 {
		t.Fatalf("bookings written = %d, 201s = %d; want equal and at most 20", n, created)
	}
}

// AC-15: a concurrent burst cannot overshoot the slot-listing limit.
func TestGetSlots_ConcurrentBurstCannotOvershoot(t *testing.T) {
	ownerID := createTestUser(t, "rl-slotburst-owner@example.com")
	link := createPublicBookingLink(t, ownerID, "rl-slotburst", "RL Slot Burst", 0, "reusable", nil)
	r := setupPublicBookingRoutes(t, &countingBookingFlow{})
	path := "/api/book/" + link.Slug + "/slots?date=" + futureBookingStart().Format(time.DateOnly)

	const k = 260
	codes := make([]limitedResponse, k)
	var wg sync.WaitGroup
	for i := 0; i < k; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = doPublic(t, r, http.MethodGet, path, "", peerA, nil)
		}(i)
	}
	wg.Wait()
	passed, limited := 0, 0
	for _, res := range codes {
		if res.code == http.StatusTooManyRequests {
			limited++
			assertRateLimited(t, res, 60)
		} else {
			passed++
		}
	}
	if passed > 120 {
		t.Fatalf("%d slot requests got past the limiter, want at most 120", passed)
	}
	if limited < k-120 {
		t.Fatalf("%d answered 429, want at least %d", limited, k-120)
	}
}

// The link-info read stays unlimited (spec §5, Q3).
func TestGetLinkInfo_NotLimited(t *testing.T) {
	ownerID := createTestUser(t, "rl-info-owner@example.com")
	link := createPublicBookingLink(t, ownerID, "rl-info", "RL Info", 0, "reusable", nil)
	r := setupPublicBookingRoutes(t, &countingBookingFlow{})
	for i := 0; i < 20; i++ {
		doPublic(t, r, http.MethodPost, "/api/book/"+link.Slug, `{}`, peerA, nil)
	}
	for i := 0; i < 130; i++ {
		if got := doPublic(t, r, http.MethodGet, "/api/book/"+link.Slug, "", peerA, nil); got.code != http.StatusOK {
			t.Fatalf("link info %d: status = %d, want 200", i+1, got.code)
		}
	}
}
