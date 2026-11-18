package api

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/internal/lifecycle"
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
