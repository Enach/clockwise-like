package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// PAC-71 sessions: the generation, requireAuth, the request lease and the
// in-request re-check (spec §3 "Sessions", "An older-generation request never
// reaches what a binding stored"; AC-42, AC-46 to AC-48, AC-50; contract §5,
// CONTRACT-1, -6, -9, -12).

const unauthorizedBody = `{"error":"unauthorized"}`

func doAs(t *testing.T, r http.Handler, method, target, body string, session *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rd)
	if session != nil {
		req.AddCookie(session)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func assert401(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusUnauthorized || strings.TrimSpace(rec.Body.String()) != unauthorizedBody {
		t.Fatalf("= %d %q, want requireAuth's 401", rec.Code, rec.Body.String())
	}
	if len(rec.Result().Header.Values("Set-Cookie")) != 0 {
		t.Fatalf("the 401 set a cookie: %v", rec.Result().Header.Values("Set-Cookie"))
	}
}

// serveGuarded runs h for a request authenticated at gen, WITHOUT the request
// lease: the situation the literal in-request re-check exists for (a binding
// that committed between authentication and the load). It applies the same
// response guard requireAuth does.
func serveGuarded(t *testing.T, h http.Handler, req *http.Request, user uuid.UUID, gen int64) *httptest.ResponseRecorder {
	t.Helper()
	g := &storage.SessionGuard{UserID: user, Gen: gen}
	ctx := context.WithValue(req.Context(), auth.UserIDKey, user)
	req = req.WithContext(storage.WithSessionGuard(ctx, g))
	rec := httptest.NewRecorder()
	gw := &guardedResponseWriter{ResponseWriter: rec, r: req, guard: g}
	h.ServeHTTP(gw, req)
	gw.finish()
	return rec
}

// commitBinding is what a rule (b) binding commits for Henry on account a:
// the identity, the standing, the advanced generation and Henry's Google
// credential (and, when ms is set, his Microsoft credential).
func commitBinding(t *testing.T, a uuid.UUID, googleAccess, msAccess string) {
	t.Helper()
	db := openTestDB(t)
	execT(t, `INSERT INTO user_identities (user_id, provider, tenant, subject, evidence, bound_by) VALUES ($1, 'google', '', $2, 'authoritative', 'rule_b')`, a, pac71GoogleID())
	execT(t, `UPDATE users SET session_generation = session_generation + 1, address_standing = 'domain_confirmed' WHERE id = $1`, a)
	if googleAccess != "" {
		if err := auth.UpsertUserToken(db, a, &oauth2.Token{AccessToken: googleAccess, RefreshToken: "r", Expiry: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	if msAccess != "" {
		if err := auth.SaveMicrosoftToken(db, a, &oauth2.Token{AccessToken: msAccess, RefreshToken: "r", Expiry: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
}

// waitForAdvisoryWaiter blocks until some transaction waits on an advisory
// lock (the binding queued behind a request lease): a deterministic seam,
// not a sleep.
func waitForAdvisoryWaiter(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := openTestDB(t).QueryRow(`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the binding never queued behind the request lease")
}

// ---- AC-42, AC-48, CONTRACT-6, CONTRACT-12 ----------------------------------

func TestPAC71_AC42_GenerationAdvanceEndsEverySessionOfTheAccount(t *testing.T) {
	d := pac71Domain()
	g1 := pac71GoogleID()
	henry := pac71Bound(t, "henry@"+d, googleIdentity(g1), storage.AddressDomainConfirmed)
	if meStatus(t, henry, 0) != http.StatusOK {
		t.Fatal("S1 not valid")
	}
	if _, err := storage.ReassignAccount(context.Background(), openTestDB(t), storage.ReassignRequest{
		Account: henry, Identity: googleIdentity(g1), Evidence: storage.EvidenceAuthoritative,
		KeepCalendarCredentials: true, Run: storage.OperatorRun{Operator: "op", Reason: "AC-42"},
	}); err != nil {
		t.Fatal(err)
	}
	if c := meStatus(t, henry, 0); c != http.StatusUnauthorized {
		t.Fatalf("S1/S2 after the advance = %d, want 401 (no cache)", c)
	}
	uid, gen := sessionOf(t, pac71Google(t, googleProfile(g1, "henry@"+d, true, d)))
	if uid != henry || gen != 1 || meStatus(t, henry, gen) != http.StatusOK {
		t.Fatalf("new sign-in session %s gen %d does not work", uid, gen)
	}
	// A refusal and a bound sign-in leave the generation alone.
	assertRefused(t, pac71Google(t, googleProfile(pac71GoogleID(), "henry@"+d, true, d)))
	if readAccount(t, henry).gen != 1 {
		t.Fatal("a refusal or a bound sign-in advanced the generation")
	}
	// A token newer than the account's generation is not valid either.
	if c := meStatus(t, henry, 2); c != http.StatusUnauthorized {
		t.Fatalf("future gen = %d", c)
	}
	// A new account starts at the initial generation (today's e2e helper).
	fresh := createTestUser(t, "fresh-"+uuid.NewString()[:8]+"@example.com")
	if meStatus(t, fresh, 0) != http.StatusOK {
		t.Fatal("a new account's generation-0 session does not work")
	}
	// A token without `gen` (issued before PAC-71) is generation 0.
	legacyTok, _ := auth.GenerateToken(fresh, "x@example.com", pac71Secret)
	if c := pac35Do(pac35Router(openTestDB(t), pac71Secret), http.MethodGet, "/api/auth/me", &http.Cookie{Name: "auth_token", Value: legacyTok}).Code; c != http.StatusOK {
		t.Fatalf("pre-PAC-71 token = %d", c)
	}
}

func TestPAC71_AC48_UnreadableGenerationIsA500NeverA401(t *testing.T) {
	u := createTestUser(t, "ac48-"+uuid.NewString()[:8]+"@example.com")
	rec := pac35Do(pac35Router(closedDB(t), pac71Secret), http.MethodGet, "/api/auth/status", sessionCookie(t, u, 0))
	if rec.Code != http.StatusInternalServerError || strings.TrimSpace(rec.Body.String()) != `{"error":"internal error"}` ||
		!strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") || len(rec.Result().Header.Values("Set-Cookie")) != 0 {
		t.Fatalf("= %d %q %v", rec.Code, rec.Body.String(), rec.Result().Header)
	}
	t.Run("an in-request re-check that cannot read is the 500 too", func(t *testing.T) {
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			storage.SessionGuardFrom(r.Context()).MarkUnavailable()
			setCookie(w, &http.Cookie{Name: "leak", Value: "x"})
			w.WriteHeader(http.StatusOK)
		})
		rec := serveGuarded(t, h, httptest.NewRequest(http.MethodGet, "/x", nil), u, 0)
		if rec.Code != http.StatusInternalServerError || len(rec.Result().Header.Values("Set-Cookie")) != 0 {
			t.Fatalf("= %d %v", rec.Code, rec.Result().Header)
		}
	})
}

func TestPAC71_CONTRACT6_StatusFallbackExcludesStaleAndUnreadableGenerations(t *testing.T) {
	u := createTestUser(t, "c6-"+uuid.NewString()[:8]+"@example.com")
	execT(t, `UPDATE users SET session_generation = 3 WHERE id = $1`, u)
	r := pac35Router(openTestDB(t), pac71Secret)
	assert401(t, doAs(t, r, http.MethodGet, "/api/auth/status", "", sessionCookie(t, u, 2)))
	if rec := doAs(t, r, http.MethodGet, "/api/auth/status", "", sessionCookie(t, u, 3)); rec.Code != http.StatusOK {
		t.Fatalf("current = %d", rec.Code)
	}
	// The re-check inside the status handler (settings and credential loads).
	ah := &authHandlers{db: openTestDB(t)}
	assert401(t, serveGuarded(t, http.HandlerFunc(ah.status), httptest.NewRequest(http.MethodGet, "/api/auth/status", nil), u, 2))
}

func TestPAC71_CONTRACT12_ReCheckOnAMissingAccountIs401(t *testing.T) {
	ghost := uuid.New()
	r := pac35Router(openTestDB(t), pac71Secret)
	// requireAuth keeps today's behaviour for a missing row ...
	if rec := doAs(t, r, http.MethodGet, "/api/auth/me", "", sessionCookie(t, ghost, 0)); rec.Code != http.StatusNotFound {
		t.Fatalf("/me = %d, want today's 404", rec.Code)
	}
	// ... and every in-request re-check answers the 401.
	assert401(t, doAs(t, r, http.MethodGet, "/api/auth/status", "", sessionCookie(t, ghost, 0)))
	// A row deleted after authentication (seam: the handler deletes it first).
	gone := createTestUser(t, "gone-"+uuid.NewString()[:8]+"@example.com")
	ah := &authHandlers{db: openTestDB(t)}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		execT(t, `DELETE FROM users WHERE id = $1`, gone)
		ah.status(w, r)
	})
	assert401(t, serveGuarded(t, h, httptest.NewRequest(http.MethodGet, "/api/auth/status", nil), gone, 0))
}

func TestPAC71_CONTRACT1_StaleGeneration401IsByteIdenticalToRequireAuths(t *testing.T) {
	u := createTestUser(t, "c1-"+uuid.NewString()[:8]+"@example.com")
	execT(t, `UPDATE users SET session_generation = 1 WHERE id = $1`, u)
	r := pac35Router(openTestDB(t), pac71Secret)
	stale := doAs(t, r, http.MethodGet, "/api/auth/me", "", sessionCookie(t, u, 0))
	none := doAs(t, r, http.MethodGet, "/api/auth/me", "", nil)
	if stale.Code != none.Code || stale.Body.String() != none.Body.String() || stale.Header().Get("Content-Type") != none.Header().Get("Content-Type") {
		t.Fatalf("stale %d %q %q vs none %d %q %q", stale.Code, stale.Body.String(), stale.Header().Get("Content-Type"), none.Code, none.Body.String(), none.Header().Get("Content-Type"))
	}
}

func TestPAC71_CONTRACT1_EveryProtectedOperationDeclares401And500(t *testing.T) {
	root := parseBundle(t, pac21Bundle)
	paths := root.get("paths")
	n := 0
	for _, p := range paths.order {
		for _, m := range []string{"get", "post", "put", "patch", "delete"} {
			op := paths.get(p, m)
			if op == nil {
				continue
			}
			if s := op.get("security"); s != nil && strings.TrimSpace(s.scalar) == "[]" {
				continue
			}
			n++
			for _, code := range []string{"'401'", "'500'"} {
				if op.get("responses", code) == nil && op.get("responses", strings.Trim(code, "'")) == nil {
					t.Errorf("%s %s declares no %s", m, p, code)
				}
			}
		}
	}
	if n < 100 {
		t.Fatalf("only %d protected operations found: parser drift", n)
	}
}

func TestPAC71_CONTRACT9_GetTeamDeletedBetweenTheReadsIs404(t *testing.T) {
	r := setupTeamRoutes(t)
	owner := createTestUser(t, "c9-"+uuid.NewString()[:8]+"@example.com")
	team, err := storage.CreateTeam(openTestDB(t), "Doomed", owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.AddTeamMember(openTestDB(t), team.ID, owner, "owner"); err != nil {
		t.Fatal(err)
	}
	getTeamAfterMembershipHook = func() { execT(t, `DELETE FROM teams WHERE id = $1`, team.ID) }
	t.Cleanup(func() { getTeamAfterMembershipHook = nil })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, withUser(httptest.NewRequest(http.MethodGet, "/api/teams/"+team.ID.String(), nil), owner))
	if rec.Code != http.StatusNotFound || strings.TrimSpace(rec.Body.String()) != `{"error":"not found"}` {
		t.Fatalf("= %d %s", rec.Code, rec.Body.String())
	}
}

// ---- AC-46, AC-47 (writes) -------------------------------------------------

// pac71Write is one of AC-46's writes made by S0 through the production
// routes; check reports whether it was stored.
type pac71Write struct {
	do    func(t *testing.T, r http.Handler, a uuid.UUID, s0 *http.Cookie) *httptest.ResponseRecorder
	check func(t *testing.T, a uuid.UUID) bool
}

func pac71Writes(t *testing.T) map[string]pac71Write {
	return map[string]pac71Write{
		"creates a team": {
			do: func(t *testing.T, r http.Handler, _ uuid.UUID, s0 *http.Cookie) *httptest.ResponseRecorder {
				return doAs(t, r, http.MethodPost, "/api/teams/", `{"name":"S0 team"}`, s0)
			},
			check: func(t *testing.T, a uuid.UUID) bool {
				var n int
				_ = openTestDB(t).QueryRow(`SELECT count(*) FROM teams WHERE created_by = $1`, a).Scan(&n)
				return n > 0
			},
		},
		"sets a personal AI key": {
			do: func(t *testing.T, r http.Handler, _ uuid.UUID, s0 *http.Cookie) *httptest.ResponseRecorder {
				return doAs(t, r, http.MethodPatch, "/api/settings/", `{"llmProvider":"openai","llmApiKey":"sk-squatter-key"}`, s0)
			},
			check: func(t *testing.T, a uuid.UUID) bool {
				var k sql.NullString
				_ = openTestDB(t).QueryRow(`SELECT llm_api_key_openai FROM settings WHERE user_id = $1`, a).Scan(&k)
				return k.String != ""
			},
		},
		"accepts a co-host invitation on B's link": {
			do: func(t *testing.T, r http.Handler, a uuid.UUID, s0 *http.Cookie) *httptest.ResponseRecorder {
				b := createTestUser(t, "b-"+uuid.NewString()[:8]+"@example.com")
				var link uuid.UUID
				if err := openTestDB(t).QueryRow(`INSERT INTO scheduling_links (owner_user_id, slug, title) VALUES ($1, md5(random()::text), 'B') RETURNING id`, b).Scan(&link); err != nil {
					t.Fatal(err)
				}
				// The pending row exists before the session acts (stored by user id).
				execT(t, `INSERT INTO scheduling_link_hosts (link_id, user_id, status) VALUES ($1, $2, 'pending')`, link, a)
				return doAs(t, r, http.MethodPost, "/api/scheduling-links/"+link.String()+"/accept", "", s0)
			},
			check: func(t *testing.T, a uuid.UUID) bool {
				var n int
				_ = openTestDB(t).QueryRow(`SELECT count(*) FROM scheduling_link_hosts WHERE user_id = $1 AND status = 'accepted'`, a).Scan(&n)
				return n > 0
			},
		},
		"completes a Slack connection": {
			// The store step of the Slack callback, under S0's session.
			do: func(t *testing.T, _ http.Handler, a uuid.UUID, _ *http.Cookie) *httptest.ResponseRecorder {
				rec := httptest.NewRecorder()
				ctx := storage.WithSessionGuard(context.Background(), &storage.SessionGuard{UserID: a, Gen: 0})
				err := storage.UpsertWorkspaceConnectionCtx(ctx, openTestDB(t), &storage.WorkspaceConnection{UserID: a, Provider: "slack", AccessToken: "xoxp-squatter", Scopes: []string{"chat:write"}})
				if err != nil {
					rec.Code = http.StatusUnauthorized
				}
				return rec
			},
			check: func(t *testing.T, a uuid.UUID) bool {
				var n int
				_ = openTestDB(t).QueryRow(`SELECT count(*) FROM workspace_connections WHERE user_id = $1`, a).Scan(&n)
				return n > 0
			},
		},
	}
}

// AC-46: S0's write is stored between G1's first check of rule (b) and the
// binding's store: the binding re-checks under the lock and is refused.
func TestPAC71_AC46_WriteInsideTheBindingWindowRefusesTheBinding(t *testing.T) {
	for name, wr := range pac71Writes(t) {
		t.Run(name, func(t *testing.T) {
			r := pac71Router(t, &oauth2.Config{})
			d := pac71Domain()
			a := pac71Legacy(t, "henry@"+d, "google", "")
			s0 := sessionCookie(t, a, 0)
			var fired atomic.Bool
			storage.SignInHooks.BeforeStore = func(storage.SignInRequest) {
				if fired.CompareAndSwap(false, true) {
					wr.do(t, r, a, s0)
				}
			}
			t.Cleanup(func() { storage.SignInHooks.BeforeStore = nil })
			assertRefused(t, pac71Google(t, googleProfile(pac71GoogleID(), "henry@"+d, true, d)))
			st := readAccount(t, a)
			if st.identity != "" || st.gen != 0 || st.googleAccess != "" {
				t.Fatalf("A = %+v, want unbound, generation 0, no credential", st)
			}
			if !wr.check(t, a) {
				t.Fatal("S0's write was not kept")
			}
		})
	}
}

// AC-47 (writes), at the re-check: S0's request was authenticated before the
// binding (its guard carries generation 0); the binding commits; the write
// is attempted: refused, nothing stored, A stays bound with Henry's
// credential. With the request lease this ordering cannot occur (next test);
// the re-check is what refuses it if it ever did.
func TestPAC71_AC47_OldGenerationWriteAfterTheBindingIsRefused(t *testing.T) {
	handlers := map[string]func(t *testing.T, a uuid.UUID) (http.Handler, *http.Request){
		"creates a team": func(t *testing.T, _ uuid.UUID) (http.Handler, *http.Request) {
			return http.HandlerFunc(newTeamHandlers(openTestDB(t), &oauth2.Config{}).createTeam), httptest.NewRequest(http.MethodPost, "/api/teams", strings.NewReader(`{"name":"S0"}`))
		},
		"sets a personal AI key": func(t *testing.T, _ uuid.UUID) (http.Handler, *http.Request) {
			return http.HandlerFunc((&settingsHandlers{db: openTestDB(t)}).patchSettings), httptest.NewRequest(http.MethodPatch, "/api/settings", strings.NewReader(`{"llmProvider":"openai","llmApiKey":"sk-squatter-key"}`))
		},
	}
	for name, mk := range handlers {
		t.Run(name, func(t *testing.T) {
			a := pac71Legacy(t, "henry@"+pac71Domain(), "google", "")
			commitBinding(t, a, "henry-g1", "")
			h, req := mk(t, a)
			assert401(t, serveGuarded(t, h, req, a, 0))
			if pac71Writes(t)[name].check(t, a) {
				t.Fatal("the old-generation write was stored")
			}
			if st := readAccount(t, a); st.googleAccess != "henry-g1" || st.gen != 1 {
				t.Fatalf("A = %+v", st)
			}
		})
	}
	t.Run("completes a Slack connection", func(t *testing.T) {
		a := pac71Legacy(t, "henry@"+pac71Domain(), "google", "")
		commitBinding(t, a, "henry-g1", "")
		rec := pac71Writes(t)["completes a Slack connection"].do(t, nil, a, nil)
		if rec.Code != http.StatusUnauthorized || pac71Writes(t)["completes a Slack connection"].check(t, a) {
			t.Fatal("the old-generation Slack store was not refused")
		}
	})
	t.Run("accepts a co-host invitation", func(t *testing.T) {
		a := pac71Legacy(t, "henry@"+pac71Domain(), "google", "")
		b := createTestUser(t, "b-"+uuid.NewString()[:8]+"@example.com")
		var link uuid.UUID
		if err := openTestDB(t).QueryRow(`INSERT INTO scheduling_links (owner_user_id, slug, title) VALUES ($1, md5(random()::text), 'B') RETURNING id`, b).Scan(&link); err != nil {
			t.Fatal(err)
		}
		execT(t, `INSERT INTO scheduling_link_hosts (link_id, user_id, status) VALUES ($1, $2, 'pending')`, link, a)
		commitBinding(t, a, "henry-g1", "")
		r := setupSchedulingLinkRoutes(t)
		req := httptest.NewRequest(http.MethodPost, "/api/scheduling-links/"+link.String()+"/accept", nil)
		assert401(t, serveGuarded(t, r, req, a, 0))
		var status string
		_ = openTestDB(t).QueryRow(`SELECT status FROM scheduling_link_hosts WHERE link_id = $1 AND user_id = $2`, link, a).Scan(&status)
		if status != "pending" {
			t.Fatalf("status = %s", status)
		}
	})
}

// AC-47 / AC-50 with the request lease: a binding cannot commit while a
// request authenticated under the old generation is running. It queues; the
// request finishes on the account as it was (empty); the binding then re-checks
// and decides; S0's next request is 401.
func TestPAC71_AC47_AC50_TheBindingWaitsForOldGenerationRequests(t *testing.T) {
	cal := newPAC67Calendar(t)
	r := pac71Router(t, &oauth2.Config{Endpoint: oauth2.Endpoint{TokenURL: "http://127.0.0.1:1/token"}})
	d := pac71Domain()
	a := pac71Legacy(t, "henry@"+d, "google", "")
	s0 := sessionCookie(t, a, 0)

	var binding *httptest.ResponseRecorder
	done := make(chan struct{})
	afterAuthLeaseHook = func(*http.Request) {
		afterAuthLeaseHook = nil
		go func() {
			defer close(done)
			binding = pac71Google(t, googleProfile(pac71GoogleID(), "henry@"+d, true, d))
		}()
		waitForAdvisoryWaiter(t)
		if st := readAccount(t, a); st.identity != "" {
			t.Error("the binding committed while S0's request was in flight")
		}
	}
	t.Cleanup(func() { afterAuthLeaseHook = nil })
	rec := doAs(t, r, http.MethodGet, "/api/calendar/events?start=2030-01-07T00:00:00Z&end=2030-01-08T00:00:00Z", "", s0)
	<-done
	if strings.Contains(rec.Body.String(), "busy-1") {
		t.Fatal("S0 received calendar data")
	}
	uid, _ := sessionOf(t, binding)
	st := readAccount(t, a)
	if uid != a || st.googleAccess == "" {
		t.Fatalf("binding = %+v", st)
	}
	if cal.saw(st.googleAccess) {
		t.Fatal("the provider received a call with Henry's credential")
	}
	assert401(t, doAs(t, r, http.MethodGet, "/api/calendar/events", "", s0))
}

// ---- AC-50 (reads and provider calls), at the re-check ---------------------

func TestPAC71_AC50_OldGenerationRequestNeverUsesWhatTheBindingStored(t *testing.T) {
	w := newPAC67World(t)
	t.Run("(a) calendar read", func(t *testing.T) {
		a := pac71Legacy(t, "henry@"+pac71Domain(), "google", "")
		commitBinding(t, a, "henry-a", "")
		w.cal.setBusy("henry-a")
		rec := serveGuarded(t, w.r, httptest.NewRequest(http.MethodGet, "/api/calendar/events?start=2030-01-07T00:00:00Z&end=2030-01-08T00:00:00Z", nil), a, 0)
		assert401(t, rec)
		if w.cal.saw("henry-a") {
			t.Fatal("the provider received a call with Henry's credential")
		}
	})
	t.Run("(b) meeting create", func(t *testing.T) {
		a := pac71Legacy(t, "henry@"+pac71Domain(), "google", "")
		commitBinding(t, a, "henry-b", "")
		before := w.cal.insertCount()
		h := http.HandlerFunc((&scheduleHandlers{db: w.db, smart: &engine.SmartScheduler{DB: w.db, OAuthConfig: w.google}}).createMeeting)
		rec := serveGuarded(t, h, httptest.NewRequest(http.MethodPost, "/api/schedule/create", strings.NewReader(`{"title":"T","start":"2030-01-07T10:00:00Z","end":"2030-01-07T10:30:00Z"}`)), a, 0)
		assert401(t, rec)
		if w.cal.saw("henry-b") || w.cal.insertCount() != before {
			t.Fatal("a meeting was created with Henry's credential")
		}
	})
	t.Run("(c) Microsoft binding: Teams link and Outlook free/busy", func(t *testing.T) {
		a := pac71Legacy(t, "henry@"+pac71Domain(), "microsoft", "")
		commitBinding(t, a, "", "henry-ms-c")
		execT(t, `UPDATE settings SET conferencing_provider = 'teams' WHERE user_id = $1`, a)
		assert401(t, serveGuarded(t, w.r, httptest.NewRequest(http.MethodPost, "/api/conference/create", strings.NewReader(pac67Meeting)), a, 0))
		assert401(t, serveGuarded(t, w.r, httptest.NewRequest(http.MethodPost, "/api/freebusy", strings.NewReader(`{"emails":["x@y.test"],"start_time":"2030-01-07T09:00:00Z","end_time":"2030-01-07T17:00:00Z"}`)), a, 0))
		if w.d.Saw("henry-ms-c") {
			t.Fatal("Graph received a call with Henry's Microsoft credential")
		}
	})
	t.Run("(d) personal calendar added after the binding", func(t *testing.T) {
		var hits atomic.Int64
		ics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			_, _ = w.Write([]byte("BEGIN:VCALENDAR\nEND:VCALENDAR\n"))
		}))
		t.Cleanup(ics.Close)
		a := pac71Legacy(t, "henry@"+pac71Domain(), "google", "")
		commitBinding(t, a, "henry-d", "")
		var calID int64
		if err := w.db.QueryRow(`INSERT INTO personal_calendars (user_id, provider, name, url) VALUES ($1, 'ics', 'Home', $2) RETURNING id`, a, ics.URL+"/h.ics").Scan(&calID); err != nil {
			t.Fatal(err)
		}
		ph := newPersonalHandlers(w.db, w.google)
		pr := chi.NewRouter()
		pr.Get("/api/personal-calendars", ph.list)
		pr.Post("/api/personal-calendars/{id}/sync", ph.sync)
		assert401(t, serveGuarded(t, pr, httptest.NewRequest(http.MethodGet, "/api/personal-calendars", nil), a, 0))
		assert401(t, serveGuarded(t, pr, httptest.NewRequest(http.MethodPost, "/api/personal-calendars/"+itoa(calID)+"/sync", nil), a, 0))
		if hits.Load() != 0 {
			t.Fatal("the calendar URL was fetched")
		}
	})
	t.Run("sequential: authenticated after the binding is 401 at requireAuth", func(t *testing.T) {
		a := pac71Legacy(t, "henry@"+pac71Domain(), "google", "")
		commitBinding(t, a, "henry-s", "")
		if c := meStatus(t, a, 0); c != http.StatusUnauthorized {
			t.Fatalf("= %d", c)
		}
	})
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
