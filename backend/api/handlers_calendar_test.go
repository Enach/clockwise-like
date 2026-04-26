package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/oauth2"
)

func TestListEvents_BadStart(t *testing.T) {
	db := openTestDB(t)
	h := &calendarHandlers{oauthConfig: &oauth2.Config{}, db: db}

	req := httptest.NewRequest(http.MethodGet, "/api/calendar/events?start=bad&end=2025-01-06T18:00:00Z", nil)
	w := httptest.NewRecorder()
	h.listEvents(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestListEvents_BadEnd(t *testing.T) {
	db := openTestDB(t)
	h := &calendarHandlers{oauthConfig: &oauth2.Config{}, db: db}

	req := httptest.NewRequest(http.MethodGet, "/api/calendar/events?start=2025-01-06T09:00:00Z&end=bad", nil)
	w := httptest.NewRecorder()
	h.listEvents(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}


func TestFreeBusy_BadStart(t *testing.T) {
	db := openTestDB(t)
	h := &calendarHandlers{oauthConfig: &oauth2.Config{}, db: db}

	req := httptest.NewRequest(http.MethodGet, "/api/calendar/freebusy?start=bad&end=2025-01-06T18:00:00Z", nil)
	w := httptest.NewRecorder()
	h.freeBusy(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestFreeBusy_BadEnd(t *testing.T) {
	db := openTestDB(t)
	h := &calendarHandlers{oauthConfig: &oauth2.Config{}, db: db}

	req := httptest.NewRequest(http.MethodGet, "/api/calendar/freebusy?start=2025-01-06T09:00:00Z&end=bad", nil)
	w := httptest.NewRecorder()
	h.freeBusy(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

