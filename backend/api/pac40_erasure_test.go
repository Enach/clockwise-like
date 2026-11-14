package api

// PAC-40 part B at the HTTP boundary (contract §2.2, §2.3, §5): after the
// erasure job ran, the owner's archive shows the erased state (AC-32), the
// slug stays reserved (AC-8, after-erasure half), and the owner's activity
// log carries the entry with the contract's action (AC-31).

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
)

func TestPAC40B_ArchiveAfterErasure(t *testing.T) {
	e := newPAC40Env(t, openTestDB(t))
	owner := createConfirmedUser(t, "pac40b-arch-owner@example.com")
	link := createSchedulingLinkRecord(t, owner, "pac40b-erased", "PAC-40B Erased")
	other := createSchedulingLinkRecord(t, owner, "pac40b-other", "Other")
	s1 := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	pac40Booking(t, e.db, link.ID, "xena", s1, "")
	b2 := pac40Booking(t, e.db, link.ID, "yuri", s1.Add(2*time.Hour), "cancelled")
	e.deleteLink(owner, link.ID)

	// Before the run: awaiting erasure with its due instant.
	lastEnd := b2.EndTime.UTC()
	due := lastEnd.Add(90 * 24 * time.Hour)
	w := e.do(http.MethodGet, "/api/scheduling-links/archive/"+link.ID.String(), owner, "")
	e.mustStatus(w, http.StatusOK, "archive before erasure")
	var before struct {
		Link     map[string]any   `json:"link"`
		Bookings []map[string]any `json:"bookings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &before); err != nil {
		t.Fatal(err)
	}
	if before.Link["erasure_due_at"] != due.Format(time.RFC3339Nano) || before.Link["erasure_state"] != "awaiting_erasure" {
		t.Fatalf("AC-21 before the run: %v; want due %s", before.Link, due)
	}

	ran := due.Add(time.Minute)
	job := &engine.BookerErasure{DB: e.db, Now: func() time.Time { return ran }, Logf: func(string, ...any) {}}
	if s := job.Run(context.Background()); s.BookingsErased != 2 {
		t.Fatalf("run = %+v", s)
	}

	// AC-32: erased state with erased_at; each booking keeps start, end,
	// status, notes, created_at; null name and email with booker_erased_at.
	w = e.do(http.MethodGet, "/api/scheduling-links/archive/"+link.ID.String(), owner, "")
	e.mustStatus(w, http.StatusOK, "AC-32 archive detail")
	var after struct {
		Link     map[string]any   `json:"link"`
		Bookings []map[string]any `json:"bookings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	ranAt := ran.Format(time.RFC3339Nano)
	if after.Link["erasure_state"] != "erased" || after.Link["erased_at"] != ranAt || after.Link["erasure_due_at"] != nil ||
		after.Link["bookings_count"] != float64(2) || after.Link["last_booking_end_at"] != lastEnd.Format(time.RFC3339Nano) {
		t.Errorf("AC-32 link = %v", after.Link)
	}
	if len(after.Bookings) != 2 {
		t.Fatalf("AC-32 bookings = %d", len(after.Bookings))
	}
	for i, b := range after.Bookings {
		if v, ok := b["booker_name"]; !ok || v != nil {
			t.Errorf("booking %d booker_name = %v (present %v); want null", i, v, ok)
		}
		if v, ok := b["booker_email"]; !ok || v != nil {
			t.Errorf("booking %d booker_email = %v (present %v); want null", i, v, ok)
		}
		if b["booker_erased_at"] != ranAt {
			t.Errorf("booking %d booker_erased_at = %v; want %s", i, b["booker_erased_at"], ranAt)
		}
		for _, k := range []string{"id", "start_time", "end_time", "status", "notes", "created_at"} {
			if b[k] != before.Bookings[i][k] {
				t.Errorf("booking %d %s = %v; before the run %v", i, k, b[k], before.Bookings[i][k])
			}
		}
	}
	// The names survive only inside the notes the fixture wrote ("notes of
	// xena"), which are kept (OQ-8 default); never as a name or an address.
	if strings.Contains(w.Body.String(), "@booker.test") || strings.Contains(w.Body.String(), `""`) {
		t.Errorf("archive after erasure holds an email or an empty string: %s", w.Body.String())
	}

	// The list agrees.
	w = e.do(http.MethodGet, "/api/scheduling-links/archive", owner, "")
	e.mustStatus(w, http.StatusOK, "archive list")
	if !strings.Contains(w.Body.String(), `"erasure_state":"erased"`) || !strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Errorf("archive list = %s", w.Body.String())
	}

	// AC-8 (after erasure): the slug is still taken.
	e.mustStatus(e.do(http.MethodPost, "/api/scheduling-links", owner, `{"title":"New","slug":"pac40b-erased"}`), http.StatusConflict, "create on an erased link's slug")
	e.mustStatus(e.do(http.MethodPatch, "/api/scheduling-links/"+other.ID.String(), owner, `{"slug":"pac40b-erased"}`), http.StatusConflict, "patch to an erased link's slug")

	// AC-31 through the activity log read: the contract's action, no booker data.
	entries, err := storage.ListAuditLog(e.db, owner, 50)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, a := range entries {
		if a.Action == "scheduling_link_bookers_erased" {
			found++
			if strings.Contains(a.Details, "xena") || strings.Contains(a.Details, "@booker.test") || !strings.Contains(a.Details, link.ID.String()) {
				t.Errorf("entry details = %s", a.Details)
			}
		}
	}
	if found != 1 {
		t.Errorf("erasure entries in the owner's log = %d; want 1", found)
	}
}
