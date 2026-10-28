package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// PAC-71 AC-47 and AC-50 through the production routes: S0's request passes
// requireAuth, then (seam afterAuthHook) Henry's rule (b) binding commits,
// then the handler loads or writes. The in-request re-check refuses it: 401,
// nothing stored, nothing returned, no provider call with Henry's credential.
// No connection is held between authentication and the handler.

// bindDuringRequest arms afterAuthHook to run Henry's real binding sign-in
// on account a once, between S0's authentication and its handler.
func bindDuringRequest(t *testing.T, d string, a uuid.UUID) {
	t.Helper()
	afterAuthHook = func(*http.Request) {
		afterAuthHook = nil
		uid, gen := sessionOf(t, pac71Google(t, googleProfile(pac71GoogleID(), "henry@"+d, true, d)))
		if uid != a || gen != 1 {
			t.Fatalf("binding reached %s gen %d", uid, gen)
		}
	}
	t.Cleanup(func() { afterAuthHook = nil })
}

func TestPAC71_AC47_AC50_InterleavedThroughRequireAuth(t *testing.T) {
	cases := map[string]struct {
		method, path, body string
		stored             func(t *testing.T, a uuid.UUID) bool
	}{
		"creates a team": {http.MethodPost, "/api/teams/", `{"name":"S0 team"}`, pac71Writes(t)["creates a team"].check},
		"sets a personal AI key": {http.MethodPatch, "/api/settings/", `{"llmProvider":"openai","llmApiKey":"sk-squatter"}`,
			pac71Writes(t)["sets a personal AI key"].check},
		"creates a scheduling link": {http.MethodPost, "/api/scheduling-links/",
			`{"title":"S0","slug":"pac71-s0-` + uuid.NewString()[:8] + `","duration_options":[30]}`,
			func(t *testing.T, a uuid.UUID) bool {
				var n int
				_ = openTestDB(t).QueryRow(`SELECT count(*) FROM scheduling_links WHERE owner_user_id = $1`, a).Scan(&n)
				return n > 0
			}},
		"(AC-50 a) lists calendar events": {http.MethodGet, "/api/calendar/events?start=2030-01-07T00:00:00Z&end=2030-01-08T00:00:00Z", "", nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			cal := newPAC67Calendar(t)
			r := pac71Router(t, &oauth2.Config{})
			d := pac71Domain()
			a := pac71Legacy(t, "henry@"+d, "google", "")
			bindDuringRequest(t, d, a)
			rec := doAs(t, r, c.method, c.path, c.body, sessionCookie(t, a, 0))
			assert401(t, rec)
			st := readAccount(t, a)
			if st.identity == "" || st.gen != 1 || st.googleAccess == "" {
				t.Fatalf("A = %+v, want bound to Henry with his credential", st)
			}
			if c.stored != nil && c.stored(t, a) {
				t.Fatal("the old-generation write was stored after the binding")
			}
			if cal.saw(st.googleAccess) {
				t.Fatal("the provider received a call with Henry's credential")
			}
		})
	}
}

// Team-invite acceptance: the account is domain-confirmed (it may accept);
// an operator step advances its generation between S0's authentication and
// the acceptance.
func TestPAC71_AC47_TeamInviteAcceptanceAfterAnAdvanceIsRefused(t *testing.T) {
	r := pac71Router(t, &oauth2.Config{})
	d := pac71Domain()
	g1 := pac71GoogleID()
	a := pac71Bound(t, "henry@"+d, googleIdentity(g1), storage.AddressDomainConfirmed)
	owner := createConfirmedUser(t, "own-"+uuid.NewString()[:8]+"@example.com")
	team, err := storage.CreateTeam(openTestDB(t), "T", owner)
	if err != nil {
		t.Fatal(err)
	}
	inv, err := storage.CreateTeamInvite(openTestDB(t), team.ID, "henry@"+d, owner)
	if err != nil {
		t.Fatal(err)
	}
	afterAuthHook = func(*http.Request) {
		afterAuthHook = nil
		if _, err := storage.ReassignAccount(context.Background(), openTestDB(t), storage.ReassignRequest{
			Account: a, Identity: googleIdentity(g1), Evidence: storage.EvidenceAuthoritative, KeepCalendarCredentials: true,
			Run: storage.OperatorRun{Operator: "op", Reason: "AC-47"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { afterAuthHook = nil })
	assert401(t, doAs(t, r, http.MethodPost, "/api/teams/invites/"+inv.Token+"/accept", "", sessionCookie(t, a, 0)))
	if _, err := storage.GetTeamMember(openTestDB(t), team.ID, a); err == nil {
		t.Fatal("the old-generation acceptance added a membership")
	}
}

// Zoom store: the Zoom callback of a request authenticated before the binding
// stores nothing after it.
func TestPAC71_AC47_ZoomStoreAfterTheBindingIsRefused(t *testing.T) {
	w := newPAC67World(t)
	a := pac71Legacy(t, "henry@"+pac71Domain(), "google", "")
	commitBinding(t, a, "henry-z", "")
	w.d.ExchangeAnswer("zoom-squatter", "zoom-r")
	req := httptest.NewRequest(http.MethodGet, "/api/auth/zoom/callback?state=s1&code=c1", nil)
	req.AddCookie(&http.Cookie{Name: "zoom_oauth_state", Value: "s1"})
	rec := serveGuarded(t, http.HandlerFunc(w.ch.zoomCallback), req, a, 0)
	assert401(t, rec)
	if c, _ := storage.LoadZoomCredential(w.db, a); c != nil {
		t.Fatalf("a Zoom credential was stored: %+v", c)
	}
}

// Scheduling-link creation at the re-check (the literal order).
func TestPAC71_AC47_SchedulingLinkCreationAfterTheBindingIsRefused(t *testing.T) {
	a := pac71Legacy(t, "henry@"+pac71Domain(), "google", "")
	commitBinding(t, a, "henry-l", "")
	req := httptest.NewRequest(http.MethodPost, "/api/scheduling-links", strings.NewReader(`{"title":"S0","slug":"pac71-l-`+uuid.NewString()[:8]+`","duration_options":[30]}`))
	assert401(t, serveGuarded(t, setupSchedulingLinkRoutes(t), req, a, 0))
	var n int
	_ = openTestDB(t).QueryRow(`SELECT count(*) FROM scheduling_links WHERE owner_user_id = $1`, a).Scan(&n)
	if n != 0 {
		t.Fatal("a link was created")
	}
}

// No request holds a database connection while its handler waits on
// something that is not the database (an outbound provider call): N requests
// in flight leave no connection idle in transaction (review of #242).
func TestPAC71_NoConnectionHeldWhileTheHandlerWaits(t *testing.T) {
	db := openTestDB(t)
	release := make(chan struct{})
	var entered sync.WaitGroup
	const n = 20
	entered.Add(n)
	h := requireAuth(db, pac71Secret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered.Done()
		<-release // stands in for a slow ICS, Google, Graph or LLM call
		w.WriteHeader(http.StatusNoContent)
	}))
	users := make([]uuid.UUID, n)
	for i := range users {
		users[i] = createTestUser(t, "held-"+uuid.NewString()[:8]+"@example.com")
	}
	var done sync.WaitGroup
	done.Add(n)
	for _, u := range users {
		go func(u uuid.UUID) {
			defer done.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			req.AddCookie(sessionCookie(t, u, 0))
			h.ServeHTTP(rec, req)
		}(u)
	}
	entered.Wait()
	var idleInTx, open int
	if err := db.QueryRow(`SELECT count(*) FILTER (WHERE state LIKE 'idle in transaction%'), count(*)
		FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid()`).Scan(&idleInTx, &open); err != nil {
		t.Fatal(err)
	}
	stats := db.Stats()
	close(release)
	done.Wait()
	if idleInTx != 0 {
		t.Fatalf("%d connections idle in transaction with %d requests waiting", idleInTx, n)
	}
	if stats.InUse > 1 { // this test's own query may still be counted
		t.Fatalf("%d pool connections in use while %d handlers wait on a non-DB call", stats.InUse, n)
	}
}
