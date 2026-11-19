package api

// PAC-132 (docs/factory/silent-failures.md SF-029, the create/PATCH half; the
// factory decision of 2026-11-18): the create (201) and PATCH (200) answers
// read the co-host roster strictly inside the write transaction. A failed
// read (the host rows, or a host's user record) rolls the write back, so the
// answer is the documented 500 and the contract's "every 500 leaves nothing
// behind" (create) and "leaves the link exactly as it was" (PATCH) stay true.
// Before PAC-132 the roster was read after the commit, non-strictly: the
// answer was a 201/200 with the hosts emptied or a host's email blank.

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

var errPAC132Roster = errors.New("pac132: roster read failed")

// pac132Failure is one way the in-write roster read fails: hosts fails the
// host-rows read of every link; user fails the user-record read of victim.
type pac132Failure struct {
	name    string
	install func(t *testing.T, victim uuid.UUID) *int
}

func pac132Failures() []pac132Failure {
	return []pac132Failure{
		{"host rows unreadable", func(t *testing.T, _ uuid.UUID) *int {
			n := 0
			storage.InstallLinkRosterHosts(t, func(uuid.UUID) error { n++; return errPAC132Roster })
			return &n
		}},
		{"host user record unreadable", func(t *testing.T, victim uuid.UUID) *int {
			n := 0
			storage.InstallLinkRosterUser(t, func(_, user uuid.UUID) error {
				if user == victim {
					n++
					return errPAC132Roster
				}
				return nil
			})
			return &n
		}},
	}
}

func TestPAC132_CreateRosterFailureIs500AndLeavesNothing(t *testing.T) {
	for _, f := range pac132Failures() {
		for _, keyed := range []bool{false, true} {
			name := f.name + map[bool]string{false: ", keyless", true: ", keyed and guarded"}[keyed]
			t.Run(name, func(t *testing.T) {
				e := newPAC119Env(t)
				owner := e.owner("p132-create")
				_, bAddr := e.confirmed("p132-b")
				// PAC-149: B's invitation has no user record to read; the
				// owner's is the one that fails.
				fired := f.install(t, owner)
				reported, logs := captureServerErrors(t)
				body := map[string]any{"title": "PAC-132 create", "co_host_emails": []string{bAddr}}
				key := ""
				if keyed {
					key = pac119NewKey()
				}
				var w *httptest.ResponseRecorder
				if keyed {
					w = e.sendJSON(owner, key, body)
				} else {
					w = e.create(owner, body)
				}
				assertPAC122Internal500(t, name, w, reported, logs)
				if *fired == 0 {
					t.Fatal("the roster read never ran")
				}
				if n := e.linkCount(owner); n != 0 {
					t.Errorf("%d links after the 500; want none", n)
				}
				if e.hostRowsOf(owner) != 0 || pac149InvitationsTo(t, e.db, bAddr) != 0 {
					t.Error("the 500 left host rows")
				}
				if n := e.keyRows(owner); n != 0 {
					t.Errorf("%d key rows after the 500; want none", n)
				}
				if strings.Contains(w.Body.String(), bAddr) {
					t.Errorf("the 500 carries the co-host: %s", w.Body.String())
				}
			})
		}
	}
}

func TestPAC132_PatchRosterFailureIs500AndChangesNothing(t *testing.T) {
	for _, f := range pac132Failures() {
		t.Run(f.name, func(t *testing.T) {
			e := newPAC112Env(t)
			owner, _ := e.confirmed("p132-patch")
			b, _ := e.confirmed("p132-b")
			c, cAddr := e.confirmed("p132-c")
			l := e.link(owner, map[uuid.UUID]string{b: "accepted"})
			before, title := e.hostRows(l.ID), e.title(l.ID)
			fired := f.install(t, owner)
			reported, logs := captureServerErrors(t)
			// The PATCH renames the link, removes B and invites C: none of
			// it may survive the 500.
			w := e.patch(owner, l.ID, map[string]any{"title": "PAC-132 renamed", "co_host_emails": []string{cAddr}})
			assertPAC122Internal500(t, f.name, w, reported, logs)
			if *fired == 0 {
				t.Fatal("the roster read never ran")
			}
			if got := e.title(l.ID); got != title {
				t.Errorf("title %q after the 500; want %q", got, title)
			}
			pac112SameHosts(t, "after the 500", e.hostRows(l.ID), before)
			if e.hostRowsOf(c) != 0 {
				t.Error("the 500 left C invited")
			}
		})
	}
}

// The PATCH without co_host_emails reads the roster too: a fields-only edit
// is all or nothing as well.
func TestPAC132_FieldsOnlyPatchRosterFailureChangesNothing(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("p132-fields")
	l := e.link(owner, nil)
	title := e.title(l.ID)
	storage.InstallLinkRosterHosts(t, func(id uuid.UUID) error {
		if id == l.ID {
			return errPAC132Roster
		}
		return nil
	})
	reported, logs := captureServerErrors(t)
	w := e.patch(owner, l.ID, map[string]any{"title": "PAC-132 fields only"})
	assertPAC122Internal500(t, "fields-only PATCH", w, reported, logs)
	if got := e.title(l.ID); got != title {
		t.Errorf("title %q after the 500; want %q", got, title)
	}
}

// The answers' roster is the one read inside the write: every host, with its
// email, as the strict single read shows it right after.
func TestPAC132_AnswersCarryTheWholeRoster(t *testing.T) {
	e := newPAC112Env(t)
	owner, ownerAddr := e.confirmed("p132-ok")
	_, bAddr := e.confirmed("p132-ok-b")
	_, cAddr := e.confirmed("p132-ok-c")
	s := pac112Saved(t, e.create(owner, map[string]any{"co_host_emails": []string{bAddr}}), http.StatusCreated)
	pac132SameRoster(t, "create", s.Hosts, map[uuid.UUID]string{owner: ownerAddr + "/accepted", pac149AddrKey(bAddr): bAddr + "/pending"})
	p := pac112Saved(t, e.patch(owner, s.Id, map[string]any{"co_host_emails": []string{cAddr}}), http.StatusOK)
	pac132SameRoster(t, "patch", p.Hosts, map[uuid.UUID]string{owner: ownerAddr + "/accepted", pac149AddrKey(cAddr): cAddr + "/pending"})
}

func pac132SameRoster(t *testing.T, what string, hosts []gen.SchedulingLinkHost, want map[uuid.UUID]string) {
	t.Helper()
	got := map[uuid.UUID]string{}
	for _, h := range hosts {
		if h.UserId == nil {
			// PAC-149: a pending invitation is shown by address only.
			got[pac149AddrKey(h.Email)] = h.Email + "/" + string(h.Status)
			continue
		}
		got[*h.UserId] = string(h.Email) + "/" + string(h.Status)
	}
	pac112SameHosts(t, what, got, want)
}

// PAC-126: each generated-slug attempt is its own transaction, and the roster
// is read inside each. A slug lost on the first attempt, then a roster read
// that fails on the second, is the 500 with nothing behind; with the read
// working, the answer's roster is the second attempt's link.
func TestPAC132_RosterReadInsideTheGeneratedSlugRetry(t *testing.T) {
	t.Run("fails on the retry", func(t *testing.T) {
		e := newPAC119Env(t)
		owner := e.owner("p132-retry")
		inserts := pac126LoseSlug(t, 1)
		var read []uuid.UUID
		storage.InstallLinkRosterHosts(t, func(id uuid.UUID) error { read = append(read, id); return errPAC132Roster })
		reported, logs := captureServerErrors(t)
		assertPAC122Internal500(t, "retry roster", e.send(owner, "", `{"title":"PAC-132 retry"}`, ""), reported, logs)
		if *inserts != 2 || len(read) != 1 {
			t.Errorf("%d inserts and %d roster reads; want 2 and 1 (only the attempt that kept its slug reads)", *inserts, len(read))
		}
		if n := e.linkCount(owner); n != 0 {
			t.Errorf("%d links after the 500; want none", n)
		}
		if e.hostRowsOf(owner) != 0 {
			t.Error("the 500 left the owner's host row")
		}
	})
	t.Run("succeeds on the retry", func(t *testing.T) {
		e := newPAC119Env(t)
		owner := e.owner("p132-retry-ok")
		inserts := pac126LoseSlug(t, 1)
		var read []uuid.UUID
		storage.InstallLinkRosterHosts(t, func(id uuid.UUID) error { read = append(read, id); return nil })
		s := pac112Saved(t, e.send(owner, "", `{"title":"PAC-132 retry ok"}`, ""), http.StatusCreated)
		if *inserts != 2 || len(read) != 1 || read[0] != s.Id {
			t.Fatalf("%d inserts, roster reads %v; want 2 inserts and one read of %s", *inserts, read, s.Id)
		}
		if len(s.Hosts) != 1 || s.Hosts[0].Status != "accepted" || s.Hosts[0].Email == "" {
			t.Errorf("hosts %+v; want the owner, accepted, with an email", s.Hosts)
		}
	})
}

// The PAC-119 replay writes nothing and reads the roster after its resolution
// committed, strictly: a failed read is the 500 with nothing written, never a
// 201 with the hosts emptied.
func TestPAC132_ReplayRosterIsStrict(t *testing.T) {
	e := newPAC119Env(t)
	owner := e.owner("p132-replay")
	_, bAddr := e.confirmed("p132-replay-b")
	key := pac119NewKey()
	body := map[string]any{"title": "PAC-132 replay", "co_host_emails": []string{bAddr}}
	pac112Saved(t, e.sendJSON(owner, key, body), http.StatusCreated)
	hideTable(t, "scheduling_link_hosts")
	reported, logs := captureServerErrors(t)
	assertPAC122Internal500(t, "replay roster", e.sendJSON(owner, key, body), reported, logs)
	if n := e.linkCount(owner); n != 1 {
		t.Errorf("%d links; want the original only", n)
	}
	if n := e.keyRows(owner); n != 1 {
		t.Errorf("%d key rows; want 1", n)
	}
}
