package main

import (
	"testing"

	"github.com/getsentry/sentry-go"
)

// TestScrubSensitiveRemovesAuthMaterial is the load-bearing PII test (contract
// §2, §5): it proves Authorization, Cookie, and any *token*/*secret* field are
// removed from an event before it can leave the process.
func TestScrubSensitiveRemovesAuthMaterial(t *testing.T) {
	event := sentry.NewEvent()
	event.Request = &sentry.Request{
		Headers: map[string]string{
			"Authorization":    "Bearer super-secret-jwt",
			"Cookie":           "auth_token=abc123",
			"X-Auth-Token":     "leaky",
			"Content-Type":     "application/json",
			"X-Request-Id":     "keep-me",
			"X-Google-Token":   "oauth-token-value",
			"X-Client-Secret":  "google-client-secret",
			"X-Password-Reset": "hunter2",
		},
		Cookies: "auth_token=abc123; other=1",
		Data:    `{"password":"hunter2","note":"body should be dropped"}`,
	}
	event.Tags = map[string]string{
		"access_token": "should-go",
		"region":       "eu",
	}
	event.Contexts = map[string]sentry.Context{
		"app": {
			"refresh_token": "should-go",
			"user_count":    3,
		},
	}

	got := scrubSensitive(event, nil)
	if got == nil {
		t.Fatal("scrubSensitive returned nil for a non-nil event")
	}

	// Sensitive headers must be gone.
	for _, k := range []string{"Authorization", "Cookie", "X-Auth-Token", "X-Google-Token", "X-Client-Secret", "X-Password-Reset"} {
		if _, ok := got.Request.Headers[k]; ok {
			t.Errorf("sensitive header %q was NOT scrubbed", k)
		}
	}
	// Non-sensitive headers must survive.
	for _, k := range []string{"Content-Type", "X-Request-Id"} {
		if _, ok := got.Request.Headers[k]; !ok {
			t.Errorf("non-sensitive header %q was wrongly removed", k)
		}
	}
	// Cookies and request body must be dropped entirely.
	if got.Request.Cookies != "" {
		t.Errorf("cookies were not cleared: %q", got.Request.Cookies)
	}
	if got.Request.Data != "" {
		t.Errorf("request body was not cleared: %q", got.Request.Data)
	}
	// Sensitive tags/extra removed, benign ones kept.
	if _, ok := got.Tags["access_token"]; ok {
		t.Error("sensitive tag access_token was not scrubbed")
	}
	if _, ok := got.Tags["region"]; !ok {
		t.Error("benign tag region was wrongly removed")
	}
	if _, ok := got.Contexts["app"]["refresh_token"]; ok {
		t.Error("sensitive context field refresh_token was not scrubbed")
	}
	if _, ok := got.Contexts["app"]["user_count"]; !ok {
		t.Error("benign context field user_count was wrongly removed")
	}
}

func TestScrubSensitiveNilEvent(t *testing.T) {
	if got := scrubSensitive(nil, nil); got != nil {
		t.Errorf("expected nil for nil event, got %+v", got)
	}
}

func TestIsSensitiveKey(t *testing.T) {
	sensitive := []string{
		"Authorization", "authorization", "Cookie", "Set-Cookie",
		"access_token", "REFRESH_TOKEN", "google_client_secret",
		"MySecret", "x-auth-token", "password",
	}
	for _, k := range sensitive {
		if !isSensitiveKey(k) {
			t.Errorf("expected %q to be flagged sensitive", k)
		}
	}
	benign := []string{"Content-Type", "X-Request-Id", "region", "user_count", "name"}
	for _, k := range benign {
		if isSensitiveKey(k) {
			t.Errorf("expected %q to be treated as benign", k)
		}
	}
}

// TestInitSentryWithoutDSNIsNonFatal is the boot-without-DSN smoke check
// (contract §5 failure path): initSentry with an empty DSN must not panic or
// exit — it degrades to "monitoring disabled" and the caller proceeds.
func TestInitSentryWithoutDSNIsNonFatal(t *testing.T) {
	t.Setenv("SENTRY_DSN", "")
	// A blank DSN passed to sentry.Init disables the SDK cleanly; the default
	// DSN would otherwise be substituted, so we assert the empty path here by
	// calling the scrubber wiring directly through Init with an explicit blank.
	err := sentry.Init(sentry.ClientOptions{
		Dsn:        "",
		BeforeSend: scrubSensitive,
	})
	if err != nil {
		t.Fatalf("empty-DSN init should be non-fatal, got: %v", err)
	}
	// initSentry itself must never panic/exit regardless of DSN validity.
	initSentry()
}
