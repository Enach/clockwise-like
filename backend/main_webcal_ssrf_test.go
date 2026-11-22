package main

import (
	"os"
	"strings"
	"testing"
)

// Security audit of 2026-11-22 (SSRF via the stored webcal feed URL): the
// feed egress decision is made once, at startup, from the test-mode
// declaration, and installed before any cron or route exists. Without the
// wiring the fail-closed default still protects production — it would be the
// test stacks that break — so this is here to keep the decision in one place
// and next to the calendar one.
func TestFeedEgressIsWiredAtStartup(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	for _, want := range []string{
		"config.DecideFeedEgress(os.Getenv(config.EnvAllowStandins))",
		"calendar.ConfigureFeeds(feedEgress)",
		"feedEgress.Notice",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("main.go does not contain %q", want)
		}
	}
	// It is decided before the database is opened, as the calendar decision
	// is: an unsafe configuration is refused, or announced, before anything
	// is touched.
	decide := strings.Index(text, "config.DecideFeedEgress")
	open := strings.Index(text, "openDatabase(context.Background(), dsn)")
	if decide < 0 || open < 0 || decide > open {
		t.Errorf("the feed egress decision is not made before the database is opened (decide=%d, open=%d)", decide, open)
	}
}
