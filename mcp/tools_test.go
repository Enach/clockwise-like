package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
