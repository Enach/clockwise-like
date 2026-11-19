package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// PAC-148 (docs/specs/PAC-148.md §4; contracts/features/PAC-148.md §13): the
// organisation member list lets no one at a consumer domain, and no account
// the domain did not vouch for, enumerate accounts. Membership is the rule
// of contract md §2.1, decided at every read; sign-in joins or leaves.

// pac148MembershipRequired is the membership 403's body, from the generated
// constant (contract challenge revision 1, N-6).
var pac148MembershipRequired = `{"error":"` + string(gen.OrgMembershipRequired) + `"}`

func pac148Members(t *testing.T, db *sql.DB, user uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	(&orgHandlers{db: db}).members(w, withUser(httptest.NewRequest(http.MethodGet, "/api/org/members", nil), user))
	return w
}

func pac148Me(t *testing.T, db *sql.DB, user uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	(&meHandlers{db: db}).me(w, withUser(httptest.NewRequest(http.MethodGet, "/api/auth/me", nil), user))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/auth/me = %d %s", w.Code, w.Body.String())
	}
	return w
}

// pac148MemberIDs decodes a member-list 200 strictly against the contract
// and returns the listed ids.
func pac148MemberIDs(t *testing.T, w *httptest.ResponseRecorder) map[uuid.UUID]bool {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("member list = %d %s", w.Code, w.Body.String())
	}
	var ms []gen.OrgMember
	strictDecode(t, "GET /api/org/members", w.Body.Bytes(), &ms)
	out := map[uuid.UUID]bool{}
	for _, m := range ms {
		out[uuid.UUID(m.Id)] = true
	}
	return out
}

func pac148AssertEmptyList(t *testing.T, what string, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Errorf("%s: member list = %d %s, want 200 []", what, w.Code, w.Body.String())
	}
}

func pac148AssertNoOrg(t *testing.T, what string, w *httptest.ResponseRecorder) {
	t.Helper()
	var me gen.CurrentUserResponse
	strictDecode(t, "GET /api/auth/me", w.Body.Bytes(), &me)
	if me.Org != nil || strings.Contains(w.Body.String(), `"org"`) {
		t.Errorf("%s: /api/auth/me carries an org: %s", what, w.Body.String())
	}
}

func pac148OrgRows(t *testing.T, db *sql.DB, d string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM organizations WHERE domain = $1`, d).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func pac148Confirmations(t *testing.T, db *sql.DB, user uuid.UUID) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM org_ai_confirmations WHERE user_id = $1`, user).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func pac148ConfirmFor(t *testing.T, db *sql.DB, user, org uuid.UUID, consentSince int64) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO org_ai_confirmations (user_id, org_id, consent_since_version, confirmed_at) VALUES ($1, $2, $3, now())`, user, org, consentSince); err != nil {
		t.Fatal(err)
	}
}

// pac148SetAccount sets an account's address and standing directly.
func pac148SetAccount(t *testing.T, db *sql.DB, user uuid.UUID, email, standing string) {
	t.Helper()
	if _, err := db.Exec(`UPDATE users SET email = $2, address_standing = $3 WHERE id = $1`, user, email, standing); err != nil {
		t.Fatal(err)
	}
}

// --------------------------------------------------------------------- AC-1

// AC-1: S signs in through Google with a verified address at an unlisted
// provider (ISP) and no hosted domain (mailbox evidence). With I1 (mailbox),
// I2 (unbound legacy) and I3 (domain-confirmed) associated with ISP's
// organisation, S gets [], no org, belongs to nothing, and creates no
// organisation row. Without them the answers are byte-identical, and the
// statements S's sign-in step and member-list read run are the same and
// never keyed on ISP.
func TestPAC148_AC1_ConsumerDomainSeesNoOne(t *testing.T) {
	db := openTestDB(t)
	type outcome struct {
		list, me       string
		signIn, read   []string
		orgReads       int64
		orgRowsCreated int
	}
	run := func(withOthers bool) outcome {
		t.Helper()
		isp := "isp-" + uuid.NewString()[:8] + ".test"
		before := 0
		if withOthers {
			org, err := storage.UpsertOrg(db, isp, isp)
			if err != nil {
				t.Fatal(err)
			}
			i1 := pac71Bound(t, "i1@"+isp, googleIdentity(pac71GoogleID()), storage.AddressUnconfirmed)
			i2 := pac71Legacy(t, "i2@"+isp, "google", pac71GoogleID())
			i3 := pac71Bound(t, "i3@"+isp, googleIdentity(pac71GoogleID()), storage.AddressDomainConfirmed)
			for _, id := range []uuid.UUID{i1, i2, i3} {
				pac148Plant(t, db, id, org.ID)
			}
			before = 1
		}
		var orgReads atomic.Int64
		storage.InstallOrgRead(t, func(uuid.UUID) error { orgReads.Add(1); return nil })
		email := "s@" + isp
		signIn := pac148Record(t)
		s, _ := sessionOf(t, pac71Google(t, googleProfile(pac71GoogleID(), email, true, "")))
		o := outcome{signIn: signIn()}
		read := pac148Record(t)
		list := pac148Members(t, db, s)
		o.read = read()
		me := pac148Me(t, db, s)
		o.orgReads = orgReads.Load()
		o.orgRowsCreated = pac148OrgRows(t, db, isp) - before

		pac148AssertEmptyList(t, "S", list)
		pac148AssertNoOrg(t, "S", me)
		if raw := pac148RawOrg(t, db, s); raw.Valid {
			t.Errorf("S's stored org_id = %s, want null", raw.UUID)
		}
		var created string
		var m map[string]any
		_ = json.Unmarshal(me.Body.Bytes(), &m)
		created, _ = m["createdAt"].(string)
		norm := func(b string) string {
			b = strings.ReplaceAll(b, s.String(), "<S-ID>")
			b = strings.ReplaceAll(b, email, "<S-EMAIL>")
			if created != "" {
				b = strings.ReplaceAll(b, created, "<S-CREATED>")
			}
			return b
		}
		o.list, o.me = norm(list.Body.String()), norm(me.Body.String())
		if ct := list.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("member list Content-Type = %q", ct)
		}
		return o
	}
	with, without := run(true), run(false)
	if with.list != without.list || with.me != without.me {
		t.Errorf("answers differ with and without accounts at the domain:\nlist %q / %q\nme   %q / %q", with.list, without.list, with.me, without.me)
	}
	if strings.Join(with.signIn, ",") != strings.Join(without.signIn, ",") || strings.Join(with.read, ",") != strings.Join(without.read, ",") {
		t.Errorf("statements differ: sign-in %v / %v, read %v / %v", with.signIn, without.signIn, with.read, without.read)
	}
	for _, o := range []outcome{with, without} {
		if pac148Contains(o.signIn, "org-upsert") || pac148Contains(o.signIn, "membership-set") || !pac148Contains(o.signIn, "membership-lock") {
			t.Errorf("sign-in step statements = %v, want the lock and no organisation statement", o.signIn)
		}
		if strings.Join(o.read, ",") != "user-row" {
			t.Errorf("member-list statements = %v, want the caller's row alone", o.read)
		}
		if o.orgReads != 0 {
			t.Errorf("%d organisation reads for S, want 0", o.orgReads)
		}
		if o.orgRowsCreated != 0 {
			t.Errorf("S's sign-in created %d organisation rows", o.orgRowsCreated)
		}
	}
}

// --------------------------------------------------------------------- AC-3

// AC-3: at a company domain, A1 (Google, hd) and A2 (Microsoft, tenant
// verifies the domain) list exactly one another; U (mailbox evidence) is
// listed by neither, lists nobody and has no org; in either sign-in order,
// and U creates no organisation row.
func TestPAC148_AC3_CompanyListsOnlyVouchedColleagues(t *testing.T) {
	db := openTestDB(t)
	for _, uFirst := range []bool{true, false} {
		d := pac71Domain()
		signU := func() uuid.UUID {
			id, _ := sessionOf(t, pac71Google(t, googleProfile(pac71GoogleID(), "u@"+d, true, "")))
			return id
		}
		var u uuid.UUID
		if uFirst {
			u = signU()
			if n := pac148OrgRows(t, db, d); n != 0 {
				t.Fatalf("U's sign-in created %d organisation rows for %s", n, d)
			}
		}
		a1, _ := sessionOf(t, pac71Google(t, googleProfile(pac71GoogleID(), "a1@"+d, true, d)))
		if n := pac148OrgRows(t, db, d); n != 1 {
			t.Fatalf("after A1's sign-in, %d organisation rows for %s, want 1", n, d)
		}
		a2, _ := sessionOf(t, pac71Microsoft(t, pac71GUID(), "a2@"+d, orgAnswer(pac71GUID(), d)))
		if !uFirst {
			u = signU()
		}
		for name, viewer := range map[string]uuid.UUID{"A1": a1, "A2": a2} {
			ids := pac148MemberIDs(t, pac148Members(t, db, viewer))
			if len(ids) != 2 || !ids[a1] || !ids[a2] || ids[u] {
				t.Errorf("U first %v: %s's list = %v, want exactly A1 and A2", uFirst, name, ids)
			}
		}
		pac148AssertEmptyList(t, "U", pac148Members(t, db, u))
		pac148AssertNoOrg(t, "U", pac148Me(t, db, u))
	}
}

// --------------------------------------------------------------------- AC-4

// AC-4: every membership consumer agrees. For U (unconfirmed at ACME, as its
// sign-in leaves it) and for two planted associations with no sign-in or
// migration after them, P1 (U-like, in ACME's organisation with a
// confirmation of the current consent period) and P2 (domain-confirmed at
// OTHER, in ACME's organisation), every answer is the non-member's. A1 is
// answered as today.
func TestPAC148_AC4_EveryConsumerAgrees(t *testing.T) {
	db := openTestDB(t)
	d := pac71Domain()
	other := pac71Domain()
	a1 := newBYOK(t, "pac148-ac4-a1-"+uuid.NewString()[:6])
	acme := p2Org(t, a1, d)
	p2Admin(t, a1.user, acme, p2OpenAI(p2KeyA))
	cfg := p2Admin(t, a1.user, acme, storage.OrgAIPatch{PersonalKeysAllowed: bp(false)})
	a1.mustConfirm(cfg.Version)
	if _, err := db.Exec(`INSERT INTO sso_providers (domain, provider_name, provider_type, oidc_issuer, oidc_client_id) VALUES ($1, 'ACME', 'oidc', 'https://idp.acme.test', 'c')`, d); err != nil {
		t.Fatal(err)
	}
	e := newSSOEnv(t)
	a1Email := pac148Email(t, db, a1.user)

	u := newBYOK(t, "pac148-ac4-u-"+uuid.NewString()[:6])
	pac148SetAccount(t, db, u.user, "u-"+uuid.NewString()[:6]+"@"+d, storage.AddressUnconfirmed)
	p1 := newBYOK(t, "pac148-ac4-p1-"+uuid.NewString()[:6])
	pac148SetAccount(t, db, p1.user, "p1-"+uuid.NewString()[:6]+"@"+d, storage.AddressUnconfirmed)
	pac148Plant(t, db, p1.user, acme)
	pac148ConfirmFor(t, db, p1.user, acme, cfg.ConsentSince)
	p2 := newBYOK(t, "pac148-ac4-p2-"+uuid.NewString()[:6])
	pac148SetAccount(t, db, p2.user, "p2-"+uuid.NewString()[:6]+"@"+other, storage.AddressDomainConfirmed)
	pac148Plant(t, db, p2.user, acme)
	pac148ConfirmFor(t, db, p2.user, acme, cfg.ConsentSince)

	sess := func(id uuid.UUID) string {
		return session(t, &storage.User{ID: id, Email: pac148Email(t, db, id)}, time.Now())
	}
	orgAI := func(id uuid.UUID) map[string]*httptest.ResponseRecorder {
		out := map[string]*httptest.ResponseRecorder{}
		w := httptest.NewRecorder()
		orgAIFor(t).get(w, withUser(httptest.NewRequest(http.MethodGet, "/api/organization/ai", nil), id))
		out["getOrganizationAI"] = w
		w = httptest.NewRecorder()
		orgAIFor(t).confirm(w, withUser(httptest.NewRequest(http.MethodPost, "/api/organization/ai/consent", strings.NewReader(`{"configurationVersion":`+jsonInt(cfg.Version)+`}`)), id))
		out["confirmOrganizationAI"] = w
		w = httptest.NewRecorder()
		orgAIFor(t).patch(w, withUser(httptest.NewRequest(http.MethodPatch, "/api/organization/ai", strings.NewReader(`{"configurationVersion":`+jsonInt(cfg.Version)+`,"model":"gpt-x"}`)), id))
		out["patchOrganizationAI"] = w
		return out
	}

	nonMember := func(name string, x *byok, planted bool) {
		t.Helper()
		email := pac148Email(t, db, x.user)
		for op, w := range orgAI(x.user) {
			assertBody(t, w, http.StatusForbidden, pac148MembershipRequired)
			if t.Failed() {
				t.Fatalf("%s: %s", name, op)
			}
		}
		if pac148Confirmations(t, db, x.user) != map[bool]int{true: 1, false: 0}[planted] {
			t.Errorf("%s: confirmOrganizationAI recorded something", name)
		}
		s := sess(x.user)
		for _, rq := range []struct{ op, method, path, body string }{
			{"listSsoProviders", http.MethodGet, "/api/admin/sso", ""},
			{"getSsoStepupOptions", http.MethodGet, "/api/admin/sso/stepup/options", ""},
			{"createSsoProvider", http.MethodPost, "/api/admin/sso", createBody(d)},
			{"deleteSsoProvider", http.MethodDelete, "/api/admin/sso/" + d, ""},
			{"startSsoAdminStepup", http.MethodGet, "/api/admin/sso/stepup?provider=google", ""},
		} {
			w := e.do(t, rq.method, rq.path, rq.body, s)
			if w.Code != http.StatusForbidden || strings.TrimSpace(w.Body.String()) != pac148MembershipRequired {
				t.Errorf("%s: %s = %d %s, want 403 %s", name, rq.op, w.Code, w.Body.String(), pac148MembershipRequired)
			}
			if w.Header().Get("Location") != "" || stateCookie(w) != nil {
				t.Errorf("%s: %s redirected or set a step-up cookie", name, rq.op)
			}
		}
		if n, _ := e.providerRow(t, d); n != 1 {
			t.Errorf("%s: ACME's SSO provider rows = %d, want 1 (nothing deleted)", name, n)
		}
		// The callback with a validly signed state naming ACME (minted with
		// the server's key; the start itself refuses).
		ck := &http.Cookie{Name: stepupCookie, Value: e.h.signState(stepupState{UserID: x.user, OrgID: acme, Provider: "google", Nonce: "n148", Expires: e.clock.Now().Add(time.Minute).Unix()})}
		cb := e.do(t, http.MethodGet, stepupCallbackPath+"?code=c1&state=n148", "", s, ck)
		wantOutcome(t, cb, cb.Code, "stepup=denied&reason=state_mismatch")
		if e.liveGrants(t, x.user) != 0 {
			t.Errorf("%s: the callback issued a grant", name)
		}
		if st := x.aiStatus(); st["inOrganization"] != false {
			t.Errorf("%s: aiStatus.inOrganization = %v, want false", name, st["inOrganization"])
		}
		if ok, err := storage.AddressVisibleTo(db, a1.user, email); err != nil || ok {
			t.Errorf("%s: rule (b) for viewer A1 = %v, %v; want false", name, ok, err)
		}
		pac148AssertEmptyList(t, name, pac148Members(t, db, x.user))
		if ids := pac148MemberIDs(t, pac148Members(t, db, a1.user)); ids[x.user] {
			t.Errorf("%s is on A1's member list", name)
		}
		pac148AssertNoOrg(t, name, pac148Me(t, db, x.user))
		// G-4: the stored choice is the default "organization": row 6,
		// nothing sent, so ACME's key never leaves (parse, brief refresh,
		// probe; MCP's clockwise_parse_command reaches only this parse).
		x.reset()
		x.allThree("nothing_configured", "", "user")
		// Personal keys are not forbidden for it: its own key is accepted
		// and is what a parse sends.
		x.mustPatch(`{"llmProvider":"openai","llmApiKey":"` + p2KeyU + `"}`)
		x.parseSends(name+" own key", p2KeyU, "api.openai.com")
	}
	nonMember("U", u, false)
	nonMember("P1", p1, true)
	nonMember("P2", p2, true)

	// A1, as today.
	for op, w := range orgAI(a1.user) {
		if op != "patchOrganizationAI" && w.Code != http.StatusOK {
			t.Errorf("A1: %s = %d %s", op, w.Code, w.Body.String())
		}
	}
	if w := e.do(t, http.MethodGet, "/api/admin/sso", "", sess(a1.user)); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "ACME") {
		t.Errorf("A1: listSsoProviders = %d %s", w.Code, w.Body.String())
	}
	ck := &http.Cookie{Name: stepupCookie, Value: e.h.signState(stepupState{UserID: a1.user, OrgID: acme, Provider: "google", Nonce: "n148", Expires: e.clock.Now().Add(time.Minute).Unix()})}
	if cb := e.do(t, http.MethodGet, stepupCallbackPath+"?code=c1&state=n148", "", sess(a1.user), ck); strings.Contains(cb.Header().Get("Location"), "state_mismatch") {
		t.Errorf("A1's callback with ACME's state = %s, want past the state check", cb.Header().Get("Location"))
	}
	if st := a1.aiStatus(); st["inOrganization"] != true || st["configuration"] != "organization" {
		t.Errorf("A1 aiStatus = %v", st)
	}
	if ids := pac148MemberIDs(t, pac148Members(t, db, a1.user)); len(ids) != 1 || !ids[a1.user] {
		t.Errorf("A1's list = %v, want A1 alone", ids)
	}
	if ok, err := storage.AddressVisibleTo(db, a1.user, a1Email); err != nil || !ok {
		t.Errorf("A1's own address under rule (b) = %v, %v", ok, err)
	}
	if !strings.Contains(pac148Me(t, db, a1.user).Body.String(), `"org":{`) {
		t.Error("A1's /api/auth/me has no org")
	}
	a1.parseSends("A1", p2KeyA, "api.openai.com")
}

func jsonInt(v int64) string { b, _ := json.Marshal(v); return string(b) }

func pac148Email(t *testing.T, db *sql.DB, id uuid.UUID) string {
	t.Helper()
	var e string
	if err := db.QueryRow(`SELECT email FROM users WHERE id = $1`, id).Scan(&e); err != nil {
		t.Fatal(err)
	}
	return e
}

// --------------------------------------------------------------------- AC-5

// AC-5: an account associated under the old rule whose sign-in no longer
// qualifies leaves at sign-in, with its organisation-AI confirmations, and
// the sign-in succeeds: (i) mailbox-bound U; (ii) a domain-confirmed
// account at a newly listed domain.
func TestPAC148_AC5_LeaveAtSignIn(t *testing.T) {
	db := openTestDB(t)
	for _, tc := range []struct {
		name, email, hd, wantStanding string
	}{
		{"mailbox-bound", "u@" + pac71Domain(), "", storage.AddressUnconfirmed},
		{"newly listed domain", "pac148-" + uuid.NewString()[:8] + "@hotmail.fr", "hotmail.fr", storage.AddressDomainConfirmed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gid := pac71GoogleID()
			profile := googleProfile(gid, tc.email, true, tc.hd)
			id, _ := sessionOf(t, pac71Google(t, profile))
			var standing string
			_ = db.QueryRow(`SELECT address_standing FROM users WHERE id = $1`, id).Scan(&standing)
			if standing != tc.wantStanding {
				t.Fatalf("standing = %s, want %s", standing, tc.wantStanding)
			}
			d := tc.email[strings.Index(tc.email, "@")+1:]
			org, err := storage.UpsertOrg(db, d, d)
			if err != nil {
				t.Fatal(err)
			}
			pac148Plant(t, db, id, org.ID)
			pac148ConfirmFor(t, db, id, org.ID, 1)
			again, _ := sessionOf(t, pac71Google(t, profile))
			if again != id {
				t.Fatalf("the second sign-in reached %s, want %s", again, id)
			}
			if raw := pac148RawOrg(t, db, id); raw.Valid {
				t.Errorf("stored org_id after the sign-in = %s, want null", raw.UUID)
			}
			if n := pac148Confirmations(t, db, id); n != 0 {
				t.Errorf("%d organisation-AI confirmations after leaving, want 0", n)
			}
		})
	}
}

// --------------------------------------------------------------------- AC-6

// AC-6: L, an unbound legacy unconfirmed account at ACME detached by 043,
// signs in through Google with hd = ACME (authoritative; rule (a) binds it):
// it joins ACME, and its organisation-AI state is "not confirmed" (row 5)
// even though a confirmation of ACME was left over.
func TestPAC148_AC6_LegacyJoinsAtSignIn(t *testing.T) {
	db := openTestDB(t)
	d := pac71Domain()
	admin := newBYOK(t, "pac148-ac6-admin-"+uuid.NewString()[:6])
	acme := p2Org(t, admin, d)
	cfg := p2Admin(t, admin.user, acme, p2OpenAI(p2KeyA))
	gid := pac71GoogleID()
	l := pac71Legacy(t, "l@"+d, "google", gid)
	pac148ConfirmFor(t, db, l, acme, cfg.ConsentSince)

	id, _ := sessionOf(t, pac71Google(t, googleProfile(gid, "l@"+d, true, d)))
	if id != l {
		t.Fatalf("the sign-in reached %s, want L %s", id, l)
	}
	if got := pac148OrgOf(t, db, l); got != acme {
		t.Fatalf("L belongs to %s, want ACME %s", got, acme)
	}
	st, err := storage.ReadOrgAIState(context.Background(), db, l)
	if err != nil || !st.InOrg() || st.Confirmed() || !st.Config.Complete() {
		t.Fatalf("L's organisation-AI state = %+v, %v; want in ACME, complete, not confirmed", st, err)
	}
	lb := &byok{pac66: &pac66{t: t, user: l}}
	if nc, _ := lb.aiStatus()["notConfigured"].(map[string]any); nc["reason"] != "organization_unconfirmed" {
		t.Errorf("L's aiStatus.notConfigured = %v, want row 5 organization_unconfirmed", nc)
	}
}

// --------------------------------------------------------------------- AC-8

// AC-8 (D-8, API-047, SF-075): a fault reading the caller is the 500, never
// []; a caller row that does not exist, and a readable caller with no
// organisation, get 200 [].
func TestPAC148_AC8_UnreadableCallerIs500(t *testing.T) {
	db := openTestDB(t)
	user := createTestUser(t, "pac148-ac8-"+uuid.NewString()[:8]+"@ac8.test")
	fault := errors.New("pac148: caller read fault")
	var answer error
	storage.InstallUserRead(t, func(id uuid.UUID) error {
		if id == user {
			return answer
		}
		return nil
	})
	answer = fault
	reported, _ := captureServerErrors(t)
	w := pac148Members(t, db, user)
	assertBody(t, w, http.StatusInternalServerError, `{"error":"internal"}`)
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("500 Content-Type = %q", ct)
	}
	var body gen.ErrorResponse
	strictDecode(t, "member list 500", w.Body.Bytes(), &body)
	if len(*reported) == 0 {
		t.Error("the fault was not reported")
	}
	answer = sql.ErrNoRows
	pac148AssertEmptyList(t, "caller row missing (seam)", pac148Members(t, db, user))
	answer = nil
	pac148AssertEmptyList(t, "readable caller with no organisation", pac148Members(t, db, user))
	pac148AssertEmptyList(t, "caller deleted under a live session", pac148Members(t, db, uuid.New()))
}

// --------------------------------------------------------------------- AC-9

// AC-9 (API-091's second opinion): the member list's 200 decodes strictly
// against the bundle, provider is one of the enum's values, and the bundle's
// OrgMember carries no x-uncertain.
func TestPAC148_AC9_MemberListDecodesStrictly(t *testing.T) {
	db := openTestDB(t)
	d := pac71Domain()
	a1, _ := sessionOf(t, pac71Google(t, googleProfile(pac71GoogleID(), "a1@"+d, true, d)))
	a2, _ := sessionOf(t, pac71Microsoft(t, pac71GUID(), "a2@"+d, orgAnswer(pac71GUID(), d)))
	w := pac148Members(t, db, a1)
	var ms []gen.OrgMember
	strictDecode(t, "GET /api/org/members", w.Body.Bytes(), &ms)
	if len(ms) != 2 {
		t.Fatalf("members = %s, want A1 and A2", w.Body.String())
	}
	providers := map[uuid.UUID]gen.OrgMemberProvider{}
	for _, m := range ms {
		switch m.Provider {
		case gen.OrgMemberProviderGoogle, gen.OrgMemberProviderMicrosoft, gen.OrgMemberProviderSso:
		default:
			t.Errorf("provider %q is not in the enum", m.Provider)
		}
		providers[uuid.UUID(m.Id)] = m.Provider
	}
	if providers[a1] != gen.OrgMemberProviderGoogle || providers[a2] != gen.OrgMemberProviderMicrosoft {
		t.Errorf("providers = %v", providers)
	}
	if block := pac148Schema(t, "OrgMember"); strings.Contains(block, "x-uncertain:") {
		t.Errorf("the bundle's OrgMember still carries an x-uncertain:\n%s", block)
	}
}

// pac148Bundle is the committed bundle's text.
func pac148Bundle(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "openapi", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(raw), "\r\n", "\n")
}

// pac148Schema is one components schema's block of the bundle.
func pac148Schema(t *testing.T, name string) string {
	t.Helper()
	b := pac148Bundle(t)
	i := strings.Index(b, "\n    "+name+":\n")
	if i < 0 {
		t.Fatalf("no schema %s in the bundle", name)
	}
	rest := b[i+1:]
	end := len(rest)
	for j := strings.Index(rest, "\n"); j >= 0 && j+1 < len(rest); {
		line := rest[j+1:]
		if strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "     ") {
			end = j
			break
		}
		k := strings.Index(line, "\n")
		if k < 0 {
			break
		}
		j += k + 1
	}
	return rest[:end]
}

// --------------------------------------------------------------------- AC-10

// AC-10: viewer A1 (V) owns T; on its roster, A2 (a member) shows the flag
// and "no data"; U (unconfirmed, planted into V's organisation) and N (an
// address nobody holds, same length) are byte-identical in the roster and in
// analytics, flag false with the same estimate. PAC-130 AC-17 still holds.
// With the viewer belonging nowhere, a confirmed and an unconfirmed account
// associated with the viewer's stored organisation are byte-identical to N.
func TestPAC148_AC10_RuleBNarrowsWithTheList(t *testing.T) {
	f := newPAC130(t)
	vOrg := pac148OrgOf(t, f.db, f.v)
	a2Addr := "a2-member@" + f.vDomain
	a2 := f.account(a2Addr, true, true, 300)
	if err := pac148ConfirmAndJoin(t, f.db, a2); err != nil {
		t.Fatal(err)
	}
	uAddr, nAddr := "u-unconf@"+f.vDomain, "n-nobody@"+f.vDomain
	u := f.account(uAddr, false, true, 300)
	pac148Plant(t, f.db, u, vOrg)
	for _, a := range []string{a2Addr, uAddr, nAddr} {
		f.mustAdd(a)
	}
	if m := f.member(a2Addr); !m.IsPacedayUser || m.ThisWeek.DataAvailable {
		t.Errorf("A2 = %+v, want flag true and no data", m)
	}
	pac148SameAsN(t, f, uAddr, nAddr)
	// PAC-130 AC-17: the flag is true exactly when V's list contains the account.
	ids := pac148MemberIDs(t, pac148Members(t, f.db, f.v))
	if !ids[a2] || ids[u] {
		t.Errorf("V's list = %v, want A2 and not U", ids)
	}

	// The viewer belongs nowhere (unconfirmed): its stored organisation's
	// accounts, confirmed or not, are estimated like N.
	g := newPAC130(t)
	gOrg := pac148OrgOf(t, g.db, g.v)
	if _, err := g.db.Exec(`UPDATE users SET address_standing = 'unconfirmed' WHERE id = $1`, g.v); err != nil {
		t.Fatal(err)
	}
	i1Addr, i3Addr, n2Addr := "i1-mailbx@"+g.vDomain, "i3-confrm@"+g.vDomain, "n2-nobody@"+g.vDomain
	i1 := g.account(i1Addr, false, true, 300)
	pac148Plant(t, g.db, i1, gOrg)
	i3 := g.account(i3Addr, true, true, 300)
	if err := pac148ConfirmAndJoin(t, g.db, i3); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{i1Addr, i3Addr, n2Addr} {
		g.mustAdd(a)
	}
	pac148SameAsN(t, g, i1Addr, n2Addr)
	pac148SameAsN(t, g, i3Addr, n2Addr)
	pac148AssertEmptyList(t, "unconfirmed viewer", pac148Members(t, g.db, g.v))
}

// pac148SameAsN asserts addr's entry equals n's, address replaced, in the
// roster and in analytics, with the flag false and an estimate.
func pac148SameAsN(t *testing.T, f *pac130Fix, addr, n string) {
	t.Helper()
	for _, path := range []string{
		"/api/manager/team" + f.scope() + "&week=" + pac130Week,
		"/api/manager/analytics" + f.scope() + "&week=" + pac130Week,
	} {
		body := f.get(path).Body.Bytes()
		ea, en := pac130Entry(t, body, addr), pac130Entry(t, body, n)
		if ea == "" || strings.ReplaceAll(ea, addr, "X") != strings.ReplaceAll(en, n, "X") {
			t.Errorf("%s: %s differs from N:\n%s\n%s", path, addr, ea, en)
		}
	}
	if m := f.member(addr); m.IsPacedayUser || !m.ThisWeek.DataAvailable {
		t.Errorf("%s = %+v, want flag false and the estimate", addr, m)
	}
}

// --------------------------------------------------------------------- AC-14

// AC-14: the bundle states the rule, its pointers, the 500 and the enum; the
// AC-1, AC-3 and AC-8 answers validate against it (their tests decode them
// strictly into the generated types).
func TestPAC148_AC14_BundleStatesTheRule(t *testing.T) {
	b := pac148Bundle(t)
	start := strings.Index(b, "\n  /api/org/members:\n")
	if start < 0 {
		t.Fatal("no /api/org/members path in the bundle")
	}
	op := b[start+1:]
	op = op[:strings.Index(op[1:], "\n  /")+1]
	for _, want := range []string{
		"**Organisation membership (PAC-148",
		"(1) its `addressStanding` is `domain_confirmed`",
		"(2) the domain of its stored address is not on the consumer list",
		"domain equals the domain of its stored address, exactly",
		"PAC-148 D-8",
		"Never `[]` for a fault",
	} {
		if !strings.Contains(strings.Join(strings.Fields(op), " "), strings.Join(strings.Fields(want), " ")) {
			t.Errorf("listOrgMembers does not state %q", want)
		}
	}
	for schema, want := range map[string]string{
		"CurrentUserResponse":        "Since PAC-148",
		"AIStatus":                   "Since PAC-148",
		"SsoWriteMembershipRequired": "Since PAC-148",
		"ManagerRosterMember":        "(Before PAC-148, (b) carried",
		"OrgMember":                  "- microsoft",
	} {
		if block := strings.Join(strings.Fields(pac148Schema(t, schema)), " "); !strings.Contains(block, strings.Join(strings.Fields(want), " ")) {
			t.Errorf("%s does not say %q", schema, want)
		}
	}
	if strings.Contains(b, "until then (b) carries the list's exposure") {
		t.Error("is_paceday_user still carries the pre-PAC-148 sentence")
	}
}

// --------------------------------------------------------------------- AC-15

// AC-15 (regression pin): U, bound on mailbox evidence, is not rescued by
// the company's Workspace identity for the same address (rule 3 refuses
// it); its own mailbox sign-in still works and joins nothing; after the
// operator reassign to the Workspace identity, that sign-in joins ACME.
func TestPAC148_AC15_MailboxBoundIsNotRescued(t *testing.T) {
	db := openTestDB(t)
	d := pac71Domain()
	a1, _ := sessionOf(t, pac71Google(t, googleProfile(pac71GoogleID(), "a1@"+d, true, d)))
	acme := pac148OrgOf(t, db, a1)
	gu, gw := pac71GoogleID(), pac71GoogleID()
	u, _ := sessionOf(t, pac71Google(t, googleProfile(gu, "u@"+d, true, "")))

	assertRefused(t, pac71Google(t, googleProfile(gw, "u@"+d, true, d)))
	var standing string
	_ = db.QueryRow(`SELECT address_standing FROM users WHERE id = $1`, u).Scan(&standing)
	if standing != storage.AddressUnconfirmed || pac148RawOrg(t, db, u).Valid {
		t.Fatalf("after the refusal: standing %s, stored org %v", standing, pac148RawOrg(t, db, u))
	}
	if again, _ := sessionOf(t, pac71Google(t, googleProfile(gu, "u@"+d, true, ""))); again != u || pac148RawOrg(t, db, u).Valid {
		t.Fatalf("U's mailbox sign-in = %s, stored org %v", again, pac148RawOrg(t, db, u))
	}

	if _, err := storage.ReassignAccount(context.Background(), db, storage.ReassignRequest{
		Account: u, Identity: googleIdentity(gw), Evidence: storage.EvidenceAuthoritative,
		KeepCalendarCredentials: true, Run: storage.OperatorRun{Operator: "op", Reason: "PAC-148 AC-15"},
	}); err != nil {
		t.Fatal(err)
	}
	if id, _ := sessionOf(t, pac71Google(t, googleProfile(gw, "u@"+d, true, d))); id != u {
		t.Fatalf("the Workspace sign-in after reassign reached %s, want U", id)
	}
	if got := pac148OrgOf(t, db, u); got != acme {
		t.Fatalf("U joined %s, want ACME %s", got, acme)
	}
}
