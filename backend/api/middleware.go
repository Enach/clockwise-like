package api

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/auth"
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

func requireAuth(jwtSecret string) func(http.Handler) http.Handler {
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
				w.Header().Set("Content-Type", "application/json")
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			claims, err := auth.ValidateToken(tokenStr, jwtSecret)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			userID, err := uuid.Parse(claims.UserID)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), auth.UserIDKey, userID)
			ctx = context.WithValue(ctx, ctxUserEmail, claims.Email)
			if claims.IssuedAt != nil {
				ctx = context.WithValue(ctx, ctxSessionIssuedAt, claims.IssuedAt.Time)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

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
// AC-A13): method, path without the query string (it can carry codes and
// tokens), the status actually sent on the wire, duration, and the client
// address as the abuse limits resolve it (TRUSTED_PROXIES and X-Real-IP).
// It sits outside every middleware that can write or replace a status, so a
// replaced status and the budget's 503 are what it records.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		defer func() {
			status := sw.status
			if status == 0 {
				status = http.StatusOK // nothing written: net/http sends 200
			}
			if p := recover(); p != nil {
				status = http.StatusInternalServerError
				accessLogf("%s %s %d %s client=%s", r.Method, r.URL.Path, status, time.Since(start), clientIP(r, trustedProxies))
				panic(p)
			}
			accessLogf("%s %s %d %s client=%s", r.Method, r.URL.Path, status, time.Since(start), clientIP(r, trustedProxies))
		}()
		next.ServeHTTP(sw, r)
	})
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
