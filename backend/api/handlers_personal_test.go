package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type fakePersonalBlocker struct {
	previewID     int64
	previewStart  time.Time
	previewEnd    time.Time
	previewEvents []calendar.GenericEvent
	previewErr    error
	syncID        int64
	syncErr       error
}

func (f *fakePersonalBlocker) Preview(_ context.Context, personalCalendarID int64, start, end time.Time) ([]calendar.GenericEvent, error) {
	f.previewID = personalCalendarID
	f.previewStart = start
	f.previewEnd = end
	return f.previewEvents, f.previewErr
}

func (f *fakePersonalBlocker) Sync(_ context.Context, personalCalendarID int64) error {
	f.syncID = personalCalendarID
	return f.syncErr
}

func setupPersonalRoutes(t *testing.T, blocker PersonalCalendarBlocker) *chi.Mux {
	t.Helper()
	h := newPersonalHandlersWithBlocker(openTestDB(t), blocker)
	r := chi.NewRouter()
	r.Get("/api/personal-calendars", h.list)
	r.Post("/api/personal-calendars", h.create)
	r.Patch("/api/personal-calendars/{id}", h.patch)
	r.Delete("/api/personal-calendars/{id}", h.delete)
	r.Get("/api/personal-calendars/{id}/preview", h.preview)
	r.Post("/api/personal-calendars/{id}/sync", h.sync)
	return r
}

func createPersonalCalendar(t *testing.T, userID uuid.UUID, provider, name, url string, enabled bool) int64 {
	t.Helper()
	id, err := storage.InsertPersonalCalendar(openTestDB(t), &storage.PersonalCalendar{
		UserID: userID, Provider: provider, Name: name, URL: url, Enabled: enabled,
	})
	if err != nil {
		t.Fatalf("create personal calendar: %v", err)
	}
	return id
}

func TestPersonalCalendarCRUDContract(t *testing.T) {
	r := setupPersonalRoutes(t, &fakePersonalBlocker{})
	userID := createTestUser(t, "personal-owner@example.com")

	createBody := `{"provider":"webcal","name":"Family","url":"webcal://example.com/family.ics","enabled":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/personal-calendars", bytes.NewBufferString(createBody))
	req = withUser(req, userID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	var created personalCalendarDTO
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Type != "webcal" || created.Label != "Family" || created.URL != "webcal://example.com/family.ics" || !created.Enabled {
		t.Fatalf("created = %+v, want webcal family enabled calendar", created)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/personal-calendars", nil)
	req = withUser(req, userID)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", w.Code)
	}
	var listed []personalCalendarDTO
	if err := json.NewDecoder(w.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listed) == 0 {
		t.Fatal("expected at least one personal calendar in list")
	}

	patchBody := `{"name":"Family Shared","enabled":false}`
	req = httptest.NewRequest(http.MethodPatch, "/api/personal-calendars/"+created.ID, bytes.NewBufferString(patchBody))
	req = withUser(req, userID)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var updated personalCalendarDTO
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode patch response: %v", err)
	}
	if updated.Label != "Family Shared" || updated.Enabled {
		t.Fatalf("updated = %+v, want renamed disabled calendar", updated)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/personal-calendars/"+created.ID, nil)
	req = withUser(req, userID)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
}

func TestPersonalCalendarCreateRequiresProvider(t *testing.T) {
	r := setupPersonalRoutes(t, &fakePersonalBlocker{})
	userID := createTestUser(t, "personal-missing-provider@example.com")

	req := httptest.NewRequest(http.MethodPost, "/api/personal-calendars", bytes.NewBufferString(`{"name":"No Provider"}`))
	req = withUser(req, userID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestPersonalCalendarPreviewUsesInjectedBlocker(t *testing.T) {
	userID := createTestUser(t, "personal-preview@example.com")
	calendarID := createPersonalCalendar(t, userID, "webcal", "Family", "webcal://example.com/family.ics", true)
	blocker := &fakePersonalBlocker{previewEvents: []calendar.GenericEvent{{ID: "evt-1", Title: "School pickup"}}}
	r := setupPersonalRoutes(t, blocker)

	path := "/api/personal-calendars/" + strconv.FormatInt(calendarID, 10) + "/preview"
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req = withUser(req, userID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if blocker.previewID != calendarID {
		t.Fatalf("preview blocker id = %d, want %d", blocker.previewID, calendarID)
	}
	if blocker.previewEnd.Sub(blocker.previewStart) != 14*24*time.Hour {
		t.Fatalf("preview range = %v, want 14 days", blocker.previewEnd.Sub(blocker.previewStart))
	}
	var events []calendar.GenericEvent
	if err := json.NewDecoder(w.Body).Decode(&events); err != nil {
		t.Fatalf("decode preview response: %v", err)
	}
	if len(events) != 1 || events[0].ID != "evt-1" {
		t.Fatalf("events = %+v, want one preview event", events)
	}
}

func TestPersonalCalendarSyncMarksCalendarSynced(t *testing.T) {
	userID := createTestUser(t, "personal-sync@example.com")
	calendarID := createPersonalCalendar(t, userID, "webcal", "Family", "webcal://example.com/family.ics", true)
	blocker := &fakePersonalBlocker{}
	r := setupPersonalRoutes(t, blocker)

	path := "/api/personal-calendars/" + strconv.FormatInt(calendarID, 10) + "/sync"
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req = withUser(req, userID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if blocker.syncID != calendarID {
		t.Fatalf("sync blocker id = %d, want %d", blocker.syncID, calendarID)
	}
	var synced personalCalendarDTO
	if err := json.NewDecoder(w.Body).Decode(&synced); err != nil {
		t.Fatalf("decode sync response: %v", err)
	}
	if synced.LastSyncedAt == nil || *synced.LastSyncedAt == "" {
		t.Fatalf("sync response = %+v, want last_synced_at", synced)
	}
}

func TestPersonalCalendarSyncPropagatesBlockerError(t *testing.T) {
	userID := createTestUser(t, "personal-sync-error@example.com")
	calendarID := createPersonalCalendar(t, userID, "webcal", "Family", "webcal://example.com/family.ics", true)
	blocker := &fakePersonalBlocker{syncErr: errors.New("provider unavailable")}
	r := setupPersonalRoutes(t, blocker)

	path := "/api/personal-calendars/" + strconv.FormatInt(calendarID, 10) + "/sync"
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req = withUser(req, userID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body: %s", w.Code, w.Body.String())
	}
}
