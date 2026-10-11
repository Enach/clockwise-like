package calendar_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/config"
)

// TestNewClient_ProductionSettingsSendBearerAndRefuseRedirect is AC-27.
//
// It lives in the external test package on purpose: it can reach the client
// only through calendar.NewClient, the entry point all ten production call
// sites use, so it cannot be satisfied by a lower-level builder while
// production still uses the library's default stack. The only setting it
// substitutes is the address; everything else is production's decision.
func TestNewClient_ProductionSettingsSendBearerAndRefuseRedirect(t *testing.T) {
	prod, err := config.DecideCalendarEndpoint("", "")
	if err != nil {
		t.Fatalf("production decision: %v", err)
	}
	if prod.Guard {
		t.Fatal("production decision has the guard on")
	}
	t.Cleanup(func() { calendar.Configure(prod) })

	run := func(t *testing.T) {
		var bHits atomic.Int32
		b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			bHits.Add(1)
			_, _ = w.Write([]byte(`{"items":[]}`))
		}))
		defer b.Close()

		var redirect atomic.Bool
		var gotAuth atomic.Value
		a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth.Store(r.Header.Get("Authorization"))
			if redirect.Load() {
				http.Redirect(w, r, b.URL+r.URL.Path, http.StatusFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"items":[]}`))
		}))
		defer a.Close()

		d := prod
		d.BaseURL = a.URL + "/calendar/v3/"
		calendar.Configure(d)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, err := calendar.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "T"}))
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		now := time.Now()

		if _, err := c.ListEvents(ctx, "primary", now, now.Add(time.Hour)); err != nil {
			t.Fatalf("ListEvents against listener A: %v", err)
		}
		if v, _ := gotAuth.Load().(string); v != "Bearer T" {
			t.Errorf("A received Authorization %q, want %q", v, "Bearer T")
		}

		redirect.Store(true)
		_, err = c.ListEvents(ctx, "primary", now, now.Add(time.Hour))
		if err == nil {
			t.Fatal("operation succeeded after A answered 302; the redirect was followed")
		}
		if !errors.Is(err, calendar.ErrRedirectRefused) {
			t.Errorf("error is not the named redirect refusal: %v", err)
		}
		if n := bHits.Load(); n != 0 {
			t.Errorf("redirect target B recorded %d request(s), want 0", n)
		}
	}

	t.Run("default environment", run)
	t.Run("legacy auth stack switch set", func(t *testing.T) {
		// The library reads this with os.Getenv on every construction, so
		// t.Setenv is enough (plan §2.2).
		t.Setenv("GOOGLE_API_GO_EXPERIMENTAL_DISABLE_NEW_AUTH_LIB", "true")
		run(t)
	})
}
