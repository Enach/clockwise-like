package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// PAC-73 Part 2, P2-AC-33 (spec docs/specs/NEW-llm-bring-your-own-part2.md
// §3.7, P2-D9): clockwise_parse_command relays the row 5, 10 and 11
// not-configured answers unchanged, and no MCP tool reads or changes the
// organisation's AI configuration or gives a confirmation (the confirmation
// is the person's, owner O-13).
func TestParseCommand_RelaysPart2Rows(t *testing.T) {
	for _, tc := range []struct{ reason, enablers, message string }{
		{"organization_unconfirmed", `["user"]`, "AI features are off until you confirm: your organisation provides AI through OpenAI. Your text and meeting details (titles, attendee emails, Slack and Notion excerpts) are sent to your organisation's AI provider account, which its administrators can access outside Paceday. Confirm in Paceday to turn AI on."},
		{"personal_keys_forbidden", `["user"]`, "AI features are off: your organisation does not allow personal API keys, so your own choice (OpenAI) and any key you saved for it are kept but not used. Choose \"Use my organisation's AI\" in Settings to use your organisation's AI."},
		{"personal_keys_forbidden", `["organization_admin"]`, "AI features are off: your organisation does not allow personal API keys and has not finished setting up its AI. An administrator of your organisation can set it up."},
	} {
		msg, _ := json.Marshal(tc.message)
		body := `{"intent":"unknown","error":` + string(msg) + `,"not_configured":{"reason":"` + tc.reason + `","enablers":` + tc.enablers + `,"message":` + string(msg) + `}}`
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		}))
		out := callTool(t, buildServer(newClient(backend.URL)), "clockwise_parse_command", map[string]any{"command": "plan my week"})
		backend.Close()
		if strings.Contains(out, `"isError":true`) {
			t.Fatalf("%s became a tool error: %s", tc.reason, out)
		}
		var resp struct {
			Result struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(out), &resp); err != nil || len(resp.Result.Content) == 0 {
			t.Fatalf("%s: unreadable tool result %s", tc.reason, out)
		}
		var relayed, sent map[string]any
		if err := json.Unmarshal([]byte(resp.Result.Content[0].Text), &relayed); err != nil {
			t.Fatalf("%s: the tool text is not the backend body: %s", tc.reason, resp.Result.Content[0].Text)
		}
		_ = json.Unmarshal([]byte(body), &sent)
		a, _ := json.Marshal(relayed)
		b, _ := json.Marshal(sent)
		if string(a) != string(b) {
			t.Errorf("%s: relayed %s, want %s unchanged", tc.reason, a, b)
		}
	}
}

func TestNoToolReachesTheOrganisationAI(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()
	s := buildServer(newClient(backend.URL))
	list, _ := json.Marshal(s.HandleMessage(context.Background(), json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)))
	var tools struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(list, &tools); err != nil || len(tools.Result.Tools) == 0 {
		t.Fatalf("tools/list = %s", list)
	}
	args := map[string]any{"command": "x", "id": "evt-1", "date": "2024-01-02", "start": "2024-01-01T00:00:00Z",
		"end": "2024-01-02T00:00:00Z", "title": "T", "attendees": "a@example.com", "plan": `{"moves":[]}`, "q": "x",
		"duration": "30", "preferred_date": "2024-01-02", "description": "d", "location": "l"}
	for _, tool := range tools.Result.Tools {
		if strings.Contains(strings.ToLower(tool.Name), "organization") || strings.Contains(strings.ToLower(tool.Name), "consent") {
			t.Errorf("tool %s looks like an organisation-AI tool", tool.Name)
		}
		callTool(t, s, tool.Name, args)
		callTool(t, s, tool.Name, map[string]any{})
	}
	for _, p := range paths {
		if strings.Contains(p, "/api/organization") {
			t.Errorf("a tool called %s", p)
		}
	}
	src, err := os.ReadFile("tools.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "/api/organization") {
		t.Error("tools.go names an organisation-AI path")
	}
}
