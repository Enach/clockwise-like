package api

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/budget"
	"github.com/Enach/paceday/backend/internal/lifecycle"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"golang.org/x/oauth2"
)

// PAC-125: no log line, request unit or error report carries a request
// path; a path parameter can be an email address or an invite token.

// withRouteMux makes r the router whose patterns logs report, for one test.
func withRouteMux(t *testing.T, r *chi.Mux) {
	t.Helper()
	prev := routeMux.Swap(r)
	t.Cleanup(func() { routeMux.Store(prev) })
}

func pac125Router(t *testing.T) *chi.Mux {
	t.Helper()
	r := chi.NewRouter()
	RegisterRoutes(r, sharedTestDB, &oauth2.Config{}, "secret", "http://app.test", "http://app.test")
	withRouteMux(t, r) // RegisterRoutes stored it; restore the previous one after
	return r
}

// The access log on both parameter routes: the pattern, never the address
// or the token, whatever status the request ends with.
func TestPAC125_AccessLogCarriesNoEmailOrToken(t *testing.T) {
	l := captureAccessLog(t)
	r := pac125Router(t)
	serveAt(r, http.MethodDelete, "/api/manager/team/members/pac125.member%40example.test", "")
	serveAt(r, http.MethodPatch, "/api/manager/team/members/pac125.member@example.test", "")
	serveAt(r, http.MethodPost, "/api/teams/invites/PAC125-TOKEN/accept", "")
	serveAt(r, http.MethodGet, "/api/no-such-route/pac125.member@example.test", "")
	all := strings.Join(l.lines, "\n")
	for _, s := range []string{"pac125.member", "example.test", "PAC125-TOKEN"} {
		if strings.Contains(all, s) {
			t.Errorf("access log carries %q:\n%s", s, all)
		}
	}
	for _, want := range []string{
		"DELETE /api/manager/team/members/{email} ",
		"PATCH /api/manager/team/members/{email} ",
		"POST /api/teams/invites/{token}/accept ",
		"GET unmatched ",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no %q line:\n%s", want, all)
		}
	}
}

// With no router registered the access log falls back to the placeholder,
// not the path.
func TestPAC125_NoRouterLogsPlaceholder(t *testing.T) {
	withRouteMux(t, nil)
	l := captureAccessLog(t)
	loggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/teams/invites/PAC125-TOKEN", nil))
	if len(l.lines) != 1 || !strings.Contains(l.lines[0], "GET unmatched 200 ") || strings.Contains(l.lines[0], "PAC125-TOKEN") {
		t.Fatalf("lines %v", l.lines)
	}
}

// logServerError, called by a handler on a router of its own (no registered
// router): the pattern chi recorded; on no route at all, the placeholder.
func TestPAC125_LogServerErrorNamesPattern(t *testing.T) {
	withRouteMux(t, nil)
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	var reported []string
	prevReport := reportServerError
	reportServerError = func(_ *http.Request, err error) { reported = append(reported, err.Error()) }
	t.Cleanup(func() { reportServerError = prevReport })

	r := chi.NewRouter()
	r.Delete("/api/manager/team/members/{email}", func(w http.ResponseWriter, r *http.Request) {
		writeServerError(w, r, "remove member", errors.New("db down"), http.StatusInternalServerError)
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodDelete, "/api/manager/team/members/pac125.member%40example.test", nil))
	logServerError(httptest.NewRequest(http.MethodGet, "/api/teams/invites/PAC125-TOKEN", nil), "op", nil, 500)
	got := buf.String()
	if !strings.Contains(got, "ERROR DELETE /api/manager/team/members/{email} -> 500: remove member: db down") ||
		!strings.Contains(got, "ERROR GET unmatched -> 500: op") {
		t.Fatalf("log %q", got)
	}
	if strings.Contains(got, "pac125.member") || strings.Contains(got, "PAC125-TOKEN") {
		t.Fatalf("log carries a parameter: %q", got)
	}
	if strings.Contains(strings.Join(reported, " "), "pac125") {
		t.Fatalf("report carries a parameter: %v", reported)
	}
}

// The request unit (named in the shutdown drain's log) is the pattern.
func TestPAC125_RequestUnitNamesPattern(t *testing.T) {
	pac125Router(t)
	g := lifecycle.NewGroup()
	t.Cleanup(lifecycle.SetDefault(g))
	release := trackRequest(httptest.NewRequest(http.MethodGet, "/api/teams/invites/PAC125-TOKEN", nil))
	defer release()
	units := g.Pending()
	if len(units) != 1 || units[0].ID != "GET /api/teams/invites/{token}" {
		t.Fatalf("units %+v", units)
	}
	release()
	g.Wait(context.Background())
}

// Review minor 3: chi routes on the escaped path when the request has one
// (an encoded "/" inside the parameter), so the label must too; otherwise
// such a request reads as unmatched while its handler runs.
func TestPAC125_EncodedSlashParamNamesPattern(t *testing.T) {
	l := captureAccessLog(t)
	r := pac125Router(t)
	serveAt(r, http.MethodDelete, "/api/manager/team/members/a%2Fpac125.member%40example.test", "")
	serveAt(r, http.MethodGet, "/api/teams/invites/PAC125%2FTOKEN", "")
	all := strings.Join(l.lines, "\n")
	for _, want := range []string{"DELETE /api/manager/team/members/{email} ", "GET /api/teams/invites/{token} "} {
		if !strings.Contains(all, want) {
			t.Errorf("no %q line:\n%s", want, all)
		}
	}
	if strings.Contains(all, "pac125.member") || strings.Contains(all, "TOKEN") {
		t.Errorf("access log carries a parameter:\n%s", all)
	}
	for _, c := range []struct{ method, target, want string }{
		{http.MethodDelete, "/api/manager/team/members/a%2Fb%40example.test", "/api/manager/team/members/{email}"},
		{http.MethodGet, "/api/teams/invites/PAC125%2FTOKEN", "/api/teams/invites/{token}"},
	} {
		if got := requestRoute(httptest.NewRequest(c.method, c.target, nil)); got != c.want {
			t.Errorf("requestRoute(%s %s) = %q, want %q", c.method, c.target, got, c.want)
		}
	}
}

// Review minor 4: signInRefused names the route pattern. The SSO callback
// carries the domain in its path; a refused sign-in (an account already
// carries the address) is logged without it.
func TestPAC125_SignInRefusedNamesPattern(t *testing.T) {
	withRouteMux(t, nil) // the pattern chi recorded on ssoRouter
	db := openTestDB(t)
	const d = "pac125-refused.test"
	idp := newFakeIdP(t, "client-"+d)
	seedOIDCProvider(t, d, idp)
	if _, err := storage.UpsertUser(db, "taken@"+d, "Taken", "", "google", "sub-pac125-taken"); err != nil {
		t.Fatal(err)
	}
	idp.email = "taken@" + d
	logs := pac71Logs(t)
	rec := httptest.NewRecorder()
	ssoRouter(&authHandlers{db: db, jwtSecret: "secret", frontendURL: pac35Frontend}).
		ServeHTTP(rec, oidcCallbackRequest(d, &http.Cookie{Name: "sso_state", Value: "s1|" + d}))
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusFound || !strings.HasSuffix(loc, signInRefusedPath) {
		t.Fatalf("not refused: %d %q", rec.Code, loc)
	}
	got := logs.String()
	if !strings.Contains(got, "pac71: GET /api/auth/callback/oidc/{domain}: ") || strings.Contains(got, "/api/auth/callback/oidc/"+d) {
		t.Fatalf("refusal line does not name the pattern alone: %q", got)
	}
}

// Review minor 4: RegisterRoutes wires budget.WithRoute(routeOf), so the
// late-panic line names the route pattern, never the path.
func TestPAC125_BudgetLatePanicNamesPattern(t *testing.T) {
	withBudget(t, 30*time.Millisecond)
	var buf lockedBuf
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	r := pac125Router(t)
	done := make(chan struct{})
	r.Get("/api/pac125-late/{token}", func(_ http.ResponseWriter, r *http.Request) {
		defer close(done)
		<-r.Context().Done()
		for !budget.Expired(r.Context()) {
			time.Sleep(time.Millisecond)
		}
		panic("pac125 late")
	})
	if rec := serveAt(r, http.MethodGet, "/api/pac125-late/PAC125-LATE-TOKEN", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want the budget's 503", rec.Code)
	}
	<-done
	for i := 0; i < 200 && !strings.Contains(buf.String(), "pac125 late"); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	got := buf.String()
	if !strings.Contains(got, "budget: GET /api/pac125-late/{token} panicked after the budget answered: pac125 late") || strings.Contains(got, "PAC125-LATE-TOKEN") {
		t.Fatalf("log %q", got)
	}
}

// lockedBuf is a log writer safe for the handler goroutine's late line.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
