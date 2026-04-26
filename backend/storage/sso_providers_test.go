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
