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

// NewClient is the single entry point every calendar call site uses, in every
// deployment. It builds the client on the shared construction path (see
// transport.go): the caller's token source attaches the bearer itself, over
// the configured base transport, redirects are refused, and requests go to the
// configured address. option.WithHTTPClient makes the library use this client
// as it is, so neither of its own credential stacks, its redirect-following
// nor its proxy handling is involved.
func NewClient(ctx context.Context, tokenSource oauth2.TokenSource) (*CalendarClient, error) {
	s := current()
	svc, err := googlecalendar.NewService(ctx,
		option.WithHTTPClient(newHTTPClient(s, tokenSource)),
		option.WithEndpoint(s.endpoint),
	)
	if err != nil {
		return nil, err
	}
	return &CalendarClient{service: svc, CalendarID: "primary"}, nil
}

func (c *CalendarClient) CurrentCalendarID() string { return c.CalendarID }
