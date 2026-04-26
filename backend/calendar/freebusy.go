package calendar

import (
	"context"
	"time"

	googlecalendar "google.golang.org/api/calendar/v3"
)

type TimeSlot struct {
	Start time.Time
	End   time.Time
}

func (c *CalendarClient) GetFreeBusy(ctx context.Context, emails []string, timeMin, timeMax time.Time) (map[string][]TimeSlot, error) {
	items := make([]*googlecalendar.FreeBusyRequestItem, len(emails))
	for i, email := range emails {
		items[i] = &googlecalendar.FreeBusyRequestItem{Id: email}
	}

	resp, err := c.service.Freebusy.Query(&googlecalendar.FreeBusyRequest{
		TimeMin: timeMin.Format(time.RFC3339),
		TimeMax: timeMax.Format(time.RFC3339),
		Items:   items,
	}).Context(ctx).Do()
	if err != nil {
		return nil, err
	}

	result := make(map[string][]TimeSlot, len(emails))
	for email, cal := range resp.Calendars {
		slots := make([]TimeSlot, 0, len(cal.Busy))
		for _, b := range cal.Busy {
			start, _ := time.Parse(time.RFC3339, b.Start)
			end, _ := time.Parse(time.RFC3339, b.End)
			slots = append(slots, TimeSlot{Start: start, End: end})
		}
		result[email] = slots
	}
	return result, nil
}
