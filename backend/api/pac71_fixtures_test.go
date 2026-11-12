package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/google/uuid"
)

// confirmAddress gives a fixture account a domain-confirmed address (PAC-71):
// it binds the account to a fixture SSO identity, so the PAC-50 step-up's
// identity check 4b (Google or Microsoft bindings only) is not involved, and
// sets its standing. Granting sites (team invites, co-hosts, the manager's
// roster) and break-glass resolve only such accounts (contract
// consumersAffected).
func confirmAddress(t testing.TB, db *sql.DB, id uuid.UUID) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO user_identities (user_id, provider, tenant, subject, evidence, bound_by)
		VALUES ($1, 'oidc', 'https://fixture-idp.test', $2, 'authoritative', 'created')
		ON CONFLICT (user_id) DO NOTHING`, id, id.String()); err != nil {
		t.Fatalf("confirmAddress bind: %v", err)
	}
	if _, err := db.Exec(`UPDATE users SET address_standing = 'domain_confirmed' WHERE id = $1`, id); err != nil {
		t.Fatalf("confirmAddress standing: %v", err)
	}
}

// createConfirmedUser is createTestUser with a domain-confirmed address.
func createConfirmedUser(t *testing.T, email string) uuid.UUID {
	t.Helper()
	id := createTestUser(t, email)
	confirmAddress(t, openTestDB(t), id)
	return id
}

// connectCallbackRequest builds the provider's return to a connect: the
// state the start would have issued for (user, gen, provider), its cookie,
// and the browser's session for sessionUser at sessionGen (uuid.Nil: no
// session). code "" means the provider returned without one.
func connectCallbackRequest(t testing.TB, secret, path, cookie, provider string, user uuid.UUID, gen int64, sessionUser uuid.UUID, sessionGen int64, code string) *http.Request {
	t.Helper()
	st, err := signConnectState(secret, connectState{User: user, Gen: gen, Provider: provider, Expires: time.Now().Add(time.Minute).Unix(), Nonce: "n"})
	if err != nil {
		t.Fatal(err)
	}
	target := path + "?state=" + url.QueryEscape(st)
	if code != "" {
		target += "&code=" + url.QueryEscape(code)
	} else {
		target += "&error=access_denied"
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.AddCookie(&http.Cookie{Name: cookie, Value: st})
	if sessionUser != uuid.Nil {
		tok, err := auth.GenerateSessionToken(sessionUser, "x@example.com", sessionGen, secret)
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(&http.Cookie{Name: "auth_token", Value: tok})
	}
	return req
}
