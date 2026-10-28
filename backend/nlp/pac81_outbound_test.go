package nlp

import (
	"context"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/outbound"
	"github.com/Enach/paceday/backend/internal/outbound/outboundtest"
)

// AC-A3: an LLM call to a provider that never answers fails on its own
// limit, independently of the request context.
func TestPAC81_LLMCallFailsWithinLimit(t *testing.T) {
	l := outbound.Default
	l.LLM = 200 * time.Millisecond
	defer outbound.SetForTest(l)()
	h := outboundtest.NewHang(t)
	h.Within(t, l.LLM, func() error {
		_, err := postJSON(context.Background(), h.URL()+"/v1/chat/completions", nil, map[string]string{"a": "b"},
			func([]byte) (string, error) { return "", nil })
		return err
	})
	if c := cloudHTTPClient("openai"); c.Timeout != l.LLM {
		t.Fatalf("cloud LLM client timeout %s", c.Timeout)
	}
	if c := userHTTPClient("openai"); c.Timeout != l.LLM {
		t.Fatalf("user LLM client timeout %s", c.Timeout)
	}
}
