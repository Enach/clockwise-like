package conference

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const zoomAuthURL  = "https://zoom.us/oauth/authorize"
const zoomTokenURL = "https://zoom.us/oauth/token"

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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, zoomTokenURL, strings.NewReader(params.Encode()))
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
