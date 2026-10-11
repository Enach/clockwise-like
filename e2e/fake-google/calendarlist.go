package main

import (
	"net/http"

	googlecalendar "google.golang.org/api/calendar/v3"
)

// calendarList serves GET /users/me/calendarList: the requesting credential's
// own calendar first, then every other calendar the stand-in knows, which is
// where the room calendars (ids under resource.calendar.google.com) that
// backend/calendar/rooms_attendees.go ListRooms filters for come from.
func (s *Server) calendarList(w http.ResponseWriter, _ *http.Request, c *call) {
	own := c.credential.Calendar
	items := []*googlecalendar.CalendarListEntry{{
		Kind:       "calendar#calendarListEntry",
		Id:         own,
		Summary:    s.store.Summary(own),
		Primary:    true,
		AccessRole: "owner",
	}}
	for _, id := range s.store.CalendarIDs() {
		if id == calKey(own) {
			continue
		}
		items = append(items, &googlecalendar.CalendarListEntry{
			Kind:       "calendar#calendarListEntry",
			Id:         id,
			Summary:    s.store.Summary(id),
			AccessRole: "reader",
		})
	}
	writeJSON(w, http.StatusOK, &googlecalendar.CalendarList{
		Kind:  "calendar#calendarList",
		Items: items,
	})
}
