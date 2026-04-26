package auth

import (
	"testing"

	"golang.org/x/oauth2"
)

func TestNewGoogleOAuthConfig(t *testing.T) {
	config := NewGoogleOAuthConfig("client-id", "client-secret", "http://localhost/callback")
	if config == nil {
		t.Fatal("config should not be nil")
	}
	if config.ClientID != "client-id" {
		t.Errorf("ClientID = %q, want client-id", config.ClientID)
	}
	if config.ClientSecret != "client-secret" {
		t.Errorf("ClientSecret = %q, want client-secret", config.ClientSecret)
	}
	if config.RedirectURL != "http://localhost/callback" {
		t.Errorf("RedirectURL = %q, want http://localhost/callback", config.RedirectURL)
	}
	if len(config.Scopes) == 0 {
		t.Error("Scopes should not be empty")
	}
}

func TestGetAuthURL(t *testing.T) {
	config := NewGoogleOAuthConfig("client-id", "secret", "http://localhost/callback")
	url := GetAuthURL(config, "test-state")
	if url == "" {
		t.Error("URL should not be empty")
	}
	// Should contain state parameter
	if len(url) < 10 {
		t.Error("URL is too short")
	}
}

func TestTokenSource(t *testing.T) {
	config := &oauth2.Config{ClientID: "test", ClientSecret: "secret"}
	token := &oauth2.Token{AccessToken: "acc", RefreshToken: "ref"}
	ts := TokenSource(nil, config, token)
	if ts == nil {
		t.Error("TokenSource should not be nil")
	}
}
