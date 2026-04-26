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

const graphBase = "https://graph.microsoft.com/v1.0"

// TeamsProvider creates Microsoft Teams online meetings via Microsoft Graph.
type TeamsProvider struct {
	AccessToken string
}

func (t *TeamsProvider) CreateMeeting(ctx context.Context, title string, start, end time.Time) (*Details, error) {
	payload := map[string]interface{}{
		"subject":       title,
		"startDateTime": start.UTC().Format(time.RFC3339),
		"endDateTime":   end.UTC().Format(time.RFC3339),
	}
	data, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, graphBase+"/me/onlineMeetings", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+t.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
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
