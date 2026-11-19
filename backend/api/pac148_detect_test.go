package api

import (
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"golang.org/x/oauth2"

	"github.com/Enach/paceday/backend/api/gen"
)

// PAC-148 AC-11 through the real router: POST /api/auth/detect answers
// generic for each of the 17 Microsoft personal-account domains with no SSO
// read (the database is closed, so a read would be the 500) and no MX query
// (no resolver is installed: the package default fails the run on any
// lookup). Before PAC-148 they took the MX path.
func TestPAC148_AC11_DetectAnswersGenericThroughTheRouter(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutes(r, closedDB(t), &oauth2.Config{}, "pac148-jwt-secret", "http://localhost", "http://localhost")
	for _, d := range []string{
		"hotmail.fr", "hotmail.co.uk", "hotmail.de", "hotmail.it", "hotmail.es", "hotmail.be",
		"live.fr", "live.co.uk", "live.de", "live.it", "live.nl", "live.be",
		"outlook.fr", "outlook.de", "outlook.it", "outlook.es",
		"windowslive.com",
	} {
		resetDetectLimiter(t, nil)
		rec := pac97Detect(r, "192.0.2.148:1000", "erin@"+d)
		res := decodeGenerated(t, rec)
		if res.Type != gen.SsoDetectResponseTypeGeneric || strings.TrimSpace(rec.Body.String()) != `{"type":"generic"}` {
			t.Errorf("%s: %s, want {\"type\":\"generic\"}", d, rec.Body.String())
		}
	}
}
