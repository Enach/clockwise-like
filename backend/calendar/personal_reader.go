package calendar

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Enach/paceday/backend/storage"
	"golang.org/x/oauth2"
)

// ReadPersonalEvents fetches events from a personal calendar using the stored provider config.
func ReadPersonalEvents(ctx context.Context, pc *storage.PersonalCalendar, start, end time.Time) ([]GenericEvent, error) {
	switch pc.Provider {
	case "webcal":
		c := NewWebcalClient(pc.URL)
		return c.ListEvents(ctx, start, end)
	case "outlook":
		tok, err := tokenFromJSON(pc.CredentialsJSON)
		if err != nil {
			return nil, err
		}
		oc := NewOutlookClient(oauth2.StaticTokenSource(tok))
		return oc.ListEvents(ctx, start, end)
	default: // google
		tok, err := tokenFromJSON(pc.CredentialsJSON)
		if err != nil {
			return nil, err
		}
		client, err := NewClient(ctx, oauth2.StaticTokenSource(tok))
		if err != nil {
			return nil, err
		}
		items, err := client.ListEvents(ctx, client.CalendarID, start, end)
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
}

func tokenFromJSON(raw string) (*oauth2.Token, error) {
	var tok oauth2.Token
	if err := json.Unmarshal([]byte(raw), &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}
