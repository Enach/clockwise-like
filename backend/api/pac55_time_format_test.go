package api

import (
	"net/http"
	"testing"

	"github.com/Enach/paceday/backend/storage"
)

// PAC-55: each user's 12/24-hour display preference.
func TestSettings_TimeFormat(t *testing.T) {
	db := openTestDB(t)
	h := &settingsHandlers{db: db}
	alice := createTestUser(t, "pac55-alice@example.com")
	bob := createTestUser(t, "pac55-bob@example.com")

	// AC-1: never chosen reads 12h.
	if doc := decodeSettingsBody(t, settingsRequest(t, h, http.MethodGet, alice, "")); doc["timeFormat"] != "12h" {
		t.Fatalf("default timeFormat = %v, want 12h", doc["timeFormat"])
	}

	// AC-2: 24h is stored and nothing else changes.
	before, _ := storage.GetOrCreateSettingsByUser(db, alice)
	if w := settingsRequest(t, h, http.MethodPatch, alice, `{"timeFormat":"24h"}`); w.Code != http.StatusOK {
		t.Fatalf("patch 24h: %d %s", w.Code, w.Body.String())
	}
	after, _ := storage.GetOrCreateSettingsByUser(db, alice)
	if after.TimeFormat != "24h" {
		t.Fatalf("timeFormat = %q, want 24h", after.TimeFormat)
	}
	after.TimeFormat, after.UpdatedAt = before.TimeFormat, before.UpdatedAt
	if after.WorkStart != before.WorkStart || after.Timezone != before.Timezone || after.FocusLabel != before.FocusLabel {
		t.Errorf("another setting changed: %+v", after)
	}

	// AC-3: the other user is unaffected, and their changes do not touch alice.
	if doc := decodeSettingsBody(t, settingsRequest(t, h, http.MethodGet, bob, "")); doc["timeFormat"] != "12h" {
		t.Errorf("bob's timeFormat = %v, want 12h", doc["timeFormat"])
	}
	for _, v := range []string{"24h", "12h"} {
		if w := settingsRequest(t, h, http.MethodPatch, bob, `{"timeFormat":"`+v+`"}`); w.Code != http.StatusOK {
			t.Fatalf("bob %s: %d", v, w.Code)
		}
		if s, _ := storage.GetOrCreateSettingsByUser(db, alice); s.TimeFormat != "24h" {
			t.Errorf("alice changed to %q after bob set %s", s.TimeFormat, v)
		}
	}

	// AC-4: saving another setting keeps it.
	if w := settingsRequest(t, h, http.MethodPatch, alice, `{"focusLabel":"Deep"}`); w.Code != http.StatusOK {
		t.Fatalf("patch label: %d", w.Code)
	}
	if s, _ := storage.GetOrCreateSettingsByUser(db, alice); s.TimeFormat != "24h" {
		t.Errorf("timeFormat after another save = %q, want 24h", s.TimeFormat)
	}

	// AC-5: anything else is refused, naming the setting; nothing stored.
	for _, body := range []string{`{"timeFormat":""}`, `{"timeFormat":"AM"}`, `{"timeFormat":"24"}`, `{"timeFormat":null}`, `{"timeFormat":24}`} {
		w := settingsRequest(t, h, http.MethodPatch, alice, body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", body, w.Code)
		}
		if doc := decodeSettingsBody(t, w); doc["error"] == nil {
			t.Errorf("%s: no error message", body)
		}
	}
	if s, _ := storage.GetOrCreateSettingsByUser(db, alice); s.TimeFormat != "24h" {
		t.Errorf("a refused write stored %q", s.TimeFormat)
	}
}
