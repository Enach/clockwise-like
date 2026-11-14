package api

// PAC-101 (contracts/features/PAC-101.md): every write on a scheduling link is
// either before the link's delete and kept in full, or after it and writes
// nothing, answering the existing 404 {"error":"not found"}. Waiting is
// observed in pg_stat_activity, never by sleeping alone.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/internal/lifecycle"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// pac101Flow offers a fixed list of slots and books through the real engine,
// so the save runs the production commit-point step.
type pac101Flow struct {
	eng   *engine.BookingEngine
	offer []engine.AvailableSlot
}

func (f *pac101Flow) CollectiveSlots(context.Context, *storage.SchedulingLink, time.Time, int) ([]engine.AvailableSlot, error) {
	return f.offer, nil
}

func (f *pac101Flow) CollectiveSlotsDays(_ context.Context, _ *storage.SchedulingLink, days []time.Time, _ int) ([][]engine.AvailableSlot, error) {
	out := make([][]engine.AvailableSlot, len(days))
	for i := range days {
		out[i] = f.offer
	}
	return out, nil
}

func (f *pac101Flow) ConfirmBooking(ctx context.Context, link *storage.SchedulingLink, name, email string, start, end time.Time, notes string) (*storage.Booking, error) {
	return f.eng.ConfirmBooking(ctx, link, name, email, start, end, notes)
}

type pac101Env struct {
	*pac40Env
	creates *atomic.Int32
	lists   *atomic.Int32
	group   *lifecycle.Group
	offer   []engine.AvailableSlot
	addr    atomic.Int32
}

// newPAC101Env is the PAC-40 router with the real booking engine, a Google
// stand-in (free calendars; event creations counted) and a shutdown group of
// its own.
func newPAC101Env(t *testing.T) *pac101Env {
	t.Helper()
	var creates, lists atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/events"):
			creates.Add(1)
			_, _ = fmt.Fprintf(w, `{"id":"pac101-%d"}`, creates.Load())
		case strings.HasSuffix(r.URL.Path, "/events"):
			lists.Add(1)
			_, _ = w.Write([]byte(`{"items":[]}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	calendar.Configure(config.CalendarEndpoint{BaseURL: srv.URL + config.CalendarAPIRoot, Guard: true})
	t.Cleanup(func() { calendar.Configure(config.ProductionCalendarEndpoint()) })
	g := lifecycle.NewGroup()
	t.Cleanup(lifecycle.SetDefault(g))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		g.Wait(ctx)
	})

	db := openTestDB(t)
	e := newPAC40Env(t, db)
	var offer []engine.AvailableSlot
	day := futureBookingStart()
	for d := 0; len(offer) < 60; d++ {
		base := day.AddDate(0, 0, d)
		if base.Weekday() == time.Saturday || base.Weekday() == time.Sunday {
			continue
		}
		for i := 0; i < 14; i++ {
			s := time.Date(base.Year(), base.Month(), base.Day(), 9, 0, 0, 0, time.UTC).Add(time.Duration(i) * 30 * time.Minute)
			offer = append(offer, engine.AvailableSlot{Start: s, End: s.Add(30 * time.Minute)})
		}
	}
	bh := newBookingHandlersWithEngine(db, &pac101Flow{eng: &engine.BookingEngine{DB: db, OAuthConfig: &oauth2.Config{}}, offer: offer})
	e.r.Post("/api/book/{slug}/pac101", bh.createBooking)
	env := &pac101Env{pac40Env: e, creates: &creates, lists: &lists, group: g, offer: offer}
	return env
}

// user is a domain-confirmed account with a valid Google token.
func (e *pac101Env) user(email string) uuid.UUID {
	e.t.Helper()
	id := createConfirmedUser(e.t, email)
	if err := auth.UpsertUserToken(e.db, id, &oauth2.Token{AccessToken: "pac101-" + id.String(), RefreshToken: "r", Expiry: time.Now().Add(2 * time.Hour)}); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// book posts a booking of slot i through the real engine, from its own client
// address (the abuse limit is per client).
func (e *pac101Env) book(slug string, i int, email string) *httptest.ResponseRecorder {
	s := e.offer[i].Start
	req := httptest.NewRequest(http.MethodPost, "/api/book/"+slug+"/pac101",
		strings.NewReader(`{"name":"Booker","email":"`+email+`","start":"`+s.Format(time.RFC3339)+`","duration":30}`))
	n := e.addr.Add(1)
	req.RemoteAddr = fmt.Sprintf("10.101.%d.%d:4000", n/250, n%250+1)
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w
}

func (e *pac101Env) bookingsBy(email string) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM bookings WHERE booker_email = $1`, email).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func pac101Waiters(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func pac101AwaitWaiter(t *testing.T, db *sql.DB) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for pac101Waiters(t, db) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no request waited on the link lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// holdDelete runs the owner's delete in a transaction left open.
func (e *pac101Env) holdDelete(id uuid.UUID) *sql.Tx {
	e.t.Helper()
	tx, err := e.db.Begin()
	if err != nil {
		e.t.Fatal(err)
	}
	if ok, err := storage.DeleteSchedulingLink(tx, id); err != nil || !ok {
		e.t.Fatalf("held delete: %v %v", ok, err)
	}
	e.t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

// async sends a request on its own goroutine.
func (e *pac101Env) async(method, path string, user uuid.UUID, body string) <-chan *httptest.ResponseRecorder {
	out := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if user != uuid.Nil {
			req = withUser(req, user)
		}
		w := httptest.NewRecorder()
		e.r.ServeHTTP(w, req)
		out <- w
	}()
	return out
}

func pac101NotFound(t *testing.T, w *httptest.ResponseRecorder, what string) {
	t.Helper()
	if w.Code != http.StatusNotFound || strings.TrimSpace(w.Body.String()) != `{"error":"not found"}` {
		t.Errorf("%s: %d %s; want 404 {\"error\":\"not found\"}", what, w.Code, w.Body.String())
	}
}

// pac101WithinBound runs f on its own goroutine and fails the test fast,
// instead of hanging, when f does not return within the (shortened) link-lock
// bound plus 5 s: a write that ignores the bound waits forever on a held lock.
// The caller's deferred rollback of the holder then releases the goroutine.
func pac101WithinBound[T any](t *testing.T, what string, f func() T) T {
	t.Helper()
	done := make(chan T, 1)
	go func() { done <- f() }()
	select {
	case v := <-done:
		return v
	case <-time.After(storage.LinkLockTimeout + 5*time.Second):
		t.Fatalf("%s did not return within the bound + 5 s: the link-lock bound is not applied", what)
		panic("unreachable")
	}
}

func pac101ShortBound(t *testing.T) {
	t.Helper()
	prev := storage.LinkLockTimeout
	storage.LinkLockTimeout = 300 * time.Millisecond
	t.Cleanup(func() { storage.LinkLockTimeout = prev })
}

// AC-1: a delete committed after the booking request read the link and
// before the host calendars are read: 404, no booking row, no host event, no
// completion unit (so no email).
func TestPAC101_BookingOnALinkDeletedMidRequestIs404(t *testing.T) {
	e := newPAC101Env(t)
	owner := e.user("pac101-ac1-owner@example.com")
	link := createSchedulingLinkRecord(t, owner, "pac101-ac1", "PAC-101 AC-1")
	afterBookingLinkRead = func() {
		if ok, err := storage.DeleteSchedulingLink(e.db, link.ID); err != nil || !ok {
			t.Errorf("inject delete: %v %v", ok, err)
		}
	}
	defer func() { afterBookingLinkRead = nil }()
	w := e.book(link.Slug, 0, "ac1@booker.test")
	pac101NotFound(t, w, "booking racing a delete")
	if e.lists.Load() == 0 {
		t.Error("the host calendar was never read: the injection was not before the reads")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	e.group.Wait(ctx)
	if n := e.bookingCount(link.ID); n != 0 {
		t.Errorf("%d bookings stored", n)
	}
	if c := e.creates.Load(); c != 0 {
		t.Errorf("%d host events created", c)
	}
	if p := e.group.Pending(); len(p) != 0 {
		t.Errorf("completion units: %v", p)
	}
}

// Sanity of the harness: the same booking with no race is a 201 with a host
// event, so the 404 above is the race and not the setup.
func TestPAC101_BookingWithoutARaceIs201(t *testing.T) {
	e := newPAC101Env(t)
	owner := e.user("pac101-ok-owner@example.com")
	link := createSchedulingLinkRecord(t, owner, "pac101-ok", "PAC-101 OK")
	e.mustStatus(e.book(link.Slug, 1, "ok@booker.test"), http.StatusCreated, "booking")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e.group.Wait(ctx)
	if e.creates.Load() != 1 || e.bookingCount(link.ID) != 1 {
		t.Fatalf("creates %d, bookings %d; want 1 and 1", e.creates.Load(), e.bookingCount(link.ID))
	}
}

// AC-7: a write on the link held past the (shortened) bound: the booking
// answers 500 {"error":"internal error"} and stores nothing.
func TestPAC101_BookingPastTheBoundIs500AndStoresNothing(t *testing.T) {
	pac101ShortBound(t)
	e := newPAC101Env(t)
	owner := e.user("pac101-ac7-owner@example.com")
	link := createSchedulingLinkRecord(t, owner, "pac101-ac7", "PAC-101 AC-7")
	holder, err := e.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback() }()
	if _, err := holder.Exec(`UPDATE scheduling_links SET title = title WHERE id = $1`, link.ID); err != nil {
		t.Fatal(err)
	}
	w := pac101WithinBound(t, "the booking", func() *httptest.ResponseRecorder { return e.book(link.Slug, 2, "ac7@booker.test") })
	if w.Code != http.StatusInternalServerError || strings.TrimSpace(w.Body.String()) != `{"error":"internal error"}` {
		t.Fatalf("status %d %s; want 500 internal error", w.Code, w.Body.String())
	}
	_ = holder.Rollback()
	if n := e.bookingCount(link.ID); n != 0 {
		t.Fatalf("%d bookings stored past the bound", n)
	}
}

// guarded returns a request for user carrying a current session guard, so the
// write runs under the PAC-71 re-check (and the InstallGuardedCallHook seam).
func (e *pac101Env) guarded(method, path string, user uuid.UUID, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	gen, ok, err := storage.CurrentGeneration(e.db, user)
	if err != nil || !ok {
		e.t.Fatalf("generation: %v %v", ok, err)
	}
	req := withUser(httptest.NewRequest(method, path, strings.NewReader(body)), user)
	req = req.WithContext(storage.WithSessionGuard(req.Context(), &storage.SessionGuard{UserID: user, Gen: gen}))
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w
}

// AC-8: a delete committed after the invite's read and ownership check and
// before the host row is written: 404 not found, no host row for the invitee.
func TestPAC101_InviteRacingADeleteIs404AndWritesNothing(t *testing.T) {
	e := newPAC101Env(t)
	owner := e.user("pac101-ac8-owner@example.com")
	invitee := e.user("pac101-ac8-invitee@example.com")
	link := createSchedulingLinkRecord(t, owner, "pac101-ac8", "PAC-101 AC-8")
	var once sync.Once
	defer storage.InstallGuardedCallHook(t, func(context.Context) {
		once.Do(func() {
			if ok, err := storage.DeleteSchedulingLink(e.db, link.ID); err != nil || !ok {
				t.Errorf("inject delete: %v %v", ok, err)
			}
		})
	})()
	w := e.guarded(http.MethodPost, "/api/scheduling-links/"+link.ID.String()+"/hosts", owner, `{"email":"pac101-ac8-invitee@example.com"}`)
	pac101NotFound(t, w, "invite racing a delete")
	if _, found := e.hostRows(link.ID)[invitee]; found {
		t.Fatal("a host row was written on a deleted link")
	}
}

// AC-9: a delete held open; the invite waits. Commit: 404, no row. Rollback:
// 201 with the pending row. Past the bound: 500, no row.
func TestPAC101_InviteWaitsForAHeldDelete(t *testing.T) {
	e := newPAC101Env(t)
	owner := e.user("pac101-ac9-owner@example.com")
	invitee := e.user("pac101-ac9-invitee@example.com")
	body := `{"email":"pac101-ac9-invitee@example.com"}`
	for _, outcome := range []string{"commit", "rollback", "bound"} {
		link := createSchedulingLinkRecord(t, owner, "pac101-ac9-"+outcome, "PAC-101 AC-9")
		path := "/api/scheduling-links/" + link.ID.String() + "/hosts"
		if outcome == "bound" {
			pac101ShortBound(t)
		}
		tx := e.holdDelete(link.ID)
		got := e.async(http.MethodPost, path, owner, body)
		pac101AwaitWaiter(t, e.db)
		var w *httptest.ResponseRecorder
		switch outcome {
		case "commit":
			_ = tx.Commit()
			w = <-got
			pac101NotFound(t, w, "invite after the delete committed")
		case "rollback":
			_ = tx.Rollback()
			w = <-got
			e.mustStatus(w, http.StatusCreated, "invite after the delete rolled back")
			if e.hostRows(link.ID)[invitee] != "pending" {
				t.Fatal("no pending row after the rollback")
			}
			continue
		case "bound":
			w = pac101WithinBound(t, "the invite", func() *httptest.ResponseRecorder { return <-got })
			_ = tx.Rollback()
			if w.Code != http.StatusInternalServerError {
				t.Errorf("invite past the bound: %d %s; want 500", w.Code, w.Body.String())
			}
		}
		if _, found := e.hostRows(link.ID)[invitee]; found {
			t.Errorf("%s: a host row was written", outcome)
		}
	}
}

// AC-10: a PATCH setting co-hosts [C] (B accepted); a delete started after
// the PATCH's link UPDATE waits until the PATCH commits. The PATCH is 200, the
// delete 204; afterwards the link is deleted with the PATCH's fields and host
// rows exactly owner + C pending; nothing changes after the delete.
func TestPAC101_DeleteDuringAPatchWaitsForItsCoHosts(t *testing.T) {
	e := newPAC101Env(t)
	owner := e.user("pac101-ac10-owner@example.com")
	b := e.user("pac101-ac10-b@example.com")
	c := e.user("pac101-ac10-c@example.com")
	link := createSchedulingLinkRecord(t, owner, "pac101-ac10", "PAC-101 AC-10")
	if _, err := storage.AddLinkHost(e.db, link.ID, b, "accepted"); err != nil {
		t.Fatal(err)
	}
	p := "/api/scheduling-links/" + link.ID.String()
	var deleted <-chan *httptest.ResponseRecorder
	uninstall := storage.InstallAfterLinkPatchUpdate(t, func(uuid.UUID) error {
		deleted = e.async(http.MethodDelete, p, owner, "")
		pac101AwaitWaiter(t, e.db)
		return nil
	})
	w := e.do(http.MethodPatch, p, owner, `{"title":"patched","co_host_emails":["pac101-ac10-c@example.com"]}`)
	e.mustStatus(w, http.StatusOK, "PATCH")
	uninstall()
	e.mustStatus(<-deleted, http.StatusNoContent, "delete that waited")
	at, active := e.linkRow(link.ID)
	if !at.Valid || active {
		t.Fatalf("link not deleted: %v %v", at, active)
	}
	var title string
	_ = e.db.QueryRow(`SELECT title FROM scheduling_links WHERE id = $1`, link.ID).Scan(&title)
	if title != "patched" {
		t.Errorf("title = %q, want the PATCH's", title)
	}
	hosts := e.hostRows(link.ID)
	if len(hosts) != 2 || hosts[owner] != "accepted" || hosts[c] != "pending" {
		t.Errorf("host rows = %v; want owner accepted and C pending", hosts)
	}
}

// AC-11 with co-hosts: a delete committed after the PATCH's read and before
// its first write: 404, and neither the fields nor any host row change.
func TestPAC101_PatchAfterADeleteChangesNoCoHost(t *testing.T) {
	e := newPAC101Env(t)
	owner := e.user("pac101-ac11-owner@example.com")
	b := e.user("pac101-ac11-b@example.com")
	e.user("pac101-ac11-c@example.com")
	link := createSchedulingLinkRecord(t, owner, "pac101-ac11", "PAC-101 AC-11")
	if _, err := storage.AddLinkHost(e.db, link.ID, b, "accepted"); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	defer storage.InstallGuardedCallHook(t, func(context.Context) {
		once.Do(func() {
			if ok, err := storage.DeleteSchedulingLink(e.db, link.ID); err != nil || !ok {
				t.Errorf("inject delete: %v %v", ok, err)
			}
		})
	})()
	w := e.guarded(http.MethodPatch, "/api/scheduling-links/"+link.ID.String(), owner,
		`{"title":"x","co_host_emails":["pac101-ac11-c@example.com"]}`)
	pac101NotFound(t, w, "PATCH after a delete")
	hosts := e.hostRows(link.ID)
	if len(hosts) != 2 || hosts[b] != "accepted" {
		t.Errorf("host rows = %v; the PATCH changed co-hosts of a deleted link", hosts)
	}
}

// AC-12: a co-host step fails: 500 internal error, neither fields nor host
// rows change (a failed lookup or write is no longer a silent 200).
func TestPAC101_FailedCoHostStepChangesNothing(t *testing.T) {
	e := newPAC101Env(t)
	owner := e.user("pac101-ac12-owner@example.com")
	b := e.user("pac101-ac12-b@example.com")
	e.user("pac101-ac12-c@example.com")
	link := createSchedulingLinkRecord(t, owner, "pac101-ac12", "PAC-101 AC-12")
	if _, err := storage.AddLinkHost(e.db, link.ID, b, "accepted"); err != nil {
		t.Fatal(err)
	}
	storage.InstallAfterLinkPatchUpdate(t, func(uuid.UUID) error { return errors.New("pac101: co-host step failed") })
	w := e.do(http.MethodPatch, "/api/scheduling-links/"+link.ID.String(), owner,
		`{"title":"changed","active":false,"co_host_emails":["pac101-ac12-c@example.com"]}`)
	if w.Code != http.StatusInternalServerError || strings.TrimSpace(w.Body.String()) != `{"error":"internal error"}` {
		t.Fatalf("status %d %s; want 500 internal error", w.Code, w.Body.String())
	}
	var title string
	var active bool
	_ = e.db.QueryRow(`SELECT title, active FROM scheduling_links WHERE id = $1`, link.ID).Scan(&title, &active)
	if title != link.Title || !active {
		t.Errorf("fields changed: %q active %v", title, active)
	}
	hosts := e.hostRows(link.ID)
	if len(hosts) != 2 || hosts[b] != "accepted" {
		t.Errorf("host rows = %v", hosts)
	}
}

// AC-13: with a delete held open, accept and decline (both spellings) and
// leave each wait. Commit: 404, host row unchanged. Rollback: as today.
func TestPAC101_RespondAndLeaveWaitForAHeldDelete(t *testing.T) {
	e := newPAC101Env(t)
	owner := e.user("pac101-ac13-owner@example.com")
	cohost := e.user("pac101-ac13-cohost@example.com")
	cases := []struct {
		name, path, initial string
		wantCode            int
		wantBody            string
		after               string // host status after a rollback; "" = removed
	}{
		{"accept", "/api/scheduling-links/host-invites/{id}/accept", "pending", http.StatusOK, `{"status":"accepted"}`, "accepted"},
		{"decline", "/api/scheduling-links/host-invites/{id}/decline", "pending", http.StatusOK, `{"status":"declined"}`, "declined"},
		{"accept-by-link", "/api/scheduling-links/{id}/accept", "pending", http.StatusOK, `{"status":"accepted"}`, "accepted"},
		{"decline-by-link", "/api/scheduling-links/{id}/decline", "pending", http.StatusOK, `{"status":"declined"}`, "declined"},
		{"leave", "/api/scheduling-links/{id}/leave", "accepted", http.StatusNoContent, "", ""},
	}
	for _, c := range cases {
		for _, commit := range []bool{true, false} {
			link := createSchedulingLinkRecord(t, owner, "pac101-ac13-"+uuid.NewString()[:8], "PAC-101 AC-13")
			if _, err := storage.AddLinkHost(e.db, link.ID, cohost, c.initial); err != nil {
				t.Fatal(err)
			}
			tx := e.holdDelete(link.ID)
			got := e.async(http.MethodPost, strings.ReplaceAll(c.path, "{id}", link.ID.String()), cohost, "")
			pac101AwaitWaiter(t, e.db)
			if commit {
				_ = tx.Commit()
				pac101NotFound(t, <-got, c.name+" after the delete committed")
				if s := e.hostRows(link.ID)[cohost]; s != c.initial {
					t.Errorf("%s: host row %q, want unchanged %q", c.name, s, c.initial)
				}
				continue
			}
			_ = tx.Rollback()
			w := <-got
			if w.Code != c.wantCode || strings.TrimSpace(w.Body.String()) != c.wantBody {
				t.Errorf("%s after a rollback: %d %s; want %d %s", c.name, w.Code, w.Body.String(), c.wantCode, c.wantBody)
			}
			if s := e.hostRows(link.ID)[cohost]; s != c.after {
				t.Errorf("%s after a rollback: host row %q, want %q", c.name, s, c.after)
			}
		}
	}
}

// AC-14 (API-072): the invite's two 404 bodies differ.
func TestPAC101_InviteNotFoundBodiesDiffer(t *testing.T) {
	e := newPAC101Env(t)
	owner := e.user("pac101-ac14-owner@example.com")
	link := createSchedulingLinkRecord(t, owner, "pac101-ac14", "PAC-101 AC-14")
	gone := createSchedulingLinkRecord(t, owner, "pac101-ac14-gone", "PAC-101 AC-14 gone")
	e.deleteLink(owner, gone.ID)
	e.user("pac101-ac14-invitee@example.com")
	for _, id := range []uuid.UUID{uuid.New(), gone.ID} {
		w := e.do(http.MethodPost, "/api/scheduling-links/"+id.String()+"/hosts", owner, `{"email":"pac101-ac14-invitee@example.com"}`)
		pac101NotFound(t, w, "invite on an unknown or deleted link")
	}
	w := e.do(http.MethodPost, "/api/scheduling-links/"+link.ID.String()+"/hosts", owner, `{"email":"nobody-pac101@example.com"}`)
	if w.Code != http.StatusNotFound || strings.TrimSpace(w.Body.String()) != `{"error":"user not found"}` {
		t.Fatalf("unknown invitee: %d %s; want 404 user not found", w.Code, w.Body.String())
	}
}

// AC-15: rounds of concurrent writes on one link (bookings of distinct free
// slots, invites, PATCHes of fields and co-hosts, accept, decline, leave,
// pause, resume, and finally a delete): no 500 (so no 40P01 deadlock and no
// 55P03 timeout); every booking answered 201 exists and is in the archive,
// every booking answered 404 does not exist.
func TestPAC101_ConcurrentWritesOnOneLinkNeverDeadlock(t *testing.T) {
	e := newPAC101Env(t)
	owner := e.user("pac101-ac15-owner@example.com")
	b := e.user("pac101-ac15-b@example.com")
	c := e.user("pac101-ac15-c@example.com")
	d := e.user("pac101-ac15-d@example.com")
	// Unique per run: the api package shares one database, and -count=N
	// reruns this test against it.
	sfx := uuid.NewString()[:8]
	link := createSchedulingLinkRecord(t, owner, "pac101-ac15-"+sfx, "PAC-101 AC-15")
	if _, err := storage.AddLinkHost(e.db, link.ID, b, "accepted"); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.AddLinkHost(e.db, link.ID, c, "pending"); err != nil {
		t.Fatal(err)
	}
	p := "/api/scheduling-links/" + link.ID.String()
	type booked struct {
		email string
		code  int
	}
	var mu sync.Mutex
	var bookings []booked
	var failures []string
	slot := 0
	const rounds = 4
	createdIn := make([]int, rounds)
	for round := 0; round < rounds; round++ {
		// Every round starts live. A title-only PATCH writes back the active
		// flag it read, so without this a round can begin paused (the previous
		// round's pause committing last), and every booking then reads no live
		// link: the flake where no booking succeeded.
		if round > 0 {
			e.mustStatus(e.do(http.MethodPatch, p, owner, `{"active":true}`), http.StatusOK, "round start resume")
		}
		// The pause, resume and delete of a round wait until one of its
		// bookings has committed (or all of them have answered), so each round
		// stores at least one booking while the other writes still race it.
		firstSaved := make(chan struct{})
		var once sync.Once
		release := func() { once.Do(func() { close(firstSaved) }) }
		var roundBookings sync.WaitGroup
		var wg sync.WaitGroup
		run := func(what string, gated bool, f func() *httptest.ResponseRecorder) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if gated {
					<-firstSaved
				}
				w := f()
				if w.Code >= 500 {
					mu.Lock()
					failures = append(failures, fmt.Sprintf("round %d %s: %d %s", round, what, w.Code, w.Body.String()))
					mu.Unlock()
				}
			}()
		}
		for i := 0; i < 4; i++ {
			i, email, r := slot, fmt.Sprintf("r%d-%d-%s@booker.test", round, slot, sfx), round
			slot++
			roundBookings.Add(1)
			run("booking", false, func() *httptest.ResponseRecorder {
				defer roundBookings.Done()
				w := e.book(link.Slug, i, email)
				mu.Lock()
				bookings = append(bookings, booked{email, w.Code})
				if w.Code == http.StatusCreated {
					createdIn[r]++
				}
				mu.Unlock()
				if w.Code == http.StatusCreated {
					release()
				}
				return w
			})
		}
		go func() { roundBookings.Wait(); release() }()
		emails := `["pac101-ac15-b@example.com","pac101-ac15-c@example.com"]`
		if round%2 == 1 {
			emails = `["pac101-ac15-b@example.com","pac101-ac15-c@example.com","pac101-ac15-d@example.com"]`
		}
		run("invite", false, func() *httptest.ResponseRecorder {
			return e.do(http.MethodPost, p+"/hosts", owner, `{"email":"pac101-ac15-d@example.com"}`)
		})
		run("patch", false, func() *httptest.ResponseRecorder {
			return e.do(http.MethodPatch, p, owner, fmt.Sprintf(`{"title":"round %d","co_host_emails":%s}`, round, emails))
		})
		run("accept", false, func() *httptest.ResponseRecorder { return e.do(http.MethodPost, p+"/accept", c, "") })
		run("decline", false, func() *httptest.ResponseRecorder { return e.do(http.MethodPost, p+"/decline", d, "") })
		run("leave", false, func() *httptest.ResponseRecorder { return e.do(http.MethodPost, p+"/leave", d, "") })
		run("pause", true, func() *httptest.ResponseRecorder { return e.do(http.MethodPatch, p, owner, `{"active":false}`) })
		run("resume", true, func() *httptest.ResponseRecorder { return e.do(http.MethodPatch, p, owner, `{"active":true}`) })
		if round == rounds-1 {
			run("delete", true, func() *httptest.ResponseRecorder { return e.do(http.MethodDelete, p, owner, "") })
		}
		wg.Wait()
	}
	for _, f := range failures {
		t.Error(f)
	}
	if at, _ := e.linkRow(link.ID); !at.Valid {
		t.Fatal("the final delete did not delete the link")
	}
	archived, err := storage.GetArchivedBookings(e.db, link.ID)
	if err != nil {
		t.Fatal(err)
	}
	inArchive := map[string]bool{}
	for _, a := range archived {
		inArchive[a.BookerEmail] = true
	}
	created := 0
	for _, bk := range bookings {
		switch bk.code {
		case http.StatusCreated:
			created++
			if e.bookingsBy(bk.email) != 1 || !inArchive[bk.email] {
				t.Errorf("booking %s answered 201 but is not stored and archived", bk.email)
			}
		case http.StatusNotFound:
			if e.bookingsBy(bk.email) != 0 {
				t.Errorf("booking %s answered 404 but is stored", bk.email)
			}
		default:
			t.Errorf("booking %s answered %d", bk.email, bk.code)
		}
	}
	for r, n := range createdIn {
		if n == 0 {
			t.Errorf("round %d: no booking succeeded although the round started live and its pause and delete waited", r)
		}
	}
	if created < rounds {
		t.Errorf("%d bookings succeeded, want at least one per round (%d)", created, rounds)
	}
}

// The handler maps the engine's link-gone sentinel before its 500 branch.
func TestPAC101_LinkGoneSentinelIsThe404(t *testing.T) {
	db := openTestDB(t)
	resetBookingLimiters(t)
	owner := createConfirmedUser(t, "pac101-sentinel-owner@example.com")
	link := createSchedulingLinkRecord(t, owner, "pac101-sentinel", "PAC-101 sentinel")
	start := futureBookingStart()
	bh := newBookingHandlersWithEngine(db, &goneFlow{offer: []engine.AvailableSlot{{Start: start, End: start.Add(30 * time.Minute)}}})
	r := chi.NewRouter()
	r.Post("/api/book/{slug}", bh.createBooking)
	req := httptest.NewRequest(http.MethodPost, "/api/book/"+link.Slug, strings.NewReader(`{"name":"N","email":"n@booker.test","start":"`+start.Format(time.RFC3339)+`","duration":30}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	pac101NotFound(t, w, "ConfirmBooking returning ErrLinkGone")
}

type goneFlow struct{ offer []engine.AvailableSlot }

func (f *goneFlow) CollectiveSlots(context.Context, *storage.SchedulingLink, time.Time, int) ([]engine.AvailableSlot, error) {
	return f.offer, nil
}

func (f *goneFlow) CollectiveSlotsDays(_ context.Context, _ *storage.SchedulingLink, days []time.Time, _ int) ([][]engine.AvailableSlot, error) {
	out := make([][]engine.AvailableSlot, len(days))
	for i := range days {
		out[i] = f.offer
	}
	return out, nil
}

func (f *goneFlow) ConfirmBooking(context.Context, *storage.SchedulingLink, string, string, time.Time, time.Time, string) (*storage.Booking, error) {
	return nil, engine.ErrLinkGone
}
