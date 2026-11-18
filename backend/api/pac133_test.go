package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/nlp"
	"github.com/Enach/paceday/backend/nlp/nlptest"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	googlecalendar "google.golang.org/api/calendar/v3"
)

// PAC-133: a calendar the provider cannot read is unknown, never free.

// pac133Google is a Google Calendar stand-in whose freeBusy answers with
// body(ids) (the whole Google JSON) and records each call's ids; status, when
// set, fails every call with it. Event listings answer empty.
type pac133Google struct {
	mu     sync.Mutex
	calls  [][]string
	body   func(ids []string) string
	status int
}

func newPAC133Google(t *testing.T, body func(ids []string) string) *pac133Google {
	t.Helper()
	g := &pac133Google{body: body}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !strings.HasSuffix(r.URL.Path, "/freeBusy") {
			_, _ = w.Write([]byte(`{"kind":"calendar#events","items":[]}`))
			return
		}
		var req googlecalendar.FreeBusyRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		ids := []string{}
		for _, it := range req.Items {
			ids = append(ids, it.Id)
		}
		g.mu.Lock()
		g.calls = append(g.calls, ids)
		status := g.status
		g.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"code":500,"message":"backend error"}}`))
			return
		}
		_, _ = w.Write([]byte(g.body(ids)))
	}))
	t.Cleanup(srv.Close)
	calendar.Configure(config.CalendarEndpoint{BaseURL: srv.URL + config.CalendarAPIRoot, Guard: true})
	t.Cleanup(func() { calendar.Configure(config.ProductionCalendarEndpoint()) })
	return g
}

func (g *pac133Google) recorded() [][]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([][]string(nil), g.calls...)
}

// calendarsBody answers each requested id from table (its Google calendars
// entry as JSON); ids not in table are left out of the answer. groups is the
// Google groups member, verbatim ("" for none).
func calendarsBody(table map[string]string, groups string) func([]string) string {
	return func(ids []string) string {
		parts := []string{}
		for id, entry := range table {
			k, _ := json.Marshal(id)
			parts = append(parts, string(k)+":"+entry)
		}
		out := `{"kind":"calendar#freeBusy","calendars":{` + strings.Join(parts, ",") + `}`
		if groups != "" {
			out += `,"groups":` + groups
		}
		return out + `}`
	}
}

const (
	notFound   = `{"busy":[],"errors":[{"domain":"global","reason":"notFound"}]}`
	freeEntry  = `{"busy":[]}`
	busy10to11 = `{"busy":[{"start":"2030-01-07T10:00:00Z","end":"2030-01-07T11:00:00Z"}]}`
)

// --- Raw lookup (getCalendarFreeBusy) ------------------------------------------

func rawLookup(t *testing.T, user uuid.UUID, query string) *httptest.ResponseRecorder {
	t.Helper()
	h := &calendarHandlers{oauthConfig: &oauth2.Config{}, db: openTestDB(t)}
	w := httptest.NewRecorder()
	h.freeBusy(w, withUser(httptest.NewRequest(http.MethodGet, "/api/calendar/freebusy?"+query, nil), user))
	return w
}

// strictDecode decodes b into v, refusing any member v does not declare.
func strictUnmarshal(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// checkRawFreeBusyAnswer is AC-18's second opinion (API-091): it decodes a
// 200 against the bundle's RawFreeBusyAnswer and its oneOf variants, each
// strictly, the variant chosen by status, and refuses an unknown entry with
// an empty busy list and a group nested in a member. It returns the decoded
// own outcome and calendars by address.
func checkRawFreeBusyAnswer(body []byte) (string, map[string]rawCal, error) {
	var top struct {
		Own       json.RawMessage   `json:"own"`
		Calendars []json.RawMessage `json:"calendars"`
	}
	if err := strictUnmarshal(body, &top); err != nil {
		return "", nil, fmt.Errorf("envelope: %w", err)
	}
	if top.Own == nil || top.Calendars == nil {
		return "", nil, fmt.Errorf("own and calendars are required: %s", body)
	}
	// Go's decoder matches member names case-insensitively, so every key
	// is also checked exactly: windows are {start, end} (PAC-26).
	var generic any
	_ = json.Unmarshal(body, &generic)
	if err := exactKeys(generic); err != nil {
		return "", nil, err
	}
	var answer gen.RawFreeBusyAnswer
	if err := json.Unmarshal(body, &answer); err != nil {
		return "", nil, fmt.Errorf("gen.RawFreeBusyAnswer: %w", err)
	}

	status, err := statusOf(top.Own)
	if err != nil {
		return "", nil, err
	}
	if status == "known" {
		var v gen.RawFreeBusyOwnKnown
		if err := strictUnmarshal(top.Own, &v); err != nil || v.Busy == nil {
			return "", nil, fmt.Errorf("own known: %v (busy required) %s", err, top.Own)
		}
	} else {
		var v gen.RawFreeBusyOwnUnknown
		if err := strictUnmarshal(top.Own, &v); err != nil {
			return "", nil, fmt.Errorf("own unknown: %w", err)
		}
		if v.Busy != nil && len(*v.Busy) == 0 {
			return "", nil, fmt.Errorf("own unknown with an empty busy list: %s", top.Own)
		}
	}

	cals := map[string]rawCal{}
	for _, raw := range top.Calendars {
		c, err := decodeRawCal(raw, false)
		if err != nil {
			return "", nil, err
		}
		cals[c.address] = c
	}
	return status, cals, nil
}

// exactKeys refuses any object key outside the schema's exact spellings, and
// any busy window that is not exactly {start, end}.
func exactKeys(v any) error {
	allowed := map[string]bool{"own": true, "calendars": true, "status": true, "busy": true, "address": true, "group": true, "members": true, "start": true, "end": true}
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			if !allowed[k] {
				return fmt.Errorf("member %q is not in the schema", k)
			}
			if k == "busy" {
				windows, _ := child.([]any)
				for _, w := range windows {
					m, _ := w.(map[string]any)
					if _, ok := m["start"]; !ok || len(m) != 2 {
						return fmt.Errorf("window %v is not {start, end}", w)
					}
					if _, ok := m["end"]; !ok {
						return fmt.Errorf("window %v is not {start, end}", w)
					}
				}
			}
			if err := exactKeys(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range x {
			if err := exactKeys(child); err != nil {
				return err
			}
		}
	}
	return nil
}

type rawCal struct {
	address string
	status  string
	busy    []gen.RawGoogleBusyWindow
	hasBusy bool
	group   *[]rawCal
}

func statusOf(raw json.RawMessage) (string, error) {
	var s struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", err
	}
	if s.Status != "known" && s.Status != "unknown" {
		return "", fmt.Errorf("status %q is neither known nor unknown: %s", s.Status, raw)
	}
	return s.Status, nil
}

func decodeRawCal(raw json.RawMessage, member bool) (rawCal, error) {
	status, err := statusOf(raw)
	if err != nil {
		return rawCal{}, err
	}
	c := rawCal{status: status}
	var group *gen.RawFreeBusyGroup
	switch {
	case member && status == "known":
		var v gen.RawFreeBusyMemberKnown
		if err := strictUnmarshal(raw, &v); err != nil || v.Busy == nil {
			return c, fmt.Errorf("member known: %v (busy required) %s", err, raw)
		}
		c.address, c.busy, c.hasBusy = v.Address, v.Busy, true
	case member:
		var v gen.RawFreeBusyMemberUnknown
		if err := strictUnmarshal(raw, &v); err != nil {
			return c, fmt.Errorf("member unknown: %w %s", err, raw)
		}
		c.address = v.Address
		if v.Busy != nil {
			c.busy, c.hasBusy = *v.Busy, true
		}
	case status == "known":
		var v gen.RawFreeBusyCalendarKnown
		if err := strictUnmarshal(raw, &v); err != nil || v.Busy == nil {
			return c, fmt.Errorf("calendar known: %v (busy required) %s", err, raw)
		}
		c.address, c.busy, c.hasBusy, group = v.Address, v.Busy, true, v.Group
	default:
		var v gen.RawFreeBusyCalendarUnknown
		if err := strictUnmarshal(raw, &v); err != nil {
			return c, fmt.Errorf("calendar unknown: %w %s", err, raw)
		}
		c.address, group = v.Address, v.Group
		if v.Busy != nil {
			c.busy, c.hasBusy = *v.Busy, true
		}
	}
	if status == "unknown" && c.hasBusy && len(c.busy) == 0 {
		return c, fmt.Errorf("an unknown calendar with an empty busy list: %s", raw)
	}
	if group != nil {
		// Re-read the members strictly as RawFreeBusyMember variants.
		var g struct {
			Group struct {
				Members []json.RawMessage `json:"members"`
			} `json:"group"`
		}
		_ = json.Unmarshal(raw, &g)
		members := []rawCal{}
		for _, m := range g.Group.Members {
			mc, err := decodeRawCal(m, true)
			if err != nil {
				return c, err
			}
			members = append(members, mc)
		}
		c.group = &members
	}
	return c, nil
}

// AC-18: the checker itself refuses what the schema forbids.
func TestPAC133_AC18_CheckerRefusesWhatTheSchemaForbids(t *testing.T) {
	for name, body := range map[string]string{
		"unknown with busy []":   `{"own":{"status":"known","busy":[]},"calendars":[{"address":"u@x.test","status":"unknown","busy":[]}]}`,
		"nested group":           `{"own":{"status":"known","busy":[]},"calendars":[{"address":"g@x.test","status":"known","busy":[],"group":{"members":[{"address":"m@x.test","status":"known","busy":[],"group":{"members":[]}}]}}]}`,
		"unexpected member":      `{"own":{"status":"known","busy":[]},"calendars":[],"extra":1}`,
		"known without busy":     `{"own":{"status":"known"},"calendars":[]}`,
		"a third status":         `{"own":{"status":"partial","busy":[]},"calendars":[]}`,
		"the old map":            `{"u@x.test":[]}`,
		"window with other keys": `{"own":{"status":"known","busy":[{"Start":"2030-01-07T10:00:00Z","End":"2030-01-07T11:00:00Z"}]},"calendars":[]}`,
	} {
		if _, _, err := checkRawFreeBusyAnswer([]byte(body)); err == nil {
			t.Errorf("%s: accepted %s", name, body)
		}
	}
}

// AC-14 (unit), AC-15, AC-18: readable, unknown with and without windows, an
// omitted address and groups, decoded strictly against the bundle schema.
func TestPAC133_AC15_AC18_RawLookupOutcomes(t *testing.T) {
	g := newPAC133Google(t, calendarsBody(map[string]string{
		"primary":      freeEntry,
		"s@x.test":     busy10to11,
		"u@x.test":     notFound,
		"p@x.test":     `{"busy":[{"start":"2030-01-07T13:00:00Z","end":"2030-01-07T14:00:00Z"}],"errors":[{"domain":"global","reason":"internalError"}]}`,
		"m1@x.test":    busy10to11,
		"m2@x.test":    freeEntry,
		"m3@x.test":    `{"busy":[{"start":"2030-01-07T15:00:00Z","end":"2030-01-07T16:00:00Z"}]}`,
		"m4@x.test":    notFound,
		"extra@x.test": busy10to11,
	}, `{"g@x.test":{"calendars":["m1@x.test","m2@x.test"]},"g2@x.test":{"errors":[{"domain":"global","reason":"groupTooBig"}]},"g3@x.test":{"calendars":["m3@x.test","m4@x.test"]}}`))
	user := userWithGoogleToken(t, "pac133-raw-"+uuid.NewString()+"@example.com")
	w := rawLookup(t, user, "start=2030-01-07T00:00:00Z&end=2030-01-08T00:00:00Z&attendees=s@x.test,u@x.test,p@x.test,o@x.test,g@x.test,g2@x.test,g3@x.test,s@x.test")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
	own, cals, err := checkRawFreeBusyAnswer(w.Body.Bytes())
	if err != nil {
		t.Fatalf("second opinion: %v", err)
	}
	if own != "known" {
		t.Errorf("own = %s", own)
	}
	if calls := g.recorded(); len(calls) != 1 || calls[0][0] != "primary" || len(calls[0]) != 8 {
		t.Errorf("provider asked %v, want one call with primary and the 7 distinct attendees", calls)
	}
	var order struct {
		Calendars []struct{ Address string } `json:"calendars"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &order)
	gotOrder := []string{}
	for _, c := range order.Calendars {
		gotOrder = append(gotOrder, c.Address)
	}
	if strings.Join(gotOrder, ",") != "s@x.test,u@x.test,p@x.test,o@x.test,g@x.test,g2@x.test,g3@x.test" {
		t.Errorf("calendars = %v, want each requested attendee once, in order", gotOrder)
	}
	want := func(addr, status string, windows int) {
		t.Helper()
		c, ok := cals[addr]
		if !ok || c.status != status || len(c.busy) != windows {
			t.Errorf("%s = %+v, want %s with %d windows", addr, c, status, windows)
		}
	}
	want("s@x.test", "known", 1)
	want("u@x.test", "unknown", 0)
	want("p@x.test", "unknown", 1)
	want("o@x.test", "unknown", 0)
	want("g@x.test", "known", 1)
	want("g2@x.test", "unknown", 0)
	want("g3@x.test", "unknown", 1)
	if c := cals["u@x.test"]; c.hasBusy {
		t.Errorf("U carries a busy member: %s", w.Body.String())
	}
	if c := cals["g@x.test"]; c.group == nil || len(*c.group) != 2 || (*c.group)[0].address != "m1@x.test" {
		t.Errorf("G group = %+v, want members m1 and m2", c.group)
	}
	if c := cals["g2@x.test"]; c.group == nil || len(*c.group) != 0 {
		t.Errorf("G2 group = %+v, want no members", c.group)
	}
	if c := cals["g3@x.test"]; c.group == nil || (*c.group)[1].status != "unknown" {
		t.Errorf("G3 members = %+v, want m4 unknown", c.group)
	}
	for _, extra := range []string{"m1@x.test", "extra@x.test", "primary"} {
		if _, ok := cals[extra]; ok {
			t.Errorf("%s listed in calendars", extra)
		}
	}
}

// AC-17: no attendees → the provider is asked about the own calendar, never
// zero calendars; an error for it is own unknown, still a 200.
func TestPAC133_AC17_OwnCalendarAlwaysRead(t *testing.T) {
	g := newPAC133Google(t, calendarsBody(map[string]string{"primary": notFound}, ""))
	user := userWithGoogleToken(t, "pac133-own-"+uuid.NewString()+"@example.com")
	w := rawLookup(t, user, "start=2030-01-07T00:00:00Z&end=2030-01-08T00:00:00Z")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", w.Code, w.Body.String())
	}
	if calls := g.recorded(); len(calls) != 1 || len(calls[0]) != 1 || calls[0][0] != "primary" {
		t.Errorf("provider asked %v, want exactly [primary]", calls)
	}
	own, cals, err := checkRawFreeBusyAnswer(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if own != "unknown" || len(cals) != 0 {
		t.Errorf("own = %s, calendars = %v; want own unknown and no calendars", own, cals)
	}
	if strings.TrimSpace(w.Body.String()) != `{"calendars":[],"own":{"status":"unknown"}}` {
		t.Errorf("body = %s", w.Body.String())
	}
}

// AC-16: end not after start is a plain-text 400 with no provider call, also
// for a user with no Google credential; a failed call is still the 500; no
// credential and a valid window is still the 401.
func TestPAC133_AC16_RawLookupWindowAndOrder(t *testing.T) {
	g := newPAC133Google(t, calendarsBody(map[string]string{"primary": freeEntry}, ""))
	connected := userWithGoogleToken(t, "pac133-win-"+uuid.NewString()+"@example.com")
	unconnected := createTestUser(t, "pac133-win-none-"+uuid.NewString()+"@example.com")
	for _, user := range []uuid.UUID{connected, unconnected} {
		for _, q := range []string{
			"start=2030-01-07T10:00:00Z&end=2030-01-07T10:00:00Z",
			"start=2030-01-07T10:00:00Z&end=2030-01-07T09:00:00Z",
			"start=2030-01-07T11:00:00%2B01:00&end=2030-01-07T10:00:00Z",
		} {
			w := rawLookup(t, user, q)
			if w.Code != http.StatusBadRequest || w.Body.String() != "end must be after start\n" || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
				t.Errorf("%s: %d %q %s, want the plain-text 400", q, w.Code, w.Body.String(), w.Header().Get("Content-Type"))
			}
		}
	}
	if w := rawLookup(t, unconnected, "start=bad&end=2030-01-07T10:00:00Z"); w.Code != http.StatusBadRequest || w.Body.String() != "invalid start param\n" {
		t.Errorf("parse 400 first: %d %q", w.Code, w.Body.String())
	}
	if len(g.recorded()) != 0 {
		t.Errorf("provider called %v on a refused window", g.recorded())
	}
	if w := rawLookup(t, unconnected, "start=2030-01-07T09:00:00Z&end=2030-01-07T10:00:00Z"); w.Code != http.StatusUnauthorized {
		t.Errorf("no credential, valid window: %d %s, want the 401", w.Code, w.Body.String())
	}
	g.status = http.StatusInternalServerError
	if w := rawLookup(t, connected, "start=2030-01-07T09:00:00Z&end=2030-01-07T10:00:00Z"); w.Code != http.StatusInternalServerError || strings.TrimSpace(w.Body.String()) != `{"error":"internal error"}` {
		t.Errorf("failed call: %d %s, want the 500", w.Code, w.Body.String())
	}
}

// --- Participant lookup (queryFreeBusy) ---------------------------------------

func participantLookup(t *testing.T, h *freeBusyHandlers, user uuid.UUID, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.query(w, withUser(httptest.NewRequest(http.MethodPost, "/api/freebusy", strings.NewReader(body)), user))
	return w
}

// AC-10: a window that does not move forward is the 400, last of the 400s,
// with no provider call.
func TestPAC133_AC10_ParticipantLookupWindow(t *testing.T) {
	g := newPAC133Google(t, calendarsBody(map[string]string{"a@corp.example": freeEntry}, ""))
	user := userWithGoogleToken(t, "pac133-pl-"+uuid.NewString()+"@example.com")
	h := newFreeBusyHandlers(openTestDB(t), &oauth2.Config{})
	many := make([]string, 21)
	for i := range many {
		many[i] = fmt.Sprintf(`"a%d@corp.example"`, i)
	}
	for _, tc := range []struct{ body, want string }{
		{`{`, "invalid JSON"},
		{`{"emails":[],"start_time":"2030-01-07T10:00:00Z","end_time":"2030-01-07T09:00:00Z"}`, "emails is required"},
		{`{"emails":[` + strings.Join(many, ",") + `],"start_time":"2030-01-07T10:00:00Z","end_time":"2030-01-07T09:00:00Z"}`, "max 20 emails per request"},
		{`{"emails":["a@corp.example"],"start_time":"x","end_time":"2030-01-07T09:00:00Z"}`, "start_time must be RFC3339"},
		{`{"emails":["a@corp.example"],"start_time":"2030-01-07T10:00:00Z","end_time":"x"}`, "end_time must be RFC3339"},
		{`{"emails":["a@corp.example"],"start_time":"2030-01-07T10:00:00Z","end_time":"2030-01-07T10:00:00Z"}`, "end_time must be after start_time"},
		{`{"emails":["a@corp.example"],"start_time":"2030-01-07T10:00:00Z","end_time":"2030-01-07T09:00:00Z"}`, "end_time must be after start_time"},
	} {
		w := participantLookup(t, h, user, tc.body)
		if w.Code != http.StatusBadRequest || strings.TrimSpace(w.Body.String()) != `{"error":"`+tc.want+`"}` {
			t.Errorf("%s: %d %s, want 400 %q", tc.body, w.Code, w.Body.String(), tc.want)
		}
	}
	if len(g.recorded()) != 0 {
		t.Errorf("provider called %v", g.recorded())
	}
	if w := participantLookup(t, h, user, `{"emails":["a@corp.example"],"start_time":"2030-01-07T09:00:00Z","end_time":"2030-01-07T10:00:00Z"}`); w.Code != http.StatusOK {
		t.Errorf("a valid window: %d %s", w.Code, w.Body.String())
	}
}

// AC-5 (unit), AC-12, AC-13: unknown in all three views, with the windows
// reported for it; a readable attendee with nothing busy stays known with [].
func TestPAC133_AC12_ParticipantLookupThreeViews(t *testing.T) {
	newPAC133Google(t, calendarsBody(map[string]string{
		"s@corp.example":  busy10to11,
		"u@corp.example":  notFound,
		"p@corp.example":  `{"busy":[{"start":"2030-01-07T13:00:00Z","end":"2030-01-07T14:00:00Z"}],"errors":[{"domain":"global","reason":"internalError"}]}`,
		"f@corp.example":  freeEntry,
		"m3@corp.example": `{"busy":[{"start":"2030-01-07T15:00:00Z","end":"2030-01-07T16:00:00Z"}]}`,
		"m4@corp.example": notFound,
	}, `{"g3@corp.example":{"calendars":["m3@corp.example","m4@corp.example"]}}`))
	user := userWithGoogleToken(t, "pac133-views-"+uuid.NewString()+"@example.com")
	h := newFreeBusyHandlers(openTestDB(t), &oauth2.Config{})
	w := participantLookup(t, h, user, `{"emails":["s@corp.example","u@corp.example","p@corp.example","f@corp.example","g3@corp.example"],"start_time":"2030-01-07T00:00:00Z","end_time":"2030-01-08T00:00:00Z"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Participants []struct {
			Email, Status string
			Busy          []map[string]string
		} `json:"participants"`
		Busy    map[string][]map[string]string `json:"busy"`
		Results []struct {
			Email, Coverage string
			Busy            []map[string]string
		} `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		status string
		starts []string
	}{
		"s@corp.example":  {"known", []string{"2030-01-07T10:00:00Z"}},
		"u@corp.example":  {"unknown", []string{}},
		"p@corp.example":  {"unknown", []string{"2030-01-07T13:00:00Z"}},
		"f@corp.example":  {"known", []string{}},
		"g3@corp.example": {"unknown", []string{"2030-01-07T15:00:00Z"}},
	}
	starts := func(ws []map[string]string) string {
		out := []string{}
		for _, w := range ws {
			out = append(out, w["start"])
		}
		return strings.Join(out, ",")
	}
	for _, p := range body.Participants {
		x := want[p.Email]
		if p.Status != x.status || starts(p.Busy) != strings.Join(x.starts, ",") || p.Busy == nil {
			t.Errorf("participants %s = %s %v, want %s %v", p.Email, p.Status, p.Busy, x.status, x.starts)
		}
		if m, ok := body.Busy[p.Email]; !ok || m == nil || starts(m) != strings.Join(x.starts, ",") {
			t.Errorf("busy[%s] = %v, want %v", p.Email, m, x.starts)
		}
	}
	for _, r := range body.Results {
		x := want[r.Email]
		if r.Coverage != x.status || starts(r.Busy) != strings.Join(x.starts, ",") {
			t.Errorf("results %s = %s %v, want %s %v", r.Email, r.Coverage, r.Busy, x.status, x.starts)
		}
	}
	if len(body.Participants) != 5 || len(body.Results) != 5 {
		t.Errorf("body = %s", w.Body.String())
	}
}

// --- Meeting suggestions -------------------------------------------------------

// AC-21 (handler), AC-23: the answer names the unknown attendee; an unknown
// own calendar is the operation's 500.
func TestPAC133_AC21_AC23_SuggestHandler(t *testing.T) {
	own := freeEntry
	newPAC133Google(t, func(ids []string) string {
		return calendarsBody(map[string]string{"primary": own, "b@corp.example": busy10to11, "u@corp.example": notFound}, "")(ids)
	})
	user := userWithGoogleToken(t, "pac133-suggest-"+uuid.NewString()+"@example.com")
	db := openTestDB(t)
	h := &scheduleHandlers{eng: &mockCompressor{}, smart: &engine.SmartScheduler{DB: db, OAuthConfig: &oauth2.Config{}}, db: db}
	call := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.suggestMeeting(w, withUser(httptest.NewRequest(http.MethodPost, "/api/schedule/suggest", strings.NewReader(
			`{"duration_minutes":30,"attendees":["b@corp.example","u@corp.example"],"range_start":"2030-01-07T09:00:00Z","range_end":"2030-01-07T17:00:00Z","title":"x"}`)), user))
		return w
	}
	w := call()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d %s", w.Code, w.Body.String())
	}
	var body gen.ScheduleSuggestions
	if err := strictUnmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode against gen.ScheduleSuggestions: %v", err)
	}
	if len(body.UnknownAttendees) != 1 || body.UnknownAttendees[0] != "u@corp.example" || len(body.Slots) == 0 {
		t.Errorf("body = %s, want slots and unknown_attendees [u@corp.example]", w.Body.String())
	}
	busyStart := time.Date(2030, 1, 7, 10, 0, 0, 0, time.UTC)
	for _, s := range body.Slots {
		if s.Start.Before(busyStart.Add(time.Hour)) && s.End.After(busyStart) {
			t.Errorf("slot %v overlaps B's 10:00-11:00", s)
		}
	}

	own = notFound
	w = call()
	if w.Code != http.StatusInternalServerError || strings.TrimSpace(w.Body.String()) != `{"error":"internal error"}` {
		t.Errorf("own calendar unknown: %d %s, want the 500", w.Code, w.Body.String())
	}
}

// --- NLP parse (AC-28, SF-037 first clause, backend half) ----------------------

// pac133Parse runs NLP parse for a new user whose model answers a
// schedule_meeting intent (with llmError in error when set), the user's
// calendar connected (own answered with ownEntry) or not.
func pac133Parse(t *testing.T, llmError, ownEntry string, connected bool) *nlp.ParseResult {
	t.Helper()
	extra := ""
	if llmError != "" {
		b, _ := json.Marshal(llmError)
		extra = `,"error":` + string(b)
	}
	content := `{"intent":"schedule_meeting","title":"Sync","duration_minutes":30,"attendees":["b@corp.example"],"range_start":"2030-01-07","range_end":"2030-01-07"` + extra + `}`
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": content}}}})
	}))
	t.Cleanup(provider.Close)
	t.Cleanup(nlp.SetUserTransport(nlptest.ForwardTo(provider)))

	db := openTestDB(t)
	user := createTestUser(t, "pac133-nlp-"+uuid.NewString()+"@example.com")
	s, err := storage.GetOrCreateSettingsByUser(db, user)
	if err != nil {
		t.Fatal(err)
	}
	s.LLMProvider, s.LLMModel, s.LLMBaseURL = "openai", "gpt-test", ""
	s.LLMAPIKeys.Set("openai", "sk-pac133")
	if err := storage.SaveSettingsForUser(db, user, s); err != nil {
		t.Fatal(err)
	}
	if connected {
		newPAC133Google(t, func(ids []string) string {
			table := map[string]string{"primary": ownEntry}
			for _, id := range ids {
				if id != "primary" {
					table[id] = freeEntry
				}
			}
			return calendarsBody(table, "")(ids)
		})
		if err := auth.UpsertUserToken(db, user, &oauth2.Token{AccessToken: "g", Expiry: time.Now().Add(24 * time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/nlp/parse", nil), user)
	res, err := (&nlp.NLPService{DB: db, OAuthConfig: &oauth2.Config{}}).Parse(req.Context(), "schedule a 30 minute sync with b@corp.example on 7 January 2030")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if res.Intent != "schedule_meeting" || res.Title != "Sync" || res.DurationMinutes != 30 || len(res.Attendees) != 1 {
		t.Errorf("other members changed: %+v", res)
	}
	return res
}

func TestPAC133_AC28_NLPParseSaysAFailedSuggestionStep(t *testing.T) {
	t.Run("own calendar unknown, no model error", func(t *testing.T) {
		res := pac133Parse(t, "", notFound, true)
		if res.Error != "Paceday could not suggest meeting times for this request. Try again later." {
			t.Errorf("error = %q, want the fixed failure sentence", res.Error)
		}
		if body, _ := json.Marshal(res); strings.Contains(string(body), "suggested_slots") {
			t.Errorf("suggested_slots present: %s", body)
		}
	})
	t.Run("no calendar connected", func(t *testing.T) {
		if res := pac133Parse(t, "", "", false); res.Error != "No Google calendar is connected, so Paceday cannot suggest meeting times." {
			t.Errorf("error = %q, want the not-connected sentence", res.Error)
		}
	})
	t.Run("the model's own error wins", func(t *testing.T) {
		if res := pac133Parse(t, "I could not tell which week you meant.", notFound, true); res.Error != "I could not tell which week you meant." {
			t.Errorf("error = %q, want the model's error unchanged", res.Error)
		}
	})
	t.Run("a successful step sets no error", func(t *testing.T) {
		if res := pac133Parse(t, "", freeEntry, true); res.Error != "" || len(res.SuggestedSlots) == 0 {
			t.Errorf("error = %q, slots = %d; want no error and slots", res.Error, len(res.SuggestedSlots))
		}
	})
}
