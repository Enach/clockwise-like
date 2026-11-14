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

// Review of #269, finding 3 (K4-9): an erasure date past the year 9999 is
// clamped to 9999-12-31T23:59:59Z, so the archive is always writable.
func TestPAC40B_ErasureDueAtIsClampedToTheLastWritableInstant(t *testing.T) {
	end := time.Date(9999, 12, 1, 10, 0, 0, 0, time.UTC)
	dto := archivedLinkDTO(&storage.ArchivedLink{BookingsCount: 1, AwaitingErasure: 1, LastBookingEndAt: &end})
	want := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	if dto.ErasureDueAt == nil || !dto.ErasureDueAt.Equal(want) {
		t.Fatalf("erasure_due_at = %v; want %v", dto.ErasureDueAt, want)
	}
	if _, err := json.Marshal(dto); err != nil {
		t.Fatalf("clamped DTO does not encode: %v", err)
	}
	normal := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := clampWireInstant(normal); !got.Equal(normal) {
		t.Errorf("clamp moved %v to %v", normal, got)
	}

	// Through the handler: a legacy booking ending in December 9999.
	e := newPAC40Env(t, openTestDB(t))
	owner := createConfirmedUser(t, "pac40b-9999@example.com")
	link := createSchedulingLinkRecord(t, owner, "pac40b-9999", "Far")
	if _, err := e.db.Exec(`INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time, notes)
		VALUES ($1, 'n', 'n@booker.test', '9999-12-01T09:30:00Z', '9999-12-01T10:00:00Z', '')`, link.ID); err != nil {
		t.Fatal(err)
	}
	e.deleteLink(owner, link.ID)
	w := e.do(http.MethodGet, "/api/scheduling-links/archive", owner, "")
	e.mustStatus(w, http.StatusOK, "archive with a 9999 booking")
	if !strings.Contains(w.Body.String(), `"erasure_due_at":"9999-12-31T23:59:59Z"`) {
		t.Errorf("archive = %s; want the clamped due instant", w.Body.String())
	}
}

// Review of #269, finding 3: a stored instant JSON cannot write (a booking
// ending after the year 9999, possible only outside the product) makes both
// archive reads a logged 500 with the JSON error body, never a 200 with an
// empty or truncated body.
func TestPAC40B_ArchiveEncodeFailureIsA500(t *testing.T) {
	e := newPAC40Env(t, openTestDB(t))
	owner := createConfirmedUser(t, "pac40b-10000@example.com")
	link := createSchedulingLinkRecord(t, owner, "pac40b-10000", "Beyond")
	if _, err := e.db.Exec(`INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time, notes)
		VALUES ($1, 'n', 'n@booker.test', '9999-12-31T23:00:00Z', '10000-01-01T01:00:00Z', '')`, link.ID); err != nil {
		t.Fatal(err)
	}
	e.deleteLink(owner, link.ID)
	logs := pac84Logs(t)
	for _, path := range []string{"/api/scheduling-links/archive", "/api/scheduling-links/archive/" + link.ID.String()} {
		w := e.do(http.MethodGet, path, owner, "")
		if w.Code != http.StatusInternalServerError || strings.TrimSpace(w.Body.String()) != internalError {
			t.Errorf("%s = %d %q; want 500 %s", path, w.Code, w.Body.String(), internalError)
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s Content-Type = %q", path, ct)
		}
	}
	if !strings.Contains(logs.String(), "encode archive") || !strings.Contains(logs.String(), "encode archived link") {
		t.Errorf("encode failures not logged: %s", logs.String())
	}
}
