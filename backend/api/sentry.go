package api

import (
	"net/http"

	sentryhttp "github.com/getsentry/sentry-go/http"
)

// sentryMiddleware wraps the router so that a panic in any handler is reported
// to Sentry and then re-raised, preserving the existing behavior where the
// stdlib/chi recoverer turns the panic into a 500 for the client. Repanic is
// true precisely so the downstream 500 path is unchanged (contract §3, §5).
//
// The middleware is always installed; when sentry.Init did not run (no DSN /
// init failed), the Sentry hub is a no-op, so reporting silently does nothing
// and the request path is unaffected (contract §2 fail-open).
func sentryMiddleware() func(http.Handler) http.Handler {
	sh := sentryhttp.New(sentryhttp.Options{
		// Repanic must be true: after capturing, re-raise so the existing
		// recover path still converts the panic into a 500. The client-facing
		// contract does not change (contract §3).
		Repanic: true,
	})
	return sh.Handle
}
