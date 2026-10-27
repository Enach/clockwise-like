package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// PAC-40: the scheduling-link lifecycle. Deleted and paused are different
// states (API-050), the canonical spelling wins over its alias on both verbs
// (API-049), uses_count counts every booking row whatever its status (API-051),
// fetching one link is limited to its owner and accepted co-hosts (API-006),
// and public booking create keeps every answer once its dead re-checks are gone
// (API-073). Spec docs/specs/PAC-40.md, contract contracts/features/PAC-40.yaml.

// dbBookingFlow writes the booking row the way the real engine does, without
// any calendar or email side effect, so the use count can be observed.
type dbBookingFlow struct{}

func (dbBookingFlow) CollectiveSlots(context.Context, *storage.SchedulingLink, time.Time, int) ([]engine.AvailableSlot, error) {
	return []engine.AvailableSlot{}, nil
}

func (dbBookingFlow) ConfirmBooking(_ context.Context, link *storage.SchedulingLink, name, email string, start, end time.Time, notes string) (*storage.Booking, error) {
	return storage.CreateBooking(sharedTestDB, &storage.Booking{
		LinkID: link.ID, BookerName: name, BookerEmail: email, StartTime: start, EndTime: end, Notes: notes,
	})
}

// pac40Routes mounts the link routes and the public booking routes on one mux.
func pac40Routes(t *testing.T) *chi.Mux {
	t.Helper()
	r := setupSchedulingLinkRoutes(t)
	b := newBookingHandlersWithEngine(openTestDB(t), dbBookingFlow{})
	r.Get("/api/book/{slug}", b.getLinkInfo)
	r.Get("/api/book/{slug}/slots", b.getSlots)
	r.Post("/api/book/{slug}", b.createBooking)
	return r
}

func pac40Do(r http.Handler, method, path, body string, user *uuid.UUID) *httptest.ResponseRecorder {
	var rd *bytes.Buffer
	if body == "" {
		rd = &bytes.Buffer{}
	} else {
		rd = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(method, path, rd)
	if user != nil {
		req = withUser(req, *user)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func pac40Body(w *httptest.ResponseRecorder) string { return strings.TrimSpace(w.Body.String()) }

func pac40Expect(t *testing.T, what string, w *httptest.ResponseRecorder, status int, body string) {
	t.Helper()
	if w.Code != status || pac40Body(w) != body {
		t.Fatalf("%s: got %d %s, want %d %s", what, w.Code, pac40Body(w), status, body)
	}
}

func pac40Delete(t *testing.T, r http.Handler, owner uuid.UUID, linkID uuid.UUID) {
	t.Helper()
	w := pac40Do(r, http.MethodDelete, "/api/scheduling-links/"+linkID.String(), "", &owner)
	if w.Code != http.StatusNoContent {
		t.Fatalf("owner delete: got %d %s, want 204", w.Code, pac40Body(w))
	}
}

func pac40BookingCount(t *testing.T, linkID uuid.UUID) int {
	t.Helper()
	var n int
	if err := sharedTestDB.QueryRow(`SELECT COUNT(*) FROM bookings WHERE link_id = $1`, linkID).Scan(&n); err != nil {
		t.Fatalf("count bookings: %v", err)
	}
	return n
}

func pac40Book(t *testing.T, linkID uuid.UUID, start time.Time, status string) uuid.UUID {
	t.Helper()
	b, err := storage.CreateBooking(sharedTestDB, &storage.Booking{
		LinkID: linkID, BookerName: "Booker", BookerEmail: "booker@example.com",
		StartTime: start, EndTime: start.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}
	if status != "" {
		if _, err := sharedTestDB.Exec(`UPDATE bookings SET status = $1 WHERE id = $2`, status, b.ID); err != nil {
			t.Fatalf("set booking status: %v", err)
		}
	}
	return b.ID
}

func pac40SetStatus(t *testing.T, bookingID uuid.UUID, status string) {
	t.Helper()
	if _, err := sharedTestDB.Exec(`UPDATE bookings SET status = $1 WHERE id = $2`, status, bookingID); err != nil {
		t.Fatalf("set booking status: %v", err)
	}
}

func pac40Link(t *testing.T, w *httptest.ResponseRecorder) schedulingLinkDTO {
	t.Helper()
	var dto schedulingLinkDTO
	if err := json.Unmarshal(w.Body.Bytes(), &dto); err != nil {
		t.Fatalf("decode link: %v; body %s", err, w.Body.String())
	}
	return dto
}

func pac40ListIDs(t *testing.T, r http.Handler, user uuid.UUID) (owned, shared []string) {
	t.Helper()
	w := pac40Do(r, http.MethodGet, "/api/scheduling-links", "", &user)
	if w.Code != http.StatusOK {
		t.Fatalf("list: got %d %s", w.Code, pac40Body(w))
	}
	got := decodeOwnedShared(t, w.Body)
	for _, l := range got["owned"] {
		owned = append(owned, l.ID)
	}
	for _, l := range got["shared"] {
		shared = append(shared, l.ID)
	}
	return owned, shared
}

func pac40Has(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// AC-1
func TestPAC40ListOmitsDeletedLinks(t *testing.T) {
	r := pac40Routes(t)
	owner := createTestUser(t, "pac40-list-owner@example.com")
	cohost := createTestUser(t, "pac40-list-cohost@example.com")
	deleted := createSchedulingLinkRecord(t, owner, "pac40-list-deleted", "Deleted")
	live := createSchedulingLinkRecord(t, owner, "pac40-list-live", "Live")
	if _, err := storage.AddLinkHost(sharedTestDB, deleted.ID, cohost, "accepted"); err != nil {
		t.Fatal(err)
	}
	if _, shared := pac40ListIDs(t, r, cohost); !pac40Has(shared, deleted.ID.String()) {
		t.Fatalf("precondition: co-host should see the live link in shared, got %v", shared)
	}
	pac40Delete(t, r, owner, deleted.ID)

	owned, shared := pac40ListIDs(t, r, owner)
	if pac40Has(owned, deleted.ID.String()) || pac40Has(shared, deleted.ID.String()) || !pac40Has(owned, live.ID.String()) {
		t.Fatalf("owner list owned=%v shared=%v: want only the live link %s", owned, shared, live.ID)
	}
	owned, shared = pac40ListIDs(t, r, cohost)
	if pac40Has(owned, deleted.ID.String()) || pac40Has(shared, deleted.ID.String()) {
		t.Fatalf("co-host list owned=%v shared=%v: deleted link %s must be absent", owned, shared, deleted.ID)
	}
}

// AC-2, AC-3, AC-6
func TestPAC40DeletedLinkIsNotFoundForItsOwnerAndCoHost(t *testing.T) {
	r := pac40Routes(t)
	owner := createTestUser(t, "pac40-del-owner@example.com")
	cohost := createTestUser(t, "pac40-del-cohost@example.com")
	invitee := createTestUser(t, "pac40-del-newinvitee@example.com")
	link := createSchedulingLinkRecord(t, owner, "pac40-del", "Deleted")
	if _, err := storage.AddLinkHost(sharedTestDB, link.ID, cohost, "accepted"); err != nil {
		t.Fatal(err)
	}
	pac40Book(t, link.ID, futureBookingStart(), "")
	pac40Delete(t, r, owner, link.ID)
	hostsBefore := hostRowsSnapshot(t, link.ID)
	base := "/api/scheduling-links/" + link.ID.String()
	nf := `{"error":"not found"}`

	// AC-2
	pac40Expect(t, "owner GET", pac40Do(r, http.MethodGet, base, "", &owner), 404, nf)
	pac40Expect(t, "co-host GET", pac40Do(r, http.MethodGet, base, "", &cohost), 404, nf)

	// AC-3: any body, including one that resumes it, and a malformed one (C-4).
	pac40Expect(t, "owner PATCH active:true", pac40Do(r, http.MethodPatch, base, `{"active":true}`, &owner), 404, nf)
	pac40Expect(t, "owner PATCH title", pac40Do(r, http.MethodPatch, base, `{"title":"Back"}`, &owner), 404, nf)
	pac40Expect(t, "owner PATCH malformed", pac40Do(r, http.MethodPatch, base, `{`, &owner), 404, nf)

	// AC-6
	pac40Expect(t, "owner bookings", pac40Do(r, http.MethodGet, base+"/bookings", "", &owner), 404, nf)
	pac40Expect(t, "owner invite", pac40Do(r, http.MethodPost, base+"/hosts", `{"email":"pac40-del-newinvitee@example.com"}`, &owner), 404, nf)
	pac40Expect(t, "owner invite malformed", pac40Do(r, http.MethodPost, base+"/hosts", `{`, &owner), 404, nf)
	pac40Expect(t, "owner delete again", pac40Do(r, http.MethodDelete, base, "", &owner), 404, nf)

	// Afterwards it is still deleted: AC-1, AC-2 and AC-4 still hold.
	if owned, _ := pac40ListIDs(t, r, owner); pac40Has(owned, link.ID.String()) {
		t.Fatalf("after PATCH the deleted link is listed again: %v", owned)
	}
	pac40Expect(t, "owner GET after PATCH", pac40Do(r, http.MethodGet, base, "", &owner), 404, nf)
	pac40Expect(t, "public info after PATCH", pac40Do(r, http.MethodGet, "/api/book/"+link.Slug, "", nil), 404, nf)
	stored, err := storage.GetSchedulingLinkBySlug(sharedTestDB, link.Slug)
	if err != nil || stored != nil {
		t.Fatalf("public lookup after PATCH = %+v, %v; want none", stored, err)
	}
	var title string
	if err := sharedTestDB.QueryRow(`SELECT title FROM scheduling_links WHERE id = $1`, link.ID).Scan(&title); err != nil || title != "Deleted" {
		t.Fatalf("stored title = %q, %v; the PATCH must not have written", title, err)
	}
	if after := hostRowsSnapshot(t, link.ID); after != hostsBefore {
		t.Fatalf("host rows changed:\nbefore:\n%s\nafter:\n%s", hostsBefore, after)
	}
	if statuses := hostStatuses(t, link.ID); statuses[invitee] != "" {
		t.Fatalf("invite on a deleted link wrote a host row: %v", statuses)
	}
	if n := pac40BookingCount(t, link.ID); n != 1 {
		t.Fatalf("booking rows = %d, want 1 (nothing destroyed)", n)
	}
}

// AC-6a: a stranger gets on a deleted link exactly what they get on an id that never existed.
func TestPAC40DeletedLinkAnswersStrangerAsUnknownID(t *testing.T) {
	r := pac40Routes(t)
	owner := createTestUser(t, "pac40-6a-owner@example.com")
	stranger := createTestUser(t, "pac40-6a-stranger@example.com")
	createTestUser(t, "pac40-6a-target@example.com")
	link := createSchedulingLinkRecord(t, owner, "pac40-6a", "Deleted 6a")
	pac40Delete(t, r, owner, link.ID)
	hostsBefore := hostRowsSnapshot(t, link.ID)
	unknown := uuid.New().String()

	cases := []struct{ method, suffix, body string }{
		{http.MethodGet, "", ""},
		{http.MethodPatch, "", `{"active":true}`},
		{http.MethodPatch, "", `{`},
		{http.MethodDelete, "", ""},
		{http.MethodGet, "/bookings", ""},
		{http.MethodPost, "/hosts", `{"email":"pac40-6a-target@example.com"}`},
	}
	for _, c := range cases {
		want := pac40Do(r, c.method, "/api/scheduling-links/"+unknown+c.suffix, c.body, &stranger)
		got := pac40Do(r, c.method, "/api/scheduling-links/"+link.ID.String()+c.suffix, c.body, &stranger)
		if got.Code != want.Code || pac40Body(got) != pac40Body(want) {
			t.Errorf("stranger %s %s %q: deleted link answers %d %s, unknown id answers %d %s",
				c.method, c.suffix, c.body, got.Code, pac40Body(got), want.Code, pac40Body(want))
		}
	}
	if after := hostRowsSnapshot(t, link.ID); after != hostsBefore {
		t.Fatalf("host rows changed:\nbefore:\n%s\nafter:\n%s", hostsBefore, after)
	}
	var active bool
	if err := sharedTestDB.QueryRow(`SELECT active FROM scheduling_links WHERE id = $1`, link.ID).Scan(&active); err != nil || active {
		t.Fatalf("active = %v, %v; want still false", active, err)
	}
}

// AC-4
func TestPAC40DeletedLinkPublicPathsAreNotFound(t *testing.T) {
	r := pac40Routes(t)
	owner := createTestUser(t, "pac40-pub-owner@example.com")
	link := createSchedulingLinkRecord(t, owner, "pac40-pub", "Public Deleted")
	pac40Delete(t, r, owner, link.ID)
	nf := `{"error":"not found"}`
	start := futureBookingStart().Format(time.RFC3339)

	pac40Expect(t, "info", pac40Do(r, http.MethodGet, "/api/book/"+link.Slug, "", nil), 404, nf)
	pac40Expect(t, "slots", pac40Do(r, http.MethodGet, "/api/book/"+link.Slug+"/slots", "", nil), 404, nf)
	pac40Expect(t, "book", pac40Do(r, http.MethodPost, "/api/book/"+link.Slug,
		fmt.Sprintf(`{"name":"A","email":"a@example.com","start":"%s","duration_minutes":30}`, start), nil), 404, nf)
	if n := pac40BookingCount(t, link.ID); n != 0 {
		t.Fatalf("booking rows = %d, want 0", n)
	}
}

// AC-5
func TestPAC40DeletedLinkInvitesAcceptDeclineAndLeave(t *testing.T) {
	r := pac40Routes(t)
	owner := createTestUser(t, "pac40-inv-owner@example.com")
	pending := createTestUser(t, "pac40-inv-pending@example.com")
	accepted := createTestUser(t, "pac40-inv-accepted@example.com")
	link := createSchedulingLinkRecord(t, owner, "pac40-inv", "Invites Deleted")
	if _, err := storage.AddLinkHost(sharedTestDB, link.ID, pending, "pending"); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.AddLinkHost(sharedTestDB, link.ID, accepted, "accepted"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/api/scheduling-links/host-invites", "/api/scheduling-links/invites"} {
		if !strings.Contains(pac40Do(r, http.MethodGet, p, "", &pending).Body.String(), link.ID.String()) {
			t.Fatalf("precondition: %s should list the invite to the live link", p)
		}
	}
	pac40Delete(t, r, owner, link.ID)
	before := hostRowsSnapshot(t, link.ID)
	nf := `{"error":"not found"}`

	for _, p := range []string{"/api/scheduling-links/host-invites", "/api/scheduling-links/invites"} {
		w := pac40Do(r, http.MethodGet, p, "", &pending)
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), link.ID.String()) {
			t.Fatalf("%s after delete: %d %s, want 200 without the deleted link", p, w.Code, pac40Body(w))
		}
	}
	id := link.ID.String()
	for _, p := range []string{
		"/api/scheduling-links/host-invites/" + id + "/accept",
		"/api/scheduling-links/host-invites/" + id + "/decline",
		"/api/scheduling-links/" + id + "/accept",
		"/api/scheduling-links/" + id + "/decline",
	} {
		pac40Expect(t, "pending POST "+p, pac40Do(r, http.MethodPost, p, "", &pending), 404, nf)
		pac40Expect(t, "accepted POST "+p, pac40Do(r, http.MethodPost, p, "", &accepted), 404, nf)
	}
	leave := "/api/scheduling-links/" + id + "/leave"
	pac40Expect(t, "accepted co-host leave", pac40Do(r, http.MethodPost, leave, "", &accepted), 404, nf)
	pac40Expect(t, "pending invitee leave", pac40Do(r, http.MethodPost, leave, "", &pending), 404, nf)
	pac40Expect(t, "owner leave", pac40Do(r, http.MethodPost, leave, "", &owner), 404, nf)
	if after := hostRowsSnapshot(t, link.ID); after != before {
		t.Fatalf("host rows changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if st := hostStatuses(t, link.ID); st[pending] != "pending" || st[accepted] != "accepted" {
		t.Fatalf("host statuses = %v, want pending and accepted unchanged", st)
	}
}

// AC-8
func TestPAC40DeletedLinkSlugStaysReserved(t *testing.T) {
	r := pac40Routes(t)
	owner := createTestUser(t, "pac40-slug-owner@example.com")
	other := createTestUser(t, "pac40-slug-other@example.com")
	link := createSchedulingLinkRecord(t, owner, "pac40-slug-reserved", "Reserved")
	mine := createSchedulingLinkRecord(t, other, "pac40-slug-mine", "Mine")
	pac40Delete(t, r, owner, link.ID)
	taken := `{"error":"slug already exists"}`

	pac40Expect(t, "create on deleted slug", pac40Do(r, http.MethodPost, "/api/scheduling-links",
		`{"title":"Copy","slug":"pac40-slug-reserved"}`, &other), 409, taken)
	pac40Expect(t, "patch to deleted slug", pac40Do(r, http.MethodPatch, "/api/scheduling-links/"+mine.ID.String(),
		`{"slug":"pac40-slug-reserved"}`, &other), 409, taken)
}

// AC-11
func TestPAC40PausedLinkStaysVisibleAndResumes(t *testing.T) {
	r := pac40Routes(t)
	owner := createTestUser(t, "pac40-pause-owner@example.com")
	link := createSchedulingLinkRecord(t, owner, "pac40-pause", "Paused")
	base := "/api/scheduling-links/" + link.ID.String()

	if w := pac40Do(r, http.MethodPatch, base, `{"active":false}`, &owner); w.Code != 200 || pac40Link(t, w).Active {
		t.Fatalf("pause: %d %s", w.Code, pac40Body(w))
	}
	if owned, _ := pac40ListIDs(t, r, owner); !pac40Has(owned, link.ID.String()) {
		t.Fatalf("paused link not listed: %v", owned)
	}
	if w := pac40Do(r, http.MethodGet, base, "", &owner); w.Code != 200 || pac40Link(t, w).Active {
		t.Fatalf("fetch paused: %d %s, want 200 active:false", w.Code, pac40Body(w))
	}
	pac40Expect(t, "public info while paused", pac40Do(r, http.MethodGet, "/api/book/"+link.Slug, "", nil), 404, `{"error":"not found"}`)

	if w := pac40Do(r, http.MethodPatch, base, `{"active":true}`, &owner); w.Code != 200 || !pac40Link(t, w).Active {
		t.Fatalf("resume: %d %s", w.Code, pac40Body(w))
	}
	if w := pac40Do(r, http.MethodGet, "/api/book/"+link.Slug, "", nil); w.Code != 200 {
		t.Fatalf("public info after resume: %d %s", w.Code, pac40Body(w))
	}
	start := futureBookingStart().Format(time.RFC3339)
	if w := pac40Do(r, http.MethodPost, "/api/book/"+link.Slug,
		fmt.Sprintf(`{"name":"A","email":"a@example.com","start":"%s","duration_minutes":30}`, start), nil); w.Code != 201 {
		t.Fatalf("book after resume: %d %s", w.Code, pac40Body(w))
	}
}

// AC-13 (POST) and AC-14 (PATCH): the canonical spelling wins on both verbs.
func TestPAC40CreatePrefersCanonicalSpellings(t *testing.T) {
	r := pac40Routes(t)
	owner := createTestUser(t, "pac40-alias-create@example.com")
	w := pac40Do(r, http.MethodPost, "/api/scheduling-links",
		`{"title":"Both","slug":"pac40-alias-create","duration_options":[30],"durations":[45],"days_of_week":[1],"days":["fri"]}`, &owner)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, pac40Body(w))
	}
	got := pac40Link(t, w)
	if fmt.Sprint(got.Durations) != "[30]" || fmt.Sprint(got.Days) != "[mon]" {
		t.Fatalf("create stored durations=%v days=%v, want [30] [mon]", got.Durations, got.Days)
	}
}

func TestPAC40PatchPrefersCanonicalSpellings(t *testing.T) {
	r := pac40Routes(t)
	owner := createTestUser(t, "pac40-alias-patch@example.com")
	link := createSchedulingLinkRecord(t, owner, "pac40-alias-patch", "Patch Both")
	w := pac40Do(r, http.MethodPatch, "/api/scheduling-links/"+link.ID.String(),
		`{"duration_options":[30],"durations":[45],"days_of_week":[1],"days":["fri"]}`, &owner)
	if w.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", w.Code, pac40Body(w))
	}
	got := pac40Link(t, w)
	if fmt.Sprint(got.Durations) != "[30]" || fmt.Sprint(got.Days) != "[mon]" {
		t.Fatalf("patch stored durations=%v days=%v, want [30] [mon] (as create)", got.Durations, got.Days)
	}
}

// AC-14a on both verbs.
func TestPAC40AliasNullAndEmptyCanonicalOnBothVerbs(t *testing.T) {
	r := pac40Routes(t)
	owner := createTestUser(t, "pac40-alias-null@example.com")

	// null canonical: the alias is used.
	w := pac40Do(r, http.MethodPost, "/api/scheduling-links",
		`{"title":"Null","slug":"pac40-alias-null-create","duration_options":null,"durations":[45],"days_of_week":null,"days":["fri"]}`, &owner)
	if w.Code != http.StatusCreated {
		t.Fatalf("create null: %d %s", w.Code, pac40Body(w))
	}
	if got := pac40Link(t, w); fmt.Sprint(got.Durations) != "[45]" || fmt.Sprint(got.Days) != "[fri]" {
		t.Fatalf("create null canonical: durations=%v days=%v, want [45] [fri]", got.Durations, got.Days)
	}
	link := createSchedulingLinkRecord(t, owner, "pac40-alias-null-patch", "Null Patch")
	base := "/api/scheduling-links/" + link.ID.String()
	w = pac40Do(r, http.MethodPatch, base, `{"duration_options":null,"durations":[45],"days_of_week":null,"days":["fri"]}`, &owner)
	if w.Code != http.StatusOK {
		t.Fatalf("patch null: %d %s", w.Code, pac40Body(w))
	}
	if got := pac40Link(t, w); fmt.Sprint(got.Durations) != "[45]" || fmt.Sprint(got.Days) != "[fri]" {
		t.Fatalf("patch null canonical: durations=%v days=%v, want [45] [fri]", got.Durations, got.Days)
	}

	// empty canonical: carried, so rejected for having no values, on both verbs.
	noDur := `{"error":"at least one duration is required"}`
	noDay := `{"error":"at least one day is required"}`
	pac40Expect(t, "create empty durations", pac40Do(r, http.MethodPost, "/api/scheduling-links",
		`{"title":"Empty","slug":"pac40-alias-empty-c1","duration_options":[],"durations":[45]}`, &owner), 422, noDur)
	pac40Expect(t, "create empty days", pac40Do(r, http.MethodPost, "/api/scheduling-links",
		`{"title":"Empty","slug":"pac40-alias-empty-c2","days_of_week":[],"days":["fri"]}`, &owner), 422, noDay)
	before := pac40Link(t, pac40Do(r, http.MethodGet, base, "", &owner))
	pac40Expect(t, "patch empty durations", pac40Do(r, http.MethodPatch, base, `{"duration_options":[],"durations":[60]}`, &owner), 422, noDur)
	pac40Expect(t, "patch empty days", pac40Do(r, http.MethodPatch, base, `{"days_of_week":[],"days":["mon"]}`, &owner), 422, noDay)
	after := pac40Link(t, pac40Do(r, http.MethodGet, base, "", &owner))
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatalf("rejected PATCH changed the link:\nbefore %+v\nafter  %+v", before, after)
	}
}

// Spec section 6 / D-10: the window aliases keep today's answers on both verbs.
func TestPAC40WindowAliasesKeepTodaysBehaviour(t *testing.T) {
	r := pac40Routes(t)
	owner := createTestUser(t, "pac40-window@example.com")
	w := pac40Do(r, http.MethodPost, "/api/scheduling-links",
		`{"title":"Win","slug":"pac40-window-create","window_start_time":"","window_start":"10:00","window_end_time":"","window_end":"16:00"}`, &owner)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, pac40Body(w))
	}
	if got := pac40Link(t, w); got.WindowStart != "10:00:00" || got.WindowEnd != "16:00:00" {
		t.Fatalf("create window = %s-%s, want 10:00:00-16:00:00", got.WindowStart, got.WindowEnd)
	}
	link := createSchedulingLinkRecord(t, owner, "pac40-window-patch", "Win Patch")
	pac40Expect(t, "patch empty canonical window", pac40Do(r, http.MethodPatch, "/api/scheduling-links/"+link.ID.String(),
		`{"window_start_time":"","window_start":"10:00","window_end_time":"","window_end":"16:00"}`, &owner),
		422, `{"error":"window times must use HH:MM"}`)
}

func pac40CreateUsageLink(t *testing.T, r http.Handler, owner uuid.UUID, slug, usage string, maxUses int) schedulingLinkDTO {
	t.Helper()
	body := fmt.Sprintf(`{"title":"Uses","slug":%q,"usage_type":%q,"days_of_week":[0,1,2,3,4,5,6],"window_start_time":"00:00","window_end_time":"23:59"`, slug, usage)
	if maxUses > 0 {
		body += fmt.Sprintf(`,"max_uses":%d`, maxUses)
	}
	w := pac40Do(r, http.MethodPost, "/api/scheduling-links", body+"}", &owner)
	if w.Code != http.StatusCreated {
		t.Fatalf("create %s: %d %s", slug, w.Code, pac40Body(w))
	}
	return pac40Link(t, w)
}

func pac40UsesCount(t *testing.T, r http.Handler, owner uuid.UUID, id string) int {
	t.Helper()
	w := pac40Do(r, http.MethodGet, "/api/scheduling-links/"+id, "", &owner)
	if w.Code != http.StatusOK {
		t.Fatalf("owner GET: %d %s", w.Code, pac40Body(w))
	}
	return pac40Link(t, w).UsesCount
}

func pac40ExpectGone(t *testing.T, r http.Handler, slug string, start time.Time) {
	t.Helper()
	gone := `{"error":"link is no longer accepting bookings"}`
	pac40Expect(t, "info", pac40Do(r, http.MethodGet, "/api/book/"+slug, "", nil), 410, gone)
	pac40Expect(t, "slots", pac40Do(r, http.MethodGet, "/api/book/"+slug+"/slots", "", nil), 410, gone)
	pac40Expect(t, "book", pac40Do(r, http.MethodPost, "/api/book/"+slug,
		fmt.Sprintf(`{"name":"A","email":"a@example.com","start":"%s","duration_minutes":30}`, start.Format(time.RFC3339)), nil), 410, gone)
}

func pac40BookOK(t *testing.T, r http.Handler, slug string, start time.Time) *httptest.ResponseRecorder {
	t.Helper()
	return pac40Do(r, http.MethodPost, "/api/book/"+slug,
		fmt.Sprintf(`{"name":"A","email":"a@example.com","start":"%s","duration_minutes":30}`, start.Format(time.RFC3339)), nil)
}

// AC-15
func TestPAC40CancelledBookingStillUsesSingleUseLink(t *testing.T) {
	r := pac40Routes(t)
	owner := createTestUser(t, "pac40-uses-single@example.com")
	link := pac40CreateUsageLink(t, r, owner, "pac40-uses-single", "single_use", 0)
	start := futureBookingStart()
	w := pac40BookOK(t, r, link.Slug, start)
	if w.Code != http.StatusCreated {
		t.Fatalf("first booking: %d %s", w.Code, pac40Body(w))
	}
	var id uuid.UUID
	if err := sharedTestDB.QueryRow(`SELECT id FROM bookings WHERE link_id = $1`, link.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	pac40SetStatus(t, id, "cancelled")
	pac40ExpectGone(t, r, link.Slug, start.Add(2*time.Hour))
	if n := pac40UsesCount(t, r, owner, link.ID); n != 1 {
		t.Fatalf("uses_count = %d, want 1", n)
	}
	if n := pac40BookingCount(t, uuid.MustParse(link.ID)); n != 1 {
		t.Fatalf("booking rows = %d, want 1", n)
	}
}

// AC-16
func TestPAC40CancelledBookingStillUsesLimitedLink(t *testing.T) {
	r := pac40Routes(t)
	owner := createTestUser(t, "pac40-uses-limited@example.com")
	link := pac40CreateUsageLink(t, r, owner, "pac40-uses-limited", "recurring", 2)
	start := futureBookingStart()
	for i := 0; i < 2; i++ {
		if w := pac40BookOK(t, r, link.Slug, start.Add(time.Duration(i)*time.Hour)); w.Code != http.StatusCreated {
			t.Fatalf("booking %d: %d %s", i, w.Code, pac40Body(w))
		}
	}
	if _, err := sharedTestDB.Exec(`UPDATE bookings SET status = 'cancelled' WHERE id = (SELECT id FROM bookings WHERE link_id = $1 ORDER BY start_time LIMIT 1)`, link.ID); err != nil {
		t.Fatal(err)
	}
	pac40ExpectGone(t, r, link.Slug, start.Add(4*time.Hour))
	if n := pac40UsesCount(t, r, owner, link.ID); n != 2 {
		t.Fatalf("uses_count = %d, want 2", n)
	}
	if w := pac40Do(r, http.MethodPatch, "/api/scheduling-links/"+link.ID, `{"max_uses":3}`, &owner); w.Code != 200 {
		t.Fatalf("raise limit: %d %s", w.Code, pac40Body(w))
	}
	if w := pac40BookOK(t, r, link.Slug, start.Add(5*time.Hour)); w.Code != http.StatusCreated {
		t.Fatalf("booking after raise: %d %s", w.Code, pac40Body(w))
	}
	pac40ExpectGone(t, r, link.Slug, start.Add(7*time.Hour))
}

// AC-17
func TestPAC40OnlyCancelledBookingCountsOnce(t *testing.T) {
	r := pac40Routes(t)
	owner := createTestUser(t, "pac40-uses-onecancel@example.com")
	link := pac40CreateUsageLink(t, r, owner, "pac40-uses-onecancel", "recurring", 2)
	start := futureBookingStart()
	pac40Book(t, uuid.MustParse(link.ID), start, "cancelled")
	if n := pac40UsesCount(t, r, owner, link.ID); n != 1 {
		t.Fatalf("uses_count = %d, want 1", n)
	}
	if w := pac40BookOK(t, r, link.Slug, start.Add(2*time.Hour)); w.Code != http.StatusCreated {
		t.Fatalf("one more booking: %d %s", w.Code, pac40Body(w))
	}
	pac40ExpectGone(t, r, link.Slug, start.Add(4*time.Hour))
}

// AC-19
func TestPAC40FetchOneLinkIsOwnerAndAcceptedCoHostsOnly(t *testing.T) {
	r := pac40Routes(t)
	owner := createTestUser(t, "pac40-fetch-owner@example.com")
	cohost := createTestUser(t, "pac40-fetch-cohost@example.com")
	pending := createTestUser(t, "pac40-fetch-pending@example.com")
	stranger := createTestUser(t, "pac40-fetch-stranger@example.com")
	link := createSchedulingLinkRecord(t, owner, "pac40-fetch", "Fetch")
	if _, err := storage.AddLinkHost(sharedTestDB, link.ID, cohost, "accepted"); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.AddLinkHost(sharedTestDB, link.ID, pending, "pending"); err != nil {
		t.Fatal(err)
	}
	base := "/api/scheduling-links/" + link.ID.String()
	for name, u := range map[string]uuid.UUID{"owner": owner, "co-host": cohost} {
		w := pac40Do(r, http.MethodGet, base, "", &u)
		if w.Code != http.StatusOK {
			t.Fatalf("%s GET: %d %s", name, w.Code, pac40Body(w))
		}
		if got := pac40Link(t, w); got.ID != link.ID.String() || len(got.Hosts) != 3 {
			t.Fatalf("%s GET: %+v, want the link with its 3 hosts", name, got)
		}
	}
	for name, u := range map[string]uuid.UUID{"pending invitee": pending, "stranger": stranger} {
		pac40Expect(t, name+" GET", pac40Do(r, http.MethodGet, base, "", &u), 403, `{"error":"forbidden"}`)
	}
	// Paused: the same rule (C-1).
	if w := pac40Do(r, http.MethodPatch, base, `{"active":false}`, &owner); w.Code != 200 {
		t.Fatalf("pause: %d", w.Code)
	}
	pac40Expect(t, "stranger GET paused", pac40Do(r, http.MethodGet, base, "", &stranger), 403, `{"error":"forbidden"}`)
	if w := pac40Do(r, http.MethodGet, base, "", &cohost); w.Code != 200 {
		t.Fatalf("co-host GET paused: %d %s", w.Code, pac40Body(w))
	}
}

// AC-20, AC-20a: every rejection of public booking create keeps its status and body.
func TestPAC40PublicBookingCreateRejectionsUnchanged(t *testing.T) {
	r := pac40Routes(t)
	owner := createTestUser(t, "pac40-reject-owner@example.com")
	link := createPublicBookingLink(t, owner, "pac40-reject", "Reject", 120, "reusable", nil)
	used := createPublicBookingLink(t, owner, "pac40-reject-used", "Used", 0, "single_use", nil)
	pac40Book(t, used.ID, futureBookingStart(), "")
	start := futureBookingStart()
	pac40Book(t, link.ID, start, "")
	free := start.Add(3 * time.Hour)
	f := func(s time.Time) string { return s.Format(time.RFC3339) }
	tooSoon := time.Now().UTC().Add(30 * time.Minute)

	cases := []struct{ name, slug, body, want string }{
		{"invalid body", link.Slug, `{`, `400 {"error":"invalid request body"}`},
		{"missing fields", link.Slug, `{}`, `400 {"error":"name, email, and start are required"}`},
		{"bad start", link.Slug, `{"name":"A","email":"a@x.com","start":"tomorrow"}`, `400 {"error":"invalid start time, use RFC3339"}`},
		{"bad end", link.Slug, fmt.Sprintf(`{"name":"A","email":"a@x.com","start":%q,"end":"later"}`, f(free)), `400 {"error":"invalid end time, use RFC3339"}`},
		{"mismatch", link.Slug, fmt.Sprintf(`{"name":"A","email":"a@x.com","start":%q,"end":%q,"duration_minutes":60}`, f(free), f(free.Add(30*time.Minute))), `400 {"error":"duration does not match end time"}`},
		{"invalid duration", link.Slug, fmt.Sprintf(`{"name":"A","email":"a@x.com","start":%q,"duration_minutes":45}`, f(free)), `400 {"error":"invalid duration"}`},
		{"too soon", link.Slug, fmt.Sprintf(`{"name":"A","email":"a@x.com","start":%q,"duration_minutes":30}`, f(tooSoon)), `422 {"error":"booking is too soon"}`},
		{"used up", used.Slug, fmt.Sprintf(`{"name":"A","email":"a@x.com","start":%q,"duration_minutes":30}`, f(free)), `410 {"error":"link is no longer accepting bookings"}`},
		{"overlap", link.Slug, fmt.Sprintf(`{"name":"A","email":"a@x.com","start":%q,"duration_minutes":30}`, f(start)), `409 {"error":"slot is no longer available"}`},
		{"unknown slug", "pac40-never-existed", fmt.Sprintf(`{"name":"A","email":"a@x.com","start":%q}`, f(free)), `404 {"error":"not found"}`},
		// end at or before start: the section 3.5 partition, cases (a)-(d).
		{"(a) stated 30, end = start-30", link.Slug, fmt.Sprintf(`{"name":"A","email":"a@x.com","start":%q,"end":%q,"duration_minutes":30}`, f(free), f(free.Add(-30*time.Minute))), `400 {"error":"duration does not match end time"}`},
		{"(b) no stated duration, end = start-30", link.Slug, fmt.Sprintf(`{"name":"A","email":"a@x.com","start":%q,"end":%q}`, f(free), f(free.Add(-30*time.Minute))), `400 {"error":"invalid duration"}`},
		{"(b) no stated duration, end = start", link.Slug, fmt.Sprintf(`{"name":"A","email":"a@x.com","start":%q,"end":%q}`, f(free), f(free)), `400 {"error":"invalid duration"}`},
		{"(c) primary 0, alternative 30", link.Slug, fmt.Sprintf(`{"name":"A","email":"a@x.com","start":%q,"end":%q,"duration":0,"duration_minutes":30}`, f(free), f(free.Add(-30*time.Minute))), `400 {"error":"invalid duration"}`},
		{"(d) stated -30 primary", link.Slug, fmt.Sprintf(`{"name":"A","email":"a@x.com","start":%q,"end":%q,"duration":-30}`, f(free), f(free.Add(-30*time.Minute))), `400 {"error":"invalid duration"}`},
		{"(d) stated -30 alternative", link.Slug, fmt.Sprintf(`{"name":"A","email":"a@x.com","start":%q,"end":%q,"duration_minutes":-30}`, f(free), f(free.Add(-30*time.Minute))), `400 {"error":"invalid duration"}`},
	}
	for _, c := range cases {
		w := pac40Do(r, http.MethodPost, "/api/book/"+c.slug, c.body, nil)
		if got := fmt.Sprintf("%d %s", w.Code, pac40Body(w)); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
	if n := pac40BookingCount(t, link.ID); n != 1 {
		t.Fatalf("booking rows on the reject link = %d, want 1 (none created)", n)
	}
}

func TestPAC40OverflowingDurationIsEndNotAfterStart(t *testing.T) {
	r := pac40Routes(t)
	owner := createTestUser(t, "pac40-overflow@example.com")
	link, err := storage.CreateSchedulingLink(sharedTestDB, &storage.SchedulingLink{
		OwnerUserID: owner, Slug: "pac40-overflow", Title: "Overflow", DurationOptions: []int{200000000},
		DaysOfWeek: []int{0, 1, 2, 3, 4, 5, 6}, WindowStart: "00:00", WindowEnd: "23:59", UsageType: "reusable", Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	w := pac40Do(r, http.MethodPost, "/api/book/"+link.Slug,
		fmt.Sprintf(`{"name":"A","email":"a@x.com","start":%q}`, futureBookingStart().Format(time.RFC3339)), nil)
	pac40Expect(t, "overflow", w, 400, `{"error":"end must be after start"}`)
	if n := pac40BookingCount(t, link.ID); n != 0 {
		t.Fatalf("booking rows = %d, want 0", n)
	}
}
