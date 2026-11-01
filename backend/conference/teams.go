package conference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Enach/paceday/backend/internal/outbound"
	"golang.org/x/oauth2"
)

// bearer returns the access token to send: the source's when there is one
// (renewing it if it expired), else the fixed one.
func bearer(src oauth2.TokenSource, fixed string) (string, error) {
	if src == nil {
		return fixed, nil
	}
	tok, err := src.Token()
	if err != nil {
		return "", err
	}
	return tok.AccessToken, nil
}

// GraphBase is Microsoft Graph's root. A variable so tests can point it at a
// stand-in; production never changes it.
var GraphBase = "https://graph.microsoft.com/v1.0"

// TeamsProvider creates Microsoft Teams online meetings via Microsoft Graph.
// Tokens, when set, supplies the access token (PAC-67: the caller's renewing
// Outlook credential); otherwise AccessToken is used as is.
type TeamsProvider struct {
	AccessToken string
	Tokens      oauth2.TokenSource
}

func (t *TeamsProvider) CreateMeeting(ctx context.Context, title string, start, end time.Time) (*Details, error) {
	accessToken, err := bearer(t.Tokens, t.AccessToken)
	if err != nil {
		return nil, err
	}
	payload := map[string]interface{}{
		"subject":       title,
		"startDateTime": start.UTC().Format(time.RFC3339),
		"endDateTime":   end.UTC().Format(time.RFC3339),
	}
	data, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, GraphBase+"/me/onlineMeetings", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := outbound.ProviderClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("teams: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		ID      string `json:"id"`
		JoinURL string `json:"joinWebUrl"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return &Details{
		Provider:  "teams",
		JoinURL:   result.JoinURL,
		MeetingID: result.ID,
	}, nil
}
