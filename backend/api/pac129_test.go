package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/internal/lifecycle"
	"github.com/Enach/paceday/backend/internal/oauthfake"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	googlecalendar "google.golang.org/api/calendar/v3"
)

// PAC-129 contract tests (spec docs/specs/PAC-129.md §4; contract
// contracts/features/PAC-129.md §14). Every request goes through the real
// router and requireAuth with a session cookie.

// day D: a Monday far from every other journey.
var pac129Day = time.Date(2030, 1, 7, 0, 0, 0, 0, time.UTC)

// --- the Google stand-in ----------------------------------------------------------

type pac129Call struct {
	path   string
	bearer string
	ids    []string
}

// pac129Google answers free/busy from table (an id's calendars entry as JSON;
// an id not in it is notFound) and event listings from events (per bearer
// token; empty otherwise), recording every call with its bearer token.
type pac129Google struct {
	mu     sync.Mutex
	calls  []pac129Call
	table  map[string]string
	events map[string]string
}

func newPAC129Google(t *testing.T) *pac129Google {
	t.Helper()
	g := &pac129Google{table: map[string]string{}, events: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !strings.HasSuffix(r.URL.Path, "/freeBusy") {
			g.mu.Lock()
			g.calls = append(g.calls, pac129Call{path: "events", bearer: bearer})
			items, ok := g.events[bearer]
			g.mu.Unlock()
			if !ok {
				items = "[]"
			}
			_, _ = w.Write([]byte(`{"kind":"calendar#events","items":` + items + `}`))
			return
		}
		var req googlecalendar.FreeBusyRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		ids := []string{}
		for _, it := range req.Items {
			ids = append(ids, it.Id)
		}
		g.mu.Lock()
		g.calls = append(g.calls, pac129Call{path: "freeBusy", bearer: bearer, ids: ids})
		parts := []string{}
		for _, id := range ids {
			entry, ok := g.table[id]
			if !ok {
				entry = `{"busy":[],"errors":[{"domain":"global","reason":"notFound"}]}`
			}
			k, _ := json.Marshal(id)
			parts = append(parts, string(k)+":"+entry)
		}
		g.mu.Unlock()
		if len(ids) > 50 { // Google's documented calendarExpansionMax
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":400,"message":"tooManyCalendarsRequested"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"kind":"calendar#freeBusy","calendars":{` + strings.Join(parts, ",") + `}}`))
	}))
	t.Cleanup(srv.Close)
	calendar.Configure(config.CalendarEndpoint{BaseURL: srv.URL + config.CalendarAPIRoot, Guard: true})
	t.Cleanup(func() { calendar.Configure(config.ProductionCalendarEndpoint()) })
	return g
}

func (g *pac129Google) busy(id string, windows ...[2]string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	ws := []string{}
	for _, w := range windows {
		ws = append(ws, fmt.Sprintf(`{"start":%q,"end":%q}`, w[0], w[1]))
	}
	g.table[id] = `{"busy":[` + strings.Join(ws, ",") + `]}`
}

func (g *pac129Google) freeBusyCalls() []pac129Call {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := []pac129Call{}
	for _, c := range g.calls {
		if c.path == "freeBusy" {
			out = append(out, c)
		}
	}
	return out
}

func (g *pac129Google) sawBearer(tok string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, c := range g.calls {
		if c.bearer == tok {
			return true
		}
	}
	return false
}

// --- fixture ----------------------------------------------------------------------

type p129 struct {
	t      *testing.T
	r      *chi.Mux
	team   uuid.UUID
	o, m   uuid.UUID
	oEmail string
	mEmail string
}

func p129Addr(tag string) string {
	return fmt.Sprintf("%s-%s@corp.example", tag, uuid.NewString()[:8])
}

// newP129 builds team T owned by O with Paceday member M, served by the full
// router with cfg.
func newP129(t *testing.T, cfg *oauth2.Config) *p129 {
	t.Helper()
	if cfg == nil {
		cfg = &oauth2.Config{}
	}
	f := &p129{t: t, r: pac71Router(t, cfg), oEmail: p129Addr("o"), mEmail: p129Addr("m")}
	f.o = createConfirmedUser(t, f.oEmail)
	f.m = createConfirmedUser(t, f.mEmail)
	db := openTestDB(t)
	team, err := storage.CreateTeam(db, "T", f.o)
	if err != nil {
		t.Fatal(err)
	}
	f.team = team.ID
	if err := storage.AddTeamMember(db, f.team, f.o, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := storage.AddTeamMember(db, f.team, f.m, "member"); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *p129) do(user uuid.UUID, method, path, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	return doAs(f.t, f.r, method, path, body, sessionCookie(f.t, user, 0))
}

func (f *p129) teamPath(rest string) string { return "/api/teams/" + f.team.String() + rest }

func (f *p129) add(user uuid.UUID, email, name string) *httptest.ResponseRecorder {
	f.t.Helper()
	b, _ := json.Marshal(map[string]string{"email": email, "name": name})
	return f.do(user, http.MethodPost, f.teamPath("/people"), string(b))
}

func (f *p129) invite(email, name string) *httptest.ResponseRecorder {
	f.t.Helper()
	b, _ := json.Marshal(map[string]string{"email": email, "name": name})
	return f.do(f.o, http.MethodPost, f.teamPath("/members/invite"), string(b))
}

func (f *p129) remove(user uuid.UUID, email string) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.do(user, http.MethodDelete, f.teamPath("/people/"+url.PathEscape(email)), "")
}

type p129Person struct {
	Email      string     `json:"email"`
	Name       string     `json:"name"`
	Kind       string     `json:"kind"`
	UserID     *uuid.UUID `json:"userId"`
	Role       *string    `json:"role"`
	Invitation *struct {
		ExpiresAt time.Time `json:"expiresAt"`
	} `json:"invitation"`
}

type p129Detail struct {
	Members []map[string]any `json:"members"`
	People  []p129Person     `json:"people"`
}

func (f *p129) detail(user uuid.UUID) p129Detail {
	f.t.Helper()
	w := f.do(user, http.MethodGet, f.teamPath(""), "")
	if w.Code != http.StatusOK {
		f.t.Fatalf("getTeam = %d %s", w.Code, w.Body.String())
	}
	var d p129Detail
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		f.t.Fatal(err)
	}
	return d
}

func (f *p129) person(user uuid.UUID, email string) *p129Person {
	f.t.Helper()
	for _, p := range f.detail(user).People {
		if p.Email == email {
			pp := p
			return &pp
		}
	}
	return nil
}

func (f *p129) count(q string, args ...any) int {
	f.t.Helper()
	var n int
	if err := openTestDB(f.t).QueryRow(q, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func (f *p129) addedRows(email string) int {
	return f.count(`SELECT count(*) FROM team_added_members WHERE team_id = $1 AND email = $2`, f.team, email)
}

func (f *p129) inviteRows(email string) int {
	return f.count(`SELECT count(*) FROM team_invites WHERE team_id = $1 AND invitee_email = $2`, f.team, email)
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return v
}

// mailRecorder captures invitation emails (SMTP configured, sendMail stubbed).
type mailRecorder struct {
	mu sync.Mutex
	to []string
}

func recordMail(t *testing.T) *mailRecorder {
	t.Helper()
	t.Setenv("SMTP_HOST", "smtp.test")
	t.Setenv("SMTP_FROM", "paceday@test")
	m := &mailRecorder{}
	prev := sendMail
	sendMail = func(_ string, _ smtp.Auth, _ string, to []string, _ []byte) error {
		m.mu.Lock()
		m.to = append(m.to, to...)
		m.mu.Unlock()
		return nil
	}
	// Cleanups run last-in first-out: the restore is registered before
	// pac81Group's drain, so the email units are awaited before sendMail is
	// put back (no race between a unit's call and the restore).
	t.Cleanup(func() { sendMail = prev })
	pac81Group(t)
	return m
}

func (m *mailRecorder) sent(t *testing.T) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !lifecycle.Default().Wait(ctx) {
		t.Fatal("email units did not finish")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.to...)
}

// --- Adding (AC-1, AC-2, AC-4, AC-5, AC-7, AC-8, T-BOM-1, T-BOM-2) -------------------

func TestPAC129_AC1_AddIsImmediateSilentAndVisibleToEveryMember(t *testing.T) {
	f := newP129(t, nil)
	mail := recordMail(t)
	pat := p129Addr("pat")
	w := f.add(f.o, pat, "Pat")
	if w.Code != http.StatusCreated {
		t.Fatalf("add = %d %s", w.Code, w.Body.String())
	}
	body := decode[map[string]any](t, w)
	if len(body) != 6 || body["kind"] != "added" || body["name"] != "Pat" || body["userId"] != nil || body["role"] != nil || body["invitation"] != nil {
		t.Errorf("201 body = %s", w.Body.String())
	}
	for _, viewer := range []uuid.UUID{f.o, f.m} {
		n := 0
		for _, p := range f.detail(viewer).People {
			if p.Email == pat {
				n++
				if p.Kind != "added" || p.Name != "Pat" || p.UserID != nil {
					t.Errorf("entry = %+v", p)
				}
			}
		}
		if n != 1 {
			t.Errorf("viewer %v sees %d entries for Pat", viewer, n)
		}
	}
	if got := mail.sent(t); len(got) != 0 {
		t.Errorf("mail sent to %v", got)
	}
	if f.inviteRows(pat) != 0 {
		t.Error("an invitation exists")
	}
}

func TestPAC129_AC2_NoExistenceOracle(t *testing.T) {
	f := newP129(t, nil)
	a := p129Addr("held")
	createConfirmedUser(t, a)
	b := p129Addr("nobody")
	wa, wb := f.add(f.o, a, "N"), f.add(f.o, b, "N")
	if wa.Code != http.StatusCreated || wb.Code != http.StatusCreated {
		t.Fatalf("adds = %d %d", wa.Code, wb.Code)
	}
	if strings.ReplaceAll(wa.Body.String(), a, "X") != strings.ReplaceAll(wb.Body.String(), b, "X") {
		t.Errorf("answers differ beyond the address:\n%s\n%s", wa.Body.String(), wb.Body.String())
	}
	pa, pb := f.person(f.o, a), f.person(f.o, b)
	if pa == nil || pb == nil || pa.Kind != pb.Kind || pa.UserID != nil || pb.UserID != nil {
		t.Errorf("entries %+v %+v", pa, pb)
	}
}

func TestPAC129_AC4_TBOM2_RepeatAddChangesNothing(t *testing.T) {
	f := newP129(t, nil)
	pat := p129Addr("pat")
	if w := f.add(f.o, pat, "Pat"); w.Code != http.StatusCreated {
		t.Fatal(w.Body.String())
	}
	for _, again := range []string{"  " + strings.ToUpper(pat) + " ", pat + "\uFEFF", "\uFEFF" + pat} {
		w := f.add(f.o, again, "Other")
		if w.Code != http.StatusOK {
			t.Fatalf("re-add %q = %d %s, want 200", again, w.Code, w.Body.String())
		}
		if p := decode[p129Person](t, w); p.Email != pat || p.Name != "Pat" {
			t.Errorf("re-add %q answered %+v", again, p)
		}
	}
	if f.addedRows(pat) != 1 {
		t.Errorf("%d entries", f.addedRows(pat))
	}
}

func TestPAC129_AC5_TBOM1_OwnAndMemberAddressesAreRefused(t *testing.T) {
	f := newP129(t, nil)
	for _, c := range []struct{ email, want string }{
		{f.oEmail, "you cannot add your own address"},
		{"\uFEFF" + f.oEmail, "you cannot add your own address"},
		{f.oEmail + "\uFEFF", "you cannot add your own address"},
		{strings.ToUpper(f.mEmail), "this address belongs to a Paceday member of this team"},
	} {
		w := f.add(f.o, c.email, "")
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%q = %d %s", c.email, w.Code, w.Body.String())
		}
		exactJSONError(t, w, c.want)
	}
	if n := f.count(`SELECT count(*) FROM team_added_members WHERE team_id = $1`, f.team); n != 0 {
		t.Errorf("%d rows stored", n)
	}
	if len(f.detail(f.o).People) != 2 {
		t.Error("the list changed")
	}
}

func TestPAC129_AddValidationOrder(t *testing.T) {
	f := newP129(t, nil)
	for _, c := range []struct {
		body string
		code int
		want string
	}{
		{`{"email":""}`, 400, "email is required"},
		{`{"email":"   "}`, 400, "email is required"},
		{`{"email":5}`, 400, "invalid body"},
		{`not json`, 400, "invalid body"},
		{`{"email":"not-an-email"}`, 422, "email must be a valid email address"},
	} {
		w := f.do(f.o, http.MethodPost, f.teamPath("/people"), c.body)
		if w.Code != c.code {
			t.Fatalf("%s = %d %s", c.body, w.Code, w.Body.String())
		}
		exactJSONError(t, w, c.want)
	}
	w := f.do(f.o, http.MethodPost, "/api/teams/not-a-uuid/people", `{"email":"a@b.example"}`)
	if w.Code != 400 {
		t.Fatalf("bad id = %d", w.Code)
	}
	exactJSONError(t, w, "invalid team id")
	if w := f.add(f.o, "pat@localhost", ""); w.Code != http.StatusCreated {
		t.Errorf("pat@localhost = %d %s (the normaliser accepts it and it must encode)", w.Code, w.Body.String())
	}
}

func TestPAC129_AC7_NonOwnerAddIsForbidden(t *testing.T) {
	f := newP129(t, nil)
	x := p129Addr("x")
	if w := f.add(f.m, x, ""); w.Code != http.StatusForbidden {
		t.Fatalf("member add = %d", w.Code)
	}
	outsider := createConfirmedUser(t, p129Addr("out"))
	if w := f.add(outsider, x, ""); w.Code != http.StatusForbidden {
		t.Fatalf("outsider add = %d", w.Code)
	}
	if f.addedRows(x) != 0 {
		t.Error("stored")
	}
}

func TestPAC129_AC8_OldClientsSeeOnlyPacedayMembers(t *testing.T) {
	f := newP129(t, nil)
	f.add(f.o, p129Addr("x"), "X")
	d := f.detail(f.m)
	if len(d.Members) != 2 || len(d.People) != 3 {
		t.Fatalf("members %d people %d", len(d.Members), len(d.People))
	}
	for _, m := range d.Members {
		keys := []string{}
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if strings.Join(keys, ",") != "email,joinedAt,name,role,teamId,userId" {
			t.Errorf("member keys = %v", keys)
		}
		if m["email"] != f.oEmail && m["email"] != f.mEmail {
			t.Errorf("unexpected member %v", m)
		}
	}
	if d.People[0].Kind != "paceday" || d.People[2].Kind != "added" {
		t.Errorf("people order = %+v", d.People)
	}
}

// AC-6 (SF-039): a failed member read is the 500, never an empty list.
func TestPAC129_AC6_FailedMemberReadIs500(t *testing.T) {
	f := newP129(t, nil)
	f.add(f.o, p129Addr("x"), "")
	// The fault hits exactly one of the two reads: the Paceday members (the
	// first), then separately the added people (the second).
	for _, nth := range []int32{1, 2} {
		var calls atomic.Int32
		uninstall := storage.InstallTeamMembersListRead(t, func(uuid.UUID) error {
			if calls.Add(1) == nth {
				return errors.New("injected")
			}
			return nil
		})
		if w := f.do(f.m, http.MethodGet, f.teamPath(""), ""); w.Code != http.StatusInternalServerError {
			t.Errorf("read %d failed: getTeam = %d %s, want 500", nth, w.Code, w.Body.String())
		}
		uninstall()
	}
}

// --- An added person gets nothing (AC-9) and Find a time (AC-10, AC-11, AC-13, AC-29) -

func pac129Token(t *testing.T, user uuid.UUID, access string, expiry time.Time) {
	t.Helper()
	if err := auth.UpsertUserToken(openTestDB(t), user, &oauth2.Token{AccessToken: access, RefreshToken: "refresh-" + access, Expiry: expiry}); err != nil {
		t.Fatal(err)
	}
}

type p129Slot map[string]any

func (f *p129) slots(user uuid.UUID) []p129Slot {
	f.t.Helper()
	w := f.do(user, http.MethodGet, f.teamPath("/availability?date=2030-01-07&duration=60"), "")
	if w.Code != http.StatusOK {
		f.t.Fatalf("availability = %d %s", w.Code, w.Body.String())
	}
	return decode[struct {
		Slots []p129Slot `json:"slots"`
	}](f.t, w).Slots
}

func slotAt(slots []p129Slot, hhmm string) p129Slot {
	for _, s := range slots {
		if strings.HasPrefix(s["start"].(string), "2030-01-07T"+hhmm) {
			return s
		}
	}
	return nil
}

func TestPAC129_AC10_AC11_AC29_FindATimeCountsAndTheOwnersCredential(t *testing.T) {
	g := newPAC129Google(t)
	f := newP129(t, nil)
	far := time.Now().Add(24 * time.Hour)
	pac129Token(t, f.o, "tok-O", far)
	pac129Token(t, f.m, "tok-M", far)
	m2 := createConfirmedUser(t, p129Addr("m2")) // no calendar connected
	if err := storage.AddTeamMember(openTestDB(t), f.team, m2, "member"); err != nil {
		t.Fatal(err)
	}
	x, y, z := p129Addr("x"), p129Addr("y"), "z-"+uuid.NewString()[:8]+"@gmail.com"
	g.busy(x, [2]string{"2030-01-07T10:00:00Z", "2030-01-07T11:00:00Z"})
	// y is not in the table: Google answers notFound (PAC-133: unknown, AC-11).
	for _, a := range []string{x, y, z} {
		if w := f.add(f.o, a, ""); w.Code != http.StatusCreated {
			t.Fatal(w.Body.String())
		}
	}

	slots := f.slots(f.m)
	tenTo11 := [2]time.Time{pac129Day.Add(10 * time.Hour), pac129Day.Add(11 * time.Hour)}
	for _, s := range slots {
		st, _ := time.Parse(time.RFC3339, s["start"].(string))
		en, _ := time.Parse(time.RFC3339, s["end"].(string))
		if st.Before(tenTo11[1]) && en.After(tenTo11[0]) {
			t.Errorf("slot %v overlaps X's 10:00-11:00", s)
		}
		if len(s) != 6 {
			t.Errorf("slot keys = %v: slots and counts only (AC-29)", s)
		}
	}
	s14 := slotAt(slots, "14:00")
	if s14 == nil || s14["free_count"] != 3.0 || s14["unknown_count"] != 3.0 || s14["total_count"] != 6.0 {
		t.Fatalf("14:00 = %v, want 3 free, 3 unknown of 6", s14)
	}
	// AC-29: X and Y are read with O's token, never M's; Z (personal) never.
	fb := g.freeBusyCalls()
	if len(fb) != 1 || fb[0].bearer != "tok-O" {
		t.Fatalf("free/busy calls = %+v, want one with O's token", fb)
	}
	sort.Strings(fb[0].ids)
	want := []string{x, y}
	sort.Strings(want)
	if fmt.Sprint(fb[0].ids) != fmt.Sprint(want) {
		t.Errorf("ids = %v, want %v", fb[0].ids, want)
	}
}

// PAC-133 D-11 for added people (contract §3.5): Google answers Y through
// O's credential with an error AND a 13:00-14:00 window. Y is unknown, and
// the reported window still blocks: no offered slot overlaps 13:00-14:00.
func TestPAC129_D11_UnknownAddedPersonsReportedWindowsBlock(t *testing.T) {
	g := newPAC129Google(t)
	f := newP129(t, nil)
	pac129Token(t, f.o, "tok-O", time.Now().Add(24*time.Hour))
	pac129Token(t, f.m, "tok-M", time.Now().Add(24*time.Hour))
	y := p129Addr("y")
	g.mu.Lock()
	g.table[y] = `{"busy":[{"start":"2030-01-07T13:00:00Z","end":"2030-01-07T14:00:00Z"}],"errors":[{"domain":"global","reason":"internalError"}]}`
	g.mu.Unlock()
	if w := f.add(f.o, y, "Y"); w.Code != http.StatusCreated {
		t.Fatal(w.Body.String())
	}
	slots := f.slots(f.m)
	if len(slots) == 0 {
		t.Fatal("no slots")
	}
	from, to := pac129Day.Add(13*time.Hour), pac129Day.Add(14*time.Hour)
	for _, s := range slots {
		st, _ := time.Parse(time.RFC3339, s["start"].(string))
		en, _ := time.Parse(time.RFC3339, s["end"].(string))
		if st.Before(to) && en.After(from) {
			t.Errorf("slot %v overlaps Y's reported 13:00-14:00", s)
		}
		if s["unknown_count"] != 1.0 || s["free_count"] != 2.0 || s["total_count"] != 3.0 {
			t.Errorf("slot %v: want Y unknown (1 of 3), O and M free", s)
		}
	}
	if fb := g.freeBusyCalls(); len(fb) != 1 || fb[0].bearer != "tok-O" {
		t.Errorf("free/busy calls %+v, want one with O's token", fb)
	}
}

func TestPAC129_AC29_OwnersRefusedRenewalIsTheOwnersLoss(t *testing.T) {
	newPAC129Google(t)
	d := oauthfake.New()
	t.Cleanup(d.Close)
	cfg := &oauth2.Config{ClientID: "g", Endpoint: oauth2.Endpoint{TokenURL: d.TokenURL()}}
	f := newP129(t, cfg)
	pac129Token(t, f.o, "tok-O", time.Now().Add(-time.Hour)) // expired: renewed on use
	d.Answer("refresh-tok-O", oauthfake.InvalidGrant)
	pac129Token(t, f.m, "tok-M", time.Now().Add(24*time.Hour))
	f.add(f.o, p129Addr("x"), "")
	slots := f.slots(f.m)
	if len(slots) == 0 {
		t.Fatal("no slots")
	}
	db := openTestDB(t)
	if lost, _ := storage.IsCredentialLost(db, f.o, storage.CredentialGoogle); !lost {
		t.Error("O's refused renewal is not recorded as O's loss")
	}
	if lost, _ := storage.IsCredentialLost(db, f.m, storage.CredentialGoogle); lost {
		t.Error("M's credential was recorded as lost")
	}
}

// AC-13: O's calendar disconnected before any read: X is unknown and no other
// member's credential reads X.
func TestPAC129_AC13_OwnerDisconnectedMakesAddedPeopleUnknown(t *testing.T) {
	g := newPAC129Google(t)
	f := newP129(t, nil)
	pac129Token(t, f.m, "tok-M", time.Now().Add(24*time.Hour))
	x := p129Addr("x")
	g.busy(x)
	f.add(f.o, x, "")
	s := slotAt(f.slots(f.m), "14:00")
	if s == nil || s["unknown_count"] != 2.0 || s["total_count"] != 3.0 {
		t.Fatalf("14:00 = %v, want O and X unknown", s)
	}
	if fb := g.freeBusyCalls(); len(fb) != 0 {
		t.Errorf("free/busy calls %+v, want none (no other credential reads X)", fb)
	}
}

func TestPAC129_AC9_AnAddedAccountGetsNothingAndGivesNothing(t *testing.T) {
	g := newPAC129Google(t)
	f := newP129(t, nil)
	pac129Token(t, f.o, "tok-O", time.Now().Add(24*time.Hour))
	uEmail := p129Addr("u")
	u := createConfirmedUser(t, uEmail)
	pac129Token(t, u, "tok-U", time.Now().Add(24*time.Hour))
	db := openTestDB(t)
	if err := storage.SaveFocusBlock(db, u, "pac129-u-"+uuid.NewString(), "2030-01-07", pac129Day.Add(14*time.Hour), pac129Day.Add(15*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.UpsertAnalyticsWeek(db, &storage.AnalyticsWeek{UserID: u, WeekStart: pac129Day, FocusMinutes: 300, MeetingMinutes: 50}); err != nil {
		t.Fatal(err)
	}
	g.busy(uEmail) // free through O's credential
	if w := f.add(f.o, uEmail, "U"); w.Code != http.StatusCreated {
		t.Fatal(w.Body.String())
	}
	teams := decode[[]map[string]any](t, f.do(u, http.MethodGet, "/api/teams", ""))
	for _, tm := range teams {
		if tm["id"] == f.team.String() {
			t.Error("U's team list includes T")
		}
	}
	for _, p := range []string{"", "/availability?date=2030-01-07", "/analytics?date=2030-01-07"} {
		if w := f.do(u, http.MethodGet, f.teamPath(p), ""); w.Code != http.StatusForbidden {
			t.Errorf("U GET %q = %d, want 403", p, w.Code)
		}
	}
	if slotAt(f.slots(f.o), "14:00") == nil {
		t.Error("the slot U's focus block covers is not offered")
	}
	an := decode[p129Analytics](t, f.do(f.o, http.MethodGet, f.teamPath("/analytics?date=2030-01-07"), ""))
	row := an.row(uEmail)
	if row == nil || row.Kind != "added" || row.FocusMinutes != nil || row.FocusScore != nil {
		t.Errorf("U's analytics row = %+v, want added with no focus figure", row)
	}
	if g.sawBearer("tok-U") {
		t.Error("the stand-in saw U's token")
	}
}

// --- Removing (AC-15, AC-16b, AC-37) ------------------------------------------------

func TestPAC129_AC15_RemovalDeletesTheAddressFromTheTeam(t *testing.T) {
	f := newP129(t, nil)
	recordMail(t)
	x := p129Addr("x")
	w := f.invite(x, "X")
	if w.Code != http.StatusCreated {
		t.Fatal(w.Body.String())
	}
	token := decode[struct {
		Invitation struct {
			Token string `json:"token"`
		} `json:"invitation"`
	}](t, w).Invitation.Token
	db := openTestDB(t)
	if _, err := db.Exec(`INSERT INTO team_invites (team_id, invitee_email, invited_by, token, status, expires_at) VALUES ($1, $2, $3, $4, 'expired', now() - interval '1 day')`, f.team, x, f.o, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if w := f.remove(f.o, strings.ToUpper(x)); w.Code != http.StatusNoContent {
		t.Fatalf("remove = %d %s", w.Code, w.Body.String())
	}
	if f.addedRows(x)+f.inviteRows(x) != 0 {
		t.Error("rows of X remain on T")
	}
	holder := createConfirmedUser(t, x)
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		path := "/api/teams/invites/" + token
		if m == http.MethodPost {
			path += "/accept"
		}
		if w := f.do(holder, m, path, ""); w.Code != http.StatusNotFound {
			t.Errorf("%s the removed invitation = %d, want 404", m, w.Code)
		}
	}
	w = f.remove(f.o, x)
	if w.Code != http.StatusNotFound {
		t.Fatalf("second remove = %d", w.Code)
	}
	exactJSONError(t, w, "person not found")
}

func TestPAC129_RemoveRules(t *testing.T) {
	f := newP129(t, nil)
	x := p129Addr("x")
	f.add(f.o, x, "")
	if w := f.remove(f.m, x); w.Code != http.StatusForbidden {
		t.Errorf("member removes X = %d, want 403", w.Code)
	}
	if w := f.remove(f.o, "nope"); w.Code != http.StatusBadRequest {
		t.Errorf("bad address = %d", w.Code)
	} else {
		exactJSONError(t, w, "invalid email")
	}
	w := f.remove(f.o, f.oEmail)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("last owner = %d", w.Code)
	}
	exactJSONError(t, w, "the team owner cannot be removed")
	if w := f.remove(f.m, f.mEmail); w.Code != http.StatusNoContent {
		t.Errorf("member removes self = %d", w.Code)
	}
}

func seedRoster(t *testing.T, manager, team uuid.UUID, email string) {
	t.Helper()
	db := openTestDB(t)
	if _, err := db.Exec(`INSERT INTO manager_team_members (manager_user_id, member_email, display_name, source) VALUES ($1, $2, 'R', 'manual') ON CONFLICT DO NOTHING`, manager, email); err != nil {
		t.Fatal(err)
	}
	if team != uuid.Nil {
		if _, err := db.Exec(`INSERT INTO manager_team_member_assignments (manager_user_id, team_id, member_email) VALUES ($1, $2, $3)`, manager, team, email); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO one_on_one_occurrences (manager_user_id, member_email, calendar_event_id, occurred_at) VALUES ($1, $2, $3, now())`, manager, email, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
}

func (f *p129) rosterRows(email string) (global, assigned, history int) {
	return f.count(`SELECT count(*) FROM manager_team_members WHERE manager_user_id = $1 AND member_email = $2`, f.o, email),
		f.count(`SELECT count(*) FROM manager_team_member_assignments WHERE manager_user_id = $1 AND team_id = $2 AND member_email = $3`, f.o, f.team, email),
		f.count(`SELECT count(*) FROM one_on_one_occurrences WHERE manager_user_id = $1 AND member_email = $2`, f.o, email)
}

func TestPAC129_AC16b_ManagerRemovalUnderTheLock(t *testing.T) {
	f := newP129(t, nil)
	recordMail(t)
	x := p129Addr("x")
	f.invite(x, "X")
	seedRoster(t, f.o, f.team, x)
	// P3-m1: a mixed-case, BOM-carrying path still matches the added entry.
	path := "/api/manager/team/members/" + url.PathEscape("\uFEFF"+strings.ToUpper(x)) + "?team_id=" + f.team.String()
	if w := f.do(f.o, http.MethodDelete, path, ""); w.Code != http.StatusNoContent {
		t.Fatalf("manager remove = %d %s", w.Code, w.Body.String())
	}
	if f.addedRows(x)+f.inviteRows(x) != 0 {
		t.Error("the added entry or an invitation remains")
	}
	if g, a, h := f.rosterRows(x); g != 1 || a != 0 || h != 1 {
		t.Errorf("roster %d assignment %d history %d, want 1 0 1", g, a, h)
	}
	if w := f.do(f.m, http.MethodDelete, path, ""); w.Code != http.StatusForbidden {
		t.Errorf("non-owner manager removal = %d, want 403", w.Code)
	}
}

func TestPAC129_AC37_RosterAndTeamAreSeparate(t *testing.T) {
	f := newP129(t, nil)
	pat := p129Addr("pat")
	f.add(f.o, pat, "Pat")
	seedRoster(t, f.o, f.team, pat)
	if w := f.remove(f.o, pat); w.Code != http.StatusNoContent {
		t.Fatal(w.Body.String())
	}
	if g, a, _ := f.rosterRows(pat); g != 1 || a != 1 {
		t.Errorf("team removal touched the roster: %d %d", g, a)
	}
	f.add(f.o, pat, "Pat")
	if w := f.do(f.o, http.MethodDelete, "/api/manager/team/members/"+url.PathEscape(pat), ""); w.Code != http.StatusNoContent {
		t.Fatal(w.Body.String())
	}
	if f.addedRows(pat) != 1 {
		t.Error("the global roster delete removed Pat from T")
	}
}

// --- Analytics (AC-16, AC-17) --------------------------------------------------------

type p129Row struct {
	Email          string     `json:"email"`
	Name           string     `json:"name"`
	Kind           string     `json:"kind"`
	UserID         *uuid.UUID `json:"userId"`
	Figures        string     `json:"figures"`
	MeetingMinutes *int       `json:"meeting_minutes"`
	FocusMinutes   *int       `json:"focus_minutes"`
	FocusScore     *int       `json:"focus_score"`
}

type p129Analytics struct {
	WeekStart         string           `json:"week_start"`
	AvgMeetingMinutes int              `json:"avg_meeting_minutes"`
	AvgFocusMinutes   int              `json:"avg_focus_minutes"`
	MemberBreakdown   []map[string]any `json:"member_breakdown"`
	People            []p129Row        `json:"people"`
}

func (a p129Analytics) row(email string) *p129Row {
	for _, r := range a.People {
		if r.Email == email {
			rr := r
			return &rr
		}
	}
	return nil
}

func TestPAC129_AC16_AC17_AnalyticsRows(t *testing.T) {
	g := newPAC129Google(t)
	f := newP129(t, nil)
	pac129Token(t, f.o, "tok-O", time.Now().Add(24*time.Hour))
	db := openTestDB(t)
	week := time.Date(2029, 12, 31, 0, 0, 0, 0, time.UTC) // a past Monday
	mustWeek := func(u uuid.UUID, meeting, focus int) {
		if _, err := storage.UpsertAnalyticsWeek(db, &storage.AnalyticsWeek{UserID: u, WeekStart: week, MeetingMinutes: meeting, FocusMinutes: focus, FocusScore: 70}); err != nil {
			t.Fatal(err)
		}
	}
	mustWeek(f.o, 600, 100)
	if _, err := db.Exec(`UPDATE users SET name = 'Alex' WHERE id = $1`, f.m); err != nil {
		t.Fatal(err)
	}
	mustWeek(f.m, 120, 240) // A: Paceday "Alex"
	p2 := createConfirmedUser(t, p129Addr("p2"))
	if err := storage.AddTeamMember(db, f.team, p2, "member"); err != nil {
		t.Fatal(err)
	}
	b := p129Addr("b")
	g.busy(b, [2]string{"2030-01-02T09:00:00Z", "2030-01-02T14:00:00Z"}) // 300 minutes in the week
	f.add(f.o, b, "Alex")

	ownerView := decode[p129Analytics](t, f.do(f.o, http.MethodGet, f.teamPath("/analytics?date=2030-01-03"), ""))
	if ownerView.WeekStart != "2029-12-31" {
		t.Errorf("week_start = %q", ownerView.WeekStart)
	}
	a, x, pp := ownerView.row(f.mEmail), ownerView.row(b), ownerView.row(strings.ToLower(storageEmail(t, p2)))
	if a == nil || a.Figures != "measured" || *a.MeetingMinutes != 120 || *a.FocusMinutes != 240 || a.Kind != "paceday" {
		t.Errorf("A = %+v", a)
	}
	if x == nil || x.Figures != "measured" || x.MeetingMinutes == nil || *x.MeetingMinutes != 300 || x.FocusMinutes != nil || x.FocusScore != nil || x.Kind != "added" {
		t.Errorf("B (added, availability only) = %+v", x)
	}
	if pp == nil || pp.Figures != "no_data" || pp.MeetingMinutes != nil {
		t.Errorf("P2 = %+v, want no_data", pp)
	}
	// Averages over the measured Paceday members (O and A), never B or P2.
	if ownerView.AvgMeetingMinutes != (600+120)/2 || ownerView.AvgFocusMinutes != (100+240)/2 {
		t.Errorf("averages %d %d", ownerView.AvgMeetingMinutes, ownerView.AvgFocusMinutes)
	}
	if len(ownerView.MemberBreakdown) != 3 {
		t.Errorf("member_breakdown = %v, want the 3 Paceday members", ownerView.MemberBreakdown)
	}
	for _, m := range ownerView.MemberBreakdown {
		if _, ok := m["free_minutes"]; ok {
			t.Error("a row carries free time")
		}
	}

	calls := len(g.freeBusyCalls())
	p2View := decode[p129Analytics](t, f.do(p2, http.MethodGet, f.teamPath("/analytics?date=2030-01-03"), ""))
	if r := p2View.row(b); r == nil || r.Figures != "withheld" || r.MeetingMinutes != nil || r.Name != "Alex" {
		t.Errorf("non-owner sees B as %+v, want withheld", r)
	}
	if len(g.freeBusyCalls()) != calls {
		t.Error("a non-owner's analytics read B's free/busy")
	}
	if n := f.count(`SELECT count(*) FROM team_analytics_cache WHERE member_email = $1`, b) + f.count(`SELECT count(*) FROM analytics_weeks aw JOIN users u ON u.id = aw.user_id WHERE lower(u.email) = $1`, b); n != 0 {
		t.Error("a figure for B was stored")
	}
}

func storageEmail(t *testing.T, u uuid.UUID) string {
	t.Helper()
	var e string
	if err := openTestDB(t).QueryRow(`SELECT email FROM users WHERE id = $1`, u).Scan(&e); err != nil {
		t.Fatal(err)
	}
	return e
}

// --- Manager mode (AC-31) -------------------------------------------------------------

func TestPAC129_AC31_ManagerModeReadOnlyAddedPeople(t *testing.T) {
	f := newP129(t, nil)
	x := p129Addr("x")
	createConfirmedUser(t, x) // any account lookup would find one
	f.add(f.o, x, "X")
	q := "?team_id=" + f.team.String()
	roster := func(flag string) map[string]map[string]any {
		w := f.do(f.o, http.MethodGet, "/api/manager/team"+q+flag, "")
		if w.Code != http.StatusOK {
			t.Fatalf("roster = %d %s", w.Code, w.Body.String())
		}
		out := map[string]map[string]any{}
		for _, m := range decode[struct {
			Members []map[string]any `json:"members"`
		}](t, w).Members {
			out[m["email"].(string)] = m
		}
		return out
	}
	if _, ok := roster("")[x]; ok {
		t.Error("without include_added the roster carries X")
	}
	if e := roster("&include_added=true")[x]; e == nil || e["source"] != "added" || e["is_paceday_user"] != false || e["cadence"] != "none" {
		t.Errorf("X in the roster = %v", e)
	}
	path := "/api/manager/team/members/" + url.PathEscape(x) + q
	// P3-m1: the {email} path in another case, or with a byte-order mark at
	// either end, names the same added person.
	for _, variant := range []string{x, strings.ToUpper(x), "\uFEFF" + x, x + "\uFEFF"} {
		seg := url.PathEscape(variant)
		for _, w := range []*httptest.ResponseRecorder{
			f.do(f.o, http.MethodPatch, "/api/manager/team/members/"+seg+q, `{"cadence":"weekly"}`),
			f.do(f.o, http.MethodPost, "/api/manager/team/members/"+seg+"/schedule"+q, ``),
		} {
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("patch/schedule %q = %d %s", variant, w.Code, w.Body.String())
			}
			exactJSONError(t, w, "this person was added to the team without Paceday access; add them with Add person to track 1:1s")
		}
	}
	if n := f.count(`SELECT count(*) FROM manager_team_members WHERE manager_user_id = $1`, f.o) + f.count(`SELECT count(*) FROM manager_team_member_assignments WHERE manager_user_id = $1`, f.o); n != 0 {
		t.Errorf("%d roster rows or assignments written by the refused calls", n)
	}
	if g, a, _ := f.rosterRows(x); g != 0 || a != 0 {
		t.Errorf("a roster row (%d) or assignment (%d) was written", g, a)
	}
	an := f.do(f.o, http.MethodGet, "/api/manager/analytics"+q+"&include_added=true", "")
	var found bool
	for _, m := range decode[struct {
		Members []map[string]any `json:"members"`
	}](t, an).Members {
		if m["email"] == x {
			found = true
			if m["is_paceday_user"] != false || m["data_available"] != false {
				t.Errorf("analytics X = %v", m)
			}
		}
	}
	if !found {
		t.Error("X missing from Manager analytics with include_added")
	}
	if n := f.count(`SELECT count(*) FROM team_analytics_cache WHERE member_email = $1`, x); n != 0 {
		t.Error("a Manager figure for X was stored")
	}

	// Manager "Add person": a roster entry with today's behaviour; X stays added.
	if w := f.do(f.o, http.MethodPost, "/api/manager/team/members"+q, `{"email":"`+x+`","cadence":"weekly"}`); w.Code/100 != 2 {
		t.Fatalf("manager add = %d %s", w.Code, w.Body.String())
	}
	if w := f.do(f.o, http.MethodPatch, path, `{"cadence":"monthly"}`); w.Code != http.StatusOK {
		t.Errorf("patch after Add person = %d %s", w.Code, w.Body.String())
	}
	if f.addedRows(x) != 1 {
		t.Error("X is no longer an added person")
	}

	// Detection confirm: an assignment with today's behaviour; X2 stays added.
	x2 := p129Addr("x2")
	f.add(f.o, x2, "")
	seedRoster(t, f.o, uuid.Nil, x2)
	if w := f.do(f.o, http.MethodPost, "/api/manager/detect/confirm"+q, `{"emails":["`+x2+`"]}`); w.Code != http.StatusOK {
		t.Fatalf("confirm = %d %s", w.Code, w.Body.String())
	}
	if _, a, _ := f.rosterRows(x2); a != 1 || f.addedRows(x2) != 1 {
		t.Errorf("confirm: assignment %d added %d", a, f.addedRows(x2))
	}

	// Pre-existing assignment and cached figure are kept by an add.
	x3 := p129Addr("x3")
	seedRoster(t, f.o, f.team, x3)
	if err := storage.UpsertTeamAnalytics(openTestDB(t), &storage.TeamAnalyticsRow{ManagerUserID: f.o, MemberEmail: x3, WeekStart: pac129Day, MeetingMinutes: 42, DataAvailable: true}); err != nil {
		t.Fatal(err)
	}
	f.add(f.o, x3, "")
	if _, a, _ := f.rosterRows(x3); a != 1 || f.count(`SELECT count(*) FROM team_analytics_cache WHERE member_email = $1 AND meeting_minutes = 42`, x3) != 1 {
		t.Error("pre-existing Manager data changed")
	}
}

// --- Invitations (AC-18, AC-19, AC-19b, AC-20, AC-22, T-BOM-3) --------------------------

type p129InviteResult struct {
	Outcome    string `json:"outcome"`
	Invitation *struct {
		ID     uuid.UUID `json:"id"`
		Token  string    `json:"token"`
		Status string    `json:"status"`
	} `json:"invitation"`
	Person p129Person `json:"person"`
}

func TestPAC129_AC18_InvitingANewAddressAddsIt(t *testing.T) {
	f := newP129(t, nil)
	mail := recordMail(t)
	sam := p129Addr("sam")
	w := f.invite(sam, " Sam ")
	if w.Code != http.StatusCreated {
		t.Fatalf("invite = %d %s", w.Code, w.Body.String())
	}
	res := decode[p129InviteResult](t, w)
	if res.Outcome != "added_and_invited" || res.Invitation == nil || res.Invitation.Token == "" || res.Person.Kind != "added" || res.Person.Name != "Sam" || res.Person.Invitation == nil {
		t.Errorf("result = %s", w.Body.String())
	}
	if p := f.person(f.o, sam); p == nil || p.Name != "Sam" || p.Invitation == nil {
		t.Errorf("owner's view = %+v", p)
	}
	if p := f.person(f.m, sam); p == nil || p.Invitation != nil {
		t.Errorf("member's view = %+v, want the invitation hidden (m-6)", p)
	}
	if got := mail.sent(t); len(got) != 1 || got[0] != sam {
		t.Errorf("mail = %v", got)
	}
	// An invitation to an already-added person is "invited".
	x := p129Addr("x")
	f.add(f.o, x, "X")
	if r := decode[p129InviteResult](t, f.invite(x, "Ignored")); r.Outcome != "invited" || r.Person.Name != "X" {
		t.Errorf("invite an added person = %+v", r)
	}
}

func TestPAC129_AC20_TBOM3_InviteOutcomes(t *testing.T) {
	f := newP129(t, nil)
	mail := recordMail(t)
	db := openTestDB(t)
	sam, lee := p129Addr("sam"), p129Addr("lee")
	if _, err := storage.CreateTeamInvite(db, f.team, sam, f.o); err != nil {
		t.Fatal(err)
	}
	var lapsed uuid.UUID
	if err := db.QueryRow(`INSERT INTO team_invites (team_id, invitee_email, invited_by, token, status, expires_at) VALUES ($1, $2, $3, $4, 'pending', now() - interval '1 hour') RETURNING id`, f.team, lee, f.o, uuid.NewString()).Scan(&lapsed); err != nil {
		t.Fatal(err)
	}

	w := f.invite(strings.ToUpper(sam[:1])+sam[1:], "")
	if r := decode[p129InviteResult](t, w); w.Code != http.StatusOK || r.Outcome != "already_pending" || r.Invitation == nil {
		t.Errorf("Sam = %d %s", w.Code, w.Body.String())
	}
	if f.inviteRows(sam) != 1 {
		t.Error("a second invitation for Sam")
	}
	w = f.invite(lee, "")
	if r := decode[p129InviteResult](t, w); w.Code != http.StatusCreated || r.Outcome != "added_and_invited" {
		t.Errorf("Lee = %d %s", w.Code, w.Body.String())
	}
	var st string
	_ = db.QueryRow(`SELECT status FROM team_invites WHERE id = $1`, lapsed).Scan(&st)
	if st != "expired" || f.count(`SELECT count(*) FROM team_invites WHERE team_id = $1 AND invitee_email = $2 AND status = 'pending'`, f.team, lee) != 1 {
		t.Errorf("lapsed row %q; want it expired and exactly one pending for Lee", st)
	}
	for _, addr := range []string{f.mEmail, "\uFEFF" + f.mEmail, f.mEmail + "\uFEFF"} {
		w = f.invite(addr, "")
		r := decode[p129InviteResult](t, w)
		if w.Code != http.StatusOK || r.Outcome != "already_member" || r.Invitation != nil || r.Person.Kind != "paceday" {
			t.Errorf("M %q = %d %s", addr, w.Code, w.Body.String())
		}
	}
	if f.inviteRows(f.mEmail)+f.addedRows(f.mEmail) != 0 {
		t.Error("an invitation or entry was written for M")
	}
	if got := mail.sent(t); len(got) != 1 || got[0] != lee {
		t.Errorf("mail = %v, want Lee only", got)
	}
}

func TestPAC129_InviteRefusals(t *testing.T) {
	f := newP129(t, nil)
	w := f.do(f.m, http.MethodPost, f.teamPath("/members/invite"), `{"email":"a@corp.example"}`)
	if w.Code != http.StatusForbidden {
		t.Errorf("non-owner invite = %d", w.Code)
	}
	if w := f.do(f.o, http.MethodPost, f.teamPath("/members/invite"), `{"email":""}`); w.Code != 400 {
		t.Errorf("blank = %d", w.Code)
	}
	if w := f.do(f.o, http.MethodPost, f.teamPath("/members/invite"), `{"email":"nope"}`); w.Code != 422 {
		t.Errorf("invalid = %d", w.Code)
	}
}

func (f *p129) fill(n int) {
	f.t.Helper()
	have := f.count(`SELECT count(*) FROM team_members WHERE team_id = $1`, f.team) + f.count(`SELECT count(*) FROM team_added_members WHERE team_id = $1`, f.team)
	if _, err := openTestDB(f.t).Exec(`INSERT INTO team_added_members (team_id, email, added_by)
		SELECT $1, 'fill' || g || '-' || $3 || '@ext.example', $2 FROM generate_series(1, $4) g`, f.team, f.o, uuid.NewString()[:8], n-have); err != nil {
		f.t.Fatal(err)
	}
}

func (f *p129) accept(user uuid.UUID, token string) *httptest.ResponseRecorder {
	return f.do(user, http.MethodPost, "/api/teams/invites/"+token+"/accept", "")
}

func TestPAC129_AC19_AcceptanceUpgradesTheEntry(t *testing.T) {
	f := newP129(t, nil)
	recordMail(t)
	x := p129Addr("x")
	res := decode[p129InviteResult](t, f.invite(x, "X"))
	holder := createConfirmedUser(t, x)
	storage.InstallAfterAcceptMembershipStep(t, func(uuid.UUID) error { return errors.New("injected") })
	if w := f.accept(holder, res.Invitation.Token); w.Code != http.StatusInternalServerError {
		t.Fatalf("faulted accept = %d", w.Code)
	}
	if p := f.person(f.o, x); p == nil || p.Kind != "added" || p.Invitation == nil {
		t.Fatalf("after the fault X = %+v, want still added with a pending invitation", p)
	}
	storage.InstallAfterAcceptMembershipStep(t, func(uuid.UUID) error { return nil })
	if w := f.accept(holder, res.Invitation.Token); w.Code != http.StatusNoContent {
		t.Fatalf("accept = %d %s", w.Code, w.Body.String())
	}
	n := 0
	for _, p := range f.detail(f.o).People {
		if p.Email == x {
			n++
			if p.Kind != "paceday" || p.UserID == nil || *p.UserID != holder {
				t.Errorf("X = %+v", p)
			}
		}
	}
	if n != 1 {
		t.Errorf("%d entries for X", n)
	}
	found := false
	for _, tm := range decode[[]map[string]any](t, f.do(holder, http.MethodGet, "/api/teams", "")) {
		found = found || tm["id"] == f.team.String()
	}
	if !found {
		t.Error("X's team list excludes T")
	}
}

func TestPAC129_AC19b_LegacyAcceptanceIsCapped(t *testing.T) {
	f := newP129(t, nil)
	recordMail(t)
	db := openTestDB(t)
	legacy := func() (string, uuid.UUID) {
		x := p129Addr("legacy")
		inv, err := storage.CreateTeamInvite(db, f.team, x, f.o)
		if err != nil {
			t.Fatal(err)
		}
		return inv.Token, createConfirmedUser(t, x)
	}
	f.fill(49)
	tok, u := legacy()
	if w := f.accept(u, tok); w.Code != http.StatusNoContent {
		t.Fatalf("at 49 = %d %s", w.Code, w.Body.String())
	}
	tok, u = legacy() // the team is now at 50
	w := f.accept(u, tok)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("at 50 = %d %s", w.Code, w.Body.String())
	}
	exactJSONError(t, w, "this team is full; ask its owner to remove someone before you accept")
	if f.count(`SELECT count(*) FROM team_members WHERE team_id = $1 AND user_id = $2`, f.team, u) != 0 || f.count(`SELECT count(*) FROM team_invites WHERE token = $1 AND status = 'pending'`, tok) != 1 {
		t.Error("the refused acceptance changed something")
	}
	// An added person is upgraded at 50.
	if _, err := db.Exec(`DELETE FROM team_added_members WHERE team_id = $1 AND email LIKE 'fill1-%'`, f.team); err != nil {
		t.Fatal(err)
	}
	x := p129Addr("x")
	res := decode[p129InviteResult](t, f.invite(x, ""))
	if res.Invitation == nil {
		t.Fatalf("invite = %+v", res)
	}
	if w := f.accept(createConfirmedUser(t, x), res.Invitation.Token); w.Code != http.StatusNoContent {
		t.Errorf("upgrade at 50 = %d %s", w.Code, w.Body.String())
	}
}

func TestPAC129_AC22_UnconfirmedHolderIsRefused(t *testing.T) {
	f := newP129(t, nil)
	recordMail(t)
	x := p129Addr("x")
	res := decode[p129InviteResult](t, f.invite(x, ""))
	unconfirmed := createTestUser(t, x)
	if w := f.accept(unconfirmed, res.Invitation.Token); w.Code != http.StatusForbidden {
		t.Fatalf("unconfirmed accept = %d", w.Code)
	}
	if p := f.person(f.o, x); p == nil || p.Kind != "added" {
		t.Errorf("X = %+v, want still added", p)
	}
}

// --- Limits (AC-23b) and session (AC-25) ------------------------------------------------

func TestPAC129_AC23b_GrandfatheredTeamCanOnlyRemove(t *testing.T) {
	f := newP129(t, nil)
	f.fill(52)
	w := f.add(f.o, p129Addr("new"), "")
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("add at 52 = %d", w.Code)
	}
	exactJSONError(t, w, "this team has reached its limit of 50 members; remove someone before adding another")
	rows, err := openTestDB(t).Query(`SELECT email FROM team_added_members WHERE team_id = $1 LIMIT 3`, f.team)
	if err != nil {
		t.Fatal(err)
	}
	var three []string
	for rows.Next() {
		var e string
		_ = rows.Scan(&e)
		three = append(three, e)
	}
	rows.Close()
	for _, e := range three {
		if w := f.remove(f.o, e); w.Code != http.StatusNoContent {
			t.Fatalf("remove = %d", w.Code)
		}
	}
	if w := f.add(f.o, p129Addr("new"), ""); w.Code != http.StatusCreated {
		t.Errorf("add at 49 = %d %s", w.Code, w.Body.String())
	}
}

func TestPAC129_AC25_AStaleSessionWritesNothing(t *testing.T) {
	f := newP129(t, nil)
	recordMail(t)
	x := p129Addr("x")
	f.add(f.o, x, "")
	y := p129Addr("y")
	res := decode[p129InviteResult](t, f.invite(y, ""))
	holder := createConfirmedUser(t, y)
	advance := func(u uuid.UUID) {
		afterAuthHook = func(*http.Request) {
			afterAuthHook = nil
			if _, err := openTestDB(t).Exec(`UPDATE users SET session_generation = session_generation + 1 WHERE id = $1`, u); err != nil {
				t.Error(err)
			}
		}
	}
	t.Cleanup(func() { afterAuthHook = nil })
	gen := int64(0)
	stale := func(u uuid.UUID, method, path, body string) {
		t.Helper()
		advance(u)
		assert401(t, doAs(t, f.r, method, path, body, sessionCookie(t, u, gen)))
	}
	newAddr := p129Addr("new")
	stale(f.o, http.MethodPost, f.teamPath("/people"), `{"email":"`+newAddr+`"}`)
	if f.addedRows(newAddr) != 0 {
		t.Error("stale add stored")
	}
	gen++
	stale(f.o, http.MethodDelete, f.teamPath("/people/"+url.PathEscape(x)), "")
	if f.addedRows(x) != 1 {
		t.Error("stale removal removed")
	}
	gen++
	stale(f.o, http.MethodDelete, "/api/manager/team/members/"+url.PathEscape(x)+"?team_id="+f.team.String(), "")
	if f.addedRows(x) != 1 {
		t.Error("stale Manager removal removed")
	}
	gen++
	inv2 := p129Addr("inv2")
	stale(f.o, http.MethodPost, f.teamPath("/members/invite"), `{"email":"`+inv2+`"}`)
	if f.addedRows(inv2)+f.inviteRows(inv2) != 0 {
		t.Error("stale invite stored")
	}
	stale(holder, http.MethodPost, "/api/teams/invites/"+res.Invitation.Token+"/accept", "")
	if f.addedRows(y) != 1 || f.count(`SELECT count(*) FROM team_members WHERE team_id = $1 AND user_id = $2`, f.team, holder) != 0 {
		t.Error("the stale acceptance wrote the upgrade")
	}
}

// --- shape check against the contract (TeamPerson, TeamSlot, TeamAnalytics keys) ----

func TestPAC129_AnalyticsShapeKeys(t *testing.T) {
	f := newP129(t, nil)
	f.add(f.o, p129Addr("x"), "X")
	w := f.do(f.m, http.MethodGet, f.teamPath("/analytics?date=2030-01-07"), "")
	if w.Code != http.StatusOK {
		t.Fatalf("analytics = %d %s", w.Code, w.Body.String())
	}
	var body map[string]json.RawMessage
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	keys := []string{}
	for k := range body {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "avg_focus_minutes,avg_meeting_minutes,member_breakdown,people,week_start" {
		t.Errorf("keys = %v", keys)
	}
	var people []map[string]any
	_ = json.Unmarshal(body["people"], &people)
	for _, p := range people {
		if len(p) != 8 {
			t.Errorf("row keys = %v", p)
		}
	}
}
