package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/conference"
	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/internal/oauthfake"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// pac67World is the PAC-67 test bench: every provider's token, revocation and
// API endpoint is the oauthfake double, Google Calendar is a recording stand-in,
// and the routes under test are mounted as in production.
type pac67World struct {
	t      *testing.T
	db     *sql.DB
	d      *oauthfake.Server
	cal    *pac67Calendar
	google *oauth2.Config
	r      *chi.Mux
	ch     *conferencingHandlers
}

var (
	pac67Past   = time.Now().Add(-2 * time.Hour)
	pac67Future = time.Now().Add(2 * time.Hour)
)

func newPAC67World(t *testing.T) *pac67World {
	t.Helper()
	d := oauthfake.New()
	t.Cleanup(d.Close)
	for _, p := range []string{"GOOGLE", "MICROSOFT", "ZOOM"} {
		t.Setenv(p+"_CLIENT_ID", strings.ToLower(p)+"-client")
		t.Setenv(p+"_CLIENT_SECRET", strings.ToLower(p)+"-secret")
		t.Setenv(p+"_REDIRECT_URL", "http://localhost/"+strings.ToLower(p)+"/cb")
	}
	prevMS := microsoftOAuthEndpoint
	prevZT, prevZR, prevZA, prevG := conference.ZoomTokenURL, conference.ZoomRevokeURL, conference.ZoomAPIBase, conference.GraphBase
	microsoftOAuthEndpoint = oauth2.Endpoint{AuthURL: d.URL + "/auth", TokenURL: d.TokenURL()}
	conference.ZoomTokenURL, conference.ZoomRevokeURL = d.TokenURL(), d.RevokeURL()
	conference.ZoomAPIBase, conference.GraphBase = d.APIBase(), d.APIBase()
	t.Cleanup(func() {
		microsoftOAuthEndpoint = prevMS
		conference.ZoomTokenURL, conference.ZoomRevokeURL, conference.ZoomAPIBase, conference.GraphBase = prevZT, prevZR, prevZA, prevG
	})
	d.APIAnswerFor("/me/onlineMeetings", http.StatusCreated, `{"id":"m-1","joinWebUrl":"https://teams.example/m-1"}`)
	d.APIAnswerFor("/users/me/meetings", http.StatusCreated, `{"id":81,"join_url":"https://zoom.example/81","password":"pw"}`)

	w := &pac67World{t: t, db: openTestDB(t), d: d, cal: newPAC67Calendar(t)}
	w.google = &oauth2.Config{ClientID: "g", ClientSecret: "gs", Endpoint: oauth2.Endpoint{AuthURL: d.URL + "/auth", TokenURL: d.TokenURL()}}
	w.ch = newConferencingHandlers(w.db, w.google)
	ah := &authHandlers{db: w.db, oauthConfig: w.google}
	calh := &calendarHandlers{oauthConfig: w.google, db: w.db}
	fb := newFreeBusyHandlers(w.db, w.google)
	r := chi.NewRouter()
	r.Post("/api/conference/create", w.ch.createConference)
	r.Get("/api/conference/providers", w.ch.providers)
	r.Post("/api/conference/zoom/disconnect", w.ch.disconnectZoom)
	r.Post("/api/events/{id}/conference", w.ch.addEventConference)
	r.Get("/api/auth/status", ah.status)
	r.Post("/api/auth/microsoft/disconnect", ah.disconnectMicrosoft)
	r.Delete("/api/auth/disconnect", ah.disconnect)
	r.Get("/api/calendar/events", calh.listEvents)
	r.Post("/api/freebusy", fb.query)
	w.r = r
	return w
}

func (w *pac67World) user(email string) uuid.UUID {
	w.t.Helper()
	return createTestUser(w.t, email)
}

func (w *pac67World) seedGoogle(user uuid.UUID, access, refresh string, expiry time.Time) {
	w.t.Helper()
	if err := auth.UpsertUserToken(w.db, user, &oauth2.Token{AccessToken: access, RefreshToken: refresh, Expiry: expiry}); err != nil {
		w.t.Fatal(err)
	}
}

// seedOutlook stores a Microsoft credential without a sign-in (the calendar
// provider is set separately by providers).
func (w *pac67World) seedOutlook(user uuid.UUID, access, refresh string, expiry time.Time) {
	w.t.Helper()
	data, _ := json.Marshal(map[string]any{"access_token": access, "refresh_token": refresh, "expiry": expiry.UTC()})
	w.exec(`INSERT INTO settings (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, user)
	w.exec(`UPDATE settings SET microsoft_tokens = $2 WHERE user_id = $1`, user, string(data))
}

func (w *pac67World) seedZoom(user uuid.UUID, access, refresh string, expiry *time.Time) {
	w.t.Helper()
	if err := storage.SaveZoomCredential(w.db, user, storage.ZoomCredential{AccessToken: access, RefreshToken: refresh, Expiry: expiry}); err != nil {
		w.t.Fatal(err)
	}
}

func (w *pac67World) providers(user uuid.UUID, calendarProvider, conferencing string) {
	w.t.Helper()
	w.exec(`INSERT INTO settings (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, user)
	w.exec(`UPDATE settings SET calendar_provider = $2, conferencing_provider = $3 WHERE user_id = $1`, user, calendarProvider, conferencing)
}

func (w *pac67World) exec(q string, args ...any) {
	w.t.Helper()
	if _, err := w.db.Exec(q, args...); err != nil {
		w.t.Fatalf("%s: %v", q, err)
	}
}

func (w *pac67World) do(user uuid.UUID, method, target, body string) *httptest.ResponseRecorder {
	w.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(method, target, rd)
	if user != uuid.Nil {
		req = withUser(req, user)
	}
	rec := httptest.NewRecorder()
	w.r.ServeHTTP(rec, req)
	return rec
}

// row returns the caller's conferencing row for provider, nil when absent.
func (w *pac67World) row(user uuid.UUID, provider string) map[string]any {
	w.t.Helper()
	rec := w.do(user, http.MethodGet, "/api/conference/providers", "")
	var rows []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		w.t.Fatalf("providers: %d %s", rec.Code, rec.Body.String())
	}
	for _, r := range rows {
		if r["provider"] == provider {
			return r
		}
	}
	return nil
}

func (w *pac67World) status(user uuid.UUID) map[string]any {
	w.t.Helper()
	var out map[string]any
	_ = json.Unmarshal(w.do(user, http.MethodGet, "/api/auth/status", "").Body.Bytes(), &out)
	return out
}

func (w *pac67World) entries(user uuid.UUID) []storage.AuditEntry {
	w.t.Helper()
	e, err := storage.ListAuditLog(w.db, user, 500)
	if err != nil {
		w.t.Fatal(err)
	}
	// oldest first reads better in assertions
	for i, j := 0, len(e)-1; i < j; i, j = i+1, j-1 {
		e[i], e[j] = e[j], e[i]
	}
	return e
}

func (w *pac67World) lost(user uuid.UUID, provider string) bool {
	l, err := storage.IsCredentialLost(w.db, user, provider)
	if err != nil {
		w.t.Fatal(err)
	}
	return l
}

// assertRow checks a conferencing row's connected value and that a not
// connected teams row carries no `enabled` key (spec section 4 preamble).
func (w *pac67World) assertRow(user uuid.UUID, provider string, connected bool) {
	w.t.Helper()
	r := w.row(user, provider)
	if r == nil {
		w.t.Fatalf("no %s row", provider)
	}
	if r["connected"] != connected {
		w.t.Errorf("%s row connected = %v, want %v (%v)", provider, r["connected"], connected, r)
	}
	if provider == "teams" {
		_, has := r["enabled"]
		if has != connected || (has && r["enabled"] != true) {
			w.t.Errorf("teams row enabled = %v (present %v), want it to follow connected=%v", r["enabled"], has, connected)
		}
	}
}

func (w *pac67World) assertEntries(user uuid.UUID, want ...string) {
	w.t.Helper()
	got := w.entries(user)
	var g []string
	for _, e := range got {
		g = append(g, e.Action+" "+e.Details)
	}
	if strings.Join(g, "\n") != strings.Join(want, "\n") {
		w.t.Errorf("activity log =\n  %s\nwant\n  %s", strings.Join(g, "\n  "), strings.Join(want, "\n  "))
	}
}

func assertBody(t *testing.T, rec *httptest.ResponseRecorder, code int, body string) {
	t.Helper()
	if rec.Code != code || strings.TrimSpace(rec.Body.String()) != body {
		t.Errorf("response = %d %s, want %d %s", rec.Code, strings.TrimSpace(rec.Body.String()), code, body)
	}
}

const (
	teamsLost = `{"error":"teams: connection lost — reconnect at /api/auth/microsoft"}`
	zoomLost  = `{"error":"zoom: connection lost — reconnect at /api/auth/zoom"}`
)

const pac67Meeting = `{"title":"Sync","start":"2030-01-07T10:00:00Z","end":"2030-01-07T10:30:00Z"}`

// pac67Calendar stands in for Google Calendar, recording the bearer of every
// request. Listing answers one busy event to the bearers in busyFor.
type pac67Calendar struct {
	mu      sync.Mutex
	bearers []string
	busyFor map[string]bool
	puts    int
	inserts int
	failAll bool
}

func newPAC67Calendar(t *testing.T) *pac67Calendar {
	c := &pac67Calendar{busyFor: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.bearers = append(c.bearers, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		busy := c.busyFor[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		fail := c.failAll
		if r.Method == http.MethodPut {
			c.puts++
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/events") {
			c.inserts++
		}
		c.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":503,"message":"backend error"}}`))
			return
		}
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/freeBusy"):
			var req struct {
				Items []struct {
					ID string `json:"id"`
				} `json:"items"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			cals := map[string]any{}
			for _, it := range req.Items {
				cals[it.ID] = map[string]any{"busy": []any{}}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"kind": "calendar#freeBusy", "calendars": cals})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/events"):
			var ev map[string]any
			_ = json.NewDecoder(r.Body).Decode(&ev)
			ev["id"], ev["kind"] = "created-1", "calendar#event"
			_ = json.NewEncoder(w).Encode(ev)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/events"):
			items := []any{}
			if busy {
				day := r.URL.Query().Get("timeMin")
				start, _ := time.Parse(time.RFC3339, day)
				s := time.Date(start.Year(), start.Month(), start.Day(), 10, 0, 0, 0, time.UTC)
				items = append(items, map[string]any{"id": "busy-1", "summary": "Busy", "status": "confirmed",
					"start": map[string]string{"dateTime": s.Format(time.RFC3339)},
					"end":   map[string]string{"dateTime": s.Add(time.Hour).Format(time.RFC3339)}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"kind": "calendar#events", "items": items})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/events/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"kind": "calendar#event", "id": "e1", "summary": "Sync",
				"start": map[string]string{"dateTime": "2030-01-07T10:00:00Z"},
				"end":   map[string]string{"dateTime": "2030-01-07T10:30:00Z"}})
		case r.Method == http.MethodPut || r.Method == http.MethodPatch:
			body, _ := io.ReadAll(r.Body)
			_, _ = w.Write(body)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	calendar.Configure(config.CalendarEndpoint{BaseURL: srv.URL + config.CalendarAPIRoot, Guard: true})
	t.Cleanup(func() { calendar.Configure(config.ProductionCalendarEndpoint()) })
	return c
}

func (c *pac67Calendar) saw(bearer string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, b := range c.bearers {
		if b == bearer {
			return true
		}
	}
	return false
}

func (c *pac67Calendar) setFailAll(v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failAll = v
}

func (c *pac67Calendar) setBusy(bearer string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.busyFor[bearer] = true
}

func (c *pac67Calendar) insertCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inserts
}

