package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/engine"
)

// PAC-88 bench: the PAC-72 bench (real handlers, real engine, test database,
// fixed clock) with a calendar stand-in that pages its events.list answers
// (nextPageToken, as Google does), counts list requests per credential, and
// can fail a given page of a credential.

type pac88Calendar struct {
	mu       sync.Mutex
	events   map[string][]pac72Event
	pageSize int
	lists    map[string]int // events.list requests, every page counted
	reads    map[string]int // first-page requests: one per read
	failPage map[string]int // 1-based page that answers 503
	delay    time.Duration
}

func newPAC88Calendar(t *testing.T) *pac88Calendar {
	t.Helper()
	c := &pac88Calendar{events: map[string][]pac72Event{}, pageSize: 250, lists: map[string]int{}, reads: map[string]int{}, failPage: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/events") {
			var ev map[string]any
			_ = json.NewDecoder(r.Body).Decode(&ev)
			ev["id"], ev["kind"] = "pac88-created", "calendar#event"
			_ = json.NewEncoder(w).Encode(ev)
			return
		}
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/events") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		q := r.URL.Query()
		page := 1
		if tok := q.Get("pageToken"); tok != "" {
			page, _ = strconv.Atoi(strings.TrimPrefix(tok, "p"))
		}
		c.mu.Lock()
		c.lists[bearer]++
		if page == 1 {
			c.reads[bearer]++
		}
		evs := append([]pac72Event(nil), c.events[bearer]...)
		size, fail, delay := c.pageSize, c.failPage[bearer], c.delay
		c.mu.Unlock()
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		if fail == page {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":503,"message":"backend error"}}`))
			return
		}
		minT, _ := time.Parse(time.RFC3339, q.Get("timeMin"))
		maxT, _ := time.Parse(time.RFC3339, q.Get("timeMax"))
		sort.Slice(evs, func(i, j int) bool { return evs[i].start.Before(evs[j].start) })
		var match []pac72Event
		for _, ev := range evs {
			if ev.start.Before(maxT) && ev.end.After(minT) {
				match = append(match, ev)
			}
		}
		from := (page - 1) * size
		to := from + size
		if to > len(match) {
			to = len(match)
		}
		items := []any{}
		for i := from; i < to; i++ {
			ev := match[i]
			item := map[string]any{"id": fmt.Sprintf("ev-%d", i), "status": "confirmed", "summary": "Busy",
				"start": map[string]string{"dateTime": ev.start.Format(time.RFC3339)},
				"end":   map[string]string{"dateTime": ev.end.Format(time.RFC3339)}}
			if ev.transparent {
				item["transparency"] = "transparent"
			}
			items = append(items, item)
		}
		resp := map[string]any{"kind": "calendar#events", "items": items}
		if to < len(match) {
			resp["nextPageToken"] = fmt.Sprintf("p%d", page+1)
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	calendar.Configure(config.CalendarEndpoint{BaseURL: srv.URL + config.CalendarAPIRoot, Guard: true})
	t.Cleanup(func() { calendar.Configure(config.ProductionCalendarEndpoint()) })
	return c
}

func (c *pac88Calendar) addEvent(bearer string, start, end time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events[bearer] = append(c.events[bearer], pac72Event{start: start, end: end})
}

func (c *pac88Calendar) counts() (reads, lists map[string]int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	reads, lists = map[string]int{}, map[string]int{}
	for k, v := range c.reads {
		reads[k] = v
	}
	for k, v := range c.lists {
		lists[k] = v
	}
	return reads, lists
}

func (c *pac88Calendar) resetCounts() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads, c.lists = map[string]int{}, map[string]int{}
}

type pac88Bench struct {
	*pac72Bench
	cal88 *pac88Calendar
}

// newPAC88Bench is the PAC-72 bench whose calendar stand-in is replaced by
// the paging one (configured last, so it is the one the engine reaches).
func newPAC88Bench(t *testing.T, now time.Time) *pac88Bench {
	t.Helper()
	b := newPAC72Bench(t, now)
	return &pac88Bench{pac72Bench: b, cal88: newPAC88Calendar(t)}
}

// pac88Answer is a decoded 200 listing (every PAC-88 key present) or the
// status and body of another answer.
type pac88Answer struct {
	code    int
	body    string
	header  http.Header
	slots   []engine.AvailableSlot
	dates   []string
	zone    string
	display string
	from    string
	to      string
}

// get requests /api/book/{slug}/slots with the given query.
func (b *pac72Bench) get(slug string, q url.Values) pac88Answer {
	b.t.Helper()
	target := "/api/book/" + slug + "/slots"
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	rec := httptest.NewRecorder()
	b.r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	out := pac88Answer{code: rec.Code, body: strings.TrimSpace(rec.Body.String()), header: rec.Header()}
	if rec.Code != http.StatusOK {
		return out
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		b.t.Fatalf("decode: %v (%s)", err, out.body)
	}
	for _, k := range []string{"slots", "available_dates", "time_zone", "display_time_zone", "range_from", "range_to"} {
		if v, ok := raw[k]; !ok || string(v) == "null" {
			b.t.Fatalf("answer lacks %s or has null: %s", k, out.body)
		}
	}
	var got struct {
		Slots   []engine.AvailableSlot `json:"slots"`
		Dates   []string               `json:"available_dates"`
		Zone    string                 `json:"time_zone"`
		Display string                 `json:"display_time_zone"`
		From    string                 `json:"range_from"`
		To      string                 `json:"range_to"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		b.t.Fatalf("decode: %v (%s)", err, out.body)
	}
	out.slots, out.dates, out.zone, out.display, out.from, out.to = got.Slots, got.Dates, got.Zone, got.Display, got.From, got.To
	return out
}

func pac88Q(kv ...string) url.Values {
	q := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		q.Set(kv[i], kv[i+1])
	}
	return q
}

// overview is the bounded overview from..to (the dates as given).
func (b *pac72Bench) overview(slug string, duration int, from, to time.Time, zone string) pac88Answer {
	b.t.Helper()
	q := pac88Q("duration", strconv.Itoa(duration), "from", from.Format(time.DateOnly), "to", to.Format(time.DateOnly))
	if zone != "" {
		q.Set("display_time_zone", zone)
	}
	return b.get(slug, q)
}

// horizonDates reads n days from first as ceil(n/42) overviews (the
// contract's replacement for the whole-horizon read) and returns the union
// of their dates, failing on any answer other than 200.
func (b *pac72Bench) horizonDates(slug string, duration int, first time.Time, n int) []string {
	b.t.Helper()
	var all []string
	for off := 0; off < n; off += maxOverviewDays {
		last := off + maxOverviewDays - 1
		if last > n-1 {
			last = n - 1
		}
		a := b.overview(slug, duration, first.AddDate(0, 0, off), first.AddDate(0, 0, last), "")
		if a.code != http.StatusOK {
			b.t.Fatalf("overview %d..%d = %d %s", off, last, a.code, a.body)
		}
		all = append(all, a.dates...)
	}
	return all
}

// startsIn is each slot start as "2006-01-02 15:04 -07:00" in zone.
func startsIn(slots []engine.AvailableSlot, zone *time.Location) []string {
	out := make([]string, 0, len(slots))
	for _, s := range slots {
		out = append(out, s.Start.In(zone).Format("2006-01-02 15:04 -07:00"))
	}
	return out
}

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return loc
}
