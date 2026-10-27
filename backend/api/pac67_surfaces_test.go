package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/internal/oauthfake"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// PAC-67 contract tests for the Google rows of AC-8, credentials used on
// someone else's behalf (AC-26 to AC-28), disconnect isolation (AC-18) and the
// activity-log contract (AC-22, CONTRACT-6).

const pac67Events = "/api/calendar/events?start=2030-01-07T00:00:00Z&end=2030-01-08T00:00:00Z"

func pac67FreeBusy(w *pac67World, user uuid.UUID) map[string]any {
	w.t.Helper()
	rec := w.do(user, http.MethodPost, "/api/freebusy", `{"emails":["colleague@corp.example"],"start_time":"2030-01-07T09:00:00Z","end_time":"2030-01-07T17:00:00Z"}`)
	if rec.Code != http.StatusOK {
		w.t.Fatalf("freebusy = %d %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func coverageOf(body map[string]any) string {
	ps, _ := body["participants"].([]any)
	if len(ps) != 1 {
		return fmt.Sprint(body)
	}
	p, _ := ps[0].(map[string]any)
	return fmt.Sprint(p["status"])
}

// AC-8 Google rows (availability lookup, listing), AC-10 (Google), AC-21
// (second half): a refused Google credential degrades the lookup as today,
// fails the listing as today, and is reported on Google's surfaces only.
func TestPAC67_GoogleRefusal(t *testing.T) {
	w := newPAC67World(t)
	alice := w.user("pac67-google-alice@example.com")
	bob := w.user("pac67-google-bob@example.com")
	w.d.Answer("g-dead", oauthfake.InvalidGrant)
	w.providers(alice, "google", "meet")
	w.seedGoogle(alice, "g-old", "g-dead", pac67Past)
	w.providers(bob, "google", "meet")
	w.seedGoogle(bob, "g-bob", "g-bob-r", pac67Future)

	if got := coverageOf(pac67FreeBusy(w, alice)); got != "unknown" {
		t.Errorf("availability coverage = %s, want unknown (D-6)", got)
	}
	w.assertRow(alice, "google_meet", false)
	if s := w.status(alice); s["provider"] != "google" || s["connected"] != false {
		t.Errorf("calendar status = %v, want google not connected", s)
	}
	w.assertEntries(alice, `connection_lost {"provider":"google"}`)

	// Listing fails exactly as a failed Google call does today (D-7).
	assertBody(t, w.do(alice, http.MethodGet, pac67Events, ""), http.StatusInternalServerError, `{"error":"internal error"}`)
	w.assertEntries(alice, `connection_lost {"provider":"google"}`)

	// Bob is untouched.
	w.assertRow(bob, "google_meet", true)
	if s := w.status(bob); s["connected"] != true {
		t.Errorf("bob's status = %v", s)
	}
	w.assertEntries(bob)
	if got := coverageOf(pac67FreeBusy(w, bob)); got != "known" {
		t.Errorf("bob's coverage = %s, want known", got)
	}

	// AC-21: an Outlook calendar user whose Google credential is refused.
	carol := w.user("pac67-google-carol@example.com")
	w.d.Answer("g-dead-carol", oauthfake.InvalidGrant)
	w.providers(carol, "outlook", "meet")
	w.seedOutlook(carol, "ms-carol", "ms-carol-r", pac67Future)
	w.seedGoogle(carol, "g-old-carol", "g-dead-carol", pac67Past)
	before := w.status(carol)
	w.do(carol, http.MethodGet, pac67Events, "")
	w.assertRow(carol, "google_meet", false)
	if s := w.status(carol); fmt.Sprint(s) != fmt.Sprint(before) || s["connected"] != true {
		t.Errorf("carol's calendar status changed: %v -> %v", before, s)
	}
	w.assertEntries(carol, `connection_lost {"provider":"google"}`)
}

// AC-10 (Google): a temporary failure keeps Google connected and records nothing.
func TestPAC67_GoogleTemporaryFailure(t *testing.T) {
	w := newPAC67World(t)
	alice := w.user("pac67-google-flaky@example.com")
	w.d.Answer("g-flaky", oauthfake.Unavailable)
	w.providers(alice, "google", "meet")
	w.seedGoogle(alice, "g-old", "g-flaky", pac67Past)
	if got := coverageOf(pac67FreeBusy(w, alice)); got != "unknown" {
		t.Errorf("coverage = %s, want unknown", got)
	}
	assertBody(t, w.do(alice, http.MethodGet, pac67Events, ""), http.StatusInternalServerError, `{"error":"internal error"}`)
	assertBody(t, w.do(alice, http.MethodPost, "/api/events/e1/conference", `{"provider":"zoom"}`), http.StatusNotFound, `{"error":"event not found"}`)
	w.assertRow(alice, "google_meet", true)
	if s := w.status(alice); s["connected"] != true {
		t.Errorf("status = %v, want connected", s)
	}
	w.assertEntries(alice)
}

// AC-6 through the API: a renewed Google token is stored, so the next listing
// sends no renewal.
func TestPAC67_GoogleRenewalIsPersisted(t *testing.T) {
	w := newPAC67World(t)
	alice := w.user("pac67-google-persist@example.com")
	bob := w.user("pac67-google-persist-bob@example.com")
	w.seedGoogle(alice, "g-old", "g-persist-r", pac67Past)
	w.seedGoogle(bob, "g-bob", "g-bob-r", pac67Past)
	for i := 0; i < 2; i++ {
		if rec := w.do(alice, http.MethodGet, pac67Events, ""); rec.Code != http.StatusOK {
			t.Fatalf("listing %d = %d %s", i, rec.Code, rec.Body.String())
		}
	}
	if n := len(w.d.Calls("/token")); n != 1 {
		t.Errorf("renewals = %d, want 1", n)
	}
	if tok, _ := auth.LoadUserToken(w.db, alice); tok.AccessToken != "access-1" {
		t.Errorf("stored = %+v", tok)
	}
	if tok, _ := auth.LoadUserToken(w.db, bob); tok.AccessToken != "g-bob" {
		t.Errorf("bob's token changed: %+v", tok)
	}
	if !w.cal.saw("access-1") {
		t.Error("the listing did not use the renewed token")
	}
}

// AC-18: A's calendar disconnect leaves B's Google credential stored.
func TestPAC67_CalendarDisconnectLeavesOthersGoogleCredential(t *testing.T) {
	w := newPAC67World(t)
	alice := w.user("pac67-ac18-alice@example.com")
	bob := w.user("pac67-ac18-bob@example.com")
	w.providers(alice, "google", "meet")
	w.seedGoogle(alice, "g-a", "g-a-r", pac67Future)
	w.seedGoogle(bob, "g-b", "g-b-r", pac67Future)
	if rec := w.do(alice, http.MethodDelete, "/api/auth/disconnect", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("disconnect = %d %s", rec.Code, rec.Body.String())
	}
	if tok, _ := auth.LoadUserToken(w.db, alice); tok != nil {
		t.Error("alice's credential was kept")
	}
	if tok, _ := auth.LoadUserToken(w.db, bob); tok == nil || tok.AccessToken != "g-b" {
		t.Errorf("bob's credential = %+v, want it kept", tok)
	}
}

// pac67Team creates a team with the given members and the routes for its
// availability, served by the real engine.
func pac67Team(w *pac67World, name string, members ...uuid.UUID) (string, *chi.Mux) {
	w.t.Helper()
	team, err := storage.CreateTeam(w.db, name, members[0])
	if err != nil {
		w.t.Fatal(err)
	}
	for i, m := range members {
		role := "member"
		if i == 0 {
			role = "owner"
		}
		if err := storage.AddTeamMember(w.db, team.ID, m, role); err != nil {
			w.t.Fatal(err)
		}
	}
	h := newTeamHandlersWithAvailabilityEngine(w.db, &engine.TeamAvailabilityEngine{DB: w.db, OAuthConfig: w.google})
	r := chi.NewRouter()
	r.Get("/api/teams/{id}/availability", h.availability)
	return "/api/teams/" + team.ID.String() + "/availability?date=2030-01-07&duration=30", r
}

func pac67Get(w *pac67World, r http.Handler, user uuid.UUID, target string) (int, string) {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if user != uuid.Nil {
		req = withUser(req, user)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// AC-26: a teammate's refused credential is B's loss, recorded once, and the
// requester learns nothing new; a temporary failure records nothing.
func TestPAC67_TeamAvailabilityWithARefusedMember(t *testing.T) {
	w := newPAC67World(t)
	alice := w.user("pac67-ac26-alice@example.com")
	bob := w.user("pac67-ac26-bob@example.com")
	carol := w.user("pac67-ac26-carol@example.com")
	w.seedGoogle(alice, "g-alice", "g-alice-r", pac67Future)
	w.seedGoogle(carol, "g-carol", "g-carol-r", pac67Future)
	target, r := pac67Team(w, "PAC-67 AC-26", alice, bob, carol)

	// Today's answer when B's calendar cannot be read: B holds no credential.
	_, baseline := pac67Get(w, r, alice, target)
	aliceTok, _ := auth.LoadUserToken(w.db, alice)
	aliceStatus := w.status(alice)

	w.d.Answer("b-dead", oauthfake.InvalidGrant)
	w.providers(bob, "google", "meet")
	w.seedGoogle(bob, "g-bob-old", "b-dead", pac67Past)
	var wg sync.WaitGroup
	bodies := make([]string, 2)
	for i, u := range []uuid.UUID{alice, carol} {
		wg.Add(1)
		go func(i int, u uuid.UUID) {
			defer wg.Done()
			_, bodies[i] = pac67Get(w, r, u, target)
		}(i, u)
	}
	wg.Wait()
	for i, b := range bodies {
		if b != baseline {
			t.Errorf("requester %d answer changed:\n%s\nwant\n%s", i, b, baseline)
		}
		if strings.Contains(b, "lost") || strings.Contains(b, "connected") {
			t.Errorf("the answer says something about bob's connection: %s", b)
		}
	}
	w.assertRow(bob, "google_meet", false)
	if s := w.status(bob); s["connected"] != false {
		t.Errorf("bob's status = %v", s)
	}
	w.assertEntries(bob, `connection_lost {"provider":"google"}`)
	w.assertEntries(alice)
	w.assertEntries(carol)
	if s := w.status(alice); fmt.Sprint(s) != fmt.Sprint(aliceStatus) {
		t.Errorf("alice's status changed: %v -> %v", aliceStatus, s)
	}
	if tok, _ := auth.LoadUserToken(w.db, alice); *tok != *aliceTok {
		t.Errorf("alice's credential changed: %+v -> %+v", aliceTok, tok)
	}

	// Temporary failure: same answer, nothing recorded.
	dan := w.user("pac67-ac26-dan@example.com")
	w.d.Answer("d-flaky", oauthfake.Unavailable)
	w.seedGoogle(dan, "g-dan-old", "d-flaky", pac67Past)
	target2, r2 := pac67Team(w, "PAC-67 AC-26 temporary", alice, dan)
	_, b := pac67Get(w, r2, alice, target2)
	if strings.Contains(b, "lost") {
		t.Errorf("answer = %s", b)
	}
	w.assertRow(dan, "google_meet", true)
	w.assertEntries(dan)
	w.assertEntries(alice)
}

// AC-27: a teammate's expired credential is renewed with their own refresh
// token, stored for them only, and their busy time counts as it does today.
func TestPAC67_TeamAvailabilityRenewsAMember(t *testing.T) {
	w := newPAC67World(t)
	alice := w.user("pac67-ac27-alice@example.com")
	bob := w.user("pac67-ac27-bob@example.com")
	w.seedGoogle(alice, "g-alice", "g-alice-r", pac67Future)
	w.seedGoogle(bob, "g-bob-live", "b-renew-r", pac67Future)
	w.cal.setBusy("g-bob-live")
	w.cal.setBusy("access-1")
	target, r := pac67Team(w, "PAC-67 AC-27", alice, bob)
	_, working := pac67Get(w, r, alice, target)

	w.seedGoogle(bob, "g-bob-old", "b-renew-r", pac67Past)
	aliceTok, _ := auth.LoadUserToken(w.db, alice)
	code, body := pac67Get(w, r, alice, target)
	if code != http.StatusOK || body != working {
		t.Errorf("answer = %d %s, want the working-credential answer %s", code, body, working)
	}
	calls := w.d.Calls("/token")
	if len(calls) != 1 || calls[0].Form.Get("refresh_token") != "b-renew-r" {
		t.Fatalf("renewals = %+v, want one with bob's refresh token", calls)
	}
	if tok, _ := auth.LoadUserToken(w.db, bob); tok.AccessToken != "access-1" || !tok.Expiry.After(time.Now()) {
		t.Errorf("bob's stored credential = %+v", tok)
	}
	if tok, _ := auth.LoadUserToken(w.db, alice); *tok != *aliceTok {
		t.Errorf("alice's credential changed")
	}
}

// AC-28: a host's refused credential on a public link: responses as when the
// host's calendar cannot be read, the loss is the host's, recorded once, and
// link info is identical before and after (D-14).
func TestPAC67_PublicBookingWithARefusedHost(t *testing.T) {
	w := newPAC67World(t)
	host := w.user("pac67-ac28-host@example.com")
	other := w.user("pac67-ac28-other@example.com")
	w.seedGoogle(other, "g-other", "g-other-r", pac67Future)
	link := createPublicBookingLink(t, host, "pac67-ac28", "PAC-67 AC-28", 0, "reusable", nil)
	bh := newBookingHandlers(w.db, w.google)
	r := chi.NewRouter()
	r.Get("/api/book/{slug}", bh.getLinkInfo)
	r.Get("/api/book/{slug}/slots", bh.getSlots)
	r.Post("/api/book/{slug}", bh.createBooking)
	start := futureBookingStart()
	slots := fmt.Sprintf("/api/book/%s/slots?date=%s&duration=30", link.Slug, start.Format(time.DateOnly))
	// book books the slot `hour` hours after start; the answer is returned
	// without the fields that differ between bookings by construction.
	book := func(email string, hour int) (int, map[string]any) {
		s := start.Add(time.Duration(hour) * time.Hour)
		req := httptest.NewRequest(http.MethodPost, "/api/book/"+link.Slug, strings.NewReader(fmt.Sprintf(
			`{"name":"Visitor","email":%q,"start":%q,"end":%q}`, email, s.Format(time.RFC3339), s.Add(30*time.Minute).Format(time.RFC3339))))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		for _, k := range []string{"id", "booker_email", "start", "end"} {
			delete(m, k)
		}
		return rec.Code, m
	}

	// Today's answers when the host's calendar cannot be read or written.
	w.seedGoogle(host, "g-host-live", "g-host-live-r", pac67Future)
	w.cal.setFailAll(true)
	_, baseSlots := pac67Get(w, r, uuid.Nil, slots)
	w.cal.setFailAll(false)
	_, infoBefore := pac67Get(w, r, uuid.Nil, "/api/book/"+link.Slug)

	w.d.Answer("h-dead", oauthfake.InvalidGrant)
	w.seedGoogle(host, "g-host-old", "h-dead", pac67Past)
	for i := 0; i < 2; i++ {
		if _, got := pac67Get(w, r, uuid.Nil, slots); got != baseSlots {
			t.Errorf("slots = %s, want %s", got, baseSlots)
		}
	}
	// Booking is saved and confirmed as today when the host's calendar cannot
	// be written (201, the confirmation names the host), with no event write.
	inserts := w.cal.insertCount()
	for i := 1; i <= 2; i++ {
		code, got := book(fmt.Sprintf("v%d@example.com", i), i)
		hosts, _ := got["hosts"].([]any)
		if code != http.StatusCreated || got["title"] != "PAC-67 AC-28" || len(hosts) != 1 || strings.Contains(fmt.Sprint(got), "lost") {
			t.Errorf("booking %d = %d %v, want today's 201 confirmation", i, code, got)
		}
	}
	if w.cal.insertCount() != inserts {
		t.Error("an event write was attempted with the refused credential")
	}
	w.assertRow(host, "google_meet", false)
	if s := w.status(host); s["connected"] != false {
		t.Errorf("host status = %v", s)
	}
	w.assertEntries(host, `connection_lost {"provider":"google"}`)
	w.assertEntries(other)
	if _, infoAfter := pac67Get(w, r, uuid.Nil, "/api/book/"+link.Slug); infoAfter != infoBefore {
		t.Errorf("link info changed after the loss:\n%s\nwant\n%s", infoAfter, infoBefore)
	}
	if !strings.Contains(infoBefore, `"coverage":{"total":1,"checked":1}`) {
		t.Errorf("link info = %s, want the host counted as checked", infoBefore)
	}

	// Temporary failure: same answers, nothing recorded.
	host2 := w.user("pac67-ac28-host2@example.com")
	link2 := createPublicBookingLink(t, host2, "pac67-ac28-tmp", "PAC-67 AC-28 tmp", 0, "reusable", nil)
	w.d.Answer("h2-flaky", oauthfake.Unavailable)
	w.seedGoogle(host2, "g-h2-old", "h2-flaky", pac67Past)
	if code, _ := pac67Get(w, r, uuid.Nil, fmt.Sprintf("/api/book/%s/slots?date=%s&duration=30", link2.Slug, start.Format(time.DateOnly))); code != http.StatusOK {
		t.Errorf("slots = %d", code)
	}
	w.assertRow(host2, "google_meet", true)
	w.assertEntries(host2)

	// Renewal stored for the host only.
	host3 := w.user("pac67-ac28-host3@example.com")
	link3 := createPublicBookingLink(t, host3, "pac67-ac28-renew", "PAC-67 AC-28 renew", 0, "reusable", nil)
	w.seedGoogle(host3, "g-h3-old", "h3-r", pac67Past)
	otherTok, _ := auth.LoadUserToken(w.db, other)
	pac67Get(w, r, uuid.Nil, fmt.Sprintf("/api/book/%s/slots?date=%s&duration=30", link3.Slug, start.Format(time.DateOnly)))
	if tok, _ := auth.LoadUserToken(w.db, host3); tok.AccessToken == "g-h3-old" || tok.RefreshToken != "h3-r" {
		t.Errorf("host3's renewal not stored: %+v", tok)
	}
	if tok, _ := auth.LoadUserToken(w.db, other); *tok != *otherTok {
		t.Error("another user's credential changed")
	}
}

// AC-22: a listing holding every new kind beside the existing ones conforms
// to the contract's closed enum, which keeps the seven existing kinds.
func TestPAC67_AuditListingWithConnectionKindsConforms(t *testing.T) {
	enum := pac67AuditEnum(t)
	for _, k := range []string{"focus_created", "focus_cleared", "meeting_scheduled", "meeting_moved", "meeting_created", "nlp_parsed", "nlp_confirmed"} {
		if !enum[k] {
			t.Errorf("the enum lost %s", k)
		}
	}
	db := openTestDB(t)
	kim := createTestUser(t, "pac67-ac22@example.com")
	for _, e := range [][2]string{
		{"focus_created", `{"n":1}`},
		{"connection_lost", `{"provider":"google"}`},
		{"provider_disconnected", `{"provider":"zoom","revocation":"unconfirmed"}`},
		{"revocation_unconfirmed", `{"provider":"zoom"}`},
	} {
		storage.WriteAuditLog(db, kim, e[0], e[1])
	}
	h := auditHandler(db)
	req := withUser(httptest.NewRequest(http.MethodGet, "/api/audit", nil), kim)
	rec := httptest.NewRecorder()
	h(rec, req)
	var got []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK || len(got) != 4 {
		t.Fatalf("listing = %d %s", rec.Code, rec.Body.String())
	}
	for _, e := range got {
		if !enum[fmt.Sprint(e["action"])] {
			t.Errorf("listed action %v is outside the contract's enum", e["action"])
		}
		var details map[string]any
		if err := json.Unmarshal([]byte(fmt.Sprint(e["details"])), &details); err != nil {
			t.Errorf("details of %v are not JSON", e["action"])
		}
	}
}

// pac67AuditEnum reads AuditEntry.action's enum from the bundle.
func pac67AuditEnum(t *testing.T) map[string]bool {
	t.Helper()
	f, err := os.Open(pac21Bundle)
	if err != nil {
		t.Fatalf("open bundle: %v", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	state := 0
	enum := map[string]bool{}
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		switch {
		case state == 0 && line == "    AuditEntry:":
			state = 1
		case state == 1 && line == "        action:":
			state = 2
		case state == 2 && strings.TrimSpace(line) == "enum:":
			state = 3
		case state == 3 && strings.HasPrefix(strings.TrimSpace(line), "- "):
			enum[strings.TrimPrefix(strings.TrimSpace(line), "- ")] = true
		case state == 3:
			return enum
		}
	}
	t.Fatal("AuditEntry.action enum not found in the bundle")
	return nil
}

// CONTRACT-6: every WriteAuditLog call site writes a literal action that is in
// the contract's enum, and every enum value has a writer.
func TestPAC67_AuditEnumMatchesTheWriters(t *testing.T) {
	enum := pac67AuditEnum(t)
	call := regexp.MustCompile(`WriteAuditLog\(`)
	literal := regexp.MustCompile(`WriteAuditLog\([^,]+,\s*[^,]+,\s*"([a-z_]+)"`)
	writers := map[string][]string{}
	err := filepath.Walk("..", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && path != ".." && (info.Name() == "gen" || strings.HasPrefix(info.Name(), ".")) {
			return filepath.SkipDir
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if !call.MatchString(line) || strings.Contains(line, "func WriteAuditLog(") {
				continue
			}
			m := literal.FindStringSubmatch(line)
			if m == nil {
				t.Errorf("%s:%d: WriteAuditLog without a literal action: %s", path, i+1, strings.TrimSpace(line))
				continue
			}
			writers[m[1]] = append(writers[m[1]], fmt.Sprintf("%s:%d", path, i+1))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for a := range writers {
		names = append(names, a)
		if !enum[a] {
			t.Errorf("action %q (%v) is not in the AuditEntry.action enum", a, writers[a])
		}
	}
	for a := range enum {
		if len(writers[a]) == 0 {
			t.Errorf("enum value %q has no writer", a)
		}
	}
	sort.Strings(names)
	if len(enum) != 10 {
		t.Errorf("enum has %d values, want 10: %v", len(enum), enum)
	}
}
