package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/api"
	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/internal/lifecycle"
	"github.com/Enach/paceday/backend/internal/testdb"
	"github.com/Enach/paceday/backend/storage"
	"github.com/getsentry/sentry-go"
	"github.com/go-chi/chi/v5"
	"golang.org/x/oauth2"
)

// PAC-125: a path parameter can carry an email address or an invite token,
// a bearer secret. A server error on such a route leaves neither in the log
// (error line and access line) nor in the Sentry event.

const (
	pac125Secret = "pac125-jwt-secret"
	pac125Origin = "http://app.pac125.test"
	pac125Email  = "pac125.member@example.test"
	pac125Token  = "PAC125-SECRET-INVITE-TOKEN"
)

func TestPAC125_ServerErrorOnParamRoutesLeaksNoParam(t *testing.T) {
	tr := &stubTransport{}
	env := map[string]string{"SENTRY_DSN": "https://k@sentry.invalid/1"}
	if !initSentryFrom(func(k string) string { return env[k] }, tr) {
		t.Fatal("sentry did not initialise")
	}
	t.Cleanup(func() { _ = sentry.Init(sentry.ClientOptions{}) })

	dsn, cleanup, err := testdb.Create()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cleanup() })
	db, err := storage.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	mgr, err := storage.UpsertUser(db, "boss@pac125.test", "Boss", "", "google", "sub-pac125")
	if err != nil {
		t.Fatal(err)
	}
	gen, ok, err := storage.CurrentGeneration(db, mgr.ID)
	if err != nil || !ok {
		t.Fatalf("generation: %v %v", ok, err)
	}
	tok, err := auth.GenerateSessionToken(mgr.ID, mgr.Email, gen, pac125Secret)
	if err != nil {
		t.Fatal(err)
	}
	// PAC-129: a team the manager owns, for removeTeamPerson's {email}.
	team, err := storage.CreateTeam(db, "PAC-125", mgr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.AddTeamMember(db, team.ID, mgr.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	api.RegisterRoutes(r, db, &oauth2.Config{}, pac125Secret, pac125Origin, pac125Origin)
	// Every handler's read now fails: the member delete, the invite sweep and
	// lookup, and removeTeamPerson's read under the team lock answer 500.
	for _, q := range []string{
		`ALTER TABLE manager_team_members RENAME TO manager_team_members_pac125`,
		`ALTER TABLE team_invites RENAME TO team_invites_pac125`,
		`ALTER TABLE team_added_members RENAME TO team_added_members_pac125`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}

	for _, c := range []struct {
		method, path, pattern string
		secrets               []string
	}{
		{http.MethodDelete, "/api/manager/team/members/pac125.member%40example.test", "/api/manager/team/members/{email}",
			[]string{pac125Email, "pac125.member", "example.test"}},
		{http.MethodGet, "/api/teams/invites/" + pac125Token, "/api/teams/invites/{token}",
			[]string{pac125Token}},
		{http.MethodDelete, "/api/teams/" + team.ID.String() + "/people/pac125.member%40example.test", "/api/teams/{id}/people/{email}",
			[]string{pac125Email, "pac125.member", "example.test", team.ID.String()}},
	} {
		t.Run(c.method, func(t *testing.T) {
			logs := captureLog(t)
			tr.mu.Lock()
			tr.events = nil
			tr.mu.Unlock()
			req := httptest.NewRequest(c.method, c.path, nil)
			req.Header.Set("Origin", pac125Origin)
			// The web app's invite page refers its API call.
			req.Header.Set("Referer", pac125Origin+"/app/team/invite/"+pac125Token)
			req.AddCookie(&http.Cookie{Name: "auth_token", Value: tok})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			sentry.Flush(time.Second)
			line := logs.String()
			// The route is still named: the error line and the access line.
			if !strings.Contains(line, "ERROR "+c.method+" "+c.pattern+" -> 500") {
				t.Errorf("no error line naming the route pattern:\n%s", line)
			}
			if !strings.Contains(line, c.method+" "+c.pattern+" 500 ") {
				t.Errorf("no access line naming the route pattern:\n%s", line)
			}
			tr.mu.Lock()
			events := append([]*sentry.Event(nil), tr.events...)
			tr.mu.Unlock()
			if len(events) == 0 {
				t.Fatal("no Sentry event for the 500")
			}
			var sentURL bool
			for _, e := range events {
				b, err := json.Marshal(e)
				if err != nil {
					t.Fatal(err)
				}
				for _, s := range c.secrets {
					if strings.Contains(string(b), s) {
						t.Errorf("Sentry event carries %q: %s", s, b)
					}
				}
				sentURL = sentURL || (e.Request != nil && e.Request.URL == "http://example.com"+c.pattern)
			}
			if !sentURL {
				t.Errorf("no event names the route pattern in its request URL: %+v", events[0].Request)
			}
			for _, s := range c.secrets {
				if strings.Contains(line, s) {
					t.Errorf("log carries %q:\n%s", s, line)
				}
			}
		})
	}
}

// scrubRequest rewrites the path of a URL to its route pattern on its own,
// whatever captured the request; an unknown path becomes the placeholder.
func TestPAC125_ScrubRequestReplacesPathWithPattern(t *testing.T) {
	r := chi.NewRouter()
	api.RegisterRoutes(r, nil, &oauth2.Config{}, "s", "http://app.test", "http://app.test")
	for _, c := range []struct{ method, url, want string }{
		{http.MethodDelete, "https://api.test/api/manager/team/members/a@b.test", "https://api.test/api/manager/team/members/{email}"},
		{http.MethodPost, "https://api.test/api/teams/invites/TOK/accept", "https://api.test/api/teams/invites/{token}/accept"},
		{http.MethodGet, "https://api.test/no/such/a@b.test", "https://api.test/unmatched"},
		{http.MethodGet, "%zz", ""},
	} {
		req := &sentry.Request{URL: c.url, Method: c.method, Headers: map[string]string{"Referer": "https://app.test/app/team/invite/TOK", "Accept": "x"}}
		scrubRequest(req)
		if req.URL != c.want {
			t.Errorf("%s %s: URL %q, want %q", c.method, c.url, req.URL, c.want)
		}
		if _, ok := req.Headers["Referer"]; ok || req.Headers["Accept"] != "x" {
			t.Errorf("headers %v: want Referer dropped, Accept kept", req.Headers)
		}
	}
}

// Grouping: Sentry's default plus the unit's kind; the id only for cron and
// encryption units, never for a per-user kind.
func TestPAC125_UnitFailureFingerprint(t *testing.T) {
	tr := &stubTransport{}
	if err := sentry.Init(sentry.ClientOptions{Dsn: "https://k@sentry.invalid/1", Transport: tr}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sentry.Init(sentry.ClientOptions{}) })
	t.Cleanup(lifecycle.SetReporter(nil))
	installReporters()
	cases := []struct {
		unit lifecycle.Unit
		want []string
	}{
		{lifecycle.Unit{Kind: "cron", ID: "auto-decline"}, []string{"{{ default }}", "cron", "auto-decline"}},
		{lifecycle.Unit{Kind: "encryption", ID: "rotate"}, []string{"{{ default }}", "encryption", "rotate"}},
		{lifecycle.Unit{Kind: "habit-reoptimize", ID: "u-1"}, []string{"{{ default }}", "habit-reoptimize"}},
		{lifecycle.Unit{Kind: "manager-detection", ID: "u-2"}, []string{"{{ default }}", "manager-detection"}},
		{lifecycle.Unit{Kind: "cron"}, []string{"{{ default }}", "cron"}},
	}
	for _, c := range cases {
		lifecycle.Report(c.unit, errors.New("boom"))
	}
	sentry.Flush(time.Second)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.events) != len(cases) {
		t.Fatalf("%d events, want %d", len(tr.events), len(cases))
	}
	for i, c := range cases {
		if got := tr.events[i].Fingerprint; strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%v: fingerprint %q, want %q", c.unit, got, c.want)
		}
	}
}
