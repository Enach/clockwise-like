package calendar

import (
	"context"
	"fmt"
	"time"

	"github.com/Enach/clockwise-like/backend/storage"
	googlecalendar "google.golang.org/api/calendar/v3"
	"golang.org/x/oauth2"
)

// NewProvider creates the appropriate calendar Provider based on settings.
func NewProvider(ctx context.Context, s *storage.Settings, googleTS oauth2.TokenSource) (Provider, error) {
	switch s.CalendarProvider {
	case "outlook":
		if s.MicrosoftTokens == "" {
			return nil, fmt.Errorf("outlook: no Microsoft tokens configured")
		}
		tok := &oauth2.Token{AccessToken: msAccessToken(s.MicrosoftTokens)}
		return NewOutlookClient(oauth2.StaticTokenSource(tok)), nil
	case "webcal":
		if s.WebcalURL == "" {
			return nil, fmt.Errorf("webcal: no URL configured")
		}
		return NewWebcalClient(s.WebcalURL), nil
	default: // "google"
		client, err := NewClient(ctx, googleTS)
		if err != nil {
			return nil, err
		}
		return &googleProvider{c: client}, nil
	}
}

// googleProvider adapts *CalendarClient to implement Provider.
type googleProvider struct{ c *CalendarClient }

func (g *googleProvider) ListEvents(ctx context.Context, start, end time.Time) ([]GenericEvent, error) {
	items, err := g.c.ListEvents(ctx, g.c.CalendarID, start, end)
	if err != nil {
		return nil, err
	}
	events := make([]GenericEvent, 0, len(items))
	for _, it := range items {
		s, _ := time.Parse(time.RFC3339, firstNonEmpty(it.Start.DateTime, it.Start.Date))
		e, _ := time.Parse(time.RFC3339, firstNonEmpty(it.End.DateTime, it.End.Date))
		events = append(events, GenericEvent{ID: it.Id, Title: it.Summary, Start: s, End: e})
	}
	return events, nil
}

func (g *googleProvider) GetFreeBusy(ctx context.Context, emails []string, start, end time.Time) (map[string][]TimeSlot, error) {
	return g.c.GetFreeBusy(ctx, emails, start, end)
}

func (g *googleProvider) CreateEvent(ctx context.Context, e GenericEvent) (string, error) {
	created, err := g.c.CreateEvent(ctx, g.c.CalendarID, &googlecalendar.Event{
		Summary: e.Title,
		Start:   &googlecalendar.EventDateTime{DateTime: e.Start.UTC().Format(time.RFC3339)},
		End:     &googlecalendar.EventDateTime{DateTime: e.End.UTC().Format(time.RFC3339)},
	})
	if err != nil {
		return "", err
	}
	return created.Id, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// msAccessToken is a placeholder; full token refresh uses auth.LoadMicrosoftToken.
func msAccessToken(tokensJSON string) string { return "" }
