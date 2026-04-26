package calendar

import (
	"context"
	"sort"
	"strings"
	"time"
)

// Room represents a calendar resource (conference room).
type Room struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// AttendeeSuggestion is a de-duplicated attendee email+name harvested from recent events.
type AttendeeSuggestion struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

// ListRooms returns Google Workspace room calendars visible to the authenticated user
// whose summary or ID contains the optional filter query (case-insensitive).
func (c *CalendarClient) ListRooms(ctx context.Context, query string) ([]Room, error) {
	list, err := c.service.CalendarList.List().Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	var rooms []Room
	for _, item := range list.Items {
		if !strings.Contains(item.Id, "resource.calendar.google.com") {
			continue
		}
		if query != "" &&
			!strings.Contains(strings.ToLower(item.Summary), query) &&
			!strings.Contains(strings.ToLower(item.Id), query) {
			continue
		}
		rooms = append(rooms, Room{ID: item.Id, Name: item.Summary})
	}
	return rooms, nil
}

// SuggestAttendees returns up to 20 unique attendees seen in events over the past lookback window
// whose email or display name contains the optional filter query (case-insensitive).
func (c *CalendarClient) SuggestAttendees(ctx context.Context, query string, lookback time.Duration) ([]AttendeeSuggestion, error) {
	since := time.Now().Add(-lookback)
	events, err := c.ListEvents(ctx, c.CalendarID, since, time.Now())
	if err != nil {
		return nil, err
	}

	seen := map[string]AttendeeSuggestion{}
	for _, e := range events {
		for _, a := range e.Attendees {
			email := strings.ToLower(a.Email)
			if email == "" {
				continue
			}
			if query != "" &&
				!strings.Contains(email, query) &&
				!strings.Contains(strings.ToLower(a.DisplayName), query) {
				continue
			}
			if _, ok := seen[email]; !ok {
				seen[email] = AttendeeSuggestion{Email: a.Email, Name: a.DisplayName}
			}
		}
	}

	result := make([]AttendeeSuggestion, 0, len(seen))
	for _, s := range seen {
		result = append(result, s)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Email < result[j].Email })
	if len(result) > 20 {
		result = result[:20]
	}
	return result, nil
}
