package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// PAC-34 AC-1: the invite preview carries team_id, expires_at and inviter_email
// beside the three keys it already had, and nothing else.
func TestPAC34InvitePreviewCarriesTeamExpiryAndInviter(t *testing.T) {
	r := setupTeamRoutes(t)
	db := openTestDB(t)
	inviterID := createTestUser(t, "pac34-inviter@example.com")
	callerID := createTestUser(t, "pac34-caller@example.com")
	team, err := storage.CreateTeam(db, "PAC-34 Team", inviterID)
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	invite, err := storage.CreateTeamInvite(db, team.ID, "pac34-invitee@example.com", inviterID)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}

	req := withUser(httptest.NewRequest(http.MethodGet, "/api/teams/invites/"+invite.Token, nil), callerID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v; body: %s", err, w.Body.String())
	}
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if want := "email,expires_at,inviterName,inviter_email,teamName,team_id"; strings.Join(keys, ",") != want {
		t.Fatalf("keys = %v, want exactly %s", keys, want)
	}
	if got["team_id"] != team.ID.String() {
		t.Errorf("team_id = %v, want %s", got["team_id"], team.ID)
	}
	expRaw, _ := got["expires_at"].(string)
	exp, err := time.Parse(time.RFC3339, expRaw)
	if err != nil {
		t.Fatalf("expires_at %q is not RFC 3339: %v", expRaw, err)
	}
	if !exp.Equal(invite.ExpiresAt) {
		t.Errorf("expires_at = %s, want the stored %s", exp, invite.ExpiresAt)
	}
	if got["inviter_email"] != "pac34-inviter@example.com" {
		t.Errorf("inviter_email = %v, want pac34-inviter@example.com", got["inviter_email"])
	}
	if got["inviterName"] != "Test User" || got["teamName"] != "PAC-34 Team" || got["email"] != "pac34-invitee@example.com" {
		t.Errorf("existing keys changed: %v", got)
	}
}

// PAC-34 AC-1 / challenger N-4: an inviter with no usable email is sent as null,
// never as "", so the format: email promise holds.
func TestPAC34InvitePreviewEmptyInviterEmailIsNull(t *testing.T) {
	r := setupTeamRoutes(t)
	db := openTestDB(t)
	var inviterID uuid.UUID
	if err := db.QueryRow(
		`INSERT INTO users (email, name) VALUES ('', '') ON CONFLICT (email) DO UPDATE SET name=EXCLUDED.name RETURNING id`,
	).Scan(&inviterID); err != nil {
		t.Fatalf("create inviter: %v", err)
	}
	callerID := createTestUser(t, "pac34-caller-null@example.com")
	team, err := storage.CreateTeam(db, "PAC-34 Null", inviterID)
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	invite, err := storage.CreateTeamInvite(db, team.ID, "pac34-null@example.com", inviterID)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	req := withUser(httptest.NewRequest(http.MethodGet, "/api/teams/invites/"+invite.Token, nil), callerID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	v, present := got["inviter_email"]
	if !present || v != nil {
		t.Fatalf("inviter_email = %#v (present %v), want null; body: %s", v, present, w.Body.String())
	}
}

// PAC-34 AC-1: the error answers are unchanged.
func TestPAC34InvitePreviewErrorsUnchanged(t *testing.T) {
	r := setupTeamRoutes(t)
	db := openTestDB(t)
	inviterID := createTestUser(t, "pac34-inviter-errs@example.com")
	callerID := createTestUser(t, "pac34-caller-errs@example.com")
	team, err := storage.CreateTeam(db, "PAC-34 Errs", inviterID)
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	mk := func(email string) *storage.TeamInvite {
		inv, err := storage.CreateTeamInvite(db, team.ID, email, inviterID)
		if err != nil {
			t.Fatalf("create invite: %v", err)
		}
		return inv
	}
	accepted := mk("pac34-acc@example.com")
	if err := storage.AcceptTeamInvite(db, accepted.ID); err != nil {
		t.Fatalf("accept: %v", err)
	}
	declined := mk("pac34-dec@example.com")
	if _, err := db.Exec(`UPDATE team_invites SET status='declined' WHERE id=$1`, declined.ID); err != nil {
		t.Fatalf("decline: %v", err)
	}
	expired := mk("pac34-exp@example.com")
	if _, err := db.Exec(`UPDATE team_invites SET expires_at=NOW() - INTERVAL '1 hour' WHERE id=$1`, expired.ID); err != nil {
		t.Fatalf("expire: %v", err)
	}

	for _, tc := range []struct {
		token string
		code  int
		body  string
	}{
		{"pac34-no-such-token", http.StatusNotFound, `{"error":"invite not found"}`},
		{accepted.Token, http.StatusGone, `{"error":"invite is accepted"}`},
		{declined.Token, http.StatusGone, `{"error":"invite is declined"}`},
		{expired.Token, http.StatusGone, `{"error":"invite is expired"}`},
	} {
		req := withUser(httptest.NewRequest(http.MethodGet, "/api/teams/invites/"+tc.token, nil), callerID)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != tc.code || strings.TrimSpace(w.Body.String()) != tc.body {
			t.Errorf("%s: got %d %s, want %d %s", tc.token, w.Code, w.Body.String(), tc.code, tc.body)
		}
	}
}

type pac34AnalyticsMember struct {
	Email         string `json:"email"`
	IsPacedayUser *bool  `json:"is_paceday_user"`
	DataAvailable *bool  `json:"data_available"`
	Weeks         []struct {
		WeekStart     string `json:"week_start"`
		FocusMinutes  int    `json:"focus_minutes"`
		DataAvailable bool   `json:"data_available"`
	} `json:"weeks"`
}

func (m pac34AnalyticsMember) measuredWeeks() int {
	n := 0
	for _, wk := range m.Weeks {
		if wk.DataAvailable {
			n++
		}
	}
	return n
}

// PAC-34 AC-3: every analytics member carries is_paceday_user and a member-level
// data_available equal to any(weeks[].data_available), through the real engine
// (PAC-60 gate included).
func TestPAC34ManagerAnalyticsMemberFlags(t *testing.T) {
	db := openTestDB(t)
	h := newManagerHandlers(db, nil)
	managerID := createTestUser(t, "pac34-manager@example.com")
	ownedID := createTestUser(t, "pac34-owned@example.com")
	unownedID := createTestUser(t, "pac34-unowned@example.com")

	team, err := storage.CreateTeam(db, "PAC-34 Analytics", managerID)
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	if err := storage.AddTeamMember(db, team.ID, managerID, "owner"); err != nil {
		t.Fatalf("add owner: %v", err)
	}
	if err := storage.AddTeamMember(db, team.ID, ownedID, "member"); err != nil {
		t.Fatalf("add member: %v", err)
	}
	for _, uid := range []uuid.UUID{ownedID, unownedID} {
		if _, err := storage.GetOrCreateUserProfile(db, uid); err != nil {
			t.Fatalf("profile: %v", err)
		}
		// Bucket 1 (2026-02-23) of the 12 ending at 2026-03-02 is the only measured week.
		if _, err := db.Exec(`INSERT INTO analytics_weeks (user_id, week_start, focus_minutes, meeting_minutes, free_minutes)
			VALUES ($1, '2026-02-23', 300, 120, 600)`, uid); err != nil {
			t.Fatalf("analytics_weeks: %v", err)
		}
	}
	for _, m := range []*storage.ManagerTeamMember{
		{ManagerUserID: managerID, MemberEmail: "pac34-external@gmail.com", DisplayName: "External", Source: "manual", Cadence: "none"},
		{ManagerUserID: managerID, MemberEmail: "pac34-owned@example.com", MemberUserID: &ownedID, DisplayName: "Owned", Source: "manual", Cadence: "none"},
		{ManagerUserID: managerID, MemberEmail: "pac34-unowned@example.com", MemberUserID: &unownedID, DisplayName: "Unowned", Source: "manual", Cadence: "none"},
	} {
		if err := storage.UpsertManagerTeamMember(db, m); err != nil {
			t.Fatalf("upsert member: %v", err)
		}
	}

	w := managerRequest(t, http.HandlerFunc(h.getAnalytics), http.MethodGet, "/api/manager/analytics?week=2026-03-02", managerID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Members []pac34AnalyticsMember `json:"members"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byEmail := map[string]pac34AnalyticsMember{}
	for _, m := range resp.Members {
		if m.IsPacedayUser == nil || m.DataAvailable == nil {
			t.Fatalf("member %s lacks is_paceday_user or data_available; body: %s", m.Email, w.Body.String())
		}
		if len(m.Weeks) != 12 {
			t.Fatalf("member %s has %d weeks, want 12", m.Email, len(m.Weeks))
		}
		if *m.DataAvailable != (m.measuredWeeks() > 0) {
			t.Errorf("member %s: data_available = %v but %d buckets measured", m.Email, *m.DataAvailable, m.measuredWeeks())
		}
		byEmail[m.Email] = m
	}
	for _, tc := range []struct {
		email    string
		paceday  bool
		data     bool
		measured int
	}{
		{"pac34-external@gmail.com", false, false, 0},
		{"pac34-owned@example.com", true, true, 1},
		{"pac34-unowned@example.com", true, false, 0},
	} {
		m, ok := byEmail[tc.email]
		if !ok {
			t.Fatalf("member %s missing; body: %s", tc.email, w.Body.String())
		}
		if *m.IsPacedayUser != tc.paceday || *m.DataAvailable != tc.data || m.measuredWeeks() != tc.measured {
			t.Errorf("%s: is_paceday_user=%v data_available=%v measured=%d, want %v %v %d",
				tc.email, *m.IsPacedayUser, *m.DataAvailable, m.measuredWeeks(), tc.paceday, tc.data, tc.measured)
		}
	}
	for _, wk := range byEmail["pac34-unowned@example.com"].Weeks {
		if wk.FocusMinutes != 0 {
			t.Errorf("unowned member leaked focus minutes %d in %s (PAC-60)", wk.FocusMinutes, wk.WeekStart)
		}
	}
	if wk := byEmail["pac34-owned@example.com"].Weeks; wk[1].WeekStart != "2026-02-23" || !wk[1].DataAvailable || wk[1].FocusMinutes != 300 {
		t.Errorf("owned member bucket 1 = %+v, want 2026-02-23 measured with 300 focus minutes", wk[1])
	}
}
