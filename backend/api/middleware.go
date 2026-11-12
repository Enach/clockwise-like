package api

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/internal/budget"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type ctxKey string

const (
	ctxUserEmail ctxKey = "userEmail"
	// ctxSessionIssuedAt is the session JWT's issue time (time.Time; zero when
	// the token carries none). PAC-50's break-glass freshness rule reads it.
	ctxSessionIssuedAt ctxKey = "sessionIssuedAt"
)

// sessionIssuedAt returns the session's issue time, or the zero time.
func sessionIssuedAt(ctx context.Context) time.Time {
	t, _ := ctx.Value(ctxSessionIssuedAt).(time.Time)
	return t
}

// sessionEmail returns the session's email claim, or "".
func sessionEmail(ctx context.Context) string {
	e, _ := ctx.Value(ctxUserEmail).(string)
	return e
}

func userIDFromCtx(ctx context.Context) uuid.UUID {
	return auth.UserIDFromContext(ctx)
}

// sessionUnauthorized writes requireAuth's 401, byte for byte today's: the
// JSON text with http.Error's text/plain Content-Type.
func sessionUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
}

// sessionGenerationUnavailable is the PAC-71 500 (SessionGenerationUnavailable,
// spec AC-48): JSON, no Set-Cookie, never a 401.
func sessionGenerationUnavailable(w http.ResponseWriter, r *http.Request, err error) {
	writeServerError(w, r, "session generation", err, http.StatusInternalServerError)
}

// afterAuthHook is a deterministic test seam (PAC-71 AC-47, AC-50): it
// runs after requireAuth has authenticated the request,
// before the handler. Production leaves it nil.
var afterAuthHook func(r *http.Request)

// requireAuth authenticates the session (PAC-39: an Authorization bearer token
// wins over the auth_token cookie) and, since PAC-71, its session generation
// (spec docs/specs/PAC-71.md §3 "Sessions"; contract bearerAuth; plan §3):
//
//   - the token's `gen` (absent = 0) must equal the account's current
//     generation, read uncached; otherwise today's 401;
//   - a generation that cannot be read is a JSON 500, never a 401 (AC-48);
//   - the request context carries the session guard for the in-request
//     re-check, and a re-check that finds the session stale (or the
//     generation unreadable) replaces whatever the handler answers with the
//     401 (or the 500);
//   - no account row for `sub`: the request proceeds as before PAC-71, and
//     every in-request re-check answers the 401 (contract §5, CONTRACT-12).
//
// preAuthExpiry holds, by request path, the budget answer a route gives
// before its handler registers its own (PAC-81 AC-A2): requireAuth registers
// it ahead of the session-generation read.
var preAuthExpiry sync.Map

func requireAuth(db *sql.DB, jwtSecret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// PAC-39 / API-008: an explicit Authorization: Bearer header wins over
			// the auth_token cookie, so a stale cookie cannot shadow a valid token.
			// A present-but-invalid bearer token is rejected, never retried with
			// the cookie: the client named the identity it wants.
			var tokenStr string
			if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
				tokenStr = strings.TrimPrefix(h, "Bearer ")
			} else if cookie, err := r.Cookie("auth_token"); err == nil {
				tokenStr = cookie.Value
			}
			if tokenStr == "" {
				sessionUnauthorized(w)
				return
			}
			claims, err := auth.ValidateToken(tokenStr, jwtSecret)
			if err != nil {
				sessionUnauthorized(w)
				return
			}
			userID, err := uuid.Parse(claims.UserID)
			if err != nil {
				sessionUnauthorized(w)
				return
			}
			gen, err := claims.Generation()
			if err != nil {
				sessionUnauthorized(w)
				return
			}
			// PAC-81 AC-A2 with PAC-71: a route whose budget answer is fixed
			// before its handler runs (the step-up callback's denied redirect)
			// gets it now, since the generation read below can be what waits.
			if answer, ok := preAuthExpiry.Load(r.URL.Path); ok {
				budget.OnExpiry(r.Context(), answer.(func(http.ResponseWriter, *http.Request)))
			}
			// One plain, uncached read: no transaction or connection is held
			// while the handler runs.
			current, found, err := storage.CurrentGeneration(storage.Bind(r.Context(), db), userID)
			if err != nil {
				sessionGenerationUnavailable(w, r, err)
				return
			}
			if found && current != gen {
				sessionUnauthorized(w)
				return
			}
			ctx := context.WithValue(r.Context(), auth.UserIDKey, userID)
			ctx = context.WithValue(ctx, ctxUserEmail, claims.Email)
			if claims.IssuedAt != nil {
				ctx = context.WithValue(ctx, ctxSessionIssuedAt, claims.IssuedAt.Time)
			}
			// Without an account row (found is false) the guard starts clean: the
			// request proceeds as before PAC-71, and the first in-request re-check
			// finds no current generation and marks it stale.
			guard := &storage.SessionGuard{UserID: userID, Gen: gen}
			ctx = storage.WithSessionGuard(ctx, guard)
			r = r.WithContext(ctx)
			if afterAuthHook != nil {
				afterAuthHook(r)
			}
			// base: the headers requireAuth's own 401 would carry (those the
			// outer middleware set, CORS included), restored on an override.
			gw := &guardedResponseWriter{ResponseWriter: w, r: r, guard: guard, base: w.Header().Clone()}
			next.ServeHTTP(gw, r)
			gw.finish()
		})
	}
}

// guardedResponseWriter turns a stale in-request re-check into requireAuth's
// 401 (and an unreadable generation into the 500), "whatever the operation
// had already decided" (contract bearerAuth): the decision is taken when the
// handler first writes, or when it returns without writing. On an override
// the header map is reset to base, the headers present when requireAuth
// handed over (exactly those its own 401 carries), so nothing the handler
// prepared (a cookie, a Location) leaks into the answer and nothing the
// outer middleware set (CORS) is lost.
type guardedResponseWriter struct {
	http.ResponseWriter
	r          *http.Request
	guard      *storage.SessionGuard
	base       http.Header
	decided    bool
	overridden bool
}

func (g *guardedResponseWriter) decide() bool {
	if g.decided {
		return g.overridden
	}
	g.decided = true
	if !g.guard.Stale() && !g.guard.Unavailable() {
		return false
	}
	g.overridden = true
	h := g.ResponseWriter.Header()
	for k := range h {
		delete(h, k)
	}
	for k, v := range g.base {
		h[k] = append([]string(nil), v...)
	}
	if g.guard.Stale() {
		sessionUnauthorized(g.ResponseWriter)
	} else {
		sessionGenerationUnavailable(g.ResponseWriter, g.r, storage.ErrGenerationUnavailable)
	}
	return true
}

func (g *guardedResponseWriter) WriteHeader(code int) {
	if g.decide() {
		return
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *guardedResponseWriter) Write(b []byte) (int, error) {
	if g.decide() {
		return len(b), nil
	}
	return g.ResponseWriter.Write(b)
}

// finish decides for a handler that wrote nothing.
func (g *guardedResponseWriter) finish() {
	if !g.decided {
		g.decide()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (g *guardedResponseWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }

func corsMiddleware(allowedOrigin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if allowedOrigin == "*" || allowedOrigin == "" {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				// Reflect the request origin only when it matches the configured value.
				// Wildcard is incompatible with credentials:include, so we need an exact origin.
				if origin == allowedOrigin {
					w.Header().Set("Access-Control-Allow-Origin", origin)
				}
				w.Header().Add("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// loggingMiddleware writes exactly one access-log line per request (PAC-81
// AC-A13): method, the matched route pattern (never the raw path, which can
// carry an invite token, and never the query string), the status actually
// written ("-" when nothing was written, e.g. the client went away), the
// duration, and the client address as the abuse limits resolve it
// (TRUSTED_PROXIES and X-Real-IP). It sits outside every middleware that can
// write or replace a status, so a replaced status and the budget's 503 are
// what it records.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		defer func() {
			status := "-"
			if sw.status != 0 {
				status = strconv.Itoa(sw.status)
			}
			p := recover()
			if p != nil {
				status = "panic"
			}
			accessLogf("%s %s %s %s client=%s", r.Method, routeOf(r), status, time.Since(start), clientIP(r, trustedProxies))
			if p != nil {
				panic(p)
			}
		}()
		next.ServeHTTP(sw, r)
	})
}

// guarded runs fn under PAC-71's in-request re-check: in one transaction whose
// first statement confirms the session's generation is still the account's
// (contract §5). Every request-path write of account data goes through it, so
// a write never lands after a binding or an operator step advanced the
// generation; on a mismatch nothing is stored and requireAuth answers 401.
// The pool wait ends with the request (PAC-81 AC-A9): db is bound to ctx.
func guarded(ctx context.Context, db storage.DBTX, fn func(q storage.Querier) error) error {
	return storage.WithSessionGeneration(ctx, storage.Bind(ctx, db), fn)
}

// routeMux is the router whose patterns the access log reports
// (RegisterRoutes sets it). Matching on a fresh route context reads nothing
// the handler goroutine writes, so a request the budget answered while its
// handler still runs is logged without a data race.
var routeMux atomic.Pointer[chi.Mux]

// routeOf is r's route pattern, "unmatched" when no route matches, or the
// path when no router is registered (handlers tested without one).
func routeOf(r *http.Request) string {
	mx := routeMux.Load()
	if mx == nil {
		return r.URL.Path
	}
	rctx := chi.NewRouteContext()
	if mx.Match(rctx, r.Method, r.URL.Path) {
		if p := rctx.RoutePattern(); p != "" {
			return p
		}
	}
	return "unmatched"
}

// accessLogf is the access log's writer (a test seam).
var accessLogf = log.Printf

// statusWriter records the first status written to the client.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// maxRequestBody is the largest request body the backend accepts (PAC-81
// AC-A12, D-7): 1 MiB, the same as nginx.
const maxRequestBody = 1 << 20

// bodyCapMiddleware answers 413 {"error":"request body too large"} before
// the handler runs when the body is over the cap: a declared Content-Length
// over it, or a body that reads past it. A body of exactly the cap is passed
// on. Bodies are read here, bounded by the cap, so the handler sees the
// whole body or never runs.
func bodyCapMiddleware(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > limit {
				writeError(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			if r.Body != nil && r.Body != http.NoBody {
				b, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
				if int64(len(b)) > limit {
					writeError(w, "request body too large", http.StatusRequestEntityTooLarge)
					return
				}
				if err != nil {
					writeError(w, "invalid request body", http.StatusBadRequest)
					return
				}
				_ = r.Body.Close()
				r.Body = io.NopCloser(bytes.NewReader(b))
			}
			next.ServeHTTP(w, r)
		})
	}
}
