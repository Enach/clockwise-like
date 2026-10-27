package conference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/oauth2"
)

// ZoomAPIBase is the Zoom REST API root. A variable so tests can point it at a
// stand-in; production never changes it.
var ZoomAPIBase = "https://api.zoom.us/v2"

// ZoomProvider creates Zoom meetings via the Zoom REST API v2. Tokens, when
// set, supplies the access token (PAC-67: the caller's renewing Zoom
// credential); otherwise AccessToken is used as is.
type ZoomProvider struct {
	AccessToken string
	Tokens      oauth2.TokenSource
}

func (z *ZoomProvider) CreateMeeting(ctx context.Context, title string, start, end time.Time) (*Details, error) {
	accessToken, err := bearer(z.Tokens, z.AccessToken)
	if err != nil {
		return nil, err
	}
	payload := map[string]interface{}{
		"topic":      title,
		"type":       2, // scheduled meeting
		"start_time": start.UTC().Format("2006-01-02T15:04:05Z"),
		"duration":   int(end.Sub(start).Minutes()),
		"settings": map[string]interface{}{
			"join_before_host": true,
		},
	}
	data, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ZoomAPIBase+"/users/me/meetings", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("zoom: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		ID       int64  `json:"id"`
		JoinURL  string `json:"join_url"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return &Details{
		Provider:  "zoom",
		JoinURL:   result.JoinURL,
		MeetingID: fmt.Sprintf("%d", result.ID),
		Password:  result.Password,
	}, nil
}
