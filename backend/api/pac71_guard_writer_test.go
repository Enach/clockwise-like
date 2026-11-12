package api

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/budget"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// PAC-71 contract bearerAuth, PAC-90 spec challenge C-3: when the
// in-request re-check finds the session's generation moved, the answer is
// requireAuth's own 401 with exactly the headers requireAuth's own 401
// carries (those the outer middleware set, CORS included), and nothing the
// handler prepared (a cookie, a Location, its own headers), whether the
// handler writes or returns without writing. (Flush through
// http.ResponseController is PAC-90's AC-8b.)

const guardOrigin = "https://app.example"

// guardChain is the production order around a protected route: CORS, the
// request budget, requireAuth.
func guardChain(t *testing.T, h http.Handler) http.Handler {
	t.Helper()
	return corsMiddleware(guardOrigin)(budget.Middleware(5*time.Second, nil)(requireAuth(openTestDB(t), pac71Secret)(h)))
}

func guardRequest(t *testing.T, chain http.Handler, session *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.Header.Set("Origin", guardOrigin)
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)
	return rec
}

// moveGeneration advances a's generation and runs one guarded load, which
// marks the request's guard stale (the in-request re-check).
func moveGeneration(t *testing.T, r *http.Request, a uuid.UUID) {
	t.Helper()
	execT(t, `UPDATE users SET session_generation = session_generation + 1 WHERE id = $1`, a)
	_, _ = storage.GetOrCreateSettingsByUserCtx(r.Context(), openTestDB(t), a)
	if g := storage.SessionGuardFrom(r.Context()); !g.Stale() {
		t.Fatal("the in-request re-check did not find the generation moved")
	}
}

func handlerHeaders(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: "auth_token", Value: "handler-made", Path: "/"})
	w.Header().Set("Location", "/somewhere")
	w.Header().Set("X-Handler", "prepared")
	w.Header().Set("Content-Type", "text/html")
}

func TestPAC71_GuardedWriter_StaleAnswerCarriesOnlyThe401sOwnHeaders(t *testing.T) {
	// requireAuth's own 401 for the same request (stale before the handler
	// runs): the reference header set.
	ref := createTestUser(t, "guard-ref-"+uuid.NewString()[:8]+"@example.com")
	execT(t, `UPDATE users SET session_generation = 1 WHERE id = $1`, ref)
	never := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("handler ran on a stale session") })
	want := guardRequest(t, guardChain(t, never), sessionCookie(t, ref, 0))
	assert401(t, want)
	if want.Header().Get("Access-Control-Allow-Origin") != guardOrigin {
		t.Fatalf("reference 401 lacks CORS: %v", want.Header())
	}

	cases := map[string]func(t *testing.T, a uuid.UUID) http.HandlerFunc{
		"writes a redirect": func(t *testing.T, a uuid.UUID) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				handlerHeaders(w)
				moveGeneration(t, r, a)
				w.WriteHeader(http.StatusFound)
			}
		},
		"writes a body": func(t *testing.T, a uuid.UUID) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				handlerHeaders(w)
				moveGeneration(t, r, a)
				_, _ = w.Write([]byte("<html>private</html>"))
			}
		},
		"returns without writing": func(t *testing.T, a uuid.UUID) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				handlerHeaders(w)
				moveGeneration(t, r, a)
			}
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			a := createTestUser(t, "guard-"+uuid.NewString()[:8]+"@example.com")
			rec := guardRequest(t, guardChain(t, mk(t, a)), sessionCookie(t, a, 0))
			assert401(t, rec)
			if !reflect.DeepEqual(rec.Header(), want.Header()) {
				t.Fatalf("headers of the guarded 401:\n got %v\nwant %v (requireAuth's own 401)", rec.Header(), want.Header())
			}
			if strings.Contains(rec.Body.String(), "private") {
				t.Fatal("the handler's body leaked")
			}
		})
	}
}

// A request whose generation stays current is untouched: the handler's
// headers and status go through.
func TestPAC71_GuardedWriter_CurrentSessionPassesThrough(t *testing.T) {
	a := createTestUser(t, "guard-ok-"+uuid.NewString()[:8]+"@example.com")
	chain := guardChain(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerHeaders(w)
		_, _ = storage.GetOrCreateSettingsByUserCtx(r.Context(), openTestDB(t), a)
		w.WriteHeader(http.StatusFound)
	}))
	rec := guardRequest(t, chain, sessionCookie(t, a, 0))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/somewhere" || rec.Header().Get("X-Handler") != "prepared" ||
		rec.Header().Get("Access-Control-Allow-Origin") != guardOrigin || len(rec.Result().Header.Values("Set-Cookie")) != 1 {
		t.Fatalf("= %d %v", rec.Code, rec.Header())
	}
}
