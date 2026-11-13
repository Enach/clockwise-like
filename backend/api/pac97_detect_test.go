package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/oauth2"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/auth"
)

// PAC-97 (docs/specs/PAC-97.md AC-7, AC-14, AC-16, AC-17): sign-in detection
// through the real router, with an injected resolver. No test in this package
// reaches live DNS.

// forbiddenMXResolver is AC-14 for backend/api: installed as the package's
// default detector at init, it stops the run if any test reaches the
// production resolver (the PAC-35 detect sub-tests and the rate-limit tests
// included). A test that needs answers installs its own fake.
type forbiddenMXResolver struct{}

func (forbiddenMXResolver) LookupMX(_ context.Context, name string) ([]*net.MX, error) {
	panic(fmt.Sprintf("PAC-97 AC-14: a backend/api test reached the production MX resolver (query %q): install a fake with installFakeMX", name))
}

func init() {
	auth.SetDefaultDetector(auth.NewDetector(auth.DetectorOptions{Resolver: forbiddenMXResolver{}}))
}

// fakeMX serves fixed MX answers and records every query.
type fakeMX struct {
	mu      sync.Mutex
	answers map[string]func(ctx context.Context) ([]*net.MX, error)
	queries []string
}

func (f *fakeMX) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	f.mu.Lock()
	f.queries = append(f.queries, name)
	a, ok := f.answers[strings.TrimSuffix(name, ".")]
	f.mu.Unlock()
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
	}
	return a(ctx)
}

func (f *fakeMX) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.queries)
}

func records(rs ...*net.MX) func(context.Context) ([]*net.MX, error) {
	return func(context.Context) ([]*net.MX, error) { return rs, nil }
}

// installFakeMX makes the handler's detector answer from a fake for this test.
func installFakeMX(t *testing.T) *fakeMX {
	t.Helper()
	f := &fakeMX{answers: map[string]func(context.Context) ([]*net.MX, error){
		"mimecast.test": records(
			&net.MX{Host: "eu-smtp-inbound-1.mimecast.com.", Pref: 10},
			&net.MX{Host: "eu-smtp-inbound-2.mimecast.com.", Pref: 10}),
		"gorgias.com": records(
			&net.MX{Host: "aspmx.l.google.com.", Pref: 1},
			&net.MX{Host: "alt1.aspmx.l.google.com.", Pref: 5}),
		"contoso.test": records(&net.MX{Host: "contoso-test.mail.protection.outlook.com.", Pref: 0}),
		"servfail.test": func(context.Context) ([]*net.MX, error) {
			return nil, &net.DNSError{Err: "server misbehaving", Name: "servfail.test.", IsTemporary: true}
		},
		"slow.test": func(ctx context.Context) ([]*net.MX, error) {
			<-ctx.Done()
			return nil, &net.DNSError{Err: "i/o timeout", Name: "slow.test.", IsTimeout: true}
		},
	}}
	restore := auth.SetDefaultDetector(auth.NewDetector(auth.DetectorOptions{Resolver: f, Budget: 200 * time.Millisecond}))
	t.Cleanup(restore)
	return f
}

// pac97Router is the production router (RegisterRoutes), as AC-16 asks.
func pac97Router(t *testing.T) *chi.Mux {
	t.Helper()
	r := chi.NewRouter()
	RegisterRoutes(r, openTestDB(t), &oauth2.Config{}, "pac97-jwt-secret", "http://localhost", "http://localhost")
	return r
}

func pac97Detect(r http.Handler, remote, email string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/detect", strings.NewReader(`{"email":"`+email+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remote
	r.ServeHTTP(rec, req)
	return rec
}

// decodeGenerated decodes a detection answer into the generated contract type
// with unknown fields refused.
func decodeGenerated(t *testing.T, rec *httptest.ResponseRecorder) gen.SsoDetectResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body %q", rec.Code, rec.Body.String())
	}
	var res gen.SsoDetectResponse
	dec := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&res); err != nil {
		t.Fatalf("body %q does not decode into gen.SsoDetectResponse: %v", rec.Body.String(), err)
	}
	return res
}

// AC-16 (and API-092 for the new outcome): a gateway domain is a 200 choose
// through the real router; the body is exactly the contract's shape with no
// provider name and no redirect path.
func TestPAC97_AC16_ChooseThroughTheRouter(t *testing.T) {
	resetDetectLimiter(t, nil)
	f := installFakeMX(t)
	rec := pac97Detect(pac97Router(t), "192.0.2.97:1000", "alice@mimecast.test")
	res := decodeGenerated(t, rec)
	if res.Type != gen.SsoDetectResponseTypeChoose {
		t.Errorf("type = %q, want %q", res.Type, gen.SsoDetectResponseTypeChoose)
	}
	if res.ProviderName != nil || res.RedirectUrl != nil {
		t.Errorf("choose carries provider_name %v / redirect_url %v", res.ProviderName, res.RedirectUrl)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != `{"type":"choose"}` {
		t.Errorf("body = %s, want {\"type\":\"choose\"}", body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	if n := f.count(); n != 1 {
		t.Errorf("queries = %d, want 1", n)
	}
}

// The strong matches through the router too: each answer is a generated value.
func TestPAC97_StrongMatchesThroughTheRouter(t *testing.T) {
	resetDetectLimiter(t, nil)
	installFakeMX(t)
	r := pac97Router(t)
	for email, want := range map[string]gen.SsoDetectResponseType{
		"alice@gorgias.com":  gen.SsoDetectResponseTypeGoogle,
		"alice@contoso.test": gen.SsoDetectResponseTypeMicrosoft,
		"carol@gmail.com":    gen.SsoDetectResponseTypeGeneric,
		"user@intranet":      gen.SsoDetectResponseTypeChoose,
	} {
		if got := decodeGenerated(t, pac97Detect(r, "192.0.2.98:1000", email)).Type; got != want {
			t.Errorf("%s = %q, want %q", email, got, want)
		}
	}
}

// AC-7: SERVFAIL and a resolver that never answers are a 200 choose, never a
// 500 and never a provider.
func TestPAC97_AC7_LookupFailuresAre200Choose(t *testing.T) {
	resetDetectLimiter(t, nil)
	installFakeMX(t)
	r := pac97Router(t)
	for _, email := range []string{"alice@servfail.test", "alice@slow.test"} {
		start := time.Now()
		res := decodeGenerated(t, pac97Detect(r, "192.0.2.99:1000", email))
		if res.Type != gen.SsoDetectResponseTypeChoose {
			t.Errorf("%s = %q, want choose", email, res.Type)
		}
		if elapsed := time.Since(start); elapsed > 200*time.Millisecond+500*time.Millisecond {
			t.Errorf("%s took %v", email, elapsed)
		}
	}
}

// AC-17: the 21st detection in a minute from one client IP is still a 429,
// company domains included (the limiter runs before any lookup).
func TestPAC97_AC17_RateLimitUnchanged(t *testing.T) {
	resetDetectLimiter(t, nil)
	f := installFakeMX(t)
	r := pac97Router(t)
	for i := 0; i < detectRateLimit; i++ {
		if rec := pac97Detect(r, "198.51.100.97:1000", "alice@mimecast.test"); rec.Code != http.StatusOK {
			t.Fatalf("request %d: status %d", i+1, rec.Code)
		}
	}
	rec := pac97Detect(r, "198.51.100.97:1001", "alice@mimecast.test")
	assertJSONError(t, rec, http.StatusTooManyRequests, "rate limit exceeded")
	if n := f.count(); n != 1 {
		t.Errorf("queries = %d, want 1 (cached after the first)", n)
	}
	if detectRateLimit != 20 {
		t.Errorf("detectRateLimit = %d, want 20", detectRateLimit)
	}
}

// AC-14: the package default is the fail-on-use resolver.
func TestPAC97_AC14_APITestsCannotReachLiveDNS(t *testing.T) {
	// Detecting a consumer address through the default detector never
	// reaches the resolver; if it did, forbiddenMXResolver would panic.
	// PAC-98: gmail.com is answered google, still without the resolver.
	res, err := auth.DefaultDetector().Detect(context.Background(), openTestDB(t), "carol@gmail.com")
	if err != nil || res.Type != auth.DetectGoogle {
		t.Fatalf("= (%+v, %v), want google", res, err)
	}
}
