package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/internal/budget"
	"github.com/Enach/paceday/backend/internal/lifecycle"
	"github.com/Enach/paceday/backend/internal/outbound"
	"github.com/Enach/paceday/backend/nlp"
	"github.com/Enach/paceday/backend/nlp/nlptest"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// PAC-81 Part A, request paths. Database tests run against the shared test
// database; a second pool with a cap of 1 is opened on it for AC-A9/AC-A10.

func pac81Group(t *testing.T) *lifecycle.Group {
	t.Helper()
	g := lifecycle.NewGroup()
	t.Cleanup(lifecycle.SetDefault(g))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		g.Wait(ctx)
	})
	return g
}

func capOnePool(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", sharedTestDSN)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

var pac81IP atomic.Int32

// pac81Addr is a distinct client address per request, so the booking abuse
// limits never interfere.
func pac81Addr() string {
	n := pac81IP.Add(1)
	return fmt.Sprintf("198.18.%d.%d:4000", (n/250)%250, n%250+1)
}

// pac81Link seeds a link with one accepted host (no calendar: no events).
func pac81Link(t *testing.T) *storage.SchedulingLink {
	t.Helper()
	host := createTestUser(t, "pac81-host-"+uuid.NewString()[:8]+"@example.com")
	link, err := storage.CreateSchedulingLink(sharedTestDB, &storage.SchedulingLink{
		OwnerUserID: host, Slug: "pac81-" + uuid.NewString()[:8], Title: "Intro",
		DurationOptions: []int{30}, DaysOfWeek: []int{0, 1, 2, 3, 4, 5, 6}, WindowStart: "00:00", WindowEnd: "23:45",
		UsageType: "reusable", Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storage.AddLinkHost(sharedTestDB, link.ID, host, "accepted"); err != nil {
		t.Fatal(err)
	}
	return link
}

func bookingRouter(db *sql.DB) *chi.Mux {
	bh := newBookingHandlers(db, &oauth2.Config{})
	r := chi.NewRouter()
	r.Get("/api/book/{slug}/slots", bh.getSlots)
	r.Post("/api/book/{slug}", bh.createBooking)
	return r
}

func pac81Start(hoursAhead int) time.Time {
	return time.Now().UTC().Truncate(time.Hour).Add(time.Duration(hoursAhead) * time.Hour)
}

func pac81BookingBody(name string, start time.Time) string {
	return fmt.Sprintf(`{"name":%q,"email":"booker@example.com","start":%q,"duration":30}`, name, start.Format(time.RFC3339))
}

func bookingCount(t *testing.T, linkID uuid.UUID) int {
	t.Helper()
	var n int
	if err := sharedTestDB.QueryRow(`SELECT COUNT(*) FROM bookings WHERE link_id = $1`, linkID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// withBudget sets the handler budget of RegisterRoutes for the test.
func withBudget(t *testing.T, d time.Duration) {
	t.Helper()
	prev := requestBudget
	requestBudget = d
	t.Cleanup(func() { requestBudget = prev })
}

func fullRouter(t *testing.T) *chi.Mux {
	t.Helper()
	r := chi.NewRouter()
	RegisterRoutes(r, sharedTestDB, &oauth2.Config{}, "secret", "http://app.test", "http://app.test")
	return r
}

func serveAt(r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = pac81Addr()
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// --- AC-A19: booking create and the budget ------------------------------------

// The insert is held (table lock) past the budget: the answer is 201 and
// the row is saved, never the 503.
func TestPAC81_BookingInsertHeldPastBudgetIs201(t *testing.T) {
	pac81Group(t)
	withBudget(t, 400*time.Millisecond)
	link := pac81Link(t)
	r := fullRouter(t)
	tx, err := sharedTestDB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`LOCK TABLE bookings IN EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	go func() { time.Sleep(900 * time.Millisecond); _ = tx.Commit() }()
	start := time.Now()
	rec := serveAt(r, http.MethodPost, "/api/book/"+link.Slug, pac81BookingBody("Held", pac81Start(48)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d %s, want 201", rec.Code, rec.Body.String())
	}
	if time.Since(start) < 800*time.Millisecond {
		t.Fatal("the insert was not held past the budget")
	}
	if bookingCount(t, link.ID) != 1 {
		t.Fatal("row not saved")
	}
}

// The insert fails after the budget: 500 (which does not prove that nothing
// was saved), never 503.
func TestPAC81_BookingInsertFailsAfterBudgetIs500(t *testing.T) {
	pac81Group(t)
	withBudget(t, 400*time.Millisecond)
	link := pac81Link(t)
	r := fullRouter(t)
	defer func() {
		_, _ = sharedTestDB.Exec(`ALTER TABLE bookings DROP CONSTRAINT IF EXISTS pac81_fail`)
	}()
	tx, err := sharedTestDB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`LOCK TABLE bookings IN EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(700 * time.Millisecond) // the insert is waiting by now
		_, _ = tx.Exec(`ALTER TABLE bookings ADD CONSTRAINT pac81_fail CHECK (booker_name <> 'pac81-fail') NOT VALID`)
		_ = tx.Commit()
	}()
	rec := serveAt(r, http.MethodPost, "/api/book/"+link.Slug, pac81BookingBody("pac81-fail", pac81Start(49)))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got %d %s, want 500", rec.Code, rec.Body.String())
	}
	if bookingCount(t, link.ID) != 0 {
		t.Fatal("a row was saved")
	}
}

// The budget runs out before the insert is sent: 503, no row.
func TestPAC81_BookingBudgetBeforeInsertIs503NoRow(t *testing.T) {
	pac81Group(t)
	withBudget(t, 300*time.Millisecond)
	link := pac81Link(t)
	r := fullRouter(t)
	tx, err := sharedTestDB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`LOCK TABLE scheduling_links IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	rec := serveAt(r, http.MethodPost, "/api/book/"+link.Slug, pac81BookingBody("Late", pac81Start(50)))
	_ = tx.Rollback()
	if rec.Code != http.StatusServiceUnavailable || rec.Body.String() != "{\"error\":\"request timed out\"}\n" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
	time.Sleep(200 * time.Millisecond)
	if bookingCount(t, link.ID) != 0 {
		t.Fatal("a booking was written after the 503")
	}
}

// --- AC-A2 / AC-A18: slot listing ---------------------------------------------

type cutEngine struct {
	calls  int
	err    error
	cancel context.CancelFunc // when set, the request ends at the cut
}

func (e *cutEngine) CollectiveSlots(ctx context.Context, _ *storage.SchedulingLink, _ time.Time, _ int) ([]engine.AvailableSlot, error) {
	e.calls++
	if e.calls == 3 {
		if e.cancel != nil {
			e.cancel()
		}
		return nil, e.err
	}
	return []engine.AvailableSlot{{Start: time.Now().Add(72 * time.Hour), End: time.Now().Add(73 * time.Hour)}}, nil
}

func (e *cutEngine) ConfirmBooking(context.Context, *storage.SchedulingLink, string, string, time.Time, time.Time, string) (*storage.Booking, error) {
	return nil, errors.New("unused")
}

// A 60-day listing cut mid-loop is a 503, not a truncated 200; a dated
// listing cut by the budget is a 503 too.
func TestPAC81_SlotListingCutIs503(t *testing.T) {
	link := pac81Link(t)
	for _, path := range []string{"/api/book/" + link.Slug + "/slots", "/api/book/" + link.Slug + "/slots?date=2030-01-07"} {
		eng := &cutEngine{err: budget.ErrExpired}
		if strings.Contains(path, "date=") {
			eng.calls = 2 // the first call is the cut one
		}
		bh := newBookingHandlersWithEngine(sharedTestDB, eng)
		r := chi.NewRouter()
		r.Get("/api/book/{slug}/slots", bh.getSlots)
		rec := serveAt(r, http.MethodGet, path, "")
		if rec.Code != http.StatusServiceUnavailable || rec.Body.String() != "{\"error\":\"request timed out\"}\n" {
			t.Fatalf("%s: %d %q", path, rec.Code, rec.Body.String())
		}
	}
	// A cancelled request context with a plain error also ends in 503.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng := &cutEngine{err: errors.New("context canceled"), cancel: cancel}
	bh := newBookingHandlersWithEngine(sharedTestDB, eng)
	r := chi.NewRouter()
	r.Get("/api/book/{slug}/slots", bh.getSlots)
	req := httptest.NewRequest(http.MethodGet, "/api/book/"+link.Slug+"/slots", nil).WithContext(ctx)
	req.RemoteAddr = pac81Addr()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("cancelled 60-day listing: %d", rec.Code)
	}
}

// --- AC-A2: browser-navigation callbacks ---------------------------------------

// slowOAuthProvider answers the token exchange after delay.
func slowOAuthProvider(t *testing.T, delay time.Duration) *fakeOAuthProvider {
	t.Helper()
	p := &fakeOAuthProvider{profile: jsonProfile(map[string]string{"id": "x", "email": "slow@example.com", "mail": "slow@example.com"})}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc("/profile", func(w http.ResponseWriter, _ *http.Request) { p.profile(w) })
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func TestPAC81_SignInCallbacksRedirectAtBudget(t *testing.T) {
	p := slowOAuthProvider(t, 2*time.Second)
	ah := &authHandlers{db: sharedTestDB, jwtSecret: "secret", frontendURL: pac35Frontend,
		oauthConfig: &oauth2.Config{ClientID: "g", ClientSecret: "s", RedirectURL: "http://localhost/cb", Endpoint: p.endpoint()}}
	t.Setenv("MICROSOFT_CLIENT_ID", "ms-client")
	t.Setenv("MICROSOFT_CLIENT_SECRET", "ms-secret")
	t.Setenv("MICROSOFT_REDIRECT_URL", "http://localhost/ms/cb")
	prevEndpoint := microsoftOAuthEndpoint
	microsoftOAuthEndpoint = p.endpoint()
	defer func() { microsoftOAuthEndpoint = prevEndpoint }()

	cases := []struct {
		name    string
		handler http.HandlerFunc
		path    string
		cookie  *http.Cookie
	}{
		{"google", ah.callback, "/api/auth/callback?state=s1&code=c1", &http.Cookie{Name: "oauth_state", Value: "s1"}},
		{"microsoft", ah.microsoftCallback, "/api/auth/microsoft/callback?state=s1&code=c1", &http.Cookie{Name: "ms_oauth_state", Value: "s1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, c.path, nil)
			req.AddCookie(c.cookie)
			rec := httptest.NewRecorder()
			budget.Middleware(200*time.Millisecond, nil)(c.handler).ServeHTTP(rec, req)
			if rec.Code != http.StatusFound || rec.Header().Get("Location") != pac35Frontend+signInFailurePath {
				t.Fatalf("got %d Location %q body %q", rec.Code, rec.Header().Get("Location"), rec.Body.String())
			}
			if ct := rec.Header().Get("Content-Type"); strings.Contains(ct, "json") {
				t.Fatalf("JSON on a browser navigation: %q", ct)
			}
		})
	}

	// SSO: the provider lookup waits on a locked table past the budget.
	t.Run("sso", func(t *testing.T) {
		tx, err := sharedTestDB.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.Exec(`LOCK TABLE sso_providers IN ACCESS EXCLUSIVE MODE`); err != nil {
			t.Fatal(err)
		}
		r := chi.NewRouter()
		r.Use(budget.Middleware(200*time.Millisecond, nil))
		r.Mount("/", ssoRouter(ah))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, oidcCallbackRequest("pac81.test", &http.Cookie{Name: "sso_state", Value: "s1|pac81.test"}))
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != pac35Frontend+signInFailurePath {
			t.Fatalf("got %d Location %q", rec.Code, rec.Header().Get("Location"))
		}
	})
}

// Step-up callback at the budget before check 0: denied, state_mismatch.
func TestPAC81_StepupCallbackAtBudgetBeforeCheck0(t *testing.T) {
	e := newSSOEnv(t)
	u, _ := e.member(t, "pac81")
	r := chi.NewRouter()
	r.Use(budget.Middleware(200*time.Millisecond, nil))
	r.Group(func(r chi.Router) {
		r.Use(requireAuth(pac50Secret))
		r.Route("/api/admin/sso", e.h.mountAdmin)
	})
	e.router = r
	tx, err := e.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`LOCK TABLE users IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, http.MethodGet, stepupCallbackPath+"?code=c1&state=x", "", session(t, u, time.Now()))
	_ = tx.Rollback()
	want := pac50Frontend + "/settings?stepup=denied&reason=state_mismatch"
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != want {
		t.Fatalf("got %d Location %q", rec.Code, rec.Header().Get("Location"))
	}
	time.Sleep(200 * time.Millisecond) // let the blocked handler finish
}

// --- AC-A4: the budget exceeds every handler's own deadline by 30 s ----------

func TestPAC81_BudgetExceedsHandlerDeadlines(t *testing.T) {
	l := outbound.Default
	sums := map[string]time.Duration{
		"natural-language parse (two LLM calls)":       2 * l.LLM,
		"Google sign-in callback (exchange, userinfo)": 2 * l.Identity,
		"SSO callback (discovery, token, JWKS)":        3 * l.Identity,
	}
	files, _ := filepath.Glob("handlers_*.go")
	re := regexp.MustCompile(`context\.WithTimeout\(r\.Context\(\), (\d+)\s*\*\s*time\.Second\)`)
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			n, _ := strconv.Atoi(m[1])
			sums[f+" deadline "+m[1]+"s"] = time.Duration(n) * time.Second
		}
	}
	if len(sums) < 5 {
		t.Fatalf("handler deadlines not found: %v", sums)
	}
	for name, d := range sums {
		if budget.Default-d < 30*time.Second {
			t.Errorf("%s: %s leaves %s of the %s budget, want >= 30 s", name, d, budget.Default-d, budget.Default)
		}
	}
}

// --- AC-A8: team-invite email failure ------------------------------------------

func TestPAC81_InviteEmailFailureLogged(t *testing.T) {
	g := pac81Group(t)
	prev := sendMail
	var calls atomic.Int32
	sendMail = func(string, smtp.Auth, string, []string, []byte) error {
		calls.Add(1)
		return errors.New("smtp rcpt: 550 <invitee-pac81@example.com> rejected")
	}
	defer func() { sendMail = prev }()
	t.Setenv("SMTP_HOST", "smtp.pac81.test")
	t.Setenv("SMTP_FROM", "noreply@example.com")
	var buf bytes.Buffer
	prevOut := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prevOut)

	r := setupTeamRoutes(t)
	owner := createTestUser(t, "pac81-invite-owner@example.com")
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/teams", strings.NewReader(`{"name":"PAC81"}`)), owner)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var team struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &team)
	req = withUser(httptest.NewRequest(http.MethodPost, "/api/teams/"+team.ID+"/members/invite", strings.NewReader(`{"email":"invitee-pac81@example.com"}`)), owner)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite: %d %s", rec.Code, rec.Body.String())
	}
	var invite struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &invite)
	g.Wait(context.Background())
	out := buf.String()
	if !strings.Contains(out, "ERROR: team invite email failed for invite "+invite.ID) {
		t.Fatalf("log %q", out)
	}
	if strings.Contains(out, "invitee-pac81@") || calls.Load() != 1 {
		t.Fatalf("address logged or retried (%d sends): %q", calls.Load(), out)
	}
}

// --- AC-A9 / AC-A10 / AC-A11 with a pool of 1 ---------------------------------

// pac81Paths are the named request paths, each run against db.
type pac81Path struct {
	name string
	want int
	run  func(t *testing.T, db *sql.DB, ctx context.Context) *httptest.ResponseRecorder
}

func pac81NamedPaths(t *testing.T) []pac81Path {
	t.Helper()
	link := pac81Link(t)
	day := time.Now().UTC().Add(72 * time.Hour).Format("2006-01-02")

	// A stub Google calendar for the personal sync's work calendar.
	gsrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"id":"ev-` + uuid.NewString()[:6] + `"}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(gsrv.Close)
	calendar.Configure(config.CalendarEndpoint{BaseURL: gsrv.URL + config.CalendarAPIRoot, Guard: true})
	t.Cleanup(func() { calendar.Configure(config.ProductionCalendarEndpoint()) })
	syncUser := createTestUser(t, "pac81-sync-"+uuid.NewString()[:6]+"@example.com")
	if err := auth.UpsertUserToken(sharedTestDB, syncUser, &oauth2.Token{AccessToken: "a", RefreshToken: "r", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	evStart := time.Now().UTC().Truncate(24 * time.Hour).Add(72*time.Hour + 10*time.Hour)
	for evStart.Weekday() == time.Saturday || evStart.Weekday() == time.Sunday {
		evStart = evStart.Add(24 * time.Hour)
	}
	ics := fmt.Sprintf("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:pac81\r\nSUMMARY:x\r\nDTSTART:%s\r\nDTEND:%s\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n",
		evStart.Format("20060102T150405Z"), evStart.Add(time.Hour).Format("20060102T150405Z"))
	isrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(ics)) }))
	t.Cleanup(isrv.Close)
	calID, err := storage.InsertPersonalCalendar(sharedTestDB, &storage.PersonalCalendar{UserID: syncUser, Provider: "webcal", Name: "Home", URL: isrv.URL, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	// A configured user LLM answered by a recording transport.
	llmUser := newPAC66B(t, "pac81-parse-"+uuid.NewString()[:6]+"@example.com", "", "", func(s *storage.Settings) {
		s.LLMProvider = "openai"
		keyed(s)
	}).user

	p := newFakeOAuthProvider(t)
	p.profile = jsonProfile(map[string]string{"id": "pac81-" + uuid.NewString()[:6], "email": "pac81-signin-" + uuid.NewString()[:6] + "@example.com", "mail": "pac81-ms-" + uuid.NewString()[:6] + "@example.com", "name": "P"})
	prevG, prevMS, prevGraph := googleUserInfoURL, microsoftOAuthEndpoint, microsoftGraphMeURL
	googleUserInfoURL, microsoftOAuthEndpoint, microsoftGraphMeURL = p.srv.URL+"/profile", p.endpoint(), p.srv.URL+"/profile"
	t.Cleanup(func() { googleUserInfoURL, microsoftOAuthEndpoint, microsoftGraphMeURL = prevG, prevMS, prevGraph })
	t.Setenv("MICROSOFT_CLIENT_ID", "ms-client")
	t.Setenv("MICROSOFT_CLIENT_SECRET", "ms-secret")
	t.Setenv("MICROSOFT_REDIRECT_URL", "http://localhost/ms/cb")

	n := 0
	return []pac81Path{
		{"booking create", http.StatusCreated, func(t *testing.T, db *sql.DB, ctx context.Context) *httptest.ResponseRecorder {
			n++
			req := httptest.NewRequest(http.MethodPost, "/api/book/"+link.Slug, strings.NewReader(pac81BookingBody("Cap", pac81Start(100+n)))).WithContext(ctx)
			req.RemoteAddr = pac81Addr()
			rec := httptest.NewRecorder()
			bookingRouter(db).ServeHTTP(rec, req)
			return rec
		}},
		{"dated slot listing", http.StatusOK, func(t *testing.T, db *sql.DB, ctx context.Context) *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodGet, "/api/book/"+link.Slug+"/slots?date="+day, nil).WithContext(ctx)
			req.RemoteAddr = pac81Addr()
			rec := httptest.NewRecorder()
			bookingRouter(db).ServeHTTP(rec, req)
			return rec
		}},
		{"natural-language parse", http.StatusOK, func(t *testing.T, db *sql.DB, ctx context.Context) *httptest.ResponseRecorder {
			req := withUser(httptest.NewRequest(http.MethodPost, "/api/nlp/parse", strings.NewReader(`{"text":"meet bob tomorrow"}`)).WithContext(ctx), llmUser)
			rec := httptest.NewRecorder()
			(&nlpHandlers{svc: &nlp.NLPService{DB: db}, smart: &mockScheduler{}, db: db}).parse(rec, req)
			return rec
		}},
		{"personal-calendar sync", http.StatusOK, func(t *testing.T, db *sql.DB, ctx context.Context) *httptest.ResponseRecorder {
			r := chi.NewRouter()
			r.Post("/api/personal-calendars/{id}/sync", newPersonalHandlers(db, &oauth2.Config{}).sync)
			req := withUser(httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/personal-calendars/%d/sync", calID), nil).WithContext(ctx), syncUser)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			return rec
		}},
		{"Google sign-in callback", http.StatusFound, func(t *testing.T, db *sql.DB, ctx context.Context) *httptest.ResponseRecorder {
			ah := &authHandlers{db: db, jwtSecret: "secret", frontendURL: pac35Frontend,
				oauthConfig: &oauth2.Config{ClientID: "g", ClientSecret: "s", RedirectURL: "http://localhost/cb", Endpoint: p.endpoint()}}
			req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?state=s1&code=c1", nil).WithContext(ctx)
			req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "s1"})
			rec := httptest.NewRecorder()
			ah.callback(rec, req)
			return rec
		}},
		{"Microsoft sign-in callback", http.StatusFound, func(t *testing.T, db *sql.DB, ctx context.Context) *httptest.ResponseRecorder {
			ah := &authHandlers{db: db, jwtSecret: "secret", frontendURL: pac35Frontend}
			req := httptest.NewRequest(http.MethodGet, "/api/auth/microsoft/callback?state=s1&code=c1", nil).WithContext(ctx)
			req.AddCookie(&http.Cookie{Name: "ms_oauth_state", Value: "s1"})
			rec := httptest.NewRecorder()
			ah.microsoftCallback(rec, req)
			return rec
		}},
	}
}

// AC-A10: with the pool capped at 1, each named path completes within 10 s
// (a path that holds a connection while waiting for a second deadlocks).
func TestPAC81_PoolCapOnePaths(t *testing.T) {
	pac81Group(t)
	nlptest.UserDestinations(t, "", "")
	db := capOnePool(t)
	for _, p := range pac81NamedPaths(t) {
		t.Run(p.name, func(t *testing.T) {
			// The production budget: the per-item margin (10 s) must not cut.
			ctx, cancel := context.WithTimeout(context.Background(), budget.Default)
			defer cancel()
			start := time.Now()
			rec := p.run(t, db, ctx)
			if took := time.Since(start); took > 10*time.Second {
				t.Fatalf("took %s", took)
			}
			if rec.Code != p.want {
				t.Fatalf("status %d %s, want %d", rec.Code, rec.Body.String(), p.want)
			}
			if p.want == http.StatusFound && strings.Contains(rec.Header().Get("Location"), "error=") {
				t.Fatalf("sign-in failed: %s", rec.Header().Get("Location"))
			}
		})
	}
	// Let booking-completion units finish on the capped pool too.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if !lifecycle.Default().Wait(ctx) {
		t.Fatal("background units did not finish on a pool of 1")
	}
}

// AC-A9: with the only connection held elsewhere, the handler goroutine of
// each named path itself returns within budget + 1 s.
func TestPAC81_HandlerGoroutineReturnsAtBudgetWithPoolHeld(t *testing.T) {
	pac81Group(t)
	nlptest.UserDestinations(t, "", "")
	db := capOnePool(t)
	paths := pac81NamedPaths(t)
	held, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	const b = 300 * time.Millisecond
	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), b)
			defer cancel()
			done := make(chan struct{})
			start := time.Now()
			go func() {
				defer close(done)
				p.run(t, db, ctx)
			}()
			select {
			case <-done:
			case <-time.After(b + time.Second):
				t.Fatalf("handler goroutine still waiting %s after the budget", time.Since(start)-b)
			}
		})
	}
}

// AC-A11: a pool whose only connection is held: readiness answers 503 within
// the readiness timeout + 1 s.
func TestPAC81_ReadinessPoolHeldIs503(t *testing.T) {
	db := capOnePool(t)
	held, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	start := time.Now()
	rec := serveReadiness(&readiness{ping: db.PingContext, shuttingDown: func() bool { return false }})
	if rec.Code != http.StatusServiceUnavailable || time.Since(start) > 3*time.Second {
		t.Fatalf("%d after %s", rec.Code, time.Since(start))
	}
	held.Close()
	if rec := serveReadiness(newReadiness(db)); rec.Code != http.StatusOK {
		t.Fatalf("after release: %d", rec.Code)
	}
}

// --- AC-A17: personal sync budget cut ------------------------------------------

type cutBlocker struct{ PersonalCalendarBlocker }

func (cutBlocker) Sync(context.Context, int64) error { return budget.ErrExpired }

func TestPAC81_PersonalSyncBudgetCutIs503NotStamped(t *testing.T) {
	user := createTestUser(t, "pac81-cut-"+uuid.NewString()[:6]+"@example.com")
	id, err := storage.InsertPersonalCalendar(sharedTestDB, &storage.PersonalCalendar{UserID: user, Provider: "webcal", Name: "Home", URL: "http://127.0.0.1:1/", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	h := newPersonalHandlersWithBlocker(sharedTestDB, cutBlocker{})
	r := chi.NewRouter()
	r.Post("/api/personal-calendars/{id}/sync", h.sync)
	req := withUser(httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/personal-calendars/%d/sync", id), nil), user)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || rec.Body.String() != "{\"error\":\"request timed out\"}\n" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
	cal, err := storage.GetPersonalCalendar(sharedTestDB, id)
	if err != nil || cal == nil {
		t.Fatal(err)
	}
	if cal.LastSyncedAt != nil {
		t.Fatal("last_synced_at stamped after a budget cut")
	}
}
