package calendar

import (
	"context"

	"golang.org/x/oauth2"
	googlecalendar "google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

type CalendarClient struct {
	service    *googlecalendar.Service
	CalendarID string
}

func NewClient(ctx context.Context, tokenSource oauth2.TokenSource) (*CalendarClient, error) {
	svc, err := googlecalendar.NewService(ctx, option.WithTokenSource(tokenSource))
	if err != nil {
		return nil, err
	}
	return &CalendarClient{service: svc, CalendarID: "primary"}, nil
}

func (c *CalendarClient) CurrentCalendarID() string { return c.CalendarID }
