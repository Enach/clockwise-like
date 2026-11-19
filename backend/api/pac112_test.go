package api

// PAC-112 (contracts/features/PAC-112.md): the link editor's save carries the
// complete co-host list, create is all or nothing, and the create and PATCH
// answers report what happened to every listed address (co_host_changes).
// Every test here is a [contract] test: a handler test against the test
// database, asserting the answer and the rows.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

const (
	pac112CapMsg   = `{"error":"a co-host list longer than 5 can only keep or remove co-hosts, and this one names someone who is not a current host of the link"}`
	pac112BlankMsg = `{"error":"co_host_emails must not contain a blank address"}`
	pac112DaysMsg  = `{"error":"days contains an unrecognised day name"}`
)

type pac112Env struct {
	*pac40Env
}

func newPAC112Env(t *testing.T) *pac112Env {
	t.Helper()
	return &pac112Env{newPAC40Env(t, openTestDB(t))}
}

// addr is a unique fixture address for this test run.
func pac112Addr(name string) string {
	return fmt.Sprintf("pac112-%s-%s@acme.test", name, uuid.NewString()[:8])
}

// confirmed is an account holding its address domain-confirmed.
func (e *pac112Env) confirmed(name string) (uuid.UUID, string) {
	e.t.Helper()
	email := pac112Addr(name)
	return createConfirmedUser(e.t, email), email
}

// unconfirmed is an account carrying its address only unconfirmed.
func (e *pac112Env) unconfirmed(name string) (uuid.UUID, string) {
	e.t.Helper()
	email := pac112Addr(name)
	return createTestUser(e.t, email), email
}

// link is a live link owned by owner, with owner's accepted row and the given
// co-hosts at the given statuses.
func (e *pac112Env) link(owner uuid.UUID, hosts map[uuid.UUID]string) *storage.SchedulingLink {
	e.t.Helper()
	l := createSchedulingLinkRecord(e.t, owner, "pac112-"+uuid.NewString()[:8], "PAC-112")
	if _, err := storage.AddLinkHost(e.db, l.ID, owner, "accepted"); err != nil {
		e.t.Fatal(err)
	}
	for id, status := range hosts {
		if _, err := storage.AddLinkHost(e.db, l.ID, id, status); err != nil {
			e.t.Fatal(err)
		}
	}
	return l
}

func pac112JSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (e *pac112Env) create(owner uuid.UUID, body map[string]any) *httptest.ResponseRecorder {
	e.t.Helper()
	if _, ok := body["title"]; !ok {
		body["title"] = "PAC-112 link"
	}
	return e.do(http.MethodPost, "/api/scheduling-links/", owner, pac112JSON(e.t, body))
}

func (e *pac112Env) patch(owner, id uuid.UUID, body map[string]any) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.do(http.MethodPatch, "/api/scheduling-links/"+id.String(), owner, pac112JSON(e.t, body))
}

// saved decodes a SchedulingLinkSaved strictly against the generated type:
// an unknown key (a field the contract does not have) fails the test.
func pac112Saved(t *testing.T, w *httptest.ResponseRecorder, wantCode int) gen.SchedulingLinkSaved {
	t.Helper()
	if w.Code != wantCode {
		t.Fatalf("status %d, want %d; body %s", w.Code, wantCode, w.Body.String())
	}
	dec := json.NewDecoder(bytes.NewReader(w.Body.Bytes()))
	dec.DisallowUnknownFields()
	var out gen.SchedulingLinkSaved
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("decode SchedulingLinkSaved: %v; body %s", err, w.Body.String())
	}
	return out
}

// outcomes is co_host_changes.addresses as "email=outcome" items.
func pac112Outcomes(t *testing.T, s gen.SchedulingLinkSaved) []string {
	t.Helper()
	if s.CoHostChanges == nil {
		t.Fatal("co_host_changes absent")
	}
	out := make([]string, 0, len(s.CoHostChanges.Addresses))
	for _, a := range s.CoHostChanges.Addresses {
		out = append(out, a.Email+"="+string(a.Outcome))
	}
	return out
}

func pac112Counts(t *testing.T, s gen.SchedulingLinkSaved, invited, removed, notAdded int) {
	t.Helper()
	c := s.CoHostChanges
	if c == nil {
		t.Fatal("co_host_changes absent")
	}
	if c.InvitedCount != invited || c.RemovedCount != removed || c.NotAddedCount != notAdded {
		t.Errorf("counts invited %d removed %d not_added %d; want %d %d %d",
			c.InvitedCount, c.RemovedCount, c.NotAddedCount, invited, removed, notAdded)
	}
	if c.RemovedCount != len(c.Removed) {
		t.Errorf("removed_count %d != len(removed) %d", c.RemovedCount, len(c.Removed))
	}
}

func pac112Equal(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("%s = %v; want %v", what, got, want)
	}
}

func pac112Body(t *testing.T, w *httptest.ResponseRecorder, code int, body, what string) {
	t.Helper()
	if w.Code != code || strings.TrimSpace(w.Body.String()) != body {
		t.Errorf("%s: %d %s; want %d %s", what, w.Code, w.Body.String(), code, body)
	}
}

// linkCount is how many links owner has, deleted ones included.
func (e *pac112Env) linkCount(owner uuid.UUID) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM scheduling_links WHERE owner_user_id = $1`, owner).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// hostRowsOf counts host rows for user on any link.
func (e *pac112Env) hostRowsOf(user uuid.UUID) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM scheduling_link_hosts WHERE user_id = $1`, user).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *pac112Env) title(id uuid.UUID) string {
	e.t.Helper()
	var title string
	if err := e.db.QueryRow(`SELECT title FROM scheduling_links WHERE id = $1`, id).Scan(&title); err != nil {
		e.t.Fatal(err)
	}
	return title
}

func pac112SameHosts(t *testing.T, what string, got, want map[uuid.UUID]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: host rows %v; want %v", what, got, want)
		return
	}
	for id, s := range want {
		if got[id] != s {
			t.Errorf("%s: host rows %v; want %v", what, got, want)
			return
		}
	}
}

// AC-7: a create listing B and C answers B invited and C not_added in the
// 201 itself; B is pending and no row exists for C.
func TestPAC112_AC7_CreateReportsEachAddress(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("ac7-owner")
	b, bAddr := e.confirmed("ac7-b")
	cAddr := pac112Addr("ac7-c-nobody")
	s := pac112Saved(t, e.create(owner, map[string]any{"co_host_emails": []string{bAddr, cAddr}}), http.StatusCreated)
	pac112Equal(t, "addresses", pac112Outcomes(t, s), []string{bAddr + "=invited", cAddr + "=not_added"})
	pac112Counts(t, s, 1, 0, 1)
	if len(s.CoHostChanges.Removed) != 0 {
		t.Errorf("removed on create = %v", s.CoHostChanges.Removed)
	}
	pac112SameHosts(t, "after create", e.hostRows(s.Id), map[uuid.UUID]string{owner: "accepted", b: "pending"})
}

// AC-8: a PATCH listing E, F, G and C on a link holding E, F and B reports E
// and F already_host, G invited, C not_added and B removed (with its status
// and stored address).
func TestPAC112_AC8_PatchReportsEveryOutcomeAndRemoval(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("ac8-owner")
	eID, eAddr := e.confirmed("ac8-e")
	f, fAddr := e.confirmed("ac8-f")
	b, bAddr := e.confirmed("ac8-b")
	g, gAddr := e.confirmed("ac8-g")
	cAddr := pac112Addr("ac8-c-nobody")
	l := e.link(owner, map[uuid.UUID]string{eID: "accepted", f: "declined", b: "pending"})
	s := pac112Saved(t, e.patch(owner, l.ID, map[string]any{"co_host_emails": []string{eAddr, fAddr, gAddr, cAddr}}), http.StatusOK)
	pac112Equal(t, "addresses", pac112Outcomes(t, s),
		[]string{eAddr + "=already_host", fAddr + "=already_host", gAddr + "=invited", cAddr + "=not_added"})
	pac112Counts(t, s, 1, 1, 1)
	if r := s.CoHostChanges.Removed; len(r) != 1 || r[0].UserId == nil || r[0].UserId.String() != b.String() || r[0].Email != bAddr || string(r[0].Status) != "pending" {
		t.Errorf("removed = %+v; want B (%s) pending", r, bAddr)
	}
	pac112SameHosts(t, "after PATCH", e.hostRows(l.ID),
		map[uuid.UUID]string{owner: "accepted", eID: "accepted", f: "declined", g: "pending"})
}

// AC-4 (contract half): adding G keeps E accepted and F declined (PAC-61
// through the list path).
func TestPAC112_AC4_AddingKeepsEveryStatus(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("ac4-owner")
	eID, eAddr := e.confirmed("ac4-e")
	f, fAddr := e.confirmed("ac4-f")
	g, gAddr := e.confirmed("ac4-g")
	l := e.link(owner, map[uuid.UUID]string{eID: "accepted", f: "declined"})
	s := pac112Saved(t, e.patch(owner, l.ID, map[string]any{"co_host_emails": []string{eAddr, fAddr, gAddr}}), http.StatusOK)
	pac112Counts(t, s, 1, 0, 0)
	pac112SameHosts(t, "after PATCH", e.hostRows(l.ID),
		map[uuid.UUID]string{owner: "accepted", eID: "accepted", f: "declined", g: "pending"})
}

// AC-9: a host listed by an address their account carries only unconfirmed
// is already_host and keeps their status, never not_added.
func TestPAC112_AC9_UnconfirmedCarrierHostIsAlreadyHost(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("ac9-owner")
	eID, eAddr := e.unconfirmed("ac9-e")
	l := e.link(owner, map[uuid.UUID]string{eID: "accepted"})
	listed := strings.ToUpper(eAddr)
	s := pac112Saved(t, e.patch(owner, l.ID, map[string]any{"co_host_emails": []string{listed}}), http.StatusOK)
	pac112Equal(t, "addresses", pac112Outcomes(t, s), []string{listed + "=already_host"})
	pac112Counts(t, s, 0, 0, 0)
	pac112SameHosts(t, "after PATCH", e.hostRows(l.ID), map[uuid.UUID]string{owner: "accepted", eID: "accepted"})
}

// AC-10: nobody (C), an unconfirmed carrier (D) and an address two accounts
// share in different case each report the same not_added and write no row,
// on create and on PATCH. A failed lookup is the all-or-nothing 500, never
// not_added.
func TestPAC112_AC10_UninvitableAddressesAreOneOutcome(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("ac10-owner")
	cAddr := pac112Addr("ac10-c-nobody")
	d, dAddr := e.unconfirmed("ac10-d")
	shared := pac112Addr("ac10-shared")
	s1 := createConfirmedUser(t, shared)
	s2 := createConfirmedUser(t, strings.ToUpper(shared))
	for _, addr := range []string{cAddr, dAddr, shared} {
		s := pac112Saved(t, e.create(owner, map[string]any{"co_host_emails": []string{addr}}), http.StatusCreated)
		pac112Equal(t, "create "+addr, pac112Outcomes(t, s), []string{addr + "=not_added"})
		pac112Counts(t, s, 0, 0, 1)
		pac112SameHosts(t, "create "+addr, e.hostRows(s.Id), map[uuid.UUID]string{owner: "accepted"})

		l := e.link(owner, nil)
		s = pac112Saved(t, e.patch(owner, l.ID, map[string]any{"co_host_emails": []string{addr}}), http.StatusOK)
		pac112Equal(t, "PATCH "+addr, pac112Outcomes(t, s), []string{addr + "=not_added"})
		pac112Counts(t, s, 0, 0, 1)
		pac112SameHosts(t, "PATCH "+addr, e.hostRows(l.ID), map[uuid.UUID]string{owner: "accepted"})
	}
	for _, id := range []uuid.UUID{d, s1, s2} {
		if n := e.hostRowsOf(id); n != 0 {
			t.Errorf("%s has %d host rows", id, n)
		}
	}

	// The failed lookup.
	b, bAddr := e.confirmed("ac10-b")
	storage.InstallCoHostLookup(t, func(string) error { return errors.New("pac112: lookup failed") })
	before := e.linkCount(owner)
	pac112Body(t, e.create(owner, map[string]any{"co_host_emails": []string{bAddr}}), http.StatusInternalServerError,
		`{"error":"internal error"}`, "create with a failed lookup")
	if n := e.linkCount(owner); n != before {
		t.Errorf("links %d -> %d: a create with a failed lookup left a link", before, n)
	}
	l := e.link(owner, nil)
	pac112Body(t, e.patch(owner, l.ID, map[string]any{"title": "changed", "co_host_emails": []string{bAddr}}),
		http.StatusInternalServerError, `{"error":"internal error"}`, "PATCH with a failed lookup")
	if got := e.title(l.ID); got != l.Title {
		t.Errorf("title %q after a failed PATCH", got)
	}
	if n := e.hostRowsOf(b); n != 0 {
		t.Errorf("B has %d host rows after failed lookups", n)
	}
}

// AC-15: a create listing the owner's own confirmed address (another case)
// reports own_address and never demotes the owner (API-048 create remnant).
func TestPAC112_AC15_CreateWithOwnAddressKeepsOwnerAccepted(t *testing.T) {
	e := newPAC112Env(t)
	owner, ownerAddr := e.confirmed("ac15-owner")
	listed := strings.ToUpper(ownerAddr)
	s := pac112Saved(t, e.create(owner, map[string]any{"co_host_emails": []string{listed}}), http.StatusCreated)
	pac112Equal(t, "addresses", pac112Outcomes(t, s), []string{listed + "=own_address"})
	pac112Counts(t, s, 0, 0, 0)
	pac112SameHosts(t, "owner row", e.hostRows(s.Id), map[uuid.UUID]string{owner: "accepted"})
	invites, err := storage.GetPendingInvitesForUser(e.db, owner)
	if err != nil {
		t.Fatal(err)
	}
	for _, inv := range invites {
		if inv.LinkID == s.Id {
			t.Error("the link is in the owner's invitation list")
		}
	}
	accepted, err := storage.GetAcceptedHosts(e.db, s.Id)
	if err != nil {
		t.Fatal(err)
	}
	if len(accepted) != 1 || accepted[0].UserID != owner {
		t.Errorf("accepted hosts = %v; want the owner", accepted)
	}
}

// AC-16: PATCH with the owner's confirmed address: own_address, row
// unchanged. The owner's address held unconfirmed: not_added on create and
// PATCH, row unchanged.
func TestPAC112_AC16_OwnAddressNeverChangesTheOwnerRow(t *testing.T) {
	e := newPAC112Env(t)
	owner, ownerAddr := e.confirmed("ac16-owner")
	l := e.link(owner, nil)
	s := pac112Saved(t, e.patch(owner, l.ID, map[string]any{"co_host_emails": []string{ownerAddr}}), http.StatusOK)
	pac112Equal(t, "PATCH confirmed", pac112Outcomes(t, s), []string{ownerAddr + "=own_address"})
	pac112SameHosts(t, "PATCH confirmed", e.hostRows(l.ID), map[uuid.UUID]string{owner: "accepted"})

	un, unAddr := e.unconfirmed("ac16-owner-unconfirmed")
	s = pac112Saved(t, e.create(un, map[string]any{"co_host_emails": []string{unAddr}}), http.StatusCreated)
	pac112Equal(t, "create unconfirmed", pac112Outcomes(t, s), []string{unAddr + "=not_added"})
	pac112SameHosts(t, "create unconfirmed", e.hostRows(s.Id), map[uuid.UUID]string{un: "accepted"})
	l2 := e.link(un, nil)
	s = pac112Saved(t, e.patch(un, l2.ID, map[string]any{"co_host_emails": []string{unAddr}}), http.StatusOK)
	pac112Equal(t, "PATCH unconfirmed", pac112Outcomes(t, s), []string{unAddr + "=not_added"})
	pac112SameHosts(t, "PATCH unconfirmed", e.hostRows(l2.ID), map[uuid.UUID]string{un: "accepted"})
}

// AC-18: two addresses reaching the same account B: one row, both invited,
// invited_count 1.
func TestPAC112_AC18_TwoAddressesOnePerson(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("ac18-owner")
	b, bAddr := e.confirmed("ac18-b")
	other := strings.ToUpper(bAddr)
	for _, verb := range []string{"create", "PATCH"} {
		var s gen.SchedulingLinkSaved
		var id uuid.UUID
		body := map[string]any{"co_host_emails": []string{bAddr, other}}
		if verb == "create" {
			s = pac112Saved(t, e.create(owner, body), http.StatusCreated)
			id = s.Id
		} else {
			id = e.link(owner, nil).ID
			s = pac112Saved(t, e.patch(owner, id, body), http.StatusOK)
		}
		pac112Equal(t, verb, pac112Outcomes(t, s), []string{bAddr + "=invited", other + "=invited"})
		pac112Counts(t, s, 1, 0, 0)
		pac112SameHosts(t, verb, e.hostRows(id), map[uuid.UUID]string{owner: "accepted", b: "pending"})
	}
}

// AC-19: six entries that are not hosts: create 422 and no link; PATCH on a
// link with no co-hosts 422 and nothing changed.
func TestPAC112_AC19_SixNewEntriesAreRefused(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("ac19-owner")
	list := make([]string, 6)
	for i := range list {
		_, list[i] = e.confirmed(fmt.Sprintf("ac19-%d", i))
	}
	before := e.linkCount(owner)
	pac112Body(t, e.create(owner, map[string]any{"co_host_emails": list}), http.StatusUnprocessableEntity, pac112CapMsg, "create with 6")
	if n := e.linkCount(owner); n != before {
		t.Errorf("links %d -> %d after a refused create", before, n)
	}
	l := e.link(owner, nil)
	pac112Body(t, e.patch(owner, l.ID, map[string]any{"title": "changed", "co_host_emails": list}),
		http.StatusUnprocessableEntity, pac112CapMsg, "PATCH with 6")
	if got := e.title(l.ID); got != l.Title {
		t.Errorf("title changed to %q by a refused PATCH", got)
	}
	pac112SameHosts(t, "after refused PATCH", e.hostRows(l.ID), map[uuid.UUID]string{owner: "accepted"})
}

// pac112Seven is a link with 7 co-hosts at mixed statuses and their addresses.
func (e *pac112Env) seven(owner uuid.UUID, tag string) (*storage.SchedulingLink, []uuid.UUID, []string, map[uuid.UUID]string) {
	e.t.Helper()
	statuses := []string{"accepted", "pending", "declined", "accepted", "pending", "accepted", "declined"}
	ids, addrs := make([]uuid.UUID, 7), make([]string, 7)
	hosts := map[uuid.UUID]string{}
	for i := range ids {
		ids[i], addrs[i] = e.confirmed(fmt.Sprintf("%s-%d", tag, i))
		hosts[ids[i]] = statuses[i]
	}
	l := e.link(owner, hosts)
	all := map[uuid.UUID]string{owner: "accepted"}
	for id, s := range hosts {
		all[id] = s
	}
	return l, ids, addrs, all
}

// AC-32 (contract half, regression guard): removing one of 7 co-hosts with a
// 6-entry keep-only list is accepted; the others keep their status.
func TestPAC112_AC32_RemoveOneOfSeven(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("ac32-owner")
	l, ids, addrs, all := e.seven(owner, "ac32")
	s := pac112Saved(t, e.patch(owner, l.ID, map[string]any{"co_host_emails": addrs[1:]}), http.StatusOK)
	pac112Counts(t, s, 0, 1, 0)
	if r := s.CoHostChanges.Removed; len(r) != 1 || r[0].UserId == nil || r[0].UserId.String() != ids[0].String() || string(r[0].Status) != "accepted" {
		t.Errorf("removed = %+v", r)
	}
	delete(all, ids[0])
	pac112SameHosts(t, "after removing one", e.hostRows(l.ID), all)
}

// AC-33 (contract half): the 7 plus G is refused with the cap text, nothing
// changed.
func TestPAC112_AC33_AddingToSevenIsRefused(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("ac33-owner")
	l, _, addrs, all := e.seven(owner, "ac33")
	g, gAddr := e.confirmed("ac33-g")
	pac112Body(t, e.patch(owner, l.ID, map[string]any{"title": "changed", "co_host_emails": append(addrs, gAddr)}),
		http.StatusUnprocessableEntity, pac112CapMsg, "7 + G")
	pac112SameHosts(t, "after refusal", e.hostRows(l.ID), all)
	if e.title(l.ID) != l.Title || e.hostRowsOf(g) != 0 {
		t.Error("a refused PATCH changed the title or invited G")
	}
}

// AC-34: "" or "   " in the list is 422 with the blank text, nothing written.
func TestPAC112_AC34_BlankEntryIsRefused(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("ac34-owner")
	b, bAddr := e.confirmed("ac34-b")
	for _, blank := range []string{"", "   "} {
		before := e.linkCount(owner)
		pac112Body(t, e.create(owner, map[string]any{"co_host_emails": []string{bAddr, blank}}),
			http.StatusUnprocessableEntity, pac112BlankMsg, fmt.Sprintf("create %q", blank))
		if n := e.linkCount(owner); n != before {
			t.Errorf("create %q left a link", blank)
		}
		l := e.link(owner, nil)
		pac112Body(t, e.patch(owner, l.ID, map[string]any{"title": "changed", "co_host_emails": []string{bAddr, blank}}),
			http.StatusUnprocessableEntity, pac112BlankMsg, fmt.Sprintf("PATCH %q", blank))
		if e.title(l.ID) != l.Title {
			t.Errorf("PATCH %q changed the title", blank)
		}
	}
	if n := e.hostRowsOf(b); n != 0 {
		t.Errorf("B has %d host rows", n)
	}
}

// AC-31 (contract half): a list containing E keeps E (the editor's second
// save, built from the strict re-read, carries {E, G}).
func TestPAC112_AC31_ListWithEKeepsE(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("ac31-owner")
	eID, eAddr := e.confirmed("ac31-e")
	g, gAddr := e.confirmed("ac31-g")
	l := e.link(owner, map[uuid.UUID]string{eID: "accepted"})
	s := pac112Saved(t, e.patch(owner, l.ID, map[string]any{"co_host_emails": []string{eAddr, gAddr}}), http.StatusOK)
	pac112Counts(t, s, 1, 0, 0)
	pac112SameHosts(t, "after PATCH", e.hostRows(l.ID), map[uuid.UUID]string{owner: "accepted", eID: "accepted", g: "pending"})
}

// AC-20, AC-21, AC-22: a failed co-host write, co-host lookup or owner-row
// write on create is the 500 with no link and no host row. Each also runs
// under a session guard (the PAC-71 re-check opens the transaction).
func TestPAC112_AC20_AC21_AC22_FailedCreateLeavesNothing(t *testing.T) {
	cases := []struct {
		name    string
		install func(t *testing.T)
	}{
		{"AC-20 co-host write", func(t *testing.T) {
			storage.InstallCoHostWrite(t, func(uuid.UUID, uuid.UUID) error { return errors.New("pac112: write failed") })
		}},
		{"AC-21 lookup", func(t *testing.T) {
			storage.InstallCoHostLookup(t, func(string) error { return errors.New("pac112: lookup failed") })
		}},
		{"AC-22 owner row", func(t *testing.T) {
			storage.InstallCreateOwnerHost(t, func(uuid.UUID) error { return errors.New("pac112: owner row failed") })
		}},
	}
	for _, c := range cases {
		for _, guarded := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s guarded=%v", c.name, guarded), func(t *testing.T) {
				e := newPAC112Env(t)
				owner, _ := e.confirmed("ac20-owner")
				b, bAddr := e.confirmed("ac20-b")
				c.install(t)
				body := pac112JSON(t, map[string]any{"title": "PAC-112", "co_host_emails": []string{bAddr}})
				req := withUser(httptest.NewRequest(http.MethodPost, "/api/scheduling-links/", strings.NewReader(body)), owner)
				if guarded {
					gen, ok, err := storage.CurrentGeneration(e.db, owner)
					if err != nil || !ok {
						t.Fatalf("generation: %v %v", ok, err)
					}
					req = req.WithContext(storage.WithSessionGuard(req.Context(), &storage.SessionGuard{UserID: owner, Gen: gen}))
				}
				w := httptest.NewRecorder()
				e.r.ServeHTTP(w, req)
				pac112Body(t, w, http.StatusInternalServerError, `{"error":"internal error"}`, c.name)
				if n := e.linkCount(owner); n != 0 {
					t.Errorf("%d links exist after a failed create", n)
				}
				if e.hostRowsOf(owner) != 0 || e.hostRowsOf(b) != 0 {
					t.Error("a failed create left host rows")
				}
			})
		}
	}
}

// Contract md §3.1: every lock wait in the create is bounded like PATCH's. An
// invitation insert takes FOR KEY SHARE on the invitee's users row; with that
// row held FOR UPDATE, the create fails at the (shortened) bound with the 500
// and leaves no link.
func TestPAC112_CreateLockWaitIsBounded(t *testing.T) {
	e := newPAC112Env(t)
	pac101ShortBound(t)
	owner, _ := e.confirmed("bound-owner")
	b, bAddr := e.confirmed("bound-b")
	holder, err := e.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback() }()
	if _, err := holder.Exec(`SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, b); err != nil {
		t.Fatal(err)
	}
	w := pac101WithinBound(t, "create waiting on an invitee's row", func() *httptest.ResponseRecorder {
		return e.create(owner, map[string]any{"co_host_emails": []string{bAddr}})
	})
	pac112Body(t, w, http.StatusInternalServerError, `{"error":"internal error"}`, "create past the bound")
	_ = holder.Rollback()
	if n := e.linkCount(owner); n != 0 {
		t.Errorf("%d links after a create that hit the bound", n)
	}
}

// AC-27: an unrecognised name in the days alias (alone or mixed), without
// days_of_week, is 422 with the days text and no link; the days text comes
// before every other field 422 (title is required).
func TestPAC112_AC27_CreateUnrecognisedDayName(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("ac27-owner")
	for _, body := range []map[string]any{
		{"days": []string{"Montag"}},
		{"days": []string{"mon", "Montag"}},
		{"days": []string{"Montag"}, "days_of_week": nil},
		{"title": "", "days": []string{"Montag"}},
	} {
		pac112Body(t, e.create(owner, body), http.StatusUnprocessableEntity, pac112DaysMsg, fmt.Sprint(body))
	}
	if n := e.linkCount(owner); n != 0 {
		t.Errorf("%d links after refused creates", n)
	}
}

// AC-28: PATCH with an unrecognised alias name is 422, link unchanged, and
// before "slug cannot be empty". days_of_week carried wins and the alias is
// ignored entirely, on create and PATCH (regression guard, PAC-40 D-5).
func TestPAC112_AC28_PatchDaysAndCanonicalSpellingWins(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("ac28-owner")
	l := e.link(owner, nil)
	pac112Body(t, e.patch(owner, l.ID, map[string]any{"title": "changed", "days": []string{"mon", "Montag"}}),
		http.StatusUnprocessableEntity, pac112DaysMsg, "PATCH mixed days")
	pac112Body(t, e.patch(owner, l.ID, map[string]any{"slug": "", "days": []string{"Montag"}}),
		http.StatusUnprocessableEntity, pac112DaysMsg, "PATCH days before slug")
	var days []int64
	if err := e.db.QueryRow(`SELECT title, days_of_week FROM scheduling_links WHERE id = $1`, l.ID).Scan(new(string), (*int64Array)(&days)); err != nil {
		t.Fatal(err)
	}
	if e.title(l.ID) != l.Title || len(days) != 5 {
		t.Errorf("link changed: title %q days %v", e.title(l.ID), days)
	}

	s := pac112Saved(t, e.create(owner, map[string]any{"days_of_week": []int{1}, "days": []string{"Montag"}}), http.StatusCreated)
	if len(s.Days) != 1 || string(s.Days[0]) != "mon" {
		t.Errorf("create days = %v; want [mon]", s.Days)
	}
	s = pac112Saved(t, e.patch(owner, l.ID, map[string]any{"days_of_week": []int{1}, "days": []string{"Montag"}}), http.StatusOK)
	if len(s.Days) != 1 || string(s.Days[0]) != "mon" {
		t.Errorf("PATCH days = %v; want [mon]", s.Days)
	}
	if s.CoHostChanges != nil {
		t.Error("co_host_changes present without a list")
	}
}

// int64Array scans a PostgreSQL integer array.
type int64Array []int64

func (a *int64Array) Scan(src any) error {
	var raw string
	switch v := src.(type) {
	case []byte:
		raw = string(v)
	case string:
		raw = v
	default:
		return fmt.Errorf("int64Array: %T", src)
	}
	raw = strings.Trim(raw, "{}")
	*a = (*a)[:0]
	if raw == "" {
		return nil
	}
	for _, part := range strings.Split(raw, ",") {
		var n int64
		if _, err := fmt.Sscan(part, &n); err != nil {
			return err
		}
		*a = append(*a, n)
	}
	return nil
}

var _ sql.Scanner = (*int64Array)(nil)

// C-1 (md §3.3, P3-m1): the over-5 rule is decided against the host set under
// the lock. After X is removed by a committed PATCH, a 6-entry list still
// naming X is refused with the cap text, nothing changed.
func TestPAC112_C1_OverFiveDecidedAgainstTheCurrentHosts(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("c1-owner")
	l, ids, addrs, all := e.seven(owner, "c1")
	pac112Saved(t, e.patch(owner, l.ID, map[string]any{"co_host_emails": addrs[1:]}), http.StatusOK) // X = ids[0] removed
	delete(all, ids[0])
	stale := append([]string{addrs[0]}, addrs[1:6]...)
	pac112Body(t, e.patch(owner, l.ID, map[string]any{"title": "changed", "co_host_emails": stale}),
		http.StatusUnprocessableEntity, pac112CapMsg, "stale 6-entry list")
	pac112SameHosts(t, "after refusal", e.hostRows(l.ID), all)
	if e.title(l.ID) != l.Title {
		t.Error("a refused PATCH changed the title")
	}
}

// C-1, ordering half: the rule uses the host set read inside the PATCH's
// transaction after the UPDATE, never an earlier read. A removal committed
// after the handler's own reads and after the UPDATE (PAC-101's seam; the
// host-row delete does not wait on the link's row lock) makes a 7-entry list
// that was keep-only when the request started refused.
func TestPAC112_C1_RuleSeesTheSetUnderTheLock(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("c1b-owner")
	l, ids, addrs, all := e.seven(owner, "c1b")
	var once sync.Once
	storage.InstallAfterLinkPatchUpdate(t, func(uuid.UUID) error {
		var err error
		once.Do(func() { _, err = storage.RemoveLinkHost(e.db, l.ID, ids[6]) })
		return err
	})
	delete(all, ids[6])
	pac112Body(t, e.patch(owner, l.ID, map[string]any{"co_host_emails": addrs}),
		http.StatusUnprocessableEntity, pac112CapMsg, "7-entry list naming a removed host")
	pac112SameHosts(t, "after refusal", e.hostRows(l.ID), all)
}

// C-2 (md §3.3, P3-m2): keep-only lists over 5 with two spellings of one
// co-host, or with the owner's address, are accepted; a 50-entry list whose
// first entry is junk is refused after exactly one lookup, of that entry;
// repeated addresses (identical after the ECMA trim and lower()) are looked
// up once, in request order.
func TestPAC112_C2_LookupOrderAndCount(t *testing.T) {
	e := newPAC112Env(t)
	owner, ownerAddr := e.confirmed("c2-owner")
	l, _, addrs, all := e.seven(owner, "c2")

	var mu sync.Mutex
	var looked []string
	storage.InstallCoHostLookup(t, func(email string) error {
		mu.Lock()
		defer mu.Unlock()
		looked = append(looked, email)
		return nil
	})
	reset := func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := looked
		looked = nil
		return out
	}

	twoSpellings := []string{addrs[0], strings.ToUpper(addrs[0]), addrs[1], addrs[2], addrs[3], addrs[4]}
	s := pac112Saved(t, e.patch(owner, l.ID, map[string]any{"co_host_emails": append(twoSpellings, addrs[5], addrs[6])}), http.StatusOK)
	pac112Counts(t, s, 0, 0, 0)
	pac112Equal(t, "lookups for two spellings", reset(), []string{addrs[0], addrs[1], addrs[2], addrs[3], addrs[4], addrs[5], addrs[6]})

	withOwner := append([]string{ownerAddr}, addrs...)
	s = pac112Saved(t, e.patch(owner, l.ID, map[string]any{"co_host_emails": withOwner}), http.StatusOK)
	pac112Counts(t, s, 0, 0, 0)
	if got := pac112Outcomes(t, s)[0]; got != ownerAddr+"=own_address" {
		t.Errorf("owner entry = %s", got)
	}
	reset()
	pac112SameHosts(t, "keep-only lists", e.hostRows(l.ID), all)

	junk := make([]string, 50)
	junk[0] = pac112Addr("c2-junk-first")
	for i := 1; i < 50; i++ {
		junk[i] = pac112Addr(fmt.Sprintf("c2-junk-%d", i))
	}
	pac112Body(t, e.patch(owner, l.ID, map[string]any{"co_host_emails": junk}), http.StatusUnprocessableEntity, pac112CapMsg, "50 junk")
	pac112Equal(t, "lookups for 50 junk", reset(), []string{junk[0]})

	// Keep-only prefix, then junk: refused at the first non-host, one lookup
	// past the prefix.
	mixed := append(append([]string{}, addrs[:4]...), junk[1], junk[2])
	pac112Body(t, e.patch(owner, l.ID, map[string]any{"co_host_emails": mixed}), http.StatusUnprocessableEntity, pac112CapMsg, "prefix then junk")
	pac112Equal(t, "lookups for prefix then junk", reset(), append(append([]string{}, addrs[:4]...), junk[1]))

	// Repeats identical after the ECMA trim and lower() share one lookup.
	other := e.link(owner, nil)
	b, bAddr := e.confirmed("c2-b")
	reps := []string{bAddr, " " + strings.ToUpper(bAddr) + "\u00a0", "\ufeff" + bAddr}
	s = pac112Saved(t, e.patch(owner, other.ID, map[string]any{"co_host_emails": reps}), http.StatusOK)
	pac112Equal(t, "lookups for repeats", reset(), []string{bAddr})
	pac112Equal(t, "repeat outcomes", pac112Outcomes(t, s),
		[]string{bAddr + "=invited", strings.ToUpper(bAddr) + "=invited", bAddr + "=invited"})
	pac112Counts(t, s, 1, 0, 0)
	pac112SameHosts(t, "repeats", e.hostRows(other.ID), map[uuid.UUID]string{owner: "accepted", b: "pending"})
}

// C-3 (md §2.1): addresses has the request's length and order, duplicates
// included; co_host_changes is absent when co_host_emails is absent or null,
// and present with empty arrays for [].
func TestPAC112_C3_PresenceAndShape(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("c3-owner")
	_, bAddr := e.confirmed("c3-b")
	cAddr := pac112Addr("c3-c")
	list := []string{cAddr, bAddr, cAddr, bAddr}
	s := pac112Saved(t, e.create(owner, map[string]any{"co_host_emails": list}), http.StatusCreated)
	pac112Equal(t, "create duplicates", pac112Outcomes(t, s),
		[]string{cAddr + "=not_added", bAddr + "=invited", cAddr + "=not_added", bAddr + "=invited"})
	pac112Counts(t, s, 1, 0, 1)

	for _, body := range []map[string]any{{}, {"co_host_emails": nil}} {
		w := e.create(owner, body)
		if s := pac112Saved(t, w, http.StatusCreated); s.CoHostChanges != nil || strings.Contains(w.Body.String(), "co_host_changes") {
			t.Errorf("create %v: co_host_changes present", body)
		}
		l := e.link(owner, nil)
		w = e.patch(owner, l.ID, body)
		if s := pac112Saved(t, w, http.StatusOK); s.CoHostChanges != nil || strings.Contains(w.Body.String(), "co_host_changes") {
			t.Errorf("PATCH %v: co_host_changes present", body)
		}
	}

	w := e.create(owner, map[string]any{"co_host_emails": []string{}})
	pac112Saved(t, w, http.StatusCreated)
	if !strings.Contains(w.Body.String(), `"co_host_changes":{"addresses":[],"removed":[],"invited_count":0,"removed_count":0,"not_added_count":0}`) {
		t.Errorf("create []: body %s", w.Body.String())
	}
	l := e.link(owner, nil)
	w = e.patch(owner, l.ID, map[string]any{"co_host_emails": []string{}})
	pac112Saved(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"co_host_changes":{"addresses":[],"removed":[],"invited_count":0,"removed_count":0,"not_added_count":0}`) {
		t.Errorf("PATCH []: body %s", w.Body.String())
	}
}

// C-4 (md §3.4, challenge C-M2): blank is the ECMA-262 \s test. U+0085 is
// not blank (not_added); U+FEFF is (422, nothing written). Entries are trimmed
// of ECMA \s in addresses[].email.
func TestPAC112_C4_BlankIsTheECMATest(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("c4-owner")
	s := pac112Saved(t, e.create(owner, map[string]any{"co_host_emails": []string{"\u0085"}}), http.StatusCreated)
	pac112Equal(t, "create U+0085", pac112Outcomes(t, s), []string{"\u0085=not_added"})
	l := e.link(owner, nil)
	s = pac112Saved(t, e.patch(owner, l.ID, map[string]any{"co_host_emails": []string{"\u0085"}}), http.StatusOK)
	pac112Equal(t, "PATCH U+0085", pac112Outcomes(t, s), []string{"\u0085=not_added"})

	before := e.linkCount(owner)
	pac112Body(t, e.create(owner, map[string]any{"co_host_emails": []string{"\ufeff"}}), http.StatusUnprocessableEntity, pac112BlankMsg, "create U+FEFF")
	if n := e.linkCount(owner); n != before {
		t.Error("a refused create left a link")
	}
	pac112Body(t, e.patch(owner, l.ID, map[string]any{"title": "changed", "co_host_emails": []string{"\ufeff"}}),
		http.StatusUnprocessableEntity, pac112BlankMsg, "PATCH U+FEFF")
	if e.title(l.ID) != l.Title {
		t.Error("a refused PATCH changed the title")
	}

	_, bAddr := e.confirmed("c4-b")
	s = pac112Saved(t, e.patch(owner, l.ID, map[string]any{"co_host_emails": []string{"\u3000 " + bAddr + "\t\ufeff"}}), http.StatusOK)
	pac112Equal(t, "trimmed", pac112Outcomes(t, s), []string{bAddr + "=invited"})
}

// Spec §6: the e2e create with one invitable co-host keeps answering 201
// with id, and the old web's per-address POST /{id}/hosts still works next to
// a list-free PATCH (no co_host_changes, co-hosts untouched).
func TestPAC112_OldClientsUnaffected(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("old-owner")
	b, _ := e.confirmed("old-b")
	l := e.link(owner, map[uuid.UUID]string{b: "accepted"})
	w := e.patch(owner, l.ID, map[string]any{"title": "renamed"})
	if s := pac112Saved(t, w, http.StatusOK); s.CoHostChanges != nil || s.Title != "renamed" {
		t.Errorf("list-free PATCH: %s", w.Body.String())
	}
	pac112SameHosts(t, "list-free PATCH", e.hostRows(l.ID), map[uuid.UUID]string{owner: "accepted", b: "accepted"})
}

// AC-3, the public-slots clause (PAC-118): an accepted co-host's busy time
// removes the link's slot while they are a host, and no longer does once the
// owner's PATCH leaves them out of the list. The busy time comes from the
// co-host's calendar on the recording stand-in (PAC-72 bench), read by the
// real booking engine; the removal goes through the real PATCH handler.
func TestPAC112_AC3_RemovedCoHostsBusyTimeNoLongerRemovesSlots(t *testing.T) {
	b := newPAC72Bench(t, pac72At(pac72Mon, "06:00"))
	slh := newSchedulingLinkHandlers(b.db, nil)
	b.r.Patch("/api/scheduling-links/{id}", slh.updateLink)

	owner, _ := b.host(pac112Addr("ac3-slots-owner"))
	coHost, coBearer := b.host(pac112Addr("ac3-slots-e"))
	link := b.link("pac112-ac3-"+uuid.NewString()[:8], owner, nil, owner, coHost)
	b.cal.addEvent(coBearer, pac72At(pac72Tue, "10:00"), pac72At(pac72Tue, "10:30"))
	busy := pac72At(pac72Tue, "10:00")

	if got := b.slots(link.Slug, "2030-01-08", 30); got.code != http.StatusOK || got.offers(busy) || !got.offers(pac72At(pac72Tue, "09:00")) {
		t.Fatalf("with the co-host: listing = %d %v, want 09:00 offered and 10:00 not", got.code, got.starts())
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/scheduling-links/"+link.ID.String(),
		strings.NewReader(`{"co_host_emails":[]}`))
	w := httptest.NewRecorder()
	b.r.ServeHTTP(w, withUser(req, owner))
	s := pac112Saved(t, w, http.StatusOK)
	pac112Counts(t, s, 0, 1, 0)

	if got := b.slots(link.Slug, "2030-01-08", 30); got.code != http.StatusOK || !got.offers(busy) {
		t.Errorf("after the removal: listing = %d %v, want 10:00 offered", got.code, got.starts())
	}
}
