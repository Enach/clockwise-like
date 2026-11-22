package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/storage"
)

// SSRF on the stored feed URL (security audit of 2026-11-22, MEDIUM). An
// authenticated user could store any URL as a personal calendar or as
// settings.webcalUrl and have the backend fetch it from inside the deployment
// network, once through the preview endpoint and then every 30 minutes
// through the personal-blocker cron. The write path is the first of the two
// layers that close it: a hostile URL never reaches storage. The second is
// the egress guard in backend/calendar (feed_guard_test.go), which is what
// catches a name that resolves to a private address only at fetch time.

// hostileFeedURLs are refused by both write paths. Each is a URL the backend
// would otherwise have fetched from inside its own network.
var hostileFeedURLs = []string{
	"http://10.0.0.5:9200/_cluster/health",       // the finding's own example
	"http://169.254.169.254/latest/meta-data/",   // IMDSv1
	"http://[fd00::1]/feed.ics",                  // IPv6 unique-local
	"http://127.0.0.1:8080/feed.ics",             // loopback
	"http://localhost:8080/feed.ics",             // loopback by name (RFC 6761)
	"http://192.168.1.1/feed.ics",                // RFC 1918
	"http://172.16.0.1/feed.ics",                 // RFC 1918
	"https://[::1]/feed.ics",                     // IPv6 loopback
	"webcal://100.64.0.1/feed.ics",               // RFC 6598 carrier NAT
	"http://0.0.0.0/feed.ics",                    // unspecified
	"file:///etc/passwd",                         // not an HTTP scheme
	"gopher://10.0.0.5:9200/_",                   // not an HTTP scheme
	"http://feeds.example.com@10.0.0.5/feed.ics", // userinfo hiding the host
	"http://10.0.0.5\x00.example.com/feed.ics",   // unparseable
	"http://[fe80::1%25eth0]/feed.ics",           // zone-scoped link-local
	"http://[::ffff:10.0.0.5]/feed.ics",          // IPv4-mapped private
	"http://2130706433/feed.ics",                 // 127.0.0.1 as a decimal integer
}

// acceptableFeedURLs must keep working: real feeds live on public hosts, and
// a name is judged at connection time, not by its shape.
var acceptableFeedURLs = []string{
	"webcal://example.com/family.ics",
	"https://calendar.google.com/calendar/ical/x/basic.ics",
	"http://feeds.example.com/team.ics",
	"webcals://example.com/family.ics",
}

func TestPersonalCalendarCreateRefusesHostileURL(t *testing.T) {
	r := setupPersonalRoutes(t, &fakePersonalBlocker{})
	userID := createTestUser(t, "ssrf-create@example.com")
	for _, raw := range hostileFeedURLs {
		body, err := json.Marshal(map[string]any{"provider": "webcal", "url": raw, "enabled": true})
		if err != nil {
			t.Fatal(err)
		}
		req := withUser(httptest.NewRequest(http.MethodPost, "/api/personal-calendars", bytes.NewReader(body)), userID)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("POST url=%q status = %d, want 400; body: %s", raw, w.Code, w.Body.String())
			continue
		}
		var doc struct{ Error string }
		if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
			t.Errorf("POST url=%q body %q: %v", raw, w.Body.String(), err)
			continue
		}
		if !strings.HasPrefix(doc.Error, "url ") {
			t.Errorf("POST url=%q error = %q, want a message naming the url property first", raw, doc.Error)
		}
		if strings.Contains(doc.Error, raw) {
			t.Errorf("POST url=%q error echoes the URL back: %q", raw, doc.Error)
		}
	}
	// Nothing was stored.
	cals, err := storage.ListPersonalCalendarsByUser(openTestDB(t), userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cals) != 0 {
		t.Fatalf("%d personal calendar(s) stored by refused writes: %+v", len(cals), cals)
	}
}

func TestPersonalCalendarCreateAcceptsPublicURL(t *testing.T) {
	r := setupPersonalRoutes(t, &fakePersonalBlocker{})
	userID := createTestUser(t, "ssrf-create-ok@example.com")
	for _, raw := range acceptableFeedURLs {
		body, err := json.Marshal(map[string]any{"provider": "webcal", "url": raw, "enabled": true})
		if err != nil {
			t.Fatal(err)
		}
		req := withUser(httptest.NewRequest(http.MethodPost, "/api/personal-calendars", bytes.NewReader(body)), userID)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Errorf("POST url=%q status = %d, want 201; body: %s", raw, w.Code, w.Body.String())
		}
	}
}

// A personal calendar with no URL (google, outlook) is unaffected: there is
// no feed to fetch and nothing to judge.
func TestPersonalCalendarCreateAllowsEmptyURL(t *testing.T) {
	r := setupPersonalRoutes(t, &fakePersonalBlocker{})
	userID := createTestUser(t, "ssrf-create-empty@example.com")
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/personal-calendars",
		strings.NewReader(`{"provider":"google","name":"Work"}`)), userID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
}

// PATCH is the same sink: it re-points an existing row's url.
func TestPersonalCalendarPatchRefusesHostileURL(t *testing.T) {
	r := setupPersonalRoutes(t, &fakePersonalBlocker{})
	userID := createTestUser(t, "ssrf-patch@example.com")
	const kept = "https://feeds.example.com/team.ics"
	id := createPersonalCalendar(t, userID, "webcal", "Team", kept, true)
	for _, raw := range hostileFeedURLs {
		body, err := json.Marshal(map[string]any{"url": raw})
		if err != nil {
			t.Fatal(err)
		}
		req := withUser(httptest.NewRequest(http.MethodPatch, "/api/personal-calendars/"+strconv.FormatInt(id, 10), bytes.NewReader(body)), userID)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("PATCH url=%q status = %d, want 400; body: %s", raw, w.Code, w.Body.String())
		}
	}
	cal, err := storage.GetPersonalCalendar(openTestDB(t), id)
	if err != nil {
		t.Fatal(err)
	}
	if cal.URL != kept {
		t.Fatalf("stored url = %q, want it unchanged at %q", cal.URL, kept)
	}
}

// The same sink through PATCH /api/settings {"webcalUrl": …}:
// validateChangedSettings had no webcalUrl case at all, though it validated
// azureEndpoint, awsRegion and llmBaseUrl.
func TestPatchSettingsRefusesHostileWebcalURL(t *testing.T) {
	h := &settingsHandlers{db: openTestDB(t)}
	user := createTestUser(t, "ssrf-settings@example.com")
	for _, raw := range hostileFeedURLs {
		body, err := json.Marshal(map[string]string{"webcalUrl": raw})
		if err != nil {
			t.Fatal(err)
		}
		w := settingsRequest(t, h, http.MethodPatch, user, string(body))
		if w.Code != http.StatusBadRequest {
			t.Errorf("PATCH webcalUrl=%q status = %d, want 400; body: %s", raw, w.Code, w.Body.String())
			continue
		}
		var doc struct{ Error string }
		if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
			t.Errorf("PATCH webcalUrl=%q body %q: %v", raw, w.Body.String(), err)
			continue
		}
		if !strings.HasPrefix(doc.Error, "webcalUrl ") {
			t.Errorf("PATCH webcalUrl=%q error = %q, want a message naming webcalUrl first", raw, doc.Error)
		}
	}
	// Nothing reached storage: the row still has no feed URL, so
	// /api/auth/status still reports webcal as not connected.
	s, err := storage.GetOrCreateSettingsByUser(openTestDB(t), user)
	if err != nil {
		t.Fatal(err)
	}
	if s.WebcalURL != "" {
		t.Fatalf("stored webcalUrl = %q, want it never written", s.WebcalURL)
	}
}

func TestPatchSettingsAcceptsPublicWebcalURL(t *testing.T) {
	h := &settingsHandlers{db: openTestDB(t)}
	user := createTestUser(t, "ssrf-settings-ok@example.com")
	for _, raw := range acceptableFeedURLs {
		body, err := json.Marshal(map[string]string{"webcalUrl": raw})
		if err != nil {
			t.Fatal(err)
		}
		if w := settingsRequest(t, h, http.MethodPatch, user, string(body)); w.Code != http.StatusOK {
			t.Errorf("PATCH webcalUrl=%q status = %d, want 200; body: %s", raw, w.Code, w.Body.String())
		}
	}
	// "" is a value here as everywhere else: it clears the feed.
	if w := settingsRequest(t, h, http.MethodPatch, user, `{"webcalUrl":""}`); w.Code != http.StatusOK {
		t.Errorf("clearing webcalUrl: status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

// PAC-24's changed-only rule still holds: a stored URL that this rule now
// rejects must not stop the user saving something else. Existing users may
// have one (see the PR body).
func TestPatchSettingsUnrelatedWriteSurvivesStoredHostileURL(t *testing.T) {
	db := openTestDB(t)
	h := &settingsHandlers{db: db}
	user := createTestUser(t, "ssrf-settings-legacy@example.com")
	s, err := storage.GetOrCreateSettingsByUser(db, user)
	if err != nil {
		t.Fatal(err)
	}
	s.WebcalURL = "http://10.0.0.5:9200/_cluster/health"
	s.MarkSecretReplaced(storage.SettingsColWebcalURL)
	if err := storage.SaveSettingsForUser(db, user, s); err != nil {
		t.Fatal(err)
	}
	if w := settingsRequest(t, h, http.MethodPatch, user, `{"workStart":"06:30"}`); w.Code != http.StatusOK {
		t.Fatalf("unrelated write with a stored hostile feed URL: %d %s", w.Code, w.Body.String())
	}
	// Writing the same value back is not a change either, which is what
	// makes "a document read from GET can always be submitted back
	// unchanged" true for a row holding a value this rule now rejects.
	body, err := json.Marshal(map[string]string{"webcalUrl": s.WebcalURL})
	if err != nil {
		t.Fatal(err)
	}
	if w := settingsRequest(t, h, http.MethodPatch, user, string(body)); w.Code != http.StatusOK {
		t.Errorf("re-writing the stored value: %d %s", w.Code, w.Body.String())
	}
}
