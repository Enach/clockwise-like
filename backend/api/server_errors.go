package api

import (
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/getsentry/sentry-go"
)

// reportServerError sends a 5xx's underlying error to Sentry. It uses the
// request's hub (set by sentryMiddleware) and falls back to the global hub;
// with no DSN configured both are no-ops. A variable so tests can observe it.
var reportServerError = func(r *http.Request, err error) {
	hub := sentry.GetHubFromContext(r.Context())
	if hub == nil {
		hub = sentry.CurrentHub()
	}
	hub.CaptureException(err)
}

// genericServerMessage is the only error-derived text a 5xx body may carry
// (PAC-21): a fixed message chosen by status, never the error itself.
func genericServerMessage(code int) string {
	switch code {
	case http.StatusBadGateway:
		return "upstream provider error"
	case http.StatusServiceUnavailable:
		return "service unavailable"
	default:
		return "internal error"
	}
}

// logServerError records a server-side failure — op plus the raw Go, driver or
// provider error — in the server log and in Sentry. The log line names method
// and path but never the query string, which on OAuth callbacks carries the
// authorization code. A nil err is reported as op alone.
func logServerError(r *http.Request, op string, err error, code int) {
	if err == nil {
		err = errors.New(op)
	} else {
		err = fmt.Errorf("%s: %w", op, err)
	}
	log.Printf("ERROR %s %s -> %d: %v", r.Method, r.URL.Path, code, err)
	reportServerError(r, err)
}

// writeServerError answers a server-side failure on an operation whose
// contract documents the JSON error envelope: the detail goes to logs and
// Sentry only, the body is {"error": genericServerMessage(code)}.
func writeServerError(w http.ResponseWriter, r *http.Request, op string, err error, code int) {
	logServerError(r, op, err, code)
	writeError(w, genericServerMessage(code), code)
}

// writeServerErrorText is writeServerError for the operations whose contract
// still documents a text/plain 5xx (http.Error). The content type is kept so
// the contract stays true; only the leaked detail is removed. Moving these to
// the JSON envelope is a contract change (API-036), not this helper's job.
func writeServerErrorText(w http.ResponseWriter, r *http.Request, op string, err error, code int) {
	logServerError(r, op, err, code)
	http.Error(w, genericServerMessage(code), code)
}
