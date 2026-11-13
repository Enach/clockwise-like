package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Enach/paceday/backend/nlp"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// PAC-84 contract sections 1 and 9.3 (AC-16, AC-16b, AC-16c): a stored
// secret that cannot be decrypted answers each operation's DOCUMENTED 500
// on act paths (never a 4xx, never "not configured"), keeps the status and
// presence mappings, never fails a settings read or save, a list or a
// disconnect, makes no provider call, records no loss, and is logged with
// contract 9.2's line without any value.

// undecryptable is a well-formed envelope under a data key no keyring holds.
var undecryptable = "pdenc:1:2147483000:" + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 48))

func pac84Logs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	var mu sync.Mutex
	prev := log.Writer()
	log.SetOutput(writerFn(func(p []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return buf.Write(p) }))
	t.Cleanup(func() { log.SetOutput(prev) })
	return &buf
}

type writerFn func([]byte) (int, error)

func (f writerFn) Write(p []byte) (int, error) { return f(p) }

const internalError = `{"error":"internal error"}`

func TestPAC84_ConferencingUndecryptableIsTheDocumented500(t *testing.T) {
	for _, provider := range []string{"zoom", "teams"} {
		t.Run(provider, func(t *testing.T) {
			logs := pac84Logs(t)
			w := newPAC67World(t)
			u := w.user("pac84-conf-" + provider + "@example.com")
			w.seedGoogle(u, "g-live", "g-r", pac67Future)
			col := "zoom_tokens"
			if provider == "teams" {
				col = "microsoft_tokens"
				w.seedOutlook(u, "ms", "ms-r", pac67Future)
				w.providers(u, "google", "teams")
			} else {
				w.seedZoom(u, "z", "z-r", &pac67Future)
			}
			w.exec(`UPDATE settings SET `+col+` = $2 WHERE user_id = $1`, u, undecryptable)
			before := len(w.d.Calls(""))

			rec := w.do(u, http.MethodPost, "/api/conference/create", pac67Meeting)
			assertBody(t, rec, http.StatusInternalServerError, internalError)
			if n := len(w.d.Calls("")); n != before {
				t.Fatalf("%d provider calls made with an undecryptable credential", n-before)
			}
			cred := storage.CredentialZoom
			if provider == "teams" {
				cred = storage.CredentialOutlook
			}
			if w.lost(u, cred) {
				t.Fatal("an undecryptable credential was marked lost")
			}
			for _, e := range w.entries(u) {
				if e.Action == "connection_lost" {
					t.Fatal("connection_lost written")
				}
			}
			if !strings.Contains(logs.String(), "encryption: cannot decrypt settings."+col+" row=user_id="+u.String()+" dek=2147483000 kek=- user="+u.String()+" reason=unknown_data_key") {
				t.Fatalf("log line missing:\n%s", logs.String())
			}
			// AC-16b: the status surfaces say not connected / not usable.
			w.assertRow(u, provider, false)
			if provider == "teams" {
				if out := w.status(u); out["outlook"] != nil && strings.Contains(toJSON(out["outlook"]), `"connected":true`) {
					t.Fatalf("outlook status connected: %v", out["outlook"])
				}
			}
			if strings.Contains(logs.String(), undecryptable) {
				t.Fatal("ciphertext logged")
			}
		})
	}
}

func toJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// AC-16c (c): disconnecting an undecryptable Zoom credential is 200,
// removes it, makes no provider call and reports "unconfirmed".
func TestPAC84_ZoomDisconnectUndecryptable(t *testing.T) {
	w := newPAC67World(t)
	u := w.user("pac84-zoomdisc@example.com")
	w.seedZoom(u, "z", "z-r", &pac67Future)
	w.exec(`UPDATE settings SET zoom_tokens = $2 WHERE user_id = $1`, u, undecryptable)
	before := len(w.d.Calls(""))
	rec := w.do(u, http.MethodPost, "/api/conference/zoom/disconnect", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"revocation":"unconfirmed"`) {
		t.Fatalf("disconnect = %d %s", rec.Code, rec.Body.String())
	}
	if len(w.d.Calls("")) != before {
		t.Fatal("provider called")
	}
	var z string
	_ = w.db.QueryRow(`SELECT zoom_tokens FROM settings WHERE user_id = $1`, u).Scan(&z)
	if z != "" {
		t.Fatal("not removed")
	}
}

// AC-16 (calendar act path, AC-16b status): an undecryptable Google token
// is the 500 on a calendar read and "not connected" on the status.
func TestPAC84_GoogleUndecryptable(t *testing.T) {
	w := newPAC67World(t)
	u := w.user("pac84-google@example.com")
	w.seedGoogle(u, "g", "g-r", pac67Future)
	w.exec(`UPDATE oauth_tokens SET refresh_token = $2 WHERE user_id = $1`, u, undecryptable)
	rec := w.do(u, http.MethodGet, "/api/calendar/events?start=2030-01-01T00:00:00Z&end=2030-01-02T00:00:00Z", "")
	assertBody(t, rec, http.StatusInternalServerError, internalError)
	if w.lost(u, storage.CredentialGoogle) {
		t.Fatal("marked lost")
	}
	if out := w.status(u); strings.Contains(toJSON(out), `"google":{"connected":true`) {
		t.Fatalf("google status connected: %v", out)
	}
}

// Contract 1 item 4: the recap test send answers its documented 500
// {"error":"db error"}, never 400 "Slack not connected".
func TestPAC84_RecapTestSendUndecryptable(t *testing.T) {
	db := openTestDB(t)
	u := createTestUser(t, "pac84-recap@example.com")
	if err := storage.UpsertWorkspaceConnection(db, &storage.WorkspaceConnection{UserID: u, Provider: "slack", AccessToken: "xoxp-a", BotToken: "xoxb-b", WorkspaceID: "W", Scopes: []string{"chat:write"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE workspace_connections SET bot_token = $2 WHERE user_id = $1`, u, undecryptable); err != nil {
		t.Fatal(err)
	}
	h := &dailyRecapHandlers{db: db}
	rec := httptest.NewRecorder()
	h.test(rec, withUser(httptest.NewRequest(http.MethodPost, "/api/settings/daily-recap/test", nil), u))
	assertBody(t, rec, http.StatusInternalServerError, `{"error":"db error"}`)

	// AC-16b: the Slack status says not connected.
	ih := &integrationsHandlers{db: db}
	rec = httptest.NewRecorder()
	ih.slackStatus(rec, withUser(httptest.NewRequest(http.MethodGet, "/api/integrations/slack/status", nil), u))
	if !strings.Contains(rec.Body.String(), `"connected":false`) {
		t.Fatalf("slack status = %s", rec.Body.String())
	}
}

// Contract 1 item 1: SSO start and callback answer the documented 500,
// never 404 "SSO not configured"; the admin list still says the secret is
// set (AC-16b).
func TestPAC84_SSOUndecryptable(t *testing.T) {
	db := openTestDB(t)
	domain := "pac84-" + uuid.NewString()[:8] + ".example"
	p, err := storage.UpsertSSOProvider(db, &storage.SSOProvider{Domain: domain, ProviderName: "Okta", ProviderType: "oidc", Enabled: true,
		OIDCIssuer: "https://idp.invalid", OIDCClientID: "cid", OIDCClientSecret: "client-secret-abcdefghijkl"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE sso_providers SET oidc_client_secret = $2 WHERE id = $1`, p.ID, undecryptable); err != nil {
		t.Fatal(err)
	}
	h := &ssoHandlers{ah: &authHandlers{db: db, jwtSecret: "test-secret", frontendURL: "http://frontend.test"}}
	r := chi.NewRouter()
	r.Get("/api/auth/sso/{domain}", h.startSSO)
	r.Get("/api/auth/callback/oidc/{domain}", h.oidcCallback)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/sso/"+domain, nil))
	assertBody(t, rec, http.StatusInternalServerError, internalError)
	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback/oidc/"+domain+"?state=s1&code=c", nil)
	req.AddCookie(&http.Cookie{Name: "sso_state", Value: "s1|" + domain})
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assertBody(t, rec, http.StatusInternalServerError, internalError)
	got, _ := storage.GetSSOProviderByDomainAnyState(db, domain)
	if dto := ssoProviderView(got); !dto.HasOidcClientSecret {
		t.Fatal("hasOidcClientSecret false for an undecryptable secret")
	}
}

// Contract 1 item 2: testLLMConnection is the 500, never the 409; parse is
// the 500, never 200 not_configured or intent unknown. Settings GET and
// PATCH succeed and keep the presence flag (AC-16b, AC-16c a, AC-19b).
func TestPAC84_AIKeyUndecryptable(t *testing.T) {
	db := openTestDB(t)
	u := createTestUser(t, "pac84-ai@example.com")
	s, err := storage.GetOrCreateSettingsByUser(db, u)
	if err != nil {
		t.Fatal(err)
	}
	s.LLMProvider, s.LLMAPIKeys.OpenAI = "openai", "sk-pac84-abcdefghijklmnop"
	s.MarkSecretReplaced("llm_api_key_openai")
	if err := storage.SaveSettingsForUser(db, u, s); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE settings SET llm_api_key_openai = $2 WHERE user_id = $1`, u, undecryptable); err != nil {
		t.Fatal(err)
	}
	lh := &llmHandlers{db: db}
	rec := httptest.NewRecorder()
	lh.testLLM(rec, withUser(httptest.NewRequest(http.MethodPost, "/api/llm/test", strings.NewReader(`{}`)), u))
	assertBody(t, rec, http.StatusInternalServerError, internalError)

	nh := &nlpHandlers{svc: &nlp.NLPService{DB: db}, db: db}
	rec = httptest.NewRecorder()
	nh.parse(rec, withUser(httptest.NewRequest(http.MethodPost, "/api/nlp/parse", strings.NewReader(`{"text":"meet bob tomorrow"}`)), u))
	assertBody(t, rec, http.StatusInternalServerError, internalError)

	sh := &settingsHandlers{db: db}
	rec = httptest.NewRecorder()
	sh.getSettings(rec, withUser(httptest.NewRequest(http.MethodGet, "/api/settings", nil), u))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"llmApiKeySet":true`) || strings.Contains(rec.Body.String(), "pdenc:") {
		t.Fatalf("settings GET = %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	sh.patchSettings(rec, withUser(httptest.NewRequest(http.MethodPatch, "/api/settings", strings.NewReader(`{"focusLabel":"Deep"}`)), u))
	if rec.Code != http.StatusOK {
		t.Fatalf("settings PATCH = %d %s", rec.Code, rec.Body.String())
	}
	var stored string
	_ = db.QueryRow(`SELECT llm_api_key_openai FROM settings WHERE user_id = $1`, u).Scan(&stored)
	if stored != undecryptable {
		t.Fatal("PATCH rewrote the undecryptable key")
	}
	// AC-16c (b): entering a key replaces it; then it reads normally.
	rec = httptest.NewRecorder()
	sh.patchSettings(rec, withUser(httptest.NewRequest(http.MethodPatch, "/api/settings", strings.NewReader(`{"llmApiKey":"sk-new-abcdefghijklmnop"}`)), u))
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH key = %d %s", rec.Code, rec.Body.String())
	}
	s, _ = storage.GetOrCreateSettingsByUser(db, u)
	if s.LLMAPIKeys.OpenAI != "sk-new-abcdefghijklmnop" || s.SecretErr("llm_api_key_openai") != nil {
		t.Fatal("replacing the key did not take")
	}
}

// Contract 9.3 (K-11): a personal calendar whose url cannot be decrypted is
// listed with url omitted; the others as usual; it can be deleted.
func TestPAC84_PersonalCalendarListOmitsUndecryptableURL(t *testing.T) {
	db := openTestDB(t)
	u := createTestUser(t, "pac84-personal@example.com")
	bad, err := storage.InsertPersonalCalendar(db, &storage.PersonalCalendar{UserID: u, Provider: "webcal", Name: "Bad", URL: "https://bad.example/a.ics", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storage.InsertPersonalCalendar(db, &storage.PersonalCalendar{UserID: u, Provider: "webcal", Name: "Good", URL: "https://good.example/b.ics", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE personal_calendars SET url = $2 WHERE id = $1`, bad, undecryptable); err != nil {
		t.Fatal(err)
	}
	h := newPersonalHandlersWithBlocker(db, nil)
	rec := httptest.NewRecorder()
	h.list(rec, withUser(httptest.NewRequest(http.MethodGet, "/api/personal-calendars", nil), u))
	var rows []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &rows)
	if rec.Code != http.StatusOK || len(rows) != 2 {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}
	if _, has := rows[0]["url"]; has || rows[1]["url"] != "https://good.example/b.ics" {
		t.Fatalf("rows = %v", rows)
	}
	r := chi.NewRouter()
	r.Delete("/api/personal-calendars/{id}", h.delete)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, withUser(httptest.NewRequest(http.MethodDelete, "/api/personal-calendars/"+toJSON(bad), nil), u))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body.String())
	}
}
