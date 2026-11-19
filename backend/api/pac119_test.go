package api

// PAC-119 (contracts/features/PAC-119.md): a link create carrying an
// Idempotency-Key is safe to retry. Every test here is a [contract] test: a
// handler test against the test database, asserting the answer and the rows.
// Keyed requests run under a session guard with requireAuth's response guard
// (serveGuarded), as in production, so every resolution runs its PAC-71
// re-checks.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

const (
	pac119KeyBadMsg  = `{"error":"invalid Idempotency-Key header"}`
	pac119DeletedMsg = `{"error":"idempotency key already used by a deleted link"}`
	pac119SlugMsg    = `{"error":"slug already exists"}`
)

type pac119Env struct {
	*pac112Env
}

func newPAC119Env(t *testing.T) *pac119Env {
	t.Helper()
	return &pac119Env{newPAC112Env(t)}
}

func pac119NewKey() string { return "k119-" + strings.ReplaceAll(uuid.NewString(), "-", "") }

// owner is a domain-confirmed account with a name of its own, so the slug it
// generates ("<first name>-30min") is free unless the test takes it.
func (e *pac119Env) owner(name string) uuid.UUID {
	e.t.Helper()
	id, _ := e.confirmed(name)
	execT(e.t, `UPDATE users SET name = $2 WHERE id = $1`, id, "P119"+strings.ReplaceAll(uuid.NewString()[:8], "-", "")+" Owner")
	return id
}

func (e *pac119Env) gen(user uuid.UUID) int64 {
	e.t.Helper()
	g, ok, err := storage.CurrentGeneration(e.db, user)
	if err != nil || !ok {
		e.t.Fatalf("generation: %v %v", ok, err)
	}
	return g
}

type pac119Tag struct{}

// send is one create from user's session at its current generation, under
// the response guard, with the key ("" sends no header) and the raw body.
// tag, when not empty, is put on the request's context for the seams.
func (e *pac119Env) send(user uuid.UUID, key, body, tag string) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.sendAt(user, e.gen(user), key, body, tag)
}

func (e *pac119Env) sendAt(user uuid.UUID, gen int64, key, body, tag string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/scheduling-links/", strings.NewReader(body))
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if tag != "" {
		req = req.WithContext(context.WithValue(req.Context(), pac119Tag{}, tag))
	}
	return serveGuarded(e.t, e.r, req, user, gen)
}

func (e *pac119Env) sendJSON(user uuid.UUID, key string, body map[string]any) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.send(user, key, pac112JSON(e.t, body), "")
}

func pac119Tagged(ctx context.Context, tag string) bool {
	v, _ := ctx.Value(pac119Tag{}).(string)
	return v == tag
}

// rawChanges is the answer's co_host_changes bytes exactly as written.
func pac119RawChanges(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return string(m["co_host_changes"])
}

// keyLinkBody is the exact body of a key refusal that names a link.
func pac119KeyLinkBody(text string, link uuid.UUID) string {
	return fmt.Sprintf(`{"error":%q,"link_id":%q}`, text, link.String())
}

func (e *pac119Env) age(key, interval string) {
	e.t.Helper()
	execT(e.t, `UPDATE scheduling_link_create_keys SET created_at = now() - interval '`+interval+`' WHERE idempotency_key = $1`, key)
}

func (e *pac119Env) keyRows(owner uuid.UUID) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM scheduling_link_create_keys WHERE owner_user_id = $1`, owner).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// invitesOf is how many times link appears in user's pending invitations.
func (e *pac119Env) invitesOf(user, link uuid.UUID) int {
	e.t.Helper()
	w := e.do(http.MethodGet, "/api/scheduling-links/host-invites", user, "")
	e.mustStatus(w, http.StatusOK, "host invites")
	return strings.Count(w.Body.String(), link.String())
}

// waitLockWaiters waits until n sessions of the test database wait on a lock
// (pg_stat_activity), never by sleeping alone.
func (e *pac119Env) waitLockWaiters(n int) {
	e.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		var got int
		if err := e.db.QueryRow(`SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&got); err != nil {
			e.t.Fatal(err)
		}
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("%d sessions waiting on a lock; want %d", got, n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// holdFirstCommit holds the first create to reach beforeLinkCreateCommit
// until release is called; later creates pass. reached is closed when the
// first one is held.
func pac119HoldFirstCommit(t *testing.T) (reached <-chan struct{}, release func()) {
	t.Helper()
	var once sync.Once
	r := make(chan struct{})
	gate := make(chan struct{})
	var rel sync.Once
	storage.InstallBeforeLinkCreateCommit(t, func(uuid.UUID) error {
		first := false
		once.Do(func() { first = true })
		if first {
			close(r)
			<-gate
		}
		return nil
	})
	release = func() { rel.Do(func() { close(gate) }) }
	t.Cleanup(release)
	return r, release
}

func pac119Async(f func() *httptest.ResponseRecorder) <-chan *httptest.ResponseRecorder {
	ch := make(chan *httptest.ResponseRecorder, 1)
	go func() { ch <- f() }()
	return ch
}

func pac119Wait(t *testing.T, ch <-chan *httptest.ResponseRecorder, what string) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case w := <-ch:
		return w
	case <-time.After(30 * time.Second):
		t.Fatalf("%s did not answer", what)
		return nil
	}
}

// AC-1: a keyed create listing B and C, then the identical request: 201 with
// the same id and slug, byte-equal co_host_changes, one link, B one host row
// and L once in B's invitations.
func TestPAC119_AC1_IdenticalRetryReplaysWithTheOriginalOutcomes(t *testing.T) {
	e := newPAC119Env(t)
	owner := e.owner("ac1-owner")
	b, bAddr := e.confirmed("ac1-b")
	_, cAddr := e.unconfirmed("ac1-c")
	key := pac119NewKey()
	body := map[string]any{"title": "AC-1 link", "co_host_emails": []string{bAddr, cAddr}}
	first := e.sendJSON(owner, key, body)
	s1 := pac112Saved(t, first, http.StatusCreated)
	// PAC-149: C (carried only unconfirmed) is invited like B.
	pac112Equal(t, "original outcomes", pac112Outcomes(t, s1), []string{bAddr + "=invited", cAddr + "=invited"})
	pac112Counts(t, s1, 2, 0, 0)

	second := e.sendJSON(owner, key, body)
	s2 := pac112Saved(t, second, http.StatusCreated)
	if s2.Id != s1.Id || s2.Slug != s1.Slug {
		t.Fatalf("replay id/slug %s %s; want %s %s", s2.Id, s2.Slug, s1.Id, s1.Slug)
	}
	if a, b := pac119RawChanges(t, first), pac119RawChanges(t, second); a != b {
		t.Fatalf("co_host_changes bytes differ:\n%s\n%s", a, b)
	}
	if n := e.linkCount(owner); n != 1 {
		t.Errorf("%d links; want 1", n)
	}
	if n := pac149InvitationsTo(t, e.db, bAddr); n != 1 {
		t.Errorf("B has %d invitations; want 1", n)
	}
	if n := e.invitesOf(b, s1.Id); n != 1 {
		t.Errorf("L appears %d times in B's invitations; want 1", n)
	}
}

// AC-2: without a list, the retry replays and neither answer carries
// co_host_changes.
func TestPAC119_AC2_ReplayWithoutAList(t *testing.T) {
	e := newPAC119Env(t)
	owner := e.owner("ac2-owner")
	key := pac119NewKey()
	body := map[string]any{"title": "AC-2 link"}
	first, second := e.sendJSON(owner, key, body), e.sendJSON(owner, key, body)
	s1, s2 := pac112Saved(t, first, http.StatusCreated), pac112Saved(t, second, http.StatusCreated)
	if s1.Id != s2.Id || s1.CoHostChanges != nil || s2.CoHostChanges != nil {
		t.Fatalf("ids %s %s; changes %v %v", s1.Id, s2.Id, s1.CoHostChanges, s2.CoHostChanges)
	}
	if strings.Contains(second.Body.String(), "co_host_changes") {
		t.Errorf("replay carries co_host_changes: %s", second.Body.String())
	}
	if n := e.linkCount(owner); n != 1 {
		t.Errorf("%d links; want 1", n)
	}
}

// AC-3: after a rename of L and B declining, the retry answers L's current
// title and roster with the original outcomes, byte for byte; B stays
// declined and nothing is written.
func TestPAC119_AC3_ReplayShowsCurrentLinkAndOriginalOutcomes(t *testing.T) {
	e := newPAC119Env(t)
	owner := e.owner("ac3-owner")
	b, bAddr := e.confirmed("ac3-b")
	key := pac119NewKey()
	body := map[string]any{"title": "AC-3 link", "co_host_emails": []string{bAddr}}
	first := e.sendJSON(owner, key, body)
	s1 := pac112Saved(t, first, http.StatusCreated)
	pac112Saved(t, e.patch(owner, s1.Id, map[string]any{"title": "AC-3 renamed"}), http.StatusOK)
	e.mustStatus(e.do(http.MethodPost, "/api/scheduling-links/"+s1.Id.String()+"/decline", b, ""), http.StatusOK, "B declines")
	rowsBefore := e.hostRows(s1.Id)

	second := e.sendJSON(owner, key, body)
	s2 := pac112Saved(t, second, http.StatusCreated)
	if s2.Id != s1.Id || s2.Title != "AC-3 renamed" {
		t.Fatalf("replay id %s title %q", s2.Id, s2.Title)
	}
	declined := false
	for _, h := range s2.Hosts {
		// PAC-149: a declined entry is shown by address and status only.
		if h.UserId == nil && h.Email == bAddr && string(h.Status) == "declined" {
			declined = true
		}
	}
	if !declined {
		t.Errorf("replay roster does not show B declined: %s", second.Body.String())
	}
	if a, c := pac119RawChanges(t, first), pac119RawChanges(t, second); a != c {
		t.Fatalf("co_host_changes bytes differ:\n%s\n%s", a, c)
	}
	pac112SameHosts(t, "after the replay", e.hostRows(s1.Id), rowsBefore)
	if e.hostRows(s1.Id)[b] != "declined" || e.hostRowsOf(b) != 1 {
		t.Error("B's row changed or a new row exists")
	}
}

// AC-4: an explicit slug, retried: the replay, never the slug 409.
func TestPAC119_AC4_ExplicitSlugRetryIsTheReplay(t *testing.T) {
	e := newPAC119Env(t)
	owner := e.owner("ac4-owner")
	key := pac119NewKey()
	body := map[string]any{"title": "AC-4", "slug": "ac4-" + uuid.NewString()[:8]}
	s1 := pac112Saved(t, e.sendJSON(owner, key, body), http.StatusCreated)
	s2 := pac112Saved(t, e.sendJSON(owner, key, body), http.StatusCreated)
	if s1.Id != s2.Id {
		t.Fatalf("ids %s %s", s1.Id, s2.Id)
	}
	// Keyless, the same slug is still the slug 409 (unchanged).
	pac112Body(t, e.sendJSON(owner, "", body), http.StatusConflict, pac119SlugMsg, "keyless same slug")
}

// AC-5: what "the same request" means. Formatting, aliases, defaults and the
// list's spelling replay; a different title, address or an empty list is the
// key-reused refusal naming L, never the slug 409, with L unchanged.
func TestPAC119_AC5_SameRequestOrNot(t *testing.T) {
	e := newPAC119Env(t)
	owner := e.owner("ac5-owner")
	_, bAddr := e.confirmed("ac5-b")
	_, gAddr := e.confirmed("ac5-g")
	_, hAddr := e.confirmed("ac5-h")
	key := pac119NewKey()
	orig := fmt.Sprintf(`{"title":"AC-5 link","duration_options":[30,45],"co_host_emails":[%q,%q]}`, bAddr, gAddr)
	s1 := pac112Saved(t, e.send(owner, key, orig, ""), http.StatusCreated)
	hostsBefore := e.hostRows(s1.Id)

	for what, body := range map[string]string{
		"keys reordered, whitespace": fmt.Sprintf("{\n  \"co_host_emails\" : [ %q , %q ],\n\t\"duration_options\":[30, 45], \"title\":\"AC-5 link\"}", bAddr, gAddr),
		"list re-spelled":            fmt.Sprintf(`{"title":"AC-5 link","duration_options":[30,45],"co_host_emails":[%q,%q,%q]}`, " "+strings.ToUpper(gAddr), bAddr, strings.ToUpper(bAddr)),
		"usage reusable":             fmt.Sprintf(`{"title":"AC-5 link","duration_options":[30,45],"usage_type":"reusable","co_host_emails":[%q,%q]}`, bAddr, gAddr),
		"horizon 60":                 fmt.Sprintf(`{"title":"AC-5 link","duration_options":[30,45],"booking_horizon_days":60,"co_host_emails":[%q,%q]}`, bAddr, gAddr),
		"durations alias":            fmt.Sprintf(`{"title":"AC-5 link","durations":[30,45],"co_host_emails":[%q,%q]}`, bAddr, gAddr),
		"title padded":               fmt.Sprintf(`{"title":"  AC-5 link ","duration_options":[30,45],"co_host_emails":[%q,%q]}`, bAddr, gAddr),
		"window defaults spelled":    fmt.Sprintf(`{"title":"AC-5 link","duration_options":[30,45],"window_start":"9:00","window_end_time":"17:00:00","co_host_emails":[%q,%q]}`, bAddr, gAddr),
	} {
		s := pac112Saved(t, e.send(owner, key, body, ""), http.StatusCreated)
		if s.Id != s1.Id {
			t.Errorf("%s: answered link %s; want the replay of %s", what, s.Id, s1.Id)
		}
	}
	reused := pac119KeyLinkBody("idempotency key already used", s1.Id)
	for what, body := range map[string]string{
		"different title":      fmt.Sprintf(`{"title":"AC-5 other","duration_options":[30,45],"co_host_emails":[%q,%q]}`, bAddr, gAddr),
		"one address replaced": fmt.Sprintf(`{"title":"AC-5 link","duration_options":[30,45],"co_host_emails":[%q,%q]}`, bAddr, hAddr),
		"empty list":           `{"title":"AC-5 link","duration_options":[30,45],"co_host_emails":[]}`,
		"no list":              `{"title":"AC-5 link","duration_options":[30,45]}`,
		"durations reordered":  fmt.Sprintf(`{"title":"AC-5 link","duration_options":[45,30],"co_host_emails":[%q,%q]}`, bAddr, gAddr),
		"buffer changed":       fmt.Sprintf(`{"title":"AC-5 link","duration_options":[30,45],"buffer_before":5,"co_host_emails":[%q,%q]}`, bAddr, gAddr),
	} {
		pac112Body(t, e.send(owner, key, body, ""), http.StatusConflict, reused, what)
	}
	if e.linkCount(owner) != 1 || e.title(s1.Id) != "AC-5 link" {
		t.Errorf("links %d, title %q", e.linkCount(owner), e.title(s1.Id))
	}
	pac112SameHosts(t, "L's hosts", e.hostRows(s1.Id), hostsBefore)
	if pac149InvitationsTo(t, e.db, bAddr) != 1 || pac149InvitationsTo(t, e.db, gAddr) != 1 {
		t.Error("B or G gained an invitation")
	}

	// Created with no list: co_host_emails null is the replay.
	key2 := pac119NewKey()
	s3 := pac112Saved(t, e.send(owner, key2, `{"title":"AC-5 bare"}`, ""), http.StatusCreated)
	s4 := pac112Saved(t, e.send(owner, key2, `{"title":"AC-5 bare","co_host_emails":null}`, ""), http.StatusCreated)
	if s3.Id != s4.Id || s4.CoHostChanges != nil {
		t.Fatalf("null list: %s %s %v", s3.Id, s4.Id, s4.CoHostChanges)
	}
}

// AC-6: another owner's identical key and request is unrelated (a new link of
// its own, nothing of L); another session of A is the replay.
func TestPAC119_AC6_KeyScopeIsTheAccount(t *testing.T) {
	e := newPAC119Env(t)
	a := e.owner("ac6-a")
	z := e.owner("ac6-z")
	key := pac119NewKey()
	body := `{"title":"AC-6 link"}`
	s1 := pac112Saved(t, e.send(a, key, body, ""), http.StatusCreated)
	wz := e.send(z, key, body, "")
	sz := pac112Saved(t, wz, http.StatusCreated)
	if sz.Id == s1.Id || sz.Slug == s1.Slug || sz.OwnerId != z {
		t.Fatalf("Z's answer %s %s owner %s; L is %s %s", sz.Id, sz.Slug, sz.OwnerId, s1.Id, s1.Slug)
	}
	if strings.Contains(wz.Body.String(), s1.Id.String()) || strings.Contains(wz.Body.String(), s1.Slug) {
		t.Errorf("Z's answer names L: %s", wz.Body.String())
	}
	// A2: a later session of A (a new sign-in bumps nothing; the generation
	// is the account's), resolving the same row.
	s2 := pac112Saved(t, e.sendAt(a, e.gen(a), key, body, "a2"), http.StatusCreated)
	if s2.Id != s1.Id {
		t.Fatalf("A2 got %s; want the replay of %s", s2.Id, s1.Id)
	}
	if e.linkCount(a) != 1 || e.linkCount(z) != 1 {
		t.Errorf("links: A %d, Z %d", e.linkCount(a), e.linkCount(z))
	}
}

// AC-7: the winner held before its commit; a second request with the same
// key waits and then resolves: (a) same generated slug, identical: both 201,
// same id; (b) base slug taken, different suffixes, identical: both 201, same
// id; (c) as (a) with another title: 201 and the key-reused refusal. One link.
func TestPAC119_AC7_ConcurrentSameKey(t *testing.T) {
	for _, c := range []struct {
		name      string
		takenBase bool
		title2    string
	}{
		{"a free base slug, identical", false, "AC-7 link"},
		{"b base slug taken, identical", true, "AC-7 link"},
		{"c free base slug, title changed", false, "AC-7 other"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newPAC119Env(t)
			owner := e.owner("ac7-owner")
			if c.takenBase {
				u, err := storage.GetUserByID(e.db, owner)
				if err != nil {
					t.Fatal(err)
				}
				base, err := e.slh.bookEng.GenerateSlug(u.Name, 30)
				if err != nil {
					t.Fatal(err)
				}
				createSchedulingLinkRecord(t, e.owner("ac7-other"), base, "taken")
			}
			key := pac119NewKey()
			reached, release := pac119HoldFirstCommit(t)
			first := pac119Async(func() *httptest.ResponseRecorder { return e.send(owner, key, `{"title":"AC-7 link"}`, "") })
			<-reached
			second := pac119Async(func() *httptest.ResponseRecorder {
				return e.send(owner, key, fmt.Sprintf(`{"title":%q}`, c.title2), "")
			})
			e.waitLockWaiters(1)
			release()
			w1, w2 := pac119Wait(t, first, "winner"), pac119Wait(t, second, "loser")
			s1 := pac112Saved(t, w1, http.StatusCreated)
			if c.title2 == "AC-7 link" {
				s2 := pac112Saved(t, w2, http.StatusCreated)
				if s2.Id != s1.Id {
					t.Fatalf("loser answered %s; winner %s", s2.Id, s1.Id)
				}
			} else {
				pac112Body(t, w2, http.StatusConflict, pac119KeyLinkBody("idempotency key already used", s1.Id), "changed title")
			}
			if n := e.linkCount(owner); n != 1 {
				t.Errorf("%d links; want 1", n)
			}
		})
	}
}

// AC-8: the winner fails inside its transaction (the co-host write seam)
// while an identical request waits: 500, then 201 with a new link; one link,
// no key left behind by the winner.
func TestPAC119_AC8_RolledBackWinnerLeavesNoKey(t *testing.T) {
	e := newPAC119Env(t)
	owner := e.owner("ac8-owner")
	_, bAddr := e.confirmed("ac8-b")
	key := pac119NewKey()
	var once sync.Once
	reached, gate := make(chan struct{}), make(chan struct{})
	storage.InstallCoHostWrite(t, func(uuid.UUID, string) error {
		first := false
		once.Do(func() { first = true })
		if !first {
			return nil
		}
		close(reached)
		<-gate
		return errors.New("pac119: co-host write failed")
	})
	body := fmt.Sprintf(`{"title":"AC-8 link","co_host_emails":[%q]}`, bAddr)
	first := pac119Async(func() *httptest.ResponseRecorder { return e.send(owner, key, body, "") })
	<-reached
	second := pac119Async(func() *httptest.ResponseRecorder { return e.send(owner, key, body, "") })
	e.waitLockWaiters(1)
	close(gate)
	pac112Body(t, pac119Wait(t, first, "winner"), http.StatusInternalServerError, `{"error":"internal error"}`, "failed winner")
	s2 := pac112Saved(t, pac119Wait(t, second, "loser"), http.StatusCreated)
	pac112Equal(t, "loser outcomes", pac112Outcomes(t, s2), []string{bAddr + "=invited"})
	if n := e.linkCount(owner); n != 1 {
		t.Errorf("%d links; want 1", n)
	}
	if n := e.keyRows(owner); n != 1 {
		t.Errorf("%d key rows; want the loser's only", n)
	}
}

// AC-9: a keyed create refused or failed stores nothing; the same key with a
// corrected body is then 201 with a new link, never a refusal.
func TestPAC119_AC9_FailedAttemptNeverConsumesTheKey(t *testing.T) {
	t.Run("422 six new entries", func(t *testing.T) {
		e := newPAC119Env(t)
		owner := e.owner("ac9a-owner")
		key := pac119NewKey()
		six := make([]string, 6)
		for i := range six {
			_, six[i] = e.confirmed(fmt.Sprintf("ac9a-%d", i))
		}
		pac112Body(t, e.sendJSON(owner, key, map[string]any{"title": "AC-9", "co_host_emails": six}),
			http.StatusUnprocessableEntity, pac112CapMsg, "six new entries")
		if e.keyRows(owner) != 0 || e.linkCount(owner) != 0 {
			t.Fatal("the 422 stored something")
		}
		s := pac112Saved(t, e.sendJSON(owner, key, map[string]any{"title": "AC-9", "co_host_emails": six[:5]}), http.StatusCreated)
		pac112Counts(t, s, 5, 0, 0)
	})
	t.Run("500 owner-host write", func(t *testing.T) {
		e := newPAC119Env(t)
		owner := e.owner("ac9b-owner")
		key := pac119NewKey()
		uninstall := storage.InstallCreateOwnerHost(t, func(uuid.UUID) error { return errors.New("pac119: owner row failed") })
		pac112Body(t, e.sendJSON(owner, key, map[string]any{"title": "AC-9"}), http.StatusInternalServerError, `{"error":"internal error"}`, "owner row")
		uninstall()
		if e.keyRows(owner) != 0 || e.linkCount(owner) != 0 {
			t.Fatal("the 500 stored something")
		}
		pac112Saved(t, e.sendJSON(owner, key, map[string]any{"title": "AC-9"}), http.StatusCreated)
		if e.linkCount(owner) != 1 {
			t.Error("not exactly one link")
		}
	})
	t.Run("401 generation moved mid-request", func(t *testing.T) {
		e := newPAC119Env(t)
		owner := e.owner("ac9c-owner")
		key := pac119NewKey()
		var calls int
		var mu sync.Mutex
		uninstall := storage.InstallGuardedCallHook(t, func(context.Context) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			if calls == 2 { // the create transaction, after R1 found nothing
				execT(t, `UPDATE users SET session_generation = session_generation + 1 WHERE id = $1`, owner)
			}
		})
		assert401(t, e.sendJSON(owner, key, map[string]any{"title": "AC-9"}))
		uninstall()
		if calls != 2 || e.keyRows(owner) != 0 || e.linkCount(owner) != 0 {
			t.Fatalf("calls %d; the 401 stored something", calls)
		}
		pac112Saved(t, e.sendJSON(owner, key, map[string]any{"title": "AC-9 corrected"}), http.StatusCreated)
	})
}

// AC-10: for a replay, a key-reused, an expired and a deleted case, a
// generation moved before the key read (guardedCallHook) or between the key
// read and re-check #2 (afterLinkCreateKeyRead) is requireAuth's 401 with
// nothing of L. The move is a raw UPDATE on another connection.
func TestPAC119_AC10_StaleSessionLearnsNothingFromAKey(t *testing.T) {
	type prep func(e *pac119Env, owner uuid.UUID, key string, l uuid.UUID) string
	cases := map[string]prep{
		"replay":     func(*pac119Env, uuid.UUID, string, uuid.UUID) string { return `{"title":"AC-10 link"}` },
		"key reused": func(*pac119Env, uuid.UUID, string, uuid.UUID) string { return `{"title":"AC-10 other"}` },
		"expired": func(e *pac119Env, _ uuid.UUID, key string, _ uuid.UUID) string {
			e.age(key, "24 hours")
			return `{"title":"AC-10 link"}`
		},
		"deleted": func(e *pac119Env, owner uuid.UUID, _ string, l uuid.UUID) string {
			e.deleteLink(owner, l)
			return `{"title":"AC-10 link"}`
		},
	}
	positions := map[string]func(t *testing.T, move func()){
		"before the key read": func(t *testing.T, move func()) {
			storage.InstallGuardedCallHook(t, func(context.Context) { move() })
			// Re-check #1 runs before the key read: a session already
			// stale never reads the key at all.
			storage.InstallAfterLinkCreateKeyRead(t, func(context.Context, string) {
				t.Error("the key was read after the generation moved before re-check #1")
			})
		},
		"between the key read and re-check #2": func(t *testing.T, move func()) {
			storage.InstallAfterLinkCreateKeyRead(t, func(context.Context, string) { move() })
		},
	}
	for cname, c := range cases {
		for pname, install := range positions {
			t.Run(cname+" / "+pname, func(t *testing.T) {
				e := newPAC119Env(t)
				owner := e.owner("ac10-owner")
				key := pac119NewKey()
				s1 := pac112Saved(t, e.send(owner, key, `{"title":"AC-10 link"}`, ""), http.StatusCreated)
				body := c(e, owner, key, s1.Id)
				gen := e.gen(owner)
				var once sync.Once
				install(t, func() {
					once.Do(func() {
						execT(t, `UPDATE users SET session_generation = session_generation + 1 WHERE id = $1`, owner)
					})
				})
				w := e.sendAt(owner, gen, key, body, "")
				assert401(t, w)
				for _, leak := range []string{s1.Id.String(), s1.Slug, "AC-10", "idempotency"} {
					if strings.Contains(w.Body.String(), leak) {
						t.Errorf("the 401 holds %q: %s", leak, w.Body.String())
					}
				}
				if n := e.linkCount(owner); n != 1 {
					t.Errorf("%d links; want 1", n)
				}
			})
		}
	}
}

// AC-11: aged 24 h - 1 s, the identical retry replays; aged 24 h, the
// identical retry and a different title are both the expired refusal with
// link_id; no link created, L unchanged.
func TestPAC119_AC11_WindowIs24HoursByTheDatabaseClock(t *testing.T) {
	e := newPAC119Env(t)
	owner := e.owner("ac11-owner")
	key := pac119NewKey()
	s1 := pac112Saved(t, e.send(owner, key, `{"title":"AC-11 link"}`, ""), http.StatusCreated)
	e.age(key, "23 hours 59 minutes 59 seconds")
	if s := pac112Saved(t, e.send(owner, key, `{"title":"AC-11 link"}`, ""), http.StatusCreated); s.Id != s1.Id {
		t.Fatalf("under 24 h: %s", s.Id)
	}
	e.age(key, "24 hours")
	expired := pac119KeyLinkBody("idempotency key expired", s1.Id)
	pac112Body(t, e.send(owner, key, `{"title":"AC-11 link"}`, ""), http.StatusConflict, expired, "identical at 24 h")
	pac112Body(t, e.send(owner, key, `{"title":"AC-11 other"}`, ""), http.StatusConflict, expired, "different at 24 h")
	if e.linkCount(owner) != 1 || e.title(s1.Id) != "AC-11 link" {
		t.Error("a link was created or L changed")
	}
}

// AC-12 (answer half): after the cleanup cleared an expired key's data, its
// retry is the expired refusal; the storage half is in storage/pac119_test.go
// and the schedule in main_pac119_test.go.
func TestPAC119_AC12_RetryAfterCleanupIsExpired(t *testing.T) {
	e := newPAC119Env(t)
	owner := e.owner("ac12-owner")
	_, bAddr := e.confirmed("ac12-b")
	key := pac119NewKey()
	body := map[string]any{"title": "AC-12 link", "co_host_emails": []string{bAddr}}
	s1 := pac112Saved(t, e.sendJSON(owner, key, body), http.StatusCreated)
	e.age(key, "25 hours")
	if _, err := storage.ClearExpiredLinkCreateKeyData(context.Background(), e.db); err != nil {
		t.Fatal(err)
	}
	var text string
	if err := e.db.QueryRow(`SELECT k::text FROM scheduling_link_create_keys k WHERE idempotency_key = $1`, key).Scan(&text); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(text), strings.ToLower(bAddr)) {
		t.Errorf("the cleared row holds B's address: %s", text)
	}
	pac112Body(t, e.sendJSON(owner, key, body), http.StatusConflict, pac119KeyLinkBody("idempotency key expired", s1.Id), "after cleanup")
}

// AC-13: L deleted; identical or different, under or over 24 h: the deleted
// refusal, no link_id, no link created, L stays deleted and unlisted.
func TestPAC119_AC13_DeletedLinkWinsOverEverything(t *testing.T) {
	for _, c := range []struct {
		name, body string
		aged       bool
	}{
		{"a identical under 24 h", `{"title":"AC-13 link"}`, false},
		{"b different under 24 h", `{"title":"AC-13 other"}`, false},
		{"c identical aged 24 h", `{"title":"AC-13 link"}`, true},
		{"d different aged 24 h", `{"title":"AC-13 other"}`, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newPAC119Env(t)
			owner := e.owner("ac13-owner")
			key := pac119NewKey()
			s1 := pac112Saved(t, e.send(owner, key, `{"title":"AC-13 link"}`, ""), http.StatusCreated)
			e.deleteLink(owner, s1.Id)
			if c.aged {
				e.age(key, "24 hours")
			}
			pac112Body(t, e.send(owner, key, c.body, ""), http.StatusConflict, pac119DeletedMsg, c.name)
			if e.linkCount(owner) != 1 {
				t.Error("a link was created")
			}
			if del, _ := e.linkRow(s1.Id); !del.Valid {
				t.Error("L is no longer deleted")
			}
			w := e.do(http.MethodGet, "/api/scheduling-links/", owner, "")
			if strings.Contains(w.Body.String(), s1.Id.String()) {
				t.Error("L is listed")
			}
		})
	}
}

// AC-14: a malformed key is the 400 before the body is read (invalid JSON
// and a 422 body included), and no link; an empty key is keyless.
func TestPAC119_AC14_KeyFormatComesFirst(t *testing.T) {
	e := newPAC119Env(t)
	owner := e.owner("ac14-owner")
	bad := map[string]func(r *http.Request){
		"15 characters":   func(r *http.Request) { r.Header.Set("Idempotency-Key", "abcdefghijklmno") },
		"outside the set": func(r *http.Request) { r.Header.Set("Idempotency-Key", "abcdefghijklmnop.") },
		"quoted":          func(r *http.Request) { r.Header.Set("Idempotency-Key", `"abcdefghijklmnop"`) },
		"two header lines": func(r *http.Request) {
			r.Header.Add("Idempotency-Key", "abcdefghijklmnop")
			r.Header.Add("Idempotency-Key", "abcdefghijklmnop")
		},
		"256 characters":    func(r *http.Request) { r.Header.Set("Idempotency-Key", strings.Repeat("a", 256)) },
		"inner space":       func(r *http.Request) { r.Header.Set("Idempotency-Key", "abcdefgh ijklmnop") },
		"two keys one line": func(r *http.Request) { r.Header.Set("Idempotency-Key", "abcdefghijklmnop, abcdefghijklmnop") },
	}
	for what, set := range bad {
		for bname, body := range map[string]string{
			"valid":        `{"title":"AC-14"}`,
			"invalid JSON": `{"title":`,
			"a 422 body":   `{"title":""}`,
		} {
			req := httptest.NewRequest(http.MethodPost, "/api/scheduling-links/", strings.NewReader(body))
			set(req)
			w := serveGuarded(t, e.r, req, owner, e.gen(owner))
			pac112Body(t, w, http.StatusBadRequest, pac119KeyBadMsg, what+" / "+bname)
		}
	}
	if n := e.linkCount(owner); n != 0 {
		t.Fatalf("%d links after malformed keys", n)
	}
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/scheduling-links/", strings.NewReader(`{"title":"AC-14"}`))
		req.Header.Set("Idempotency-Key", "")
		pac112Saved(t, serveGuarded(t, e.r, req, owner, e.gen(owner)), http.StatusCreated)
	}
	if n, k := e.linkCount(owner), e.keyRows(owner); n != 2 || k != 0 {
		t.Fatalf("empty key: %d links, %d keys; want 2 and 0 (keyless)", n, k)
	}
}

// AC-15: two identical keyless creates are two links, with no key row;
// keyless answers are unchanged (regression guard).
func TestPAC119_AC15_KeylessIsUnchanged(t *testing.T) {
	e := newPAC119Env(t)
	owner := e.owner("ac15-owner")
	s1 := pac112Saved(t, e.send(owner, "", `{"title":"AC-15"}`, ""), http.StatusCreated)
	s2 := pac112Saved(t, e.send(owner, "", `{"title":"AC-15"}`, ""), http.StatusCreated)
	if s1.Id == s2.Id || e.linkCount(owner) != 2 || e.keyRows(owner) != 0 {
		t.Fatalf("keyless: %s %s, %d links, %d keys", s1.Id, s2.Id, e.linkCount(owner), e.keyRows(owner))
	}
	// Without a session guard (withUser only) a keyless create also works.
	pac112Saved(t, e.create(owner, map[string]any{"title": "AC-15 plain"}), http.StatusCreated)
	pac112Body(t, e.send(owner, "", `{"title":`, ""), http.StatusBadRequest, `{"error":"invalid request body"}`, "keyless bad body")
}

// AC-29: the winner held before its commit and A2's identical keyed request
// waiting on it; the generation moved (raw UPDATE) during the wait; release:
// the winner 201, the waiter requireAuth's 401 with nothing of the link; one
// link.
func TestPAC119_AC29_RevokedWhileWaitingIs401(t *testing.T) {
	e := newPAC119Env(t)
	owner := e.owner("ac29-owner")
	key := pac119NewKey()
	reached, release := pac119HoldFirstCommit(t)
	body := `{"title":"AC-29 link"}`
	first := pac119Async(func() *httptest.ResponseRecorder { return e.send(owner, key, body, "") })
	<-reached
	second := pac119Async(func() *httptest.ResponseRecorder { return e.send(owner, key, body, "a2") })
	e.waitLockWaiters(1)
	execT(t, `UPDATE users SET session_generation = session_generation + 1 WHERE id = $1`, owner)
	release()
	s1 := pac112Saved(t, pac119Wait(t, first, "winner"), http.StatusCreated)
	w2 := pac119Wait(t, second, "waiter")
	assert401(t, w2)
	if strings.Contains(w2.Body.String(), s1.Id.String()) || strings.Contains(w2.Body.String(), s1.Slug) {
		t.Errorf("the 401 names the link: %s", w2.Body.String())
	}
	if n := e.linkCount(owner); n != 1 {
		t.Errorf("%d links; want 1", n)
	}
}

// AC-30: an explicit free slug; the second identical keyed request held after
// its key resolution and before its slug check (its context is tagged); the
// first commits; release: the replay, never the slug 409. With the title
// changed: the key-reused refusal. One link.
func TestPAC119_AC30_SlugPreCheckResolvesTheKeyAgain(t *testing.T) {
	for _, c := range []struct{ name, title2 string }{
		{"identical", "AC-30 link"},
		{"title changed", "AC-30 other"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newPAC119Env(t)
			owner := e.owner("ac30-owner")
			key := pac119NewKey()
			slug := "ac30-" + uuid.NewString()[:8]
			reached, gate := make(chan struct{}), make(chan struct{})
			var once sync.Once
			storage.InstallAfterLinkCreateKeyResolved(t, func(ctx context.Context, k string) {
				if !pac119Tagged(ctx, "held") || k != key {
					return
				}
				once.Do(func() {
					close(reached)
					<-gate
				})
			})
			second := pac119Async(func() *httptest.ResponseRecorder {
				return e.send(owner, key, fmt.Sprintf(`{"title":%q,"slug":%q}`, c.title2, slug), "held")
			})
			<-reached
			s1 := pac112Saved(t, e.send(owner, key, fmt.Sprintf(`{"title":"AC-30 link","slug":%q}`, slug), ""), http.StatusCreated)
			close(gate)
			w2 := pac119Wait(t, second, "held request")
			if c.title2 == "AC-30 link" {
				if s2 := pac112Saved(t, w2, http.StatusCreated); s2.Id != s1.Id {
					t.Fatalf("held request answered %s; want the replay of %s", s2.Id, s1.Id)
				}
			} else {
				pac112Body(t, w2, http.StatusConflict, pac119KeyLinkBody("idempotency key already used", s1.Id), "changed title")
			}
			if n := e.linkCount(owner); n != 1 {
				t.Errorf("%d links; want 1", n)
			}
		})
	}
}

// Contract md §4.3: a loser that waits past the link-lock bound gets the 500
// with nothing stored; a later retry then resolves by the table (the replay).
func TestPAC119_LoserPastTheBoundIs500ThenRetryReplays(t *testing.T) {
	e := newPAC119Env(t)
	pac101ShortBound(t)
	owner := e.owner("bound-owner")
	key := pac119NewKey()
	reached, release := pac119HoldFirstCommit(t)
	body := `{"title":"Bound link"}`
	first := pac119Async(func() *httptest.ResponseRecorder { return e.send(owner, key, body, "") })
	<-reached
	w2 := pac101WithinBound(t, "a same-key loser", func() *httptest.ResponseRecorder { return e.send(owner, key, body, "") })
	pac112Body(t, w2, http.StatusInternalServerError, `{"error":"internal error"}`, "loser past the bound")
	release()
	s1 := pac112Saved(t, pac119Wait(t, first, "winner"), http.StatusCreated)
	if s := pac112Saved(t, e.send(owner, key, body, ""), http.StatusCreated); s.Id != s1.Id {
		t.Fatalf("retry after the bound: %s; want %s", s.Id, s1.Id)
	}
	if n := e.linkCount(owner); n != 1 {
		t.Errorf("%d links; want 1", n)
	}
}

// Contract md §1.1: the key's 400 comes after requireAuth only; the body 400
// and field 422s keep their order for a well-formed key, and the replay is
// written by writeSavedLink like every 201 (decoded strictly). The refusal
// bodies decode as exactly the contract's 409 shapes.
func TestPAC119_RefusalEnvelopesAreExact(t *testing.T) {
	e := newPAC119Env(t)
	owner := e.owner("env-owner")
	key := pac119NewKey()
	pac112Body(t, e.send(owner, key, `{"title":`, ""), http.StatusBadRequest, `{"error":"invalid request body"}`, "keyed bad body")
	pac112Body(t, e.send(owner, key, `{"title":""}`, ""), http.StatusUnprocessableEntity, `{"error":"title is required"}`, "keyed 422")
	pac112Body(t, e.send(owner, key, `{"title":"x","co_host_emails":["  "]}`, ""), http.StatusUnprocessableEntity, pac112BlankMsg, "keyed blank entry")
	if e.keyRows(owner) != 0 {
		t.Fatal("a refused request stored a key")
	}
	s1 := pac112Saved(t, e.send(owner, key, `{"title":"Env link"}`, ""), http.StatusCreated)
	w := e.send(owner, key, `{"title":"Env other"}`, "")
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("refusal Content-Type %q", ct)
	}
	dec := json.NewDecoder(bytes.NewReader(w.Body.Bytes()))
	dec.DisallowUnknownFields()
	var body struct {
		Error  string    `json:"error"`
		LinkID uuid.UUID `json:"link_id"`
	}
	if err := dec.Decode(&body); err != nil || body.Error != "idempotency key already used" || body.LinkID != s1.Id {
		t.Fatalf("refusal %s (%v)", w.Body.String(), err)
	}
	if w.Header().Get("Idempotency-Key") != "" || strings.Contains(w.Body.String(), key) {
		t.Error("the key is echoed")
	}
}
