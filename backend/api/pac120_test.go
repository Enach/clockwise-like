package api

// PAC-120 (contracts/features/PAC-120.md): a link has at most 5 co-hosts on
// the single invitation too, decided under the link lock inside the write;
// a failed address lookup is the 500 "invite lookup" (SF-068) and a failed
// link read is logged and reported as "invite link read" (SF-101); and the
// PAC-81 body cap is proven wired in the production route chain (AC-1, AC-2).
// Waits are observed in pg_stat_activity, never by sleeping alone.

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

const pac120LimitBody = `{"error":"this link has reached its limit of 5 co-hosts; remove a co-host before inviting another"}`

type pac120Env struct {
	*pac101Env
}

func newPAC120Env(t *testing.T) *pac120Env {
	t.Helper()
	return &pac120Env{newPAC101Env(t)}
}

func pac120Addr(name string) string {
	return fmt.Sprintf("pac120-%s-%s@acme.test", name, uuid.NewString()[:8])
}

// person is a domain-confirmed account with a fresh address.
func (e *pac120Env) person(name string) (uuid.UUID, string) {
	e.t.Helper()
	email := pac120Addr(name)
	return e.user(email), email
}

// link is a live link owned by owner with the owner's row at ownerStatus
// ("" seeds none) and one co-host per status given.
func (e *pac120Env) link(owner uuid.UUID, ownerStatus string, statuses ...string) (*storage.SchedulingLink, []uuid.UUID, []string) {
	e.t.Helper()
	l := createSchedulingLinkRecord(e.t, owner, "pac120-"+uuid.NewString()[:8], "PAC-120")
	if ownerStatus == "" {
		// A link created before PAC-112 has no owner row (spec §2.5).
		execT(e.t, `DELETE FROM scheduling_link_hosts WHERE link_id = $1`, l.ID)
	} else if _, err := storage.AddLinkHost(e.db, l.ID, owner, ownerStatus); err != nil {
		e.t.Fatal(err)
	}
	ids := make([]uuid.UUID, 0, len(statuses))
	emails := make([]string, 0, len(statuses))
	for i, s := range statuses {
		id, email := e.person(fmt.Sprintf("co%d", i))
		if _, err := storage.AddLinkHost(e.db, l.ID, id, s); err != nil {
			e.t.Fatal(err)
		}
		if s != "pending" {
			execT(e.t, `UPDATE scheduling_link_hosts SET responded_at = now() - interval '1 hour' WHERE link_id = $1 AND user_id = $2`, l.ID, id)
		}
		ids = append(ids, id)
		emails = append(emails, email)
	}
	return l, ids, emails
}

func pac120Statuses(n int) []string {
	cycle := []string{"pending", "accepted", "declined"}
	out := make([]string, n)
	for i := range out {
		out[i] = cycle[i%len(cycle)]
	}
	return out
}

func (e *pac120Env) invite(owner, link uuid.UUID, email string) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.do(http.MethodPost, "/api/scheduling-links/"+link.String()+"/hosts", owner, `{"email":"`+email+`"}`)
}

// snapshot is every host row of the link, in a fixed order, with every
// column an invitation could change.
func (e *pac120Env) snapshot(link uuid.UUID) string {
	e.t.Helper()
	rows, err := e.db.Query(`SELECT user_id::text, status, invited_at, responded_at FROM scheduling_link_hosts
		WHERE link_id = $1 ORDER BY user_id::text`, link)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var u, s string
		var inv time.Time
		var resp sql.NullTime
		if err := rows.Scan(&u, &s, &inv, &resp); err != nil {
			e.t.Fatal(err)
		}
		fmt.Fprintf(&b, "%s %s %s %v %s;", u, s, inv.UTC().Format(time.RFC3339Nano), resp.Valid, resp.Time.UTC().Format(time.RFC3339Nano))
	}
	if err := rows.Err(); err != nil {
		e.t.Fatal(err)
	}
	return b.String()
}

// coHosts counts the link's non-owner host rows, any status.
func (e *pac120Env) coHosts(link *storage.SchedulingLink) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT count(*) FROM scheduling_link_hosts WHERE link_id = $1 AND user_id <> $2`,
		link.ID, link.OwnerUserID).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// hostRow is userID's host row on link as status and responded_at (the
// columns accept, decline and leave write), or "absent".
func (e *pac120Env) hostRow(link, userID uuid.UUID) string {
	e.t.Helper()
	var s string
	var resp sql.NullTime
	err := e.db.QueryRow(`SELECT status, responded_at FROM scheduling_link_hosts WHERE link_id = $1 AND user_id = $2`,
		link, userID).Scan(&s, &resp)
	if errors.Is(err, sql.ErrNoRows) {
		return "absent"
	}
	if err != nil {
		e.t.Fatal(err)
	}
	if !resp.Valid {
		return s + " responded_at=null"
	}
	return s + " responded_at=" + resp.Time.UTC().Format(time.RFC3339Nano)
}

func pac120Is422(t *testing.T, w *httptest.ResponseRecorder, what string) {
	t.Helper()
	if w.Code != http.StatusUnprocessableEntity || strings.TrimSpace(w.Body.String()) != pac120LimitBody {
		t.Errorf("%s: %d %s; want 422 %s", what, w.Code, w.Body.String(), pac120LimitBody)
	}
}

// pac120LockWaiters counts sessions of this database waiting on a lock on
// a statement that touches scheduling_links (the link lock).
func pac120LockWaiters(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pg_stat_activity WHERE datname = current_database()
		AND wait_event_type = 'Lock' AND query ILIKE '%scheduling_links%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// pac120Gate is installed in afterLinkInviteLock for the link under test:
// each invitation that reaches the seam (it holds the link lock) waits until
// the other invitation of the round has also reached it, or is observed
// waiting on the link lock. Under a lock that orders invitations the first
// sees the second waiting and goes on; under one that does not (FOR SHARE)
// both reach the seam and are released at the same instant, so both decide
// before either commits: the race AC-9 and AC-5b falsify.
type pac120Gate struct {
	mu      sync.Mutex
	link    uuid.UUID
	arrived int
	both    chan struct{}
}

func (g *pac120Gate) install(t *testing.T, db *sql.DB) {
	storage.InstallAfterLinkInviteLock(t, func(_ context.Context, linkID uuid.UUID) error {
		g.mu.Lock()
		if g.link != linkID {
			g.mu.Unlock()
			return nil
		}
		g.arrived++
		both := g.both
		if g.arrived == 2 {
			close(both)
		}
		g.mu.Unlock()
		deadline := time.Now().Add(5 * time.Second)
		for {
			select {
			case <-both:
				return nil
			case <-time.After(5 * time.Millisecond):
			}
			if pac120LockWaiters(t, db) > 0 {
				return nil
			}
			if time.Now().After(deadline) {
				t.Error("the other invitation neither reached the seam nor waited on the link lock")
				return nil
			}
		}
	})
}

func (g *pac120Gate) arm(id uuid.UUID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.link, g.arrived, g.both = id, 0, make(chan struct{})
}

// together sends the requests from a barrier and returns their answers in
// order.
func pac120Together(fs ...func() *httptest.ResponseRecorder) []*httptest.ResponseRecorder {
	out := make([]*httptest.ResponseRecorder, len(fs))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, f := range fs {
		wg.Add(1)
		go func(i int, f func() *httptest.ResponseRecorder) {
			defer wg.Done()
			<-start
			out[i] = f()
		}(i, f)
	}
	close(start)
	wg.Wait()
	return out
}

// --- AC-1, AC-2: the body cap in the production chain -----------------------

func pac120ProductionMux(t *testing.T) *chi.Mux {
	t.Helper()
	r := chi.NewRouter()
	RegisterRoutes(r, openTestDB(t), &oauth2.Config{}, pac71Secret, guardOrigin, pac35Frontend)
	return r
}

func pac120Assert413(t *testing.T, w *httptest.ResponseRecorder, what string) {
	t.Helper()
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("%s: status %d %q, want 413", what, w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("%s: Content-Type %q", what, ct)
	}
	if got := w.Body.String(); got != "{\"error\":\"request body too large\"}\n" {
		t.Errorf("%s: body %q", what, got)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != guardOrigin {
		t.Errorf("%s: Access-Control-Allow-Origin %q, want %q", what, got, guardOrigin)
	}
}

// AC-1: the cap answers before requireAuth on a protected route, and a route
// behind requireAuth on the same mux receives exactly 1 MiB whole.
func TestPAC120_AC1_BodyCapRunsBeforeRequireAuth(t *testing.T) {
	db := openTestDB(t)
	r := pac120ProductionMux(t)
	var entered atomic.Int32
	// Registered after RegisterRoutes on the same mux: chi applies the
	// mux's Use stack (CORS, the cap, the budget) to it.
	r.With(requireAuth(db, pac71Secret)).Post("/api/pac120-echo", func(w http.ResponseWriter, req *http.Request) {
		entered.Add(1)
		b, err := io.ReadAll(req.Body)
		if err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = fmt.Fprintf(w, "%d", len(b))
	})
	for _, path := range []string{"/api/scheduling-links/", "/api/pac120-echo"} {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(make([]byte, 1048577)))
		req.Header.Set("Origin", guardOrigin)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		pac120Assert413(t, w, path+" without a session")
	}
	if n := entered.Load(); n != 0 {
		t.Fatalf("the echo handler ran %d times for an oversized body", n)
	}

	user := createConfirmedUser(t, pac120Addr("ac1"))
	g, ok, err := storage.CurrentGeneration(db, user)
	if err != nil || !ok {
		t.Fatalf("generation: %v %v", ok, err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/pac120-echo", bytes.NewReader(bytes.Repeat([]byte{'a'}, 1048576)))
	req.Header.Set("Origin", guardOrigin)
	req.AddCookie(sessionCookie(t, user, g))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "1048576" {
		t.Fatalf("exactly 1 MiB with a session: %d %q, want 200 \"1048576\"", w.Code, w.Body.String())
	}
	if entered.Load() != 1 {
		t.Fatalf("echo handler entered %d times", entered.Load())
	}
}

// AC-2: the public booking route answers the same 413 before its handler.
func TestPAC120_AC2_BookingBodyCapBeforeTheHandler(t *testing.T) {
	db := openTestDB(t)
	resetBookingLimiters(t)
	r := pac120ProductionMux(t)
	owner := createConfirmedUser(t, pac120Addr("ac2-owner"))
	link := createSchedulingLinkRecord(t, owner, "pac120-ac2-"+uuid.NewString()[:8], "PAC-120 AC-2")
	var fired atomic.Int32
	afterBookingLinkRead = func() { fired.Add(1) }
	defer func() { afterBookingLinkRead = nil }()
	req := httptest.NewRequest(http.MethodPost, "/api/book/"+link.Slug, bytes.NewReader(make([]byte, 1048577)))
	req.Header.Set("Origin", guardOrigin)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	pac120Assert413(t, w, "booking of 1 MiB + 1")
	if fired.Load() != 0 {
		t.Error("the booking handler read the link for an oversized body")
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM bookings WHERE link_id = $1`, link.ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("bookings for the link: %d %v", n, err)
	}
}

// --- AC-3 .. AC-8: the limit -------------------------------------------------

// AC-3: 5 co-hosts of mixed statuses; a sixth is the 422 and nothing changes.
func TestPAC120_AC3_SixthInvitationIs422AndWritesNothing(t *testing.T) {
	e := newPAC120Env(t)
	owner, _ := e.person("ac3-owner")
	l, _, _ := e.link(owner, "accepted", "pending", "pending", "accepted", "accepted", "declined")
	invitee, email := e.person("ac3-sixth")
	before := e.snapshot(l.ID)
	w := e.invite(owner, l.ID, email)
	pac120Is422(t, w, "sixth invitation")
	if pac120LimitBody == pac112CapMsg {
		t.Fatal("the refusal reuses the list's message")
	}
	if got := strings.TrimSpace(w.Body.String()); got != `{"error":"`+string(gen.SchedulingLinkHostLimitReached)+`"}` {
		t.Errorf("body %s is not the contract's enum value", got)
	}
	if after := e.snapshot(l.ID); after != before {
		t.Errorf("host rows changed:\n before %s\n after  %s", before, after)
	}
	if _, found := e.hostRows(l.ID)[invitee]; found {
		t.Error("the refused invitee has a row")
	}
}

// AC-4: 4 co-hosts; a fifth is 201 pending.
func TestPAC120_AC4_FifthInvitationIs201(t *testing.T) {
	e := newPAC120Env(t)
	owner, _ := e.person("ac4-owner")
	l, _, _ := e.link(owner, "accepted", pac120Statuses(4)...)
	invitee, email := e.person("ac4-fifth")
	e.mustStatus(e.invite(owner, l.ID, email), http.StatusCreated, "fifth invitation")
	if e.hostRows(l.ID)[invitee] != "pending" {
		t.Fatal("no pending row for the fifth")
	}
	if n := e.coHosts(l); n != 5 {
		t.Errorf("%d co-hosts, want 5", n)
	}
}

// AC-5: at 5 and at 7 co-hosts, re-inviting a co-host of each status is 200
// with the row unchanged and nothing added.
func TestPAC120_AC5_ReinviteAtOrAboveTheLimitIs200(t *testing.T) {
	e := newPAC120Env(t)
	owner, _ := e.person("ac5-owner")
	for _, n := range []int{5, 7} {
		l, ids, emails := e.link(owner, "accepted", pac120Statuses(n)...)
		for i := 0; i < 3; i++ { // pending, accepted, declined
			before := e.snapshot(l.ID)
			w := e.invite(owner, l.ID, emails[i])
			e.mustStatus(w, http.StatusOK, fmt.Sprintf("re-invite at %d of a %s co-host", n, pac120Statuses(n)[i]))
			if !strings.Contains(w.Body.String(), ids[i].String()) {
				t.Errorf("the 200 is not the existing row: %s", w.Body.String())
			}
			if after := e.snapshot(l.ID); after != before {
				t.Errorf("at %d, re-invite of %s changed rows:\n %s\n %s", n, pac120Statuses(n)[i], before, after)
			}
		}
	}
}

// AC-5b: two simultaneous invitations of one new address at 4 co-hosts: one
// 201 and one 200, every round.
func TestPAC120_AC5b_SameAddressTwiceIs201And200(t *testing.T) {
	e := newPAC120Env(t)
	owner, _ := e.person("ac5b-owner")
	var gate pac120Gate
	gate.install(t, e.db)
	for round := 0; round < 20; round++ {
		l, _, _ := e.link(owner, "accepted", pac120Statuses(4)...)
		_, email := e.person("ac5b-x")
		gate.arm(l.ID)
		ws := pac120Together(
			func() *httptest.ResponseRecorder { return e.invite(owner, l.ID, email) },
			func() *httptest.ResponseRecorder { return e.invite(owner, l.ID, email) },
		)
		codes := []int{ws[0].Code, ws[1].Code}
		sort.Ints(codes)
		if codes[0] != http.StatusOK || codes[1] != http.StatusCreated {
			t.Errorf("round %d: answers %d %s / %d %s; want one 201 and one 200", round,
				ws[0].Code, ws[0].Body.String(), ws[1].Code, ws[1].Body.String())
		}
		if n := e.coHosts(l); n != 5 {
			t.Errorf("round %d: %d co-hosts, want 5", round, n)
		}
	}
}

// AC-6: the owner's own address, in another letter case, at 5 co-hosts.
func TestPAC120_AC6_OwnersOwnAddressIsNeverRefused(t *testing.T) {
	e := newPAC120Env(t)
	for _, ownerStatus := range []string{"accepted", "declined", ""} {
		owner, email := e.person("ac6-owner")
		l, _, _ := e.link(owner, ownerStatus, pac120Statuses(5)...)
		before := e.snapshot(l.ID)
		coBefore := e.coHostSnapshot(l)
		w := e.invite(owner, l.ID, strings.ToUpper(email))
		switch ownerStatus {
		case "":
			if w.Code == http.StatusUnprocessableEntity || w.Code >= 500 {
				t.Errorf("no owner row: %d %s", w.Code, w.Body.String())
			}
			if got := e.coHostSnapshot(l); got != coBefore {
				t.Errorf("no owner row: co-host rows changed:\n %s\n %s", coBefore, got)
			}
		default:
			e.mustStatus(w, http.StatusOK, "owner row "+ownerStatus)
			if after := e.snapshot(l.ID); after != before {
				t.Errorf("owner row %s: rows changed:\n %s\n %s", ownerStatus, before, after)
			}
			if e.hostRows(l.ID)[owner] != ownerStatus {
				t.Errorf("owner row is %q, want %q", e.hostRows(l.ID)[owner], ownerStatus)
			}
		}
	}
}

// coHostSnapshot is the snapshot without the owner's row.
func (e *pac120Env) coHostSnapshot(l *storage.SchedulingLink) string {
	e.t.Helper()
	var keep []string
	for _, part := range strings.Split(e.snapshot(l.ID), ";") {
		if part != "" && !strings.HasPrefix(part, l.OwnerUserID.String()) {
			keep = append(keep, part)
		}
	}
	return strings.Join(keep, ";")
}

// AC-7: at 5 co-hosts an address nobody holds domain-confirmed is still
// "user not found", never the 422.
func TestPAC120_AC7_UserNotFoundBeforeTheLimit(t *testing.T) {
	e := newPAC120Env(t)
	owner, _ := e.person("ac7-owner")
	l, _, _ := e.link(owner, "accepted", pac120Statuses(5)...)
	unconfirmed := pac120Addr("ac7-unconfirmed")
	createTestUser(t, unconfirmed)
	collision := pac120Addr("ac7-collision")
	createConfirmedUser(t, collision)
	createConfirmedUser(t, strings.ToUpper(collision))
	for what, addr := range map[string]string{
		"nobody":           pac120Addr("ac7-nobody"),
		"unconfirmed only": unconfirmed,
		"two accounts":     collision,
	} {
		w := e.invite(owner, l.ID, addr)
		if w.Code != http.StatusNotFound || strings.TrimSpace(w.Body.String()) != `{"error":"user not found"}` {
			t.Errorf("%s: %d %s; want 404 user not found", what, w.Code, w.Body.String())
		}
	}
}

// AC-8: a link seeded above the limit refuses new invitations, keeps its
// keep-only PATCH, and accepts again once back at 4.
func TestPAC120_AC8_LinkAboveTheLimit(t *testing.T) {
	e := newPAC120Env(t)
	owner, _ := e.person("ac8-owner")
	l, _, emails := e.link(owner, "accepted", pac120Statuses(7)...)
	_, newcomer := e.person("ac8-new")
	before := e.snapshot(l.ID)
	pac120Is422(t, e.invite(owner, l.ID, newcomer), "invitation at 7")
	if after := e.snapshot(l.ID); after != before {
		t.Fatalf("rows changed by a refused invitation")
	}
	p := "/api/scheduling-links/" + l.ID.String()
	e.mustStatus(e.do(http.MethodPatch, p, owner, pac112JSON(t, map[string]any{"co_host_emails": emails[:6]})), http.StatusOK, "keep-only PATCH of 6")
	if n := e.coHosts(l); n != 6 {
		t.Fatalf("%d co-hosts after the keep-only PATCH, want 6", n)
	}
	pac120Is422(t, e.invite(owner, l.ID, newcomer), "invitation at 6")
	e.mustStatus(e.do(http.MethodPatch, p, owner, pac112JSON(t, map[string]any{"co_host_emails": emails[:4]})), http.StatusOK, "PATCH down to 4")
	e.mustStatus(e.invite(owner, l.ID, newcomer), http.StatusCreated, "invitation at 4")
	if n := e.coHosts(l); n != 5 {
		t.Errorf("%d co-hosts, want 5", n)
	}
}

// --- AC-9 .. AC-13: ordering --------------------------------------------------

// AC-9: two different new addresses at 4 co-hosts, released together: one
// 201 and one 422, 5 co-hosts, every round.
func TestPAC120_AC9_ConcurrentInvitationsCannotPassTheLimit(t *testing.T) {
	e := newPAC120Env(t)
	owner, _ := e.person("ac9-owner")
	var gate pac120Gate
	gate.install(t, e.db)
	for round := 0; round < 20; round++ {
		l, _, _ := e.link(owner, "accepted", pac120Statuses(4)...)
		_, x := e.person("ac9-x")
		_, y := e.person("ac9-y")
		gate.arm(l.ID)
		ws := pac120Together(
			func() *httptest.ResponseRecorder { return e.invite(owner, l.ID, x) },
			func() *httptest.ResponseRecorder { return e.invite(owner, l.ID, y) },
		)
		codes := []int{ws[0].Code, ws[1].Code}
		sort.Ints(codes)
		if codes[0] != http.StatusCreated || codes[1] != http.StatusUnprocessableEntity {
			t.Errorf("round %d: answers %d %s / %d %s; want one 201 and one 422", round,
				ws[0].Code, ws[0].Body.String(), ws[1].Code, ws[1].Body.String())
		}
		for _, w := range ws {
			if w.Code == http.StatusUnprocessableEntity {
				pac120Is422(t, w, fmt.Sprintf("round %d refusal", round))
			}
		}
		if n := e.coHosts(l); n != 5 {
			t.Errorf("round %d: %d co-hosts, want 5", round, n)
		}
	}
}

// AC-10: an invitation of X against a PATCH of 4+Y (a) or 4+X (b), in both
// commit orders, forced with the seams.
func TestPAC120_AC10_InvitationAndPatchAreOrdered(t *testing.T) {
	e := newPAC120Env(t)
	owner, _ := e.person("ac10-owner")
	var current atomic.Pointer[uuid.UUID]
	var started atomic.Pointer[func()]
	fire := func(id uuid.UUID) {
		if c := current.Load(); c != nil && *c == id {
			if f := started.Swap(nil); f != nil {
				(*f)()
			}
		}
	}
	storage.InstallAfterLinkInviteLock(t, func(_ context.Context, id uuid.UUID) error { fire(id); return nil })
	storage.InstallAfterLinkPatchUpdate(t, func(id uuid.UUID) error { fire(id); return nil })
	for _, sameAddress := range []bool{false, true} {
		for _, inviteFirst := range []bool{true, false} {
			name := fmt.Sprintf("same=%v inviteFirst=%v", sameAddress, inviteFirst)
			for round := 0; round < 20; round++ {
				l, ids, emails := e.link(owner, "accepted", pac120Statuses(4)...)
				xID, x := e.person("ac10-x")
				yID, y := e.person("ac10-y")
				listed, listedID := y, yID
				if sameAddress {
					listed, listedID = x, xID
				}
				list := append(append([]string{}, emails...), listed)
				p := "/api/scheduling-links/" + l.ID.String()
				patch := func() *httptest.ResponseRecorder {
					return e.do(http.MethodPatch, p, owner, pac112JSON(t, map[string]any{"co_host_emails": list}))
				}
				invite := func() *httptest.ResponseRecorder { return e.invite(owner, l.ID, x) }
				// The first request, holding the link, starts the second
				// and waits until it waits on the link lock.
				first, second := invite, patch
				if !inviteFirst {
					first, second = patch, invite
				}
				var secondW <-chan *httptest.ResponseRecorder
				hold := func() {
					out := make(chan *httptest.ResponseRecorder, 1)
					go func() { out <- second() }()
					secondW = out
					deadline := time.Now().Add(5 * time.Second)
					for pac120LockWaiters(t, e.db) == 0 {
						if time.Now().After(deadline) {
							t.Error(name + ": the second request never waited on the link lock")
							return
						}
						time.Sleep(5 * time.Millisecond)
					}
				}
				current.Store(&l.ID)
				started.Store(&hold)
				fw := first()
				if secondW == nil {
					t.Fatalf("%s round %d: the seam did not fire", name, round)
				}
				sw := <-secondW
				inviteW, patchW := fw, sw
				if !inviteFirst {
					inviteW, patchW = sw, fw
				}
				e.mustStatus(patchW, http.StatusOK, name+" PATCH")
				want := map[uuid.UUID]bool{owner: true, listedID: true}
				for _, id := range ids {
					want[id] = true
				}
				switch {
				case sameAddress && inviteFirst:
					e.mustStatus(inviteW, http.StatusCreated, name+" invite")
				case sameAddress:
					e.mustStatus(inviteW, http.StatusOK, name+" invite")
				case inviteFirst:
					// The PATCH's complete list then removes X again.
					e.mustStatus(inviteW, http.StatusCreated, name+" invite")
				default:
					pac120Is422(t, inviteW, name+" invite")
				}
				hosts := e.hostRows(l.ID)
				if len(hosts) != len(want) {
					t.Errorf("%s round %d: %d host rows %v, want %d", name, round, len(hosts), hosts, len(want))
				}
				for id := range want {
					if _, ok := hosts[id]; !ok {
						t.Errorf("%s round %d: %s missing", name, round, id)
					}
				}
				if n := e.coHosts(l); n > 5 {
					t.Errorf("%s round %d: %d co-hosts", name, round, n)
				}
			}
		}
	}
}

// AC-11: mixed load at the limit's boundary: no 500, at most 5 co-hosts
// after every round, every answer in its set.
func TestPAC120_AC11_MixedLoadAtTheLimit(t *testing.T) {
	e := newPAC120Env(t)
	sfx := uuid.NewString()[:8]
	owner, _ := e.person("ac11-owner")
	link, ids, emails := e.link(owner, "accepted", "pending", "pending", "accepted", "accepted")
	p := "/api/scheduling-links/" + link.ID.String()
	type booked struct {
		email string
		code  int
	}
	var mu sync.Mutex
	var bookings []booked
	var failures []string
	invited404 := map[uuid.UUID]bool{}
	type responder struct {
		what, path string
		who        int // index into ids
		before     string
		code       int
	}
	responders := []*responder{
		{what: "accept", path: "/accept", who: 0},
		{what: "decline", path: "/decline", who: 1},
		{what: "leave", path: "/leave", who: 3},
	}
	var listPatch int
	slot := 0
	const rounds = 10
	for round := 0; round < rounds; round++ {
		if round > 0 {
			e.mustStatus(e.do(http.MethodPatch, p, owner, `{"active":true}`), http.StatusOK, "round start resume")
		}
		firstSaved := make(chan struct{})
		var once sync.Once
		release := func() { once.Do(func() { close(firstSaved) }) }
		start := make(chan struct{})
		var roundBookings, wg sync.WaitGroup
		run := func(what string, gated bool, allowed []int, f func() *httptest.ResponseRecorder) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if gated {
					<-firstSaved
				}
				w := f()
				ok := allowed == nil
				for _, c := range allowed {
					ok = ok || w.Code == c
				}
				if w.Code >= 500 || !ok {
					mu.Lock()
					failures = append(failures, fmt.Sprintf("round %d %s: %d %s", round, what, w.Code, w.Body.String()))
					mu.Unlock()
				}
			}()
		}
		for i := 0; i < 3; i++ {
			i, email := slot, fmt.Sprintf("r%d-%d-%s@booker.test", round, slot, sfx)
			slot++
			roundBookings.Add(1)
			run("booking", false, nil, func() *httptest.ResponseRecorder {
				defer roundBookings.Done()
				w := e.book(link.Slug, i, email)
				mu.Lock()
				bookings = append(bookings, booked{email, w.Code})
				mu.Unlock()
				if w.Code == http.StatusCreated {
					release()
				}
				return w
			})
		}
		go func() { roundBookings.Wait(); release() }()
		for k := 0; k < 2; k++ {
			id, addr := e.person(fmt.Sprintf("ac11-new%d", k))
			run("invite", false, []int{http.StatusCreated, http.StatusOK, http.StatusUnprocessableEntity, http.StatusNotFound},
				func() *httptest.ResponseRecorder {
					w := e.invite(owner, link.ID, addr)
					if w.Code == http.StatusNotFound {
						if strings.TrimSpace(w.Body.String()) != `{"error":"not found"}` {
							mu.Lock()
							failures = append(failures, "invite 404 body "+w.Body.String())
							mu.Unlock()
						}
						mu.Lock()
						invited404[id] = true
						mu.Unlock()
					}
					return w
				})
		}
		// The last round's accept, decline and leave race the delete: they
		// wait for the same first booking the delete waits for, so some of
		// them land after it. Each one's host row is recorded before the
		// round; one answered 404 ran after the delete and must have changed
		// nothing (AC-11).
		last := round == rounds-1
		for _, r := range responders {
			if last {
				r.before = e.hostRow(link.ID, ids[r.who])
			}
			r.code = 0
		}
		for _, r := range responders {
			run(r.what, last, nil, func() *httptest.ResponseRecorder {
				w := e.do(http.MethodPost, p+r.path, ids[r.who], "")
				mu.Lock()
				r.code = w.Code
				mu.Unlock()
				return w
			})
		}
		run("fields patch", false, nil, func() *httptest.ResponseRecorder {
			return e.do(http.MethodPatch, p, owner, fmt.Sprintf(`{"title":"round %d"}`, round))
		})
		run("list patch", false, nil, func() *httptest.ResponseRecorder {
			w := e.do(http.MethodPatch, p, owner, pac112JSON(t, map[string]any{"co_host_emails": emails}))
			mu.Lock()
			listPatch = w.Code
			mu.Unlock()
			return w
		})
		run("pause", true, nil, func() *httptest.ResponseRecorder { return e.do(http.MethodPatch, p, owner, `{"active":false}`) })
		run("resume", true, nil, func() *httptest.ResponseRecorder { return e.do(http.MethodPatch, p, owner, `{"active":true}`) })
		if round == rounds-1 {
			run("delete", true, nil, func() *httptest.ResponseRecorder { return e.do(http.MethodDelete, p, owner, "") })
		}
		close(start)
		wg.Wait()
		if n := e.coHosts(link); n > 5 {
			t.Errorf("round %d: %d co-hosts", round, n)
		}
	}
	for _, f := range failures {
		t.Error(f)
	}
	if at, _ := e.linkRow(link.ID); !at.Valid {
		t.Fatal("the final delete did not delete the link")
	}
	// A 404 changed nothing: either the request came after the delete, or
	// (leave only) the user had no row to remove. The only other writer of
	// these rows in the round is the list PATCH. When it answered 200 it ran
	// before the delete, and it re-adds a co-host that had left as a fresh
	// pending row and leaves the others exactly as they are. A leave that
	// found no row and answered 404 cannot have come after such a re-add, so
	// in both cases the row is the recorded one, or that re-added row.
	answered404 := 0
	for _, r := range responders {
		if r.code != http.StatusNotFound {
			continue
		}
		answered404++
		want := r.before
		if want == "absent" && listPatch == http.StatusOK {
			want = "pending responded_at=null"
		}
		if got := e.hostRow(link.ID, ids[r.who]); got != want {
			t.Errorf("last round: %s answered 404 but its host row is %q; want unchanged %q (list patch %d)", r.what, got, want, listPatch)
		}
	}
	t.Logf("last round: %d of accept, decline, leave answered 404 (list patch %d)", answered404, listPatch)
	hosts := e.hostRows(link.ID)
	for id := range invited404 {
		if _, found := hosts[id]; found {
			t.Errorf("invitation answered 404 but %s has a row", id)
		}
	}
	// After the delete every host write is the 404 and changes no row.
	before := e.snapshot(link.ID)
	_, late := e.person("ac11-late")
	pac101NotFound(t, e.invite(owner, link.ID, late), "invite after the delete")
	for _, path := range []string{"/accept", "/decline", "/leave"} {
		if w := e.do(http.MethodPost, p+path, ids[0], ""); w.Code != http.StatusNotFound {
			t.Errorf("%s after the delete: %d %s", path, w.Code, w.Body.String())
		}
	}
	if after := e.snapshot(link.ID); after != before {
		t.Error("a request answered 404 after the delete changed a host row")
	}
	created := 0
	for _, bk := range bookings {
		switch bk.code {
		case http.StatusCreated:
			created++
			if e.bookingsBy(bk.email) != 1 {
				t.Errorf("booking %s answered 201 but is not stored", bk.email)
			}
		default:
			if e.bookingsBy(bk.email) != 0 {
				t.Errorf("booking %s answered %d but is stored", bk.email, bk.code)
			}
		}
	}
	if created == 0 {
		t.Error("no booking succeeded")
	}
}

// AC-12: a delete held open while an invitation waits on the link lock; the
// delete commits: 404 not found, no row.
func TestPAC120_AC12_InvitationWaitingOnADeleteIs404(t *testing.T) {
	e := newPAC120Env(t)
	owner, _ := e.person("ac12-owner")
	l, _, _ := e.link(owner, "accepted", pac120Statuses(4)...)
	invitee, email := e.person("ac12-x")
	tx := e.holdDelete(l.ID)
	got := e.async(http.MethodPost, "/api/scheduling-links/"+l.ID.String()+"/hosts", owner, `{"email":"`+email+`"}`)
	pac101AwaitWaiter(t, e.db)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	pac101NotFound(t, <-got, "invitation after the delete committed")
	if _, found := e.hostRows(l.ID)[invitee]; found {
		t.Fatal("a host row was written on a deleted link")
	}
}

// AC-13: at 5 co-hosts, the generation moved by a raw UPDATE after
// requireAuth's read and before the write's re-check: requireAuth's 401,
// not the 422, and no row.
func TestPAC120_AC13_StaleSessionInTheWriteIs401NotTheLimit(t *testing.T) {
	e := newPAC120Env(t)
	owner, _ := e.person("ac13-owner")
	l, _, _ := e.link(owner, "accepted", pac120Statuses(5)...)
	invitee, email := e.person("ac13-x")
	var calls atomic.Int32
	storage.InstallGuardedCallHook(t, func(context.Context) {
		if calls.Add(1) == 1 {
			execT(t, `UPDATE users SET session_generation = session_generation + 1 WHERE id = $1`, owner)
		}
	})
	g, ok, err := storage.CurrentGeneration(e.db, owner)
	if err != nil || !ok {
		t.Fatalf("generation: %v %v", ok, err)
	}
	before := e.snapshot(l.ID)
	req := httptest.NewRequest(http.MethodPost, "/api/scheduling-links/"+l.ID.String()+"/hosts", strings.NewReader(`{"email":"`+email+`"}`))
	req.Header.Set("Origin", guardOrigin)
	req.AddCookie(sessionCookie(t, owner, g))
	w := httptest.NewRecorder()
	guardChainOn(e.db, e.r).ServeHTTP(w, req)
	assert401(t, w)
	if calls.Load() == 0 {
		t.Fatal("the write's guarded call never ran")
	}
	if after := e.snapshot(l.ID); after != before {
		t.Error("host rows changed")
	}
	if _, found := e.hostRows(l.ID)[invitee]; found {
		t.Error("the invitee has a row")
	}
}

// --- AC-14, AC-15: the two 500s ----------------------------------------------

func pac120AssertStep(t *testing.T, w *httptest.ResponseRecorder, reported *[]error, logs *bytes.Buffer, step string) {
	t.Helper()
	if w.Code != http.StatusInternalServerError || strings.TrimSpace(w.Body.String()) != `{"error":"internal error"}` {
		t.Fatalf("status %d %s; want 500 internal error", w.Code, w.Body.String())
	}
	if !strings.Contains(logs.String(), "-> 500: "+step+": ") {
		t.Errorf("no log line names %q: %q", step, logs.String())
	}
	found := false
	for _, err := range *reported {
		found = found || strings.HasPrefix(err.Error(), step+": ")
	}
	if !found {
		t.Errorf("no Sentry event names %q: %v", step, *reported)
	}
}

// AC-14: only the lookup fails: 500 under "invite lookup", never 404.
func TestPAC120_AC14_FailedLookupIs500(t *testing.T) {
	e := newPAC120Env(t)
	owner, _ := e.person("ac14-owner")
	l, _, _ := e.link(owner, "accepted", pac120Statuses(2)...)
	invitee, email := e.person("ac14-x")
	storage.InstallConfirmedHolderLookup(t, func(addr string) error {
		if addr == email {
			return errors.New("pac120: lookup failed")
		}
		return nil
	})
	reported, logs := captureServerErrors(t)
	before := e.snapshot(l.ID)
	w := e.invite(owner, l.ID, email)
	pac120AssertStep(t, w, reported, logs, "invite lookup")
	if strings.Contains(logs.String(), "invite link read") {
		t.Error("the link read failed instead of the lookup")
	}
	if after := e.snapshot(l.ID); after != before {
		t.Error("host rows changed")
	}
	if _, found := e.hostRows(l.ID)[invitee]; found {
		t.Error("the invitee has a row")
	}
}

// AC-15: the link read fails: 500 under "invite link read".
func TestPAC120_AC15_FailedLinkReadIsLoggedAndReported(t *testing.T) {
	e := newPAC120Env(t)
	owner, _ := e.person("ac15-owner")
	l, _, _ := e.link(owner, "accepted", pac120Statuses(2)...)
	invitee, email := e.person("ac15-x")
	storage.InstallLinkReadByID(t, func(id uuid.UUID) error {
		if id == l.ID {
			return errors.New("pac120: link read failed")
		}
		return nil
	})
	reported, logs := captureServerErrors(t)
	before := e.snapshot(l.ID)
	w := e.invite(owner, l.ID, email)
	pac120AssertStep(t, w, reported, logs, "invite link read")
	if after := e.snapshot(l.ID); after != before {
		t.Error("host rows changed")
	}
	if _, found := e.hostRows(l.ID)[invitee]; found {
		t.Error("the invitee has a row")
	}
}
