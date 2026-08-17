package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type integrationAvailability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// integrationAvailabilitySnapshot reports whether a provider can be started
// with the current server configuration. It deliberately never returns any
// credential values.
func integrationAvailabilitySnapshot() map[string]integrationAvailability {
	return map[string]integrationAvailability{
		"google":    oauthProviderAvailability("GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "GOOGLE_REDIRECT_URL"),
		"microsoft": oauthProviderAvailability("MICROSOFT_CLIENT_ID", "MICROSOFT_CLIENT_SECRET", "MICROSOFT_REDIRECT_URL"),
		"zoom":      oauthProviderAvailability("ZOOM_CLIENT_ID", "ZOOM_CLIENT_SECRET", "ZOOM_REDIRECT_URL"),
		"slack":     oauthProviderAvailability("SLACK_CLIENT_ID", "SLACK_CLIENT_SECRET", "SLACK_REDIRECT_URI"),
		"notion":    oauthProviderAvailability("NOTION_CLIENT_ID", "NOTION_CLIENT_SECRET", "NOTION_REDIRECT_URI"),
		"webcal":    {Available: true, Reason: "built_in"},
	}
}

func oauthProviderAvailability(clientIDKey, clientSecretKey, redirectKey string) integrationAvailability {
	if strings.TrimSpace(os.Getenv(clientIDKey)) == "" ||
		strings.TrimSpace(os.Getenv(clientSecretKey)) == "" ||
		strings.TrimSpace(os.Getenv(redirectKey)) == "" {
		return integrationAvailability{Reason: "missing_credentials"}
	}

	redirectURI, err := url.Parse(strings.TrimSpace(os.Getenv(redirectKey)))
	if err != nil || redirectURI.Host == "" || (redirectURI.Scheme != "http" && redirectURI.Scheme != "https") {
		return integrationAvailability{Reason: "invalid_redirect_uri"}
	}

	return integrationAvailability{Available: true, Reason: "configured"}
}

func (h *integrationsHandlers) availability(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(integrationAvailabilitySnapshot())
}
