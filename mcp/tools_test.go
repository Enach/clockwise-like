package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// callTool invokes a named tool on the server via JSON-RPC and returns the
// raw result text (or an error string if the call failed).
func callTool(t *testing.T, srv interface {
	HandleMessage(context.Context, json.RawMessage) mcp.JSONRPCMessage
}, name string, args map[string]any) string {
	t.Helper()
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	}
	msg, _ := json.Marshal(payload)
	resp := srv.HandleMessage(context.Background(), msg)
	b, _ := json.Marshal(resp)
	return string(b)
}

// mockBackend returns an httptest.Server that responds 200 with `{"ok":true}`
// for all requests, and a cleanup function.
func mockBackend(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
}

func TestStrArg_Present(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"key": "value"}
	if got := strArg(req, "key"); got != "value" {
		t.Errorf("strArg = %q, want %q", got, "value")
	}
}

func TestStrArg_Missing(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}
	if got := strArg(req, "missing"); got != "" {
		t.Errorf("strArg = %q, want empty string", got)
	}
}

func TestStrArg_WrongType(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"num": 42}
	if got := strArg(req, "num"); got != "" {
		t.Errorf("strArg with int value = %q, want empty string", got)
	}
}

func TestStrArg_NilArguments(t *testing.T) {
	req := mcp.CallToolRequest{}
	if got := strArg(req, "key"); got != "" {
		t.Errorf("strArg with nil args = %q, want empty string", got)
	}
}

func TestJsonResult_Success(t *testing.T) {
	result, err := jsonResult([]byte(`{"ok":true}`), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestJsonResult_Error(t *testing.T) {
	result, err := jsonResult(nil, fmt.Errorf("backend error"))
	if err != nil {
		t.Fatalf("jsonResult should absorb errors into result, got: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result even on error")
	}
}

// TestTools_AllHandlers exercises every tool handler registered in buildServer
// by routing calls through HandleMessage against a mock backend.
func TestTools_AllHandlers(t *testing.T) {
	backend := mockBackend(t)
	defer backend.Close()

	s := buildServer(newClient(backend.URL))

	tools := []struct {
		name string
		args map[string]any
	}{
		{"clockwise_list_events", map[string]any{
			"start": "2024-01-01T00:00:00Z",
			"end":   "2024-01-07T00:00:00Z",
		}},
		{"clockwise_create_event", map[string]any{
			"title":     "Team Sync",
			"attendees": "alice@example.com",
			"duration":  "30",
		}},
		{"clockwise_create_event", map[string]any{
			"title":          "Standup",
			"attendees":      "bob@example.com",
			"preferred_date": "2024-01-02",
			"location":       "Zoom",
		}},
		{"clockwise_update_event", map[string]any{
			"id":          "evt-123",
			"title":       "New Title",
			"description": "New desc",
			"location":    "Room A",
			"start":       "2024-01-02T10:00:00Z",
			"end":         "2024-01-02T11:00:00Z",
			"attendees":   "alice@example.com",
		}},
		{"clockwise_update_event", map[string]any{"id": "evt-456"}}, // no optional fields
		{"clockwise_delete_event", map[string]any{"id": "evt-789"}},
		{"clockwise_find_free_slots", map[string]any{
			"start": "2024-01-01T00:00:00Z",
			"end":   "2024-01-07T00:00:00Z",
		}},
		{"clockwise_run_focus_engine", map[string]any{}},
		{"clockwise_list_focus_blocks", map[string]any{}},
		{"clockwise_clear_focus_blocks", map[string]any{}},
		{"clockwise_compress_schedule", map[string]any{"date": "2024-01-02"}},
		{"clockwise_compress_schedule", map[string]any{}}, // no date
		{"clockwise_apply_compression", map[string]any{"plan": `{"moves":[]}`}},
		{"clockwise_parse_command", map[string]any{"command": "schedule a meeting tomorrow"}},
		{"clockwise_search_rooms", map[string]any{"q": "conf"}},
		{"clockwise_search_rooms", map[string]any{}}, // no query
		{"clockwise_suggest_attendees", map[string]any{"q": "alice"}},
		{"clockwise_suggest_attendees", map[string]any{}}, // no query
		{"clockwise_get_settings", map[string]any{}},
		{"clockwise_calendar_status", map[string]any{}},
		{"clockwise_suggest_meeting", map[string]any{
			"title":     "Sync",
			"attendees": "alice@example.com",
			"duration":  "30",
		}},
		{"clockwise_suggest_meeting", map[string]any{
			"title":     "Sync",
			"attendees": "alice@example.com",
		}},
	}

	for _, tt := range tools {
		t.Run(tt.name, func(t *testing.T) {
			resp := callTool(t, s, tt.name, tt.args)
			if resp == "" {
				t.Error("expected non-empty response")
			}
			// All calls hit the mock backend which returns 200, so none should
			// contain a JSON-RPC error code.
			if strings.Contains(resp, `"error"`) && strings.Contains(resp, `"code"`) {
				t.Errorf("unexpected JSON-RPC error in response: %s", resp)
			}
		})
	}
}

// TestTools_BackendError verifies handlers surface errors when the backend
// returns a 4xx/5xx — they should return a result (not a Go error) whose text
// contains "Error:".

func TestTools_SendBackendPayloads(t *testing.T) {
	var paths []string
	var bodies []map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Body != nil {
			body, _ := io.ReadAll(r.Body)
			if len(body) > 0 {
				var decoded map[string]any
				if err := json.Unmarshal(body, &decoded); err != nil {
					t.Fatalf("decode %s body: %v", r.URL.Path, err)
				}
				bodies = append(bodies, decoded)
			} else {
				bodies = append(bodies, nil)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/schedule/suggest" {
			_, _ = w.Write([]byte(`{"slots":[{"start":"2026-05-04T10:00:00Z","end":"2026-05-04T10:30:00Z"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()

	s := buildServer(newClient(backend.URL))
	callTool(t, s, "clockwise_parse_command", map[string]any{"command": "find focus time"})
	callTool(t, s, "clockwise_suggest_meeting", map[string]any{
		"title": "Planning", "attendees": "alice@example.com, bob@example.com", "duration": "45",
	})
	callTool(t, s, "clockwise_apply_compression", map[string]any{
		// PAC-122 SF-063: the plan as compress_schedule returns it (camelCase).
		"plan": `[{"date":"2026-05-04","proposals":[{"eventId":"evt-1","proposedStart":"2026-05-04T10:00:00Z","proposedEnd":"2026-05-04T10:30:00Z"}]}]`,
	})
	callTool(t, s, "clockwise_create_event", map[string]any{
		"title": "Standup", "attendees": "alice@example.com", "preferred_date": "2026-05-04",
	})
	callTool(t, s, "clockwise_update_event", map[string]any{
		"id": "evt-1", "attendees": "alice@example.com, bob@example.com",
	})

	if len(paths) != 6 {
		t.Fatalf("request count = %d, want 6 (%v)", len(paths), paths)
	}
	if paths[0] != "/api/nlp/parse" || bodies[0]["text"] != "find focus time" {
		t.Fatalf("NLP request = %s %+v", paths[0], bodies[0])
	}
	if got := bodies[1]["attendees"].([]any); len(got) != 2 || got[0] != "alice@example.com" || got[1] != "bob@example.com" {
		t.Fatalf("suggest attendees = %#v", bodies[1]["attendees"])
	}
	if bodies[1]["duration_minutes"] != float64(45) {
		t.Fatalf("suggest duration = %#v", bodies[1]["duration_minutes"])
	}
	if paths[2] != "/api/schedule/compress/apply" || len(bodies[2]["proposals"].([]any)) != 1 {
		t.Fatalf("compression request = %s %+v", paths[2], bodies[2])
	}
	if p := bodies[2]["proposals"].([]any)[0].(map[string]any); p["event_id"] != "evt-1" || p["proposed_start"] != "2026-05-04T10:00:00Z" {
		t.Fatalf("compression proposal = %+v", p)
	}
	if paths[3] != "/api/schedule/suggest" || paths[4] != "/api/schedule/create" {
		t.Fatalf("create flow paths = %v", paths[3:5])
	}
	if paths[5] != "/api/events/evt-1" || len(bodies[5]["attendees"].([]any)) != 2 {
		t.Fatalf("update request = %s %+v", paths[5], bodies[5])
	}
}

func TestTools_BackendError(t *testing.T) {
	errBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer errBackend.Close()

	s := buildServer(newClient(errBackend.URL))
	resp := callTool(t, s, "clockwise_list_events", map[string]any{
		"start": "2024-01-01T00:00:00Z",
		"end":   "2024-01-07T00:00:00Z",
	})
	if !strings.Contains(resp, "Error:") {
		t.Errorf("expected error text in response, got: %s", resp)
	}
}

// --- PAC-133 -------------------------------------------------------------------

// recordedCall is one request the PAC-133 fake backend received.
type recordedCall struct {
	method, path string
	query        url.Values
	body         map[string]any
}

// pac133Backend records every request and answers each with answer.
type pac133Backend struct {
	mu     sync.Mutex
	calls  []recordedCall
	answer func(c recordedCall) (int, string)
	srv    *httptest.Server
}

func newPAC133Backend(t *testing.T, answer func(c recordedCall) (int, string)) *pac133Backend {
	t.Helper()
	b := &pac133Backend{answer: answer}
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := recordedCall{method: r.Method, path: r.URL.Path, query: r.URL.Query()}
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			_ = json.Unmarshal(raw, &c.body)
		}
		b.mu.Lock()
		b.calls = append(b.calls, c)
		b.mu.Unlock()
		status, body := b.answer(c)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(b.srv.Close)
	return b
}

func (b *pac133Backend) to(path string) []recordedCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []recordedCall
	for _, c := range b.calls {
		if c.path == path {
			out = append(out, c)
		}
	}
	return out
}

func (b *pac133Backend) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.calls)
}

// resultText is the text of a tools/call answer.
func resultText(t *testing.T, raw string) string {
	t.Helper()
	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil || len(resp.Result.Content) == 0 {
		t.Fatalf("not a tool result: %s (%v)", raw, err)
	}
	return resp.Result.Content[0].Text
}

func toolDescription(t *testing.T, name string) string {
	t.Helper()
	s := buildServer(newClient("http://unused.invalid"))
	resp := s.HandleMessage(context.Background(), json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	b, _ := json.Marshal(resp)
	var list struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}
	_ = json.Unmarshal(b, &list)
	for _, tool := range list.Result.Tools {
		if tool.Name == name {
			return tool.Description
		}
	}
	t.Fatalf("tool %s not listed", name)
	return ""
}

// AC-19: the named attendees pass through, trimmed; none means no attendees
// parameter, and the tool never names the user's own calendar.
func TestPAC133_AC19_FindFreeSlotsPassesAttendees(t *testing.T) {
	b := newPAC133Backend(t, func(recordedCall) (int, string) { return 200, `{"own":{"status":"known","busy":[]},"calendars":[]}` })
	s := buildServer(newClient(b.srv.URL))
	callTool(t, s, "clockwise_find_free_slots", map[string]any{"start": "2030-01-07T00:00:00Z", "end": "2030-01-08T00:00:00Z", "attendees": " a@x.test, b@x.test ,"})
	callTool(t, s, "clockwise_find_free_slots", map[string]any{"start": "2030-01-07T00:00:00Z", "end": "2030-01-08T00:00:00Z"})
	callTool(t, s, "clockwise_find_free_slots", map[string]any{"start": "2030-01-07T00:00:00Z", "end": "2030-01-08T00:00:00Z", "attendees": " , "})
	calls := b.to("/api/calendar/freebusy")
	if len(calls) != 3 {
		t.Fatalf("calls = %+v", calls)
	}
	if got := calls[0].query["attendees"]; len(got) != 1 || got[0] != "a@x.test,b@x.test" {
		t.Errorf("attendees = %v, want exactly a@x.test,b@x.test", got)
	}
	for _, c := range calls[1:] {
		if _, ok := c.query["attendees"]; ok {
			t.Errorf("attendees sent without any named: %v", c.query)
		}
		if strings.Contains(c.query.Encode(), "primary") {
			t.Errorf("the tool named the user's own calendar: %v", c.query)
		}
	}
}

// AC-20: the unknown marker is relayed, an unknown calendar never shows an
// empty window list, and the description says unknown means not checked.
func TestPAC133_AC20_FindFreeSlotsRendersUnknown(t *testing.T) {
	answer := `{"own":{"status":"known","busy":[]},"calendars":[{"address":"a@x.test","status":"unknown"},{"address":"b@x.test","status":"known","busy":[{"start":"2030-01-07T10:00:00Z","end":"2030-01-07T11:00:00Z"}]}]}`
	b := newPAC133Backend(t, func(recordedCall) (int, string) { return 200, answer })
	text := resultText(t, callTool(t, buildServer(newClient(b.srv.URL)), "clockwise_find_free_slots", map[string]any{"start": "2030-01-07T00:00:00Z", "end": "2030-01-08T00:00:00Z", "attendees": "a@x.test,b@x.test"}))
	var got struct {
		Calendars []map[string]json.RawMessage `json:"calendars"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil || len(got.Calendars) != 2 {
		t.Fatalf("text = %s", text)
	}
	if string(got.Calendars[0]["status"]) != `"unknown"` {
		t.Errorf("A = %v, want the unknown marker", got.Calendars[0])
	}
	if _, ok := got.Calendars[0]["busy"]; ok {
		t.Errorf("A carries a window list: %s", text)
	}
	if !strings.Contains(string(got.Calendars[1]["busy"]), "2030-01-07T10:00:00Z") {
		t.Errorf("B's window missing: %s", text)
	}
	if desc := toolDescription(t, "clockwise_find_free_slots"); !strings.Contains(desc, "Unknown means not checked, never free") {
		t.Errorf("description = %q", desc)
	}
	if d := toolDescription(t, "clockwise_suggest_meeting"); !strings.Contains(d, "unknown_attendees") || !strings.Contains(d, "Unknown means not checked, never free.") {
		t.Errorf("suggest_meeting description = %q", d)
	}
	// The window rule reaches the model as the usual Error text.
	refusing := newPAC133Backend(t, func(recordedCall) (int, string) { return 400, "end must be after start" })
	text = resultText(t, callTool(t, buildServer(newClient(refusing.srv.URL)), "clockwise_find_free_slots", map[string]any{"start": "2030-01-07T10:00:00Z", "end": "2030-01-07T10:00:00Z"}))
	if !strings.HasPrefix(text, "Error: ") || !strings.Contains(text, "end must be after start") {
		t.Errorf("text = %q", text)
	}
}

// createEventBackend answers suggest calls: a re-check (a range exactly as
// long as the duration) with recheck, any other with fresh; create with an
// event.
func createEventBackend(t *testing.T, fresh string, recheck func() (int, string)) *pac133Backend {
	t.Helper()
	return newPAC133Backend(t, func(c recordedCall) (int, string) {
		switch c.path {
		case "/api/schedule/suggest":
			start, _ := time.Parse(time.RFC3339, c.body["range_start"].(string))
			end, _ := time.Parse(time.RFC3339, c.body["range_end"].(string))
			if end.Sub(start) == time.Duration(c.body["duration_minutes"].(float64))*time.Minute {
				return recheck()
			}
			return 200, fresh
		case "/api/schedule/create":
			return 200, `{"id":"evt-new"}`
		}
		return 404, `{"error":"not found"}`
	})
}

const (
	slotS       = "2030-01-07T10:00:00Z"
	slotE       = "2030-01-07T10:30:00Z"
	freshWithU  = `{"slots":[{"start":"` + slotS + `","end":"` + slotE + `","score":1,"reasons":[]}],"unknown_attendees":["u@x.test"]}`
	freshMoved  = `{"slots":[{"start":"2030-01-07T15:00:00Z","end":"2030-01-07T15:30:00Z","score":1,"reasons":[]}],"unknown_attendees":["u@x.test"]}`
	recheckOkU  = `{"slots":[{"start":"` + slotS + `","end":"` + slotE + `","score":1,"reasons":[]}],"unknown_attendees":["u@x.test"]}`
	recheckNone = `{"slots":[],"unknown_attendees":[]}`
	noLonger    = "Not booked: that time is no longer available — ask again (call clockwise_create_event without start)."
	notChecked  = "Not booked: that time could not be checked — ask again (call clockwise_create_event without start)."
)

func createArgs(extra map[string]any) map[string]any {
	args := map[string]any{"title": "Sync", "attendees": "b@x.test,u@x.test", "duration": "30", "preferred_date": "2030-01-07"}
	for k, v := range extra {
		args[k] = v
	}
	return args
}

func runCreate(t *testing.T, b *pac133Backend, args map[string]any) string {
	t.Helper()
	return resultText(t, callTool(t, buildServer(newClient(b.srv.URL)), "clockwise_create_event", args))
}

// AC-26: create_event takes two explicit steps past an unknown attendee and
// re-checks exactly the slot shown before booking it.
func TestPAC133_AC26_CreateEventTwoSteps(t *testing.T) {
	ok := func() (int, string) { return 200, recheckOkU }
	accept := map[string]any{"start": slotS, "accept_unknown": "true", "accepted_unknown": "u@x.test"}

	t.Run("a first call books nothing and says how to accept", func(t *testing.T) {
		b := createEventBackend(t, freshWithU, ok)
		text := runCreate(t, b, createArgs(nil))
		if len(b.to("/api/schedule/create")) != 0 {
			t.Fatal("a create request was made")
		}
		for _, want := range []string{"u@x.test", "start " + slotS, "end " + slotE, `start="` + slotS + `"`, `accept_unknown="true"`, `accepted_unknown="u@x.test"`} {
			if !strings.Contains(text, want) {
				t.Errorf("text lacks %q: %s", want, text)
			}
		}
	})

	t.Run("b acceptance books exactly the slot shown", func(t *testing.T) {
		b := createEventBackend(t, freshMoved, ok)
		runCreate(t, b, createArgs(map[string]any{"start": slotS, "accept_unknown": "TRUE ", "accepted_unknown": "U@x.test"}))
		creates := b.to("/api/schedule/create")
		if len(creates) != 1 || creates[0].body["start"] != slotS || creates[0].body["end"] != slotE {
			t.Fatalf("creates = %+v, want one at %s-%s", creates, slotS, slotE)
		}
		sugg := b.to("/api/schedule/suggest")
		if len(sugg) != 1 || sugg[0].body["range_start"] != slotS || sugg[0].body["range_end"] != slotE {
			t.Errorf("re-check = %+v, want one suggest over exactly [S, E]", sugg)
		}
	})

	t.Run("c a readable person is now busy", func(t *testing.T) {
		b := createEventBackend(t, freshWithU, func() (int, string) { return 200, recheckNone })
		if text := runCreate(t, b, createArgs(accept)); len(b.to("/api/schedule/create")) != 0 || text != noLonger {
			t.Errorf("text = %q", text)
		}
	})

	t.Run("c the requester's own calendar became unknown", func(t *testing.T) {
		b := createEventBackend(t, freshWithU, func() (int, string) { return 500, `{"error":"internal error"}` })
		if text := runCreate(t, b, createArgs(accept)); len(b.to("/api/schedule/create")) != 0 || text != notChecked {
			t.Errorf("text = %q", text)
		}
	})

	t.Run("d the unknown set grew", func(t *testing.T) {
		b := createEventBackend(t, freshWithU, func() (int, string) {
			return 200, `{"slots":[{"start":"` + slotS + `","end":"` + slotE + `"}],"unknown_attendees":["u@x.test","v@x.test"]}`
		})
		want := "Not booked: the availability of v@x.test could not be checked — ask again (call clockwise_create_event without start)."
		if text := runCreate(t, b, createArgs(accept)); len(b.to("/api/schedule/create")) != 0 || text != want {
			t.Errorf("text = %q", text)
		}
	})

	t.Run("e accept_unknown without start is a first call", func(t *testing.T) {
		b := createEventBackend(t, freshWithU, ok)
		text := runCreate(t, b, createArgs(map[string]any{"accept_unknown": "true", "accepted_unknown": "u@x.test"}))
		if len(b.to("/api/schedule/create")) != 0 || !strings.HasPrefix(text, "Not booked yet:") {
			t.Errorf("text = %q", text)
		}
	})

	t.Run("accepted names but accept_unknown is not the string true", func(t *testing.T) {
		for _, v := range []any{true, "yes", ""} {
			b := createEventBackend(t, freshWithU, ok)
			text := runCreate(t, b, createArgs(map[string]any{"start": slotS, "accept_unknown": v, "accepted_unknown": "u@x.test"}))
			if len(b.to("/api/schedule/create")) != 0 || !strings.HasPrefix(text, "Not booked yet:") || !strings.Contains(text, "start "+slotS) {
				t.Errorf("accept_unknown=%v: text = %q", v, text)
			}
		}
	})

	t.Run("f no unknown attendee books the first slot as today", func(t *testing.T) {
		b := createEventBackend(t, `{"slots":[{"start":"`+slotS+`","end":"`+slotE+`"}],"unknown_attendees":[]}`, ok)
		runCreate(t, b, createArgs(nil))
		if creates := b.to("/api/schedule/create"); len(creates) != 1 || creates[0].body["start"] != slotS {
			t.Errorf("creates = %+v", creates)
		}
	})

	t.Run("g a start over lunch is refused", func(t *testing.T) {
		b := createEventBackend(t, freshWithU, func() (int, string) { return 200, recheckNone })
		text := runCreate(t, b, map[string]any{"title": "x", "attendees": "b@x.test", "start": "2030-01-07T12:00:00Z", "duration": "60"})
		if len(b.to("/api/schedule/create")) != 0 || text != noLonger {
			t.Errorf("text = %q", text)
		}
		if s := b.to("/api/schedule/suggest"); len(s) != 1 || s[0].body["range_end"] != "2030-01-07T13:00:00Z" {
			t.Errorf("re-check = %+v, want [12:00, 13:00]", s)
		}
	})

	t.Run("a first slot other than the start is refused", func(t *testing.T) {
		b := createEventBackend(t, freshWithU, func() (int, string) {
			return 200, `{"slots":[{"start":"2030-01-07T10:30:00Z","end":"2030-01-07T11:00:00Z"}],"unknown_attendees":[]}`
		})
		if text := runCreate(t, b, createArgs(map[string]any{"start": slotS})); len(b.to("/api/schedule/create")) != 0 || text != noLonger {
			t.Errorf("text = %q", text)
		}
	})

	t.Run("an unparseable start", func(t *testing.T) {
		b := createEventBackend(t, freshWithU, ok)
		if text := runCreate(t, b, createArgs(map[string]any{"start": "tomorrow"})); b.count() != 0 || text != "Error: start must be RFC3339" {
			t.Errorf("text = %q after %d calls", text, b.count())
		}
	})

	if d := toolDescription(t, "clockwise_create_event"); !strings.Contains(d, `accept_unknown="true"`) || !strings.Contains(d, "nothing is booked") {
		t.Errorf("description = %q", d)
	}
}

// PAC-133 §10 (m-6): clockwise_parse_command relays NLP parse's answer as
// text, so the model sees the suggestion-failure sentence in error.
func TestPAC133_ParseCommandRelaysTheSuggestionFailure(t *testing.T) {
	const sentence = "Paceday could not suggest meeting times for this request. Try again later."
	b := newPAC133Backend(t, func(recordedCall) (int, string) {
		return 200, `{"intent":"schedule_meeting","title":"Sync","error":"` + sentence + `"}`
	})
	text := resultText(t, callTool(t, buildServer(newClient(b.srv.URL)), "clockwise_parse_command", map[string]any{"command": "meet bob tomorrow"}))
	if !strings.Contains(text, sentence) {
		t.Errorf("text = %q, want the error sentence relayed", text)
	}
}
