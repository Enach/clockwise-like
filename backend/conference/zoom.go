package conference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const zoomAPIBase = "https://api.zoom.us/v2"

// ZoomProvider creates Zoom meetings via the Zoom REST API v2.
type ZoomProvider struct {
	AccessToken string
}

func (z *ZoomProvider) CreateMeeting(ctx context.Context, title string, start, end time.Time) (*Details, error) {
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, zoomAPIBase+"/users/me/meetings", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+z.AccessToken)
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
