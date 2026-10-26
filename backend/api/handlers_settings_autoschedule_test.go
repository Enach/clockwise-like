package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
)

// PAC-49 through the PATCH handler: the write goes through the watermark rule
// and acts on the caller's row only.

// AC-8 (regression guard): A's auto-schedule save leaves B's settings as they were.
func TestPatchSettings_AutoScheduleIsPerUser(t *testing.T) {
	db := openTestDB(t)
	h := &settingsHandlers{db: db}
	a := createTestUser(t, "pac49-ac8-a@example.com")
	b := createTestUser(t, "pac49-ac8-b@example.com")
	before := settingsRequest(t, h, http.MethodGet, b, "").Body.String()

	if w := settingsRequest(t, h, http.MethodPatch, a, `{"autoScheduleEnabled":true,"autoScheduleCron":"0 7 * * 1-5"}`); w.Code != http.StatusOK {
		t.Fatalf("patch A: %d %s", w.Code, w.Body.String())
	}
	if after := settingsRequest(t, h, http.MethodGet, b, "").Body.String(); after != before {
		t.Errorf("B's settings changed:\nbefore %s\nafter  %s", before, after)
	}
	if w, _ := storage.GetAutoScheduleLastRun(db, b); w != nil {
		t.Errorf("B's watermark = %v, want null", w)
	}
	got, err := storage.ListAutoScheduleCandidates(db)
	if err != nil || len(got) != 1 || got[0].UserID != a {
		t.Errorf("candidates = %+v (err %v), want exactly [A]", got, err)
	}
}

// Enabling through PATCH sets the watermark to the save instant; a PATCH of
// an unrelated property leaves it. The wire shape is unchanged by this PR: the
// watermark is not on the settings document until the contract stage adds
// autoScheduleLastRun (spec v3.1 §4.4).
func TestPatchSettings_EnableSetsWatermark(t *testing.T) {
	db := openTestDB(t)
	h := &settingsHandlers{db: db}
	a := createTestUser(t, "pac49-patch-enable@example.com")

	start := time.Now()
	if w := settingsRequest(t, h, http.MethodPatch, a, `{"autoScheduleEnabled":true}`); w.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", w.Code, w.Body.String())
	}
	w1, err := storage.GetAutoScheduleLastRun(db, a)
	if err != nil || w1 == nil || w1.Before(start.Add(-time.Second)) || w1.After(time.Now().Add(time.Second)) {
		t.Fatalf("watermark after enabling = %v (err %v), want the save instant", w1, err)
	}

	w := settingsRequest(t, h, http.MethodPatch, a, `{"focusLabel":"Heads down"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("unrelated patch: %d %s", w.Code, w.Body.String())
	}
	if w2, _ := storage.GetAutoScheduleLastRun(db, a); w2 == nil || !w2.Equal(*w1) {
		t.Errorf("watermark after an unrelated save = %v, want %v", w2, w1)
	}
	if _, present := decodeSettingsBody(t, w)["autoScheduleLastRun"]; present {
		t.Error("autoScheduleLastRun is on the wire before the contract carries it")
	}
}

// The write refuses what the scheduler would skip: descriptors and a
// CRON_TZ prefix, with the messages it has always used.
func TestValidateSettings_CronSharesTheSchedulersParser(t *testing.T) {
	for expr, msg := range map[string]string{
		"@daily":                         "autoScheduleCron must be a 5-part cron expression",
		"CRON_TZ=Europe/Paris 0 7 * * *": "autoScheduleCron must be a 5-part cron expression",
		"61 7 * * *":                     "autoScheduleCron is invalid: ",
	} {
		err := validateSettings(completeSettings(storage.Settings{AutoScheduleCron: expr}))
		if err == nil {
			t.Errorf("%q: accepted", expr)
			continue
		}
		if got := err.Error(); len(got) < len(msg) || !containsString(got, msg) {
			t.Errorf("%q: error %q, want it to contain %q", expr, got, msg)
		}
	}
}
