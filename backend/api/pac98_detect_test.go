package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"golang.org/x/oauth2"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/domain"
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

// PAC-98 review: the domains detection answers google without a lookup
// (backend/auth/sso_detect.go) must be exactly the domains on which a verified
// Google address is authoritative evidence (googleConsumerDomains here,
// handlers_pac71_auth.go, PAC-71; outside PAC-98's allowed paths, so the two
// sets are pinned equal rather than merged). Detection is the oracle for the
// auth set: over every consumer-list entry and every entry of the evidence
// set, google from detection <=> authoritative here.
func TestPAC98_GoogleConsumerSetsAgree(t *testing.T) {
	candidates := map[string]bool{}
	for _, d := range domain.GenericDomains() {
		candidates[d] = true
	}
	for d := range googleConsumerDomains {
		candidates[d] = true
		if !domain.IsGenericDomain(d) {
			t.Errorf("%q is authoritative Google evidence but not on the consumer list", d)
		}
	}
	det := auth.NewDetector(auth.DetectorOptions{Resolver: forbiddenMXResolver{}})
	db := openTestDB(t)
	for d := range candidates {
		res, err := det.Detect(t.Context(), db, "user@"+d)
		if err != nil {
			t.Fatalf("Detect(%s): %v", d, err)
		}
		if got, want := res.Type == auth.DetectGoogle, googleConsumerDomains[d]; got != want {
			t.Errorf("%s: detection google = %v, PAC-71 authoritative Google consumer domain = %v; the two sets must be equal", d, got, want)
		}
	}
}
