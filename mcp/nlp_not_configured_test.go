package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// NEW-llm-bring-your-own AC-13: when AI is not configured, /api/nlp/parse
// answers 200 with intent "unknown", error, and not_configured. The
// clockwise_parse_command tool relays that body as its result (not a tool
// error), so an MCP client shows the sentence and can read the reason.
func TestParseCommand_RelaysNotConfigured(t *testing.T) {
	body := `{"intent":"unknown","error":"AI features are off: no AI provider is set up. Add your own API key in Settings.",` +
		`"not_configured":{"reason":"nothing_configured","enablers":["user"],"message":"AI features are off: no AI provider is set up. Add your own API key in Settings."}}`
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer backend.Close()

	out := callTool(t, buildServer(newClient(backend.URL)), "clockwise_parse_command", map[string]any{"command": "plan my week"})
	if strings.Contains(out, `"isError":true`) {
		t.Fatalf("the not-configured answer became a tool error: %s", out)
	}
	for _, want := range []string{"not_configured", "nothing_configured", "AI features are off"} {
		if !strings.Contains(out, want) {
			t.Errorf("tool result does not carry %q: %s", want, out)
		}
	}
}
