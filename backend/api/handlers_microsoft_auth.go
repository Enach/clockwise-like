package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"golang.org/x/oauth2"
)

func (h *authHandlers) startMicrosoftOAuth(w http.ResponseWriter, r *http.Request) {
	cfg := microsoftConfig()
	if cfg == nil {
		writeError(w, "Microsoft OAuth not configured", http.StatusServiceUnavailable)
		return
	}

	b := make([]byte, 16)
	_, _ = rand.Read(b)
	state := hex.EncodeToString(b)

	setCookie(w, &http.Cookie{
		Name:     "ms_oauth_state",
		Value:    state,
		Path:     "/",
		MaxAge:   300,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, auth.GetMicrosoftAuthURL(cfg, state, loginHintFrom(r)), http.StatusFound)
}


// Test seams: Microsoft's OAuth endpoint (zero means the real one) and the
// Graph profile URL.
var (
	microsoftOAuthEndpoint oauth2.Endpoint
	microsoftGraphMeURL    = "https://graph.microsoft.com/v1.0/me?$select=id,mail,displayName"
)

type msUserInfo struct {
	ID    string `json:"id"`
	Email string `json:"mail"`
	Name  string `json:"displayName"`
}

func fetchMicrosoftUserInfo(ctx context.Context, token *oauth2.Token) (*msUserInfo, error) {
	client := oauth2.NewClient(ctx, oauth2.StaticTokenSource(token))
	client.Timeout = 5 * time.Second
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, microsoftGraphMeURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var info msUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}
	return &info, nil
}

func microsoftConfig() *auth.MicrosoftConfig {
	clientID := os.Getenv("MICROSOFT_CLIENT_ID")
	clientSecret := os.Getenv("MICROSOFT_CLIENT_SECRET")
	redirectURL := os.Getenv("MICROSOFT_REDIRECT_URL")
	if clientID == "" {
		return nil
	}
	return &auth.MicrosoftConfig{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Endpoint:     microsoftOAuthEndpoint,
	}
}
