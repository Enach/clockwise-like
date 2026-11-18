package calendar

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Enach/paceday/backend/internal/outbound"
	"github.com/emersion/go-ical"
)

// WebcalClient fetches and parses read-only iCal (.ics) feeds.
// Responses are cached for 15 minutes.
type WebcalClient struct {
	URL string

	mu       sync.Mutex
	cached   []icsEvent
	cachedAt time.Time
	cacheTTL time.Duration
}

func NewWebcalClient(url string) *WebcalClient {
	return &WebcalClient{URL: url, cacheTTL: 15 * time.Minute}
}

// ListEvents returns the feed's events overlapping [start, end). A
// recurring event is expanded into its instances in the window, each with
// the ID "<UID>/<instance start, UTC, 20060102T150405Z>"; a non-recurring
// event keeps its UID as ID. An event whose time or recurrence cannot be
// read is returned once as Unreadable, whatever the window (PAC-122 SF-044).
func (c *WebcalClient) ListEvents(ctx context.Context, start, end time.Time) ([]GenericEvent, error) {
	all, err := c.fetch(ctx)
	if err != nil {
		return nil, err
	}
	return expandICS(all, start, end), nil
}

// GetFreeBusy reads nothing (a feed has no free/busy for others), so every
// address is unknown with no windows, never free (PAC-133 §3.3, D-11).
func (c *WebcalClient) GetFreeBusy(_ context.Context, emails []string, _, _ time.Time) (map[string]FreeBusyEntry, error) {
	return unknownForAll(emails), nil
}

func (c *WebcalClient) CreateEvent(_ context.Context, _ GenericEvent) (string, error) {
	return "", ErrReadOnly
}

func (c *WebcalClient) fetch(ctx context.Context) ([]icsEvent, error) {
	c.mu.Lock()
	if time.Since(c.cachedAt) < c.cacheTTL && c.cached != nil {
		events := c.cached
		c.mu.Unlock()
		return events, nil
	}
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("webcal: request: %w", withoutURL(err))
	}
	// PAC-81 AC-A17: the fetch is bounded in time and size. Over either
	// limit the feed is not parsed at all (no blocker from a partial feed),
	// and the error names the limit that was hit.
	limits := outbound.Current()
	resp, err := outbound.ICSClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("webcal: fetch: %w", nameTimeLimit(err, limits.ICS))
	}
	defer resp.Body.Close()
	body, err := outbound.ReadLimited(resp.Body, limits.ICSMaxBytes)
	if errors.Is(err, outbound.ErrTooLarge) {
		return nil, fmt.Errorf("webcal: feed exceeds the %d-byte size limit: %w", limits.ICSMaxBytes, err)
	}
	if err != nil {
		return nil, fmt.Errorf("webcal: read: %w", nameTimeLimit(err, limits.ICS))
	}

	events, err := parseICS(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("webcal: parse: %w", err)
	}

	c.mu.Lock()
	c.cached = events
	c.cachedAt = time.Now()
	c.mu.Unlock()

	return events, nil
}

// recurrence is the part of rrule.Set that expansion needs.
type recurrence interface {
	Iterator() (next func() (time.Time, bool))
}

// Expansion bounds (PAC-122 SF-044, review minor 2). A series with more
// than maxInstancesPerSeries instances in the window lists the first ones
// and is also marked unreadable (its other blockers are kept, not deleted);
// one that needs more than maxRecurrenceSteps occurrences to reach the end
// of the window is unreadable. FREQ=SECONDLY and MINUTELY are refused
// outright.
const (
	maxInstancesPerSeries = 500
	maxRecurrenceSteps    = 100000
)

// icsEvent is one VEVENT as read from the feed, before expansion.
type icsEvent struct {
	uid   string
	title string
	start time.Time
	dur   time.Duration
	// rset is set for an event with an RRULE (and its RDATE/EXDATE).
	rset recurrence
	// recurrenceID is set for an override of one instance of a series.
	recurrenceID time.Time
	cancelled    bool
	unreadable   bool
}

// instanceID names one instance of a recurring event.
func instanceID(uid string, at time.Time) string {
	return uid + "/" + at.UTC().Format("20060102T150405Z")
}

// parseICS reads every VEVENT. Since PAC-122 (SF-044) an event with no
// DTEND is read through its DURATION (or as RFC 5545's default: one day
// for a date, an instant for a date-time) instead of being skipped, a
// recurring event keeps its RRULE for expansion, and an event whose time or
// recurrence cannot be read is kept as unreadable rather than dropped (a
// dropped event read as "gone", and its blocker was deleted).
func parseICS(r io.Reader) ([]icsEvent, error) {
	dec := ical.NewDecoder(r)
	cal, err := dec.Decode()
	if err != nil {
		return nil, err
	}
	var events []icsEvent
	for _, comp := range cal.Children {
		if comp.Name != ical.CompEvent {
			continue
		}
		ev := ical.Event{Component: comp}
		var out icsEvent
		if uid := comp.Props.Get(ical.PropUID); uid != nil {
			out.uid = uid.Value
		}
		if summary := comp.Props.Get(ical.PropSummary); summary != nil {
			out.title = summary.Value
		}
		if status, err := ev.Status(); err == nil && status == ical.EventCancelled {
			out.cancelled = true
		}
		if !readICSEvent(&ev, &out) {
			out = icsEvent{uid: out.uid, unreadable: true}
		}
		events = append(events, out)
	}
	return events, nil
}

// readICSEvent fills out's times and recurrence; false when any of them
// cannot be read.
func readICSEvent(ev *ical.Event, out *icsEvent) bool {
	if ev.Props.Get(ical.PropDateTimeStart) == nil {
		return false
	}
	start, err := ev.DateTimeStart(time.UTC)
	if err != nil {
		return false
	}
	end, err := ev.DateTimeEnd(time.UTC)
	if err != nil || end.Before(start) {
		return false
	}
	out.start, out.dur = start, end.Sub(start)
	if rid := ev.Props.Get(ical.PropRecurrenceID); rid != nil {
		at, err := rid.DateTime(time.UTC)
		if err != nil {
			return false
		}
		out.recurrenceID = at
		return true
	}
	if rule := ev.Props.Get(ical.PropRecurrenceRule); rule != nil {
		v := strings.ToUpper(rule.Value)
		if strings.Contains(v, "FREQ=SECONDLY") || strings.Contains(v, "FREQ=MINUTELY") {
			return false
		}
	}
	splitDateLists(ev.Props, ical.PropExceptionDates)
	splitDateLists(ev.Props, ical.PropRecurrenceDates)
	rset, err := ev.RecurrenceSet(time.UTC)
	if err != nil {
		return false
	}
	if rset != nil {
		out.rset = rset
	}
	return true
}

// splitDateLists turns each multi-valued EXDATE or RDATE property
// ("20250107T090000,20250108T090000", RFC 5545 §3.8.5.1/2) into one property
// per value with the same parameters (TZID, VALUE), which is the form
// go-ical parses (PAC-122 SF-044, review major 2).
func splitDateLists(props ical.Props, name string) {
	var out []ical.Prop
	for _, p := range props[name] {
		if !strings.Contains(p.Value, ",") {
			out = append(out, p)
			continue
		}
		for _, v := range strings.Split(p.Value, ",") {
			q := p
			q.Value = strings.TrimSpace(v)
			out = append(out, q)
		}
	}
	if len(out) > 0 {
		props[name] = out
	}
}

// seriesInstances returns the starts of the instances of rset whose
// [start, start+dur) overlaps [start, end), in order; ok is false when the
// series is over the expansion bounds (the instances returned are then the
// first maxInstancesPerSeries, or none when maxRecurrenceSteps was hit).
func seriesInstances(rset recurrence, dur time.Duration, start, end time.Time) ([]time.Time, bool) {
	next := rset.Iterator()
	var out []time.Time
	for steps := 0; ; steps++ {
		if steps >= maxRecurrenceSteps {
			return nil, false
		}
		at, more := next()
		if !more || !at.Before(end) {
			return out, true
		}
		if !at.Add(dur).After(start) {
			continue
		}
		if len(out) == maxInstancesPerSeries {
			return out, false
		}
		out = append(out, at)
	}
}

// expandICS returns the events of all that overlap [start, end), recurring
// ones expanded into instances (an instance replaced by an override is
// returned as the override, or not at all when the override is cancelled),
// plus every unreadable event.
func expandICS(all []icsEvent, start, end time.Time) []GenericEvent {
	overridden := map[string]bool{}
	for _, e := range all {
		if !e.unreadable && !e.recurrenceID.IsZero() {
			overridden[instanceID(e.uid, e.recurrenceID)] = true
		}
	}
	overlaps := func(s time.Time, d time.Duration) bool {
		return s.Add(d).After(start) && s.Before(end)
	}
	var events []GenericEvent
	for _, e := range all {
		switch {
		case e.unreadable:
			events = append(events, GenericEvent{ID: e.uid, unreadable: true})
		case !e.recurrenceID.IsZero():
			if !e.cancelled && overlaps(e.start, e.dur) {
				events = append(events, GenericEvent{ID: instanceID(e.uid, e.recurrenceID), Title: e.title, Start: e.start, End: e.start.Add(e.dur)})
			}
		case e.rset != nil:
			instances, ok := seriesInstances(e.rset, e.dur, start, end)
			for _, at := range instances {
				id := instanceID(e.uid, at)
				if overridden[id] {
					continue
				}
				events = append(events, GenericEvent{ID: id, Title: e.title, Start: at, End: at.Add(e.dur)})
			}
			if !ok {
				events = append(events, GenericEvent{ID: e.uid, unreadable: true})
			}
		default:
			if overlaps(e.start, e.dur) {
				events = append(events, GenericEvent{ID: e.uid, Title: e.title, Start: e.start, End: e.start.Add(e.dur)})
			}
		}
	}
	return events
}

// nameTimeLimit says so when err is the fetch's own time limit.
func nameTimeLimit(err error, limit time.Duration) error {
	var ne net.Error
	timeout := errors.As(err, &ne) && ne.Timeout()
	err = withoutURL(err)
	if timeout {
		return fmt.Errorf("feed exceeded the %s time limit: %w", limit, err)
	}
	return err
}

// withoutURL drops the request URL from a client error: a webcal URL is a
// secret (and may carry an address), and fetch errors are logged and
// reported (PAC-122). The cause is kept.
func withoutURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return ue.Err
	}
	return err
}
