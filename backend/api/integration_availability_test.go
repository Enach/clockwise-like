package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIntegrationAvailabilitySnapshot(t *testing.T) {
	for _, key := range []string{
		"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "GOOGLE_REDIRECT_URL",
		"MICROSOFT_CLIENT_ID", "MICROSOFT_CLIENT_SECRET", "MICROSOFT_REDIRECT_URL",
		"ZOOM_CLIENT_ID", "ZOOM_CLIENT_SECRET", "ZOOM_REDIRECT_URL",
		"SLACK_CLIENT_ID", "SLACK_CLIENT_SECRET", "SLACK_REDIRECT_URI",
		"NOTION_CLIENT_ID", "NOTION_CLIENT_SECRET", "NOTION_REDIRECT_URI",
	} {
		t.Setenv(key, "")
	}

	got := integrationAvailabilitySnapshot()
	for _, provider := range []string{"google", "microsoft", "zoom", "slack", "notion"} {
		if got[provider].Available {
			t.Fatalf("%s should be unavailable when credentials are empty", provider)
		}
		if got[provider].Reason != "missing_credentials" {
			t.Fatalf("%s reason = %q, want missing_credentials", provider, got[provider].Reason)
		}
	}
	if !got["webcal"].Available {
		t.Fatal("webcal should always be available")
	}
}

func TestIntegrationAvailabilityRequiresValidRedirectURI(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "client")
	t.Setenv("GOOGLE_CLIENT_SECRET", "secret")
	t.Setenv("GOOGLE_REDIRECT_URL", "not-a-url")

	got := integrationAvailabilitySnapshot()["google"]
	if got.Available || got.Reason != "invalid_redirect_uri" {
		t.Fatalf("google availability = %+v, want invalid redirect URI", got)
	}

	t.Setenv("GOOGLE_REDIRECT_URL", "http://localhost:8080/api/auth/callback")
	got = integrationAvailabilitySnapshot()["google"]
	if !got.Available || got.Reason != "configured" {
		t.Fatalf("google availability = %+v, want configured", got)
	}
}

func TestIntegrationAvailabilityHandlerDoesNotExposeCredentials(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "client-id-that-must-not-be-returned")
	t.Setenv("GOOGLE_CLIENT_SECRET", "secret-that-must-not-be-returned")
	t.Setenv("GOOGLE_REDIRECT_URL", "http://localhost:8080/api/auth/callback")

	req := httptest.NewRequest(http.MethodGet, "/api/integrations/availability", nil)
	rec := httptest.NewRecorder()
	(&integrationsHandlers{}).availability(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]integrationAvailability
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body["google"].Available {
		t.Fatalf("google availability = %+v, want available", body["google"])
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode response for inspection: %v", err)
	}
	for _, forbidden := range []string{"client-id-that-must-not-be-returned", "secret-that-must-not-be-returned"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("response contains credential value %q", forbidden)
		}
	}
}
