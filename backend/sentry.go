package main

import (
	"cmp"
	"log"
	"os"
	"strings"

	"github.com/getsentry/sentry-go"
)

// defaultBackendDSN is the PAC-14 backend Sentry DSN. It is used only when the
// SENTRY_DSN env var is unset. A Sentry ingest DSN is a client-side identifier,
// not a classic secret, but it MUST remain env-configurable so staging and prod
// can target different projects (contract §2).
const defaultBackendDSN = "https://4383608feea22d8fe1b1bb0c4a922ab1@o4512081160896512.ingest.de.sentry.io/4512083547062352"

// sensitiveHeaderKeys are request headers that must never be shipped to Sentry.
var sensitiveHeaderKeys = []string{"authorization", "cookie", "set-cookie", "x-auth-token"}

// isSensitiveKey reports whether a field name should be scrubbed. It matches the
// explicit header names plus any key containing "token" or "secret"
// (case-insensitive) so OAuth/calendar tokens and client secrets never leak
// (contract §2 PII rule, §5 PII failure path).
func isSensitiveKey(key string) bool {
	k := strings.ToLower(key)
	for _, h := range sensitiveHeaderKeys {
		if k == h {
			return true
		}
	}
	return strings.Contains(k, "token") || strings.Contains(k, "secret") || strings.Contains(k, "password")
}

// scrubSensitive is the Sentry BeforeSend hook. It strips sensitive request
// headers and any request cookies from every event and breadcrumb before the
// event leaves the process. Returning the (mutated) event keeps error reporting
// working while guaranteeing auth material is removed (contract §2, §5).
func scrubSensitive(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
	if event == nil {
		return nil
	}
	scrubRequest(event.Request)
	for i := range event.Exception {
		if event.Exception[i].Mechanism != nil {
			scrubAnyMap(event.Exception[i].Mechanism.Data)
		}
	}
	scrubStringMap(event.Tags)
	for i := range event.Breadcrumbs {
		if event.Breadcrumbs[i] != nil {
			scrubAnyMap(event.Breadcrumbs[i].Data)
		}
	}
	for k := range event.Contexts {
		scrubAnyMap(event.Contexts[k])
	}
	return event
}

// scrubRequest removes sensitive headers, all cookies, and any request body
// from a captured HTTP request. Request bodies are never attached.
func scrubRequest(req *sentry.Request) {
	if req == nil {
		return
	}
	if req.Headers != nil {
		for k := range req.Headers {
			if isSensitiveKey(k) {
				delete(req.Headers, k)
			}
		}
	}
	// Cookies carry the auth_token session cookie — drop wholesale.
	req.Cookies = ""
	// Never ship request bodies (may contain tokens/secrets/PII).
	req.Data = ""
}

func scrubStringMap(m map[string]string) {
	for k := range m {
		if isSensitiveKey(k) {
			delete(m, k)
		}
	}
}

func scrubAnyMap(m map[string]interface{}) {
	for k := range m {
		if isSensitiveKey(k) {
			delete(m, k)
		}
	}
}

// initSentry initializes Sentry from the environment. It is deliberately
// non-fatal: a bad/empty DSN degrades to "monitoring disabled" and the caller
// still boots (unlike DATABASE_URL, which is fatal by design — contract §2).
func initSentry() {
	if err := sentry.Init(sentry.ClientOptions{
		Dsn:              cmp.Or(os.Getenv("SENTRY_DSN"), defaultBackendDSN),
		Environment:      cmp.Or(os.Getenv("SENTRY_ENVIRONMENT"), "development"),
		Release:          os.Getenv("SENTRY_RELEASE"), // set by CI; empty locally
		SendDefaultPII:   false,                       // PII off by default (contract §2)
		TracesSampleRate: 0.0,                         // errors only for v1
		BeforeSend:       scrubSensitive,              // drops auth/cookie/token/secret
	}); err != nil {
		log.Printf("sentry init failed, monitoring disabled: %v", err)
	}
}
