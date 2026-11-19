package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// PAC-148 AC-4, G-4's MCP leg (docs/specs/PAC-148.md §2.2 C-3b): MCP's
// clockwise_parse_command reaches the organisation's AI only through the
// backend's POST /api/nlp/parse, which decides membership under PAC-148's
// rule (backend TestPAC148_AC4_EveryConsumerAgrees drives that endpoint for
// the planted accounts). This pins the MCP half: the tool makes exactly that
// one request, with the command as `text`, and relays the non-member's row-6
// answer unchanged as its result (not a tool error). The backend is a stub;
// nothing leaves the test.
func TestPAC148_AC4_MCPParseGoesThroughTheBackendParse(t *testing.T) {
	const row6 = `{"intent":"unknown","error":"AI features are off: no AI provider is set up. Add your own API key in Settings.",` +
		`"not_configured":{"reason":"nothing_configured","enablers":["user"],"message":"AI features are off: no AI provider is set up. Add your own API key in Settings."}}`
	var mu sync.Mutex
	var seen []string
	var bodies []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		bodies = append(bodies, string(b))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(row6))
	}))
	defer backend.Close()

	out := callTool(t, buildServer(newClient(backend.URL)), "clockwise_parse_command", map[string]any{"command": "meet bob tomorrow"})

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || seen[0] != "POST /api/nlp/parse" {
		t.Fatalf("backend requests = %v, want exactly POST /api/nlp/parse", seen)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &sent); err != nil || sent["text"] != "meet bob tomorrow" {
		t.Fatalf("parse body = %s, want the command as text", bodies[0])
	}
	if strings.Contains(out, `"isError":true`) {
		t.Fatalf("the non-member's answer became a tool error: %s", out)
	}
	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil || len(resp.Result.Content) == 0 {
		t.Fatalf("unreadable tool result %s", out)
	}
	var relayed, want map[string]any
	_ = json.Unmarshal([]byte(resp.Result.Content[0].Text), &relayed)
	_ = json.Unmarshal([]byte(row6), &want)
	a, _ := json.Marshal(relayed)
	b, _ := json.Marshal(want)
	if string(a) != string(b) {
		t.Errorf("relayed %s, want %s unchanged", a, b)
	}
}
