package calendar

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/internal/outbound"
	"golang.org/x/oauth2"
)

// graphPublishedURL is Microsoft Graph's address. Production never changes
// it; a test reaches a stand-in through InstallGraphBaseURL (PAC-80 rules).
const graphPublishedURL = "https://graph.microsoft.com/v1.0"

// graphBase is the Graph address every OutlookClient call uses.
func graphBase() string {
	if u, ok := graphBaseURL.get(); ok {
		return u
	}
	return graphPublishedURL
}

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

	resp, err := outbound.ProviderClient().Do(req)
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
		graphBase(),
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

// GetFreeBusy asks Graph's getSchedule about every address in one call and
// answers every distinct address with an outcome (PAC-133 §3.2):
//
//   - a schedule with a non-null error is unknown, with the windows of its
//     items whose status is not free kept;
//   - a schedule without an error is readable with those windows;
//   - a requested address with no schedule is unknown with no windows.
//
// Schedules are matched by scheduleId exactly, else case-insensitively; one
// for an unrequested address is dropped. A failed call (transport, non-2xx,
// undecodable body) is the error.
func (c *OutlookClient) GetFreeBusy(ctx context.Context, emails []string, start, end time.Time) (map[string]FreeBusyEntry, error) {
	emails = distinctIDs(emails)
	url := graphBase() + "/me/calendar/getSchedule"
	payload := map[string]interface{}{
		"schedules":                emails,
		"startTime":                map[string]string{"dateTime": start.UTC().Format(time.RFC3339), "timeZone": "UTC"},
		"endTime":                  map[string]string{"dateTime": end.UTC().Format(time.RFC3339), "timeZone": "UTC"},
		"availabilityViewInterval": 30,
	}
	var resp struct {
		Value []graphSchedule `json:"value"`
	}
	if err := c.doJSON(ctx, http.MethodPost, url, payload, &resp); err != nil {
		return nil, err
	}
	return graphFreeBusyAnswer(emails, resp.Value), nil
}

// graphSchedule is one value[] entry of getSchedule.
type graphSchedule struct {
	ScheduleID    string `json:"scheduleId"`
	ScheduleItems []struct {
		Start  map[string]string `json:"start"`
		End    map[string]string `json:"end"`
		Status string            `json:"status"`
	} `json:"scheduleItems"`
	// Error is Graph's freeBusyError; only its presence is read (§3.2).
	Error json.RawMessage `json:"error"`
}

func (g graphSchedule) hasError() bool {
	e := bytes.TrimSpace(g.Error)
	return len(e) > 0 && !bytes.Equal(e, []byte("null"))
}

func graphFreeBusyAnswer(emails []string, schedules []graphSchedule) map[string]FreeBusyEntry {
	out := make(map[string]FreeBusyEntry, len(emails))
	for _, email := range emails {
		sched, ok := findSchedule(schedules, email)
		if !ok {
			out[email] = FreeBusyEntry{Known: false, Busy: []TimeSlot{}}
			continue
		}
		slots := make([]TimeSlot, 0, len(sched.ScheduleItems))
		for _, item := range sched.ScheduleItems {
			if item.Status == "free" {
				continue
			}
			s, _ := time.Parse("2006-01-02T15:04:05.0000000", item.Start["dateTime"])
			e, _ := time.Parse("2006-01-02T15:04:05.0000000", item.End["dateTime"])
			slots = append(slots, TimeSlot{Start: s.UTC(), End: e.UTC()})
		}
		out[email] = FreeBusyEntry{Known: !sched.hasError(), Busy: slots}
	}
	return out
}

func findSchedule(schedules []graphSchedule, email string) (graphSchedule, bool) {
	for _, s := range schedules {
		if s.ScheduleID == email {
			return s, true
		}
	}
	for _, s := range schedules {
		if strings.EqualFold(s.ScheduleID, email) {
			return s, true
		}
	}
	return graphSchedule{}, false
}

func (c *OutlookClient) CreateEvent(ctx context.Context, e GenericEvent) (string, error) {
	url := graphBase() + "/me/events"
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
