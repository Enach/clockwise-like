package auth

import (
	"bytes"
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
)

// PAC-97: sign-in detection follows the domain's MX records
// (docs/specs/PAC-97.md §4, contracts/features/PAC-97.yaml). Every test here
// uses the in-memory fake of pac97_fake_resolver_test.go: no live DNS, no
// live HTTP.

// noSSO is the SSO-row lookup of a deployment with no SSO rows.
func noSSO(string) (*storage.SSOProvider, error) { return nil, nil }

// testBudget keeps the slow fixture short; the production budget is pinned by
// TestPAC97_AC15_ProductionConstruction.
const testBudget = 150 * time.Millisecond

func newTestDetector(f *fakeMX) *Detector {
	return NewDetector(DetectorOptions{Resolver: f, Budget: testBudget})
}

func detectType(t *testing.T, d *Detector, ctx context.Context, email string) string {
	t.Helper()
	res, err := d.detect(ctx, noSSO, email)
	if err != nil {
		t.Fatalf("detect(%q): %v", email, err)
	}
	if res.Type != DetectSSO && (res.ProviderName != "" || res.RedirectURL != "") {
		t.Fatalf("detect(%q) = %+v: only sso carries a provider name or a redirect path", email, res)
	}
	return res.Type
}

// recordingTransport counts every HTTP request made through it.
type recordingTransport struct{ n atomic.Int64 }

func (r *recordingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	r.n.Add(1)
	return nil, fmt.Errorf("PAC-97: detection made an HTTP request")
}

func installRecordingTransport(t *testing.T) *recordingTransport {
	t.Helper()
	rt := &recordingTransport{}
	prev := http.DefaultTransport
	http.DefaultTransport = rt
	t.Cleanup(func() { http.DefaultTransport = prev })
	return rt
}

// AC-1: gorgias.com (Google MX, an Entra tenant that would answer "Federated")
// is google, and nothing asks Microsoft.
func TestPAC97_AC1_GoogleMXWinsOverAMicrosoftTenant(t *testing.T) {
	var msHits atomic.Int64
	ms := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		msHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"issuer":"https://sts.windows.net/2f4a9838-26b7-47ee-be60-ccc1fdec5953/","NameSpaceType":"Federated"}`))
	}))
	defer ms.Close()
	rt := installRecordingTransport(t)

	d := newTestDetector(newFixtureFake())
	if got := detectType(t, d, context.Background(), "alice@gorgias.com"); got != DetectGoogle {
		t.Fatalf("alice@gorgias.com = %q, want google", got)
	}
	if n := msHits.Load(); n != 0 {
		t.Errorf("the Microsoft endpoint received %d requests, want 0", n)
	}
	if n := rt.n.Load(); n != 0 {
		t.Errorf("detection made %d HTTP requests, want 0", n)
	}
}

// AC-1 / AC-12 / D-5, by construction: the detection source imports no HTTP
// client at all, so no code path in it can make a third-party HTTP call.
func TestPAC97_AC1_DetectionSourceHasNoHTTPClient(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "sso_detect.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if p == "net/http" || strings.HasSuffix(p, "/internal/outbound") {
			t.Errorf("sso_detect.go imports %q: detection must make no HTTP call (spec D-5)", p)
		}
	}
	if isMicrosoftMX("login.microsoftonline.com") {
		t.Error("the tenant endpoint's host must not count as a Microsoft MX")
	}
}

// AC-2: every row of the fixture table, one sub-test per row.
func TestPAC97_AC2_FixtureTable(t *testing.T) {
	f := newFixtureFake()
	d := newTestDetector(f)
	for _, tc := range pac97Fixtures {
		t.Run(tc.domain, func(t *testing.T) {
			if got := detectType(t, d, context.Background(), "user@"+tc.domain); got != tc.want {
				t.Errorf("user@%s = %q, want %q", tc.domain, got, tc.want)
			}
		})
	}
	// The single label is never queried; every other row is, exactly once.
	for _, q := range f.recorded() {
		if q.name == "intranet." || q.name == "intranet" {
			t.Errorf("the single label was queried: %q", q.name)
		}
	}
	if got, want := f.count(), len(pac97Fixtures)-1; got != want {
		t.Errorf("queries = %d, want %d (one per queryable fixture)", got, want)
	}
}

// AC-2, the rule on its own: the label-boundary matcher and D-3's record rule.
func TestPAC97_AC2_MatchersAndRecordRule(t *testing.T) {
	for _, tc := range []struct {
		host      string
		google    bool
		microsoft bool
	}{
		{"aspmx.l.google.com.", true, false},
		{"ASPMX.L.GOOGLE.COM.", true, false},
		{"google.com", true, false},
		{"googlemail.com.", true, false},
		{"aspmx2.googlemail.com", true, false},
		{"evilgoogle.com", false, false},
		{"aspmx.l.google.com.evil.example", false, false},
		{"x.mail.protection.outlook.com.", false, true},
		{"x.mail.eo.outlook.com", false, true},
		{"x.o-v1.mx.microsoft.", false, true},
		{"mail.protection.outlook.com", false, false},
		{"x.olc.protection.outlook.com", false, false},
		{"x.mail.protection.office365.us", false, false},
		{"x.mail.protection.partner.outlook.cn", false, false},
		{"x.mail.protection.outlook.de", false, false},
		{"x.evilmx.microsoft", false, false},
	} {
		if g := isGoogleMX(tc.host); g != tc.google {
			t.Errorf("isGoogleMX(%q) = %v, want %v", tc.host, g, tc.google)
		}
		if m := isMicrosoftMX(tc.host); m != tc.microsoft {
			t.Errorf("isMicrosoftMX(%q) = %v, want %v", tc.host, m, tc.microsoft)
		}
	}
	for _, tc := range []struct {
		name     string
		records  []*net.MX
		err      error
		want     string
		definite bool
	}{
		{"duplicates are harmless", mx("5 aspmx.l.google.com.", "5 aspmx.l.google.com."), nil, DetectGoogle, true},
		{"lowest preference is numeric, not first", mx("20 mx.gateway.example.", "10 a.mail.protection.outlook.com."), nil, DetectMicrosoft, true},
		{"equal primaries must all match", mx("10 aspmx.l.google.com.", "10 mx.gateway.example."), nil, DetectChoose, true},
		{"Microsoft backup blocks Google", mx("1 aspmx.l.google.com.", "50 a.mail.protection.outlook.com."), nil, DetectChoose, true},
		{"no records, no error", nil, nil, DetectChoose, true},
		{"timeout", nil, &net.DNSError{Err: "i/o timeout", IsTimeout: true}, DetectChoose, false},
		{"context deadline", nil, context.DeadlineExceeded, DetectChoose, false},
		{"partial answer", mx("10 aspmx.l.google.com."), &net.DNSError{Err: "invalid names"}, DetectChoose, false},
		{"not found", nil, &net.DNSError{Err: "no such host", IsNotFound: true}, DetectChoose, true},
	} {
		got, definite := classifyMX(tc.records, tc.err)
		if got != tc.want || definite != tc.definite {
			t.Errorf("%s: classifyMX = (%q, %v), want (%q, %v)", tc.name, got, definite, tc.want, tc.definite)
		}
	}
}

// AC-3: an SSO row wins with today's name and path, and nothing is queried.
func TestPAC97_AC3_SSORowWinsWithoutAQuery(t *testing.T) {
	db := openTestDB(t)
	if _, err := storage.UpsertSSOProvider(db, &storage.SSOProvider{
		Domain: "contoso.test", ProviderName: "Contoso IdP", ProviderType: "oidc",
		Enabled: true, OIDCIssuer: "https://idp.contoso.test",
	}); err != nil {
		t.Fatalf("UpsertSSOProvider: %v", err)
	}
	f := newFixtureFake()
	res, err := newTestDetector(f).Detect(context.Background(), db, "alice@contoso.test")
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Type != DetectSSO || res.ProviderName != "Contoso IdP" || res.RedirectURL != "/api/auth/sso/contoso.test" {
		t.Errorf("result = %+v, want sso / Contoso IdP / /api/auth/sso/contoso.test", res)
	}
	if n := f.count(); n != 0 {
		t.Errorf("queries = %d, want 0", n)
	}
}

// AC-4: consumer domains and an invalid address are generic, nothing queried,
// and the SSO table is not even read.
func TestPAC97_AC4_ConsumerAndInvalidAreGenericWithoutAQuery(t *testing.T) {
	f := newFixtureFake()
	d := newTestDetector(f)
	for _, email := range []string{"carol@gmail.com", "carol@outlook.com", "not-an-email"} {
		res, err := d.detect(context.Background(), func(string) (*storage.SSOProvider, error) {
			t.Errorf("%s: the SSO row was read", email)
			return nil, nil
		}, email)
		if err != nil || res.Type != DetectGeneric {
			t.Errorf("%s = (%+v, %v), want generic", email, res, err)
		}
	}
	if n := f.count(); n != 0 {
		t.Errorf("queries = %d, want 0", n)
	}
}

// AC-5: a resolver that never answers, a caller without a deadline: choose
// within the budget plus 500 ms, no error.
func TestPAC97_AC5_SlowResolverIsBoundedByTheBudget(t *testing.T) {
	d := newTestDetector(newFixtureFake())
	start := time.Now()
	res, err := d.detect(context.Background(), noSSO, "alice@slow.test")
	elapsed := time.Since(start)
	if err != nil || res.Type != DetectChoose {
		t.Fatalf("= (%+v, %v), want choose and no error", res, err)
	}
	if elapsed > testBudget+500*time.Millisecond {
		t.Errorf("took %v, want at most %v", elapsed, testBudget+500*time.Millisecond)
	}
}

// AC-5 also holds for a resolver that ignores its context altogether.
func TestPAC97_AC5_ResolverIgnoringItsContextIsStillBounded(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	d := NewDetector(DetectorOptions{Resolver: ignoringResolver{release}, Budget: testBudget})
	start := time.Now()
	if got := detectType(t, d, context.Background(), "alice@stuck.test"); got != DetectChoose {
		t.Fatalf("= %q, want choose", got)
	}
	if elapsed := time.Since(start); elapsed > testBudget+500*time.Millisecond {
		t.Errorf("took %v", elapsed)
	}
}

type ignoringResolver struct{ release chan struct{} }

func (r ignoringResolver) LookupMX(context.Context, string) ([]*net.MX, error) {
	<-r.release
	return mx("1 aspmx.l.google.com."), nil
}

// AC-6 (cancelled before): choose at once, no query, nothing cached; the next
// live detection queries and gets the real answer.
func TestPAC97_AC6_CancelledBeforeIsNotCached(t *testing.T) {
	f := newFixtureFake()
	d := newTestDetector(f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if got := detectType(t, d, ctx, "alice@gorgias.com"); got != DetectChoose {
		t.Fatalf("cancelled caller = %q, want choose", got)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("cancelled caller took %v, want within 100ms", elapsed)
	}
	if got := detectType(t, d, context.Background(), "alice@gorgias.com"); got != DetectGoogle {
		t.Fatalf("live caller after a cancellation = %q, want google", got)
	}
	if n := f.count(); n != 1 {
		t.Errorf("queries = %d, want 1 (the live caller queried again)", n)
	}
}

// AC-6 (cancelled during): the cancelled caller is released within 100 ms with
// choose; a second caller waiting on the same shared lookup still gets the
// real answer; the cancellation is not cached; every lookup carries a
// deadline no later than the budget.
func TestPAC97_AC6_CancelledDuringDoesNotCancelOtherWaiters(t *testing.T) {
	f := newFakeMX()
	gate := make(chan struct{})
	f.set("shared.test", fakeAnswer{records: mx("1 aspmx.l.google.com."), gate: gate})
	f.started = make(chan string, 4)
	d := NewDetector(DetectorOptions{Resolver: f}) // production budget: 2 s

	ctxA, cancelA := context.WithCancel(context.Background())
	aDone := make(chan string, 1)
	go func() {
		res, _ := d.detect(ctxA, noSSO, "a@shared.test")
		aDone <- res.Type
	}()
	<-f.started // the shared lookup is in flight

	bDone := make(chan string, 1)
	go func() {
		res, _ := d.detect(context.Background(), noSSO, "b@shared.test")
		bDone <- res.Type
	}()

	cancelAt := time.Now()
	cancelA()
	select {
	case got := <-aDone:
		if got != DetectChoose {
			t.Errorf("cancelled caller = %q, want choose", got)
		}
		if elapsed := time.Since(cancelAt); elapsed > 100*time.Millisecond {
			t.Errorf("cancelled caller released after %v, want within 100ms", elapsed)
		}
	case <-time.After(time.Second):
		t.Fatal("the cancelled caller was not released")
	}

	close(gate)
	if got := <-bDone; got != DetectGoogle {
		t.Errorf("second waiter = %q, want google (not cancelled by the first)", got)
	}
	if got := detectType(t, d, context.Background(), "c@shared.test"); got != DetectGoogle {
		t.Errorf("after the cancellation = %q, want google: a cancellation is never cached", got)
	}
	qs := f.recorded()
	if len(qs) != 1 {
		t.Fatalf("queries = %d, want 1 shared lookup", len(qs))
	}
	for _, q := range qs {
		if !q.hasDeadline || q.deadline.Sub(q.at) > MXLookupBudget {
			t.Errorf("lookup %q deadline %v after start (set %v), want at most %v", q.name, q.deadline.Sub(q.at), q.hasDeadline, MXLookupBudget)
		}
	}
}

// AC-7: SERVFAIL and a slow resolver never surface as an error, nor as a
// provider. (The HTTP half is backend/api/pac97_detect_test.go.)
func TestPAC97_AC7_FailuresAreChooseNeverAnError(t *testing.T) {
	d := newTestDetector(newFixtureFake())
	for _, dom := range []string{"servfail.test", "slow.test"} {
		res, err := d.detect(context.Background(), noSSO, "alice@"+dom)
		if err != nil || res.Type != DetectChoose {
			t.Errorf("%s = (%+v, %v), want choose and no error", dom, res, err)
		}
	}
}

// AC-8: a failure is cached for one minute (no query, choose), then the domain
// is queried again.
func TestPAC97_AC8_FailureCachedOneMinute(t *testing.T) {
	f := newFixtureFake()
	clk := newFakeClock()
	d := NewDetector(DetectorOptions{Resolver: f, Now: clk.now, Budget: testBudget})
	if got := detectType(t, d, context.Background(), "alice@servfail.test"); got != DetectChoose {
		t.Fatalf("first = %q", got)
	}
	f.set("servfail.test", fakeAnswer{records: mx("1 aspmx.l.google.com.")})

	clk.advance(MXFailureTTL - time.Second)
	if got := detectType(t, d, context.Background(), "alice@servfail.test"); got != DetectChoose {
		t.Errorf("before the failure period ends = %q, want choose (cached)", got)
	}
	if n := f.count(); n != 1 {
		t.Errorf("queries before the period ends = %d, want 1", n)
	}

	clk.advance(2 * time.Second)
	if got := detectType(t, d, context.Background(), "alice@servfail.test"); got != DetectGoogle {
		t.Errorf("after the failure period = %q, want google", got)
	}
	if n := f.count(); n != 2 {
		t.Errorf("queries after the period = %d, want 2", n)
	}
	if MXFailureTTL != time.Minute {
		t.Errorf("MXFailureTTL = %v, want 1m (D-8)", MXFailureTTL)
	}
}

// AC-9: a definite answer is cached for one hour.
func TestPAC97_AC9_DefiniteAnswerCachedOneHour(t *testing.T) {
	f := newFixtureFake()
	clk := newFakeClock()
	d := NewDetector(DetectorOptions{Resolver: f, Now: clk.now, Budget: testBudget})
	if got := detectType(t, d, context.Background(), "alice@contoso.test"); got != DetectMicrosoft {
		t.Fatalf("first = %q", got)
	}
	clk.advance(MXDefiniteTTL - time.Second)
	if got := detectType(t, d, context.Background(), "bob@contoso.test"); got != DetectMicrosoft {
		t.Errorf("cached = %q", got)
	}
	if n := f.count(); n != 1 {
		t.Errorf("queries within the hour = %d, want 1", n)
	}
	clk.advance(2 * time.Second)
	if got := detectType(t, d, context.Background(), "alice@contoso.test"); got != DetectMicrosoft {
		t.Errorf("after the hour = %q", got)
	}
	if n := f.count(); n != 2 {
		t.Errorf("queries after the hour = %d, want exactly 2", n)
	}
	if MXDefiniteTTL != time.Hour {
		t.Errorf("MXDefiniteTTL = %v, want 1h (D-8)", MXDefiniteTTL)
	}
}

// AC-10: the cache is bounded; the first domain detected is the first evicted.
func TestPAC97_AC10_CacheIsBoundedOldestFirst(t *testing.T) {
	f := newFakeMX()
	f.fallback = &fakeAnswer{records: mx("1 aspmx.l.google.com.")}
	const n = 3
	d := NewDetector(DetectorOptions{Resolver: f, Budget: testBudget, CacheSize: n})
	for i := 0; i <= n; i++ { // N+1 distinct domains
		detectType(t, d, context.Background(), fmt.Sprintf("u@d%d.test", i))
	}
	if got := d.cached(); got > n {
		t.Errorf("cache holds %d entries, want at most %d", got, n)
	}
	before := f.count()
	detectType(t, d, context.Background(), fmt.Sprintf("u@d%d.test", n)) // newest: still cached
	if f.count() != before {
		t.Errorf("the newest domain was queried again")
	}
	detectType(t, d, context.Background(), "u@d0.test") // first detected: evicted
	if f.count() != before+1 {
		t.Errorf("queries = %d, want %d: the first domain should have been evicted", f.count(), before+1)
	}
}

// AC-11: 50 concurrent detections of one uncached domain share one query and
// one answer. Run under -race.
func TestPAC97_AC11_ConcurrentDetectionsShareOneQuery(t *testing.T) {
	f := newFakeMX()
	gate := make(chan struct{})
	f.set("busy.test", fakeAnswer{records: mx("0 busy-test.mail.protection.outlook.com."), gate: gate})
	d := newTestDetector(f)
	d.budget = 5 * time.Second

	const callers = 50
	var ready, done sync.WaitGroup
	ready.Add(callers)
	done.Add(callers)
	answers := make([]string, callers)
	for i := 0; i < callers; i++ {
		go func(i int) {
			defer done.Done()
			ready.Done()
			res, err := d.detect(context.Background(), noSSO, fmt.Sprintf("u%d@busy.test", i))
			if err == nil {
				answers[i] = res.Type
			}
		}(i)
	}
	ready.Wait()
	close(gate)
	done.Wait()
	if n := f.count(); n != 1 {
		t.Errorf("queries = %d, want exactly 1", n)
	}
	for i, a := range answers {
		if a != DetectMicrosoft {
			t.Errorf("caller %d = %q, want microsoft", i, a)
		}
	}
}

// AC-12: only the domain is queried, no HTTP request is made, and the
// detection path's log output never contains the local part.
func TestPAC97_AC12_PrivacyOnlyTheDomainLeavesTheServer(t *testing.T) {
	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prevOut); log.SetFlags(prevFlags) })
	rt := installRecordingTransport(t)

	f := newFixtureFake()
	d := newTestDetector(f)
	if got := detectType(t, d, context.Background(), "secret.local.part@contoso.test"); got != DetectMicrosoft {
		t.Fatalf("= %q", got)
	}
	// The failure paths log; they must log the domain only.
	detectType(t, d, context.Background(), "secret.local.part@servfail.test")
	detectType(t, d, context.Background(), "secret.local.part@slow.test")

	qs := f.recorded()
	if len(qs) == 0 || qs[0].name != "contoso.test." {
		t.Errorf("first query = %+v, want contoso.test.", qs)
	}
	for _, q := range qs {
		if strings.Contains(q.name, "secret") || q.name != strings.ToLower(q.name) {
			t.Errorf("query name %q carries the local part or is not lower case", q.name)
		}
	}
	if n := rt.n.Load(); n != 0 {
		t.Errorf("HTTP requests = %d, want 0", n)
	}
	if !strings.Contains(buf.String(), "servfail.test") {
		t.Errorf("the failure was not logged (log: %q): the assertion below would prove nothing", buf.String())
	}
	if strings.Contains(buf.String(), "secret.local.part") || strings.Contains(buf.String(), "secret") {
		t.Errorf("log output contains the local part: %q", buf.String())
	}
}

// AC-13: names that are not valid multi-label ASCII host names are choose and
// never queried; a valid name is queried fully qualified.
func TestPAC97_AC13_InvalidNamesAreNeverQueried(t *testing.T) {
	f := newFixtureFake()
	d := newTestDetector(f)
	for _, email := range []string{
		"user@exa mple.com",
		"user@-bad-.test",
		"user@" + strings.Repeat("a", 64) + ".test",
		"user@" + strings.Repeat(strings.Repeat("a", 63)+".", 4) + "test",
		"user@intranet",
		"user@exämple.com",
	} {
		if got := detectType(t, d, context.Background(), email); got != DetectChoose {
			t.Errorf("%q = %q, want choose", email, got)
		}
	}
	if n := f.count(); n != 0 {
		t.Fatalf("queries = %d, want 0", n)
	}
	detectType(t, d, context.Background(), "user@gorgias.com")
	if qs := f.recorded(); len(qs) != 1 || qs[0].name != "gorgias.com." {
		t.Errorf("queries = %+v, want exactly gorgias.com. (fully qualified)", qs)
	}
}

// AC-14: the package's default detector fails the run on any resolver use
// (pac97_fake_resolver_test.go init), so every other test in this package,
// the pre-PAC-97 detection tests included, passed without live DNS.
func TestPAC97_AC14_DefaultDetectorIsForbiddenInTests(t *testing.T) {
	if _, ok := DefaultDetector().resolver.(forbiddenResolver); !ok {
		t.Fatalf("the default detector's resolver is %T, want the fail-on-use resolver", DefaultDetector().resolver)
	}
	// The pre-existing detection paths through the default detector do not
	// reach it: generic and invalid return before any lookup.
	for _, email := range []string{"user@gmail.com", "not-an-email"} {
		res, err := DetectAuthProvider(context.Background(), nil, email)
		if err != nil || res.Type != DetectGeneric {
			t.Errorf("%s = (%+v, %v)", email, res, err)
		}
	}
}

// AC-15 (and AC-10's production N): built without injection, detection uses
// the system resolver, the 2 s budget and a 10 000-entry cache.
func TestPAC97_AC15_ProductionConstruction(t *testing.T) {
	d := NewDetector(DetectorOptions{})
	if d.resolver != net.DefaultResolver {
		t.Errorf("resolver = %T %p, want net.DefaultResolver", d.resolver, d.resolver)
	}
	if d.budget != 2*time.Second || MXLookupBudget != 2*time.Second {
		t.Errorf("budget = %v, want 2s (D-7)", d.budget)
	}
	if d.size != 10000 || MXCacheSize != 10000 {
		t.Errorf("cache size = %d, want 10000 (D-8)", d.size)
	}
	if d.now == nil {
		t.Error("no clock")
	}
	restore := SetDefaultDetector(d)
	if DefaultDetector() != d {
		t.Error("SetDefaultDetector did not install the detector")
	}
	restore()
	if DefaultDetector() == d {
		t.Error("restore did not put the previous detector back")
	}
}
