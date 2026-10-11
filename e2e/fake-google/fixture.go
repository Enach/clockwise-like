package main

import (
	"encoding/json"
	"net/http"
	"strconv"

	googlecalendar "google.golang.org/api/calendar/v3"
)

// The control plane. The real API has none of this; only the tests call it,
// through the 127.0.0.1-published port (e2e/fake.ts).
//
// Isolation rule: the suite runs its specs in parallel against one stand-in,
// so nothing here is "reset per scenario". A scenario owns its calendars (by
// id) and its credential (by label), clears what it owns, and reads the log
// filtered by its own label. POST /__fixture/reset wipes everything and is for
// global setup only.
func (s *Server) registerFixture() {
	s.mux.HandleFunc("GET /__fixture/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	s.mux.HandleFunc("POST /__fixture/reset", func(w http.ResponseWriter, _ *http.Request) {
		s.store.Reset()
		w.WriteHeader(http.StatusNoContent)
	})

	// PUT replaces the accepted credential set.
	s.mux.HandleFunc("PUT /__fixture/tokens", func(w http.ResponseWriter, r *http.Request) {
		var creds []Credential
		if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
			http.Error(w, "body must be a JSON array of {token,label,calendar}", http.StatusBadRequest)
			return
		}
		for _, c := range creds {
			if c.Token == "" || c.Label == "" || c.Calendar == "" {
				http.Error(w, "every credential needs token, label and calendar", http.StatusBadRequest)
				return
			}
		}
		s.store.SetTokens(creds)
		w.WriteHeader(http.StatusNoContent)
	})

	// PUT registers a calendar's summary (rooms, for calendarList).
	s.mux.HandleFunc("PUT /__fixture/calendars/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Summary string `json:"summary"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "body must be {summary}", http.StatusBadRequest)
			return
		}
		s.store.SetCalendarMeta(r.PathValue("id"), body.Summary)
		w.WriteHeader(http.StatusNoContent)
	})

	s.mux.HandleFunc("DELETE /__fixture/calendars/{id}", func(w http.ResponseWriter, r *http.Request) {
		s.store.ClearCalendar(r.PathValue("id"))
		w.WriteHeader(http.StatusNoContent)
	})

	// POST seeds events (a JSON array of calendar#event) and answers with what
	// was stored, ids included.
	s.mux.HandleFunc("POST /__fixture/calendars/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		var evs []*googlecalendar.Event
		if err := json.NewDecoder(r.Body).Decode(&evs); err != nil {
			http.Error(w, "body must be a JSON array of events", http.StatusBadRequest)
			return
		}
		out := make([]*googlecalendar.Event, 0, len(evs))
		for _, ev := range evs {
			if ev == nil || ev.Start == nil || ev.End == nil {
				http.Error(w, "every event needs start and end", http.StatusBadRequest)
				return
			}
			out = append(out, s.store.Insert(r.PathValue("id"), ev))
		}
		writeJSON(w, http.StatusOK, out)
	})

	// GET answers every event the calendar holds, unfiltered. This is how a
	// test checks what the backend wrote (AC-18) or deleted (AC-19).
	s.mux.HandleFunc("GET /__fixture/calendars/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		evs := s.store.List(r.PathValue("id"), zeroTime, zeroTime)
		if evs == nil {
			evs = []*googlecalendar.Event{}
		}
		writeJSON(w, http.StatusOK, evs)
	})

	// GET answers the request log after ?since=<seq>, and the newest seq, so a
	// test can take a mark before it acts and read only what came after.
	s.mux.HandleFunc("GET /__fixture/requests", func(w http.ResponseWriter, r *http.Request) {
		var since int64
		if v := r.URL.Query().Get("since"); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				http.Error(w, "since must be an integer", http.StatusBadRequest)
				return
			}
			since = n
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"last_seq": s.store.LastSeq(),
			"requests": s.store.Requests(since),
		})
	})

	// POST arms a 302 for the next `count` (default 1) product requests
	// carrying the label; count 0 disarms.
	s.mux.HandleFunc("POST /__fixture/redirect-next", func(w http.ResponseWriter, r *http.Request) {
		var body Redirect
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Label == "" ||
			(body.Location == "" && (body.Count == nil || *body.Count != 0)) {
			http.Error(w, "body must be {label, location, count?}", http.StatusBadRequest)
			return
		}
		s.store.ArmRedirect(body)
		w.WriteHeader(http.StatusNoContent)
	})
}
