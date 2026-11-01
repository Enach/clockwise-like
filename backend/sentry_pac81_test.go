package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
)

// stubTransport records every event Sentry would send.
type stubTransport struct {
	mu     sync.Mutex
	events []*sentry.Event
}

func (s *stubTransport) Configure(sentry.ClientOptions)        {}
func (s *stubTransport) Flush(time.Duration) bool              { return true }
func (s *stubTransport) FlushWithContext(context.Context) bool { return true }
func (s *stubTransport) Close()                                {}
func (s *stubTransport) SendEvent(e *sentry.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

func (s *stubTransport) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &buf
}

// AC-A14: SENTRY_DSN unset or empty -> monitoring off: a captured error
// reaches no transport, and "monitoring disabled" is logged.
func TestPAC81_SentryOffWithoutDSN(t *testing.T) {
	for _, dsn := range []string{"", "   "} {
		sentry.CurrentHub().BindClient(nil)
		buf := captureLog(t)
		stub := &stubTransport{}
		env := map[string]string{"SENTRY_DSN": dsn}
		if initSentryFrom(func(k string) string { return env[k] }, stub) {
			t.Fatalf("DSN %q: monitoring reported on", dsn)
		}
		sentry.CaptureException(errors.New("boom"))
		sentry.Flush(time.Second)
		if stub.count() != 0 {
			t.Fatalf("DSN %q: %d event(s) sent", dsn, stub.count())
		}
		if !strings.Contains(buf.String(), "monitoring disabled") {
			t.Fatalf("log %q", buf.String())
		}
	}
}

// AC-A14: set -> events carry the configured environment and release.
func TestPAC81_SentryOnCarriesEnvironmentRelease(t *testing.T) {
	defer sentry.CurrentHub().BindClient(nil)
	stub := &stubTransport{}
	env := map[string]string{
		"SENTRY_DSN":         "https://public@o1.ingest.example.test/2",
		"SENTRY_ENVIRONMENT": "production",
		"SENTRY_RELEASE":     "paceday@1.2.3",
	}
	if !initSentryFrom(func(k string) string { return env[k] }, stub) {
		t.Fatal("monitoring off with a DSN")
	}
	sentry.CaptureException(errors.New("boom"))
	sentry.Flush(time.Second)
	if stub.count() != 1 {
		t.Fatalf("%d events", stub.count())
	}
	e := stub.events[0]
	if e.Environment != "production" || e.Release != "paceday@1.2.3" {
		t.Fatalf("environment %q release %q", e.Environment, e.Release)
	}
}

// The hard-coded fallback is gone from the backend source.
func TestPAC81_NoHardCodedDSN(t *testing.T) {
	src := readFile(t, "sentry.go")
	if strings.Contains(src, "ingest.de.sentry.io") || strings.Contains(src, "defaultBackendDSN") {
		t.Fatal("backend still carries a default DSN")
	}
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
