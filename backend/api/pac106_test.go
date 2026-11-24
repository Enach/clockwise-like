package api

// PAC-106 (issue #339): POST /api/book/{slug} judged the requested start
// against a link row read before the host calendars were fetched, which can
// take seconds. An owner's PATCH that committed in between left the booking
// stored on a slot the new settings would not offer: the save locked the link
// FOR SHARE but re-checked only `active AND deleted_at IS NULL`.
//
// The race is injected on the PAC-101 seam (afterBookingLinkRead): it runs
// after the request read the link and before any host calendar is read, which
// is exactly the window the issue describes. The PATCH is the owner's real
// request through the production route, so what commits is what an owner's
// edit commits.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/google/uuid"
)

// pac106Book posts a booking of start (30 minutes) on slug through the real
// engine, from a client address of its own: the abuse limit is per client.
func pac106Book(e *pac101Env, slug string, start time.Time, duration int) *httptest.ResponseRecorder {
	body := fmt.Sprintf(`{"name":"Booker","email":"pac106@booker.test","start":"%s","duration":%d}`,
		start.Format(time.RFC3339), duration)
	req := httptest.NewRequest(http.MethodPost, "/api/book/"+slug+"/pac101", strings.NewReader(body))
	n := e.addr.Add(1)
	req.RemoteAddr = fmt.Sprintf("10.106.%d.%d:4000", n/250, n%250+1)
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w
}

// pac106PatchDuringBooking makes the owner's PATCH commit on the seam, once:
// after the booking request read the link, before any host calendar is read.
func pac106PatchDuringBooking(t *testing.T, e *pac101Env, owner, link uuid.UUID, body string) {
	t.Helper()
	patched := false
	afterBookingLinkRead = func() {
		if patched {
			return
		}
		patched = true
		w := e.do(http.MethodPatch, "/api/scheduling-links/"+link.String(), owner, body)
		if w.Code != http.StatusOK {
			t.Errorf("owner PATCH %s during the booking: %d %s", body, w.Code, w.Body.String())
		}
	}
	t.Cleanup(func() {
		afterBookingLinkRead = nil
		if !patched {
			t.Error("the PATCH never ran: the seam did not fire")
		}
	})
}

// pac106Specific makes the link report the rule's reason with its 409s
// (refusal_message = specific, PAC-72 D-5), so a test can read which refusal
// the rule gave.
func pac106Specific(t *testing.T, e *pac101Env, owner, link uuid.UUID) {
	t.Helper()
	w := e.do(http.MethodPatch, "/api/scheduling-links/"+link.String(), owner, `{"refusal_message":"specific"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("set refusal_message=specific: %d %s", w.Code, w.Body.String())
	}
}

// pac106Refusal decodes a specific 409 body.
func pac106Refusal(t *testing.T, w *httptest.ResponseRecorder) gen.BookingSlotUnavailableError {
	t.Helper()
	var got gen.BookingSlotUnavailableError
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode refusal: %v; body %s", err, w.Body.String())
	}
	return got
}

// pac106Settled waits for any completion unit the request may have started,
// so "nothing was written" is asserted after everything that could write.
func pac106Settled(t *testing.T, e *pac101Env) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e.group.Wait(ctx)
}

// A PATCH that commits between the request's read of the link and the save,
// and that makes the slot unofferable, is refused with the rule's own 409 and
// writes nothing: no booking row, no host event, no completion unit (so no
// confirmation email).
func TestPAC106_APatchThatInvalidatesTheSlotMidRequestRefusesAndWritesNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		patch  string
		reason gen.BookingRefusalReason
		text   gen.BookingSlotUnavailableErrorError
	}{
		{"horizon shrunk", `{"booking_horizon_days":1}`, gen.BeyondHorizon, gen.SlotIsBeyondTheLinksBookingHorizon},
		{"hours moved", `{"window_start_time":"13:00"}`, gen.OutsideLinkHours, gen.SlotIsOutsideTheLinksDaysOrHours},
		{"day dropped", `{"days_of_week":[0]}`, gen.OutsideLinkHours, gen.SlotIsOutsideTheLinksDaysOrHours},
		{"duration withdrawn", `{"duration_options":[60]}`, gen.OutsideLinkHours, gen.SlotIsOutsideTheLinksDaysOrHours},
		{"notice raised", `{"min_notice_minutes":525600}`, gen.TooSoon, gen.SlotIsTooSoon},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newPAC101Env(t)
			owner := e.user("pac106-" + uuid.NewString()[:8] + "@example.com")
			link := createSchedulingLinkRecord(t, owner, "pac106-"+uuid.NewString()[:8], "PAC-106")
			pac106Specific(t, e, owner, link.ID)
			pac106PatchDuringBooking(t, e, owner, link.ID, tc.patch)

			w := e.book(link.Slug, 0, "pac106@booker.test")

			if w.Code != http.StatusConflict {
				t.Fatalf("booking racing %s: %d %s; want 409", tc.patch, w.Code, w.Body.String())
			}
			got := pac106Refusal(t, w)
			if got.Error != tc.text || got.Reason == nil || *got.Reason != tc.reason {
				t.Errorf("refusal %+v; want error %q reason %q", got, tc.text, tc.reason)
			}
			if e.lists.Load() == 0 {
				t.Error("no host calendar was read: the PATCH did not land in the window the issue describes")
			}
			pac106Settled(t, e)
			if n := e.bookingCount(link.ID); n != 0 {
				t.Errorf("%d bookings stored on a slot the new settings do not offer", n)
			}
			if c := e.creates.Load(); c != 0 {
				t.Errorf("%d host events created for a refused booking", c)
			}
			if p := e.group.Pending(); len(p) != 0 {
				t.Errorf("completion units started for a refused booking: %v", p)
			}
		})
	}
}

// The refusal is the one the rule already gives for an unofferable slot, not
// a new answer: the 409 of a booking refused at the save is byte for byte the
// 409 of the same booking refused by the first pass of the same rule.
func TestPAC106_TheRefusalIsTheRulesOwnAnswer(t *testing.T) {
	e := newPAC101Env(t)
	owner := e.user("pac106-same-answer@example.com")
	link := createSchedulingLinkRecord(t, owner, "pac106-same-answer", "PAC-106 same answer")
	pac106Specific(t, e, owner, link.ID)

	// The first pass: a start before the link's window, no race at all.
	slot := e.offer[0].Start
	outside := time.Date(slot.Year(), slot.Month(), slot.Day(), 7, 0, 0, 0, time.UTC)
	byTheRule := pac106Book(e, link.Slug, outside, 30)
	if byTheRule.Code != http.StatusConflict {
		t.Fatalf("a start outside the link's hours: %d %s; want 409", byTheRule.Code, byTheRule.Body.String())
	}

	// The save: the same cause, reached only because the window moved while
	// the host calendars were read.
	pac106PatchDuringBooking(t, e, owner, link.ID, `{"window_start_time":"13:00"}`)
	atTheSave := e.book(link.Slug, 0, "pac106-same-answer@booker.test")

	if atTheSave.Code != byTheRule.Code || atTheSave.Body.String() != byTheRule.Body.String() {
		t.Errorf("refused at the save: %d %q; the rule's own refusal is %d %q",
			atTheSave.Code, atTheSave.Body.String(), byTheRule.Code, byTheRule.Body.String())
	}
	pac106Settled(t, e)
	if n := e.bookingCount(link.ID); n != 0 {
		t.Errorf("%d bookings stored", n)
	}
}

// A link that chose the generic refusal still gets the generic body: the
// save-time refusal introduces no new status, reason or body.
func TestPAC106_AGenericLinkKeepsTheGenericBody(t *testing.T) {
	e := newPAC101Env(t)
	owner := e.user("pac106-generic@example.com")
	link := createSchedulingLinkRecord(t, owner, "pac106-generic", "PAC-106 generic")
	pac106PatchDuringBooking(t, e, owner, link.ID, `{"booking_horizon_days":1}`)
	w := e.book(link.Slug, 0, "pac106-generic@booker.test")
	if w.Code != http.StatusConflict || strings.TrimSpace(w.Body.String()) != `{"error":"`+errSlotUnavailable+`"}` {
		t.Fatalf("generic refusal: %d %s; want 409 {\"error\":%q}", w.Code, w.Body.String(), errSlotUnavailable)
	}
	pac106Settled(t, e)
	if n := e.bookingCount(link.ID); n != 0 {
		t.Errorf("%d bookings stored", n)
	}
}

// The other half of the fix, which keeps it from becoming a
// correctness-for-availability trade: a PATCH that commits in the same window
// and leaves the slot offerable still lets the booking through, saved and
// completed.
func TestPAC106_APatchThatKeepsTheSlotOfferableStillBooks(t *testing.T) {
	for _, tc := range []struct{ name, patch string }{
		{"cosmetic", `{"title":"PAC-106 renamed mid-request"}`},
		{"widened", `{"window_start_time":"08:00","window_end_time":"18:00","booking_horizon_days":365}`},
		{"durations extended", `{"duration_options":[30,60]}`},
		{"notice dropped", `{"min_notice_minutes":0}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newPAC101Env(t)
			owner := e.user("pac106-ok-" + uuid.NewString()[:8] + "@example.com")
			link := createSchedulingLinkRecord(t, owner, "pac106-ok-"+uuid.NewString()[:8], "PAC-106 still offerable")
			pac106PatchDuringBooking(t, e, owner, link.ID, tc.patch)

			w := e.book(link.Slug, 0, "pac106-ok@booker.test")

			if w.Code != http.StatusCreated {
				t.Fatalf("booking racing %s: %d %s; want 201", tc.patch, w.Code, w.Body.String())
			}
			pac106Settled(t, e)
			if n := e.bookingCount(link.ID); n != 1 {
				t.Errorf("%d bookings stored, want 1", n)
			}
			if c := e.creates.Load(); c != 1 {
				t.Errorf("%d host events created, want 1", c)
			}
		})
	}
}
