package api

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/storage"
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

// afterAuthLeaseHook is a deterministic test seam (PAC-71 AC-47, AC-50): it
// runs after requireAuth has authenticated the request and taken its lease,
// before the handler. Production leaves it nil.
var afterAuthLeaseHook func(r *http.Request)

// requireAuth authenticates the session (PAC-39: an Authorization bearer token
// wins over the auth_token cookie) and, since PAC-71, its session generation
// (spec docs/specs/PAC-71.md §3 "Sessions"; contract bearerAuth; plan §3):
//
//   - the token's `gen` (absent = 0) must equal the account's current
//     generation, read uncached; otherwise today's 401;
//   - a generation that cannot be read is a JSON 500, never a 401 (AC-48);
//   - the request holds the account's lease until the handler returns, so no
//     generation advance (a binding, an operator step) can commit while it
//     runs;
//   - the request context carries the session guard for the in-request
//     re-check, and a re-check that finds the session stale (or the
//     generation unreadable) replaces whatever the handler answers with the
//     401 (or the 500);
//   - no account row for `sub`: the request proceeds as before PAC-71, and
//     every in-request re-check answers the 401 (contract §5, CONTRACT-12).
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
			lease, current, found, err := storage.BeginSessionLease(r.Context(), db, userID)
			if err != nil {
				sessionGenerationUnavailable(w, r, err)
				return
			}
			defer lease.Release()
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
			if afterAuthLeaseHook != nil {
				afterAuthLeaseHook(r)
			}
			gw := &guardedResponseWriter{ResponseWriter: w, r: r, guard: guard}
			next.ServeHTTP(gw, r)
			gw.finish()
		})
	}
}

// guardedResponseWriter turns a stale in-request re-check into requireAuth's
// 401 (and an unreadable generation into the 500), "whatever the operation
// had already decided" (contract bearerAuth): the decision is taken when the
// handler first writes, or when it returns without writing, and every header
// the handler set is dropped so nothing it prepared (a cookie, a Location)
// leaks into the 401.
type guardedResponseWriter struct {
	http.ResponseWriter
	r          *http.Request
	guard      *storage.SessionGuard
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

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}
