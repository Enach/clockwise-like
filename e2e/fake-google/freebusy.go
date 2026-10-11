package main

import (
	"encoding/json"
	"net/http"
	"time"

	googlecalendar "google.golang.org/api/calendar/v3"
)

// freeBusy serves POST /freeBusy. Each requested identifier is answered from
// the stand-in's calendar of that id. An identifier the stand-in holds no
// calendar for is answered the way the real service answers one it cannot
// see: an entry with a notFound error and no busy windows.
func (s *Server) freeBusy(w http.ResponseWriter, r *http.Request, c *call) {
	var req googlecalendar.FreeBusyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeGoogleError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "parseError", "Parse Error")
		return
	}
	for _, it := range req.Items {
		if it != nil {
			c.entry.FreeBusy = append(c.entry.FreeBusy, it.Id)
		}
	}
	c.entry.TimeMin = req.TimeMin
	c.entry.TimeMax = req.TimeMax

	tMin, err1 := time.Parse(time.RFC3339, req.TimeMin)
	tMax, err2 := time.Parse(time.RFC3339, req.TimeMax)
	if err1 != nil || err2 != nil || !tMin.Before(tMax) {
		writeGoogleError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "timeRangeEmpty", "The specified time range is empty.")
		return
	}

	resp := &googlecalendar.FreeBusyResponse{
		Kind:      "calendar#freeBusy",
		TimeMin:   req.TimeMin,
		TimeMax:   req.TimeMax,
		Calendars: map[string]googlecalendar.FreeBusyCalendar{},
	}
	for _, id := range c.entry.FreeBusy {
		busy, known := s.store.Busy(c.resolve(id), tMin, tMax)
		if !known {
			resp.Calendars[id] = googlecalendar.FreeBusyCalendar{
				Busy:   []*googlecalendar.TimePeriod{},
				Errors: []*googlecalendar.Error{{Domain: "global", Reason: "notFound"}},
			}
			continue
		}
		resp.Calendars[id] = googlecalendar.FreeBusyCalendar{Busy: busy}
	}
	writeJSON(w, http.StatusOK, resp)
}
