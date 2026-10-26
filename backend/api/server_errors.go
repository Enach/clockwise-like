package api

import (
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/Enach/paceday/backend/engine"
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

// calendarNotConnectedMessage is the one body of the 401 CalendarNotConnected
// (PAC-21, API-054): the contract pins it with an enum so clients can branch
// on it.
const calendarNotConnectedMessage = "calendar not connected"

// writeCalendarNotConnected answers an operation that needs the caller's
// calendar when they have none connected. The requireAuth middleware's 401
// stays text/plain; this one is JSON, so a client can tell them apart.
func writeCalendarNotConnected(w http.ResponseWriter) {
	writeError(w, calendarNotConnectedMessage, http.StatusUnauthorized)
}

// writeCalendarFailure answers a failed calendar-dependent operation: the
// 401 CalendarNotConnected when the caller has no stored credential, else
// the generic 500 with the detail in the log and Sentry only. A failure to
// read the credential store is the 500, not the 401.
func writeCalendarFailure(w http.ResponseWriter, r *http.Request, op string, err error) {
	if errors.Is(err, engine.ErrCalendarNotConnected) {
		writeCalendarNotConnected(w)
		return
	}
	writeServerError(w, r, op, err, http.StatusInternalServerError)
}
