package calendar

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/emersion/go-ical"
)

// WebcalClient fetches and parses read-only iCal (.ics) feeds.
// Responses are cached for 15 minutes.
type WebcalClient struct {
	URL string

	mu        sync.Mutex
	cached    []GenericEvent
	cachedAt  time.Time
	cacheTTL  time.Duration
}

func NewWebcalClient(url string) *WebcalClient {
	return &WebcalClient{URL: url, cacheTTL: 15 * time.Minute}
}

func (c *WebcalClient) ListEvents(ctx context.Context, start, end time.Time) ([]GenericEvent, error) {
	all, err := c.fetch(ctx)
	if err != nil {
		return nil, err
	}
	var events []GenericEvent
	for _, e := range all {
		if e.End.After(start) && e.Start.Before(end) {
			events = append(events, e)
		}
	}
	return events, nil
}

func (c *WebcalClient) GetFreeBusy(_ context.Context, emails []string, start, end time.Time) (map[string][]TimeSlot, error) {
	return map[string][]TimeSlot{}, nil
}

func (c *WebcalClient) CreateEvent(_ context.Context, _ GenericEvent) (string, error) {
	return "", ErrReadOnly
}

func (c *WebcalClient) fetch(ctx context.Context) ([]GenericEvent, error) {
	c.mu.Lock()
	if time.Since(c.cachedAt) < c.cacheTTL && c.cached != nil {
		events := c.cached
		c.mu.Unlock()
		return events, nil
	}
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("webcal: fetch: %w", err)
	}
	defer resp.Body.Close()

	events, err := parseICS(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("webcal: parse: %w", err)
	}

	c.mu.Lock()
	c.cached = events
	c.cachedAt = time.Now()
	c.mu.Unlock()

	return events, nil
}

func parseICS(r io.Reader) ([]GenericEvent, error) {
	dec := ical.NewDecoder(r)
	cal, err := dec.Decode()
	if err != nil {
		return nil, err
	}
	var events []GenericEvent
	for _, comp := range cal.Children {
		if comp.Name != ical.CompEvent {
			continue
		}
		uid := comp.Props.Get(ical.PropUID)
		summary := comp.Props.Get(ical.PropSummary)
		dtstart := comp.Props.Get(ical.PropDateTimeStart)
		dtend := comp.Props.Get(ical.PropDateTimeEnd)

		if dtstart == nil || dtend == nil {
			continue
		}
		startTime, err := dtstart.DateTime(time.UTC)
		if err != nil {
			continue
		}
		endTime, err := dtend.DateTime(time.UTC)
		if err != nil {
			continue
		}

		id := ""
		if uid != nil {
			id = uid.Value
		}
		title := ""
		if summary != nil {
			title = summary.Value
		}
		events = append(events, GenericEvent{
			ID:    id,
			Title: title,
			Start: startTime,
			End:   endTime,
		})
	}
	return events, nil
}
