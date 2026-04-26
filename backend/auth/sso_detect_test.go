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

func TestDetectAuthProvider_CustomDomain_DefaultsGoogle(t *testing.T) {
	db := openTestDB(t)
	// "example.internal" is not generic, not SSO-configured, not a MS tenant
	// (MS heuristic will fail for a non-existent domain — that's fine, defaults to Google)
	result, err := DetectAuthProvider(context.Background(), db, "user@paceday-test-nonexistent.internal")
	if err != nil {
		t.Fatalf("DetectAuthProvider: %v", err)
	}
	// Should be google or microsoft depending on heuristic, but not generic or sso
	if result.Type == "generic" || result.Type == "sso" {
		t.Errorf("unexpected type %q for custom domain", result.Type)
	}
}

func TestIsMicrosoftTenant_Cached(t *testing.T) {
	// Seed cache with a known result
	cacheTenant("cached-domain.example", true)

	result, err := isMicrosoftTenant(context.Background(), "cached-domain.example")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("expected cached result to be true")
	}
}
