package auth

import (
	"context"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/outbound"
	"github.com/Enach/paceday/backend/internal/outbound/outboundtest"
	"golang.org/x/oauth2"
)

func pac81Identity(t *testing.T) (*outboundtest.Hang, time.Duration) {
	t.Helper()
	l := outbound.Default
	l.Identity = 200 * time.Millisecond
	t.Cleanup(outbound.SetForTest(l))
	return outboundtest.NewHang(t), l.Identity
}

// AC-A3: identity-provider calls (code exchange, OIDC discovery, credential
// renewal) fail on the identity limit against an endpoint that never
// answers, whatever the caller's context.
func TestPAC81_GoogleExchangeFailsWithinLimit(t *testing.T) {
	h, limit := pac81Identity(t)
	cfg := &oauth2.Config{ClientID: "c", ClientSecret: "s", Endpoint: oauth2.Endpoint{TokenURL: h.URL() + "/token"}}
	h.Within(t, limit, func() error {
		_, err := ExchangeCode(context.Background(), cfg, "code")
		return err
	})
}

func TestPAC81_OIDCDiscoveryFailsWithinLimit(t *testing.T) {
	h, limit := pac81Identity(t)
	h.Within(t, limit, func() error {
		_, err := NewOIDCClient(context.Background(), h.URL()+"/pac81-issuer", "c", "s", "http://localhost/cb")
		return err
	})
}

func TestPAC81_RenewalFailsWithinLimit(t *testing.T) {
	db := openTestDB(t)
	user := seedUser(t, db, "pac81-renew@example.com")
	h, limit := pac81Identity(t)
	cfg := &oauth2.Config{ClientID: "c", Endpoint: oauth2.Endpoint{TokenURL: h.URL() + "/token"}}
	expired := &oauth2.Token{AccessToken: "old", RefreshToken: "r", Expiry: time.Now().Add(-time.Hour)}
	if err := UpsertUserToken(db, user, expired); err != nil {
		t.Fatal(err)
	}
	h.Within(t, limit, func() error {
		_, err := UserTokenSource(context.Background(), db, cfg, user, expired).Token()
		return err
	})
}
