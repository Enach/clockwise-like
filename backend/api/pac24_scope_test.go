package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/nlp"
	"github.com/Enach/paceday/backend/nlp/nlptest"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// PAC-24: the daily-recap settings, the provider probe and the auth status
// all describe the caller, and nothing returns a provider key.

func TestDailyRecapSettings_IsolatedPerUserAndStrict(t *testing.T) {
	db := openTestDB(t)
	h := &dailyRecapHandlers{db: db}
	alice := createTestUser(t, "pac24-recap-alice@example.com")
	bob := createTestUser(t, "pac24-recap-bob@example.com")

	do := func(method string, userID uuid.UUID, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := withUser(httptest.NewRequest(method, "/api/settings/daily-recap/", bytes.NewBufferString(body)), userID)
		w := httptest.NewRecorder()
		if method == http.MethodGet {
			h.getSettings(w, req)
		} else {
			h.patchSettings(w, req)
		}
		return w
	}

	if w := do(http.MethodPatch, alice, `{"enabled":true,"send_time":"07:15","send_to":"channel","channel_id":"C1"}`); w.Code != http.StatusNoContent {
		t.Fatalf("patch alice: %d %s", w.Code, w.Body.String())
	}
	var a, b map[string]any
	_ = json.Unmarshal(do(http.MethodGet, alice, "").Body.Bytes(), &a)
	_ = json.Unmarshal(do(http.MethodGet, bob, "").Body.Bytes(), &b)
	if a["enabled"] != true || a["send_time"] != "07:15" || a["send_to"] != "channel" {
		t.Errorf("alice's recap = %v", a)
	}
	if b["enabled"] != false || b["send_time"] != "08:00" || b["send_to"] != "dm" {
		t.Errorf("bob sees alice's recap: %v", b)
	}

	for name, body := range map[string]string{
		"null":         `{"enabled":null}`,
		"unknown":      `{"sendTime":"07:00"}`,
		"bad time":     `{"send_time":"7am"}`,
		"bad dest":     `{"send_to":"email"}`,
		"not object":   `[]`,
		"invalid json": `{`,
	} {
		if w := do(http.MethodPatch, alice, body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400; body %s", name, w.Code, w.Body.String())
		}
	}
	_ = json.Unmarshal(do(http.MethodGet, alice, "").Body.Bytes(), &a)
	if a["send_time"] != "07:15" || a["enabled"] != true {
		t.Errorf("a rejected recap PATCH changed alice's recap: %v", a)
	}
}

func TestLLMTest_BodyRulesAndNoKeyEcho(t *testing.T) {
	// A provider that rejects every key and echoes what it was sent, the
	// worst case for leaking the key into a 502 body.
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key: ` + r.Header.Get("Authorization") + `"}`))
	}))
	defer provider.Close()

	db := openTestDB(t)
	h := &llmHandlers{db: db}
	user := createTestUser(t, "pac24-llm@example.com")
	s, err := storage.GetOrCreateSettingsByUser(db, user)
	if err != nil {
		t.Fatal(err)
	}
	// PAC-66-B §B6: the public-host Given. The stored base URL is empty, so the
	// destination the rule sees is api.openai.com (approved, D-B2); the test's
	// transport delivers the request to the in-process provider instead.
	t.Cleanup(nlp.SetUserTransport(nlptest.ForwardTo(provider)))
	s.LLMProvider, s.LLMModel, s.LLMBaseURL, s.LLMAPIKey = "openai", "gpt-test", "", "sk-stored-secret-1"
	if err := storage.SaveSettingsForUser(db, user, s); err != nil {
		t.Fatal(err)
	}

	probe := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		req := withUser(httptest.NewRequest(http.MethodPost, "/api/llm/test", bytes.NewReader([]byte(body))), user)
		w := httptest.NewRecorder()
		h.testLLM(w, req)
		return w
	}

	for name, body := range map[string]string{
		"absent body":      "",
		"null body":        "null",
		"a destination":    `{"llmBaseUrl":"https://attacker.example"}`,
		"an endpoint":      `{"ollamaBaseUrl":"http://10.0.0.1"}`,
		"unknown provider": `{"llmProvider":"skynet"}`,
		"malformed":        `{"llmProvider":`,
	} {
		if w := probe(body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400; body %s", name, w.Code, w.Body.String())
		}
	}

	for name, tc := range map[string]struct{ body, key string }{
		"stored key":    {`{}`, "sk-stored-secret-1"},
		"submitted key": {`{"llmApiKey":"sk-submitted-secret-2"}`, "sk-submitted-secret-2"},
	} {
		w := probe(tc.body)
		if w.Code != http.StatusBadGateway {
			t.Errorf("%s: status = %d, want 502; body %s", name, w.Code, w.Body.String())
		}
		for _, key := range []string{"sk-stored-secret-1", "sk-submitted-secret-2"} {
			if strings.Contains(w.Body.String(), key) {
				t.Errorf("%s: the response carries a key: %s", name, w.Body.String())
			}
		}
	}

	// PAC-66-B AC-B1 supersedes the old 502 after contacting an
	// http://127.0.0.1 base URL: it is now refused with the 422, still with no
	// key in the body (AC-B16), and nothing reaches the provider.
	s.LLMBaseURL = provider.URL
	if err := storage.SaveSettingsForUser(db, user, s); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"stored key": `{}`, "submitted key": `{"llmApiKey":"sk-submitted-secret-2"}`} {
		w := probe(body)
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), `"property":"llmBaseUrl"`) {
			t.Errorf("%s with a loopback base URL: %d %s, want 422 naming llmBaseUrl", name, w.Code, w.Body.String())
		}
		for _, key := range []string{"sk-stored-secret-1", "sk-submitted-secret-2"} {
			if strings.Contains(w.Body.String(), key) {
				t.Errorf("%s: the 422 carries a key: %s", name, w.Body.String())
			}
		}
	}
}

func TestAuthStatus_DescribesOnlyTheCaller(t *testing.T) {
	db := openTestDB(t)
	alice := createTestUser(t, "pac24-status-alice@example.com")
	bob := createTestUser(t, "pac24-status-bob@example.com")
	if err := auth.SaveMicrosoftToken(db, alice, &oauth2.Token{AccessToken: "a", RefreshToken: "alice-refresh", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("save alice's outlook token: %v", err)
	}
	if _, err := db.Exec(`UPDATE settings SET calendar_email = 'alice@outlook.example' WHERE user_id = $1`, alice); err != nil {
		t.Fatal(err)
	}

	h := &authHandlers{oauthConfig: &oauth2.Config{}, db: db}
	status := func(user uuid.UUID) map[string]any {
		t.Helper()
		req := withUser(httptest.NewRequest(http.MethodGet, "/api/auth/status", nil), user)
		w := httptest.NewRecorder()
		h.status(w, req)
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return body
	}
	if a := status(alice); a["provider"] != "outlook" || a["connected"] != true || a["email"] != "alice@outlook.example" {
		t.Errorf("alice's status = %v", a)
	}
	if b := status(bob); b["provider"] != "google" || b["connected"] != false || b["email"] != "" {
		t.Errorf("bob's status carries alice's connection: %v", b)
	}
	// Bob connecting Outlook does not replace Alice's credential.
	if err := auth.SaveMicrosoftToken(db, bob, &oauth2.Token{AccessToken: "b", RefreshToken: "bob-refresh"}); err != nil {
		t.Fatal(err)
	}
	if tok, _ := auth.LoadMicrosoftToken(db, alice); tok == nil || tok.RefreshToken != "alice-refresh" {
		t.Errorf("alice's outlook token after bob connected = %+v", tok)
	}
}

// A document read from GET can be submitted back unchanged even when it holds
// a legacy combination a newer rule rejects, and such a value never stops the
// user saving something else (contract revision 3, challenge finding 2).
func TestPatchSettings_LegacyValuesDoNotLockTheUserOut(t *testing.T) {
	db := openTestDB(t)
	h := &settingsHandlers{db: db}
	user := createTestUser(t, "pac24-legacy@example.com")
	if _, err := storage.GetOrCreateSettingsByUser(db, user); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE settings SET work_start = '17:00', work_end = '08:00',
			focus_min_block_minutes = 200, focus_max_block_minutes = 60 WHERE user_id = $1`, user); err != nil {
		t.Fatal(err)
	}

	read := settingsRequest(t, h, http.MethodGet, user, "").Body.String()
	if w := settingsRequest(t, h, http.MethodPatch, user, read); w.Code != http.StatusOK {
		t.Errorf("round trip of the GET document: %d %s", w.Code, w.Body.String())
	}
	if w := settingsRequest(t, h, http.MethodPatch, user, `{"timezone":"Europe/Paris"}`); w.Code != http.StatusOK {
		t.Errorf("unrelated change: %d %s", w.Code, w.Body.String())
	}
	if w := settingsRequest(t, h, http.MethodPatch, user, `{"workEnd":"07:00"}`); w.Code != http.StatusBadRequest {
		t.Errorf("changing workEnd to another inverted value: %d, want 400", w.Code)
	}
	if w := settingsRequest(t, h, http.MethodPatch, user, `{"workingHours":{"mode":"BY_DAY","default":{"enabled":true,"start":"09:00","end":"17:00"},"days":{"Monday":{"enabled":true,"start":"09:00","end":"17:00"}}}}`); w.Code != http.StatusBadRequest {
		t.Errorf("an upper-case mode or weekday key: %d, want 400", w.Code)
	}
	// Contract challenge r3, finding 1: days and default are checked whatever the mode.
	for name, body := range map[string]string{
		"bad day in all_days":   `{"workingHours":{"mode":"all_days","default":{"enabled":true,"start":"09:00","end":"17:00"},"days":{"Monday":{"enabled":true,"start":"99:99","end":"x"}}}}`,
		"bad clock in all_days": `{"workingHours":{"mode":"all_days","default":{"enabled":true,"start":"09:00","end":"17:00"},"days":{"monday":{"enabled":true,"start":"99:99","end":"10:00"}}}}`,
		"bad default in by_day": `{"workingHours":{"mode":"by_day","default":{"enabled":true,"start":"99:99","end":"17:00"},"days":{"monday":{"enabled":true,"start":"09:00","end":"17:00"}}}}`,
	} {
		if w := settingsRequest(t, h, http.MethodPatch, user, body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400; body %s", name, w.Code, w.Body.String())
		}
	}
}

// Contract challenge r5, finding 2: the preview is built in the caller's zone.
func TestDailyRecapPreview_UsesTheCallersTimezone(t *testing.T) {
	db := openTestDB(t)
	h := &dailyRecapHandlers{db: db}
	user := createTestUser(t, "pac24-recap-tz@example.com")
	s, err := storage.GetOrCreateSettingsByUser(db, user)
	if err != nil {
		t.Fatal(err)
	}
	s.Timezone = "Pacific/Kiritimati"
	if err := storage.SaveSettingsForUser(db, user, s); err != nil {
		t.Fatal(err)
	}
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/settings/daily-recap/preview", nil), user)
	w := httptest.NewRecorder()
	h.preview(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Pacific/Kiritimati") {
		t.Errorf("preview = %d %s, want a header in Pacific/Kiritimati", w.Code, w.Body.String())
	}
}
