package api

// PAC-122 phase 2, batch 1 (docs/factory/silent-failures.md §7): scheduling
// links and booking, light rows. A failed roster read is the documented 500,
// never a degraded 200/201 (the PAC-25 C-2 pattern), a failed host-invite
// read is the 500, never a shorter list, and an email skipped because SMTP is
// not configured is said, never passed off as sent.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/outbound"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// --- SF-027: GET /api/book/{slug} --------------------------------------------

func pac122PublicInfo(t *testing.T, slug string) *httptest.ResponseRecorder {
	t.Helper()
	bh := newBookingHandlersWithEngine(openTestDB(t), nil)
	r := chi.NewRouter()
	r.Get("/api/book/{slug}", bh.getLinkInfo)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/book/"+slug, nil))
	return w
}

// assertPAC122Internal500 is the documented 500 with no part of the roster
// in the body, and a log line that names no address.
func assertPAC122Internal500(t *testing.T, what string, w *httptest.ResponseRecorder, reported *[]error, logs interface{ String() string }) {
	t.Helper()
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("%s: status = %d, want 500; body: %s", what, w.Code, w.Body.String())
	}
	exactJSONError(t, w, "internal error")
	if len(*reported) == 0 {
		t.Fatalf("%s: the failure was not reported", what)
	}
	if strings.Contains(logs.String(), "@example.com") {
		t.Fatalf("%s: an address was logged: %s", what, logs.String())
	}
}

func TestPAC122_SF027_PublicLinkRosterFailureIs500(t *testing.T) {
	for _, table := range []string{"scheduling_link_hosts", "users"} {
		t.Run(table+" unreadable", func(t *testing.T) {
			f := newPAC25LinkFixture(t, "pac122-sf027-"+strings.ReplaceAll(table, "_", "")+"-"+uuid.NewString()[:8])
			if w := pac122PublicInfo(t, f.link.Slug); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "-accepted@example.com") {
				t.Fatalf("baseline: %d %s", w.Code, w.Body.String())
			}
			reported, logs := captureServerErrors(t)
			hideTable(t, table)
			assertPAC122Internal500(t, "public link, "+table+" down", pac122PublicInfo(t, f.link.Slug), reported, logs)
		})
	}
}

// --- SF-028: POST /api/book/{slug} confirmation -------------------------------

// pac122Flow books through the real engine and then runs after (nil: nothing),
// so a test can break the roster read between the save and the 201.
type pac122Flow struct {
	*pac82Flow
	after func()
}

func (f *pac122Flow) ConfirmBooking(ctx context.Context, link *storage.SchedulingLink, name, email string, start, end time.Time, notes, key string) (*storage.Booking, error) {
	b, err := f.pac82Flow.ConfirmBooking(ctx, link, name, email, start, end, notes, key)
	if err == nil && f.after != nil {
		f.after()
	}
	return b, err
}

func TestPAC122_SF028_ConfirmationRosterFailureIs500(t *testing.T) {
	// The host rows, or a host's user record, unreadable when the 201 is built.
	for _, table := range []string{"scheduling_link_hosts", "users"} {
		t.Run("fresh save, "+table+" unreadable", func(t *testing.T) {
			e := newPAC82Env(t)
			flow := &pac122Flow{pac82Flow: e.flow}
			bh := newBookingHandlersWithEngine(e.db, flow)
			e.r.Post("/api/book/{slug}/pac122", bh.createBooking)
			owner := e.host("sf028-owner")
			link := e.link(owner, "reusable", nil, []int{30}, false)
			reported, logs := captureServerErrors(t)
			flow.after = func() { hideTable(t, table) }
			body := `{"name":"Booker","email":"sf028-booker@example.com","start":"` + pac82At(10, 0).Format(time.RFC3339) + `","duration":30}`
			req := httptest.NewRequest(http.MethodPost, "/api/book/"+link.Slug+"/pac122", strings.NewReader(body))
			req.RemoteAddr = "10.122.28.1:4000"
			w := httptest.NewRecorder()
			e.r.ServeHTTP(w, req)
			assertPAC122Internal500(t, "fresh booking, "+table+" down after the save", w, reported, logs)
			// The booking stands: the 500 does not prove nothing was saved.
			var n int
			if err := e.db.QueryRow(`SELECT COUNT(*) FROM bookings WHERE link_id = $1`, link.ID).Scan(&n); err != nil || n != 1 {
				t.Fatalf("bookings on the link = %d (%v), want 1", n, err)
			}
		})
		t.Run("replay, "+table+" unreadable", func(t *testing.T) {
			e := newPAC82Env(t)
			owner := e.host("sf028r-owner")
			link := e.link(owner, "reusable", nil, []int{30}, false)
			q := pac82Req{email: "sf028r-booker@example.com", start: pac82At(11, 0), key: "pac122-sf028-replay-" + e.sfx}
			if w := e.post(link.Slug, q); w.Code != http.StatusCreated {
				t.Fatalf("original: %d %s", w.Code, w.Body.String())
			}
			e.settle()
			reported, logs := captureServerErrors(t)
			hideTable(t, table)
			assertPAC122Internal500(t, "replay, "+table+" down", e.post(link.Slug, q), reported, logs)
		})
	}
}

// --- SF-029: GET /api/scheduling-links -----------------------------------------

func TestPAC122_SF029_ListRosterFailureIs500(t *testing.T) {
	f := newPAC25LinkFixture(t, "pac122-sf029-"+uuid.NewString()[:8])
	_ = f.listEntry(t, f.owner) // baseline: listed with its roster
	reported, logs := captureServerErrors(t)
	hideTable(t, "users")
	req := httptest.NewRequest(http.MethodGet, "/api/scheduling-links", nil)
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, withUser(req, f.owner))
	assertPAC122Internal500(t, "list, host users down", w, reported, logs)
	if strings.Contains(w.Body.String(), f.link.Slug) {
		t.Fatalf("body carries the link: %s", w.Body.String())
	}
}

// --- SF-030: GET /api/scheduling-links/host-invites ------------------------------

func pac122Invites(t *testing.T, f pac25LinkFixture, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, withUser(req, f.pending))
	return w
}

func TestPAC122_SF030_HostInviteReadErrorIs500(t *testing.T) {
	for _, path := range []string{"/api/scheduling-links/host-invites", "/api/scheduling-links/invites"} {
		t.Run("link read"+path, func(t *testing.T) {
			f := newPAC25LinkFixture(t, "pac122-sf030l-"+uuid.NewString()[:8])
			if w := pac122Invites(t, f, path); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), f.link.ID.String()) {
				t.Fatalf("baseline: %d %s", w.Code, w.Body.String())
			}
			reported, logs := captureServerErrors(t)
			storage.InstallLinkReadByID(t, func(id uuid.UUID) error {
				if id == f.link.ID {
					return errors.New("pac122 forced link read failure")
				}
				return nil
			})
			assertPAC122Internal500(t, "invites, link read down", pac122Invites(t, f, path), reported, logs)
		})
	}
	t.Run("owner read", func(t *testing.T) {
		f := newPAC25LinkFixture(t, "pac122-sf030o-"+uuid.NewString()[:8])
		reported, logs := captureServerErrors(t)
		hideTable(t, "users")
		assertPAC122Internal500(t, "invites, owner read down", pac122Invites(t, f, "/api/scheduling-links/host-invites"), reported, logs)
	})
}

// Regression guard: an invite to a deleted link is still omitted, not a 500.
func TestPAC122_SF030_DeletedLinkStillOmitted(t *testing.T) {
	f := newPAC25LinkFixture(t, "pac122-sf030d-"+uuid.NewString()[:8])
	if _, err := openTestDB(t).Exec(`UPDATE scheduling_links SET deleted_at = now(), active = false WHERE id = $1`, f.link.ID); err != nil {
		t.Fatal(err)
	}
	w := pac122Invites(t, f, "/api/scheduling-links/host-invites")
	var got []map[string]any
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &got) != nil || len(got) != 0 {
		t.Fatalf("got %d %s, want 200 []", w.Code, w.Body.String())
	}
}

// --- SF-066: team-invite email with SMTP unset -----------------------------------

func TestPAC122_SF066_InviteEmailSkipIsNotASend(t *testing.T) {
	t.Setenv("SMTP_HOST", "")
	t.Setenv("SMTP_FROM", "")
	sent := 0
	prev := sendMail
	sendMail = func(string, smtp.Auth, string, []string, []byte) error { sent++; return nil }
	t.Cleanup(func() { sendMail = prev })

	err := sendTeamInviteEmail("sf066-invitee@example.com", "Inviter", "Team", "tok")
	if !errors.Is(err, outbound.ErrMailNotConfigured) {
		t.Fatalf("err = %v, want ErrMailNotConfigured (a skip is not a send)", err)
	}
	if sent != 0 {
		t.Fatalf("%d sends with SMTP unset", sent)
	}
	_, logs := captureServerErrors(t)
	reportEmailFailure("team invite", "invite", "inv-122", err)
	out := logs.String()
	if !strings.Contains(out, "WARN: team invite email for invite inv-122 not sent") || strings.Contains(out, "ERROR") {
		t.Fatalf("log %q: want one WARN skip line with the invite id", out)
	}
	if strings.Contains(out, "@example.com") {
		t.Fatalf("an address was logged: %q", out)
	}
}
