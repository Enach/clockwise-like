package api

// PAC-149 (docs/specs/PAC-149.md; contracts/features/PAC-149.md): a co-host
// invitation is to an address. Nothing the owner or an accepted co-host can
// observe about a pending invitation depends on whether an account carries
// the address; the address is matched to an account only on the invitee's
// side, for the caller's own address, and only for its single
// domain-confirmed holder. Every test here is a [contract] test (a handler
// test against the test database) except AC-6, the structural [unit] test.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// pac149Four is the spec's AC fixture (§4 "Fixtures"): four addresses of equal
// length, unique to the run. H is held domain-confirmed by one account that
// has nothing to do with the owner (with a name and a picture); N is carried
// by no account; U only by an unconfirmed account; C by two accounts that
// differ in letter case (legacy, email_key NULL).
type pac149Four struct {
	H, N, U, C string
	hID, uID   uuid.UUID
	c1, c2     uuid.UUID
}

func (a pac149Four) all() []string { return []string{a.H, a.N, a.U, a.C} }

func (e *pac112Env) pac149Four() pac149Four {
	e.t.Helper()
	run := uuid.NewString()[:8]
	a := pac149Four{
		H: "hold-" + run + "@ext.example", N: "none-" + run + "@ext.example",
		U: "uncf-" + run + "@ext.example", C: "coll-" + run + "@ext.example",
	}
	a.hID = createConfirmedUser(e.t, a.H)
	if _, err := e.db.Exec(`UPDATE users SET name = 'Holder Person', avatar_url = 'https://pictures.example/h.png' WHERE id = $1`, a.hID); err != nil {
		e.t.Fatal(err)
	}
	a.uID = createTestUser(e.t, a.U)
	a.c1 = createConfirmedUser(e.t, a.C)
	a.c2 = createConfirmedUser(e.t, strings.ToUpper(a.C[:1])+a.C[1:])
	return a
}

var (
	pac149UUIDRe = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	pac149TimeRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})`)
)

// pac149Mask is spec §3.1's "modulo the address": each address in subst (and
// its lower- and upper-case forms) becomes its token, then server-assigned
// ids and instants become fixed tokens.
func pac149Mask(body string, subst map[string]string) string {
	keys := make([]string, 0, len(subst))
	for k := range subst {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, k := range keys {
		for _, form := range []string{k, strings.ToLower(k), strings.ToUpper(k)} {
			body = strings.ReplaceAll(body, form, subst[k])
		}
	}
	body = pac149UUIDRe.ReplaceAllString(body, "<ID>")
	return pac149TimeRe.ReplaceAllString(body, "<T>")
}

// pac149Keys is the key set of one JSON object, sorted.
func pac149Keys(t *testing.T, raw []byte) []string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (e *pac112Env) invite(owner, link uuid.UUID, addr string) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.do(http.MethodPost, "/api/scheduling-links/"+link.String()+"/hosts", owner, pac112JSON(e.t, map[string]string{"email": addr}))
}

func (e *pac112Env) read(user, link uuid.UUID) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.do(http.MethodGet, "/api/scheduling-links/"+link.String(), user, "")
}

// rosterEntries is the raw `hosts` items of a link record.
func pac149Roster(t *testing.T, body []byte) []json.RawMessage {
	t.Helper()
	var rec struct {
		Hosts []json.RawMessage `json:"hosts"`
	}
	if err := json.Unmarshal(body, &rec); err != nil {
		t.Fatalf("decode roster: %v; body %s", err, body)
	}
	return rec.Hosts
}

// pac149Entry is the raw roster item whose email is addr.
func pac149Entry(t *testing.T, hosts []json.RawMessage, addr string) json.RawMessage {
	t.Helper()
	for _, h := range hosts {
		var x struct {
			Email string `json:"email"`
		}
		if err := json.Unmarshal(h, &x); err != nil {
			t.Fatal(err)
		}
		if x.Email == addr {
			return h
		}
	}
	t.Fatalf("no roster entry %s in %s", addr, hosts)
	return nil
}

// AC-1: the single invite of H, N, U and C answers the same 201, byte for
// byte modulo the address, with no account identifier; none is the 404 "user
// not found" (API-072 superseded).
func TestPAC149_AC1_InviteAnswersByAddress(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("ac1-owner")
	a := e.pac149Four()
	l := e.link(owner, nil)
	var first string
	for _, addr := range a.all() {
		w := e.invite(owner, l.ID, addr)
		if w.Code != http.StatusCreated || w.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("invite %s: %d %q %s", addr, w.Code, w.Header().Get("Content-Type"), w.Body.String())
		}
		if strings.Contains(w.Body.String(), "user_id") || strings.Contains(w.Body.String(), "user not found") {
			t.Errorf("invite %s carries an account identifier or the old refusal: %s", addr, w.Body.String())
		}
		masked := pac149Mask(w.Body.String(), map[string]string{addr: "<ADDR>"})
		if first == "" {
			first = masked
			if want := `{"email":"<ADDR>","is_owner":false,"status":"pending"}` + "\n"; masked != want {
				t.Errorf("201 body = %q, want %q", masked, want)
			}
		} else if masked != first {
			t.Errorf("invite %s = %q, differs from H's %q", addr, masked, first)
		}
	}
	for _, id := range []uuid.UUID{a.hID, a.uID, a.c1, a.c2} {
		if n := e.hostRowsOf(id); n != 0 {
			t.Errorf("account %s has %d host rows after the invitations", id, n)
		}
	}
	for _, addr := range a.all() {
		if n := pac149InvitationsTo(t, e.db, addr); n != 1 {
			t.Errorf("%d invitations to %s, want 1", n, addr)
		}
	}
}

// AC-2: the list [H, N, U, C] on create and on PATCH of a link with no
// co-hosts: every entry invited, counts 4/0/0; swapping H and N gives a
// byte-identical answer modulo the addresses, the roster in the answer and in
// a following read included, in the request's order. The owner's address in
// another case is own_address, also with a legacy case-variant carrier.
func TestPAC149_AC2_ListOutcomesByAddress(t *testing.T) {
	e := newPAC112Env(t)
	owner, ownerAddr := e.confirmed("ac2-owner")
	a := e.pac149Four()
	order1 := []string{a.H, a.N, a.U, a.C}
	order2 := []string{a.N, a.H, a.U, a.C}
	subst := func(order []string) map[string]string {
		m := map[string]string{}
		for i, addr := range order {
			m[addr] = fmt.Sprintf("<A%d>", i)
		}
		return m
	}
	run := uuid.NewString()[:8]
	type saved struct{ answer, read string }
	save := func(verb string, order []string, n int) saved {
		t.Helper()
		var w *httptest.ResponseRecorder
		slug := fmt.Sprintf("pac149-ac2-%s-%s-%d", verb, run, n)
		if verb == "create" {
			w = e.create(owner, map[string]any{"slug": slug, "co_host_emails": order})
		} else {
			l := e.link(owner, nil)
			w = e.patch(owner, l.ID, map[string]any{"slug": slug, "co_host_emails": order})
		}
		s := pac112Saved(t, w, map[string]int{"create": http.StatusCreated, "PATCH": http.StatusOK}[verb])
		want := make([]string, len(order))
		for i, addr := range order {
			want[i] = addr + "=invited"
		}
		pac112Equal(t, verb, pac112Outcomes(t, s), want)
		pac112Counts(t, s, 4, 0, 0)
		// Roster: the owner, then the request's order.
		hosts := pac149Roster(t, w.Body.Bytes())
		if len(hosts) != 5 {
			t.Fatalf("%s roster: %s", verb, hosts)
		}
		for i, addr := range order {
			var x struct {
				Email string `json:"email"`
			}
			_ = json.Unmarshal(hosts[i+1], &x)
			if x.Email != addr {
				t.Errorf("%s roster[%d] = %s, want %s (request order)", verb, i+1, x.Email, addr)
			}
		}
		r := e.read(owner, s.Id)
		e.mustStatus(r, http.StatusOK, verb+" read")
		m := subst(order)
		m[slug] = "<SLUG>"
		return saved{answer: pac149Mask(w.Body.String(), m), read: pac149Mask(r.Body.String(), m)}
	}
	for _, verb := range []string{"create", "PATCH"} {
		s1, s2 := save(verb, order1, 1), save(verb, order2, 2)
		if s1.answer != s2.answer {
			t.Errorf("%s: swapping H and N changed the answer:\n%s\n%s", verb, s1.answer, s2.answer)
		}
		if s1.read != s2.read {
			t.Errorf("%s: swapping H and N changed the following read:\n%s\n%s", verb, s1.read, s2.read)
		}
	}
	// The owner's own address in another letter case: own_address, also once
	// a legacy case-variant account carries it.
	listed := strings.ToUpper(ownerAddr)
	for _, phase := range []string{"plain", "with a case-variant carrier"} {
		if phase != "plain" {
			createTestUser(t, strings.ToUpper(ownerAddr[:1])+ownerAddr[1:])
		}
		s := pac112Saved(t, e.create(owner, map[string]any{"co_host_emails": []string{listed}}), http.StatusCreated)
		pac112Equal(t, phase, pac112Outcomes(t, s), []string{listed + "=own_address"})
		l := e.link(owner, nil)
		s = pac112Saved(t, e.patch(owner, l.ID, map[string]any{"co_host_emails": []string{listed}}), http.StatusOK)
		pac112Equal(t, phase+" PATCH", pac112Outcomes(t, s), []string{listed + "=own_address"})
	}
}

// AC-3: AC-1's four invitations, read by the owner (single read and list)
// and by an accepted co-host: the four pending entries are byte-identical
// modulo the address, show the address as typed and `pending`, and carry
// exactly email, is_owner and status.
func TestPAC149_AC3_PendingEntriesIdentical(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("ac3-owner")
	q, _ := e.confirmed("ac3-q")
	a := e.pac149Four()
	l := e.link(owner, map[uuid.UUID]string{q: "accepted"})
	for _, addr := range a.all() {
		e.mustStatus(e.invite(owner, l.ID, addr), http.StatusCreated, "invite "+addr)
	}
	check := func(what string, hosts []json.RawMessage) {
		t.Helper()
		var first string
		for _, addr := range a.all() {
			raw := pac149Entry(t, hosts, addr)
			if keys := strings.Join(pac149Keys(t, raw), ","); keys != "email,is_owner,status" {
				t.Errorf("%s: entry %s has keys %s", what, addr, keys)
			}
			masked := pac149Mask(string(raw), map[string]string{addr: "<ADDR>"})
			if masked != `{"email":"<ADDR>","is_owner":false,"status":"pending"}` {
				t.Errorf("%s: entry %s = %s", what, addr, raw)
			}
			if first == "" {
				first = masked
			} else if masked != first {
				t.Errorf("%s: entry %s differs", what, addr)
			}
		}
	}
	w := e.read(owner, l.ID)
	e.mustStatus(w, http.StatusOK, "owner read")
	check("owner single read", pac149Roster(t, w.Body.Bytes()))
	pac149StrictLink(t, w.Body.Bytes())
	w = e.read(q, l.ID)
	e.mustStatus(w, http.StatusOK, "co-host read")
	check("accepted co-host read", pac149Roster(t, w.Body.Bytes()))
	for _, viewer := range []struct {
		id   uuid.UUID
		list string
	}{{owner, "owned"}, {q, "shared"}} {
		w := e.do(http.MethodGet, "/api/scheduling-links/", viewer.id, "")
		e.mustStatus(w, http.StatusOK, "list")
		var lists map[string][]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &lists); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, rec := range lists[viewer.list] {
			if strings.Contains(string(rec), l.ID.String()) {
				found = true
				check(viewer.list+" list", pac149Roster(t, rec))
			}
		}
		if !found {
			t.Errorf("link missing from the %s list", viewer.list)
		}
	}
}

// pac149StrictLink decodes a link record strictly into the generated type
// (AC-15).
func pac149StrictLink(t *testing.T, body []byte) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var out gen.SchedulingLink
	if err := dec.Decode(&out); err != nil {
		t.Errorf("decode SchedulingLink strictly: %v; body %s", err, body)
	}
}

// AC-4: with 5 co-hosts, the single invite of H, N, U and C is the same 422
// and writes nothing; a list adding any one of them to the 5 is the over-5
// 422 alike.
func TestPAC149_AC4_LimitSameForEveryAddress(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("ac4-owner")
	a := e.pac149Four()
	l := e.link(owner, nil)
	five := make([]string, 5)
	for i := range five {
		five[i] = pac112Addr(fmt.Sprintf("ac4-co-%d", i))
		e.mustStatus(e.invite(owner, l.ID, five[i]), http.StatusCreated, "fill")
	}
	before := e.hostRows(l.ID)
	for _, addr := range a.all() {
		w := e.invite(owner, l.ID, addr)
		pac112Body(t, w, http.StatusUnprocessableEntity, `{"error":"this link has reached its limit of 5 co-hosts; remove a co-host before inviting another"}`, "invite "+addr)
		pac112Body(t, e.patch(owner, l.ID, map[string]any{"co_host_emails": append(append([]string{}, five...), addr)}),
			http.StatusUnprocessableEntity, pac112CapMsg, "list adding "+addr)
	}
	pac112SameHosts(t, "after the refusals", e.hostRows(l.ID), before)
}

// AC-5: pending invitations for H and N removed by a list keeping neither:
// both removal items byte-identical modulo the address, keys email and status
// only.
func TestPAC149_AC5_RemovalItemsByAddress(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("ac5-owner")
	a := e.pac149Four()
	l := e.link(owner, nil)
	e.mustStatus(e.invite(owner, l.ID, a.H), http.StatusCreated, "invite H")
	e.mustStatus(e.invite(owner, l.ID, a.N), http.StatusCreated, "invite N")
	w := e.patch(owner, l.ID, map[string]any{"co_host_emails": []string{}})
	s := pac112Saved(t, w, http.StatusOK)
	pac112Counts(t, s, 0, 2, 0)
	var report struct {
		Changes struct {
			Removed []json.RawMessage `json:"removed"`
		} `json:"co_host_changes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Changes.Removed) != 2 {
		t.Fatalf("removed = %s", report.Changes.Removed)
	}
	masked := map[string]bool{}
	for _, raw := range report.Changes.Removed {
		if keys := strings.Join(pac149Keys(t, raw), ","); keys != "email,status" {
			t.Errorf("removed item %s has keys %s", raw, keys)
		}
		masked[pac149Mask(string(raw), map[string]string{a.H: "<ADDR>", a.N: "<ADDR>"})] = true
	}
	if len(masked) != 1 || !masked[`{"email":"<ADDR>","status":"pending"}`] {
		t.Errorf("removed items differ modulo the address: %v", masked)
	}
	if n := pac149InvitationsTo(t, e.db, a.H) + pac149InvitationsTo(t, e.db, a.N); n != 0 {
		t.Errorf("%d invitations left", n)
	}

	// Contract md §2.5: a removed declined entry is by address and status
	// only; a removed accepted entry carries its account id.
	acc, accAddr := e.confirmed("ac5-acc")
	dec, decAddr := e.confirmed("ac5-dec")
	l2 := e.link(owner, map[uuid.UUID]string{acc: "accepted", dec: "declined"})
	w = e.patch(owner, l2.ID, map[string]any{"co_host_emails": []string{}})
	pac112Counts(t, pac112Saved(t, w, http.StatusOK), 0, 2, 0)
	report.Changes.Removed = nil
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	for _, raw := range report.Changes.Removed {
		var item struct {
			Email string `json:"email"`
		}
		_ = json.Unmarshal(raw, &item)
		keys := strings.Join(pac149Keys(t, raw), ",")
		switch item.Email {
		case decAddr:
			if keys != "email,status" {
				t.Errorf("removed declined entry %s has keys %s", raw, keys)
			}
		case accAddr:
			if keys != "email,status,user_id" || !strings.Contains(string(raw), acc.String()) {
				t.Errorf("removed accepted entry %s", raw)
			}
		default:
			t.Errorf("unexpected removed item %s", raw)
		}
	}
}

// --- AC-6: the same work for H and N ---------------------------------------

// pac149Stmt is one recorded statement, its arguments as their driver text.
type pac149Stmt struct {
	sql  string
	args []string
}

// pac149ArgText is an argument's text as the driver sends it (N-8): a
// driver.Valuer's value (pq.Array's encoded array included), raw bytes as
// such, anything else as fmt prints it, plus the elements of a slice or array
// argument one by one.
func pac149ArgText(a any) []string {
	out := []string{fmt.Sprintf("%v", a)}
	if v, ok := a.(interface{ Value() (any, error) }); ok {
		if dv, err := v.Value(); err == nil {
			switch x := dv.(type) {
			case string:
				out = append(out, x)
			case []byte:
				out = append(out, string(x))
			default:
				out = append(out, fmt.Sprint(x))
			}
		}
	}
	switch x := a.(type) {
	case []byte:
		out = append(out, string(x))
	case []string:
		out = append(out, x...)
	case *[]string:
		if x != nil {
			out = append(out, (*x)...)
		}
	}
	return out
}

// pac149Recorder records every statement of the requests run while it is
// installed (storage.InstallCoHostStatement) and every carriers read
// (storage.InstallCarriersRead).
type pac149Recorder struct {
	stmts    []pac149Stmt
	carriers []string
}

func pac149Record(t *testing.T) *pac149Recorder {
	t.Helper()
	r := &pac149Recorder{}
	storage.InstallCoHostStatement(t, func(sql string, args []any) {
		s := pac149Stmt{sql: sql}
		for _, a := range args {
			s.args = append(s.args, strings.Join(pac149ArgText(a), "\x00"))
		}
		r.stmts = append(r.stmts, s)
	})
	storage.InstallCarriersRead(t, func(addr string) { r.carriers = append(r.carriers, addr) })
	return r
}

func (r *pac149Recorder) take() ([]pac149Stmt, []string) {
	s, c := r.stmts, r.carriers
	r.stmts, r.carriers = nil, nil
	return s, c
}

var (
	pac149UsersRe     = regexp.MustCompile(`(?i)\busers\b`)
	pac149UsersFromRe = regexp.MustCompile(`(?i)\b(?:FROM|JOIN)\s+users\b(?:\s+(?:AS\s+)?([a-z_][a-z0-9_]*))?`)
	// An email column compared with anything (N-7): `x.email =`, `lower(x.email) =`,
	// `= x.email`, `= lower(x.email)`, LIKE, IN and the regex operators.
	pac149EmailCmpRe  = regexp.MustCompile(`(?i)((?:\b[a-z_][a-z0-9_]*\.)?\bemail\b\s*\)?\s*(=|<>|!=|\bLIKE\b|\bILIKE\b|\bIN\b|\bSIMILAR\b|~))|((=|<>|!=|\bLIKE\b|\bILIKE\b|~)\s*(lower\s*\(\s*)?(?:\b[a-z_][a-z0-9_]*\.)?\bemail\b)`)
	pac149LinkRowsRe  = regexp.MustCompile(`(?i)\b(?:FROM|JOIN)\s+scheduling_link(?:_host)?s\b(?:\s+(?:AS\s+)?([a-z_][a-z0-9_]*))?`)
	pac149SQLKeywords = map[string]bool{"where": true, "on": true, "left": true, "right": true, "inner": true, "join": true, "cross": true, "full": true, "order": true, "group": true, "limit": true, "for": true, "using": true, "natural": true}
)

// pac149UsersByIDOnly is N-7: a statement that references users restricts
// every users reference only by users.id, equal to a bind argument or to the
// user_id / owner_user_id of a row of the link, and never compares an email
// column with anything. It returns why not, or "".
func pac149UsersByIDOnly(sql string) string {
	if !pac149UsersRe.MatchString(sql) {
		return ""
	}
	if m := pac149EmailCmpRe.FindString(sql); m != "" {
		return "compares an email column: " + m
	}
	refs := pac149UsersFromRe.FindAllStringSubmatch(sql, -1)
	if len(refs) == 0 {
		return "references users other than in FROM or JOIN"
	}
	// The link's rows: scheduling_links and scheduling_link_hosts, by alias
	// (or by table name when unaliased).
	var rowQual []string
	for _, m := range pac149LinkRowsRe.FindAllStringSubmatch(sql, -1) {
		alias := strings.ToLower(m[1])
		if alias == "" || pac149SQLKeywords[alias] {
			alias = strings.ToLower(strings.Fields(m[0])[1])
		}
		rowQual = append(rowQual, regexp.QuoteMeta(alias))
	}
	rhs := `(?:\$\d+)`
	if len(rowQual) > 0 {
		rhs = `(?:\$\d+|\b(?:` + strings.Join(rowQual, "|") + `)\.(?:user_id|owner_user_id)\b)`
	}
	for _, ref := range refs {
		alias := strings.ToLower(ref[1])
		qual := `(?:\b` + regexp.QuoteMeta(alias) + `\.)`
		if alias == "" || pac149SQLKeywords[alias] {
			qual = `(?:\busers\.)?`
		}
		id := qual + `\bid\b`
		re := regexp.MustCompile(`(?i)(` + id + `\s*=\s*` + rhs + `)|(` + rhs + `\s*=\s*` + id + `)`)
		if !re.MatchString(sql) {
			return fmt.Sprintf("users reference %q is not restricted by its id", ref[0])
		}
	}
	return ""
}

// TestPAC149_N7_UsersByIDOnlyRule pins the N-7 predicate itself, so a
// statement the inviter paths must never run is caught.
func TestPAC149_N7_UsersByIDOnlyRule(t *testing.T) {
	for sql, ok := range map[string]bool{
		`SELECT session_generation FROM users WHERE id = $1 FOR KEY SHARE`:                                                      true,
		`SELECT email, lower(email), name, avatar_url FROM users WHERE id = $1`:                                                 true,
		`SELECT h.id FROM scheduling_link_hosts h LEFT JOIN users u ON u.id = h.user_id WHERE h.link_id = $1`:                   true,
		`SELECT 1 FROM scheduling_links l JOIN users o ON o.id = l.owner_user_id WHERE l.id = $1`:                               true,
		`SELECT lower(v) FROM unnest($1::text[]) WITH ORDINALITY AS t(v, n) ORDER BY n`:                                         true,
		`SELECT h.id FROM scheduling_link_hosts h JOIN users u ON lower(u.email) = lower(h.invitee_email) WHERE h.link_id = $1`: false,
		`SELECT id FROM users WHERE lower(email) = lower($1)`:                                                                   false,
		`SELECT u.id FROM users u WHERE u.email = $1`:                                                                           false,
		`SELECT u.id FROM users u WHERE u.email ILIKE $1`:                                                                       false,
		`SELECT u.id FROM users u WHERE $1 = lower(u.email)`:                                                                    false,
		`SELECT u.id FROM users u WHERE u.email IN ($1)`:                                                                        false,
		`SELECT count(*) FROM users`:                                                                                            false,
		`SELECT u.id FROM users u JOIN user_identities i ON i.user_id = u.id WHERE i.subject = $1`:                              false,
	} {
		if got := pac149UsersByIDOnly(sql) == ""; got != ok {
			t.Errorf("%s: by-id-only = %v, want %v (%s)", sql, got, ok, pac149UsersByIDOnly(sql))
		}
	}
}

// pac149Tokenise is a recorded statement with the typed address and what
// derives from it (its lower() and upper case, a 32-byte digest), the
// request-chosen slug and key, server-assigned ids and instants replaced by
// tokens (contract md §6, revision 3).
func pac149Tokenise(s pac149Stmt, addr string, extra ...string) string {
	var b strings.Builder
	b.WriteString(s.sql)
	for _, arg := range s.args {
		b.WriteString(" | ")
		parts := strings.Split(arg, "\x00")
		if len(parts) > 1 && strings.HasPrefix(parts[0], "[") && len(strings.Fields(strings.Trim(parts[0], "[]"))) == 32 {
			b.WriteString("<DIGEST>")
			continue
		}
		text := arg
		for _, x := range extra {
			text = strings.ReplaceAll(text, x, "<REQ>")
		}
		text = strings.NewReplacer(addr, "<ADDR>", strings.ToLower(addr), "<ADDR>", strings.ToUpper(addr), "<ADDR>").Replace(text)
		text = pac149UUIDRe.ReplaceAllString(text, "<ID>")
		b.WriteString(pac149TimeRe.ReplaceAllString(text, "<T>"))
	}
	return b.String()
}

// pac149CheckWork is AC-6's comparison of one path for H and N.
func pac149CheckWork(t *testing.T, what string, h, n []pac149Stmt, hc, nc []string, hAddr, nAddr string, hExtra, nExtra []string) {
	t.Helper()
	if len(hc) != 0 || len(nc) != 0 {
		t.Errorf("%s: carriers read on the inviter side: H %v, N %v", what, hc, nc)
	}
	if len(h) == 0 {
		t.Fatalf("%s: no statement recorded", what)
	}
	for who, seq := range map[string][]pac149Stmt{"H": h, "N": n} {
		addr := map[string]string{"H": hAddr, "N": nAddr}[who]
		for _, s := range seq {
			if why := pac149UsersByIDOnly(s.sql); why != "" {
				t.Errorf("%s %s: %s\n  %s", what, who, why, s.sql)
			}
			if pac149UsersRe.MatchString(s.sql) {
				for _, arg := range s.args {
					if strings.Contains(arg, addr) || strings.Contains(arg, strings.ToLower(addr)) {
						t.Errorf("%s %s: a statement reading users carries the typed address: %s %q", what, who, s.sql, arg)
					}
				}
			}
		}
	}
	if len(h) != len(n) {
		t.Errorf("%s: %d statements for H, %d for N", what, len(h), len(n))
	}
	for i := 0; i < len(h) && i < len(n); i++ {
		if a, b := pac149Tokenise(h[i], hAddr, hExtra...), pac149Tokenise(n[i], nAddr, nExtra...); a != b {
			t.Errorf("%s: statement %d differs:\n  H %s\n  N %s", what, i, a, b)
		}
	}
}

// AC-6 [unit]: for the single invite of H and of N on fresh links, and the
// list saves [H] and [N] on create (keyless and keyed) and on PATCH: no
// carriers read on any inviter path, and the recorded statement sequences of
// H and N are equal, SQL for SQL, with the arguments equal after the
// tokenising of contract md §6; no statement that reads users is restricted
// by anything but the account id (N-7), or carries the typed address (B-5).
func TestPAC149_AC6_SameWork(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("ac6-owner")
	a := e.pac149Four()
	rec := pac149Record(t)
	run := uuid.NewString()[:8]

	single := func(addr string) ([]pac149Stmt, []string) {
		l := e.link(owner, nil)
		rec.take()
		e.mustStatus(e.invite(owner, l.ID, addr), http.StatusCreated, "invite "+addr)
		return rec.take()
	}
	hs, hc := single(a.H)
	ns, nc := single(a.N)
	pac149CheckWork(t, "single invite", hs, ns, hc, nc, a.H, a.N, nil, nil)

	// Under a session guard the write's first statement is the PAC-71
	// re-check, recorded too.
	guarded := func(addr string) ([]pac149Stmt, []string) {
		l := e.link(owner, nil)
		gen, ok, err := storage.CurrentGeneration(e.db, owner)
		if err != nil || !ok {
			t.Fatalf("generation: %v %v", ok, err)
		}
		rec.take()
		req := withUser(httptest.NewRequest(http.MethodPost, "/api/scheduling-links/"+l.ID.String()+"/hosts",
			strings.NewReader(pac112JSON(t, map[string]string{"email": addr}))), owner)
		req = req.WithContext(storage.WithSessionGuard(req.Context(), &storage.SessionGuard{UserID: owner, Gen: gen}))
		w := httptest.NewRecorder()
		e.r.ServeHTTP(w, req)
		e.mustStatus(w, http.StatusCreated, "guarded invite "+addr)
		return rec.take()
	}
	hs, hc = guarded(a.H)
	ns, nc = guarded(a.N)
	pac149CheckWork(t, "guarded single invite", hs, ns, hc, nc, a.H, a.N, nil, nil)
	sawRecheck := false
	for _, s := range hs {
		sawRecheck = sawRecheck || strings.Contains(s.sql, "SELECT session_generation FROM users WHERE id = $1")
	}
	if !sawRecheck {
		t.Error("the guarded invite's re-check was not recorded")
	}

	create := func(addr, slug, key string) ([]pac149Stmt, []string) {
		rec.take()
		body := pac112JSON(t, map[string]any{"title": "AC-6", "slug": slug, "co_host_emails": []string{addr}})
		req := withUser(httptest.NewRequest(http.MethodPost, "/api/scheduling-links/", strings.NewReader(body)), owner)
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		e.r.ServeHTTP(w, req)
		e.mustStatus(w, http.StatusCreated, "create "+addr)
		return rec.take()
	}
	hSlug, nSlug := "pac149-ac6-h-"+run, "pac149-ac6-n-"+run
	hs, hc = create(a.H, hSlug, "")
	ns, nc = create(a.N, nSlug, "")
	pac149CheckWork(t, "create [x]", hs, ns, hc, nc, a.H, a.N, []string{hSlug}, []string{nSlug})

	hKey, nKey := "pac149-ac6-key-h-"+run, "pac149-ac6-key-n-"+run
	hSlug, nSlug = "pac149-ac6-kh-"+run, "pac149-ac6-kn-"+run
	hs, hc = create(a.H, hSlug, hKey)
	ns, nc = create(a.N, nSlug, nKey)
	pac149CheckWork(t, "keyed create [x]", hs, ns, hc, nc, a.H, a.N, []string{hSlug, hKey}, []string{nSlug, nKey})

	patch := func(addr string) ([]pac149Stmt, []string, string) {
		l := e.link(owner, nil)
		rec.take()
		s := pac112Saved(t, e.patch(owner, l.ID, map[string]any{"title": "AC-6 patch", "co_host_emails": []string{addr}}), http.StatusOK)
		pac112Counts(t, s, 1, 0, 0)
		st, c := rec.take()
		return st, c, l.Slug
	}
	hs, hc, hSlug = patch(a.H)
	ns, nc, nSlug = patch(a.N)
	pac149CheckWork(t, "PATCH [x]", hs, ns, hc, nc, a.H, a.N, []string{hSlug}, []string{nSlug})
}

// --- Invitee side ------------------------------------------------------------

// invites is the caller's invitation list under one spelling.
func (e *pac112Env) invites(user uuid.UUID, path string) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.do(http.MethodGet, "/api/scheduling-links/"+path, user, "")
}

func (e *pac112Env) lists(user, link uuid.UUID) bool {
	e.t.Helper()
	w := e.invites(user, "host-invites")
	e.mustStatus(w, http.StatusOK, "host-invites")
	return strings.Contains(w.Body.String(), link.String())
}

// answer accepts or declines under the canonical spelling.
func (e *pac112Env) answer(user, link uuid.UUID, verb string) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.do(http.MethodPost, "/api/scheduling-links/host-invites/"+link.String()+"/"+verb, user, "")
}

func (e *pac112Env) leave(user, link uuid.UUID) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.do(http.MethodPost, "/api/scheduling-links/"+link.String()+"/leave", user, "")
}

// AC-7: the holder lists the invitation; accepting answers as today and makes
// H an accepted host with id, name and picture; the link's public slots then
// exclude H's busy time. Declining instead shows H declined, by address, with
// no name, picture or id.
func TestPAC149_AC7_HolderAcceptsAndDeclines(t *testing.T) {
	b := newPAC72Bench(t, pac72At(pac72Mon, "06:00"))
	slh := newSchedulingLinkHandlers(b.db, nil)
	b.r.Post("/api/scheduling-links/{id}/hosts", slh.inviteHost)
	b.r.Get("/api/scheduling-links/host-invites", slh.listHostInvites)
	b.r.Post("/api/scheduling-links/host-invites/{id}/accept", slh.acceptInvite)
	b.r.Post("/api/scheduling-links/host-invites/{id}/decline", slh.declineInvite)
	b.r.Get("/api/scheduling-links/{id}", slh.getLink)
	do := func(method, path string, user uuid.UUID, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		b.r.ServeHTTP(w, withUser(httptest.NewRequest(method, path, strings.NewReader(body)), user))
		return w
	}
	owner, _ := b.host(pac112Addr("ac7-owner"))
	hAddr := "hold-" + uuid.NewString()[:8] + "@ext.example"
	h, hBearer := b.host(hAddr)
	confirmAddress(t, b.db, h)
	if _, err := b.db.Exec(`UPDATE users SET name = 'Holder Person', avatar_url = 'https://pictures.example/h.png' WHERE id = $1`, h); err != nil {
		t.Fatal(err)
	}
	link := b.link("pac149-ac7-"+uuid.NewString()[:8], owner, nil, owner)
	b.cal.addEvent(hBearer, pac72At(pac72Tue, "10:00"), pac72At(pac72Tue, "10:30"))
	busy := pac72At(pac72Tue, "10:00")

	if w := do(http.MethodPost, "/api/scheduling-links/"+link.ID.String()+"/hosts", owner, `{"email":"`+hAddr+`"}`); w.Code != http.StatusCreated {
		t.Fatalf("invite: %d %s", w.Code, w.Body.String())
	}
	// A second invitation after H's, so the roster order can show whether
	// H's entry keeps its place when H answers (spec D-16).
	later := "later-" + uuid.NewString()[:8] + "@ext.example"
	if w := do(http.MethodPost, "/api/scheduling-links/"+link.ID.String()+"/hosts", owner, `{"email":"`+later+`"}`); w.Code != http.StatusCreated {
		t.Fatalf("invite later: %d %s", w.Code, w.Body.String())
	}
	rosterOrder := func() []string {
		t.Helper()
		w := do(http.MethodGet, "/api/scheduling-links/"+link.ID.String(), owner, "")
		var out []string
		for _, raw := range pac149Roster(t, w.Body.Bytes()) {
			var x struct {
				Email string `json:"email"`
			}
			_ = json.Unmarshal(raw, &x)
			out = append(out, x.Email)
		}
		return out
	}
	orderBefore := rosterOrder()
	if got := b.slots(link.Slug, "2030-01-08", 30); got.code != http.StatusOK || !got.offers(busy) {
		t.Fatalf("pending: listing = %d %v, want 10:00 offered (a pending invitation is not a host)", got.code, got.starts())
	}
	if w := do(http.MethodGet, "/api/scheduling-links/host-invites", h, ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), link.ID.String()) {
		t.Fatalf("H's invitations: %d %s", w.Code, w.Body.String())
	}
	if w := do(http.MethodPost, "/api/scheduling-links/host-invites/"+link.ID.String()+"/accept", h, ""); w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"status":"accepted"}` {
		t.Fatalf("accept: %d %s", w.Code, w.Body.String())
	}
	if after := rosterOrder(); strings.Join(after, ",") != strings.Join(orderBefore, ",") {
		t.Errorf("roster order changed when H accepted: %v, was %v (an entry keeps its position, D-16)", after, orderBefore)
	}
	w := do(http.MethodGet, "/api/scheduling-links/"+link.ID.String(), owner, "")
	entry := pac149Entry(t, pac149Roster(t, w.Body.Bytes()), hAddr)
	want := fmt.Sprintf(`{"user_id":%q,"email":%q,"name":"Holder Person","avatar_url":"https://pictures.example/h.png","is_owner":false,"status":"accepted"}`, h.String(), hAddr)
	if string(entry) != want {
		t.Errorf("accepted entry = %s, want %s", entry, want)
	}
	if got := b.slots(link.Slug, "2030-01-08", 30); got.code != http.StatusOK || got.offers(busy) || !got.offers(pac72At(pac72Tue, "09:00")) {
		t.Errorf("accepted: listing = %d %v, want 09:00 offered and 10:00 not", got.code, got.starts())
	}
	if w := do(http.MethodGet, "/api/scheduling-links/host-invites", h, ""); strings.Contains(w.Body.String(), link.ID.String()) {
		t.Errorf("an answered invitation is still listed: %s", w.Body.String())
	}

	// Decline instead, on a second link.
	link2 := b.link("pac149-ac7d-"+uuid.NewString()[:8], owner, nil, owner)
	if w := do(http.MethodPost, "/api/scheduling-links/"+link2.ID.String()+"/hosts", owner, `{"email":"`+hAddr+`"}`); w.Code != http.StatusCreated {
		t.Fatalf("invite 2: %d %s", w.Code, w.Body.String())
	}
	if w := do(http.MethodPost, "/api/scheduling-links/host-invites/"+link2.ID.String()+"/decline", h, ""); w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"status":"declined"}` {
		t.Fatalf("decline: %d %s", w.Code, w.Body.String())
	}
	w = do(http.MethodGet, "/api/scheduling-links/"+link2.ID.String(), owner, "")
	if entry := pac149Entry(t, pac149Roster(t, w.Body.Bytes()), hAddr); string(entry) != fmt.Sprintf(`{"email":%q,"is_owner":false,"status":"declined"}`, hAddr) {
		t.Errorf("declined entry = %s", entry)
	}
	var uid uuid.NullUUID
	if err := b.db.QueryRow(`SELECT user_id FROM scheduling_link_hosts WHERE link_id = $1 AND status = 'declined'`, link2.ID).Scan(&uid); err != nil || uid.UUID != h {
		t.Errorf("the declined entry is not bound to H: %v %v", uid, err)
	}
}

// AC-8: an account created for N after the invitation lists it and accepts
// when domain-confirmed; with mailbox evidence only (unconfirmed) it lists
// nothing and its accept is the 404. Neither account of C's case pair lists
// or accepts; U's unconfirmed carrier lists nothing.
func TestPAC149_AC8_LaterAccountAndNonHolders(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("ac8-owner")
	a := e.pac149Four()
	n2 := "late-" + uuid.NewString()[:8] + "@ext.example"
	l1, l2, l3, l4 := e.link(owner, nil), e.link(owner, nil), e.link(owner, nil), e.link(owner, nil)
	e.mustStatus(e.invite(owner, l1.ID, a.N), http.StatusCreated, "invite N")
	e.mustStatus(e.invite(owner, l2.ID, n2), http.StatusCreated, "invite N2")
	e.mustStatus(e.invite(owner, l3.ID, a.C), http.StatusCreated, "invite C")
	e.mustStatus(e.invite(owner, l4.ID, a.U), http.StatusCreated, "invite U")

	confirmed := createConfirmedUser(t, a.N)
	if !e.lists(confirmed, l1.ID) {
		t.Error("the later domain-confirmed account does not list its invitation")
	}
	pac112Body(t, e.answer(confirmed, l1.ID, "accept"), http.StatusOK, `{"status":"accepted"}`, "later account accepts")
	if got := e.hostRows(l1.ID); got[confirmed] != "accepted" {
		t.Errorf("after accept: %v", got)
	}

	mailbox := createTestUser(t, n2)
	if e.lists(mailbox, l2.ID) {
		t.Error("an unconfirmed later account lists the invitation")
	}
	pac112Body(t, e.answer(mailbox, l2.ID, "accept"), http.StatusNotFound, `{"error":"not found"}`, "unconfirmed accept")

	for _, c := range []uuid.UUID{a.c1, a.c2} {
		if e.lists(c, l3.ID) {
			t.Errorf("case-pair account %s lists the invitation", c)
		}
		pac112Body(t, e.answer(c, l3.ID, "accept"), http.StatusNotFound, `{"error":"not found"}`, "case-pair accept")
	}
	if e.lists(a.uID, l4.ID) {
		t.Error("U's unconfirmed carrier lists the invitation")
	}
	for _, l := range []uuid.UUID{l2.ID, l3.ID, l4.ID} {
		if rows := e.hostRows(l); len(rows) != 2 {
			t.Errorf("link %s: %v, want the owner and the invitation unchanged", l, rows)
		}
	}
}

// AC-13: a malformed address: the single invite's 400, nothing written; in a
// list not_added, nothing written. Blank entries keep their refusals; U+0085
// alone keeps not_added.
func TestPAC149_AC13_MalformedAndBlank(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("ac13-owner")
	l := e.link(owner, nil)
	for _, bad := range []string{"not-an-address", "Pat <pat@example.com>", "a@b c"} {
		pac112Body(t, e.invite(owner, l.ID, bad), http.StatusBadRequest, `{"error":"email must be a valid email address"}`, "invite "+bad)
		s := pac112Saved(t, e.patch(owner, l.ID, map[string]any{"co_host_emails": []string{bad}}), http.StatusOK)
		pac112Equal(t, "list "+bad, pac112Outcomes(t, s), []string{bad + "=not_added"})
		pac112Counts(t, s, 0, 0, 1)
	}
	for _, blank := range []string{"", "   ", "\ufeff"} {
		pac112Body(t, e.invite(owner, l.ID, blank), http.StatusBadRequest, `{"error":"email is required"}`, fmt.Sprintf("invite %q", blank))
	}
	pac112Body(t, e.patch(owner, l.ID, map[string]any{"co_host_emails": []string{""}}), http.StatusUnprocessableEntity, pac112BlankMsg, "list blank")
	s := pac112Saved(t, e.patch(owner, l.ID, map[string]any{"co_host_emails": []string{"\u0085"}}), http.StatusOK)
	pac112Equal(t, "U+0085", pac112Outcomes(t, s), []string{"\u0085=not_added"})
	pac112SameHosts(t, "nothing written", e.hostRows(l.ID), map[uuid.UUID]string{owner: "accepted"})
	// ECMA trim on the single invite (spec D-15): U+3000 and U+FEFF around a
	// valid address are trimmed, letter case kept.
	addr := "Trim-" + uuid.NewString()[:8] + "@Ext.example"
	pac112Body(t, e.invite(owner, l.ID, "\u3000"+addr+"\ufeff"), http.StatusCreated, `{"email":"`+addr+`","is_owner":false,"status":"pending"}`, "trimmed invite")
}

// AC-14: a failed invitee-side match is the 500 for the list (both
// spellings), accept and decline (both spellings) and leave; never [] or the
// 404, and nothing changes.
func TestPAC149_AC14_InviteeMatchFailureIs500(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("ac14-owner")
	a := e.pac149Four()
	l := e.link(owner, nil)
	e.mustStatus(e.invite(owner, l.ID, a.H), http.StatusCreated, "invite H")
	before := e.hostRows(l.ID)
	storage.InstallInviteeMatch(t, func(id uuid.UUID) error {
		if id == a.hID {
			return errors.New("pac149: invitee match failed")
		}
		return nil
	})
	for what, w := range map[string]*httptest.ResponseRecorder{
		"list":          e.invites(a.hID, "host-invites"),
		"list alias":    e.invites(a.hID, "invites"),
		"accept":        e.answer(a.hID, l.ID, "accept"),
		"accept alias":  e.do(http.MethodPost, "/api/scheduling-links/"+l.ID.String()+"/accept", a.hID, ""),
		"decline":       e.answer(a.hID, l.ID, "decline"),
		"decline alias": e.do(http.MethodPost, "/api/scheduling-links/"+l.ID.String()+"/decline", a.hID, ""),
		"leave":         e.leave(a.hID, l.ID),
	} {
		pac112Body(t, w, http.StatusInternalServerError, `{"error":"internal error"}`, what)
	}
	pac112SameHosts(t, "after the failures", e.hostRows(l.ID), before)
}

// AC-15: the bundle states the rule, and AC-2/AC-3/AC-5's answers decode
// strictly into the generated types (AC-2 and AC-5 through pac112Saved, AC-3
// through pac149StrictLink).
func TestPAC149_AC15_BundleStatesTheRule(t *testing.T) {
	b := pac148Bundle(t)
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	op := func(path, method string) string {
		t.Helper()
		i := strings.Index(b, "\n  "+path+":\n")
		if i < 0 {
			t.Fatalf("no path %s", path)
		}
		rest := b[i+1:]
		if j := strings.Index(rest[1:], "\n  /"); j >= 0 {
			rest = rest[:j+1]
		}
		k := strings.Index(rest, "\n    "+method+":\n")
		if k < 0 {
			t.Fatalf("no %s %s", method, path)
		}
		return flat(rest[k:])
	}
	invite := op("/api/scheduling-links/{id}/hosts", "post")
	for _, want := range []string{
		"The server never reads accounts by address here",
		"PAC-149 removed the second body, `{\"error\":\"user not found\"}`",
		"given for every new well-formed address alike, whoever carries it",
		"\"email must be a valid email address\"",
	} {
		if !strings.Contains(invite, flat(want)) {
			t.Errorf("inviteSchedulingLinkHost does not state %q", want)
		}
	}
	if strings.Contains(invite, "user_id: ") {
		t.Error("the invite's answer still names a user_id")
	}
	for schema, want := range map[string]string{
		"SchedulingLinkCoHostOutcome": "not_added",
		"SchedulingLinkHost":          "pending",
		"SchedulingLinkRemovedHost":   "accepted",
	} {
		if !strings.Contains(pac148Schema(t, schema), want) {
			t.Errorf("%s does not mention %q", schema, want)
		}
	}
	accept := op("/api/scheduling-links/host-invites/{id}/accept", "post")
	if !strings.Contains(accept, "single domain-confirmed holder") {
		t.Error("acceptSchedulingLinkHostInvite does not require the single domain-confirmed holder")
	}
	if !strings.Contains(flat(pac148Schema(t, "SchedulingLinkCoHostChanges")), "PAC-149: distinct new pending invitations this save recorded") {
		t.Error("invited_count is not stated as distinct new invitations")
	}
}

// AC-16: accept, decline and the invitation list answer identically under
// both spellings for AC-7's, AC-8's and AC-18's cases; the older spellings
// are deprecated in the bundle.
func TestPAC149_AC16_BothSpellingsIdentical(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("ac16-owner")
	a := e.pac149Four()
	type spelling struct{ list, accept, decline string }
	canon := spelling{"host-invites", "host-invites/%s/accept", "host-invites/%s/decline"}
	alias := spelling{"invites", "%s/accept", "%s/decline"}
	run := func(sp spelling) []string {
		var out []string
		l1, l2, l3 := e.link(owner, nil), e.link(owner, nil), e.link(owner, nil)
		e.mustStatus(e.invite(owner, l1.ID, a.H), http.StatusCreated, "invite")
		e.mustStatus(e.invite(owner, l2.ID, a.H), http.StatusCreated, "invite")
		e.mustStatus(e.invite(owner, l3.ID, a.C), http.StatusCreated, "invite")
		subst := map[string]string{l1.ID.String(): "<L1>", l2.ID.String(): "<L2>", l3.ID.String(): "<L3>"}
		rec := func(w *httptest.ResponseRecorder) {
			body := w.Body.String()
			for k, v := range subst {
				body = strings.ReplaceAll(body, k, v)
			}
			var mine []string
			// The holder's list holds earlier runs' links too: keep this run's.
			if strings.HasPrefix(body, "[") {
				var items []map[string]any
				_ = json.Unmarshal([]byte(body), &items)
				for _, it := range items {
					if id, _ := it["link_id"].(string); strings.HasPrefix(id, "<L") {
						it["invited_at"] = "<T>"
						raw, _ := json.Marshal(it)
						mine = append(mine, string(raw))
					}
				}
				sort.Strings(mine)
				body = strings.Join(mine, ",")
			}
			out = append(out, fmt.Sprintf("%d %s", w.Code, body))
		}
		rec(e.invites(a.hID, sp.list))                                                                 // AC-7: listed
		rec(e.do(http.MethodPost, "/api/scheduling-links/"+fmt.Sprintf(sp.accept, l1.ID), a.hID, ""))  // AC-7: accept
		rec(e.do(http.MethodPost, "/api/scheduling-links/"+fmt.Sprintf(sp.decline, l2.ID), a.hID, "")) // AC-7: decline
		rec(e.invites(a.c1, sp.list))                                                                  // AC-8: case pair lists nothing
		rec(e.do(http.MethodPost, "/api/scheduling-links/"+fmt.Sprintf(sp.accept, l3.ID), a.c1, ""))   // AC-8: 404
		rec(e.do(http.MethodPost, "/api/scheduling-links/"+fmt.Sprintf(sp.decline, l3.ID), a.c2, ""))  // AC-8: 404
		rec(e.leave(a.hID, l2.ID))                                                                     // AC-18: leave a declined entry
		rec(e.do(http.MethodPost, "/api/scheduling-links/"+fmt.Sprintf(sp.accept, l2.ID), a.hID, ""))  // AC-18: after leaving, 404
		return out
	}
	c, al := run(canon), run(alias)
	if strings.Join(c, "\n") != strings.Join(al, "\n") {
		t.Errorf("the spellings answer differently:\ncanonical %q\nalias     %q", c, al)
	}
	b := pac148Bundle(t)
	for _, id := range []string{"listSchedulingLinkInvitesAlias", "acceptSchedulingLinkInviteByLink", "declineSchedulingLinkInviteByLink"} {
		i := strings.Index(b, "operationId: "+id+"\n")
		if i < 0 {
			t.Fatalf("no operation %s", id)
		}
		block := b[i:]
		if j := strings.Index(block, "\n    post:"); j > 0 {
			block = block[:j]
		}
		if j := strings.Index(block, "\n  /"); j > 0 {
			block = block[:j]
		}
		if !strings.Contains(block, "deprecated: true") {
			t.Errorf("%s is not deprecated", id)
		}
	}
	for _, id := range []string{"listSchedulingLinkHostInvites", "acceptSchedulingLinkHostInvite", "declineSchedulingLinkHostInvite"} {
		i := strings.Index(b, "operationId: "+id+"\n")
		block := b[i:]
		if j := strings.Index(block[1:], "operationId: "); j > 0 {
			block = block[:j]
		}
		if strings.Contains(block, "deprecated: true") {
			t.Errorf("%s (the web's spelling) is deprecated", id)
		}
	}
}

// AC-17: deleting a link erases its pending invitations in the delete itself
// and keeps the accepted host, for a link with no bookings and one whose
// bookers were already erased; H lists L before and not after, and its
// accept, decline and leave are the 404.
func TestPAC149_AC17_DeleteErasesInvitations(t *testing.T) {
	for _, withBookings := range []bool{false, true} {
		t.Run(fmt.Sprintf("erased bookings=%v", withBookings), func(t *testing.T) {
			e := newPAC112Env(t)
			owner, _ := e.confirmed("ac17-owner")
			q, _ := e.confirmed("ac17-q")
			a := e.pac149Four()
			l := e.link(owner, map[uuid.UUID]string{q: "accepted"})
			e.mustStatus(e.invite(owner, l.ID, a.H), http.StatusCreated, "invite H")
			e.mustStatus(e.invite(owner, l.ID, a.N), http.StatusCreated, "invite N")
			if withBookings {
				if _, err := e.db.Exec(`INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time, booker_erased_at)
					VALUES ($1, '', '', now() - interval '200 days', now() - interval '200 days' + interval '30 minutes', now() - interval '1 day')`, l.ID); err != nil {
					t.Fatal(err)
				}
			}
			if !e.lists(a.hID, l.ID) {
				t.Fatal("H does not list L before the delete")
			}
			e.deleteLink(owner, l.ID)
			var pending, accepted int
			if err := e.db.QueryRow(`SELECT count(*) FILTER (WHERE user_id IS NULL), count(*) FILTER (WHERE user_id = $2)
				FROM scheduling_link_hosts WHERE link_id = $1`, l.ID, q).Scan(&pending, &accepted); err != nil {
				t.Fatal(err)
			}
			if pending != 0 || accepted != 1 {
				t.Errorf("after the delete: %d invitations, %d rows of Q; want 0 and 1", pending, accepted)
			}
			if e.lists(a.hID, l.ID) {
				t.Error("H lists the deleted link")
			}
			for _, w := range []*httptest.ResponseRecorder{e.answer(a.hID, l.ID, "accept"), e.answer(a.hID, l.ID, "decline"), e.leave(a.hID, l.ID)} {
				pac112Body(t, w, http.StatusNotFound, `{"error":"not found"}`, "answer on the deleted link")
			}
		})
	}
}

// AC-18: leave on a pending invitation: by the holder 204 and gone (a second
// leave 404); by an unrelated account 404, unchanged; by the unconfirmed
// carrier of U 404, unchanged; by the owner 400.
func TestPAC149_AC18_LeavePendingInvitation(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("ac18-owner")
	stranger, _ := e.confirmed("ac18-stranger")
	a := e.pac149Four()
	l, l2 := e.link(owner, nil), e.link(owner, nil)
	e.mustStatus(e.invite(owner, l.ID, a.H), http.StatusCreated, "invite H")
	e.mustStatus(e.invite(owner, l2.ID, a.U), http.StatusCreated, "invite U")

	pac112Body(t, e.leave(stranger, l.ID), http.StatusNotFound, `{"error":"not found"}`, "stranger leaves")
	if n := pac149InvitationsTo(t, e.db, a.H); n != 1 {
		t.Errorf("after the stranger's leave: %d invitations to H", n)
	}
	pac112Body(t, e.leave(a.uID, l2.ID), http.StatusNotFound, `{"error":"not found"}`, "Uc leaves")
	if n := pac149InvitationsTo(t, e.db, a.U); n != 1 {
		t.Errorf("after Uc's leave: %d invitations to U", n)
	}
	pac112Body(t, e.leave(owner, l.ID), http.StatusBadRequest, `{"error":"owner cannot leave link"}`, "owner leaves")
	w := e.leave(a.hID, l.ID)
	if w.Code != http.StatusNoContent {
		t.Fatalf("holder leaves: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(e.read(owner, l.ID).Body.String(), a.H) {
		t.Error("the invitation is still in the owner's roster")
	}
	pac112Body(t, e.leave(a.hID, l.ID), http.StatusNotFound, `{"error":"not found"}`, "second leave")
}

// AC-19: states no product path creates, inserted directly. A, the holder of
// a@x, with an accepted entry on L and a stray invitation for A@x: A's list
// omits L; A's decline acts on A's entry and removes the invitation in the
// same write. An invitation equal to the owner's address: the owner's list
// omits the link; with an owner row the owner's accept is 200 on it and
// removes the invitation (deviation D-a); on a link with no owner row it is
// the 404 and nothing is written (B-2). No 500, and the owner never becomes a
// non-owner entry.
func TestPAC149_AC19_StrayInvitationNever500(t *testing.T) {
	e := newPAC112Env(t)
	owner, ownerAddr := e.confirmed("ac19-owner")
	aID, aAddr := e.confirmed("ac19-a")
	l := e.link(owner, map[uuid.UUID]string{aID: "accepted"})
	if _, err := storage.AddLinkInvitation(e.db, l.ID, strings.ToUpper(aAddr)); err != nil {
		t.Fatalf("the storage refused the stray invitation (the AC would pass on the refusal): %v", err)
	}
	if e.lists(aID, l.ID) {
		t.Error("A lists a link it already has an entry on")
	}
	pac112Body(t, e.answer(aID, l.ID, "decline"), http.StatusOK, `{"status":"declined"}`, "A declines")
	if got := e.hostRows(l.ID); got[aID] != "declined" || len(got) != 2 {
		t.Errorf("after A's decline: %v, want the owner and A declined, no invitation", got)
	}
	// Leave (contract md §2.4 case 1', deviation D-b): A leaves a link where it
	// has an entry beside a stray invitation for its address; both go.
	lLeave := e.link(owner, map[uuid.UUID]string{aID: "accepted"})
	if _, err := storage.AddLinkInvitation(e.db, lLeave.ID, strings.ToUpper(aAddr)); err != nil {
		t.Fatal(err)
	}
	if w := e.leave(aID, lLeave.ID); w.Code != http.StatusNoContent {
		t.Fatalf("A leaves: %d %s", w.Code, w.Body.String())
	}
	if n := pac149InvitationsTo(t, e.db, aAddr); n != 0 {
		t.Errorf("after A left: %d stray invitations to A's address, want 0", n)
	}
	if got := e.hostRows(lLeave.ID); len(got) != 1 || got[owner] != "accepted" {
		t.Errorf("after A left: %v, want the owner alone", got)
	}

	// The owner's address, with an owner row.
	l2 := e.link(owner, nil)
	if _, err := storage.AddLinkInvitation(e.db, l2.ID, strings.ToUpper(ownerAddr)); err != nil {
		t.Fatal(err)
	}
	if e.lists(owner, l2.ID) {
		t.Error("the owner lists its own link")
	}
	pac112Body(t, e.answer(owner, l2.ID, "accept"), http.StatusOK, `{"status":"accepted"}`, "owner accepts (owner row)")
	if got := e.hostRows(l2.ID); len(got) != 1 || got[owner] != "accepted" {
		t.Errorf("after the owner's accept: %v, want the owner row alone", got)
	}

	// The owner's address, on a link with no owner row.
	l3, err := storage.CreateSchedulingLink(e.db, &storage.SchedulingLink{OwnerUserID: owner, Slug: "pac149-ac19-" + uuid.NewString()[:8],
		Title: "PAC-149", DurationOptions: []int{30}, DaysOfWeek: []int{1}, WindowStart: "09:00", WindowEnd: "17:00", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storage.AddLinkInvitation(e.db, l3.ID, ownerAddr); err != nil {
		t.Fatal(err)
	}
	if e.lists(owner, l3.ID) {
		t.Error("the owner lists its own link with no owner row")
	}
	pac112Body(t, e.answer(owner, l3.ID, "accept"), http.StatusNotFound, `{"error":"not found"}`, "owner accepts (no owner row)")
	var n, mine int
	if err := e.db.QueryRow(`SELECT count(*), count(*) FILTER (WHERE user_id = $2) FROM scheduling_link_hosts WHERE link_id = $1`, l3.ID, owner).Scan(&n, &mine); err != nil {
		t.Fatal(err)
	}
	if n != 1 || mine != 0 {
		t.Errorf("no owner row: %d rows, %d of the owner; want the stray invitation alone", n, mine)
	}
}

// API-072 and SF-068 (spec §2.4): the PAC-101 "unknown invitee" and the
// PAC-120 "user not found before the limit" pins are superseded: no answer of
// the single invite is "user not found", at any count.
func TestPAC149_NoUserNotFound(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("unf-owner")
	l := e.link(owner, nil)
	for i := 0; i < 7; i++ {
		w := e.invite(owner, l.ID, pac112Addr(fmt.Sprintf("unf-%d", i)))
		if strings.Contains(w.Body.String(), "user not found") || w.Code == http.StatusNotFound {
			t.Errorf("invite %d: %d %s", i, w.Code, w.Body.String())
		}
	}
}

// Spec §3.2 (the over-5 rule): a list longer than 5 is refused at the first
// entry that equals neither the owner's address nor a current entry, a
// malformed one included: six malformed entries are the 422, on create and
// on PATCH, and nothing is written.
func TestPAC149_OverFiveMalformedIsRefused(t *testing.T) {
	e := newPAC112Env(t)
	owner, _ := e.confirmed("over5-owner")
	list := make([]string, 6)
	for i := range list {
		list[i] = fmt.Sprintf("not-an-address-%d", i)
	}
	before := e.linkCount(owner)
	pac112Body(t, e.create(owner, map[string]any{"co_host_emails": list}), http.StatusUnprocessableEntity, pac112CapMsg, "create with 6 malformed")
	if n := e.linkCount(owner); n != before {
		t.Errorf("links %d -> %d after a refused create", before, n)
	}
	l := e.link(owner, nil)
	pac112Body(t, e.patch(owner, l.ID, map[string]any{"title": "changed", "co_host_emails": list}),
		http.StatusUnprocessableEntity, pac112CapMsg, "PATCH with 6 malformed")
	if e.title(l.ID) != l.Title {
		t.Error("a refused PATCH changed the title")
	}
}
