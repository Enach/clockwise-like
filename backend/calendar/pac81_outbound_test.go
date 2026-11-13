package calendar

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/internal/outbound"
	"github.com/Enach/paceday/backend/internal/outbound/outboundtest"
	"golang.org/x/oauth2"
)

// AC-A3 / AC-A17: an ICS server that never answers fails the fetch on the
// ICS limit, and the error names the time limit.
func TestPAC81_WebcalTimeLimit(t *testing.T) {
	l := outbound.Default
	l.ICS = 200 * time.Millisecond
	defer outbound.SetForTest(l)()
	h := outboundtest.NewHang(t)
	h.Within(t, l.ICS, func() error {
		_, err := NewWebcalClient(h.URL()).ListEvents(context.Background(), time.Now(), time.Now().Add(time.Hour))
		if err != nil && !strings.Contains(err.Error(), "time limit") {
			t.Errorf("error does not name the limit: %v", err)
		}
		return err
	})
}

// A feed over the size cap fails, naming the size limit; a feed exactly at
// the cap is read.
func TestPAC81_WebcalSizeLimit(t *testing.T) {
	l := outbound.Default
	l.ICSMaxBytes = 1000
	defer outbound.SetForTest(l)()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		chunk := []byte(strings.Repeat("X", 500))
		for i := 0; i < 2000; i++ { // 1 MB offered
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	_, err := NewWebcalClient(srv.URL).ListEvents(context.Background(), time.Now(), time.Now().Add(time.Hour))
	if err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("got %v", err)
	}
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nEND:VCALENDAR\r\n"
	l.ICSMaxBytes = int64(len(ics))
	defer outbound.SetForTest(l)()
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(ics)) }))
	defer ok.Close()
	if _, err := NewWebcalClient(ok.URL).ListEvents(context.Background(), time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("feed at the cap: %v", err)
	}
}

// AC-A3: a Google calendar call to a server that never answers fails on the
// provider limit even with a context that never ends.
func TestPAC81_GoogleCalendarCallFailsWithinLimit(t *testing.T) {
	l := outbound.Default
	l.Provider = 200 * time.Millisecond
	defer outbound.SetForTest(l)()
	h := outboundtest.NewHang(t)
	Configure(config.CalendarEndpoint{BaseURL: h.URL() + config.CalendarAPIRoot, Guard: true})
	defer Configure(config.ProductionCalendarEndpoint())
	c, err := NewClient(context.Background(), oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "a", Expiry: time.Now().Add(time.Hour)}))
	if err != nil {
		t.Fatal(err)
	}
	h.Within(t, l.Provider, func() error {
		_, err := c.ListEvents(context.Background(), c.CalendarID, time.Now(), time.Now().Add(time.Hour))
		return err
	})
}
