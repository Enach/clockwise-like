package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// PAC-50 spec §4.2: the write gate on POST and DELETE /api/admin/sso.

func wantStepupRequired(t *testing.T, rec interface {
	Result() *http.Response
}, code int, body map[string]any) {
	t.Helper()
	if code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%v)", code, body)
	}
	// newSSOEnv configures both providers, so both keys are present
	// (contract revision 6, AC-24).
	got, _ := json.Marshal(body)
	if string(got) != bothProvidersStepupBody {
		t.Fatalf("body = %s, want exactly %s", got, bothProvidersStepupBody)
	}
}

// AC-1a and the first Linear acceptance item.
func TestSSOWrite_MemberWithoutAuthorityIs403AndNothingChanges(t *testing.T) {
	e := newSSOEnv(t)
	u, org := e.member(t, "ac1a")
	sess := session(t, u, time.Now())

	rec := e.do(t, http.MethodPost, "/api/admin/sso", createBody(org.Domain), sess)
	wantStepupRequired(t, rec, rec.Code, errorBody(t, rec))
	if n, _ := e.providerRow(t, org.Domain); n != 0 {
		t.Fatalf("create wrote %d rows", n)
	}

	// Update (a second POST) and delete of an existing row.
	if _, err := storage.UpsertSSOProvider(e.db, &storage.SSOProvider{Domain: org.Domain, ProviderName: "Seed", ProviderType: "oidc", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	_, before := e.providerRow(t, org.Domain)
	rec = e.do(t, http.MethodPost, "/api/admin/sso", createBody(org.Domain), sess)
	wantStepupRequired(t, rec, rec.Code, errorBody(t, rec))
	rec = e.do(t, http.MethodDelete, "/api/admin/sso/"+org.Domain, "", sess)
	wantStepupRequired(t, rec, rec.Code, errorBody(t, rec))
	n, after := e.providerRow(t, org.Domain)
	if n != 1 || !after.Equal(before) {
		t.Fatalf("rows = %d, updated_at %v -> %v; want 1 and unchanged", n, before, after)
	}
	if ev := e.events(t, u.ID); len(ev) != 0 {
		t.Fatalf("refused writes were recorded: %v", ev)
	}
}

// AC-1b: membership is checked before break-glass; no stepup_url, no rows.
func TestSSOWrite_OrglessCallerGetsMembershipRefusal(t *testing.T) {
	listed := "pac50-orgless-listed@gmail.com"
	e := newSSOEnv(t, listed)
	for _, email := range []string{listed, "pac50-orgless-other@gmail.com"} {
		u, org := e.memberOf(t, email)
		if org != nil {
			t.Fatalf("%s has an org", email)
		}
		sess := session(t, u, time.Now())
		for _, req := range []struct{ method, path, body string }{
			{http.MethodPost, "/api/admin/sso", createBody("gmail.com")},
			{http.MethodDelete, "/api/admin/sso/gmail.com", ""},
		} {
			rec := e.do(t, req.method, req.path, req.body, sess)
			body := errorBody(t, rec)
			if rec.Code != http.StatusForbidden || body["error"] != "org membership required" || len(body) != 1 {
				t.Fatalf("%s %s %s = %d %v; want 403 {error: org membership required}", email, req.method, req.path, rec.Code, body)
			}
		}
		// D-1: the step-up start refuses the same caller and sets no state.
		rec := e.do(t, http.MethodGet, "/api/admin/sso/stepup?provider=google", "", sess)
		if rec.Code != http.StatusForbidden || errorBody(t, rec)["error"] != "org membership required" || stateCookie(rec) != nil {
			t.Fatalf("start for an orgless caller = %d %s", rec.Code, rec.Body.String())
		}
		if e.liveGrants(t, u.ID) != 0 || len(e.events(t, u.ID)) != 0 {
			t.Fatal("an orgless caller left a grant or an event")
		}
	}
}

func issueGrant(t *testing.T, e *ssoEnv, user, org uuid.UUID, at time.Time) *storage.SSOStepupGrant {
	t.Helper()
	g, err := storage.IssueSSOStepupGrant(context.Background(), e.db, user, org, "google", storage.SSOGrantPurposeSSOWrite, at)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func grantConsumed(t *testing.T, e *ssoEnv, id uuid.UUID) bool {
	t.Helper()
	g, err := storage.GetSSOStepupGrant(context.Background(), e.db, id)
	if err != nil || g == nil {
		t.Fatalf("GetSSOStepupGrant: %v", err)
	}
	return g.ConsumedAt != nil
}

// AC-2a: a grant is bound to the org it was issued for.
func TestSSOWrite_GrantForAnotherOrgDoesNotAuthorize(t *testing.T) {
	e := newSSOEnv(t)
	u, orgA := e.member(t, "ac2a-a")
	_, orgB := e.member(t, "ac2a-b")
	g := issueGrant(t, e, u.ID, orgA.ID, e.clock.Now())
	if err := storage.SetUserOrg(e.db, u.ID, orgB.ID); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, http.MethodPost, "/api/admin/sso", createBody(orgB.Domain), session(t, u, time.Now()))
	wantStepupRequired(t, rec, rec.Code, errorBody(t, rec))
	if grantConsumed(t, e, g.ID) {
		t.Fatal("the (U, A) grant was consumed")
	}
}

// AC-2b: a grant is bound to the user it was issued to.
func TestSSOWrite_AnotherUsersGrantDoesNotAuthorize(t *testing.T) {
	e := newSSOEnv(t)
	v, org := e.member(t, "ac2b")
	u, _ := e.memberOf(t, "colleague@"+org.Domain)
	g := issueGrant(t, e, v.ID, org.ID, e.clock.Now())
	rec := e.do(t, http.MethodPost, "/api/admin/sso", createBody(org.Domain), session(t, u, time.Now()))
	wantStepupRequired(t, rec, rec.Code, errorBody(t, rec))
	if grantConsumed(t, e, g.ID) {
		t.Fatal("V's grant was consumed by U")
	}
}

// AC-3a.
func TestSSOWrite_OneGrantOneWrite(t *testing.T) {
	e := newSSOEnv(t)
	u, org := e.member(t, "ac3a")
	issueGrant(t, e, u.ID, org.ID, e.clock.Now())
	sess := session(t, u, time.Now())
	first := e.do(t, http.MethodPost, "/api/admin/sso", createBody(org.Domain), sess)
	if first.Code != http.StatusCreated {
		t.Fatalf("first write = %d %s", first.Code, first.Body.String())
	}
	// Contract revision 8: the 201 is PAC-22's SsoProvider, exactly its
	// thirteen snake_case keys, with a lower-cased domain and no secret.
	got := errorBody(t, first)
	for _, k := range []string{"id", "domain", "provider_name", "provider_type", "state", "oidc_issuer", "oidc_client_id",
		"has_oidc_client_secret", "saml_entry_point", "saml_issuer", "has_saml_cert", "created_at", "updated_at"} {
		if _, ok := got[k]; !ok {
			t.Errorf("201 body lacks %s", k)
		}
	}
	if len(got) != 13 || got["domain"] != org.Domain || got["has_oidc_client_secret"] != true || strings.Contains(first.Body.String(), `"sec"`) {
		t.Errorf("201 body = %v; want 13 keys, domain %s, no secret", got, org.Domain)
	}
	rec := e.do(t, http.MethodPost, "/api/admin/sso", createBody(org.Domain), sess)
	wantStepupRequired(t, rec, rec.Code, errorBody(t, rec))
}

// AC-3b: two writes released together on one grant.
func TestSSOWrite_ConcurrentWritesOnOneGrant(t *testing.T) {
	e := newSSOEnv(t)
	u, org := e.member(t, "ac3b")
	g := issueGrant(t, e, u.ID, org.ID, e.clock.Now())
	sess := session(t, u, time.Now())
	codes := make([]int, 2)
	var wg sync.WaitGroup
	barrier := make(chan struct{})
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-barrier
			codes[i] = e.do(t, http.MethodPost, "/api/admin/sso", createBody(org.Domain), sess).Code
		}(i)
	}
	close(barrier)
	wg.Wait()
	if !((codes[0] == 201 && codes[1] == 403) || (codes[0] == 403 && codes[1] == 201)) {
		t.Fatalf("codes = %v, want one 201 and one 403", codes)
	}
	if !grantConsumed(t, e, g.ID) {
		t.Fatal("grant not consumed")
	}
	if ev := e.events(t, u.ID); len(ev) != 1 {
		t.Fatalf("%d write events, want 1", len(ev))
	}
}

// AC-3c, AC-16b and the second Linear acceptance item: a foreign domain, a
// subdomain or a padded domain is a 403 provider_domain_mismatch (owner,
// 2026-10-27, spec §9 D-10) before the grant is touched; nothing is written
// or deleted.
func TestSSOWrite_DomainMismatchLeavesGrantLive(t *testing.T) {
	e := newSSOEnv(t)
	u, org := e.member(t, "ac3c")
	g := issueGrant(t, e, u.ID, org.ID, e.clock.Now())
	sess := session(t, u, time.Now())
	// A row for a foreign domain exists, so a mismatched delete that slipped
	// through would remove it.
	if _, err := e.db.Exec(`INSERT INTO sso_providers (domain, provider_name, provider_type) VALUES ('other.com', 'Theirs', 'oidc') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("seed foreign row: %v", err)
	}
	for _, d := range []string{"other.com", "other.test", "sub." + org.Domain, "evil-" + org.Domain, " " + org.Domain + " "} {
		rec := e.do(t, http.MethodPost, "/api/admin/sso", createBody(d), sess)
		wantDomainMismatch(t, "create "+d, rec)
		if d != "other.com" {
			if n, _ := e.providerRow(t, d); n != 0 {
				t.Fatalf("a row was written for %s", d)
			}
		}
		rec = e.do(t, http.MethodDelete, "/api/admin/sso/"+url.PathEscape(d), "", sess)
		wantDomainMismatch(t, "delete "+d, rec)
	}
	if n, _ := e.providerRow(t, "other.com"); n != 1 {
		t.Fatalf("foreign row count = %d after mismatched writes, want 1 (untouched)", n)
	}
	if n, _ := e.providerRow(t, org.Domain); n != 0 {
		t.Fatal("a mismatched write created the org's row")
	}
	if ev := e.events(t, u.ID); len(ev) != 0 {
		t.Fatalf("%d write events after mismatches, want 0", len(ev))
	}
	if grantConsumed(t, e, g.ID) {
		t.Fatal("a domain mismatch consumed the grant")
	}
	if rec := e.do(t, http.MethodPost, "/api/admin/sso", createBody(org.Domain), sess); rec.Code != http.StatusCreated {
		t.Fatalf("valid write after the mismatches = %d %s", rec.Code, rec.Body.String())
	}
}

type failingSSOStore struct {
	ssoStore
	failWrite, failRecord, failDelete, failRead, failConsume bool
}

func (f *failingSSOStore) deleteProvider(ctx context.Context, d string, actor, org uuid.UUID, via string) (bool, error) {
	if f.failDelete {
		return false, errors.New("injected store failure")
	}
	return f.ssoStore.deleteProvider(ctx, d, actor, org, via)
}

func (f *failingSSOStore) getProvider(ctx context.Context, d string) (*storage.SSOProvider, error) {
	if f.failRead {
		return nil, errors.New("injected store failure")
	}
	return f.ssoStore.getProvider(ctx, d)
}

func (f *failingSSOStore) consumeGrant(ctx context.Context, user, org uuid.UUID, now time.Time) (bool, error) {
	if f.failConsume {
		return false, errors.New("injected store failure")
	}
	return f.ssoStore.consumeGrant(ctx, user, org, now)
}

func (f *failingSSOStore) saveProvider(ctx context.Context, w storage.OIDCProviderWrite, actor, org uuid.UUID, via string) (*storage.SSOProvider, error) {
	if f.failWrite {
		return nil, errors.New("injected store failure")
	}
	return f.ssoStore.saveProvider(ctx, w, actor, org, via)
}

func (f *failingSSOStore) recordStepup(ctx context.Context, o storage.SSOStepupOutcome) (*storage.SSOStepupGrant, error) {
	if f.failRecord {
		return nil, errors.New("injected store failure")
	}
	return f.ssoStore.recordStepup(ctx, o)
}

// AC-3d: a failure after the consume leaves the grant used.
func TestSSOWrite_StoreFailureConsumesGrant(t *testing.T) {
	e := newSSOEnv(t)
	u, org := e.member(t, "ac3d")
	g := issueGrant(t, e, u.ID, org.ID, e.clock.Now())
	real := e.h.store
	e.h.store = &failingSSOStore{ssoStore: real, failWrite: true}
	sess := session(t, u, time.Now())
	rec := e.do(t, http.MethodPost, "/api/admin/sso", createBody(org.Domain), sess)
	wantInternalError(t, "create after the gate", rec, rec.Code, rec.Body.String())
	if !grantConsumed(t, e, g.ID) {
		t.Fatal("grant not consumed by a write that passed the gate")
	}
	e.h.store = real
	rec = e.do(t, http.MethodPost, "/api/admin/sso", createBody(org.Domain), sess)
	wantStepupRequired(t, rec, rec.Code, errorBody(t, rec))
}

// AC-4a / AC-4b: the 10-minute lifetime, on the injectable clock.
func TestSSOWrite_GrantLifetime(t *testing.T) {
	e := newSSOEnv(t)
	u, org := e.member(t, "ac4")
	sess := session(t, u, time.Now())
	t0 := e.clock.Now()

	g := issueGrant(t, e, u.ID, org.ID, t0)
	e.clock.Set(t0.Add(11 * time.Minute))
	rec := e.do(t, http.MethodPost, "/api/admin/sso", createBody(org.Domain), sess)
	wantStepupRequired(t, rec, rec.Code, errorBody(t, rec))
	if grantConsumed(t, e, g.ID) {
		t.Fatal("an expired grant was consumed")
	}

	issueGrant(t, e, u.ID, org.ID, t0.Add(11*time.Minute))
	e.clock.Set(t0.Add(20 * time.Minute))
	if rec := e.do(t, http.MethodPost, "/api/admin/sso", createBody(org.Domain), sess); rec.Code != http.StatusCreated {
		t.Fatalf("write at T+9 = %d %s", rec.Code, rec.Body.String())
	}
}

// AC-14a..d: the break-glass list and its fresh-session rule.
func TestSSOWrite_BreakGlass(t *testing.T) {
	t.Run("listed with a fresh session writes, recorded as breakglass (AC-14a)", func(t *testing.T) {
		e := newSSOEnv(t)
		u, org := e.member(t, "ac14a")
		e = newSSOEnv(t, " someone@else.test ", u.Email)
		rec := e.do(t, http.MethodPost, "/api/admin/sso", createBody(org.Domain), session(t, u, time.Now().Add(-5*time.Minute)))
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
		}
		ev := e.events(t, u.ID)
		if len(ev) != 1 || ev[0].Kind != "sso_provider_written" || ev[0].Details["via"] != "breakglass" || ev[0].Details["op"] != "create" {
			t.Fatalf("events = %v", ev)
		}
	})
	t.Run("listed with a stale session is told to sign in again (AC-14b)", func(t *testing.T) {
		e := newSSOEnv(t)
		u, org := e.member(t, "ac14b")
		e = newSSOEnv(t, u.Email)
		rec := e.do(t, http.MethodPost, "/api/admin/sso", createBody(org.Domain), session(t, u, time.Now().Add(-11*time.Minute)))
		body := errorBody(t, rec)
		if rec.Code != http.StatusForbidden || body["error"] != "sso_write_requires_fresh_login" || len(body) != 1 {
			t.Fatalf("= %d %v; want 403 {error: sso_write_requires_fresh_login}", rec.Code, body)
		}
		if n, _ := e.providerRow(t, org.Domain); n != 0 {
			t.Fatal("a stale break-glass session wrote a row")
		}
	})
	t.Run("the list is matched lower-cased (AC-14c)", func(t *testing.T) {
		e := newSSOEnv(t)
		u, org := e.member(t, "ac14c")
		e = newSSOEnv(t, strings.ToUpper(u.Email))
		rec := e.do(t, http.MethodPost, "/api/admin/sso", createBody(org.Domain), session(t, u, time.Now()))
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("no list means step-up is required (AC-14d)", func(t *testing.T) {
		e := newSSOEnv(t)
		u, org := e.member(t, "ac14d")
		rec := e.do(t, http.MethodPost, "/api/admin/sso", createBody(org.Domain), session(t, u, time.Now()))
		wantStepupRequired(t, rec, rec.Code, errorBody(t, rec))
	})
}

// AC-16a: an undeclared property is a 400, never silently dropped.
func TestSSOWrite_UnknownPropertyIs400(t *testing.T) {
	e := newSSOEnv(t)
	u, org := e.member(t, "ac16a")
	e = newSSOEnv(t, u.Email)
	body := strings.TrimSuffix(createBody(org.Domain), "}") + `,"org_id":"` + uuid.NewString() + `"}`
	rec := e.do(t, http.MethodPost, "/api/admin/sso", body, session(t, u, time.Now()))
	if rec.Code != http.StatusBadRequest || errorBody(t, rec)["error"] != "unknown property: org_id" {
		t.Fatalf("= %d %s; want 400 unknown property: org_id", rec.Code, rec.Body.String())
	}
	if n, _ := e.providerRow(t, org.Domain); n != 0 {
		t.Fatal("a row was written")
	}
	for _, bad := range []string{`not json`, `{"domain":"` + org.Domain + `"}`, strings.Replace(createBody(org.Domain), `"oidc"`, `"ldap"`, 1)} {
		if rec := e.do(t, http.MethodPost, "/api/admin/sso", bad, session(t, u, time.Now())); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", bad, rec.Code)
		}
	}
}

// AC-16c / AC-16d (API-067): delete is exact, lower-cased, and 404 on nothing.
func TestSSODelete_AbsentIs404AndCasingIsIgnored(t *testing.T) {
	e := newSSOEnv(t)
	u, org := e.member(t, "ac16")
	e = newSSOEnv(t, u.Email)
	sess := session(t, u, time.Now())

	rec := e.do(t, http.MethodDelete, "/api/admin/sso/"+org.Domain, "", sess)
	if rec.Code != http.StatusNotFound || errorBody(t, rec)["error"] != "sso provider not found" {
		t.Fatalf("delete absent = %d %s; want 404 sso provider not found", rec.Code, rec.Body.String())
	}
	if ev := e.events(t, u.ID); len(ev) != 0 {
		t.Fatalf("a 404 delete was recorded: %v", ev)
	}

	if _, err := storage.UpsertSSOProvider(e.db, &storage.SSOProvider{Domain: org.Domain, ProviderName: "Seed", ProviderType: "oidc", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	rec = e.do(t, http.MethodDelete, "/api/admin/sso/"+strings.ToUpper(org.Domain), "", sess)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete upper-cased = %d %s; want 204", rec.Code, rec.Body.String())
	}
	if n, _ := e.providerRow(t, org.Domain); n != 0 {
		t.Fatal("row still present")
	}
	ev := e.events(t, u.ID)
	if len(ev) != 1 || ev[0].Details["op"] != "delete" {
		t.Fatalf("events = %v", ev)
	}
}

// AC-20b: another casing of the same domain updates the one row.
func TestSSOWrite_CasingUpdatesTheSameRow(t *testing.T) {
	e := newSSOEnv(t)
	u, org := e.member(t, "ac20b")
	e = newSSOEnv(t, u.Email)
	if _, err := storage.UpsertSSOProvider(e.db, &storage.SSOProvider{Domain: org.Domain, ProviderName: "Seed", ProviderType: "oidc", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	_, before := e.providerRow(t, org.Domain)
	rec := e.do(t, http.MethodPost, "/api/admin/sso", createBody(strings.ToUpper(org.Domain)), session(t, u, time.Now()))
	if rec.Code != http.StatusCreated {
		t.Fatalf("= %d %s", rec.Code, rec.Body.String())
	}
	n, after := e.providerRow(t, org.Domain)
	if n != 1 || !after.After(before) {
		t.Fatalf("rows = %d, updated_at %v -> %v", n, before, after)
	}
	if body := errorBody(t, rec); body["domain"] != org.Domain {
		t.Fatalf("stored domain = %v, want %s", body["domain"], org.Domain)
	}
	if ev := e.events(t, u.ID); len(ev) != 1 || ev[0].Details["op"] != "update" {
		t.Fatalf("events = %v", ev)
	}
}

func TestSSOAdmin_NoSessionIs401(t *testing.T) {
	e := newSSOEnv(t)
	for _, req := range []struct{ m, p string }{
		{http.MethodPost, "/api/admin/sso"}, {http.MethodDelete, "/api/admin/sso/x.test"},
		{http.MethodGet, "/api/admin/sso/stepup?provider=google"},
	} {
		if rec := e.do(t, req.m, req.p, "", ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401", req.m, req.p, rec.Code)
		}
	}
}
