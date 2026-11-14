package api

// PAC-40 part A (contracts/features/PAC-40.md revision 3 + K4, bundle A): a
// deleted link is gone from every live and public path for every caller,
// with the unknown-id 404; the owner reads it only through the archive; the
// canonical spelling wins on PATCH; uses_count counts cancelled bookings.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/internal/testdb"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// pac40Flow offers a fixed list of slots and stores a confirmed booking the
// way the engine does, so the use count moves as in production.
type pac40Flow struct {
	db    *sql.DB
	offer []engine.AvailableSlot
}

func (f *pac40Flow) CollectiveSlots(context.Context, *storage.SchedulingLink, time.Time, int) ([]engine.AvailableSlot, error) {
	return f.offer, nil
}

func (f *pac40Flow) CollectiveSlotsDays(_ context.Context, _ *storage.SchedulingLink, days []time.Time, _ int) ([][]engine.AvailableSlot, error) {
	out := make([][]engine.AvailableSlot, len(days))
	for i := range days {
		out[i] = f.offer
	}
	return out, nil
}

func (f *pac40Flow) ConfirmBooking(_ context.Context, link *storage.SchedulingLink, name, email string, start, end time.Time, notes, _ string) (*storage.Booking, error) {
	return storage.CreateBooking(f.db, &storage.Booking{LinkID: link.ID, BookerName: name, BookerEmail: email, StartTime: start, EndTime: end, Notes: notes})
}

type pac40Env struct {
	t   *testing.T
	db  *sql.DB
	r   *chi.Mux
	slh *schedulingLinkHandlers
}

// newPAC40Env mounts the scheduling-link routes in production's order (the
// static archive segments before /{id}) and the public booking routes, on db.
func newPAC40Env(t *testing.T, db *sql.DB) *pac40Env {
	t.Helper()
	resetBookingLimiters(t)
	start := futureBookingStart()
	var offer []engine.AvailableSlot
	for i := 0; i < 4; i++ {
		s := start.Add(time.Duration(i) * time.Hour)
		offer = append(offer, engine.AvailableSlot{Start: s, End: s.Add(30 * time.Minute)})
	}
	slh := newSchedulingLinkHandlers(db, nil)
	bh := newBookingHandlersWithEngine(db, &pac40Flow{db: db, offer: offer})
	r := chi.NewRouter()
	r.Route("/api/scheduling-links", func(r chi.Router) {
		r.Post("/", slh.createLink)
		r.Get("/", slh.listLinks)
		r.Get("/host-invites", slh.listHostInvites)
		r.Get("/invites", slh.listHostInvites)
		r.Post("/host-invites/{id}/accept", slh.acceptInvite)
		r.Post("/host-invites/{id}/decline", slh.declineInvite)
		r.Get("/archive", slh.listArchivedLinks)
		r.Get("/archive/{id}", slh.getArchivedLink)
		r.Get("/{id}", slh.getLink)
		r.Patch("/{id}", slh.updateLink)
		r.Delete("/{id}", slh.deleteLink)
		r.Get("/{id}/bookings", slh.listBookings)
		r.Post("/{id}/hosts", slh.inviteHost)
		r.Post("/{id}/accept", slh.acceptInvite)
		r.Post("/{id}/decline", slh.declineInvite)
		r.Post("/{id}/leave", slh.leaveLink)
	})
	r.Get("/api/book/{slug}", bh.getLinkInfo)
	r.Get("/api/book/{slug}/slots", bh.getSlots)
	r.Post("/api/book/{slug}", bh.createBooking)
	return &pac40Env{t: t, db: db, r: r, slh: slh}
}

// do sends one request; user uuid.Nil sends it without a signed-in user.
func (e *pac40Env) do(method, path string, user uuid.UUID, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd *bytes.Buffer
	if body != "" {
		rd = bytes.NewBufferString(body)
	} else {
		rd = &bytes.Buffer{}
	}
	req := httptest.NewRequest(method, path, rd)
	if user != uuid.Nil {
		req = withUser(req, user)
	}
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w
}

func (e *pac40Env) mustStatus(w *httptest.ResponseRecorder, want int, what string) {
	e.t.Helper()
	if w.Code != want {
		e.t.Fatalf("%s: status %d, want %d; body %s", what, w.Code, want, w.Body.String())
	}
}

func (e *pac40Env) deleteLink(owner, id uuid.UUID) {
	e.t.Helper()
	e.mustStatus(e.do(http.MethodDelete, "/api/scheduling-links/"+id.String(), owner, ""), http.StatusNoContent, "delete")
}

func (e *pac40Env) linkRow(id uuid.UUID) (deletedAt sql.NullTime, active bool) {
	e.t.Helper()
	if err := e.db.QueryRow(`SELECT deleted_at, active FROM scheduling_links WHERE id = $1`, id).Scan(&deletedAt, &active); err != nil {
		e.t.Fatalf("read link row: %v", err)
	}
	return deletedAt, active
}

func (e *pac40Env) hostRows(id uuid.UUID) map[uuid.UUID]string {
	e.t.Helper()
	hosts, err := storage.GetLinkHosts(e.db, id)
	if err != nil {
		e.t.Fatal(err)
	}
	out := map[uuid.UUID]string{}
	for _, h := range hosts {
		out[h.UserID] = h.Status
	}
	return out
}

func (e *pac40Env) bookingCount(id uuid.UUID) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM bookings WHERE link_id = $1`, id).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// sameAsUnknown asserts that a request naming id answers exactly what the
// same request by the same caller answers for an id that never existed.
func (e *pac40Env) sameAsUnknown(method, pathTmpl string, user, id uuid.UUID, body, what string) {
	e.t.Helper()
	got := e.do(method, strings.ReplaceAll(pathTmpl, "{id}", id.String()), user, body)
	want := e.do(method, strings.ReplaceAll(pathTmpl, "{id}", uuid.New().String()), user, body)
	if got.Code != want.Code || got.Body.String() != want.Body.String() {
		e.t.Errorf("%s: got %d %q; an unknown id gets %d %q", what, got.Code, got.Body.String(), want.Code, want.Body.String())
	}
	if got.Code != http.StatusNotFound || strings.TrimSpace(got.Body.String()) != `{"error":"not found"}` {
		e.t.Errorf("%s: got %d %q; want the 404 not found", what, got.Code, got.Body.String())
	}
}

func pac40Booking(t *testing.T, db *sql.DB, linkID uuid.UUID, name string, start time.Time, status string) *storage.Booking {
	t.Helper()
	b, err := storage.CreateBooking(db, &storage.Booking{LinkID: linkID, BookerName: name, BookerEmail: name + "@booker.test",
		StartTime: start, EndTime: start.Add(30 * time.Minute), Notes: "notes of " + name})
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}
	if status != "" {
		if _, err := db.Exec(`UPDATE bookings SET status = $1 WHERE id = $2`, status, b.ID); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

func pac40DecodeLink(t *testing.T, w *httptest.ResponseRecorder) schedulingLinkDTO {
	t.Helper()
	var dto schedulingLinkDTO
	if err := json.Unmarshal(w.Body.Bytes(), &dto); err != nil {
		t.Fatalf("decode link: %v; body %s", err, w.Body.String())
	}
	return dto
}

// AC-1..AC-6, AC-6a: after the owner deletes a link, every live and public
// path answers every caller exactly as for an id or slug that never existed,
// and nothing about the link, its host rows or its bookings changes.
func TestPAC40_DeletedLinkIsNotFoundOnEveryPathForEveryCaller(t *testing.T) {
	e := newPAC40Env(t, openTestDB(t))
	owner := createConfirmedUser(t, "pac40-every-owner@example.com")
	cohost := createConfirmedUser(t, "pac40-every-cohost@example.com")
	invitee := createConfirmedUser(t, "pac40-every-invitee@example.com")
	stranger := createConfirmedUser(t, "pac40-every-stranger@example.com")
	other := createConfirmedUser(t, "pac40-every-other@example.com")
	link := createSchedulingLinkRecord(t, owner, "pac40-every", "PAC-40 Every Path")
	if _, err := storage.AddLinkHost(e.db, link.ID, cohost, "accepted"); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.AddLinkHost(e.db, link.ID, invitee, "pending"); err != nil {
		t.Fatal(err)
	}
	pac40Booking(t, e.db, link.ID, "kim", futureBookingStart(), "")
	id := link.ID.String()

	e.deleteLink(owner, link.ID)
	deletedAt, _ := e.linkRow(link.ID)
	hostsBefore := e.hostRows(link.ID)

	// AC-1: neither the owner's nor the co-host's list has it.
	for _, u := range []uuid.UUID{owner, cohost} {
		w := e.do(http.MethodGet, "/api/scheduling-links", u, "")
		e.mustStatus(w, http.StatusOK, "list")
		got := decodeOwnedShared(t, w.Body)
		for _, group := range []string{"owned", "shared"} {
			for _, l := range got[group] {
				if l.ID == id {
					t.Errorf("AC-1: the deleted link is in %s's %s group", u, group)
				}
			}
		}
	}
	// AC-5: the invite is gone from the invitee's lists (both routes).
	for _, p := range []string{"/api/scheduling-links/invites", "/api/scheduling-links/host-invites"} {
		w := e.do(http.MethodGet, p, invitee, "")
		e.mustStatus(w, http.StatusOK, p)
		if strings.Contains(w.Body.String(), id) || strings.TrimSpace(w.Body.String()) != "[]" {
			t.Errorf("AC-5: %s = %s; want [] (the deleted link's invite omitted)", p, w.Body.String())
		}
	}

	const (
		one      = "/api/scheduling-links/{id}"
		bookings = "/api/scheduling-links/{id}/bookings"
		hosts    = "/api/scheduling-links/{id}/hosts"
		leave    = "/api/scheduling-links/{id}/leave"
		archived = "/api/scheduling-links/archive/{id}"
	)
	inviteBody := `{"email":"pac40-every-other@example.com"}`
	_ = other
	// AC-2, AC-3, AC-6 (owner), AC-5 (owner leave): the unknown-id 404, before
	// the owner check and the body read (a malformed body is still a 404).
	e.sameAsUnknown(http.MethodGet, one, owner, link.ID, "", "AC-2 owner fetch")
	e.sameAsUnknown(http.MethodPatch, one, owner, link.ID, `{"active":true}`, "AC-3 owner PATCH active:true")
	e.sameAsUnknown(http.MethodPatch, one, owner, link.ID, `{"title":`, "AC-3 owner PATCH malformed body")
	e.sameAsUnknown(http.MethodDelete, one, owner, link.ID, "", "AC-6 repeated delete")
	e.sameAsUnknown(http.MethodGet, bookings, owner, link.ID, "", "AC-6 owner live bookings")
	e.sameAsUnknown(http.MethodPost, hosts, owner, link.ID, inviteBody, "AC-6 owner invite")
	e.sameAsUnknown(http.MethodPost, hosts, owner, link.ID, `not json`, "AC-6 owner invite, malformed body")
	e.sameAsUnknown(http.MethodPost, leave, owner, link.ID, "", "AC-5 owner leave")
	// AC-2, AC-5: the accepted co-host.
	e.sameAsUnknown(http.MethodGet, one, cohost, link.ID, "", "AC-2 co-host fetch")
	e.sameAsUnknown(http.MethodPost, leave, cohost, link.ID, "", "AC-5 co-host leave")
	// AC-5: the pending invitee's answers and leave, on both invite routes.
	for _, p := range []string{"/api/scheduling-links/{id}/accept", "/api/scheduling-links/{id}/decline",
		"/api/scheduling-links/host-invites/{id}/accept", "/api/scheduling-links/host-invites/{id}/decline"} {
		e.sameAsUnknown(http.MethodPost, p, invitee, link.ID, "", "AC-5 invitee "+p)
	}
	e.sameAsUnknown(http.MethodPost, leave, invitee, link.ID, "", "AC-5 invitee leave")
	// AC-6a: a stranger gets the unknown-id answer, not the live link's 403.
	e.sameAsUnknown(http.MethodGet, one, stranger, link.ID, "", "AC-6a stranger fetch")
	e.sameAsUnknown(http.MethodPatch, one, stranger, link.ID, `{"title":"x"}`, "AC-6a stranger edit")
	e.sameAsUnknown(http.MethodDelete, one, stranger, link.ID, "", "AC-6a stranger delete")
	e.sameAsUnknown(http.MethodGet, bookings, stranger, link.ID, "", "AC-6a stranger bookings")
	e.sameAsUnknown(http.MethodPost, hosts, stranger, link.ID, inviteBody, "AC-6a stranger invite")
	e.sameAsUnknown(http.MethodPost, "/api/scheduling-links/{id}/accept", stranger, link.ID, "", "AC-6a stranger invite answer")
	e.sameAsUnknown(http.MethodGet, archived, stranger, link.ID, "", "AC-6a stranger archive detail")
	// AC-4: public info, slots and booking on the slug.
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/book/" + link.Slug, ""},
		{http.MethodGet, "/api/book/" + link.Slug + "/slots?date=" + futureBookingStart().Format("2006-01-02") + "&duration=30", ""},
		{http.MethodPost, "/api/book/" + link.Slug, `{"name":"Lea","email":"lea@booker.test","start":"` + futureBookingStart().Add(time.Hour).Format(time.RFC3339) + `","duration":30}`},
	} {
		w := e.do(c.method, c.path, uuid.Nil, c.body)
		unknown := e.do(c.method, strings.Replace(c.path, link.Slug, "pac40-never-existed-"+uuid.NewString()[:8], 1), uuid.Nil, c.body)
		if w.Code != http.StatusNotFound || w.Body.String() != unknown.Body.String() || unknown.Code != http.StatusNotFound {
			t.Errorf("AC-4 %s %s: %d %q; an unknown slug gets %d %q", c.method, c.path, w.Code, w.Body.String(), unknown.Code, unknown.Body.String())
		}
	}
	// AC-6a: absent from the stranger's archive list too.
	if w := e.do(http.MethodGet, "/api/scheduling-links/archive", stranger, ""); strings.Contains(w.Body.String(), id) {
		t.Errorf("AC-6a: the stranger's archive lists the link: %s", w.Body.String())
	}

	// Nothing changed (AC-3 "still deleted", AC-5 "status unchanged", AC-6).
	at, active := e.linkRow(link.ID)
	if !at.Valid || !at.Time.Equal(deletedAt.Time) || active {
		t.Errorf("link row: deleted_at %v (was %v), active %v; want unchanged, paused", at, deletedAt, active)
	}
	if got := e.hostRows(link.ID); len(got) != len(hostsBefore) || got[cohost] != "accepted" || got[invitee] != "pending" || got[owner] != "accepted" {
		t.Errorf("host rows = %v; want %v unchanged", got, hostsBefore)
	}
	if n := e.bookingCount(link.ID); n != 1 {
		t.Errorf("bookings = %d; want 1 (no booking created, none removed)", n)
	}
	var invited int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM scheduling_link_hosts WHERE link_id = $1 AND user_id = $2`, link.ID, other).Scan(&invited)
	if invited != 0 {
		t.Error("an invite to the deleted link was stored")
	}
}

// AC-6 / AC-6a on the live link: PAC-40 does not change the 403 a non-owner
// gets on a LIVE link's edit, delete, bookings and invite (spec §5).
func TestPAC40_LiveLinkKeepsItsForbiddenForNonOwners(t *testing.T) {
	e := newPAC40Env(t, openTestDB(t))
	owner := createConfirmedUser(t, "pac40-live403-owner@example.com")
	stranger := createConfirmedUser(t, "pac40-live403-stranger@example.com")
	link := createSchedulingLinkRecord(t, owner, "pac40-live403", "PAC-40 Live 403")
	p := "/api/scheduling-links/" + link.ID.String()
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPatch, p, `{"title":"x"}`},
		{http.MethodDelete, p, ""},
		{http.MethodGet, p + "/bookings", ""},
		{http.MethodPost, p + "/hosts", `{"email":"pac40-live403-owner@example.com"}`},
	} {
		e.mustStatus(e.do(c.method, c.path, stranger, c.body), http.StatusForbidden, c.method+" "+c.path)
	}
	if at, active := e.linkRow(link.ID); at.Valid || !active {
		t.Fatalf("live link changed: deleted_at %v active %v", at, active)
	}
}

// AC-6b (K4-1): a PATCH whose read ran before a concurrent delete committed
// answers 404, writes nothing (co-hosts included), and leaves the link
// deleted and paused. The delete is committed from the zone read, which runs
// after the PATCH's link read and before its UPDATE.
func TestPAC40_PatchRacingADeleteIs404AndWritesNothing(t *testing.T) {
	e := newPAC40Env(t, openTestDB(t))
	owner := createConfirmedUser(t, "pac40-race-patch-owner@example.com")
	invitee := createConfirmedUser(t, "pac40-race-patch-invitee@example.com")
	link := createSchedulingLinkRecord(t, owner, "pac40-race-patch", "PAC-40 Race Patch")
	var once sync.Once
	e.slh.ownerZone = func(db storage.DBTX, userID uuid.UUID) (string, bool, error) {
		once.Do(func() {
			if ok, err := storage.DeleteSchedulingLink(e.db, link.ID); err != nil || !ok {
				t.Errorf("concurrent delete: %v %v", ok, err)
			}
		})
		return "UTC", true, nil
	}
	w := e.do(http.MethodPatch, "/api/scheduling-links/"+link.ID.String(), owner,
		`{"title":"revived","active":true,"co_host_emails":["pac40-race-patch-invitee@example.com"]}`)
	e.mustStatus(w, http.StatusNotFound, "PATCH racing a delete")
	if strings.TrimSpace(w.Body.String()) != `{"error":"not found"}` {
		t.Errorf("body = %s", w.Body.String())
	}
	at, active := e.linkRow(link.ID)
	if !at.Valid || active {
		t.Fatalf("after the race: deleted_at %v, active %v; want deleted and paused", at, active)
	}
	var title string
	_ = e.db.QueryRow(`SELECT title FROM scheduling_links WHERE id = $1`, link.ID).Scan(&title)
	if title != link.Title {
		t.Errorf("title = %q; the PATCH wrote over a deleted link", title)
	}
	if hosts := e.hostRows(link.ID); len(hosts) != 1 || hosts[invitee] != "" {
		t.Errorf("host rows = %v; the PATCH changed co-hosts of a deleted link", hosts)
	}
}

// AC-6b (K4-2): two concurrent deletes answer one 204 and one 404, and the
// second never moves deleted_at.
func TestPAC40_ConcurrentDeletesAnswerOne204AndOne404(t *testing.T) {
	e := newPAC40Env(t, openTestDB(t))
	owner := createConfirmedUser(t, "pac40-race-del-owner@example.com")
	for i := 0; i < 5; i++ {
		link := createSchedulingLinkRecord(t, owner, "pac40-race-del-"+uuid.NewString()[:8], "PAC-40 Race Delete")
		codes := make([]int, 2)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for j := range codes {
			wg.Add(1)
			go func(j int) {
				defer wg.Done()
				<-start
				req := withUser(httptest.NewRequest(http.MethodDelete, "/api/scheduling-links/"+link.ID.String(), nil), owner)
				w := httptest.NewRecorder()
				e.r.ServeHTTP(w, req)
				codes[j] = w.Code
			}(j)
		}
		close(start)
		wg.Wait()
		n204, n404 := 0, 0
		for _, c := range codes {
			switch c {
			case http.StatusNoContent:
				n204++
			case http.StatusNotFound:
				n404++
			}
		}
		if n204 != 1 || n404 != 1 {
			t.Fatalf("round %d: concurrent deletes answered %v; want one 204 and one 404", i, codes)
		}
		at, active := e.linkRow(link.ID)
		e.sameAsUnknown(http.MethodDelete, "/api/scheduling-links/{id}", owner, link.ID, "", "a third delete")
		if at2, _ := e.linkRow(link.ID); !at.Valid || active || !at2.Time.Equal(at.Time) {
			t.Fatalf("round %d: deleted_at %v -> %v, active %v", i, at, at2, active)
		}
	}
}

// AC-6b, third case: a row forced to deleted_at set with active = true (the
// CHECK bypassed, in a database of its own) is not found on the public slug
// paths: deleted_at IS NULL is part of the public lookup (K4-1).
func TestPAC40_ForcedDeletedActiveRowIs404OnPublicPaths(t *testing.T) {
	db, _ := pac40IsolatedDB(t, 0)
	e := newPAC40Env(t, db)
	owner := pac40RawUser(t, db, "pac40-forced@example.com")
	link, err := storage.CreateSchedulingLink(db, &storage.SchedulingLink{OwnerUserID: owner, Slug: "pac40-forced", Title: "Forced",
		DurationOptions: []int{30}, DaysOfWeek: []int{1, 2, 3, 4, 5}, WindowStart: "09:00", WindowEnd: "17:00", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE scheduling_links DROP CONSTRAINT scheduling_links_deleted_inactive`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE scheduling_links SET deleted_at = NOW(), active = true WHERE id = $1`, link.ID); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/book/pac40-forced", ""},
		{http.MethodGet, "/api/book/pac40-forced/slots?date=" + futureBookingStart().Format("2006-01-02") + "&duration=30", ""},
		{http.MethodPost, "/api/book/pac40-forced", `{"name":"Mo","email":"mo@booker.test","start":"` + futureBookingStart().Format(time.RFC3339) + `","duration":30}`},
	} {
		e.mustStatus(e.do(c.method, c.path, uuid.Nil, c.body), http.StatusNotFound, c.method+" "+c.path)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM bookings WHERE link_id = $1`, link.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("a booking was stored on the forced row: %d", n)
	}
}

// AC-8: a deleted link keeps its slug; creating or patching another link to
// it is the 409.
func TestPAC40_DeletedLinkKeepsItsSlug(t *testing.T) {
	e := newPAC40Env(t, openTestDB(t))
	owner := createConfirmedUser(t, "pac40-slug-owner@example.com")
	gone := createSchedulingLinkRecord(t, owner, "pac40-slug-kept", "Gone")
	live := createSchedulingLinkRecord(t, owner, "pac40-slug-live", "Live")
	e.deleteLink(owner, gone.ID)
	w := e.do(http.MethodPost, "/api/scheduling-links", owner, `{"title":"New","slug":"pac40-slug-kept"}`)
	e.mustStatus(w, http.StatusConflict, "create on a deleted link's slug")
	w = e.do(http.MethodPatch, "/api/scheduling-links/"+live.ID.String(), owner, `{"slug":"pac40-slug-kept"}`)
	e.mustStatus(w, http.StatusConflict, "patch to a deleted link's slug")
	if !strings.Contains(w.Body.String(), "slug already exists") {
		t.Errorf("body = %s", w.Body.String())
	}
}

// AC-11: a paused link is listed and fetched with active=false, is not in
// the archive, and resuming it re-opens its public page.
func TestPAC40_PausedLinkStaysLiveAndResumes(t *testing.T) {
	e := newPAC40Env(t, openTestDB(t))
	owner := createConfirmedUser(t, "pac40-pause-owner@example.com")
	link := createSchedulingLinkRecord(t, owner, "pac40-pause", "PAC-40 Paused")
	p := "/api/scheduling-links/" + link.ID.String()
	e.mustStatus(e.do(http.MethodPatch, p, owner, `{"active":false}`), http.StatusOK, "pause")

	w := e.do(http.MethodGet, "/api/scheduling-links", owner, "")
	owned := decodeOwnedShared(t, w.Body)["owned"]
	if len(owned) != 1 || owned[0].ID != link.ID.String() || owned[0].Active {
		t.Fatalf("list = %+v; want the paused link, active=false", owned)
	}
	if dto := pac40DecodeLink(t, e.do(http.MethodGet, p, owner, "")); dto.Active || dto.ID != link.ID.String() {
		t.Fatalf("fetch = %+v; want it, active=false", dto)
	}
	if w := e.do(http.MethodGet, "/api/scheduling-links/archive", owner, ""); strings.Contains(w.Body.String(), link.ID.String()) {
		t.Fatalf("the paused link is in the archive: %s", w.Body.String())
	}
	e.sameAsUnknown(http.MethodGet, "/api/scheduling-links/archive/{id}", owner, link.ID, "", "archive detail of the owner's paused link")
	e.mustStatus(e.do(http.MethodGet, "/api/book/"+link.Slug, uuid.Nil, ""), http.StatusNotFound, "paused public page")

	e.mustStatus(e.do(http.MethodPatch, p, owner, `{"active":true}`), http.StatusOK, "resume")
	e.mustStatus(e.do(http.MethodGet, "/api/book/"+link.Slug, uuid.Nil, ""), http.StatusOK, "resumed public page")
}

// AC-19: on a live link, the owner and an accepted co-host get it with its
// hosts; a pending invitee and a stranger get the unknown-id 404 (PAC-25).
func TestPAC40_LiveLinkFetchKeepsPAC25(t *testing.T) {
	e := newPAC40Env(t, openTestDB(t))
	owner := createConfirmedUser(t, "pac40-ac19-owner@example.com")
	cohost := createConfirmedUser(t, "pac40-ac19-cohost@example.com")
	invitee := createConfirmedUser(t, "pac40-ac19-invitee@example.com")
	stranger := createConfirmedUser(t, "pac40-ac19-stranger@example.com")
	link := createSchedulingLinkRecord(t, owner, "pac40-ac19", "PAC-40 AC-19")
	_, _ = storage.AddLinkHost(e.db, link.ID, cohost, "accepted")
	_, _ = storage.AddLinkHost(e.db, link.ID, invitee, "pending")
	for _, u := range []uuid.UUID{owner, cohost} {
		w := e.do(http.MethodGet, "/api/scheduling-links/"+link.ID.String(), u, "")
		e.mustStatus(w, http.StatusOK, "fetch")
		if dto := pac40DecodeLink(t, w); len(dto.Hosts) != 3 {
			t.Errorf("hosts = %+v; want all three", dto.Hosts)
		}
	}
	for _, u := range []uuid.UUID{invitee, stranger} {
		e.sameAsUnknown(http.MethodGet, "/api/scheduling-links/{id}", u, link.ID, "", "AC-19 fetch by a non-reader")
	}
}

// AC-13, AC-14, AC-14a (API-049): the canonical spelling wins on both verbs
// whenever it is carried; null falls back to the alias; [] is carried and
// refused, leaving the link unchanged on PATCH.
func TestPAC40_CanonicalSpellingWinsOnBothVerbs(t *testing.T) {
	e := newPAC40Env(t, openTestDB(t))
	owner := createConfirmedUser(t, "pac40-alias-owner@example.com")
	both := `"duration_options":[45],"durations":[15],"days_of_week":[2],"days":["fri"]`

	w := e.do(http.MethodPost, "/api/scheduling-links", owner, `{"title":"Both","slug":"pac40-alias-create",`+both+`}`)
	e.mustStatus(w, http.StatusCreated, "AC-13 create with both spellings")
	created := pac40DecodeLink(t, w)
	if len(created.Durations) != 1 || created.Durations[0] != 45 || len(created.Days) != 1 || created.Days[0] != "tue" {
		t.Fatalf("AC-13 created = %v %v; want [45] [tue]", created.Durations, created.Days)
	}

	link := createSchedulingLinkRecord(t, owner, "pac40-alias-patch", "Patch")
	p := "/api/scheduling-links/" + link.ID.String()
	w = e.do(http.MethodPatch, p, owner, `{`+both+`}`)
	e.mustStatus(w, http.StatusOK, "AC-14 PATCH with both spellings")
	patched := pac40DecodeLink(t, w)
	if len(patched.Durations) != 1 || patched.Durations[0] != 45 || len(patched.Days) != 1 || patched.Days[0] != "tue" {
		t.Fatalf("AC-14 patched = %v %v; want the same as AC-13", patched.Durations, patched.Days)
	}

	// AC-14a, null canonical: the alias is used, on both verbs.
	w = e.do(http.MethodPost, "/api/scheduling-links", owner, `{"title":"Null","slug":"pac40-alias-null","duration_options":null,"durations":[20],"days_of_week":null,"days":["wed"]}`)
	e.mustStatus(w, http.StatusCreated, "AC-14a create with null canonical")
	if dto := pac40DecodeLink(t, w); dto.Durations[0] != 20 || dto.Days[0] != "wed" {
		t.Fatalf("AC-14a create = %v %v; want the alias", dto.Durations, dto.Days)
	}
	w = e.do(http.MethodPatch, p, owner, `{"duration_options":null,"durations":[20],"days_of_week":null,"days":["wed"]}`)
	e.mustStatus(w, http.StatusOK, "AC-14a PATCH with null canonical")
	if dto := pac40DecodeLink(t, w); dto.Durations[0] != 20 || dto.Days[0] != "wed" {
		t.Fatalf("AC-14a PATCH = %v %v; want the alias", dto.Durations, dto.Days)
	}

	// AC-14a, empty canonical: refused as create refuses an empty list.
	before := e.do(http.MethodGet, p, owner, "").Body.String()
	for _, c := range []struct{ body, msg string }{
		{`"duration_options":[],"durations":[20]`, "at least one duration is required"},
		{`"days_of_week":[],"days":["wed"]`, "at least one day is required"},
	} {
		cw := e.do(http.MethodPost, "/api/scheduling-links", owner, `{"title":"Empty","slug":"pac40-alias-empty",`+c.body+`}`)
		pw := e.do(http.MethodPatch, p, owner, `{`+c.body+`}`)
		for name, w := range map[string]*httptest.ResponseRecorder{"create": cw, "PATCH": pw} {
			if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), c.msg) {
				t.Errorf("AC-14a %s %s: %d %s; want 422 %q", name, c.body, w.Code, w.Body.String(), c.msg)
			}
		}
		if pw.Body.String() != cw.Body.String() {
			t.Errorf("AC-14a: PATCH %q and create %q answer differently", pw.Body.String(), cw.Body.String())
		}
	}
	if after := e.do(http.MethodGet, p, owner, "").Body.String(); after != before {
		t.Errorf("AC-14a: a refused PATCH changed the link:\n%s\n%s", before, after)
	}
}

func pac40BookBody(start time.Time, who string) string {
	return `{"name":"` + who + `","email":"` + who + `@booker.test","start":"` + start.Format(time.RFC3339) + `","duration":30}`
}

func (e *pac40Env) usesCount(owner, id uuid.UUID) int {
	e.t.Helper()
	w := e.do(http.MethodGet, "/api/scheduling-links/"+id.String(), owner, "")
	e.mustStatus(w, http.StatusOK, "owner fetch")
	return pac40DecodeLink(e.t, w).UsesCount
}

func (e *pac40Env) publicGone(slug, what string) {
	e.t.Helper()
	start := futureBookingStart().Add(3 * time.Hour)
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/book/" + slug, ""},
		{http.MethodGet, "/api/book/" + slug + "/slots?date=" + start.Format("2006-01-02") + "&duration=30", ""},
		{http.MethodPost, "/api/book/" + slug, pac40BookBody(start, "nia")},
	} {
		w := e.do(c.method, c.path, uuid.Nil, c.body)
		if w.Code != http.StatusGone || !strings.Contains(w.Body.String(), "no longer accepting bookings") {
			e.t.Errorf("%s: %s %s = %d %s; want 410", what, c.method, c.path, w.Code, w.Body.String())
		}
	}
}

// AC-15, AC-16, AC-17 (API-051): a cancelled booking keeps its use.
func TestPAC40_CancelledBookingsKeepTheirUse(t *testing.T) {
	e := newPAC40Env(t, openTestDB(t))
	owner := createConfirmedUser(t, "pac40-uses-owner@example.com")
	start := futureBookingStart()

	// AC-15: single use, booked once, cancelled.
	single := createPublicBookingLink(t, owner, "pac40-uses-single", "Single", 0, "single_use", nil)
	pac40Booking(t, e.db, single.ID, "oli", start.Add(-48*time.Hour), "cancelled")
	e.publicGone(single.Slug, "AC-15")
	if n := e.usesCount(owner, single.ID); n != 1 {
		t.Errorf("AC-15 uses_count = %d; want 1", n)
	}
	if n := e.bookingCount(single.ID); n != 1 {
		t.Errorf("AC-15: %d bookings; want no new booking", n)
	}

	// AC-16: limit 2, two bookings, one cancelled; then the limit raised to 3.
	two := 2
	limited := createPublicBookingLink(t, owner, "pac40-uses-limit", "Limit", 0, "recurring", &two)
	pac40Booking(t, e.db, limited.ID, "pat", start.Add(-48*time.Hour), "cancelled")
	pac40Booking(t, e.db, limited.ID, "quin", start.Add(-47*time.Hour), "")
	e.publicGone(limited.Slug, "AC-16")
	if n := e.usesCount(owner, limited.ID); n != 2 {
		t.Errorf("AC-16 uses_count = %d; want 2", n)
	}
	e.mustStatus(e.do(http.MethodPatch, "/api/scheduling-links/"+limited.ID.String(), owner, `{"max_uses":3}`), http.StatusOK, "AC-16 raise the limit")
	e.mustStatus(e.do(http.MethodPost, "/api/book/"+limited.Slug, uuid.Nil, pac40BookBody(start, "rae")), http.StatusCreated, "AC-16 one more booking")
	e.publicGone(limited.Slug, "AC-16 after the third booking")

	// AC-17: limit 2 with only one cancelled booking: one use, one more.
	open := createPublicBookingLink(t, owner, "pac40-uses-open", "Open", 0, "recurring", &two)
	pac40Booking(t, e.db, open.ID, "sam", start.Add(-48*time.Hour), "cancelled")
	if n := e.usesCount(owner, open.ID); n != 1 {
		t.Errorf("AC-17 uses_count = %d; want 1", n)
	}
	e.mustStatus(e.do(http.MethodPost, "/api/book/"+open.Slug, uuid.Nil, pac40BookBody(start.Add(time.Hour), "tom")), http.StatusCreated, "AC-17 one more booking")
	e.publicGone(open.Slug, "AC-17 after exactly one more")
}

// AC-21 (with part B's due instant), AC-22, AC-23, AC-24: the owner's archive.
func TestPAC40_OwnerArchive(t *testing.T) {
	e := newPAC40Env(t, openTestDB(t))
	owner := createConfirmedUser(t, "pac40-arch-owner@example.com")
	cohost := createConfirmedUser(t, "pac40-arch-cohost@example.com")
	invitee := createConfirmedUser(t, "pac40-arch-invitee@example.com")
	stranger := createConfirmedUser(t, "pac40-arch-stranger@example.com")

	// AC-24: an owner who never deleted a link: 200, links [], the policy.
	w := e.do(http.MethodGet, "/api/scheduling-links/archive", owner, "")
	e.mustStatus(w, http.StatusOK, "AC-24 empty archive")
	if got := strings.TrimSpace(w.Body.String()); got != `{"booker_erasure":{"enabled":true,"retention_days":90},"links":[]}` {
		t.Fatalf("AC-24 body = %s", got)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}

	link := createSchedulingLinkRecord(t, owner, "pac40-arch", "PAC-40 Archived")
	live := createSchedulingLinkRecord(t, owner, "pac40-arch-live", "Live")
	paused := createSchedulingLinkRecord(t, owner, "pac40-arch-paused", "Paused")
	e.mustStatus(e.do(http.MethodPatch, "/api/scheduling-links/"+paused.ID.String(), owner, `{"active":false}`), http.StatusOK, "pause")
	_, _ = storage.AddLinkHost(e.db, link.ID, cohost, "accepted")
	_, _ = storage.AddLinkHost(e.db, link.ID, invitee, "pending")
	past := time.Now().UTC().Add(-72 * time.Hour).Truncate(time.Second)
	ahead := futureBookingStart()
	pac40Booking(t, e.db, link.ID, "uma", past, "")
	later := pac40Booking(t, e.db, link.ID, "vic", ahead, "")

	// The live bookings read before the delete (AC-22's reference).
	w = e.do(http.MethodGet, "/api/scheduling-links/"+link.ID.String()+"/bookings", owner, "")
	e.mustStatus(w, http.StatusOK, "live bookings before delete")
	var liveBookings []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &liveBookings); err != nil || len(liveBookings) != 2 {
		t.Fatalf("live bookings = %s", w.Body.String())
	}

	e.deleteLink(owner, link.ID)
	deletedAt, _ := e.linkRow(link.ID)

	// AC-21 with the due instant (part B: the later end + 90 x 24 h).
	w = e.do(http.MethodGet, "/api/scheduling-links/archive", owner, "")
	e.mustStatus(w, http.StatusOK, "AC-21 archive")
	var list struct {
		Links []map[string]any `json:"links"`
		BE    map[string]any   `json:"booker_erasure"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Links) != 1 {
		t.Fatalf("AC-21 archive = %s; want exactly the deleted link (not the live %s or paused %s)", w.Body.String(), live.ID, paused.ID)
	}
	entry := list.Links[0]
	wantEnd := later.EndTime.UTC().Format(time.RFC3339Nano)
	for k, want := range map[string]any{
		"id": link.ID.String(), "title": link.Title, "slug": link.Slug,
		"deleted_at": deletedAt.Time.UTC().Format(time.RFC3339Nano), "bookings_count": float64(2),
		"last_booking_end_at": wantEnd, "erasure_state": "awaiting_erasure",
		"erasure_due_at": later.EndTime.UTC().Add(90 * 24 * time.Hour).Format(time.RFC3339Nano), "erased_at": nil,
	} {
		if got, ok := entry[k]; !ok || got != want {
			t.Errorf("AC-21 %s = %v (present %v); want %v", k, got, ok, want)
		}
	}
	if len(entry) != 9 {
		t.Errorf("AC-21 entry has %d members: %v", len(entry), entry)
	}
	if list.BE["enabled"] != true || list.BE["retention_days"] != float64(90) {
		t.Errorf("booker_erasure = %v; want enabled true, 90 days", list.BE)
	}

	// AC-22: both bookings, each field equal to the live read before delete.
	w = e.do(http.MethodGet, "/api/scheduling-links/archive/"+link.ID.String(), owner, "")
	e.mustStatus(w, http.StatusOK, "AC-22 archive detail")
	var detail struct {
		Link     map[string]any   `json:"link"`
		Bookings []map[string]any `json:"bookings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Link["id"] != link.ID.String() || detail.Link["erasure_state"] != "awaiting_erasure" {
		t.Errorf("detail link = %v", detail.Link)
	}
	if len(detail.Bookings) != 2 {
		t.Fatalf("AC-22 bookings = %d; want both (the one ahead included)", len(detail.Bookings))
	}
	for i, b := range detail.Bookings {
		for _, k := range []string{"id", "link_id", "booker_name", "booker_email", "status", "notes"} {
			if b[k] != liveBookings[i][k] {
				t.Errorf("AC-22 booking %d %s = %v; the live read said %v", i, k, b[k], liveBookings[i][k])
			}
		}
		// The same instants, serialised in UTC with a Z suffix (the web's
		// z.iso.datetime() accepts nothing else).
		for _, k := range []string{"start_time", "end_time", "created_at"} {
			got, _ := b[k].(string)
			if !strings.HasSuffix(got, "Z") {
				t.Errorf("AC-22 booking %d %s = %q; want UTC with Z", i, k, got)
			}
			gt, err1 := time.Parse(time.RFC3339Nano, got)
			lt, err2 := time.Parse(time.RFC3339Nano, liveBookings[i][k].(string))
			if err1 != nil || err2 != nil || !gt.Equal(lt) {
				t.Errorf("AC-22 booking %d %s = %v; the live read said %v", i, k, b[k], liveBookings[i][k])
			}
		}
		if v, ok := b["booker_erased_at"]; !ok || v != nil {
			t.Errorf("AC-22 booking %d booker_erased_at = %v (present %v); want null", i, v, ok)
		}
		if len(b) != 10 {
			t.Errorf("AC-22 booking %d has %d members: %v", i, len(b), b)
		}
	}

	// AC-23: not in the co-host's, invitee's or stranger's archive; the
	// detail is the unknown-id answer for each of them.
	for _, u := range []uuid.UUID{cohost, invitee, stranger} {
		w := e.do(http.MethodGet, "/api/scheduling-links/archive", u, "")
		e.mustStatus(w, http.StatusOK, "AC-23 archive")
		if strings.Contains(w.Body.String(), link.ID.String()) || !strings.Contains(w.Body.String(), `"links":[]`) {
			t.Errorf("AC-23: %s's archive = %s", u, w.Body.String())
		}
		e.sameAsUnknown(http.MethodGet, "/api/scheduling-links/archive/{id}", u, link.ID, "", "AC-23 archive detail")
	}
	// The owner's own live link is not in the archive either (contract §10).
	e.sameAsUnknown(http.MethodGet, "/api/scheduling-links/archive/{id}", owner, live.ID, "", "archive detail of a live link")
	e.mustStatus(e.do(http.MethodGet, "/api/scheduling-links/archive/not-a-uuid", owner, ""), http.StatusBadRequest, "archive detail, bad id")
}

// The archive's mapping of every display state (contract §2.2, §2.3): no
// bookings; awaiting with its due date (part B: the job runs); erased with
// null name and email and the instant, never "".
func TestPAC40_ArchiveDTOStates(t *testing.T) {
	end := time.Date(2026, 5, 1, 10, 30, 0, 0, time.UTC)
	erased := time.Date(2026, 8, 1, 3, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		in   storage.ArchivedLink
		want string
	}{
		{"none", storage.ArchivedLink{}, "no_bookings"},
		{"awaiting", storage.ArchivedLink{BookingsCount: 2, AwaitingErasure: 2, LastBookingEndAt: &end}, "awaiting_erasure"},
		{"erased", storage.ArchivedLink{BookingsCount: 2, LastBookingEndAt: &end, ErasedAt: &erased}, "erased"},
		{"late booking", storage.ArchivedLink{BookingsCount: 3, AwaitingErasure: 1, LastBookingEndAt: &end, ErasedAt: &erased}, "awaiting_erasure"},
	}
	for _, c := range cases {
		dto := archivedLinkDTO(&c.in)
		if string(dto.ErasureState) != c.want {
			t.Errorf("%s: state %q; want %q", c.name, dto.ErasureState, c.want)
		}
		// AC-21 (part B): a due instant exactly when awaiting erasure, the
		// latest end + 90 x 24 h; none for no bookings or all erased.
		if c.want == "awaiting_erasure" {
			if want := end.Add(90 * 24 * time.Hour); dto.ErasureDueAt == nil || !dto.ErasureDueAt.Equal(want) {
				t.Errorf("%s: erasure_due_at %v; want %v", c.name, dto.ErasureDueAt, want)
			}
		} else if dto.ErasureDueAt != nil {
			t.Errorf("%s: erasure_due_at %v; want none", c.name, dto.ErasureDueAt)
		}
		if (c.in.ErasedAt == nil) != (dto.ErasedAt == nil) {
			t.Errorf("%s: erased_at %v", c.name, dto.ErasedAt)
		}
	}
	gone := archivedBookingDTO(&storage.ArchivedBooking{Booking: storage.Booking{BookerName: "", BookerEmail: ""}, BookerErasedAt: &erased})
	raw, _ := json.Marshal(gone)
	if !strings.Contains(string(raw), `"booker_name":null`) || !strings.Contains(string(raw), `"booker_email":null`) ||
		!strings.Contains(string(raw), `"booker_erased_at":"2026-08-01T03:00:00Z"`) {
		t.Errorf("erased booking = %s; want null name and email with the instant", raw)
	}
	kept := archivedBookingDTO(&storage.ArchivedBooking{Booking: storage.Booking{BookerName: "null", BookerEmail: "w@x.test"}})
	raw, _ = json.Marshal(kept)
	if !strings.Contains(string(raw), `"booker_name":"null"`) || !strings.Contains(string(raw), `"booker_erased_at":null`) {
		t.Errorf("kept booking = %s; want the stored strings and no erasure instant", raw)
	}
	if p := bookerErasurePolicy(); !p.Enabled || p.RetentionDays != 90 {
		t.Errorf("policy = %+v; part B ships enabled=true, 90 days", p)
	}
}

// The archive operations are in the bundle this build implements (bundle A):
// read-only GETs, the detail declares 404 and never 403.
func TestPAC40_ContractDeclaresTheArchive(t *testing.T) {
	root := parseBundle(t, pac21Bundle)
	for path, opID := range map[string]string{
		"/api/scheduling-links/archive":      "listArchivedSchedulingLinks",
		"/api/scheduling-links/archive/{id}": "getArchivedSchedulingLink",
	} {
		op := root.get("paths", path, "get")
		if op == nil {
			t.Fatalf("bundle has no GET %s", path)
		}
		if id := op.get("operationId"); id == nil || id.scalar != opID {
			t.Errorf("%s operationId = %v, want %s", path, id, opID)
		}
		if op.get("responses", "403") != nil {
			t.Errorf("%s declares a 403", path)
		}
		for _, m := range []string{"post", "patch", "put", "delete"} {
			if root.get("paths", path, m) != nil {
				t.Errorf("%s declares %s; the archive is read-only", path, m)
			}
		}
	}
	if root.get("paths", "/api/scheduling-links/archive/{id}", "get", "responses", "404") == nil {
		t.Error("getArchivedSchedulingLink declares no 404")
	}
}

// pac40IsolatedDB is a database of its own, migrated to ceiling (0: every
// migration), for tests that bypass a constraint or start below 037.
func pac40IsolatedDB(t *testing.T, ceiling uint) (*sql.DB, string) {
	t.Helper()
	dsn, cleanup, err := testdb.Create()
	if err != nil {
		t.Fatalf("create isolated database: %v", err)
	}
	if ceiling > 0 {
		restore := storage.SetMigrationCeilingForTest(ceiling)
		defer restore()
	}
	db, err := storage.Open(dsn)
	if err != nil {
		_ = cleanup()
		t.Fatalf("open isolated database: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		if err := cleanup(); err != nil {
			t.Errorf("cleanup isolated database: %v", err)
		}
	})
	return db, dsn
}

func pac40RawUser(t *testing.T, db *sql.DB, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := db.QueryRow(`INSERT INTO users (email, name) VALUES ($1, 'PAC-40') RETURNING id`, email).Scan(&id); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

// AC-12 and AC-17a (data-migration tests): rows written at schema 036,
// before PAC-40, read correctly once 037 is applied. A link inactive before
// the change is paused (listed, not archived, resumable, deletable); a
// limit-1 link whose only booking was cancelled before the deploy is used up
// and shows one use.
func TestPAC40_DataMigration037KeepsInactiveLinksPausedAndCountsOldCancellations(t *testing.T) {
	db, dsn := pac40IsolatedDB(t, 36)
	var v int
	if err := db.QueryRow(`SELECT version FROM schema_migrations`).Scan(&v); err != nil || v != 36 {
		t.Fatalf("schema = %d, %v; want 36", v, err)
	}
	owner := pac40RawUser(t, db, "pac40-dm-owner@example.com")
	var inactive, used uuid.UUID
	if err := db.QueryRow(`INSERT INTO scheduling_links (owner_user_id, slug, title, window_start_time, window_end_time, active)
		VALUES ($1, 'pac40-dm-inactive', 'Inactive before 037', '09:00', '17:00', false) RETURNING id`, owner).Scan(&inactive); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO scheduling_links (owner_user_id, slug, title, window_start_time, window_end_time, usage_type, max_uses, active)
		VALUES ($1, 'pac40-dm-used', 'Limit 1', '09:00', '17:00', 'recurring', 1, true) RETURNING id`, owner).Scan(&used); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{inactive, used} {
		if _, err := db.Exec(`INSERT INTO scheduling_link_hosts (link_id, user_id, status) VALUES ($1, $2, 'accepted')`, id, owner); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time, status)
		VALUES ($1, 'Old', 'old@booker.test', '2026-01-05T10:00:00Z', '2026-01-05T10:30:00Z', 'cancelled')`, used); err != nil {
		t.Fatal(err)
	}

	// PAC-116 (OQ-3): pending migrations are applied only while nothing
	// else is connected, so the test closes its pool, waits until its
	// sessions are gone, and reopens the database through the procedure.
	db.Close()
	if err := testdb.WaitNoSessions(dsn, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(dsn)
	if err != nil {
		t.Fatalf("migrate to 037: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.QueryRow(`SELECT version FROM schema_migrations`).Scan(&v); err != nil || v < 37 {
		t.Fatalf("schema = %d, %v; want 037 applied", v, err)
	}
	e := newPAC40Env(t, db)

	// AC-12: listed as paused, not archived.
	owned := decodeOwnedShared(t, e.do(http.MethodGet, "/api/scheduling-links", owner, "").Body)["owned"]
	found := false
	for _, l := range owned {
		if l.ID == inactive.String() {
			found = !l.Active
		}
	}
	if !found {
		t.Fatalf("AC-12: the pre-037 inactive link is not listed as paused: %+v", owned)
	}
	if w := e.do(http.MethodGet, "/api/scheduling-links/archive", owner, ""); !strings.Contains(w.Body.String(), `"links":[]`) {
		t.Fatalf("AC-12: archive = %s; want empty", w.Body.String())
	}
	// AC-17a: used up, one use.
	e.publicGone("pac40-dm-used", "AC-17a")
	if n := e.usesCount(owner, used); n != 1 {
		t.Errorf("AC-17a uses_count = %d; want 1", n)
	}
	// AC-12: resumable, then deletable (and then archived).
	p := "/api/scheduling-links/" + inactive.String()
	e.mustStatus(e.do(http.MethodPatch, p, owner, `{"active":true}`), http.StatusOK, "AC-12 resume")
	e.mustStatus(e.do(http.MethodGet, "/api/book/pac40-dm-inactive", uuid.Nil, ""), http.StatusOK, "AC-12 resumed public page")
	e.deleteLink(owner, inactive)
	if w := e.do(http.MethodGet, "/api/scheduling-links/archive", owner, ""); !strings.Contains(w.Body.String(), inactive.String()) {
		t.Fatalf("AC-12: the deleted link is not archived: %s", w.Body.String())
	}
}
