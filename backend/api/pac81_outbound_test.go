package api

import (
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/outbound"
	"github.com/Enach/paceday/backend/internal/outbound/outboundtest"
)

// AC-A3: the Slack and Notion token exchanges fail within their limit
// (scaled) on a token endpoint that never answers.
func TestPAC81_SlackNotionExchangeFailWithinLimit(t *testing.T) {
	l := outbound.Default
	l.TokenExchange = 200 * time.Millisecond
	defer outbound.SetForTest(l)()
	h := outboundtest.NewHang(t)
	prevS, prevN := slackTokenURL, notionTokenURL
	slackTokenURL, notionTokenURL = h.URL()+"/slack", h.URL()+"/notion"
	defer func() { slackTokenURL, notionTokenURL = prevS, prevN }()
	h.Within(t, l.TokenExchange, func() error {
		_, err := slackExchangeCode("c", "id", "secret", "http://localhost/cb")
		return err
	})
	h.Within(t, l.TokenExchange, func() error {
		_, err := notionExchangeCode("c", "id", "secret", "http://localhost/cb")
		return err
	})
}
