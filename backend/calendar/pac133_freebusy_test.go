package calendar

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// PAC-133 §3: every requested id is answered readable, unknown with the
// windows the provider reported, or unknown with none.

var pac133Day = time.Date(2030, 1, 7, 0, 0, 0, 0, time.UTC)

func at(h, m int) time.Time {
	return pac133Day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
}

func googleFreeBusy(t *testing.T, body string, ids ...string) map[string]FreeBusyEntry {
	t.Helper()
	recordingStandIn(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
	got, err := newTestClient(t).GetFreeBusy(context.Background(), ids, pac133Day, pac133Day.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("GetFreeBusy: %v", err)
	}
	if len(got) != len(distinctIDs(ids)) {
		t.Fatalf("answer has %d ids, want one per requested id (%v): %+v", len(got), ids, got)
	}
	return got
}

func wantEntry(t *testing.T, got map[string]FreeBusyEntry, id string, known bool, windows ...TimeSlot) {
	t.Helper()
	e, ok := got[id]
	if !ok {
		t.Fatalf("%s is not answered: %+v", id, got)
	}
	if e.Known != known {
		t.Errorf("%s known = %v, want %v", id, e.Known, known)
	}
	if e.Busy == nil {
		t.Errorf("%s busy is nil, want a list", id)
	}
	if len(e.Busy) != len(windows) {
		t.Fatalf("%s busy = %+v, want %+v", id, e.Busy, windows)
	}
	for i, w := range windows {
		if !e.Busy[i].Start.Equal(w.Start) || !e.Busy[i].End.Equal(w.End) {
			t.Errorf("%s busy[%d] = %+v, want %+v", id, i, e.Busy[i], w)
		}
	}
}

// AC-1: notFound with no windows is unknown; windows with no error are
// readable; neither is readable and free.
func TestPAC133_AC1_GoogleNotFoundIsUnknown(t *testing.T) {
	got := googleFreeBusy(t, `{"calendars":{
		"a@x.test":{"busy":[],"errors":[{"domain":"global","reason":"notFound"}]},
		"b@x.test":{"busy":[{"start":"2030-01-07T09:00:00Z","end":"2030-01-07T10:00:00Z"},{"start":"2030-01-07T11:00:00Z","end":"2030-01-07T12:00:00Z"}]},
		"c@x.test":{}
	}}`, "a@x.test", "b@x.test", "c@x.test")
	wantEntry(t, got, "a@x.test", false)
	wantEntry(t, got, "b@x.test", true, TimeSlot{at(9, 0), at(10, 0)}, TimeSlot{at(11, 0), at(12, 0)})
	wantEntry(t, got, "c@x.test", true)
}

// AC-2: an error with a window is unknown with the window kept; a requested
// id absent from the answer is unknown with none.
func TestPAC133_AC2_GoogleErrorKeepsWindowsAndAbsentIsUnknown(t *testing.T) {
	got := googleFreeBusy(t, `{"calendars":{
		"a@x.test":{"busy":[{"start":"2030-01-07T13:00:00Z","end":"2030-01-07T14:00:00Z"}],"errors":[{"domain":"global","reason":"internalError"}]}
	}}`, "a@x.test", "d@x.test")
	wantEntry(t, got, "a@x.test", false, TimeSlot{at(13, 0), at(14, 0)})
	wantEntry(t, got, "d@x.test", false)
}

// AC-3: groups are expanded; members' windows are the group's; an expansion
// error or one unknown member makes the group unknown, windows kept; members
// are part of the group, and unrequested extras are dropped.
func TestPAC133_AC3_GoogleGroups(t *testing.T) {
	got := googleFreeBusy(t, `{
		"groups":{
			"g@x.test":{"calendars":["m1@x.test","m2@x.test"]},
			"g2@x.test":{"errors":[{"domain":"global","reason":"groupTooBig"}]},
			"g3@x.test":{"calendars":["m3@x.test","m4@x.test"]}
		},
		"calendars":{
			"m1@x.test":{"busy":[{"start":"2030-01-07T10:00:00Z","end":"2030-01-07T11:00:00Z"}]},
			"m2@x.test":{"busy":[]},
			"m3@x.test":{"busy":[{"start":"2030-01-07T15:00:00Z","end":"2030-01-07T16:00:00Z"}]},
			"m4@x.test":{"busy":[],"errors":[{"domain":"global","reason":"notFound"}]},
			"stray@x.test":{"busy":[{"start":"2030-01-07T08:00:00Z","end":"2030-01-07T09:00:00Z"}]}
		}}`, "g@x.test", "g2@x.test", "g3@x.test")

	wantEntry(t, got, "g@x.test", true, TimeSlot{at(10, 0), at(11, 0)})
	g := got["g@x.test"]
	if !g.IsGroup() || len(g.Members) != 2 || g.Members[0].ID != "m1@x.test" || !g.Members[0].Known || !g.Members[1].Known {
		t.Errorf("G members = %+v, want m1 and m2 readable", g.Members)
	}
	wantEntry(t, got, "g2@x.test", false)
	if g2 := got["g2@x.test"]; !g2.IsGroup() || len(g2.Members) != 0 {
		t.Errorf("G2 = %+v, want a group with no members", g2)
	}
	wantEntry(t, got, "g3@x.test", false, TimeSlot{at(15, 0), at(16, 0)})
	if g3 := got["g3@x.test"]; len(g3.Members) != 2 || !g3.Members[0].Known || g3.Members[1].Known {
		t.Errorf("G3 members = %+v, want m3 readable and m4 unknown", g3.Members)
	}
	for _, extra := range []string{"m1@x.test", "m2@x.test", "stray@x.test"} {
		if _, ok := got[extra]; ok {
			t.Errorf("%s answered at the top level; members belong to their group, extras are dropped", extra)
		}
	}
}

// §3.1: keys match exactly, else case-insensitively; repeats are asked once.
func TestPAC133_GoogleCaseInsensitiveMatchAndRepeats(t *testing.T) {
	_, reqs := recordingStandIn(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"calendars":{"primary":{"busy":[]},"bob@x.test":{"busy":[{"start":"2030-01-07T09:00:00Z","end":"2030-01-07T10:00:00Z"}]}}}`))
	})
	got, err := newTestClient(t).GetFreeBusy(context.Background(), []string{"primary", "Bob@X.test", "primary"}, pac133Day, pac133Day.Add(time.Hour*24))
	if err != nil {
		t.Fatal(err)
	}
	wantEntry(t, got, "primary", true)
	wantEntry(t, got, "Bob@X.test", true, TimeSlot{at(9, 0), at(10, 0)})
	var body struct {
		Items []struct{ ID string } `json:"items"`
	}
	_ = json.Unmarshal(reqs()[0].body, &body)
	if len(body.Items) != 2 {
		t.Errorf("items = %+v, want the two distinct ids", body.Items)
	}
}

func TestPAC133_GoogleWholeCallFailureIsTheError(t *testing.T) {
	recordingStandIn(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":500,"message":"boom"}}`))
	})
	got, err := newTestClient(t).GetFreeBusy(context.Background(), []string{"a@x.test"}, pac133Day, pac133Day.Add(time.Hour))
	if err == nil {
		t.Fatalf("a failed call answered %+v, want the error", got)
	}
}

// --- The backend-test Graph stand-in (PAC-133 §7.3; shareable with PAC-129 AC-14).

// GraphSchedule is one schedule the stand-in answers: an optional error and
// the items (status, start, end in Graph's local-time format).
type graphStandInSchedule struct {
	Error map[string]any
	Items []graphStandInItem
}

type graphStandInItem struct{ Status, Start, End string }

// graphStandIn answers POST /me/calendar/getSchedule from a per-test table.
// Addresses not in the table are omitted from the answer. It records every
// request's schedules, may refuse more than maxSchedules (0 = no limit), and
// fails the request whose 1-based number is failRequest (0 = none).
type graphStandIn struct {
	mu           sync.Mutex
	table        map[string]graphStandInSchedule
	maxSchedules int
	failRequest  int
	requests     [][]string
	srv          *httptest.Server
}

func newGraphStandIn(t *testing.T, table map[string]graphStandInSchedule) *graphStandIn {
	t.Helper()
	g := &graphStandIn{table: table}
	g.srv = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.srv.Close)
	InstallGraphBaseURL(t, g.srv.URL+"/v1.0")
	return g
}

func (g *graphStandIn) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/v1.0/me/calendar/getSchedule" {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Schedules []string `json:"schedules"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":{"code":"BadRequest"}}`, http.StatusBadRequest)
		return
	}
	g.mu.Lock()
	g.requests = append(g.requests, body.Schedules)
	n := len(g.requests)
	g.mu.Unlock()
	if g.failRequest == n {
		http.Error(w, `{"error":{"code":"ServiceUnavailable"}}`, http.StatusServiceUnavailable)
		return
	}
	if g.maxSchedules > 0 && len(body.Schedules) > g.maxSchedules {
		http.Error(w, `{"error":{"code":"ErrorInvalidRequest","message":"too many schedules"}}`, http.StatusBadRequest)
		return
	}
	value := []map[string]any{}
	for _, asked := range body.Schedules {
		// Graph answers under the mailbox's own address, whatever its case
		// in the request.
		id, s, ok := g.lookup(asked)
		if !ok {
			continue
		}
		items := []map[string]any{}
		for _, it := range s.Items {
			items = append(items, map[string]any{
				"status": it.Status,
				"start":  map[string]string{"dateTime": it.Start, "timeZone": "UTC"},
				"end":    map[string]string{"dateTime": it.End, "timeZone": "UTC"},
			})
		}
		entry := map[string]any{"scheduleId": id, "scheduleItems": items}
		if s.Error != nil {
			entry["error"] = s.Error
		}
		value = append(value, entry)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"value": value})
}

func (g *graphStandIn) lookup(asked string) (string, graphStandInSchedule, bool) {
	for id, s := range g.table {
		if strings.EqualFold(id, asked) {
			return id, s, true
		}
	}
	return "", graphStandInSchedule{}, false
}

func (g *graphStandIn) recorded() [][]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([][]string(nil), g.requests...)
}

func graphTime(h, m int) string { return at(h, m).Format("2006-01-02T15:04:05.0000000") }

// AC-4: a schedule error is unknown (windows kept); no error is readable;
// an omitted address is unknown.
func TestPAC133_AC4_GraphScheduleError(t *testing.T) {
	notFound := map[string]any{"message": "not found", "responseCode": "ErrorMailboxNotFound"}
	g := newGraphStandIn(t, map[string]graphStandInSchedule{
		"a@x.test": {Error: notFound},
		"b@x.test": {Items: []graphStandInItem{{"busy", graphTime(9, 0), graphTime(10, 0)}, {"free", graphTime(11, 0), graphTime(12, 0)}}},
		"c@x.test": {},
		"e@x.test": {Error: notFound, Items: []graphStandInItem{{"busy", graphTime(13, 0), graphTime(14, 0)}}},
	})
	c := NewOutlookClient(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "m"}))
	got, err := c.GetFreeBusy(context.Background(), []string{"a@x.test", "b@x.test", "c@x.test", "e@x.test", "f@x.test"}, pac133Day, pac133Day.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("GetFreeBusy: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("answer = %+v, want five addresses", got)
	}
	wantEntry(t, got, "a@x.test", false)
	wantEntry(t, got, "b@x.test", true, TimeSlot{at(9, 0), at(10, 0)})
	wantEntry(t, got, "c@x.test", true)
	wantEntry(t, got, "e@x.test", false, TimeSlot{at(13, 0), at(14, 0)})
	wantEntry(t, got, "f@x.test", false)
	if reqs := g.recorded(); len(reqs) != 1 || len(reqs[0]) != 5 {
		t.Errorf("requests = %v, want one with the five addresses", reqs)
	}
}

func TestPAC133_GraphNullErrorIsReadableAndFailureIsTheError(t *testing.T) {
	newGraphStandIn(t, map[string]graphStandInSchedule{"a@x.test": {}})
	c := NewOutlookClient(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "m"}))
	got, err := c.GetFreeBusy(context.Background(), []string{"A@x.test"}, pac133Day, pac133Day.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	wantEntry(t, got, "A@x.test", true)

	failing := newGraphStandIn(t, nil)
	failing.failRequest = 1
	if got, err := c.GetFreeBusy(context.Background(), []string{"a@x.test"}, pac133Day, pac133Day.Add(time.Hour)); err == nil {
		t.Fatalf("a failed getSchedule answered %+v, want the error", got)
	}
}

func TestPAC133_GraphScheduleNullErrorDecodes(t *testing.T) {
	got := graphFreeBusyAnswer([]string{"a@x.test"}, []graphSchedule{{ScheduleID: "a@x.test", Error: json.RawMessage("null")}})
	if !got["a@x.test"].Known {
		t.Errorf(`"error": null read as an error: %+v`, got)
	}
}

// §8: the seam is cleared when the test ends, and the published address is
// the default.
func TestPAC133_InstallGraphBaseURL(t *testing.T) {
	if graphBase() != graphPublishedURL {
		t.Fatalf("graphBase = %q before any install", graphBase())
	}
	t.Run("installed", func(t *testing.T) {
		InstallGraphBaseURL(t, "http://graph.stand-in.test/v1.0")
		if graphBase() != "http://graph.stand-in.test/v1.0" {
			t.Errorf("graphBase = %q", graphBase())
		}
	})
	if graphBase() != graphPublishedURL {
		t.Errorf("graphBase = %q after the test ended, want the published address", graphBase())
	}
}

// §3.3: the dead WebCal reader answers every address unknown, never free.
func TestPAC133_WebcalFreeBusyIsUnknown(t *testing.T) {
	got, err := NewWebcalClient("https://feed.example/x.ics").GetFreeBusy(context.Background(), []string{"a@x.test", "a@x.test", "b@x.test"}, pac133Day, pac133Day.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["a@x.test"].Known || got["b@x.test"].Known || got["a@x.test"].Busy == nil {
		t.Errorf("webcal answer = %+v, want both unknown with no windows", got)
	}
}
