package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// PAC-122 batch 7: the MCP light rows of docs/factory/silent-failures.md.
// Every backend here is an httptest fake: SF-062 (MCP sends no backend
// credential) is not fixed, so none of this runs against the real backend.

// toolResult is a decoded tools/call answer.
type toolResult struct {
	text    string
	isError bool
}

func decodeToolResult(t *testing.T, raw string) toolResult {
	t.Helper()
	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil || len(resp.Result.Content) == 0 {
		t.Fatalf("unreadable tool result: %s", raw)
	}
	return toolResult{text: resp.Result.Content[0].Text, isError: resp.Result.IsError}
}

// toolDescriptions maps each tool name to its description (tools/list).
func toolDescriptions(t *testing.T) map[string]string {
	t.Helper()
	s := buildServer(newClient("http://127.0.0.1:1"))
	raw, _ := json.Marshal(s.HandleMessage(context.Background(), json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)))
	var list struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				InputSchema struct {
					Properties map[string]struct {
						Description string `json:"description"`
					} `json:"properties"`
				} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("tools/list = %s", raw)
	}
	out := map[string]string{}
	for _, tool := range list.Result.Tools {
		out[tool.Name] = tool.Description
		for prop, p := range tool.InputSchema.Properties {
			out[tool.Name+"."+prop] = p.Description
		}
	}
	return out
}

// SF-026: create_event refuses a missing title or attendee list itself,
// before any backend call, instead of creating the event.
func TestPAC122_SF026_CreateEventRequiresTitleAndAttendees(t *testing.T) {
	b := newPAC133Backend(t, func(recordedCall) (int, string) {
		return 200, `{"slots":[{"start":"2026-05-04T10:00:00Z","end":"2026-05-04T10:30:00Z"}]}`
	})
	s := buildServer(newClient(b.srv.URL))
	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"no title", map[string]any{"attendees": "a@x.test"}, "title"},
		{"blank title", map[string]any{"title": "   ", "attendees": "a@x.test"}, "title"},
		{"no attendees", map[string]any{"title": "Sync"}, "attendees"},
		{"only separators", map[string]any{"title": "Sync", "attendees": " , ,"}, "attendees"},
		{"with start", map[string]any{"title": "", "attendees": "a@x.test", "start": "2026-05-04T10:00:00Z"}, "title"},
	} {
		got := decodeToolResult(t, callTool(t, s, "clockwise_create_event", tc.args))
		if !got.isError || !strings.Contains(got.text, tc.want) {
			t.Errorf("%s: result %+v, want a tool error naming %s", tc.name, got, tc.want)
		}
	}
	if n := b.count(); n != 0 {
		t.Errorf("backend called %d times, want 0", n)
	}
}

// SF-061: a failed backend call is a tool error (isError), not a success
// whose text starts with "Error:".
func TestPAC122_SF061_BackendFailureIsToolError(t *testing.T) {
	b := newPAC133Backend(t, func(recordedCall) (int, string) {
		return 500, `{"error":"boom"}`
	})
	s := buildServer(newClient(b.srv.URL))
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"clockwise_list_events", map[string]any{"start": "2026-05-04T00:00:00Z", "end": "2026-05-05T00:00:00Z"}},
		{"clockwise_delete_event", map[string]any{"id": "evt-1"}},
		{"clockwise_clear_focus_blocks", map[string]any{}},
		{"clockwise_get_settings", map[string]any{}},
	} {
		got := decodeToolResult(t, callTool(t, s, tc.tool, tc.args))
		if !got.isError {
			t.Errorf("%s: backend 500 relayed as success: %+v", tc.tool, got)
		}
		if !strings.Contains(got.text, "500") {
			t.Errorf("%s: text %q does not carry the status", tc.tool, got.text)
		}
	}
}

// SF-063: apply_compression reads the plan exactly as compress_schedule
// returns it (camelCase, an array of results) and sends the backend the
// snake_case proposals it decodes; a proposal it cannot read is refused.
func TestPAC122_SF063_ApplyCompressionReadsCompressOutput(t *testing.T) {
	b := newPAC133Backend(t, func(recordedCall) (int, string) {
		return 200, `{"applied":["evt-1"],"failed":[]}`
	})
	s := buildServer(newClient(b.srv.URL))
	// The shape of engine.CompressionResult as POST /api/schedule/compress
	// encodes it.
	plan := `[{"date":"2026-05-04","proposals":[{"eventId":"evt-1","eventTitle":"Sync",` +
		`"currentStart":"2026-05-04T14:00:00Z","currentEnd":"2026-05-04T14:30:00Z",` +
		`"proposedStart":"2026-05-04T10:00:00Z","proposedEnd":"2026-05-04T10:30:00Z",` +
		`"reason":"cluster","focusGainMinutes":30}],"totalFocusGainMinutes":30}]`
	got := decodeToolResult(t, callTool(t, s, "clockwise_apply_compression", map[string]any{"plan": plan}))
	if got.isError {
		t.Fatalf("apply failed: %+v", got)
	}
	calls := b.to("/api/schedule/compress/apply")
	if len(calls) != 1 {
		t.Fatalf("apply calls = %d, want 1", len(calls))
	}
	props, _ := calls[0].body["proposals"].([]any)
	if len(props) != 1 {
		t.Fatalf("proposals sent = %v", calls[0].body)
	}
	p, _ := props[0].(map[string]any)
	if p["event_id"] != "evt-1" || p["proposed_start"] != "2026-05-04T10:00:00Z" || p["proposed_end"] != "2026-05-04T10:30:00Z" {
		t.Errorf("proposal sent = %v, want evt-1 10:00-10:30 in snake_case", p)
	}

	// A proposal with no readable event or time is refused, not sent empty.
	bad := decodeToolResult(t, callTool(t, s, "clockwise_apply_compression", map[string]any{
		"plan": `{"proposals":[{"eventID":"evt-2","start":"2026-05-04T10:00:00Z"}]}`,
	}))
	if !bad.isError {
		t.Errorf("an unreadable proposal was accepted: %+v", bad)
	}
	if n := len(b.to("/api/schedule/compress/apply")); n != 1 {
		t.Errorf("apply calls = %d after the unreadable plan, want still 1", n)
	}
}

// SF-064: clear_focus_blocks says it clears the current Monday-Sunday week
// and reports the backend's deleted count.
func TestPAC122_SF064_ClearFocusBlocksSaysWeekAndCount(t *testing.T) {
	b := newPAC133Backend(t, func(recordedCall) (int, string) {
		return 200, `{"deleted":3}`
	})
	got := decodeToolResult(t, callTool(t, buildServer(newClient(b.srv.URL)), "clockwise_clear_focus_blocks", map[string]any{}))
	if got.isError || !strings.Contains(got.text, "3") || !strings.Contains(got.text, "Monday") {
		t.Errorf("result %+v, want the count 3 and the Monday-Sunday week", got)
	}
	desc := toolDescriptions(t)["clockwise_clear_focus_blocks"]
	if strings.Contains(strings.ToLower(desc), "all ") || !strings.Contains(desc, "Monday") {
		t.Errorf("description %q, want the current Monday-Sunday week, not all", desc)
	}
}

// SF-065: list_focus_blocks says it lists the next 7 days, not all.
func TestPAC122_SF065_ListFocusBlocksSaysSevenDays(t *testing.T) {
	desc := toolDescriptions(t)["clockwise_list_focus_blocks"]
	if strings.Contains(strings.ToLower(desc), "all ") || !strings.Contains(desc, "7 days") {
		t.Errorf("description %q, want the 7 days from now, not all", desc)
	}
}

// SF-094: update_event refuses an update that changes nothing, and an
// explicit "" clears description and location.
func TestPAC122_SF094_UpdateEventEmptyAndClear(t *testing.T) {
	b := newPAC133Backend(t, func(recordedCall) (int, string) {
		return 200, `{"id":"evt-1"}`
	})
	s := buildServer(newClient(b.srv.URL))
	for _, args := range []map[string]any{
		{"id": "evt-1"},
		{"id": "evt-1", "title": "", "start": "", "end": "", "attendees": ""},
	} {
		got := decodeToolResult(t, callTool(t, s, "clockwise_update_event", args))
		if !got.isError {
			t.Errorf("args %v: empty update reported as %+v, want a tool error", args, got)
		}
	}
	if n := b.count(); n != 0 {
		t.Fatalf("an empty update reached the backend %d times", n)
	}

	got := decodeToolResult(t, callTool(t, s, "clockwise_update_event", map[string]any{"id": "evt-1", "description": "", "location": ""}))
	if got.isError {
		t.Fatalf("clearing failed: %+v", got)
	}
	calls := b.to("/api/events/evt-1")
	if len(calls) != 1 {
		t.Fatalf("patch calls = %d, want 1", len(calls))
	}
	for _, f := range []string{"description", "location"} {
		if v, ok := calls[0].body[f]; !ok || v != "" {
			t.Errorf("%s not sent as \"\" to clear it: %v", f, calls[0].body)
		}
	}
	if _, ok := calls[0].body["title"]; ok {
		t.Errorf("title sent though not given: %v", calls[0].body)
	}
}

// truncatingBackend answers 200 announcing more body than it sends, so the
// client's read fails with an unexpected EOF.
func truncatingBackend(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"deleted":`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// SF-095: a backend body that cannot be read in full is an error, never a
// success carrying half a body.
func TestPAC122_SF095_TruncatedBodyIsError(t *testing.T) {
	c := newClient(truncatingBackend(t).URL)
	ctx := context.Background()
	if _, err := c.get(ctx, "/x", nil); err == nil {
		t.Error("get: truncated body accepted")
	}
	if _, err := c.post(ctx, "/x", map[string]any{}); err == nil {
		t.Error("post: truncated body accepted")
	}
	if _, err := c.delete(ctx, "/x"); err == nil {
		t.Error("delete: truncated body accepted")
	}
	got := decodeToolResult(t, callTool(t, buildServer(c), "clockwise_get_settings", map[string]any{}))
	if !got.isError {
		t.Errorf("tool relayed a truncated body as success: %+v", got)
	}
}
