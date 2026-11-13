package auth

import (
	"context"
	"testing"

	"github.com/Enach/paceday/backend/storage"
)

func TestDetectAuthProvider_GenericDomain(t *testing.T) {
	db := openTestDB(t)
	result, err := DetectAuthProvider(context.Background(), db, "user@gmail.com")
	if err != nil {
		t.Fatalf("DetectAuthProvider: %v", err)
	}
	if result.Type != "generic" {
		t.Errorf("expected generic for gmail.com, got %q", result.Type)
	}
}

func TestDetectAuthProvider_InvalidEmail(t *testing.T) {
	db := openTestDB(t)
	result, err := DetectAuthProvider(context.Background(), db, "not-an-email")
	if err != nil {
		t.Fatalf("DetectAuthProvider: %v", err)
	}
	if result.Type != "generic" {
		t.Errorf("expected generic for invalid email, got %q", result.Type)
	}
}

func TestDetectAuthProvider_SSODomain(t *testing.T) {
	db := openTestDB(t)

	_, err := storage.UpsertSSOProvider(db, &storage.SSOProvider{
		Domain:       "sso-corp.com",
		ProviderName: "Okta",
		ProviderType: "oidc",
		Enabled:      true,
		OIDCIssuer:   "https://sso-corp.okta.com",
	})
	if err != nil {
		t.Fatalf("UpsertSSOProvider: %v", err)
	}

	result, err := DetectAuthProvider(context.Background(), db, "alice@sso-corp.com")
	if err != nil {
		t.Fatalf("DetectAuthProvider: %v", err)
	}
	if result.Type != "sso" {
		t.Errorf("expected sso for sso-corp.com, got %q", result.Type)
	}
	if result.ProviderName != "Okta" {
		t.Errorf("ProviderName = %q, want Okta", result.ProviderName)
	}
	if result.RedirectURL == "" {
		t.Error("RedirectURL should not be empty for SSO")
	}
}

// TestDetectAuthProvider_CustomDomain_DefaultsGoogle (a live HTTPS call to
// login.microsoftonline.com) and TestIsMicrosoftTenant_Cached (package cache
// seeding) are gone with the probe they tested (PAC-97 AC-14). Their ground
// is covered without live DNS or HTTP by pac97_detect_test.go: AC-1, AC-2
// (every fixture) and AC-9 (the cache).
