package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// PAC-24: GET and PATCH /api/settings act on the caller's own row, the
// provider key is write-only, and PATCH merges.

func settingsRequest(t *testing.T, h *settingsHandlers, method string, user uuid.UUID, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != "" {
		reader = bytes.NewReader([]byte(body))
	} else {
		reader = bytes.NewReader(nil)
	}
	req := withUser(httptest.NewRequest(method, "/api/settings/", reader), user)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	if method == http.MethodGet {
		h.getSettings(w, req)
	} else {
		h.patchSettings(w, req)
	}
	return w
}

func decodeSettingsBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return doc
}

func TestGetSettings_CreatesRowForNewUser(t *testing.T) {
	h := &settingsHandlers{db: openTestDB(t)}
	user := createTestUser(t, "pac24-new@example.com")
	w := settingsRequest(t, h, http.MethodGet, user, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", w.Code, w.Body.String())
	}
	doc := decodeSettingsBody(t, w)
	if doc["workStart"] != "09:00" || doc["recapSendTime"] != "08:00" || doc["llmApiKeySet"] != false {
		t.Errorf("new user's settings = %v", doc)
	}
}

func TestGetSettings_IsolatedPerUser(t *testing.T) {
	db := openTestDB(t)
	h := &settingsHandlers{db: db}
	alice := createTestUser(t, "pac24-iso-alice@example.com")
	bob := createTestUser(t, "pac24-iso-bob@example.com")

	if w := settingsRequest(t, h, http.MethodPatch, alice, `{"workStart":"06:00","timezone":"Asia/Tokyo"}`); w.Code != http.StatusOK {
		t.Fatalf("patch alice: %d %s", w.Code, w.Body.String())
	}
	a := decodeSettingsBody(t, settingsRequest(t, h, http.MethodGet, alice, ""))
	b := decodeSettingsBody(t, settingsRequest(t, h, http.MethodGet, bob, ""))
	if a["workStart"] != "06:00" || a["timezone"] != "Asia/Tokyo" {
		t.Errorf("alice = %v", a)
	}
	if b["workStart"] != "09:00" || b["timezone"] != "UTC" {
		t.Errorf("bob sees alice's settings: %v", b)
	}
}

// containsString reports whether any string anywhere in v equals or contains needle.
func containsString(v any, needle string) bool {
	switch x := v.(type) {
	case string:
		return strings.Contains(x, needle)
	case map[string]any:
		for k, e := range x {
			if strings.Contains(k, needle) || containsString(e, needle) {
				return true
			}
		}
	case []any:
		for _, e := range x {
			if containsString(e, needle) {
				return true
			}
		}
	}
	return false
}

func TestSettings_NeverReturnsProviderKey(t *testing.T) {
	h := &settingsHandlers{db: openTestDB(t)}
	user := createTestUser(t, "pac24-key@example.com")
	const key = "sk-pac24-very-secret"

	w := settingsRequest(t, h, http.MethodPatch, user, `{"llmProvider":"openai","llmApiKey":"`+key+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", w.Code, w.Body.String())
	}
	for name, resp := range map[string]*httptest.ResponseRecorder{
		"PATCH": w,
		"GET":   settingsRequest(t, h, http.MethodGet, user, ""),
	} {
		doc := decodeSettingsBody(t, resp)
		if containsString(doc, key) {
			t.Errorf("%s response carries the provider key: %s", name, resp.Body.String())
		}
		if _, ok := doc["llmApiKey"]; ok {
			t.Errorf("%s response has an llmApiKey property", name)
		}
		if doc["llmApiKeySet"] != true {
			t.Errorf("%s llmApiKeySet = %v, want true", name, doc["llmApiKeySet"])
		}
	}
}

func TestPatchSettings_ChangesOnlyWhatIsSent(t *testing.T) {
	db := openTestDB(t)
	h := &settingsHandlers{db: db}
	user := createTestUser(t, "pac24-merge@example.com")
	setup := `{"workStart":"07:00","focusLabel":"Deep","llmProvider":"openai","llmApiKey":"kept","compressionEnabled":true,
		"workingHours":{"mode":"by_day","days":{"monday":{"enabled":true,"start":"07:00","end":"12:00"}}}}`
	if w := settingsRequest(t, h, http.MethodPatch, user, setup); w.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	before, _ := storage.GetOrCreateSettingsByUser(db, user)

	if w := settingsRequest(t, h, http.MethodPatch, user, `{"timezone":"Europe/Paris"}`); w.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", w.Code, w.Body.String())
	}
	after, _ := storage.GetOrCreateSettingsByUser(db, user)
	if after.Timezone != "Europe/Paris" {
		t.Errorf("timezone = %q, want Europe/Paris", after.Timezone)
	}
	after.Timezone, after.UpdatedAt = before.Timezone, before.UpdatedAt
	a, _ := json.Marshal(after)
	b, _ := json.Marshal(before)
	if string(a) != string(b) || after.LLMAPIKeys.OpenAI != "kept" {
		t.Errorf("properties not sent were changed:\nbefore %s\nafter  %s", b, a)
	}
}

func TestPatchSettings_EmptyValueClears(t *testing.T) {
	db := openTestDB(t)
	h := &settingsHandlers{db: db}
	user := createTestUser(t, "pac24-clear@example.com")
	if w := settingsRequest(t, h, http.MethodPatch, user, `{"llmProvider":"openai","llmApiKey":"to-clear","bufferBeforeMinutes":10,"compressionEnabled":true}`); w.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	if w := settingsRequest(t, h, http.MethodPatch, user, `{"llmApiKey":"","bufferBeforeMinutes":0,"compressionEnabled":false}`); w.Code != http.StatusOK {
		t.Fatalf("clear: %d %s", w.Code, w.Body.String())
	}
	got, _ := storage.GetOrCreateSettingsByUser(db, user)
	if got.LLMAPIKeys.OpenAI != "" || got.BufferBeforeMinutes != 0 || got.CompressionEnabled {
		t.Errorf(`"", 0 and false must clear: %+v`, got)
	}
}

func TestPatchSettings_RejectsNullAndUnknown(t *testing.T) {
	db := openTestDB(t)
	h := &settingsHandlers{db: db}
	user := createTestUser(t, "pac24-reject@example.com")
	before, _ := storage.GetOrCreateSettingsByUser(db, user)
	for name, body := range map[string]string{
		"null":             `{"workStart":null}`,
		"unknown":          `{"notASetting":1}`,
		"snake_case":       `{"work_start":"07:00"}`,
		"not an object":    `[1,2]`,
		"null body":        `null`,
		"wrong type":       `{"workStart":7}`,
		"bad clock":        `{"workStart":"25:00"}`,
		"inverted day":     `{"workStart":"18:00","workEnd":"09:00"}`,
		"bad timezone":     `{"timezone":"Mars/Olympus"}`,
		"bad calendar":     `{"calendarProvider":"yahoo"}`,
		"bad conferencing": `{"conferencingProvider":""}`,
		"bad recap time":   `{"recapSendTime":"8am"}`,
		"focus min > max":  `{"focusMinBlockMinutes":200,"focusMaxBlockMinutes":60}`,
	} {
		w := settingsRequest(t, h, http.MethodPatch, user, body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400; body %s", name, w.Code, w.Body.String())
		}
	}
	after, _ := storage.GetOrCreateSettingsByUser(db, user)
	if after.WorkStart != before.WorkStart || after.Timezone != before.Timezone || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("a rejected PATCH stored something: %+v", after)
	}
}

func TestPatchSettings_ReplacesScheduleMapsWholly(t *testing.T) {
	db := openTestDB(t)
	h := &settingsHandlers{db: db}
	user := createTestUser(t, "pac24-maps@example.com")
	first := `{"workingHours":{"mode":"by_day","days":{"monday":{"enabled":true,"start":"08:00","end":"12:00"},"tuesday":{"enabled":true,"start":"08:00","end":"12:00"}}},
		"lunchBreaks":{"monday":{"enabled":true,"start":"12:00","end":"13:00"}}}`
	if w := settingsRequest(t, h, http.MethodPatch, user, first); w.Code != http.StatusOK {
		t.Fatalf("first: %d %s", w.Code, w.Body.String())
	}
	second := `{"workingHours":{"mode":"by_day","days":{"monday":{"enabled":true,"start":"09:00","end":"11:00"}}},"lunchBreaks":{}}`
	if w := settingsRequest(t, h, http.MethodPatch, user, second); w.Code != http.StatusOK {
		t.Fatalf("second: %d %s", w.Code, w.Body.String())
	}
	got, _ := storage.GetOrCreateSettingsByUser(db, user)
	if _, ok := got.WorkingHours.Days["tuesday"]; ok {
		t.Errorf("a weekday omitted from workingHours.days was kept: %+v", got.WorkingHours.Days)
	}
	if got.WorkingHours.Days["monday"].Start != "09:00" {
		t.Errorf("monday = %+v, want 09:00", got.WorkingHours.Days["monday"])
	}
	if len(got.LunchBreaks) != 0 {
		t.Errorf("lunchBreaks {} should clear every override: %+v", got.LunchBreaks)
	}
}

func TestPatchSettings_RoundTripKeepsKeyAndIgnoresReadOnly(t *testing.T) {
	db := openTestDB(t)
	h := &settingsHandlers{db: db}
	user := createTestUser(t, "pac24-roundtrip@example.com")
	if w := settingsRequest(t, h, http.MethodPatch, user, `{"llmProvider":"openai","llmApiKey":"round-trip-key"}`); w.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	// A client submits back exactly the document it read, readOnly properties included.
	read := settingsRequest(t, h, http.MethodGet, user, "").Body.String()
	doc := decodeSettingsBody(t, settingsRequest(t, h, http.MethodGet, user, ""))
	doc["llmApiKeySet"] = false
	doc["updatedAt"] = "2000-01-01T00:00:00Z"
	body, _ := json.Marshal(doc)
	if w := settingsRequest(t, h, http.MethodPatch, user, string(body)); w.Code != http.StatusOK {
		t.Fatalf("round trip: %d %s (read %s)", w.Code, w.Body.String(), read)
	}
	got, _ := storage.GetOrCreateSettingsByUser(db, user)
	if got.LLMAPIKeys.OpenAI != "round-trip-key" || !got.LLMAPIKeySet {
		t.Errorf("round trip lost the key: set=%v", got.LLMAPIKeySet)
	}
	if got.UpdatedAt.Year() == 2000 {
		t.Error("updatedAt from the body was stored")
	}
}

func TestValidateSettings_TimeFields(t *testing.T) {
	cases := []struct {
		name    string
		s       storage.Settings
		wantErr bool
	}{
		{"valid", storage.Settings{WorkStart: "09:00", WorkEnd: "18:00"}, false},
		{"invalid workStart", storage.Settings{WorkStart: "9:00"}, true},
		{"invalid workEnd", storage.Settings{WorkEnd: "18:0"}, true},
		{"invalid lunchStart", storage.Settings{LunchStart: "12"}, true},
		{"invalid lunchEnd", storage.Settings{LunchEnd: "badtime"}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSettings(completeSettings(tc.s))
			if (err != nil) != tc.wantErr {
				t.Errorf("wantErr=%v, got err=%v", tc.wantErr, err)
			}
		})
	}
}

func TestValidateSettings_NegativeValues(t *testing.T) {
	cases := []struct {
		name string
		s    storage.Settings
	}{
		{"negative focusMin", storage.Settings{FocusMinBlockMinutes: -1}},
		{"negative focusMax", storage.Settings{FocusMaxBlockMinutes: -1}},
		{"negative focusTarget", storage.Settings{FocusDailyTargetMinutes: -1}},
		{"negative bufferBefore", storage.Settings{BufferBeforeMinutes: -1}},
		{"negative bufferAfter", storage.Settings{BufferAfterMinutes: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateSettings(completeSettings(tc.s)); err == nil {
				t.Error("expected error for negative value")
			}
		})
	}
}

func TestValidateSettings_InvalidCron(t *testing.T) {
	s := &storage.Settings{AutoScheduleCron: "not a cron"}
	if err := validateSettings(completeSettings(*s)); err == nil {
		t.Error("expected error for invalid cron")
	}
}

func TestValidateSettings_ValidCron(t *testing.T) {
	s := &storage.Settings{AutoScheduleCron: "0 8 * * *"}
	if err := validateSettings(completeSettings(*s)); err != nil {
		t.Errorf("valid cron should not error: %v", err)
	}
}

func TestValidateSettings_InvalidLLMProvider(t *testing.T) {
	// NEW-llm-bring-your-own AC-6: ollama is no longer a choice; "unsupported"
	// is only ever accepted unchanged (validateSettings checks everything).
	for _, p := range []string{"unknown_provider", "ollama", "unsupported"} {
		s := &storage.Settings{LLMProvider: p}
		if err := validateSettings(completeSettings(*s)); err == nil {
			t.Errorf("expected error for LLM provider %q", p)
		}
	}
}

func TestValidateSettings_ValidLLMProvider(t *testing.T) {
	for _, p := range []string{"openai", "anthropic", "azure_openai", "bedrock", "vertex", "organization", ""} {
		s := &storage.Settings{LLMProvider: p}
		if err := validateSettings(completeSettings(*s)); err != nil {
			t.Errorf("valid LLM provider %q should not error: %v", p, err)
		}
	}
}
