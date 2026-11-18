package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	googlecalendar "google.golang.org/api/calendar/v3"
)

// PAC-129 AC-14: the stand-in answers up to 50 calendars per request, as
// Google does, and refuses 51 with tooManyCalendarsRequested.
func TestPAC129_FreeBusyRefusesMoreThanFiftyCalendars(t *testing.T) {
	_, srv := newTestServer(t)
	svc := client(t, srv.URL, "tok-a")
	items := func(n int) []*googlecalendar.FreeBusyRequestItem {
		out := make([]*googlecalendar.FreeBusyRequestItem, n)
		for i := range out {
			out[i] = &googlecalendar.FreeBusyRequestItem{Id: fmt.Sprintf("p%02d@example.test", i)}
		}
		return out
	}
	q := func(n int) (*googlecalendar.FreeBusyResponse, error) {
		return svc.Freebusy.Query(&googlecalendar.FreeBusyRequest{
			TimeMin: "2030-01-07T00:00:00Z", TimeMax: "2030-01-08T00:00:00Z", Items: items(n),
		}).Context(context.Background()).Do()
	}
	if fb, err := q(50); err != nil || len(fb.Calendars) != 50 {
		t.Fatalf("50 calendars: %v (%d answered)", err, len(fb.Calendars))
	}
	_, err := q(51)
	if err == nil {
		t.Fatal("51 calendars were answered")
	}
	if want := "tooManyCalendarsRequested"; !strings.Contains(err.Error(), want) {
		t.Errorf("51 calendars: %v, want %s", err, want)
	}
}
