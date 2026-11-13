package calendar

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Enach/paceday/backend/internal/outbound"
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
	// PAC-81 AC-A17: the fetch is bounded in time and size. Over either
	// limit the feed is not parsed at all (no blocker from a partial feed),
	// and the error names the limit that was hit.
	limits := outbound.Current()
	resp, err := outbound.ICSClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("webcal: fetch: %w", nameTimeLimit(err, limits.ICS))
	}
	defer resp.Body.Close()
	body, err := outbound.ReadLimited(resp.Body, limits.ICSMaxBytes)
	if errors.Is(err, outbound.ErrTooLarge) {
		return nil, fmt.Errorf("webcal: feed exceeds the %d-byte size limit: %w", limits.ICSMaxBytes, err)
	}
	if err != nil {
		return nil, fmt.Errorf("webcal: read: %w", nameTimeLimit(err, limits.ICS))
	}

	events, err := parseICS(bytes.NewReader(body))
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

// nameTimeLimit says so when err is the fetch's own time limit.
func nameTimeLimit(err error, limit time.Duration) error {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return fmt.Errorf("feed exceeded the %s time limit: %w", limit, err)
	}
	return err
}
