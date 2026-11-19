package api

// PAC-142 (docs/factory/silent-failures.md SF-113): GET /api/auth/me answers
// its documented 500 when the caller's organisation cannot be read, never a
// 200 user with `org` left out ("no organisation"). Readers that never use
// the organisation (GetUserByIDNoOrg) are not failed by it.

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

func pac142Org(t *testing.T, user uuid.UUID, tag string) uuid.UUID {
	t.Helper()
	db := openTestDB(t)
	org, err := storage.UpsertOrg(db, "pac142-"+tag+"-"+uuid.NewString()[:8]+".example", "Org "+tag)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.SetUserOrg(db, user, org.ID); err != nil {
		t.Fatal(err)
	}
	return org.ID
}

func failOrgRead(t *testing.T, org uuid.UUID, err error) {
	t.Helper()
	storage.InstallOrgRead(t, func(id uuid.UUID) error {
		if id == org {
			return err
		}
		return nil
	})
}

func TestPAC142_MeOrgReadErrorIs500(t *testing.T) {
	db := openTestDB(t)
	user := createTestUser(t, "pac142-me-orgerr@example.com")
	org := pac142Org(t, user, "me")
	failOrgRead(t, org, errors.New("pac142: org read failed"))
	reported, _ := captureServerErrors(t)

	w := httptest.NewRecorder()
	(&meHandlers{db: db}).me(w, withUser(httptest.NewRequest(http.MethodGet, "/api/auth/me", nil), user))
	assertBody(t, w, http.StatusInternalServerError, `{"error":"internal error"}`)
	if len(*reported) == 0 {
		t.Fatal("the failure was not reported")
	}
}

func TestPAC142_MeCarriesTheOrg(t *testing.T) {
	db := openTestDB(t)
	user := createTestUser(t, "pac142-me-orgok@example.com")
	pac142Org(t, user, "meok")

	w := httptest.NewRecorder()
	(&meHandlers{db: db}).me(w, withUser(httptest.NewRequest(http.MethodGet, "/api/auth/me", nil), user))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"org":{`) {
		t.Fatalf("me = %d %s, want 200 with org", w.Code, w.Body.String())
	}
}

// Caller audit: GET /api/org/members reads only OrgID. A failed org read
// must not turn it into the SF-075 `[]` (it never reads the org row).
func TestPAC142_OrgMembersUnaffectedByOrgReadError(t *testing.T) {
	db := openTestDB(t)
	user := createTestUser(t, "pac142-members@example.com")
	org := pac142Org(t, user, "members")
	failOrgRead(t, org, errors.New("pac142: org read failed"))

	w := httptest.NewRecorder()
	(&orgHandlers{db: db}).members(w, withUser(httptest.NewRequest(http.MethodGet, "/api/org/members", nil), user))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "pac142-members@example.com") {
		t.Fatalf("members = %d %s, want the caller listed", w.Code, w.Body.String())
	}
}
