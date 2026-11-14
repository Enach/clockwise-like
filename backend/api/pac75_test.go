package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/internal/budget"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// PAC-75 (spec docs/specs/PAC-75.md, contract contracts/features/PAC-75.md):
// item 1, the ASCII-only domain match on SSO writes and deletes; item 2, the
// bound on individually recorded denied step-up outcomes; item 6 (PAC-95),
// the budget's answer once the administrator check passed or the outcome is
// decided.

// --- Item 1: domain match ------------------------------------------------------

// pac75Org makes a member (with a break-glass-free env) whose org domain is
// prefix + a random label + ".test", and a grant for them.
func pac75Org(t *testing.T, e *ssoEnv, prefix string) (*storage.User, *storage.Org, string) {
	t.Helper()
	label := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	u, org := e.memberOf(t, "admin@"+prefix+label+".test")
	if org == nil || org.Domain != prefix+label+".test" {
		t.Fatalf("org = %+v, want %s%s.test", org, prefix, label)
	}
	return u, org, label
}

func pac75ProviderRows(t *testing.T, e *ssoEnv, domains ...string) int {
	t.Helper()
	n := 0
	for _, d := range domains {
		var c int
		if err := e.db.QueryRow(`SELECT count(*) FROM sso_providers WHERE domain = $1 OR lower(domain) = lower($1)`, d).Scan(&c); err != nil {
			t.Fatal(err)
		}
		n += c
	}
	return n
}

// AC-1, AC-2: a create for a domain that matches the org's only through
// non-ASCII case folding is the foreign-domain 403; the grant stays live; no
// row for any domain; nothing recorded.
func TestPAC75_CreateRefusesNonASCIICaseFold(t *testing.T) {
	e := newSSOEnv(t)
	for _, c := range []struct{ name, prefix, sent string }{
		{"long s U+017F", "s", "ſ"},
		{"Kelvin U+212A", "k", "K"},
		{"fullwidth s", "s", "ｓ"},
		{"dotted I U+0130", "i", "İ"},
		{"dotless i U+0131", "i", "ı"},
	} {
		t.Run(c.name, func(t *testing.T) {
			u, org, label := pac75Org(t, e, c.prefix)
			g := issueGrant(t, e, u.ID, org.ID, e.clock.Now())
			sent := c.sent + label + ".test"
			rec := e.do(t, http.MethodPost, "/api/admin/sso", createBody(sent), session(t, u, time.Now()))
			wantDomainMismatch(t, "create "+sent, rec)
			if grantConsumed(t, e, g.ID) {
				t.Fatal("the refusal consumed the grant")
			}
			if n := pac75ProviderRows(t, e, sent, org.Domain); n != 0 {
				t.Fatalf("%d sso_providers rows after the refusal", n)
			}
			if ev := e.events(t, u.ID); len(ev) != 0 {
				t.Fatalf("events = %v, want none", ev)
			}
		})
	}
}

// AC-3, AC-4: every accepted create (ASCII case variants) stores exactly the
// org domain's bytes, answers it, and the org's list shows it.
func TestPAC75_AcceptedCreateStoresTheOrgDomain(t *testing.T) {
	e := newSSOEnv(t)
	u, org, _ := pac75Org(t, e, "acme")
	variants := []string{strings.ToUpper(org.Domain), org.Domain, "AcMe" + org.Domain[4:], strings.ToUpper(org.Domain[:1]) + org.Domain[1:]}
	for _, sent := range variants {
		issueGrant(t, e, u.ID, org.ID, e.clock.Now())
		rec := e.do(t, http.MethodPost, "/api/admin/sso", createBody(sent), session(t, u, time.Now()))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %q = %d %s", sent, rec.Code, rec.Body.String())
		}
		if body := errorBody(t, rec); body["domain"] != org.Domain {
			t.Fatalf("create %q answered domain %v, want %q", sent, body["domain"], org.Domain)
		}
		var stored string
		if err := e.db.QueryRow(`SELECT domain FROM sso_providers WHERE lower(domain) = lower($1)`, org.Domain).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if stored != org.Domain {
			t.Fatalf("create %q stored %q, want the org domain %q byte for byte", sent, stored, org.Domain)
		}
		list, err := storage.ListSSOProvidersByOrg(e.db, org.ID)
		if err != nil || len(list) != 1 || list[0].Domain != org.Domain {
			t.Fatalf("org list after %q = %v (%v), want the one row", sent, list, err)
		}
	}
}

// AC-5: a delete naming the org domain only through non-ASCII folding, or
// through a non-canonical escape the router leaves literal (contract C-2), is
// the 403; no grant consumed; no row removed. An ASCII case variant deletes
// the org's own row.
func TestPAC75_DeleteRefusesNonASCIICaseFold(t *testing.T) {
	e := newSSOEnv(t)
	for _, c := range []struct{ name, prefix, path string }{
		{"long s, canonical escape", "s", "%C5%BF"},
		{"Kelvin, canonical escape", "k", "%E2%84%AA"},
		{"Kelvin, lower-case escape", "k", "%e2%84%aa"},
		{"ASCII A as an escape", "a", "%41"},
	} {
		t.Run(c.name, func(t *testing.T) {
			u, org, label := pac75Org(t, e, c.prefix)
			if _, err := storage.UpsertSSOProvider(e.db, &storage.SSOProvider{Domain: org.Domain, ProviderName: "Seed", ProviderType: "oidc", Enabled: true}); err != nil {
				t.Fatal(err)
			}
			g := issueGrant(t, e, u.ID, org.ID, e.clock.Now())
			rec := e.do(t, http.MethodDelete, "/api/admin/sso/"+c.path+label+".test", "", session(t, u, time.Now()))
			wantDomainMismatch(t, "delete "+c.path+label, rec)
			if grantConsumed(t, e, g.ID) {
				t.Fatal("the refusal consumed the grant")
			}
			if n, _ := e.providerRow(t, org.Domain); n != 1 {
				t.Fatalf("%d rows, want the org's row kept", n)
			}
		})
	}
	t.Run("ASCII upper case targets the org row", func(t *testing.T) {
		u, org, _ := pac75Org(t, e, "z")
		if _, err := storage.UpsertSSOProvider(e.db, &storage.SSOProvider{Domain: org.Domain, ProviderName: "Seed", ProviderType: "oidc", Enabled: true}); err != nil {
			t.Fatal(err)
		}
		issueGrant(t, e, u.ID, org.ID, e.clock.Now())
		rec := e.do(t, http.MethodDelete, "/api/admin/sso/"+strings.ToUpper(org.Domain), "", session(t, u, time.Now()))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("= %d %s", rec.Code, rec.Body.String())
		}
		if n, _ := e.providerRow(t, org.Domain); n != 0 {
			t.Fatal("row still present")
		}
	})
	t.Run("a stray lookalike row is no longer deletable", func(t *testing.T) {
		u, org, label := pac75Org(t, e, "s")
		stray := "ſ" + label + ".test"
		if _, err := storage.UpsertSSOProvider(e.db, &storage.SSOProvider{Domain: stray, ProviderName: "Stray", ProviderType: "oidc", Enabled: true}); err != nil {
			t.Fatal(err)
		}
		issueGrant(t, e, u.ID, org.ID, e.clock.Now())
		rec := e.do(t, http.MethodDelete, "/api/admin/sso/"+url.PathEscape(stray), "", session(t, u, time.Now()))
		wantDomainMismatch(t, "delete stray", rec)
		var n int
		if err := e.db.QueryRow(`SELECT count(*) FROM sso_providers WHERE domain = $1`, stray).Scan(&n); err != nil || n != 1 {
			t.Fatalf("stray rows = %d (%v), want 1", n, err)
		}
	})
}

// --- Item 2: denial bound ------------------------------------------------------

// pac75Log captures the standard logger for one test.
type pac75Log struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *pac75Log) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *pac75Log) count(sub string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Count(l.buf.String(), sub)
}

func capturePAC75Log(t *testing.T) *pac75Log {
	t.Helper()
	l := &pac75Log{}
	prev := log.Writer()
	log.SetOutput(l)
	t.Cleanup(func() { log.SetOutput(prev) })
	return l
}

func pac75StepupEvents(t *testing.T, e *ssoEnv, u uuid.UUID) (granted, denied int) {
	t.Helper()
	for _, ev := range e.events(t, u) {
		if ev.Kind != "sso_stepup" {
			continue
		}
		if ev.Details["outcome"] == "granted" {
			granted++
		} else {
			denied++
		}
	}
	return granted, denied
}

func pac75Window(t *testing.T, e *ssoEnv, u uuid.UUID) []storage.SSOStepupDenialWindow {
	t.Helper()
	ws, err := storage.ListSSOStepupDenialWindows(context.Background(), e.db, u)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

// pac75Denial makes one denied callback: even i without any start (bare bad
// state: state_mismatch), odd i after a real start with the provider's
// error= (token_invalid). It returns the callback's answer.
func pac75Denial(t *testing.T, e *ssoEnv, sess string, i int) (*httptest.ResponseRecorder, string) {
	t.Helper()
	if i%2 == 0 {
		return e.do(t, http.MethodGet, stepupCallbackPath+"?code=c1&state=bogus", "", sess), "stepup=denied&reason=state_mismatch"
	}
	start := e.do(t, http.MethodGet, "/api/admin/sso/stepup?provider=google", "", sess)
	loc, err := url.Parse(start.Header().Get("Location"))
	if err != nil || start.Code != http.StatusFound {
		t.Fatalf("start = %d %v", start.Code, err)
	}
	return e.do(t, http.MethodGet, stepupCallbackPath+"?error=access_denied&state="+url.QueryEscape(loc.Query().Get("state")), "", sess, stateCookie(start)),
		"stepup=denied&reason=token_invalid"
}

// AC-8, AC-9, AC-10, AC-11: 50 denied callbacks (mixed reasons, with and
// without a start): the answers are today's; 5 recorded, 45 counted; one log
// line; a later grant is recorded; another user is recorded; the next window
// records again.
func TestPAC75_DeniedCallbacksAreBounded(t *testing.T) {
	logs := capturePAC75Log(t)
	e := newSSOEnv(t)
	e.clock.Set(time.Date(2026, 11, 14, 10, 1, 0, 0, time.UTC))
	u, org := e.member(t, "pac75-bound")
	sess := session(t, u, time.Now())
	for i := 0; i < 50; i++ {
		rec, want := pac75Denial(t, e, sess, i)
		wantOutcome(t, rec, rec.Code, want)
	}
	if g, d := pac75StepupEvents(t, e, u.ID); g != 0 || d != 5 {
		t.Fatalf("events: %d granted, %d denied; want 0 and 5", g, d)
	}
	ws := pac75Window(t, e, u.ID)
	if len(ws) != 1 || ws[0].Recorded != 5 || ws[0].LeftOut != 45 || !ws[0].WindowStart.Equal(time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("windows = %+v, want 10:00Z recorded 5 left_out 45", ws)
	}
	line := "sso step-up: denied outcomes of user " + u.ID.String() + " in window 2026-11-14T10:00:00Z beyond 5 are counted, not recorded (sso_stepup_denial_windows.left_out)"
	if n := logs.count(line); n != 1 {
		t.Fatalf("%d left-out log lines, want 1:\n%s", n, logs.buf.String())
	}

	// AC-9: at the bound, a granted step-up is issued and recorded as today.
	e.google.set(googleAdmin(u.Email, org.Domain))
	rec := e.stepup(t, "google", sess)
	wantOutcome(t, rec, rec.Code, "stepup=granted")
	if g, d := pac75StepupEvents(t, e, u.ID); g != 1 || d != 5 {
		t.Fatalf("after the grant: %d granted, %d denied; want 1 and 5", g, d)
	}
	if e.liveGrants(t, u.ID) != 1 {
		t.Fatal("no live grant")
	}
	if ws := pac75Window(t, e, u.ID); len(ws) != 1 || ws[0].Recorded != 5 || ws[0].LeftOut != 45 {
		t.Fatalf("the grant changed the window: %+v", ws)
	}

	// AC-10: user B of the same org is recorded.
	b, borg := e.memberOf(t, "b-"+uuid.NewString()[:6]+"@"+org.Domain)
	if borg == nil || borg.ID != org.ID {
		t.Fatalf("B's org = %+v, want %s", borg, org.ID)
	}
	rec, want := pac75Denial(t, e, session(t, b, time.Now()), 0)
	wantOutcome(t, rec, rec.Code, want)
	if _, d := pac75StepupEvents(t, e, b.ID); d != 1 {
		t.Fatalf("B: %d denied events, want 1", d)
	}

	// AC-11: once the window has passed, the next denial is recorded.
	e.clock.Set(time.Date(2026, 11, 14, 10, 10, 0, 0, time.UTC))
	rec, want = pac75Denial(t, e, sess, 0)
	wantOutcome(t, rec, rec.Code, want)
	if _, d := pac75StepupEvents(t, e, u.ID); d != 6 {
		t.Fatalf("%d denied events after the window passed, want 6", d)
	}
	if ws := pac75Window(t, e, u.ID); len(ws) != 2 || ws[1].Recorded != 1 || ws[1].LeftOut != 0 {
		t.Fatalf("windows = %+v", ws)
	}
}

// AC-12 (through the handler): 50 concurrent denied callbacks for one user:
// every answer is today's, exactly 5 recorded and 45 counted.
func TestPAC75_ConcurrentDeniedCallbacksAreBounded(t *testing.T) {
	e := newSSOEnv(t)
	u, _ := e.member(t, "pac75-conc")
	sess := session(t, u, time.Now())
	var wg sync.WaitGroup
	bad := make(chan string, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, stepupCallbackPath+"?code=c1&state=bogus", nil)
			req.AddCookie(&http.Cookie{Name: "auth_token", Value: sess})
			rec := httptest.NewRecorder()
			e.router.ServeHTTP(rec, req)
			if rec.Code != http.StatusFound || rec.Header().Get("Location") != pac50Frontend+"/settings?stepup=denied&reason=state_mismatch" {
				bad <- rec.Header().Get("Location") + " " + rec.Body.String()
			}
		}()
	}
	wg.Wait()
	close(bad)
	for b := range bad {
		t.Errorf("answer: %s", b)
	}
	if _, d := pac75StepupEvents(t, e, u.ID); d != 5 {
		t.Fatalf("%d denied events, want 5", d)
	}
	if ws := pac75Window(t, e, u.ID); len(ws) != 1 || ws[0].Recorded != 5 || ws[0].LeftOut != 45 {
		t.Fatalf("windows = %+v, want recorded 5 left_out 45", ws)
	}
}

// AC-13: an orgless user's state_mismatch callbacks are bounded the same way
// (org_id NULL rows).
func TestPAC75_OrglessCallbacksAreBounded(t *testing.T) {
	e := newSSOEnv(t)
	u, org := e.memberOf(t, "pac75-"+uuid.NewString()[:8]+"@gmail.com")
	if org != nil {
		t.Fatalf("gmail.com user has org %v", org.ID)
	}
	sess := session(t, u, time.Now())
	for i := 0; i < 8; i++ {
		rec := e.do(t, http.MethodGet, stepupCallbackPath+"?code=c1&state=bogus", "", sess)
		wantOutcome(t, rec, rec.Code, "stepup=denied&reason=state_mismatch")
	}
	evs := e.events(t, u.ID)
	if len(evs) != 5 {
		t.Fatalf("%d events, want 5", len(evs))
	}
	for _, ev := range evs {
		if ev.Details["org_id"] != nil {
			t.Fatalf("event with org: %v", ev.Details)
		}
	}
	if ws := pac75Window(t, e, u.ID); len(ws) != 1 || ws[0].Recorded != 5 || ws[0].LeftOut != 3 {
		t.Fatalf("windows = %+v, want recorded 5 left_out 3", ws)
	}
}

// AC-33: a denied record waiting on the window row past the (shortened) lock
// timeout: today's 500 with the generic body; nothing recorded or counted.
func TestPAC75_WindowLockTimeoutIs500(t *testing.T) {
	e := newSSOEnv(t)
	e.clock.Set(time.Date(2026, 11, 14, 10, 1, 0, 0, time.UTC))
	u, _ := e.member(t, "pac75-wait")
	prev := storage.LinkLockTimeout
	storage.LinkLockTimeout = 300 * time.Millisecond
	t.Cleanup(func() { storage.LinkLockTimeout = prev })
	if _, err := e.db.Exec(`INSERT INTO sso_stepup_denial_windows (user_id, window_start, recorded) VALUES ($1, '2026-11-14T10:00:00Z', 1)`, u.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := e.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`SELECT 1 FROM sso_stepup_denial_windows WHERE user_id = $1 FOR UPDATE`, u.ID); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, http.MethodGet, stepupCallbackPath+"?code=c1&state=bogus", "", session(t, u, time.Now()))
	_ = tx.Rollback()
	wantInternalError(t, "window wait", rec, rec.Code, rec.Body.String())
	if ev := e.events(t, u.ID); len(ev) != 0 {
		t.Fatalf("events = %v, want none", ev)
	}
	if ws := pac75Window(t, e, u.ID); len(ws) != 1 || ws[0].Recorded != 1 || ws[0].LeftOut != 0 {
		t.Fatalf("window = %+v, want unchanged", ws)
	}
}

// --- Item 6 (PAC-95): the budget's answer --------------------------------------

const pac75Budget = 400 * time.Millisecond

// pac75BudgetEnv is the PAC-50 fixture behind a short request budget, on db,
// with a Google Directory stand-in that runs dirHook (when set) before
// answering "admin". done receives once per callback when its handler
// goroutine has returned.
type pac75BudgetEnv struct {
	*ssoEnv
	done chan struct{}
}

func newPAC75BudgetEnv(t *testing.T, db *sql.DB, dirHook func()) *pac75BudgetEnv {
	t.Helper()
	e := newSSOEnv(t)
	s := &pac75BudgetEnv{ssoEnv: e, done: make(chan struct{}, 16)}
	dir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if dirHook != nil {
			dirHook()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"isAdmin": true, "isDelegatedAdmin": false})
	}))
	t.Cleanup(dir.Close)
	ep := e.google.endpoints()
	ep.APIBase = dir.URL
	ah := &authHandlers{db: db, jwtSecret: pac50Secret, frontendURL: pac50Frontend}
	e.h = newSSOHandlers(ah, ssoOptions{
		Clock:     e.clock,
		Google:    auth.NewGoogleStepup(e.google.clientID, "google-secret", pac50AppURL+stepupCallbackPath, ep),
		Microsoft: auth.NewMicrosoftStepup(e.ms.clientID, "ms-secret", pac50AppURL+stepupCallbackPath, e.ms.endpoints()),
	})
	r := chi.NewRouter()
	r.Use(budget.Middleware(pac75Budget, nil))
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Path == stepupCallbackPath {
				defer func() { s.done <- struct{}{} }()
			}
			next.ServeHTTP(w, req)
		})
	})
	r.Group(func(r chi.Router) {
		r.Use(requireAuth(db, pac50Secret))
		r.Route("/api/admin/sso", e.h.mountAdmin)
	})
	e.router = r
	return s
}

// callback drives start (with purpose, when set) and the callback with query
// (the state is added), and waits for the handler goroutine to return.
func (s *pac75BudgetEnv) callback(t *testing.T, provider, purpose, query, sess string) *httptest.ResponseRecorder {
	t.Helper()
	path := "/api/admin/sso/stepup?provider=" + provider
	if purpose != "" {
		path += "&purpose=" + purpose
	}
	start := s.do(t, http.MethodGet, path, "", sess)
	if start.Code != http.StatusFound {
		t.Fatalf("start = %d %s", start.Code, start.Body.String())
	}
	loc, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	idp := s.google
	if provider == "microsoft" {
		idp = s.ms
	}
	idp.set(func(i *stepupIdP) { i.nonce = loc.Query().Get("nonce") })
	rec := s.do(t, http.MethodGet, stepupCallbackPath+"?"+query+"&state="+url.QueryEscape(loc.Query().Get("state")), "", sess, stateCookie(start))
	select {
	case <-s.done:
	case <-time.After(pac75Budget + 5*time.Second):
		t.Fatal("the callback's handler did not return")
	}
	return rec
}

// wantNothingStored: no step-up event, no grant, no denial window row.
func (s *pac75BudgetEnv) wantNothingStored(t *testing.T, u uuid.UUID) {
	t.Helper()
	if g, d := pac75StepupEvents(t, s.ssoEnv, u); g+d != 0 {
		t.Fatalf("%d granted and %d denied events after the budget answered", g, d)
	}
	if n := s.liveGrants(t, u); n != 0 {
		t.Fatalf("%d grants after the budget answered", n)
	}
	if ws := pac75Window(t, s.ssoEnv, u); len(ws) != 0 {
		t.Fatalf("window rows %+v after the budget answered", ws)
	}
}

func pac75WaitForBudget(t *testing.T, seam *func(context.Context)) {
	t.Helper()
	*seam = func(ctx context.Context) { <-ctx.Done() }
	t.Cleanup(func() { *seam = nil })
}

// pac75Setup prepares the provider stand-in for an admin who passes every
// check, then applies tweak (a denial), and returns a session.
func pac75Setup(t *testing.T, s *pac75BudgetEnv, provider string, tweak func(*stepupIdP)) (*storage.User, string) {
	t.Helper()
	u, org := s.member(t, "pac75-"+provider)
	if provider == "google" {
		s.google.set(googleAdmin(u.Email, org.Domain))
		if tweak != nil {
			s.google.set(tweak)
		}
	} else {
		s.ms.set(microsoftAdmin(u.Email, msTenantT1, org.Domain))
		if tweak != nil {
			s.ms.set(tweak)
		}
	}
	return u, session(t, u, time.Now())
}

// AC-25: a step-up that passed every check, with the budget expiring at the
// commit seam: reason=unavailable (purpose kept), nothing stored.
func TestPAC75_BudgetAfterGrantDecidedIsUnavailable(t *testing.T) {
	pac75WaitForBudget(t, &stepupBeforeCommit)
	for _, c := range []struct{ provider, purpose, want string }{
		{"google", "", "stepup=denied&reason=unavailable"},
		{"microsoft", "", "stepup=denied&reason=unavailable"},
		{"google", storage.SSOGrantPurposeLLMOrgKeyWrite, "stepup=denied&reason=unavailable&purpose=llm_org_key_write"},
		{"microsoft", storage.SSOGrantPurposeLLMOrgKeyWrite, "stepup=denied&reason=unavailable&purpose=llm_org_key_write"},
	} {
		t.Run(c.provider+" "+c.purpose, func(t *testing.T) {
			s := newPAC75BudgetEnv(t, sharedTestDB, nil)
			u, sess := pac75Setup(t, s, c.provider, nil)
			rec := s.callback(t, c.provider, c.purpose, "code=c1", sess)
			wantOutcome(t, rec, rec.Code, c.want)
			s.wantNothingStored(t, u.ID)
		})
	}
}

// AC-25 (reservation) and AC-26: the administrator check passed and the only
// pooled connection is held from check 4b on, or the budget expires inside
// check 4b (seam): reason=unavailable, nothing stored.
func TestPAC75_BudgetFromCheck4bIsUnavailable(t *testing.T) {
	t.Run("pool held from check 4b, google", func(t *testing.T) {
		pool := capOnePool(t)
		s := newPAC75BudgetEnv(t, pool, nil)
		u, sess := pac75Setup(t, s, "google", nil)
		var held *sql.Conn
		stepupBeforeIdentityCheck = func(context.Context) {
			if c, err := pool.Conn(context.Background()); err == nil {
				held = c
			}
		}
		t.Cleanup(func() { stepupBeforeIdentityCheck = nil })
		rec := s.callback(t, "google", "", "code=c1", sess)
		stepupBeforeIdentityCheck = nil
		if held == nil {
			t.Fatal("the seam did not take the connection")
		}
		_ = held.Close()
		wantOutcome(t, rec, rec.Code, "stepup=denied&reason=unavailable")
		s.wantNothingStored(t, u.ID)
	})
	for _, provider := range []string{"google", "microsoft"} {
		t.Run("expiry in check 4b, "+provider, func(t *testing.T) {
			pac75WaitForBudget(t, &stepupBeforeIdentityCheck)
			s := newPAC75BudgetEnv(t, sharedTestDB, nil)
			u, sess := pac75Setup(t, s, provider, nil)
			rec := s.callback(t, provider, "", "code=c1", sess)
			wantOutcome(t, rec, rec.Code, "stepup=denied&reason=unavailable")
			s.wantNothingStored(t, u.ID)
		})
	}
}

// AC-27: a denial already decided, with the budget expiring before its record
// starts: that reason (and purpose), nothing recorded or counted.
func TestPAC75_BudgetAfterDenialDecidedIsThatReason(t *testing.T) {
	pac75WaitForBudget(t, &stepupBeforeCommit)
	for _, c := range []struct {
		name, provider, purpose, query string
		tweak                          func(*stepupIdP)
		want                           string
	}{
		{"google hd_mismatch", "google", "", "code=c1", func(i *stepupIdP) { i.claims["hd"] = "elsewhere.test" }, "reason=hd_mismatch"},
		{"google no_hd", "google", "", "code=c1", func(i *stepupIdP) { i.claims["hd"] = nil }, "reason=no_hd"},
		{"google email_mismatch", "google", "", "code=c1", func(i *stepupIdP) { i.claims["email"] = "someone@else.test" }, "reason=email_mismatch"},
		{"microsoft email_mismatch", "microsoft", "", "code=c1", func(i *stepupIdP) { i.claims["preferred_username"] = "someone@else.test" }, "reason=email_mismatch"},
		{"microsoft not_admin", "microsoft", "", "code=c1", func(i *stepupIdP) { i.claims["wids"] = []string{ordinaryMember} }, "reason=not_admin"},
		{"early token_invalid with purpose", "microsoft", storage.SSOGrantPurposeLLMOrgKeyWrite, "error=access_denied", nil, "reason=token_invalid&purpose=llm_org_key_write"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newPAC75BudgetEnv(t, sharedTestDB, nil)
			u, sess := pac75Setup(t, s, c.provider, c.tweak)
			rec := s.callback(t, c.provider, c.purpose, c.query, sess)
			wantOutcome(t, rec, rec.Code, "stepup=denied&"+c.want)
			s.wantNothingStored(t, u.ID)
		})
	}
	t.Run("microsoft tid_changed", func(t *testing.T) {
		s := newPAC75BudgetEnv(t, sharedTestDB, nil)
		u, sess := pac75Setup(t, s, "microsoft", nil)
		if _, err := s.db.Exec(`UPDATE organizations SET ms_tenant_id = $2 WHERE id = $1`, *u.OrgID, msTenantT9); err != nil {
			t.Fatal(err)
		}
		rec := s.callback(t, "microsoft", "", "code=c1", sess)
		wantOutcome(t, rec, rec.Code, "stepup=denied&reason=tid_changed")
		s.wantNothingStored(t, u.ID)
	})
}

// AC-28: while checks 0-4 run, PAC-81's rule is unchanged: the running
// check's reason (check 4: not_admin; check 1: token_invalid).
func TestPAC75_BudgetDuringChecksKeepsPAC81Rule(t *testing.T) {
	t.Run("check 4 (Directory) running: not_admin", func(t *testing.T) {
		release := make(chan struct{})
		s := newPAC75BudgetEnv(t, sharedTestDB, func() { <-release })
		u, sess := pac75Setup(t, s, "google", nil)
		var rec *httptest.ResponseRecorder
		go func() {
			time.Sleep(pac75Budget + 300*time.Millisecond)
			close(release)
		}()
		rec = s.callback(t, "google", "", "code=c1", sess)
		wantOutcome(t, rec, rec.Code, "stepup=denied&reason=not_admin")
		s.wantNothingStored(t, u.ID)
	})
	t.Run("check 1 (Microsoft keys) running: token_invalid", func(t *testing.T) {
		s := newPAC75BudgetEnv(t, sharedTestDB, nil)
		u, sess := pac75Setup(t, s, "microsoft", nil)
		release := make(chan struct{})
		s.ms.set(func(i *stepupIdP) {
			i.keysHook = func(w http.ResponseWriter, r *http.Request) {
				<-release
				w.WriteHeader(http.StatusServiceUnavailable)
			}
		})
		go func() {
			time.Sleep(pac75Budget + 300*time.Millisecond)
			close(release)
		}()
		rec := s.callback(t, "microsoft", "", "code=c1", sess)
		wantOutcome(t, rec, rec.Code, "stepup=denied&reason=token_invalid")
		s.wantNothingStored(t, u.ID)
	})
}
