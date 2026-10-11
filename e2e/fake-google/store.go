package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	googlecalendar "google.golang.org/api/calendar/v3"
)

// Credential is one bearer the stand-in accepts. Label is what the request
// log records, never the bearer itself, and Calendar is the calendar id that
// "primary" means for a request carrying it (the owner's address, as on the
// real service).
type Credential struct {
	Token    string `json:"token"`
	Label    string `json:"label"`
	Calendar string `json:"calendar"`
}

// LogEntry is one request that reached a product route (/calendar/v3/...).
// It is what AC-22 and AC-25 read: which operation, on which calendar, for
// which window or identifiers, and under which seeded credential.
type LogEntry struct {
	Seq        int64     `json:"seq"`
	At         time.Time `json:"at"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Operation  string    `json:"operation"`
	CalendarID string    `json:"calendar_id,omitempty"`
	EventID    string    `json:"event_id,omitempty"`
	TimeMin    string    `json:"time_min,omitempty"`
	TimeMax    string    `json:"time_max,omitempty"`
	FreeBusy   []string  `json:"freebusy_ids,omitempty"`
	// Credential is the label of the presented bearer, "" when none was
	// presented and "rejected" when the bearer is not in the accepted set.
	Credential string `json:"credential"`
	Status     int    `json:"status"`
}

// Redirect arms a single 302 for the next product-route request that
// presents the credential with the given label. It is scoped to a label so a
// scenario running in parallel with others can never consume or be consumed
// by someone else's arming.
type Redirect struct {
	Label    string `json:"label"`
	Location string `json:"location"`
}

// calendarMeta is what calendarList returns for a calendar.
type calendarMeta struct {
	Summary string
}

// Store is the stand-in's whole state, behind one mutex.
type Store struct {
	mu        sync.Mutex
	nextID    int64
	nextSeq   int64
	tokens    map[string]Credential
	calendars map[string][]*googlecalendar.Event
	meta      map[string]calendarMeta
	log       []LogEntry
	redirects map[string]string // label -> location
}

// NewStore returns an empty store: no accepted credentials, no calendars.
func NewStore() *Store {
	s := &Store{}
	s.Reset()
	return s
}

// Reset forgets everything, including the accepted credentials and the log.
func (s *Store) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens = map[string]Credential{}
	s.calendars = map[string][]*googlecalendar.Event{}
	s.meta = map[string]calendarMeta{}
	s.log = nil
	s.redirects = map[string]string{}
}

// SetTokens replaces the accepted credential set.
func (s *Store) SetTokens(creds []Credential) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens = make(map[string]Credential, len(creds))
	for _, c := range creds {
		s.tokens[c.Token] = c
	}
}

// Lookup returns the credential for a bearer.
func (s *Store) Lookup(token string) (Credential, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.tokens[token]
	return c, ok
}

// ArmRedirect arms one redirect for a label.
func (s *Store) ArmRedirect(r Redirect) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.redirects[r.Label] = r.Location
}

// TakeRedirect returns, and disarms, the redirect armed for a label.
func (s *Store) TakeRedirect(label string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	loc, ok := s.redirects[label]
	if ok {
		delete(s.redirects, label)
	}
	return loc, ok
}

// Record appends a log entry and assigns its sequence number.
func (s *Store) Record(e LogEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextSeq++
	e.Seq = s.nextSeq
	s.log = append(s.log, e)
}

// Requests returns every entry with Seq > since.
func (s *Store) Requests(since int64) []LogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]LogEntry, 0, len(s.log))
	for _, e := range s.log {
		if e.Seq > since {
			out = append(out, e)
		}
	}
	return out
}

// LastSeq is the sequence number of the newest entry, 0 when empty.
func (s *Store) LastSeq() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nextSeq
}

// zeroTime is an open bound for List.
var zeroTime time.Time

func calKey(id string) string { return strings.ToLower(strings.TrimSpace(id)) }

// SetCalendarMeta registers a calendar for calendarList.
func (s *Store) SetCalendarMeta(id, summary string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.meta[calKey(id)] = calendarMeta{Summary: summary}
}

// ClearCalendar removes every event of one calendar, and its metadata.
func (s *Store) ClearCalendar(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.calendars, calKey(id))
	delete(s.meta, calKey(id))
}

func (s *Store) newEventID() string {
	s.nextID++
	// Google event ids are base32hex (a-v, 0-9); "fg" + digits stays inside it.
	return fmt.Sprintf("fg%010d", s.nextID)
}

// Insert stores a copy of ev on a calendar and returns it with the fields the
// real service fills in. A caller-supplied id is kept, as Google does.
func (s *Store) Insert(calendarID string, ev *googlecalendar.Event) *googlecalendar.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *ev
	if cp.Id == "" {
		cp.Id = s.newEventID()
	}
	if cp.Status == "" {
		cp.Status = "confirmed"
	}
	cp.Kind = "calendar#event"
	now := time.Now().UTC().Format(time.RFC3339)
	if cp.Created == "" {
		cp.Created = now
	}
	cp.Updated = now
	if cp.Organizer == nil {
		cp.Organizer = &googlecalendar.EventOrganizer{Email: calendarID}
	}
	key := calKey(calendarID)
	s.calendars[key] = append(s.calendars[key], &cp)
	out := cp
	return &out
}

// Get returns a copy of one event.
func (s *Store) Get(calendarID, eventID string) (*googlecalendar.Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.calendars[calKey(calendarID)] {
		if e.Id == eventID {
			cp := *e
			return &cp, true
		}
	}
	return nil, false
}

// Update replaces one event (PUT semantics) and returns the stored copy.
func (s *Store) Update(calendarID, eventID string, ev *googlecalendar.Event) (*googlecalendar.Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.calendars[calKey(calendarID)]
	for i, e := range list {
		if e.Id == eventID {
			cp := *ev
			cp.Id = eventID
			cp.Kind = "calendar#event"
			cp.Created = e.Created
			cp.Updated = time.Now().UTC().Format(time.RFC3339)
			if cp.Status == "" {
				cp.Status = e.Status
			}
			if cp.Organizer == nil {
				cp.Organizer = e.Organizer
			}
			list[i] = &cp
			out := cp
			return &out, true
		}
	}
	return nil, false
}

// Delete removes one event.
func (s *Store) Delete(calendarID, eventID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := calKey(calendarID)
	list := s.calendars[key]
	for i, e := range list {
		if e.Id == eventID {
			s.calendars[key] = append(list[:i:i], list[i+1:]...)
			return true
		}
	}
	return false
}

// List returns the events of a calendar that overlap [timeMin, timeMax),
// sorted by start. A zero bound is open. This is the real service's rule:
// timeMin filters on the event's end, timeMax on its start, so an event that
// straddles either boundary is included.
func (s *Store) List(calendarID string, timeMin, timeMax time.Time) []*googlecalendar.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*googlecalendar.Event
	for _, e := range s.calendars[calKey(calendarID)] {
		if e.Status == "cancelled" {
			continue
		}
		start, end, ok := eventBounds(e)
		if !ok {
			continue
		}
		if !timeMin.IsZero() && !end.After(timeMin) {
			continue
		}
		if !timeMax.IsZero() && !start.Before(timeMax) {
			continue
		}
		cp := *e
		out = append(out, &cp)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, _, _ := eventBounds(out[i])
		b, _, _ := eventBounds(out[j])
		return a.Before(b)
	})
	return out
}

// Busy projects a calendar onto free/busy: the opaque, non-cancelled events
// overlapping the window, clipped to it, as the real service reports them.
// known is false when the stand-in holds no such calendar.
func (s *Store) Busy(calendarID string, timeMin, timeMax time.Time) (busy []*googlecalendar.TimePeriod, known bool) {
	s.mu.Lock()
	_, hasEvents := s.calendars[calKey(calendarID)]
	_, hasMeta := s.meta[calKey(calendarID)]
	s.mu.Unlock()
	if !hasEvents && !hasMeta {
		return nil, false
	}
	busy = []*googlecalendar.TimePeriod{}
	for _, e := range s.List(calendarID, timeMin, timeMax) {
		if e.Transparency == "transparent" {
			continue
		}
		start, end, _ := eventBounds(e)
		if start.Before(timeMin) {
			start = timeMin
		}
		if end.After(timeMax) {
			end = timeMax
		}
		busy = append(busy, &googlecalendar.TimePeriod{
			Start: start.UTC().Format(time.RFC3339),
			End:   end.UTC().Format(time.RFC3339),
		})
	}
	return busy, true
}

// CalendarIDs returns every calendar id the stand-in knows, sorted.
func (s *Store) CalendarIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	for id := range s.calendars {
		seen[id] = true
	}
	for id := range s.meta {
		seen[id] = true
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Summary returns the registered summary of a calendar, or its id.
func (s *Store) Summary(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.meta[calKey(id)]; ok && m.Summary != "" {
		return m.Summary
	}
	return id
}

// eventBounds parses an event's start and end. An all-day event's date is
// read as midnight UTC.
func eventBounds(e *googlecalendar.Event) (start, end time.Time, ok bool) {
	if e.Start == nil || e.End == nil {
		return time.Time{}, time.Time{}, false
	}
	start, ok1 := parseEventTime(e.Start)
	end, ok2 := parseEventTime(e.End)
	return start, end, ok1 && ok2
}

func parseEventTime(t *googlecalendar.EventDateTime) (time.Time, bool) {
	if t.DateTime != "" {
		v, err := time.Parse(time.RFC3339, t.DateTime)
		return v, err == nil
	}
	if t.Date != "" {
		v, err := time.Parse("2006-01-02", t.Date)
		return v, err == nil
	}
	return time.Time{}, false
}
