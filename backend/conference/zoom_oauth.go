package conference

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"golang.org/x/oauth2"
)

const zoomAuthURL = "https://zoom.us/oauth/authorize"

// Zoom's token and revocation endpoints. Variables so tests can point them at
// a stand-in; production never changes them.
var (
	ZoomTokenURL  = "https://zoom.us/oauth/token"
	ZoomRevokeURL = "https://zoom.us/oauth/revoke"
)

// ZoomOAuthConfig is Zoom's token endpoint for renewing a stored credential:
// client credentials in Basic auth, as the code exchange sends them (PAC-67).
func ZoomOAuthConfig(clientID, clientSecret string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     oauth2.Endpoint{AuthURL: zoomAuthURL, TokenURL: ZoomTokenURL, AuthStyle: oauth2.AuthStyleInHeader},
	}
}

// ZoomOAuthConfigFromEnv is ZoomOAuthConfig for this server's Zoom app, or
// nil when Zoom is not configured.
func ZoomOAuthConfigFromEnv() *oauth2.Config {
	id := os.Getenv("ZOOM_CLIENT_ID")
	if id == "" {
		return nil
	}
	return ZoomOAuthConfig(id, os.Getenv("ZOOM_CLIENT_SECRET"))
}

// RevokeZoomToken asks Zoom to revoke accessToken (POST /oauth/revoke, client
// credentials in Basic auth). It returns nil only when Zoom answers success,
// {"status":"success"}; anything else, including Zoom being unreachable, is an
// error, which the disconnect reports as an unconfirmed revocation (PAC-67).
func RevokeZoomToken(ctx context.Context, clientID, clientSecret, accessToken string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ZoomRevokeURL, strings.NewReader(url.Values{"token": {accessToken}}.Encode()))
	if err != nil {
		return err
	}
	req.SetBasicAuth(clientID, clientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var body struct {
		Status string `json:"status"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&body)
	if resp.StatusCode != http.StatusOK || body.Status != "success" {
		return fmt.Errorf("zoom revoke: HTTP %d, status %q", resp.StatusCode, body.Status)
	}
	return nil
}

type ZoomTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

func ZoomAuthURL(clientID, redirectURL, state string) string {
	params := url.Values{
		"response_type": {"code"},
		"client_id":     {clientID},
		"redirect_uri":  {redirectURL},
		"state":         {state},
	}
	return zoomAuthURL + "?" + params.Encode()
}

func ZoomExchangeCode(ctx context.Context, clientID, clientSecret, redirectURL, code string) (*ZoomTokens, error) {
	params := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {redirectURL},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ZoomTokenURL, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(clientID, clientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("zoom token: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var tok ZoomTokens
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}
