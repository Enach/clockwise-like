package api

// PAC-141: the guardrail baseline's unregistered silent defaults in the API.
// A failed read of the caller's user row (recap preview and test send) and of
// the invite's team or inviter row (invite preview) is the operation's
// documented 500, never a 200 with an empty name. Each test fails on the code
// before the fix: the read's error was discarded and the answer was a 200.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// noSlack points every Slack Web API call at a stub that fails the test on
// any request, so a send path under test never reaches slack.com (PAC-141
// review).
func noSlack(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("Slack was called: %s %s", r.Method, r.URL.Path)
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	engine.InstallSlackAPIBase(t, srv.URL)
}

// failUserRead makes GetUserByID fail with err for user only (the test
// database is shared).
func failUserRead(t *testing.T, user uuid.UUID, err error) {
	t.Helper()
	storage.InstallUserRead(t, func(id uuid.UUID) error {
		if id == user {
			return err
		}
		return nil
	})
}

// SF-109: a failed read of the caller's name is the preview's 500, not a
// recap greeting nobody.
func TestPAC141_RecapPreviewUserReadErrorIs500(t *testing.T) {
	db := openTestDB(t)
	h := &dailyRecapHandlers{db: db}
	user := createTestUser(t, "pac141-recap-preview@example.com")
	failUserRead(t, user, errors.New("pac141: user read failed"))
	reported, _ := captureServerErrors(t)

	w := httptest.NewRecorder()
	h.preview(w, withUser(httptest.NewRequest(http.MethodPost, "/api/settings/daily-recap/preview", nil), user))
	assertBody(t, w, http.StatusInternalServerError, `{"error":"db error"}`)
	if len(*reported) == 0 {
		t.Fatal("the failure was not reported")
	}
}

// SF-109: a user row that does not exist is not a failed read: the preview
// is still built, with no name, as before.
func TestPAC141_RecapPreviewMissingUserRowStillPreviews(t *testing.T) {
	db := openTestDB(t)
	h := &dailyRecapHandlers{db: db}
	user := createTestUser(t, "pac141-recap-norow@example.com")
	failUserRead(t, user, sql.ErrNoRows)

	w := httptest.NewRecorder()
	h.preview(w, withUser(httptest.NewRequest(http.MethodPost, "/api/settings/daily-recap/preview", nil), user))
	if w.Code != http.StatusOK {
		t.Fatalf("preview = %d %s, want 200", w.Code, w.Body.String())
	}
}

// SF-109: the test send, with a stored Slack connection: a failed read of
// the caller's name is the 500 and nothing is sent.
func TestPAC141_RecapTestSendUserReadErrorIs500(t *testing.T) {
	db := openTestDB(t)
	user := createTestUser(t, "pac141-recap-test@example.com")
	if err := storage.UpsertWorkspaceConnection(db, &storage.WorkspaceConnection{UserID: user, Provider: "slack", AccessToken: "xoxp-a", BotToken: "xoxb-b", WorkspaceID: "W", Scopes: []string{"chat:write"}}); err != nil {
		t.Fatal(err)
	}
	failUserRead(t, user, errors.New("pac141: user read failed"))
	noSlack(t)
	reported, _ := captureServerErrors(t)

	h := &dailyRecapHandlers{db: db}
	w := httptest.NewRecorder()
	h.test(w, withUser(httptest.NewRequest(http.MethodPost, "/api/settings/daily-recap/test", nil), user))
	assertBody(t, w, http.StatusInternalServerError, `{"error":"db error"}`)
	if len(*reported) == 0 {
		t.Fatal("the failure was not reported")
	}
}

// pac141Invite creates a pending invite from owner and returns the team and
// the invite's token.
func pac141Invite(t *testing.T, r http.Handler, prefix string) (owner uuid.UUID, team *storage.Team, token string) {
	t.Helper()
	owner = createTestUser(t, prefix+"-owner@example.com")
	team = createManagerTestTeam(t, owner, prefix)
	w := managerRequest(t, r, http.MethodPost, "/api/teams/"+team.ID.String()+"/members/invite", owner, map[string]string{"email": prefix + "-invitee@example.com"})
	if w.Code != http.StatusCreated {
		t.Fatalf("invite = %d %s", w.Code, w.Body.String())
	}
	if err := openTestDB(t).QueryRow(`SELECT token FROM team_invites WHERE team_id = $1`, team.ID).Scan(&token); err != nil {
		t.Fatal(err)
	}
	return owner, team, token
}

// SF-110: a failed team or inviter read is the invite preview's 500, never a
// 200 with a blank team name or inviter.
func TestPAC141_InvitePreviewReadErrorsAre500(t *testing.T) {
	r := setupTeamRoutes(t)
	owner, team, token := pac141Invite(t, r, "pac141-invite-err")
	viewer := createTestUser(t, "pac141-invite-err-viewer@example.com")

	t.Run("team", func(t *testing.T) {
		storage.InstallTeamRead(t, func(id uuid.UUID) error {
			if id == team.ID {
				return errors.New("pac141: team read failed")
			}
			return nil
		})
		reported, _ := captureServerErrors(t)
		pac122AssertServerError(t, managerRequest(t, r, http.MethodGet, "/api/teams/invites/"+token, viewer, nil), reported)
	})
	t.Run("inviter", func(t *testing.T) {
		failUserRead(t, owner, errors.New("pac141: inviter read failed"))
		reported, _ := captureServerErrors(t)
		pac122AssertServerError(t, managerRequest(t, r, http.MethodGet, "/api/teams/invites/"+token, viewer, nil), reported)
	})
	t.Run("both readable", func(t *testing.T) {
		w := managerRequest(t, r, http.MethodGet, "/api/teams/invites/"+token, viewer, nil)
		var got teamInvitePreview
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &got) != nil {
			t.Fatalf("preview = %d %s", w.Code, w.Body.String())
		}
		if got.TeamName != "pac141-invite-err" || got.InviterName != "Test User" || got.InviterEmail == nil {
			t.Fatalf("preview = %+v", got)
		}
	})
}

// SF-110: a team or inviter row that does not exist is not a failed read:
// the contract's empty name and null inviter_email still apply.
func TestPAC141_InvitePreviewMissingRowsAreEmpty(t *testing.T) {
	r := setupTeamRoutes(t)
	owner, team, token := pac141Invite(t, r, "pac141-invite-norow")
	viewer := createTestUser(t, "pac141-invite-norow-viewer@example.com")
	storage.InstallTeamRead(t, func(id uuid.UUID) error {
		if id == team.ID {
			return sql.ErrNoRows
		}
		return nil
	})
	failUserRead(t, owner, sql.ErrNoRows)

	w := managerRequest(t, r, http.MethodGet, "/api/teams/invites/"+token, viewer, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("preview = %d %s, want 200", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{`"teamName":""`, `"inviterName":""`, `"inviter_email":null`} {
		if !strings.Contains(body, want) {
			t.Errorf("preview %s lacks %s", body, want)
		}
	}
}

// SF-111 (handler): a failed read of the caller's focus blocks is the
// suggest operation's documented 500, never slots over focus time.
func TestPAC141_SuggestHandlerFocusReadErrorIs500(t *testing.T) {
	newPAC133Google(t, func(ids []string) string {
		return calendarsBody(map[string]string{"primary": freeEntry}, "")(ids)
	})
	user := userWithGoogleToken(t, "pac141-suggest-"+uuid.NewString()+"@example.com")
	db := openTestDB(t)
	h := &scheduleHandlers{eng: &mockCompressor{}, smart: &engine.SmartScheduler{DB: db, OAuthConfig: &oauth2.Config{}}, db: db}
	call := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.suggestMeeting(w, withUser(httptest.NewRequest(http.MethodPost, "/api/schedule/suggest", strings.NewReader(
			`{"duration_minutes":30,"range_start":"2030-01-07T09:00:00Z","range_end":"2030-01-07T17:00:00Z","title":"x"}`)), user))
		return w
	}
	if w := call(); w.Code != http.StatusOK {
		t.Fatalf("setup: status = %d %s", w.Code, w.Body.String())
	}

	storage.InstallFocusBlocksRead(t, func(id uuid.UUID) error {
		if id == user {
			return errors.New("pac141: focus read failed")
		}
		return nil
	})
	reported, _ := captureServerErrors(t)
	w := call()
	assertBody(t, w, http.StatusInternalServerError, `{"error":"internal error"}`)
	if len(*reported) == 0 {
		t.Fatal("the failure was not reported")
	}
}
