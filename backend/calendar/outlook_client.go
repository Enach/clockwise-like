package calendar

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

const graphBaseURL = "https://graph.microsoft.com/v1.0"

// OutlookClient calls Microsoft Graph API using an OAuth2 token source.
type OutlookClient struct {
	tokenSource oauth2.TokenSource
}

func NewOutlookClient(tokenSource oauth2.TokenSource) *OutlookClient {
	return &OutlookClient{tokenSource: tokenSource}
}

func (c *OutlookClient) authHeader() (string, error) {
	tok, err := c.tokenSource.Token()
	if err != nil {
		return "", fmt.Errorf("outlook: get token: %w", err)
	}
	return "Bearer " + tok.AccessToken, nil
}

func (c *OutlookClient) doJSON(ctx context.Context, method, url string, body interface{}, out interface{}) error {
	auth, err := c.authHeader()
	if err != nil {
		return err
	}

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("outlook: HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	if out != nil {
		return json.Unmarshal(respBody, out)
	}
	return nil
}

func (c *OutlookClient) ListEvents(ctx context.Context, start, end time.Time) ([]GenericEvent, error) {
	url := fmt.Sprintf("%s/me/calendarView?startDateTime=%s&endDateTime=%s",
		graphBaseURL,
		start.UTC().Format(time.RFC3339),
		end.UTC().Format(time.RFC3339),
	)
	var resp struct {
		Value []struct {
			ID      string `json:"id"`
			Subject string `json:"subject"`
			Start   struct {
				DateTime string `json:"dateTime"`
				TimeZone string `json:"timeZone"`
			} `json:"start"`
			End struct {
				DateTime string `json:"dateTime"`
				TimeZone string `json:"timeZone"`
			} `json:"end"`
		} `json:"value"`
	}
	if err := c.doJSON(ctx, http.MethodGet, url, nil, &resp); err != nil {
		return nil, err
	}
	events := make([]GenericEvent, 0, len(resp.Value))
	for _, v := range resp.Value {
		s, _ := time.Parse("2006-01-02T15:04:05.0000000", v.Start.DateTime)
		e, _ := time.Parse("2006-01-02T15:04:05.0000000", v.End.DateTime)
		events = append(events, GenericEvent{
			ID:    v.ID,
			Title: v.Subject,
			Start: s.UTC(),
			End:   e.UTC(),
		})
	}
	return events, nil
}

func (c *OutlookClient) GetFreeBusy(ctx context.Context, emails []string, start, end time.Time) (map[string][]TimeSlot, error) {
	url := graphBaseURL + "/me/calendar/getSchedule"
	payload := map[string]interface{}{
		"schedules":          emails,
		"startTime":          map[string]string{"dateTime": start.UTC().Format(time.RFC3339), "timeZone": "UTC"},
		"endTime":            map[string]string{"dateTime": end.UTC().Format(time.RFC3339), "timeZone": "UTC"},
		"availabilityViewInterval": 30,
	}
	var resp struct {
		Value []struct {
			ScheduleID   string `json:"scheduleId"`
			ScheduleItems []struct {
				Start  map[string]string `json:"start"`
				End    map[string]string `json:"end"`
				Status string            `json:"status"`
			} `json:"scheduleItems"`
		} `json:"value"`
	}
	if err := c.doJSON(ctx, http.MethodPost, url, payload, &resp); err != nil {
		return nil, err
	}
	result := make(map[string][]TimeSlot)
	for _, v := range resp.Value {
		var slots []TimeSlot
		for _, item := range v.ScheduleItems {
			if item.Status != "free" {
				s, _ := time.Parse("2006-01-02T15:04:05.0000000", item.Start["dateTime"])
				e, _ := time.Parse("2006-01-02T15:04:05.0000000", item.End["dateTime"])
				slots = append(slots, TimeSlot{Start: s.UTC(), End: e.UTC()})
			}
		}
		result[v.ScheduleID] = slots
	}
	return result, nil
}

func (c *OutlookClient) CreateEvent(ctx context.Context, e GenericEvent) (string, error) {
	url := graphBaseURL + "/me/events"
	payload := map[string]interface{}{
		"subject": e.Title,
		"start":   map[string]string{"dateTime": e.Start.UTC().Format("2006-01-02T15:04:05"), "timeZone": "UTC"},
		"end":     map[string]string{"dateTime": e.End.UTC().Format("2006-01-02T15:04:05"), "timeZone": "UTC"},
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := c.doJSON(ctx, http.MethodPost, url, payload, &resp); err != nil {
		return "", err
	}
	return resp.ID, nil
}
