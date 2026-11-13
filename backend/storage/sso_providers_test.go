package storage

import (
	"testing"

	"github.com/google/uuid"
)

func TestUpsertAndGetSSOProvider(t *testing.T) {
	db := openTestDB(t)

	p, err := UpsertSSOProvider(db, &SSOProvider{
		Domain:       "acme.com",
		ProviderName: "Okta",
		ProviderType: "oidc",
		Enabled:      true,
		OIDCIssuer:   "https://acme.okta.com",
		OIDCClientID: "client-id",
	})
	if err != nil {
		t.Fatalf("UpsertSSOProvider: %v", err)
	}
	if p == nil {
		t.Fatal("expected provider, got nil")
		return
	}
	if p.Domain != "acme.com" {
		t.Errorf("Domain = %q, want acme.com", p.Domain)
	}
	if p.OIDCIssuer != "https://acme.okta.com" {
		t.Errorf("OIDCIssuer = %q, want https://acme.okta.com", p.OIDCIssuer)
	}

	got, err := GetSSOProviderByDomain(db, "acme.com")
	if err != nil {
		t.Fatalf("GetSSOProviderByDomain: %v", err)
	}
	if got == nil {
		t.Fatal("expected provider from DB, got nil")
		return
	}
	if got.ProviderName != "Okta" {
		t.Errorf("ProviderName = %q, want Okta", got.ProviderName)
	}
}

func TestGetSSOProvider_NotFound(t *testing.T) {
	db := openTestDB(t)

	got, err := GetSSOProviderByDomain(db, "unknown.example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for unknown domain, got %+v", got)
	}
}

func TestUpsertSSOProvider_Update(t *testing.T) {
	db := openTestDB(t)

	_, _ = UpsertSSOProvider(db, &SSOProvider{
		Domain:       "corp.example.com",
		ProviderName: "Auth0",
		ProviderType: "oidc",
		Enabled:      true,
		OIDCIssuer:   "https://corp.auth0.com",
	})

	updated, err := UpsertSSOProvider(db, &SSOProvider{
		Domain:       "corp.example.com",
		ProviderName: "Okta",
		ProviderType: "oidc",
		Enabled:      true,
		OIDCIssuer:   "https://corp.okta.com",
	})
	if err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	if updated == nil {
		t.Fatal("expected updated provider")
		return
	}
	if updated.ProviderName != "Okta" {
		t.Errorf("ProviderName after update = %q, want Okta", updated.ProviderName)
	}
}

func TestDeleteSSOProvider(t *testing.T) {
	db := openTestDB(t)

	_, _ = UpsertSSOProvider(db, &SSOProvider{
		Domain:       "delete-me.com",
		ProviderName: "Okta",
		ProviderType: "oidc",
		Enabled:      true,
	})

	if err := DeleteSSOProvider(db, "delete-me.com"); err != nil {
		t.Fatalf("DeleteSSOProvider: %v", err)
	}

	got, err := GetSSOProviderByDomain(db, "delete-me.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Error("expected nil after delete")
	}
}

func TestListSSOProvidersByOrg(t *testing.T) {
	db := openTestDB(t)

	// Create an org for "listtest.com"
	org, err := UpsertOrg(db, "listtest.com", "ListTest")
	if err != nil {
		t.Fatalf("UpsertOrg: %v", err)
	}

	_, _ = UpsertSSOProvider(db, &SSOProvider{
		Domain:       "listtest.com",
		ProviderName: "Okta",
		ProviderType: "oidc",
		Enabled:      true,
	})

	providers, err := ListSSOProvidersByOrg(db, org.ID)
	if err != nil {
		t.Fatalf("ListSSOProvidersByOrg: %v", err)
	}
	if len(providers) != 1 {
		t.Errorf("expected 1 provider, got %d", len(providers))
	}

	// Unknown org returns empty list
	providers2, err := ListSSOProvidersByOrg(db, uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(providers2) != 0 {
		t.Errorf("expected 0 providers for unknown org, got %d", len(providers2))
	}
}

// PAC-22 spec D-13: the kept secret is bound to the issuer and client id.
func TestKeepableClientSecret(t *testing.T) {
	stored := &SSOProvider{ProviderType: "oidc", OIDCIssuer: "https://i", OIDCClientID: "c", OIDCClientSecret: "s"}
	cases := []struct {
		name     string
		current  *SSOProvider
		issuer   string
		clientID string
		want     error
	}{
		{"nothing stored", nil, "https://i", "c", ErrSSOClientSecretRequired},
		{"saml row", &SSOProvider{ProviderType: "saml", OIDCIssuer: "https://i", OIDCClientID: "c", OIDCClientSecret: "s"}, "https://i", "c", ErrSSOClientSecretRequired},
		{"empty stored secret", &SSOProvider{ProviderType: "oidc", OIDCIssuer: "https://i", OIDCClientID: "c"}, "https://i", "c", ErrSSOClientSecretRequired},
		{"same target", stored, "https://i", "c", nil},
		{"issuer changed", stored, "https://evil", "c", ErrSSOClientSecretTargetChanged},
		{"trailing slash is a change", stored, "https://i/", "c", ErrSSOClientSecretTargetChanged},
		{"case is a change", stored, "https://I", "c", ErrSSOClientSecretTargetChanged},
		{"client id changed", stored, "https://i", "c2", ErrSSOClientSecretTargetChanged},
	}
	for _, c := range cases {
		if got := KeepableClientSecret(c.current, c.issuer, c.clientID); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// An empty supplied secret is refused by storage itself, whatever the handler
// let through (PAC-22 AC-26: no write can clear the secret).
func TestSaveOIDCProvider_RefusesAnEmptySuppliedSecret(t *testing.T) {
	db := openTestDB(t)
	d := "pac22-storage-" + uuid.NewString()[:8] + ".test"
	empty := ""

	if _, err := SaveOIDCProvider(db, OIDCProviderWrite{Domain: d, ProviderName: "A", Issuer: "https://i", ClientID: "c", ClientSecret: &empty}); err != ErrSSOClientSecretEmpty {
		t.Fatalf("create with an empty secret: err = %v, want ErrSSOClientSecretEmpty", err)
	}
	if p, _ := GetSSOProviderByDomainAnyState(db, d); p != nil {
		t.Fatal("refused create stored a row")
	}

	secret := "s1"
	if _, err := SaveOIDCProvider(db, OIDCProviderWrite{Domain: d, ProviderName: "A", Issuer: "https://i", ClientID: "c", ClientSecret: &secret}); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveOIDCProvider(db, OIDCProviderWrite{Domain: d, ProviderName: "B", Issuer: "https://i", ClientID: "c", ClientSecret: &empty}); err != ErrSSOClientSecretEmpty {
		t.Fatalf("replace with an empty secret: err = %v, want ErrSSOClientSecretEmpty", err)
	}
	after, _ := GetSSOProviderByDomainAnyState(db, d)
	if after == nil || after.OIDCClientSecret != "s1" || after.ProviderName != "A" {
		t.Fatalf("refused replace changed the row: %+v", after)
	}
}

func TestSaveOIDCProvider_CreateKeepAndRefuse(t *testing.T) {
	db := openTestDB(t)
	d := "pac22-storage-" + uuid.NewString()[:8] + ".test"
	secret := "s1"

	if _, err := SaveOIDCProvider(db, OIDCProviderWrite{Domain: d, ProviderName: "A", Issuer: "https://i", ClientID: "c"}); err != ErrSSOClientSecretRequired {
		t.Fatalf("create without a secret: err = %v, want ErrSSOClientSecretRequired", err)
	}
	if p, _ := GetSSOProviderByDomainAnyState(db, d); p != nil {
		t.Fatal("refused create stored a row")
	}

	created, err := SaveOIDCProvider(db, OIDCProviderWrite{Domain: d, ProviderName: "A", Issuer: "https://i", ClientID: "c", ClientSecret: &secret})
	if err != nil || !created.Enabled || created.OIDCClientSecret != "s1" || created.ProviderType != "oidc" {
		t.Fatalf("create: %+v, %v", created, err)
	}

	if _, err := db.Exec(`UPDATE sso_providers SET enabled = false WHERE domain = $1`, d); err != nil {
		t.Fatal(err)
	}
	kept, err := SaveOIDCProvider(db, OIDCProviderWrite{Domain: d, ProviderName: "B", Issuer: "https://i", ClientID: "c"})
	if err != nil || kept.OIDCClientSecret != "s1" || kept.ProviderName != "B" || kept.Enabled {
		t.Fatalf("replace keeping the secret: %+v, %v (want secret kept, still suspended)", kept, err)
	}

	if _, err := SaveOIDCProvider(db, OIDCProviderWrite{Domain: d, ProviderName: "C", Issuer: "https://evil", ClientID: "c"}); err != ErrSSOClientSecretTargetChanged {
		t.Fatalf("issuer change without a secret: err = %v", err)
	}
	after, _ := GetSSOProviderByDomainAnyState(db, d)
	if after.OIDCIssuer != "https://i" || after.ProviderName != "B" {
		t.Fatalf("refused write changed the row: %+v", after)
	}
}
