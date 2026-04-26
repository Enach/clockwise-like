package calendar

import (
	"context"

	googlecalendar "google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
	"golang.org/x/oauth2"
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
