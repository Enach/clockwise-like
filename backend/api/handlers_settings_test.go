package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Enach/paceday/backend/storage"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestGetSettings(t *testing.T) {
	db := openTestDB(t)
	h := &settingsHandlers{db: db}

	req := httptest.NewRequest(http.MethodGet, "/api/settings/", nil)
	w := httptest.NewRecorder()
	h.getSettings(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var s storage.Settings
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if s.WorkStart == "" {
		t.Error("WorkStart should not be empty")
	}
}

func TestPutSettings_Valid(t *testing.T) {
	db := openTestDB(t)
	h := &settingsHandlers{db: db}

	update := storage.Settings{
		WorkStart:   "08:00",
		WorkEnd:     "17:00",
		Timezone:    "UTC",
		LLMProvider: "openai",
		LLMAPIKey:   "sk-test",
	}
	body, _ := json.Marshal(update)

	req := httptest.NewRequest(http.MethodPut, "/api/settings/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.putSettings(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var got storage.Settings
	json.Unmarshal(w.Body.Bytes(), &got)
	if got.WorkStart != "08:00" {
		t.Errorf("WorkStart = %q, want 08:00", got.WorkStart)
	}
}

func TestPutSettings_InvalidJSON(t *testing.T) {
	db := openTestDB(t)
	h := &settingsHandlers{db: db}

	req := httptest.NewRequest(http.MethodPut, "/api/settings/", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()
	h.putSettings(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestPutSettings_InvalidTimeFormat(t *testing.T) {
	db := openTestDB(t)
	h := &settingsHandlers{db: db}

	update := storage.Settings{WorkStart: "9:00"} // missing leading zero
	body, _ := json.Marshal(update)

	req := httptest.NewRequest(http.MethodPut, "/api/settings/", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.putSettings(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
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
			err := validateSettings(&tc.s)
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
			if err := validateSettings(&tc.s); err == nil {
				t.Error("expected error for negative value")
			}
		})
	}
}

func TestValidateSettings_InvalidCron(t *testing.T) {
	s := &storage.Settings{AutoScheduleCron: "not a cron"}
	if err := validateSettings(s); err == nil {
		t.Error("expected error for invalid cron")
	}
}

func TestValidateSettings_ValidCron(t *testing.T) {
	s := &storage.Settings{AutoScheduleCron: "0 8 * * *"}
	if err := validateSettings(s); err != nil {
		t.Errorf("valid cron should not error: %v", err)
	}
}

func TestValidateSettings_InvalidLLMProvider(t *testing.T) {
	s := &storage.Settings{LLMProvider: "unknown_provider"}
	if err := validateSettings(s); err == nil {
		t.Error("expected error for unknown LLM provider")
	}
}

func TestValidateSettings_ValidLLMProvider(t *testing.T) {
	for _, p := range []string{"openai", "anthropic", "ollama", ""} {
		s := &storage.Settings{LLMProvider: p}
		if err := validateSettings(s); err != nil {
			t.Errorf("valid LLM provider %q should not error: %v", p, err)
		}
	}
}
