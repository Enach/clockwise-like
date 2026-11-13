package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"golang.org/x/oauth2"

	"github.com/Enach/paceday/backend/api/gen"
)

// PAC-98 AC-6 (and API-092 for the changed outcomes): through the real
// router, a gmail.com address is google and other consumer addresses are the
// personal-address answer (generic). Both decode strictly into the generated
// contract type and carry neither a provider name nor a redirect path. No
// resolver is installed: the package default fails the run on any lookup.
func TestPAC98_AC6_ConsumerAnswersThroughTheRouter(t *testing.T) {
	r := pac97Router(t)
	for _, tc := range []struct {
		email string
		want  gen.SsoDetectResponseType
		body  string
	}{
		{"alice@gmail.com", gen.SsoDetectResponseTypeGoogle, `{"type":"google"}`},
		{"bob@yahoo.com", gen.SsoDetectResponseTypeGeneric, `{"type":"generic"}`},
		{"carol@outlook.com", gen.SsoDetectResponseTypeGeneric, `{"type":"generic"}`},
		{"not-an-email", gen.SsoDetectResponseTypeChoose, `{"type":"choose"}`},
	} {
		resetDetectLimiter(t, nil)
		rec := pac97Detect(r, "192.0.2.98:1000", tc.email)
		res := decodeGenerated(t, rec)
		if res.Type != tc.want {
			t.Errorf("%s: type = %q, want %q", tc.email, res.Type, tc.want)
		}
		if res.ProviderName != nil || res.RedirectUrl != nil {
			t.Errorf("%s: carries provider_name %v / redirect_url %v", tc.email, res.ProviderName, res.RedirectUrl)
		}
		if body := strings.TrimSpace(rec.Body.String()); body != tc.body {
			t.Errorf("%s: body = %s, want %s", tc.email, body, tc.body)
		}
	}
}

// AC-6: 400, 429 (the 21st request in a minute) and 500 are unchanged
// through the real router.
func TestPAC98_AC6_ErrorStatusesUnchanged(t *testing.T) {
	r := pac97Router(t)

	resetDetectLimiter(t, nil)
	rec := httptestDetectBody(r, "192.0.2.98:2000", `{}`)
	assertJSONError(t, rec, http.StatusBadRequest, "email required")

	resetDetectLimiter(t, nil)
	for i := 0; i < detectRateLimit; i++ {
		if rec := pac97Detect(r, "192.0.2.98:3000", "bob@yahoo.com"); rec.Code != http.StatusOK {
			t.Fatalf("request %d: status %d", i+1, rec.Code)
		}
	}
	assertJSONError(t, pac97Detect(r, "192.0.2.98:3001", "bob@yahoo.com"), http.StatusTooManyRequests, "rate limit exceeded")

	// A failed SSO-row read for a company domain is still a 500.
	resetDetectLimiter(t, nil)
	broken := chi.NewRouter()
	RegisterRoutes(broken, closedDB(t), &oauth2.Config{}, "pac98-jwt-secret", "http://localhost", "http://localhost")
	assertJSONError(t, pac97Detect(broken, "192.0.2.98:4000", "alice@acme-pac98.test"), http.StatusInternalServerError, "internal error")
}

func httptestDetectBody(r http.Handler, remote, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/detect", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remote
	r.ServeHTTP(rec, req)
	return rec
}
