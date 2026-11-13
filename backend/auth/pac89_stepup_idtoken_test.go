package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/config"
)

// PAC-89: the Microsoft step-up verifies ID tokens with PAC-71's
// tenant-pinned verifier, at its own per-tenant key address (unit level:
// AC-3, AC-10, AC-12, AC-18).

// pac89StandIn is a Microsoft stand-in key server: every tenant's keys at
// /{tid}/discovery/v2.0/keys, signed with key.
type pac89StandIn struct {
	srv  *httptest.Server
	key  *rsa.PrivateKey
	mu   sync.Mutex
	hits []string // request paths
	gate chan struct{}
}

func newPAC89StandIn(t *testing.T) *pac89StandIn {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	s := &pac89StandIn{key: k}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.Path)
		gate := s.gate
		s.mu.Unlock()
		if gate != nil {
			<-gate
		}
		writeJWKS(w, k, "k1")
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func writeJWKS(w http.ResponseWriter, k *rsa.PrivateKey, kid string) {
	enc := base64.RawURLEncoding
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{
		"kty": "RSA", "kid": kid, "n": enc.EncodeToString(k.N.Bytes()), "e": enc.EncodeToString(big.NewInt(int64(k.E)).Bytes()),
	}}})
}

func (s *pac89StandIn) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

// stepup is a Microsoft step-up provider redirected to this stand-in as a
// test-mode deployment declares it (config.DecideMicrosoftIdP).
func (s *pac89StandIn) stepup(t *testing.T) *StepupProvider {
	t.Helper()
	ep, err := config.DecideMicrosoftIdP(s.srv.URL, "true")
	if err != nil {
		t.Fatal(err)
	}
	return NewMicrosoftStepup("client", "secret", "https://app.test/cb", ep)
}

// token signs a step-up ID token for tid issued by the stand-in, at now.
func (s *pac89StandIn) token(t *testing.T, tid string, now time.Time, edit func(h, c map[string]any)) string {
	t.Helper()
	b := &msVerifierBench{key: s.key, now: now}
	return b.token(t, tid, func(h, c map[string]any) {
		c["iss"] = s.srv.URL + "/" + tid + "/v2.0"
		c["email"], c["preferred_username"] = "admin@example.test", "admin@example.test"
		if edit != nil {
			edit(h, c)
		}
	})
}

func TestPAC89_StepupReturnsTheAdminClaims(t *testing.T) {
	s := newPAC89StandIn(t)
	v := s.stepup(t).MicrosoftIDTokens()
	tid := "7E4A0000-0000-4000-8000-0000000000AB"
	got, err := v.VerifyClaims(context.Background(), s.token(t, tid, time.Now(), func(_, c map[string]any) {
		c["wids"] = []string{"62e90394-69f5-4237-9190-012177145e10"}
		c["oid"] = "0A5E0000-0000-4000-8000-0000000000CD"
	}), "client", "n")
	if err != nil {
		t.Fatal(err)
	}
	if got.TenantID != strings.ToLower(tid) || got.ObjectID != "0a5e0000-0000-4000-8000-0000000000cd" ||
		got.Email != "admin@example.test" || got.PreferredUsername != "admin@example.test" ||
		!got.WIDsPresent || len(got.WIDs) != 1 {
		t.Fatalf("claims %+v", got)
	}
	got, err = v.VerifyClaims(context.Background(), s.token(t, tid, time.Now(), nil), "client", "n")
	if err != nil || got.WIDsPresent || got.WIDs != nil {
		t.Fatalf("no wids: %+v %v", got, err)
	}
	// A claim of the wrong type is refused rather than read as empty.
	if _, err := v.VerifyClaims(context.Background(), s.token(t, tid, time.Now(), func(_, c map[string]any) { c["wids"] = "x" }), "client", "n"); err == nil {
		t.Fatal("wids as a string was accepted")
	}
}

// AC-3 (unit): a tid that is not a canonical GUID is refused before any
// key is fetched.
func TestPAC89_AC3_NonGUIDTenantMakesNoFetch(t *testing.T) {
	s := newPAC89StandIn(t)
	v := s.stepup(t).MicrosoftIDTokens()
	for name, tid := range map[string]any{
		"label": "t1", "common": "common", "organizations": "organizations",
		"slash": "a/b", "dots": "..", "dot-dot GUID": "../00000000-0000-4000-8000-000000000001",
		"empty": "", "absent": nil, "number": 7,
	} {
		tok := s.token(t, "x", time.Now(), func(_, c map[string]any) {
			if tid == nil {
				delete(c, "tid")
			} else {
				c["tid"] = tid
			}
		})
		if _, err := v.VerifyClaims(context.Background(), tok, "client", "n"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if n := len(s.requests()); n != 0 || v.Fetches() != 0 {
		t.Fatalf("%d key requests (%d fetches), want 0", n, v.Fetches())
	}
}

// AC-10: per-tenant keys, cached, single flight, rate-limited refetch.
func TestPAC89_AC10_StepupKeyCache(t *testing.T) {
	ctx := context.Background()
	t.Run("same tenant twice: one fetch", func(t *testing.T) {
		s := newPAC89StandIn(t)
		v := s.stepup(t).MicrosoftIDTokens()
		tid := guid(1)
		for i := 0; i < 2; i++ {
			if _, err := v.VerifyClaims(ctx, s.token(t, tid, time.Now(), nil), "client", "n"); err != nil {
				t.Fatal(err)
			}
		}
		if got := s.requests(); len(got) != 1 {
			t.Fatalf("requests %v, want one", got)
		}
	})
	t.Run("20 concurrent cold verifications: one fetch", func(t *testing.T) {
		s := newPAC89StandIn(t)
		gate := make(chan struct{})
		s.gate = gate
		v := s.stepup(t).MicrosoftIDTokens()
		tok := s.token(t, guid(2), time.Now(), nil)
		var wg sync.WaitGroup
		errs := make(chan error, 20)
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := v.VerifyClaims(ctx, tok, "client", "n")
				errs <- err
			}()
		}
		deadline := time.Now().Add(5 * time.Second)
		for len(s.requests()) == 0 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(100 * time.Millisecond)
		close(gate)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Errorf("a verification failed: %v", err)
			}
		}
		if got := s.requests(); len(got) != 1 {
			t.Fatalf("requests %v, want one", got)
		}
	})
	t.Run("unknown kid: at most one refetch a minute", func(t *testing.T) {
		s := newPAC89StandIn(t)
		v := s.stepup(t).MicrosoftIDTokens()
		now := time.Now()
		v.Now = func() time.Time { return now }
		tid := guid(3)
		if _, err := v.VerifyClaims(ctx, s.token(t, tid, now, nil), "client", "n"); err != nil {
			t.Fatal(err)
		}
		unknown := s.token(t, tid, now, func(h, _ map[string]any) { h["kid"] = "k9" })
		for i := 0; i < 3; i++ {
			if _, err := v.VerifyClaims(ctx, unknown, "client", "n"); err == nil {
				t.Fatal("unknown kid accepted")
			}
		}
		if got := len(s.requests()); got != 2 {
			t.Fatalf("%d requests, want 1 + 1 refetch", got)
		}
		now = now.Add(61 * time.Second)
		_, _ = v.VerifyClaims(ctx, unknown, "client", "n")
		if got := len(s.requests()); got != 3 {
			t.Fatalf("%d requests after a minute, want 3", got)
		}
	})
	t.Run("tenants A and B: one fetch at each tenant's address", func(t *testing.T) {
		s := newPAC89StandIn(t)
		v := s.stepup(t).MicrosoftIDTokens()
		a, b := guid(10), strings.ToUpper("0000000b-0000-4000-8000-00000000000b")
		for _, tid := range []string{a, b, a, b} {
			if _, err := v.VerifyClaims(ctx, s.token(t, tid, time.Now(), nil), "client", "n"); err != nil {
				t.Fatal(err)
			}
		}
		got := s.requests()
		want := []string{"/" + a + "/discovery/v2.0/keys", "/" + strings.ToLower(b) + "/discovery/v2.0/keys"}
		if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("requests %v, want %v", got, want)
		}
	})
}

// AC-12: the step-up's key address and issuer follow the identity-provider
// decision; a stand-in is reached only through the connection guard.
func TestPAC89_AC12_KeyAddressFollowsTheIdPDecision(t *testing.T) {
	tid := guid(12)
	t.Run("stand-in", func(t *testing.T) {
		s := newPAC89StandIn(t)
		p := s.stepup(t)
		v := p.MicrosoftIDTokens()
		if _, err := v.VerifyClaims(context.Background(), s.token(t, tid, time.Now(), nil), "client", "n"); err != nil {
			t.Fatal(err)
		}
		if got := s.requests(); len(got) != 1 || got[0] != "/"+tid+"/discovery/v2.0/keys" {
			t.Fatalf("requests %v", got)
		}
		// The expected issuer is the stand-in's per-tenant issuer: Microsoft's
		// issuer for the same tenant is refused.
		ms := s.token(t, tid, time.Now(), func(_, c map[string]any) { c["iss"] = "https://login.microsoftonline.com/" + tid + "/v2.0" })
		if _, err := v.VerifyClaims(context.Background(), ms, "client", "n"); err == nil {
			t.Fatal("Microsoft's issuer accepted by a stand-in deployment")
		}
		// The step-up's own client: no proxy, no redirect.
		tr, ok := v.HTTPClient.Transport.(*http.Transport)
		if v.HTTPClient != p.client || !ok || tr.Proxy != nil || v.HTTPClient.CheckRedirect == nil {
			t.Fatal("the key fetch does not use the step-up's client")
		}
	})
	t.Run("guard", func(t *testing.T) {
		// A stand-in address outside the permitted ranges is never connected to.
		ep := config.MicrosoftIdPAt("http://192.0.2.1:9")
		ep.Guard = true
		s := newPAC89StandIn(t)
		v := NewMicrosoftStepup("client", "secret", "https://app.test/cb", ep).MicrosoftIDTokens()
		_, err := v.VerifyClaims(context.Background(), s.token(t, tid, time.Now(), func(_, c map[string]any) {
			c["iss"] = "http://192.0.2.1:9/" + tid + "/v2.0"
		}), "client", "n")
		if err == nil || !strings.Contains(err.Error(), "connection guard") {
			t.Fatalf("err = %v, want the connection guard's refusal", err)
		}
	})
	t.Run("production", func(t *testing.T) {
		v := NewMicrosoftStepup("client", "secret", "https://app.test/cb", config.ProductionMicrosoftIdP()).MicrosoftIDTokens()
		if got := v.KeysURL(tid); got != "https://login.microsoftonline.com/"+tid+"/discovery/v2.0/keys" || got != MicrosoftKeysURL(tid) {
			t.Fatalf("production key address %q", got)
		}
		if v.issuer() != microsoftIssuer {
			t.Fatalf("production issuer %q", v.issuer())
		}
	})
}

// AC-18: keys cached from the stand-in's address never satisfy a
// verification that expects sign-in's address, even in one cache instance.
func TestPAC89_AC18_StandInKeysNeverServeSignIn(t *testing.T) {
	s := newPAC89StandIn(t)
	signInKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var signInHits []string
	signIn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		signInHits = append(signInHits, r.URL.Path)
		mu.Unlock()
		writeJWKS(w, signInKey, "k1")
	}))
	t.Cleanup(signIn.Close)
	prev := MicrosoftKeysURL
	MicrosoftKeysURL = func(tid string) string { return signIn.URL + "/keys/" + tid }
	t.Cleanup(func() { MicrosoftKeysURL = prev })

	ctx := context.Background()
	tid := guid(18)
	stepup := s.stepup(t).MicrosoftIDTokens()
	if _, err := stepup.VerifyClaims(ctx, s.token(t, tid, time.Now(), nil), "client", "n"); err != nil {
		t.Fatal(err)
	}
	// A sign-in token for the same tenant, signed by the stand-in's key.
	forged := s.token(t, tid, time.Now(), func(_, c map[string]any) { c["iss"] = "https://login.microsoftonline.com/" + tid + "/v2.0" })

	check := func(t *testing.T, v *MicrosoftIDTokenVerifier) {
		t.Helper()
		mu.Lock()
		signInHits = nil
		mu.Unlock()
		if _, err := v.Verify(ctx, forged, "client", "n"); err == nil {
			t.Fatal("a stand-in key verified a sign-in token")
		}
		mu.Lock()
		defer mu.Unlock()
		if len(signInHits) != 1 || signInHits[0] != "/keys/"+tid {
			t.Fatalf("sign-in key requests %v, want one at MicrosoftKeysURL(tid)", signInHits)
		}
	}
	t.Run("separate sign-in verifier (the wiring)", func(t *testing.T) {
		check(t, NewMicrosoftIDTokenVerifier())
	})
	t.Run("one shared cache", func(t *testing.T) {
		// The step-up's verifier, re-pointed at sign-in's address and issuer,
		// still holds the stand-in's keys for tid: they must not be used.
		stepup.KeysURL, stepup.Issuer = nil, ""
		if stepup.Cached(tid) {
			t.Fatal("an entry fetched from the stand-in is visible at sign-in's address")
		}
		check(t, stepup)
	})
}
