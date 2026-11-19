package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// PAC-130 (spec docs/specs/PAC-130.md; contract contracts/features/PAC-130.md):
// Manager mode must not reveal whether an email address has a Paceday account.
// H is held, domain-confirmed, by an account that shares no team and no
// organisation with the viewer V; N is held by nobody. Every Manager answer,
// and the work done to produce it, is the same for both.

const pac130Week = "2026-11-09" // a Monday

// pac130Google is the Google Calendar stand-in for these tests. freeBusy
// answers the same busy hour for every requested calendar and counts, per
// address, the requests that covered it and the bearer token each carried;
// events answers the 1:1s of detection.
type pac130Google struct {
	mu       sync.Mutex
	requests map[string]int
	tokens   map[string]map[string]bool
	oneOnOne []string // attendees of the manager's recurring 1:1s
	manager  string
}

func newPAC130Google(t *testing.T) *pac130Google {
	t.Helper()
	g := &pac130Google{requests: map[string]int{}, tokens: map[string]map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/freeBusy"):
			var req struct {
				Items []struct {
					ID string `json:"id"`
				} `json:"items"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			busyStart := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
			cals := map[string]any{}
			g.mu.Lock()
			for _, item := range req.Items {
				g.requests[item.ID]++
				if g.tokens[item.ID] == nil {
					g.tokens[item.ID] = map[string]bool{}
				}
				g.tokens[item.ID][r.Header.Get("Authorization")] = true
				cals[item.ID] = map[string]any{"busy": []map[string]string{{
					"start": busyStart.Format(time.RFC3339), "end": busyStart.Add(time.Hour).Format(time.RFC3339),
				}}}
			}
			g.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"kind": "calendar#freeBusy", "calendars": cals})
		case strings.HasSuffix(r.URL.Path, "/events"):
			g.mu.Lock()
			attendees, manager := append([]string(nil), g.oneOnOne...), g.manager
			g.mu.Unlock()
			now := time.Now().UTC()
			items := []any{}
			for i, a := range attendees {
				for j, at := range []time.Time{now.Add(-14 * 24 * time.Hour), now.Add(-7 * 24 * time.Hour)} {
					items = append(items, map[string]any{
						"id": "pac130-" + string(rune('a'+i)) + string(rune('0'+j)), "recurringEventId": "series-" + string(rune('a'+i)),
						"start": map[string]string{"dateTime": at.Format(time.RFC3339)},
						"end":   map[string]string{"dateTime": at.Add(30 * time.Minute).Format(time.RFC3339)},
						"attendees": []map[string]any{
							{"email": manager, "self": true},
							{"email": a, "displayName": "Same"},
						},
					})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"kind": "calendar#events", "items": items})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	calendar.Configure(config.CalendarEndpoint{BaseURL: srv.URL + config.CalendarAPIRoot, Guard: true})
	t.Cleanup(func() { calendar.Configure(config.ProductionCalendarEndpoint()) })
	return g
}

func (g *pac130Google) count(addr string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.requests[addr]
}

func (g *pac130Google) tokensFor(addr string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := []string{}
	for k := range g.tokens[addr] {
		out = append(out, k)
	}
	return out
}

// pac130Fix is one viewer V (domain-confirmed, in its own organisation,
// calendar connected to the stand-in) owning team T, with H and N at a
// run-unique external domain.
type pac130Fix struct {
	t       *testing.T
	db      *sql.DB
	g       *pac130Google
	eng     *engine.ManagerEngine
	r       http.Handler
	teams   http.Handler
	v       uuid.UUID
	vEmail  string
	vDomain string
	ext     string
	team    *storage.Team
	h, n    string
	token   string
}

func newPAC130(t *testing.T) *pac130Fix {
	t.Helper()
	db := openTestDB(t)
	run := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	f := &pac130Fix{t: t, db: db, g: newPAC130Google(t), vDomain: "v" + run + ".pac130.test", ext: "ext" + run + ".pac130.test"}
	f.vEmail = "v@" + f.vDomain
	f.v = createConfirmedUser(t, f.vEmail)
	if err := pac148ConfirmAndJoin(t, db, f.v); err != nil {
		t.Fatal(err)
	}
	f.token = "pac130-" + run
	if err := auth.UpsertUserToken(db, f.v, &oauth2.Token{AccessToken: f.token, RefreshToken: "r", Expiry: time.Now().Add(24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	f.g.manager = f.vEmail
	f.eng = &engine.ManagerEngine{DB: db, OAuthConfig: &oauth2.Config{}}
	f.r = setupManagerRoutes(t, f.eng)
	f.teams = setupTeamRoutes(t)
	f.team = createManagerTestTeam(t, f.v, "PAC-130 "+run)
	f.h, f.n = "hold@"+f.ext, "none@"+f.ext
	// H: one domain-confirmed account, outside V's teams and organisation,
	// sharing analytics with a stored week full of focus.
	f.account(f.h, true, true, 615)
	return f
}

// account creates an account at email (confirmed or not) that shares
// analytics (or not) with a stored week of focus minutes for pac130Week.
func (f *pac130Fix) account(email string, confirmed, shares bool, focus int) uuid.UUID {
	f.t.Helper()
	id := createTestUser(f.t, email)
	if confirmed {
		confirmAddress(f.t, f.db, id)
	}
	if err := storage.UpsertUserProfile(f.db, &storage.UserProfile{UserID: id, AnalyticsSharedWithManager: shares}); err != nil {
		f.t.Fatal(err)
	}
	ws, _ := time.Parse("2006-01-02", pac130Week)
	if _, err := storage.UpsertAnalyticsWeek(f.db, &storage.AnalyticsWeek{UserID: id, WeekStart: ws, FocusMinutes: focus, MeetingMinutes: 240, FreeMinutes: 1545}); err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f *pac130Fix) scope() string { return "?team_id=" + f.team.ID.String() }

func (f *pac130Fix) add(email string, scoped bool) *httptest.ResponseRecorder {
	f.t.Helper()
	path := "/api/manager/team/members"
	if scoped {
		path += f.scope()
	}
	return managerRequest(f.t, f.r, http.MethodPost, path, f.v, map[string]any{"email": email, "display_name": "Same", "cadence": "weekly"})
}

func (f *pac130Fix) mustAdd(email string) {
	f.t.Helper()
	if w := f.add(email, true); w.Code != http.StatusCreated {
		f.t.Fatalf("add %s = %d %s", email, w.Code, w.Body.String())
	}
}

func (f *pac130Fix) get(path string) *httptest.ResponseRecorder {
	f.t.Helper()
	w := managerRequest(f.t, f.r, http.MethodGet, path, f.v, nil)
	if w.Code != http.StatusOK {
		f.t.Fatalf("GET %s = %d %s", path, w.Code, w.Body.String())
	}
	return w
}

// reads are AC-4's four reads: the roster of T with and without the added
// people, and analytics global and scoped to T, for pac130Week.
func (f *pac130Fix) reads() map[string]*httptest.ResponseRecorder {
	f.t.Helper()
	out := map[string]*httptest.ResponseRecorder{}
	for _, p := range []string{
		"/api/manager/team" + f.scope() + "&week=" + pac130Week,
		"/api/manager/team" + f.scope() + "&include_added=true&week=" + pac130Week,
		"/api/manager/analytics?week=" + pac130Week,
		"/api/manager/analytics" + f.scope() + "&week=" + pac130Week,
	} {
		out[p] = f.get(p)
	}
	return out
}

// entry is the raw JSON of the member whose email is addr in a roster or
// analytics body, or "" when there is none.
func pac130Entry(t *testing.T, body []byte, addr string) string {
	t.Helper()
	var resp struct {
		Members []json.RawMessage `json:"members"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	for _, m := range resp.Members {
		var e struct {
			Email string `json:"email"`
		}
		_ = json.Unmarshal(m, &e)
		if e.Email == addr {
			return string(m)
		}
	}
	return ""
}

type pac130Member struct {
	Email         string                 `json:"email"`
	IsPacedayUser bool                   `json:"is_paceday_user"`
	DataAvailable *bool                  `json:"data_available"`
	ThisWeek      engine.MemberWeekStats `json:"this_week"`
	Weeks         []struct {
		WeekStart string `json:"week_start"`
		engine.MemberWeekStats
	} `json:"weeks"`
}

// member decodes addr's entry of the roster of T for pac130Week.
func (f *pac130Fix) member(addr string) pac130Member {
	f.t.Helper()
	raw := pac130Entry(f.t, f.get("/api/manager/team"+f.scope()+"&week="+pac130Week).Body.Bytes(), addr)
	if raw == "" {
		f.t.Fatalf("%s is not on the roster", addr)
	}
	var m pac130Member
	_ = json.Unmarshal([]byte(raw), &m)
	return m
}

// analyticsWeek is addr's pac130Week bucket of analytics scoped to T.
func (f *pac130Fix) analyticsWeek(addr string) (bool, engine.MemberWeekStats) {
	f.t.Helper()
	raw := pac130Entry(f.t, f.get("/api/manager/analytics"+f.scope()+"&week="+pac130Week).Body.Bytes(), addr)
	var m pac130Member
	_ = json.Unmarshal([]byte(raw), &m)
	if len(m.Weeks) == 0 {
		f.t.Fatalf("%s has no analytics weeks: %s", addr, raw)
	}
	return m.IsPacedayUser, m.Weeks[0].MemberWeekStats
}

// recordSteps records the engine's step sequence per address.
func pac130RecordSteps(t *testing.T) func(addr string) []string {
	var mu sync.Mutex
	steps := map[string][]string{}
	engine.InstallManagerStep(t, func(step, email string) error {
		mu.Lock()
		defer mu.Unlock()
		steps[strings.ToLower(email)] = append(steps[strings.ToLower(email)], step)
		return nil
	})
	return func(addr string) []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), steps[addr]...)
	}
}

func pac130Has(steps []string, step string) bool {
	for _, s := range steps {
		if s == step {
			return true
		}
	}
	return false
}

var pac130Volatile = regexp.MustCompile(`"(id|created_at|updated_at)":"[^"]*"`)

// pac130Norm is "byte-identical modulo the address" (spec §4): the address,
// then the row id and timestamps, replaced by fixed tokens.
func pac130Norm(body, addr string) string {
	return strings.ReplaceAll(pac130Volatile.ReplaceAllString(body, `"$1":"T"`), addr, "X")
}

func pac130SameAnswer(t *testing.T, what string, a, b *httptest.ResponseRecorder, addrA, addrB string) {
	t.Helper()
	if a.Code != b.Code || a.Header().Get("Content-Type") != b.Header().Get("Content-Type") {
		t.Errorf("%s: status/type %d %q vs %d %q", what, a.Code, a.Header().Get("Content-Type"), b.Code, b.Header().Get("Content-Type"))
	}
	if na, nb := pac130Norm(a.Body.String(), addrA), pac130Norm(b.Body.String(), addrB); na != nb {
		t.Errorf("%s: answers differ beyond the address:\n%s\n%s", what, na, nb)
	}
}

func (f *pac130Fix) storedLinks() int {
	f.t.Helper()
	return pac122Count(f.t, `SELECT count(*) FROM manager_team_members WHERE manager_user_id=$1 AND member_user_id IS NOT NULL`, f.v)
}

// --- Add, patch, detection: no account signal --------------------------------

// AC-1: the add with no team scope answers the same for H and N, with
// member_user_id null, and stores no link. (Today H's answer carries the id.)
func TestPAC130_AC1_AddRevealsNothing(t *testing.T) {
	f := newPAC130(t)
	wh, wn := f.add(f.h, false), f.add(f.n, false)
	if wh.Code != http.StatusCreated {
		t.Fatalf("add = %d %s", wh.Code, wh.Body.String())
	}
	pac130SameAnswer(t, "add", wh, wn, f.h, f.n)
	for _, w := range []*httptest.ResponseRecorder{wh, wn} {
		if !strings.Contains(w.Body.String(), `"member_user_id":null`) {
			t.Errorf("answer carries an account id: %s", w.Body.String())
		}
	}
	if n := f.storedLinks(); n != 0 {
		t.Errorf("stored links = %d, want 0", n)
	}
}

// AC-2: as AC-1, scoped to T.
func TestPAC130_AC2_ScopedAddRevealsNothing(t *testing.T) {
	f := newPAC130(t)
	wh, wn := f.add(f.h, true), f.add(f.n, true)
	if wh.Code != http.StatusCreated {
		t.Fatalf("add = %d %s", wh.Code, wh.Body.String())
	}
	pac130SameAnswer(t, "scoped add", wh, wn, f.h, f.n)
	if !strings.Contains(wh.Body.String(), `"member_user_id":null`) {
		t.Errorf("answer carries an account id: %s", wh.Body.String())
	}
	if n := f.storedLinks(); n != 0 {
		t.Errorf("stored links = %d, want 0", n)
	}
}

// AC-3: an address carried only unconfirmed (U) and one carried by a case
// pair of accounts (C) answer exactly as N.
func TestPAC130_AC3_UnconfirmedAndCollisionAddsMatchNobody(t *testing.T) {
	f := newPAC130(t)
	u, c := "unco@"+f.ext, "pair@"+f.ext
	createTestUser(t, u)
	createTestUser(t, "Pair@"+f.ext)
	createTestUser(t, c)
	wn := f.add(f.n, true)
	for _, addr := range []string{u, c} {
		w := f.add(addr, true)
		pac130SameAnswer(t, "add "+addr, w, wn, addr, f.n)
	}
	if n := f.storedLinks(); n != 0 {
		t.Errorf("stored links = %d, want 0", n)
	}
}

// pac130AssertSameEntries checks AC-4's equalities on every read.
func pac130AssertSameEntries(t *testing.T, reads map[string]*httptest.ResponseRecorder, h, n string) {
	t.Helper()
	for path, w := range reads {
		eh, en := pac130Entry(t, w.Body.Bytes(), h), pac130Entry(t, w.Body.Bytes(), n)
		if eh == "" || en == "" {
			t.Fatalf("%s: H or N missing: %s", path, w.Body.String())
		}
		if strings.ReplaceAll(eh, h, "X") != strings.ReplaceAll(en, n, "X") {
			t.Errorf("%s: entries differ beyond the address:\n%s\n%s", path, eh, en)
		}
		var m pac130Member
		_ = json.Unmarshal([]byte(eh), &m)
		if m.IsPacedayUser {
			t.Errorf("%s: H is_paceday_user true", path)
		}
		if m.ThisWeek.FocusMinutes != 0 {
			t.Errorf("%s: H focus %d leaked", path, m.ThisWeek.FocusMinutes)
		}
		for _, wk := range m.Weeks {
			if wk.FocusMinutes != 0 {
				t.Errorf("%s: H focus %d leaked in %s", path, wk.FocusMinutes, wk.WeekStart)
			}
		}
	}
}

// AC-4: the roster (with and without the added people) and analytics (global
// and T) show H and N identically: flag false, the same meeting minutes from
// the stand-in, no focus for H although H shares and has a stored week.
func TestPAC130_AC4_RosterAndAnalyticsSameForHolderAndNobody(t *testing.T) {
	f := newPAC130(t)
	f.mustAdd(f.h)
	f.mustAdd(f.n)
	reads := f.reads()
	pac130AssertSameEntries(t, reads, f.h, f.n)
	m := f.member(f.h)
	if !m.ThisWeek.DataAvailable || m.ThisWeek.MeetingMinutes != 60 {
		t.Errorf("H this week = %+v, want the stand-in's 60 meeting minutes", m.ThisWeek)
	}
}

// AC-5: identical patches of H and N answer identically, member_user_id null,
// even when H's stored row holds a link kept from before (migration 042).
func TestPAC130_AC5_PatchRevealsNothing(t *testing.T) {
	f := newPAC130(t)
	f.mustAdd(f.h)
	f.mustAdd(f.n)
	var hid uuid.UUID
	_ = f.db.QueryRow(`SELECT id FROM users WHERE email=$1`, f.h).Scan(&hid)
	if _, err := f.db.Exec(`UPDATE manager_team_members SET member_user_id=$1 WHERE manager_user_id=$2 AND member_email=$3`, hid, f.v, f.h); err != nil {
		t.Fatal(err)
	}
	patch := func(addr string) *httptest.ResponseRecorder {
		return managerRequest(t, f.r, http.MethodPatch, "/api/manager/team/members/"+url.PathEscape(addr)+f.scope(), f.v, map[string]any{"cadence": "biweekly"})
	}
	wh, wn := patch(f.h), patch(f.n)
	if wh.Code != http.StatusOK {
		t.Fatalf("patch = %d %s", wh.Code, wh.Body.String())
	}
	pac130SameAnswer(t, "patch", wh, wn, f.h, f.n)
	if !strings.Contains(wh.Body.String(), `"member_user_id":null`) {
		t.Errorf("patch answer carries the kept link: %s", wh.Body.String())
	}
}

// AC-7: detection (the request and the scheduled job's entry point) over
// recurring 1:1s with H and N: preview entries equal modulo the address, no
// link stored, and after confirming both for T, AC-4's equalities hold.
func TestPAC130_AC7_DetectionStoresNoLink(t *testing.T) {
	f := newPAC130(t)
	f.g.oneOnOne = []string{f.h, f.n}
	lookups := 0
	storage.InstallConfirmedHolderLookup(t, func(e string) error {
		if strings.EqualFold(e, f.h) || strings.EqualFold(e, f.n) {
			lookups++
		}
		return nil
	})
	w := managerRequest(t, f.r, http.MethodPost, "/api/manager/detect"+f.scope(), f.v, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("detect = %d %s", w.Code, w.Body.String())
	}
	var preview struct {
		Candidates []json.RawMessage `json:"candidates"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &preview)
	byAddr := map[string]string{}
	for _, c := range preview.Candidates {
		var e struct {
			Email string `json:"email"`
		}
		_ = json.Unmarshal(c, &e)
		byAddr[e.Email] = string(c)
	}
	if byAddr[f.h] == "" || strings.ReplaceAll(byAddr[f.h], f.h, "X") != strings.ReplaceAll(byAddr[f.n], f.n, "X") {
		t.Fatalf("preview entries differ: %s", w.Body.String())
	}
	// The scheduled job runs the same engine entry point.
	if _, err := f.eng.DetectTeam(context.Background(), f.v); err != nil {
		t.Fatal(err)
	}
	if lookups != 0 {
		t.Errorf("detection looked an address up %d times", lookups)
	}
	if n := f.storedLinks(); n != 0 {
		t.Errorf("stored links = %d, want 0", n)
	}
	cw := managerRequest(t, f.r, http.MethodPost, "/api/manager/detect/confirm"+f.scope(), f.v, map[string]any{"emails": []string{f.h, f.n}})
	if cw.Code != http.StatusOK {
		t.Fatalf("confirm = %d %s", cw.Code, cw.Body.String())
	}
	pac130AssertSameEntries(t, f.reads(), f.h, f.n)
	if n := f.storedLinks(); n != 0 {
		t.Errorf("stored links after confirm = %d, want 0", n)
	}
}

// AC-8: the same work for H and N (structural): equal stand-in requests,
// each with V's token; no Paceday read, holder lookup, ownership or consent
// read for H; the same step sequence, one visibility read per entry per read.
func TestPAC130_AC8_SameWorkForHolderAndNobody(t *testing.T) {
	f := newPAC130(t)
	f.mustAdd(f.h)
	f.mustAdd(f.n)
	steps := pac130RecordSteps(t)
	f.reads()
	if f.g.count(f.h) == 0 || f.g.count(f.h) != f.g.count(f.n) {
		t.Errorf("free/busy requests H %d, N %d; want equal and non-zero", f.g.count(f.h), f.g.count(f.n))
	}
	for _, addr := range []string{f.h, f.n} {
		if tk := f.g.tokensFor(addr); len(tk) != 1 || tk[0] != "Bearer "+f.token {
			t.Errorf("%s read with %v, want only V's token", addr, tk)
		}
	}
	sh, sn := steps(f.h), steps(f.n)
	if strings.Join(sh, ",") != strings.Join(sn, ",") {
		t.Errorf("step sequences differ:\nH %v\nN %v", sh, sn)
	}
	visibility := 0
	for _, s := range sh {
		switch s {
		case engine.StepVisibilityRead:
			visibility++
		case engine.StepAnalyticsWeekRead, engine.StepHolderLookup, engine.StepOwnerCheck, engine.StepConsentRead:
			t.Errorf("H took a Paceday read: %s in %v", s, sh)
		}
	}
	if visibility != 4 {
		t.Errorf("visibility reads for H = %d, want 4 (one per entry per read)", visibility)
	}
}

// --- What the viewer legitimately keeps --------------------------------------

// pac130R adds R, a domain-confirmed Paceday member of T outside V's
// organisation who shares analytics with 615 focus minutes, with a manual
// roster row of V assigned to T.
func (f *pac130Fix) pac130R() (uuid.UUID, string) {
	f.t.Helper()
	email := "r@other-" + f.ext
	r := f.account(email, true, true, 615)
	if err := storage.AddTeamMember(f.db, f.team.ID, r, "member"); err != nil {
		f.t.Fatal(err)
	}
	f.mustAdd(email)
	return r, email
}

// AC-9: a co-member R keeps their figures for the owning manager; sharing
// off, or no profile row, is the flag and "no data" (even with a cached row,
// nothing read past consent); a profile row at its defaults shares.
func TestPAC130_AC9_CoMemberKeepsFigures(t *testing.T) {
	f := newPAC130(t)
	r, email := f.pac130R()
	if m := f.member(email); !m.IsPacedayUser || m.ThisWeek.FocusMinutes != 615 || !m.ThisWeek.DataAvailable {
		t.Fatalf("R = %+v, want flag true and 615 focus minutes", m)
	}
	if flag, wk := f.analyticsWeek(email); !flag || wk.FocusMinutes != 615 {
		t.Fatalf("R analytics = %v %+v, want true 615", flag, wk)
	}
	// Sharing off: the cached figures above are not served.
	if err := storage.UpsertUserProfile(f.db, &storage.UserProfile{UserID: r, AnalyticsSharedWithManager: false}); err != nil {
		t.Fatal(err)
	}
	if m := f.member(email); !m.IsPacedayUser || m.ThisWeek.DataAvailable || m.ThisWeek.FocusMinutes != 0 {
		t.Fatalf("R with sharing off = %+v, want flag true, no data", m)
	}
	if flag, wk := f.analyticsWeek(email); !flag || wk.DataAvailable {
		t.Fatalf("R analytics with sharing off = %v %+v", flag, wk)
	}
	// No profile row reads as not shared, as before (contract md §2.2 step 5).
	if _, err := f.db.Exec(`DELETE FROM user_profiles WHERE user_id=$1`, r); err != nil {
		t.Fatal(err)
	}
	steps := pac130RecordSteps(t)
	if m := f.member(email); !m.IsPacedayUser || m.ThisWeek.DataAvailable {
		t.Fatalf("R with no profile row = %+v, want flag true, no data", m)
	}
	if s := steps(email); len(s) == 0 || s[len(s)-1] != engine.StepConsentRead {
		t.Fatalf("steps with no profile row = %v, want nothing after consent-read", s)
	}
	// A profile row at its defaults shares.
	if _, err := f.db.Exec(`INSERT INTO user_profiles (user_id) VALUES ($1)`, r); err != nil {
		t.Fatal(err)
	}
	if m := f.member(email); !m.IsPacedayUser || m.ThisWeek.FocusMinutes != 615 {
		t.Fatalf("R with a default profile row = %+v, want 615", m)
	}
}

// AC-10: visible but not entitled is the flag and "no data", with no
// free/busy request and no Paceday read: a co-member where V is not owner (P),
// a member of V's organisation (Q), unconfirmed carriers in each, a case pair
// with one carrier in a shared team, and a mixed-case account address. And the
// transition: a cached estimate is not served once the address is visible.
func TestPAC130_AC10_VisibleButNotEntitled(t *testing.T) {
	f := newPAC130(t)
	other := createConfirmedUser(t, "o@"+f.ext)
	t2 := createManagerTestTeam(t, other, "PAC-130 T2")
	if err := storage.AddTeamMember(f.db, t2.ID, f.v, "member"); err != nil {
		t.Fatal(err)
	}
	inT2 := func(id uuid.UUID) {
		t.Helper()
		if err := storage.AddTeamMember(f.db, t2.ID, id, "member"); err != nil {
			t.Fatal(err)
		}
	}
	inOrg := func(id uuid.UUID, _ string) {
		t.Helper()
		if err := pac148ConfirmAndJoin(t, f.db, id); err != nil {
			t.Fatal(err)
		}
	}
	p := "p@" + f.ext
	inT2(f.account(p, true, true, 300))
	q := "q@" + f.vDomain
	inOrg(f.account(q, true, true, 300), q)
	// Amended by PAC-148 (spec §3.6, contract md §9): an unconfirmed carrier
	// whose stored association names V's organisation is NOT a member of it
	// (the membership rule needs domain_confirmed standing), so it is outside
	// rule (b) and takes the estimate, like an address nobody holds. Planted
	// directly, as an older release's join would have left it.
	uOrg := "u-org@" + f.vDomain
	uOrgID := f.account(uOrg, false, true, 300)
	pac148Plant(t, f.db, uOrgID, pac148OrgOf(t, f.db, f.v))
	uTeam := "u-team@" + f.ext
	inT2(f.account(uTeam, false, true, 300))
	kim := "kim@" + f.ext
	inT2(createTestUser(t, "Kim@"+f.ext))
	createTestUser(t, kim)
	alex := "alex@" + f.vDomain
	alexID := f.account("Alex@"+f.vDomain, true, true, 300)
	inOrg(alexID, "Alex@"+f.vDomain)

	cases := []string{p, q, uTeam, kim, alex}
	for _, addr := range append(cases, uOrg) {
		w := f.add(addr, true)
		if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"member_user_id":null`) {
			t.Fatalf("add %s = %d %s", addr, w.Code, w.Body.String())
		}
	}
	// PAC-148: the planted unconfirmed carrier takes the estimate.
	if m := f.member(uOrg); m.IsPacedayUser || !m.ThisWeek.DataAvailable {
		t.Errorf("%s (unconfirmed, planted in V's organisation) = %+v, want flag false and the estimate", uOrg, m)
	}
	if flag, wk := f.analyticsWeek(uOrg); flag || !wk.DataAvailable {
		t.Errorf("%s analytics = %v %+v, want flag false and the estimate", uOrg, flag, wk)
	}
	steps := pac130RecordSteps(t)
	for _, addr := range cases {
		m := f.member(addr)
		if !m.IsPacedayUser || m.ThisWeek.DataAvailable || m.ThisWeek.FocusMinutes != 0 {
			t.Errorf("%s = %+v, want flag true and no data", addr, m)
		}
		if flag, wk := f.analyticsWeek(addr); !flag || wk.DataAvailable {
			t.Errorf("%s analytics = %v %+v, want flag true and no data", addr, flag, wk)
		}
		if n := f.g.count(addr); n != 0 {
			t.Errorf("%s: %d free/busy requests, want 0", addr, n)
		}
		if s := steps(addr); pac130Has(s, engine.StepAnalyticsWeekRead) || pac130Has(s, engine.StepFreeBusy) || pac130Has(s, engine.StepCacheRead) {
			t.Errorf("%s: steps %v read data", addr, s)
		}
	}

	// Transition: X is estimated while not visible (and the estimate is
	// cached), then joins T2: flag true, "no data", the estimate not served.
	x := "x@" + f.ext
	xID := f.account(x, true, true, 300)
	f.mustAdd(x)
	if m := f.member(x); m.IsPacedayUser || !m.ThisWeek.DataAvailable {
		t.Fatalf("X before = %+v, want an estimate", m)
	}
	if n := pac122Count(t, `SELECT count(*) FROM team_analytics_cache WHERE manager_user_id=$1 AND member_email=$2 AND NOT is_paceday_user`, f.v, x); n == 0 {
		t.Fatal("the estimate was not cached")
	}
	inT2(xID)
	before := f.g.count(x)
	if m := f.member(x); !m.IsPacedayUser || m.ThisWeek.DataAvailable || m.ThisWeek.MeetingMinutes != 0 {
		t.Fatalf("X after joining T2 = %+v, want flag true, no data (cached estimate not served)", m)
	}
	if f.g.count(x) != before {
		t.Fatal("a free/busy request was made for a visible address")
	}
}

// AC-11: R's cached figures are not served after R leaves T, by either
// removal path: R takes the estimate, exactly as N.
func TestPAC130_AC11_RemovalDropsToEstimate(t *testing.T) {
	for _, path := range []string{"team member removal", "manager team-scoped removal"} {
		t.Run(path, func(t *testing.T) {
			f := newPAC130(t)
			r, email := f.pac130R()
			f.mustAdd(f.n)
			if m := f.member(email); m.ThisWeek.FocusMinutes != 615 {
				t.Fatalf("R before = %+v", m)
			}
			if path == "team member removal" {
				w := managerRequest(t, f.teams, http.MethodDelete, "/api/teams/"+f.team.ID.String()+"/members/"+r.String(), f.v, nil)
				if w.Code != http.StatusNoContent {
					t.Fatalf("remove = %d %s", w.Code, w.Body.String())
				}
			} else {
				w := managerRequest(t, f.r, http.MethodDelete, "/api/manager/team/members/"+url.PathEscape(email)+f.scope(), f.v, nil)
				if w.Code != http.StatusNoContent {
					t.Fatalf("remove = %d %s", w.Code, w.Body.String())
				}
				f.mustAdd(email)
			}
			body := f.get("/api/manager/team" + f.scope() + "&week=" + pac130Week).Body.Bytes()
			er, en := pac130Entry(t, body, email), pac130Entry(t, body, f.n)
			if er == "" || strings.ReplaceAll(er, email, "X") != strings.ReplaceAll(en, f.n, "X") {
				t.Fatalf("R after removal differs from N:\n%s\n%s", er, en)
			}
			var m pac130Member
			_ = json.Unmarshal([]byte(er), &m)
			if m.IsPacedayUser || m.ThisWeek.FocusMinutes != 0 || !m.ThisWeek.DataAvailable {
				t.Fatalf("R after removal = %+v, want flag false and the estimate", m)
			}
		})
	}
}

// --- Errors -------------------------------------------------------------------

// AC-13: a failed visibility read (and, as extra cover, a failed holder
// lookup, ownership check or consent read) is the 500 on the roster and
// analytics of a one-entry roster, and nothing is cached for the entry.
func TestPAC130_AC13_VisibilityReadFailureIs500(t *testing.T) {
	for _, step := range []string{engine.StepVisibilityRead, engine.StepHolderLookup, engine.StepOwnerCheck, engine.StepConsentRead} {
		t.Run(step, func(t *testing.T) {
			f := newPAC130(t)
			email := f.n
			if step != engine.StepVisibilityRead {
				_, email = f.pac130R() // a visible, owned, sharing co-member reaches every step
			} else {
				f.mustAdd(email)
			}
			engine.InstallManagerStep(t, func(s, e string) error {
				if s == step && strings.EqualFold(e, email) {
					return errors.New("pac130: " + step + " failed")
				}
				return nil
			})
			for _, path := range []string{
				"/api/manager/team" + f.scope() + "&week=" + pac130Week,
				"/api/manager/analytics?week=" + pac130Week,
				"/api/manager/analytics" + f.scope() + "&week=" + pac130Week,
			} {
				reported, _ := captureServerErrors(t)
				w := managerRequest(t, f.r, http.MethodGet, path, f.v, nil)
				pac122AssertServerError(t, w, reported)
			}
			if n := pac122Count(t, `SELECT count(*) FROM team_analytics_cache WHERE manager_user_id=$1`, f.v); n != 0 {
				t.Errorf("cached rows = %d, want 0", n)
			}
			if f.g.count(email) != 0 {
				t.Errorf("a free/busy request was made after the failure")
			}
		})
	}
}

// --- Remaining paths stay closed ----------------------------------------------

var pac130InviteVolatile = regexp.MustCompile(`"(id|token|created_at|updated_at|expires_at|expiresAt|createdAt|invitationId|pendingExpiresAt)":"[^"]*"`)

// AC-15: the team invite answers H and N identically modulo the address, the
// token and the invitation's id and timestamps. (PAC-129's AC-2 test pins the
// team direct add and still passes.)
func TestPAC130_AC15_InviteRevealsNothing(t *testing.T) {
	f := newPAC130(t)
	_ = recordMail(t)
	invite := func(addr string) *httptest.ResponseRecorder {
		return managerRequest(t, f.teams, http.MethodPost, "/api/teams/"+f.team.ID.String()+"/members/invite", f.v, map[string]string{"email": addr})
	}
	wh, wn := invite(f.h), invite(f.n)
	if wh.Code != http.StatusCreated {
		t.Fatalf("invite = %d %s", wh.Code, wh.Body.String())
	}
	norm := func(body, addr string) string {
		return strings.ReplaceAll(pac130InviteVolatile.ReplaceAllString(body, `"$1":"T"`), addr, "X")
	}
	if wh.Code != wn.Code || wh.Header().Get("Content-Type") != wn.Header().Get("Content-Type") || norm(wh.Body.String(), f.h) != norm(wn.Body.String(), f.n) {
		t.Errorf("invite answers differ:\n%s\n%s", norm(wh.Body.String(), f.h), norm(wn.Body.String(), f.n))
	}
}

// --- Contract and compatibility -----------------------------------------------

func pac130Strict(t *testing.T, what string, body []byte, into any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		t.Errorf("%s does not decode strictly into the contract type: %v; body %s", what, err, body)
	}
}

// AC-16: the AC-1, AC-4 and AC-5 answers are instances of the contract's
// schemas, which PAC-130 changed in descriptions only (so they are the
// previous bundle's), and the bundle states the new meanings.
func TestPAC130_AC16_AnswersParseUnderPreviousSchemas(t *testing.T) {
	f := newPAC130(t)
	add := f.add(f.h, true)
	pac130Strict(t, "add", add.Body.Bytes(), &gen.ManagerTeamMemberGoStruct{})
	patch := managerRequest(t, f.r, http.MethodPatch, "/api/manager/team/members/"+url.PathEscape(f.h)+f.scope(), f.v, map[string]any{"cadence": "monthly"})
	pac130Strict(t, "patch", patch.Body.Bytes(), &gen.ManagerTeamMemberGoStruct{})
	for path, w := range f.reads() {
		if strings.Contains(path, "/team?") {
			pac130Strict(t, path, w.Body.Bytes(), &gen.ManagerRoster{})
		} else {
			pac130Strict(t, path, w.Body.Bytes(), &gen.ManagerAnalytics{})
		}
	}

	raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "openapi", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	bundle := string(raw)
	i := strings.Index(bundle, "    ManagerTeamMemberGoStruct:")
	if i < 0 {
		t.Fatal("ManagerTeamMemberGoStruct not in the bundle")
	}
	j := strings.Index(bundle[i:], "member_user_id:")
	prop := bundle[i+j : i+j+1500]
	if !strings.Contains(prop, "deprecated: true") || !strings.Contains(strings.ToLower(prop), "always") || !strings.Contains(strings.ToLower(prop), "null") {
		t.Errorf("member_user_id is not stated deprecated and always null:\n%s", prop)
	}
	for _, want := range []string{"PAC-130", "listOrgMembers"} {
		if !strings.Contains(bundle, want) {
			t.Errorf("the bundle does not mention %s", want)
		}
	}
}

// AC-17: rule (b) is the organisation member list. Q in V's organisation is
// "On Paceday" exactly when GET /api/org/members lists Q to V; when the
// shared set's seam excludes Q (standing in for PAC-148), the list omits Q
// and Q takes the estimate.
func TestPAC130_AC17_RuleBIsTheOrgList(t *testing.T) {
	f := newPAC130(t)
	q := "q@" + f.vDomain
	qID := f.account(q, true, true, 300)
	if err := pac148ConfirmAndJoin(t, f.db, qID); err != nil {
		t.Fatal(err)
	}
	f.mustAdd(q)
	orgList := func() bool {
		t.Helper()
		r := chi.NewRouter()
		r.Get("/api/org/members", (&orgHandlers{db: f.db}).members)
		w := managerRequest(t, r, http.MethodGet, "/api/org/members", f.v, nil)
		return strings.Contains(w.Body.String(), qID.String())
	}
	if !orgList() || !f.member(q).IsPacedayUser {
		t.Fatal("Q is not both listed and On Paceday")
	}
	storage.InstallOrgMembersExclude(t, func(viewer uuid.UUID) []uuid.UUID {
		if viewer == f.v {
			return []uuid.UUID{qID}
		}
		return nil
	})
	if orgList() {
		t.Fatal("the organisation list still lists Q")
	}
	m := f.member(q)
	if m.IsPacedayUser || !m.ThisWeek.DataAvailable || f.g.count(q) == 0 {
		t.Fatalf("Q excluded = %+v (%d requests), want flag false and the estimate", m, f.g.count(q))
	}
}

// AC-18: no Manager action writes a link: an add of H, detection over H, and
// a patch and a schedule of formal member R (which create R's roster row).
func TestPAC130_AC18_NoManagerActionWritesALink(t *testing.T) {
	f := newPAC130(t)
	f.mustAdd(f.h)
	f.g.oneOnOne = []string{f.h}
	if w := managerRequest(t, f.r, http.MethodPost, "/api/manager/detect"+f.scope(), f.v, nil); w.Code != http.StatusOK {
		t.Fatalf("detect = %d %s", w.Code, w.Body.String())
	}
	r := f.account("formal@"+f.ext, true, true, 615)
	if err := storage.AddTeamMember(f.db, f.team.ID, r, "member"); err != nil {
		t.Fatal(err)
	}
	if w := managerRequest(t, f.r, http.MethodPatch, "/api/manager/team/members/"+url.PathEscape("formal@"+f.ext)+f.scope(), f.v, map[string]any{"cadence": "weekly"}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"member_user_id":null`) {
		t.Fatalf("patch = %d %s", w.Code, w.Body.String())
	}
	if _, err := f.db.Exec(`DELETE FROM manager_team_members WHERE manager_user_id=$1 AND member_email=$2`, f.v, "formal@"+f.ext); err != nil {
		t.Fatal(err)
	}
	if w := managerRequest(t, f.r, http.MethodPost, "/api/manager/team/members/"+url.PathEscape("formal@"+f.ext)+"/schedule"+f.scope(), f.v, nil); w.Code != http.StatusOK {
		t.Fatalf("schedule = %d %s", w.Code, w.Body.String())
	}
	if n := pac122Count(t, `SELECT count(*) FROM manager_team_members WHERE manager_user_id=$1`, f.v); n != 2 {
		t.Fatalf("roster rows = %d, want 2 (H and R)", n)
	}
	if n := f.storedLinks(); n != 0 {
		t.Fatalf("stored links = %d, want 0", n)
	}
	// The formal member's figures still reach the owner through the
	// membership-free path: R's row is resolved from its address.
	if m := f.member("formal@" + f.ext); !m.IsPacedayUser || m.ThisWeek.FocusMinutes != 615 {
		t.Fatalf("R = %+v", m)
	}
}

// AC-19: figures by how the account is known. An unconfirmed S (an unbound
// legacy account; an account created on mailbox evidence), member of T,
// outside V's organisation, sharing, 300 focus minutes: the formal entry
// keeps its figures; a manual row is the flag and "no data" with no reads;
// once S is confirmed the manual row has the figures; and an address whose
// only account is unconfirmed and outside V is estimated, the holder lookup
// never reached.
func TestPAC130_AC19_FiguresByHowTheAccountIsKnown(t *testing.T) {
	for _, kind := range []string{"unbound legacy", "mailbox evidence"} {
		t.Run(kind, func(t *testing.T) {
			f := newPAC130(t)
			email := "s@other-" + f.ext
			s := f.account(email, false, true, 300)
			if kind == "mailbox evidence" {
				if _, err := f.db.Exec(`INSERT INTO user_identities (user_id, provider, tenant, subject, evidence, bound_by)
					VALUES ($1, 'google', '', $2, 'mailbox', 'created')`, s, s.String()); err != nil {
					t.Fatal(err)
				}
			}
			if err := storage.AddTeamMember(f.db, f.team.ID, s, "member"); err != nil {
				t.Fatal(err)
			}
			if m := f.member(email); !m.IsPacedayUser || m.ThisWeek.FocusMinutes != 300 {
				t.Fatalf("formal S = %+v, want flag true and 300 (no standing check)", m)
			}
			f.mustAdd(email)
			steps := pac130RecordSteps(t)
			if m := f.member(email); !m.IsPacedayUser || m.ThisWeek.DataAvailable {
				t.Fatalf("manual S = %+v, want flag true and no data", m)
			}
			if st := steps(email); pac130Has(st, engine.StepAnalyticsWeekRead) || pac130Has(st, engine.StepFreeBusy) || f.g.count(email) != 0 {
				t.Fatalf("manual S read data: %v", st)
			}
			confirmAddress(t, f.db, s)
			if _, err := f.db.Exec(`UPDATE users SET address_standing='domain_confirmed' WHERE id=$1`, s); err != nil {
				t.Fatal(err)
			}
			if m := f.member(email); !m.IsPacedayUser || m.ThisWeek.FocusMinutes != 300 {
				t.Fatalf("manual S confirmed = %+v, want 300", m)
			}

			u := "unco@" + f.ext
			f.account(u, false, true, 300)
			f.mustAdd(u)
			steps = pac130RecordSteps(t)
			if m := f.member(u); m.IsPacedayUser || !m.ThisWeek.DataAvailable || m.ThisWeek.FocusMinutes != 0 {
				t.Fatalf("U = %+v, want flag false and the estimate", m)
			}
			if pac130Has(steps(u), engine.StepHolderLookup) {
				t.Fatalf("the holder lookup ran for a not-visible address: %v", steps(u))
			}
		})
	}
}
