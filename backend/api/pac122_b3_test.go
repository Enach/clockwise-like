package api

// PAC-122 phase 2, batch 3 (docs/factory/silent-failures.md §7), the light
// rows of auth and integrations: SF-023 (a refused Notion token exchange is
// not a connection), SF-045 (a failed integration-status read is not "not
// connected"), SF-071 (a failed org association is reported), SF-072 (a
// failed user or SSO lookup is not a 404) and SF-081 (no redirect URI is
// "not configured"). SF-007 is pinned in auth/pac122_b3_test.go. Each test
// fails on the code before the fix.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/lifecycle/lifecycletest"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// SF-023: a Notion token answer that is not a 2xx, or a 2xx with no access
// token, is the documented 502 and stores nothing (before: an empty token
// was stored and the callback redirected to "connected").
func TestPAC122_SF023_RefusedNotionExchangeStoresNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"non-2xx without an error field", http.StatusBadRequest, `{"object":"error","status":400,"code":"invalid_grant","message":"refused"}`},
		{"non-2xx with an empty body", http.StatusServiceUnavailable, `{}`},
		{"2xx without an access token", http.StatusOK, `{"workspace_id":"w","workspace_name":"W"}`},
		{"non-2xx that carries a token", http.StatusUnauthorized, `{"access_token":"leftover","workspace_id":"w"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(srv.Close)
			prev := notionTokenURL
			notionTokenURL = srv.URL
			t.Cleanup(func() { notionTokenURL = prev })

			user := createTestUser(t, "pac122-sf023-"+uuid.NewString()[:8]+"@example.com")
			req := withUser(httptest.NewRequest(http.MethodGet, "/api/integrations/notion/callback?state=s1&code=c1", nil), user)
			req.AddCookie(&http.Cookie{Name: "notion_oauth_state", Value: "s1"})
			rec := httptest.NewRecorder()
			(&integrationsHandlers{db: openTestDB(t)}).notionCallback(rec, req)

			assertBody(t, rec, http.StatusBadGateway, `{"error":"upstream provider error"}`)
			if n := pac122Count(t, `SELECT count(*) FROM workspace_connections WHERE user_id=$1 AND provider='notion'`, user); n != 0 {
				t.Fatalf("%d Notion connections stored, want 0", n)
			}
		})
	}
}

// SF-045: a Slack or Notion status read that fails is the 500, never
// "connected": false. No connection is still not connected (and an
// undecryptable token too: TestPAC84_RecapTestSendUndecryptable).
func TestPAC122_SF045_StatusReadFailureIs500(t *testing.T) {
	for _, tc := range []struct {
		provider string
		handler  func(h *integrationsHandlers) http.HandlerFunc
	}{
		{"slack", func(h *integrationsHandlers) http.HandlerFunc { return h.slackStatus }},
		{"notion", func(h *integrationsHandlers) http.HandlerFunc { return h.notionStatus }},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			user := createTestUser(t, "pac122-sf045-"+tc.provider+"@example.com")
			target := "/api/integrations/" + tc.provider + "/status"

			reported, _ := captureServerErrors(t)
			rec := httptest.NewRecorder()
			tc.handler(&integrationsHandlers{db: closedDB(t)})(rec, withUser(httptest.NewRequest(http.MethodGet, target, nil), user))
			pac122AssertServerError(t, rec, reported)

			rec = httptest.NewRecorder()
			tc.handler(&integrationsHandlers{db: openTestDB(t)})(rec, withUser(httptest.NewRequest(http.MethodGet, target, nil), user))
			assertBody(t, rec, http.StatusOK, `{"connected":false}`)
		})
	}
}

// pac122FailOrgAssociation makes every change of the org of the user
// carrying email fail, until the test ends.
func pac122FailOrgAssociation(t *testing.T, email string) {
	t.Helper()
	db := openTestDB(t)
	name := "pac122_fail_org_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	fn := fmt.Sprintf(`CREATE FUNCTION %[1]s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.email = %[2]s AND NEW.org_id IS DISTINCT FROM OLD.org_id THEN
				RAISE EXCEPTION 'pac122: forced org association failure';
			END IF;
			RETURN NEW;
		END $$`, name, quoteLiteral(email))
	if _, err := db.Exec(fn); err != nil {
		t.Fatalf("install failure function: %v", err)
	}
	if _, err := db.Exec(fmt.Sprintf(`CREATE TRIGGER %[1]s BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION %[1]s()`, name)); err != nil {
		t.Fatalf("install failure trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON users`, name))
		_, _ = db.Exec(fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, name))
	})
}

// pac122AssertOrgAssociationReported checks that the sign-in went through,
// that email's account has no stored org, and that the failure was reported
// once under the account's id, with neither the address nor its domain. The
// stored users.org_id is read by SQL: User.OrgID is the effective
// organisation since PAC-148 (contract md §2.3), nil for every non-member
// whatever is stored, so it cannot show that the association failed.
func pac122AssertOrgAssociationReported(t *testing.T, rec *lifecycletest.Recorder, email string) {
	t.Helper()
	pac122AssertOrgFailureReported(t, rec, email, uuid.NullUUID{})
}

// pac122AssertOrgFailureReported is pac122AssertOrgAssociationReported with
// the stored org_id the failed step must have left (want; invalid: none).
func pac122AssertOrgFailureReported(t *testing.T, rec *lifecycletest.Recorder, email string, want uuid.NullUUID) {
	t.Helper()
	db := openTestDB(t)
	u, err := storage.GetUserByEmail(db, email)
	if err != nil || u == nil {
		t.Fatalf("account for the sign-in: %v", err)
	}
	if raw := pac148RawOrg(t, db, u.ID); raw != want {
		t.Fatalf("the account's stored org_id is %v, want %v (the failed step rolled back)", raw, want)
	}
	got := rec.Matching("org-association", "")
	if len(got) != 1 || got[0].Unit.ID != u.ID.String() {
		t.Fatalf("org-association reports = %+v, want one for %s", rec.All(), u.ID)
	}
	domain := email[strings.Index(email, "@")+1:]
	if msg := got[0].Err.Error() + got[0].Unit.String(); strings.Contains(msg, email) || strings.Contains(msg, domain) {
		t.Fatalf("the report carries the address or its domain: %q", msg)
	}
}

// SF-071: a sign-in whose org association fails still signs the user in
// (the next sign-in applies the rule again), and the failure is reported by
// the account's id (before: `_ =`, nothing logged or reported).
func TestPAC122_SF071_OrgAssociationFailureIsReported(t *testing.T) {
	t.Run("google", func(t *testing.T) {
		rec := lifecycletest.Record(t)
		email := "pac122-sf071-google-" + uuid.NewString()[:8] + "@example.com"
		pac122FailOrgAssociation(t, email)
		p := newFakeOAuthProvider(t)
		// PAC-148 (contract md §8): domain-confirmed at its own domain (hd),
		// so the sign-in step takes the join and the trigger fires; a
		// mailbox-evidence account (no hd) would write nothing.
		p.profile = jsonProfile(map[string]any{"verified_email": true, "hd": "example.com", "id": "g-sf071-" + uuid.NewString()[:8], "email": email, "name": "G"})
		assertSignedIn(t, runGoogleCallback(t, &authHandlers{db: openTestDB(t), jwtSecret: "secret"}, p), pac35Frontend+"/auth/callback")
		pac122AssertOrgAssociationReported(t, rec, email)
	})
	t.Run("microsoft", func(t *testing.T) {
		rec := lifecycletest.Record(t)
		email := "pac122-sf071-ms-" + uuid.NewString()[:8] + "@example.com"
		pac122FailOrgAssociation(t, email)
		p := newFakeOAuthProvider(t)
		p.profile = jsonProfile(map[string]string{"id": uuid.NewString(), "mail": email, "displayName": "M"})
		assertSignedIn(t, runMicrosoftCallback(t, &authHandlers{db: openTestDB(t), jwtSecret: "secret"}, p), pac35Frontend+"/auth/callback?provider=outlook")
		pac122AssertOrgAssociationReported(t, rec, email)
	})
	t.Run("sso", func(t *testing.T) {
		rec := lifecycletest.Record(t)
		d := "pac122-sf071-" + uuid.NewString()[:8] + ".test"
		idp := newFakeIdP(t, "client-"+d)
		seedOIDCProvider(t, d, idp)
		idp.email = "alice@" + d
		pac122FailOrgAssociation(t, idp.email)
		w := httptest.NewRecorder()
		ssoRouter(&authHandlers{db: openTestDB(t), jwtSecret: "secret", frontendURL: pac35Frontend}).
			ServeHTTP(w, oidcCallbackRequest(d, &http.Cookie{Name: "sso_state", Value: "s1|" + d}))
		assertSignedIn(t, w, pac35Frontend+"/auth/callback?provider=sso")
		pac122AssertOrgAssociationReported(t, rec, idp.email)
	})
	// PAC-148 (contract md §2.4, §8, revision 3): the leave branch is reported
	// too. A mailbox-bound (unconfirmed) account does not qualify, so its
	// sign-in step ends its stored association; that UPDATE is forced to fail.
	t.Run("leave", func(t *testing.T) {
		db := openTestDB(t)
		d := "pac122-sf071-leave-" + uuid.NewString()[:8] + ".test"
		email := "u@" + d
		profile := map[string]any{"verified_email": true, "id": "g-sf071-leave-" + uuid.NewString()[:8], "email": email, "name": "U"}
		p := newFakeOAuthProvider(t)
		p.profile = jsonProfile(profile)
		// First sign-in: creates and binds the account on mailbox evidence;
		// it does not qualify and has no association, so nothing is written.
		assertSignedIn(t, runGoogleCallback(t, &authHandlers{db: db, jwtSecret: "secret"}, p), pac35Frontend+"/auth/callback")
		u, err := storage.GetUserByEmail(db, email)
		if err != nil || u == nil || u.AddressStanding != storage.AddressUnconfirmed {
			t.Fatalf("mailbox-bound account = %+v, %v", u, err)
		}
		// Plant an association and a confirmation, then install the trigger.
		org, err := storage.UpsertOrg(db, d, d)
		if err != nil {
			t.Fatal(err)
		}
		pac148Plant(t, db, u.ID, org.ID)
		if _, err := db.Exec(`INSERT INTO org_ai_confirmations (user_id, org_id, consent_since_version, confirmed_at) VALUES ($1, $2, 1, now())`, u.ID, org.ID); err != nil {
			t.Fatal(err)
		}
		pac122FailOrgAssociation(t, email)
		recorded := pac148Record(t)
		rec := lifecycletest.Record(t)
		// Second sign-in: PAC-71 rule 1 reaches the bound identity.
		assertSignedIn(t, runGoogleCallback(t, &authHandlers{db: db, jwtSecret: "secret"}, p), pac35Frontend+"/auth/callback")
		steps := recorded()
		if !pac148Contains(steps, "membership-leave") || pac148Contains(steps, "org-upsert") || pac148Contains(steps, "membership-set") {
			t.Fatalf("sign-in step statements = %v, want the leave branch (membership-leave, no org-upsert or membership-set)", steps)
		}
		pac122AssertOrgFailureReported(t, rec, email, uuid.NullUUID{UUID: org.ID, Valid: true})
		if n := pac122Count(t, `SELECT count(*) FROM org_ai_confirmations WHERE user_id=$1`, u.ID); n != 1 {
			t.Fatalf("%d organisation-AI confirmations after the failed leave, want 1 (rolled back)", n)
		}
	})
}

// SF-072: a user or SSO-provider lookup that fails is the documented 500,
// never 404 "user not found" / "SSO not configured for this domain". The
// 404s for an absent row are pinned by TestPAC35_CurrentUser_EveryDocumentedStatus,
// TestPAC35_SSOStart_EveryDocumentedStatusAndHint and
// TestPAC35_OIDCCallback_EveryDocumentedStatus.
func TestPAC122_SF072_LookupFailureIs500(t *testing.T) {
	t.Run("current user", func(t *testing.T) {
		reported, _ := captureServerErrors(t)
		rec := httptest.NewRecorder()
		(&meHandlers{db: closedDB(t)}).me(rec, withUser(httptest.NewRequest(http.MethodGet, "/api/auth/me", nil), uuid.New()))
		pac122AssertServerError(t, rec, reported)
	})
	ah := &authHandlers{db: closedDB(t), jwtSecret: "secret", frontendURL: pac35Frontend}
	const d = "pac122-sf072.test"
	t.Run("sso start", func(t *testing.T) {
		reported, _ := captureServerErrors(t)
		rec := httptest.NewRecorder()
		ssoRouter(ah).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/sso/"+d, nil))
		pac122AssertServerError(t, rec, reported)
	})
	t.Run("sso callback", func(t *testing.T) {
		reported, _ := captureServerErrors(t)
		rec := httptest.NewRecorder()
		ssoRouter(ah).ServeHTTP(rec, oidcCallbackRequest(d, &http.Cookie{Name: "sso_state", Value: "s1|" + d}))
		pac122AssertServerError(t, rec, reported)
		if len(setCookieNamed(rec, "auth_token")) != 0 {
			t.Error("the 500 touched auth_token")
		}
	})
}

// SF-081: a Slack or Notion connect with a client id but no redirect URI is
// the documented 503 "not configured" (before: a redirect to the provider's
// own error page). With both set it still redirects.
func TestPAC122_SF081_NoRedirectURIIsNotConfigured(t *testing.T) {
	for _, tc := range []struct {
		provider, env, msg string
		handler            func(h *integrationsHandlers) http.HandlerFunc
	}{
		{"slack", "SLACK", "Slack not configured", func(h *integrationsHandlers) http.HandlerFunc { return h.slackConnect }},
		{"notion", "NOTION", "Notion not configured", func(h *integrationsHandlers) http.HandlerFunc { return h.notionConnect }},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			t.Setenv(tc.env+"_CLIENT_ID", "client")
			t.Setenv(tc.env+"_REDIRECT_URI", "")
			target := "/api/integrations/" + tc.provider + "/connect"
			rec := httptest.NewRecorder()
			tc.handler(&integrationsHandlers{})(rec, httptest.NewRequest(http.MethodGet, target, nil))
			assertBody(t, rec, http.StatusServiceUnavailable, `{"error":"`+tc.msg+`"}`)
			if len(setCookieNamed(rec, tc.provider+"_oauth_state")) != 0 {
				t.Error("a state cookie was set although nothing was started")
			}

			t.Setenv(tc.env+"_REDIRECT_URI", "http://localhost/cb")
			rec = httptest.NewRecorder()
			tc.handler(&integrationsHandlers{})(rec, httptest.NewRequest(http.MethodGet, target, nil))
			if rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), "redirect_uri=http%3A%2F%2Flocalhost%2Fcb") {
				t.Fatalf("configured connect = %d %q, want 302 to the provider", rec.Code, rec.Header().Get("Location"))
			}
		})
	}
}

// SF-045 keeps PAC-84 AC-16b for Notion: a stored connection whose token
// cannot be decrypted is "connected": false (200), never the 500.
func TestPAC122_SF045_NotionUndecryptableIsNotConnected(t *testing.T) {
	db := openTestDB(t)
	user := createTestUser(t, "pac122-sf045-notion-pac84@example.com")
	if err := storage.UpsertWorkspaceConnection(db, &storage.WorkspaceConnection{
		UserID: user, Provider: "notion", AccessToken: "secret_n", WorkspaceID: "W", WorkspaceName: "Docs", Scopes: []string{}, ConnectedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE workspace_connections SET access_token = $2 WHERE user_id = $1 AND provider = 'notion'`, user, undecryptable); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	(&integrationsHandlers{db: db}).notionStatus(rec, withUser(httptest.NewRequest(http.MethodGet, "/api/integrations/notion/status", nil), user))
	assertBody(t, rec, http.StatusOK, `{"connected":false}`)
}
