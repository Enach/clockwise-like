package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// PAC-71 Microsoft sign-in ID-token verifier (contract revision 3.1): unit
// tests of the checks and of the per-tenant key cache.

type msVerifierBench struct {
	key   *rsa.PrivateKey
	srv   *httptest.Server
	hits  sync.Map // tid -> *atomic.Int64
	paths atomic.Int64
	v     *MicrosoftIDTokenVerifier
	now   time.Time
}

func newMSVerifierBench(t *testing.T) *msVerifierBench {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	b := &msVerifierBench{key: k, now: time.Now()}
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.paths.Add(1)
		tid := strings.TrimPrefix(r.URL.Path, "/keys/")
		c, _ := b.hits.LoadOrStore(tid, &atomic.Int64{})
		c.(*atomic.Int64).Add(1)
		enc := base64.RawURLEncoding
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{
			map[string]any{"kty": "RSA", "kid": "k1", "n": enc.EncodeToString(k.N.Bytes()), "e": enc.EncodeToString(big.NewInt(int64(k.E)).Bytes())},
			map[string]any{"kty": "EC", "kid": "ec"},
		}})
	}))
	t.Cleanup(b.srv.Close)
	prev := MicrosoftKeysURL
	MicrosoftKeysURL = func(tid string) string { return b.srv.URL + "/keys/" + tid }
	t.Cleanup(func() { MicrosoftKeysURL = prev })
	b.v = NewMicrosoftIDTokenVerifier()
	b.v.Now = func() time.Time { return b.now }
	return b
}

func (b *msVerifierBench) fetchesFor(tid string) int64 {
	c, ok := b.hits.Load(tid)
	if !ok {
		return 0
	}
	return c.(*atomic.Int64).Load()
}

func (b *msVerifierBench) token(t testing.TB, tid string, edit func(h, c map[string]any)) string {
	t.Helper()
	h := map[string]any{"alg": "RS256", "kid": "k1"}
	c := map[string]any{
		"iss": "https://login.microsoftonline.com/" + tid + "/v2.0", "ver": "2.0", "aud": "client",
		"tid": tid, "oid": "0a5e0000-0000-4000-8000-000000000001", "nonce": "n",
		"exp": b.now.Add(time.Hour).Unix(), "nbf": b.now.Add(-time.Minute).Unix(),
	}
	if edit != nil {
		edit(h, c)
	}
	hb, _ := json.Marshal(h)
	cb, _ := json.Marshal(c)
	in := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	sum := sha256.Sum256([]byte(in))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, b.key, crypto.SHA256, sum[:])
	return in + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func guid(i int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", i) }

func TestPAC71_MicrosoftIDToken_AcceptsAValidTokenAndPinsTheTenant(t *testing.T) {
	b := newMSVerifierBench(t)
	tid := "7E4A0000-0000-4000-8000-000000000035"
	id, err := b.v.Verify(context.Background(), b.token(t, tid, nil), "client", "n")
	if err != nil {
		t.Fatal(err)
	}
	if id.TenantID != strings.ToLower(tid) || id.ObjectID != "0a5e0000-0000-4000-8000-000000000001" {
		t.Fatalf("identity %+v", id)
	}
	if b.fetchesFor(strings.ToLower(tid)) != 1 {
		t.Fatal("keys not fetched from the tenant's own document")
	}
	// A one-element audience array is accepted; 59 s past exp still is (skew).
	tok := b.token(t, tid, func(_, c map[string]any) {
		c["aud"] = []string{"client"}
		c["exp"] = b.now.Add(-59 * time.Second).Unix()
	})
	if _, err := b.v.Verify(context.Background(), tok, "client", "n"); err != nil {
		t.Fatalf("one-element aud within skew: %v", err)
	}
}

func TestPAC71_MicrosoftIDToken_RejectsEveryFailure(t *testing.T) {
	b := newMSVerifierBench(t)
	tid := guid(1)
	cases := map[string]string{
		"not a JWS":       "a.b",
		"bad header":      "!!!.e30.sig",
		"bad payload":     b.token(t, tid, nil)[:5] + ".!!!." + "sig",
		"bad signature":   b.token(t, tid, nil)[:len(b.token(t, tid, nil))-4] + "AAAA",
		"tid with a path": b.token(t, "11111111-1111-1111-1111-111111111111/../x", nil),
		"no exp":          b.token(t, tid, func(_, c map[string]any) { delete(c, "exp") }),
		"exp not a number": b.token(t, tid, func(_, c map[string]any) {
			c["exp"] = "soon"
		}),
		"nbf too far ahead": b.token(t, tid, func(_, c map[string]any) { c["nbf"] = b.now.Add(2 * time.Minute).Unix() }),
		"aud object":        b.token(t, tid, func(_, c map[string]any) { c["aud"] = map[string]any{"x": 1} }),
		"no aud":            b.token(t, tid, func(_, c map[string]any) { delete(c, "aud") }),
		"wrong nonce":       b.token(t, tid, func(_, c map[string]any) { c["nonce"] = "m" }),
		"EC kid":            b.token(t, tid, func(h, _ map[string]any) { h["kid"] = "ec" }),
	}
	before := b.paths.Load()
	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := b.v.Verify(context.Background(), tok, "client", "n"); !errors.Is(err, ErrMicrosoftIDToken) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	if _, err := b.v.Verify(context.Background(), b.token(t, tid, nil), "client", ""); err == nil {
		t.Fatal("an empty expected nonce was accepted")
	}
	if _, err := b.v.Verify(context.Background(), b.token(t, tid, nil), "", "n"); err == nil {
		t.Fatal("an empty client id was accepted")
	}
	if got := b.fetchesFor("11111111-1111-1111-1111-111111111111/../x"); got != 0 {
		t.Fatal("a non-GUID tid reached a URL")
	}
	_ = before
}

// The first fetch for a tenant that is not cached (new, evicted or expired)
// is not rate-limited; at most 1000 tenants are kept, least recently used
// evicted first.
func TestPAC71_MicrosoftIDToken_CacheMissIsNotRateLimitedAndLRUEvicts(t *testing.T) {
	b := newMSVerifierBench(t)
	ctx := context.Background()
	first := guid(0)
	if _, err := b.v.Verify(ctx, b.token(t, first, nil), "client", "n"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= msKeysMaxTenants; i++ {
		if _, err := b.v.Verify(ctx, b.token(t, guid(i), nil), "client", "n"); err != nil {
			t.Fatal(i, err)
		}
	}
	if b.v.Cached(first) {
		t.Fatal("the least recently used tenant was not evicted at 1001 tenants")
	}
	// The evicted tenant signs in again within the minute: a first fetch,
	// not a rate-limited refetch, and it succeeds.
	if _, err := b.v.Verify(ctx, b.token(t, first, nil), "client", "n"); err != nil {
		t.Fatalf("evicted tenant: %v", err)
	}
	if b.fetchesFor(first) != 2 {
		t.Fatalf("fetches = %d, want 2", b.fetchesFor(first))
	}
	// Expiry after 24 h: the next use fetches again.
	b.now = b.now.Add(25 * time.Hour)
	if _, err := b.v.Verify(ctx, b.token(t, first, nil), "client", "n"); err != nil {
		t.Fatal(err)
	}
	if b.fetchesFor(first) != 3 || b.v.Fetches() < 3 {
		t.Fatalf("fetches = %d after 24 h", b.fetchesFor(first))
	}
	// An unknown kid: one refetch, then rate-limited for a minute, then again.
	unknown := b.token(t, first, func(h, _ map[string]any) { h["kid"] = "k2" })
	for i := 0; i < 3; i++ {
		if _, err := b.v.Verify(ctx, unknown, "client", "n"); err == nil {
			t.Fatal("unknown kid accepted")
		}
	}
	if b.fetchesFor(first) != 4 {
		t.Fatalf("fetches = %d, want one refetch", b.fetchesFor(first))
	}
	b.now = b.now.Add(61 * time.Second)
	_, _ = b.v.Verify(ctx, b.token(t, first, func(h, c map[string]any) { h["kid"] = "k2"; c["exp"] = b.now.Add(time.Hour).Unix() }), "client", "n")
	if b.fetchesFor(first) != 5 {
		t.Fatalf("fetches = %d, want a refetch after the minute", b.fetchesFor(first))
	}
}

// Concurrent sign-ins from many tenants (run with -race).
func TestPAC71_MicrosoftIDToken_ConcurrentUse(t *testing.T) {
	b := newMSVerifierBench(t)
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tok := b.token(t, guid(i%8), nil)
			if _, err := b.v.Verify(context.Background(), tok, "client", "n"); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestPAC71_MicrosoftIDToken_KeysEndpointFailures(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"status 503": func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "x", http.StatusServiceUnavailable) },
		"not JSON":   func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) },
		"slow": func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(300 * time.Millisecond)
		},
	} {
		t.Run(name, func(t *testing.T) {
			b := newMSVerifierBench(t)
			srv := httptest.NewServer(h)
			t.Cleanup(srv.Close)
			MicrosoftKeysURL = func(string) string { return srv.URL }
			prev := MicrosoftKeysFetchTimeout
			MicrosoftKeysFetchTimeout = 50 * time.Millisecond
			t.Cleanup(func() { MicrosoftKeysFetchTimeout = prev })
			if _, err := b.v.Verify(context.Background(), b.token(t, guid(9), nil), "client", "n"); !errors.Is(err, ErrMicrosoftIDToken) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	if !IsGUID(MicrosoftConsumerTenant) || IsGUID("common") {
		t.Fatal("IsGUID")
	}
}
