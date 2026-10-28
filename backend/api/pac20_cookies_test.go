package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// PAC-20 / API-007 / API-027: every Set-Cookie the backend emits carries
// Secure unless the operator opted out (PACEDAY_INSECURE_COOKIES=true), and
// the Slack/Notion OAuth state cookies carry SameSite=Lax.

func withSecureCookies(t *testing.T, secure bool) {
	t.Helper()
	prev := secureCookies
	SetSecureCookies(secure)
	t.Cleanup(func() { SetSecureCookies(prev) })
}

// emitters drives every cookie-emitting path that can run without a database
// or a live provider. Each returns the recorded response.
func cookieEmitters(t *testing.T) map[string]func() *httptest.ResponseRecorder {
	t.Helper()
	t.Setenv("SLACK_CLIENT_ID", "slack-client")
	t.Setenv("NOTION_CLIENT_ID", "notion-client")
	t.Setenv("ZOOM_CLIENT_ID", "zoom-client")
	t.Setenv("MICROSOFT_CLIENT_ID", "ms-client")

	// A token endpoint that always refuses, so the Google callback runs its
	// state-cookie deletion and then fails without leaving the test host.
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
	}))
	t.Cleanup(tokenSrv.Close)
	oc := &oauth2.Config{
		ClientID: "g", ClientSecret: "s", RedirectURL: "http://localhost/cb",
		Endpoint: oauth2.Endpoint{AuthURL: "http://localhost/auth", TokenURL: tokenSrv.URL},
	}
	ah := &authHandlers{oauthConfig: oc, jwtSecret: "pac20-test-secret"}

	get := func(target string, h http.HandlerFunc, cookies ...*http.Cookie) func() *httptest.ResponseRecorder {
		return func() *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodGet, target, nil)
			for _, c := range cookies {
				req.AddCookie(c)
			}
			rec := httptest.NewRecorder()
			h(rec, req)
			return rec
		}
	}
	return map[string]func() *httptest.ResponseRecorder{
		"slack connect":          get("/api/integrations/slack/connect", (&integrationsHandlers{}).slackConnect),
		"notion connect":         get("/api/integrations/notion/connect", (&integrationsHandlers{}).notionConnect),
		"google start":           get("/api/auth/google", ah.startOAuth),
		"google callback delete": get("/api/auth/callback?state=s1&code=c", ah.callback, &http.Cookie{Name: "oauth_state", Value: "s1"}),
		"microsoft start":        get("/api/auth/microsoft", ah.startMicrosoftOAuth),
		"zoom start":             get("/api/auth/zoom", (&conferencingHandlers{}).startZoomOAuth),
		"logout":                 get("/api/auth/logout", (&meHandlers{}).logout),
		"issue session": func() *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			_ = ah.issueJWT(rec, &storage.User{ID: uuid.New(), Email: "pac20@example.test"}, 0)
			return rec
		},
	}
}

func TestPAC20_EveryCookieIsSecureByDefault(t *testing.T) {
	withSecureCookies(t, true)
	for name, run := range cookieEmitters(t) {
		t.Run(name, func(t *testing.T) {
			rec := run()
			headers := rec.Result().Header.Values("Set-Cookie")
			if len(headers) == 0 {
				t.Fatalf("no Set-Cookie emitted (status %d, body %q)", rec.Code, rec.Body.String())
			}
			for _, h := range headers {
				if !hasAttr(h, "Secure") {
					t.Errorf("Set-Cookie without Secure: %q", h)
				}
			}
		})
	}
}

func TestPAC20_InsecureFlagDropsSecureForLocalHTTP(t *testing.T) {
	withSecureCookies(t, false)
	for name, run := range cookieEmitters(t) {
		t.Run(name, func(t *testing.T) {
			for _, h := range run().Result().Header.Values("Set-Cookie") {
				if hasAttr(h, "Secure") {
					t.Errorf("Set-Cookie carries Secure with the insecure flag on: %q", h)
				}
			}
		})
	}
}

func TestPAC20_SlackAndNotionStateCookiesAreSameSiteLax(t *testing.T) {
	withSecureCookies(t, true)
	emitters := cookieEmitters(t)
	for name, cookie := range map[string]string{"slack connect": "slack_oauth_state", "notion connect": "notion_oauth_state"} {
		t.Run(cookie, func(t *testing.T) {
			var found bool
			for _, h := range emitters[name]().Result().Header.Values("Set-Cookie") {
				if !strings.HasPrefix(h, cookie+"=") {
					continue
				}
				found = true
				for _, attr := range []string{"SameSite=Lax", "HttpOnly", "Secure"} {
					if !hasAttr(h, attr) {
						t.Errorf("%s lacks %s: %q", cookie, attr, h)
					}
				}
			}
			if !found {
				t.Fatalf("%s was not set", cookie)
			}
		})
	}
}

// Every Set-Cookie in the package must go through setCookie, so a cookie added
// later cannot forget Secure. Generated code is excluded: it only forwards a
// header value a handler built.
func TestPAC20_NoCookieBypassesSetCookieHelper(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	bypass := regexp.MustCompile(`http\.SetCookie\(|"Set-Cookie"`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "cookies.go" {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if bypass.MatchString(line) {
				t.Errorf("%s:%d sets a cookie without setCookie: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}

func hasAttr(setCookie, attr string) bool {
	for _, part := range strings.Split(setCookie, ";") {
		if strings.EqualFold(strings.TrimSpace(part), attr) {
			return true
		}
	}
	return false
}
