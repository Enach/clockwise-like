package conference

import (
	"context"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/outbound"
	"github.com/Enach/paceday/backend/internal/outbound/outboundtest"
)

// AC-A3: the Zoom token exchange and revocation fail on the identity limit
// against an endpoint that never answers.
func TestPAC81_ZoomTokenCallsFailWithinLimit(t *testing.T) {
	l := outbound.Default
	l.Identity = 200 * time.Millisecond
	defer outbound.SetForTest(l)()
	h := outboundtest.NewHang(t)
	prevT, prevR := ZoomTokenURL, ZoomRevokeURL
	ZoomTokenURL, ZoomRevokeURL = h.URL()+"/token", h.URL()+"/revoke"
	defer func() { ZoomTokenURL, ZoomRevokeURL = prevT, prevR }()
	h.Within(t, l.Identity, func() error {
		_, err := ZoomExchangeCode(context.Background(), "id", "secret", "http://localhost/cb", "code")
		return err
	})
	h.Within(t, l.Identity, func() error {
		return RevokeZoomToken(context.Background(), "id", "secret", "at")
	})
}
