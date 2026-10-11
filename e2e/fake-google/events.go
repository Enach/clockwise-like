package main

import (
	"encoding/json"
	"net/http"
	"time"

	googlecalendar "google.golang.org/api/calendar/v3"
)

// listEvents serves GET /calendars/{id}/events. It honours timeMin/timeMax
// (backend/calendar/events.go always sends both) and ignores every other
// parameter (singleEvents, orderBy, alt, prettyPrint): the stand-in stores
// single instances already, and always answers in start order.
func (s *Server) listEvents(w http.ResponseWriter, r *http.Request, c *call) {
	var tMin, tMax time.Time
	if v := r.URL.Query().Get("timeMin"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeGoogleError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "badRequest", "Bad Request: timeMin")
			return
		}
		tMin = t
	}
	if v := r.URL.Query().Get("timeMax"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeGoogleError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "badRequest", "Bad Request: timeMax")
			return
		}
		tMax = t
	}
	items := s.store.List(c.calendarID, tMin, tMax)
	if items == nil {
		items = []*googlecalendar.Event{}
	}
	writeJSON(w, http.StatusOK, &googlecalendar.Events{
		Kind:     "calendar#events",
		Summary:  c.calendarID,
		TimeZone: "UTC",
		Items:    items,
	})
}

// insertEvent serves POST /calendars/{id}/events.
func (s *Server) insertEvent(w http.ResponseWriter, r *http.Request, c *call) {
	var ev googlecalendar.Event
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		writeGoogleError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "parseError", "Parse Error")
		return
	}
	if ev.Start == nil || ev.End == nil {
		writeGoogleError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "required", "Missing time.")
		return
	}
	c.entry.Summary = ev.Summary
	writeJSON(w, http.StatusOK, s.store.Insert(c.calendarID, &ev))
}

// getEvent serves GET /calendars/{id}/events/{eventId}.
func (s *Server) getEvent(w http.ResponseWriter, _ *http.Request, c *call) {
	ev, ok := s.store.Get(c.calendarID, c.eventID)
	if !ok {
		writeGoogleError(w, http.StatusNotFound, "NOT_FOUND", "notFound", "Not Found")
		return
	}
	writeJSON(w, http.StatusOK, ev)
}

// updateEvent serves PUT /calendars/{id}/events/{eventId}: UpdateEvent,
// DeclineEvent, AddGoogleMeet and ClearGoogleMeet all arrive here
// (backend/calendar/events.go uses Events.Update for every write, never
// Patch).
func (s *Server) updateEvent(w http.ResponseWriter, r *http.Request, c *call) {
	var ev googlecalendar.Event
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		writeGoogleError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "parseError", "Parse Error")
		return
	}
	if ev.ConferenceData != nil && ev.ConferenceData.CreateRequest != nil {
		// What the real service does with conferenceDataVersion=1: it answers
		// the create request with a Meet link.
		link := "https://meet.google.com/fake-" + c.eventID
		ev.HangoutLink = link
		ev.ConferenceData.EntryPoints = []*googlecalendar.EntryPoint{{EntryPointType: "video", Uri: link}}
	}
	c.entry.Summary = ev.Summary
	updated, ok := s.store.Update(c.calendarID, c.eventID, &ev)
	if !ok {
		writeGoogleError(w, http.StatusNotFound, "NOT_FOUND", "notFound", "Not Found")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// deleteEvent serves DELETE /calendars/{id}/events/{eventId}: 204 on
// success, 410 for an id the stand-in does not hold (the real service answers
// 410 "Resource has been deleted" for an event it already deleted).
func (s *Server) deleteEvent(w http.ResponseWriter, _ *http.Request, c *call) {
	if !s.store.Delete(c.calendarID, c.eventID) {
		writeGoogleError(w, http.StatusGone, "GONE", "deleted", "Resource has been deleted")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
