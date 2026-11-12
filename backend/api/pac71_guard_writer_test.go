package api

import (
	"database/sql"
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
	return guardChainOn(openTestDB(t), h)
}

func guardChainOn(db *sql.DB, h http.Handler) http.Handler {
	return corsMiddleware(guardOrigin)(budget.Middleware(5*time.Second, nil)(requireAuth(db, pac71Secret)(h)))
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
	never := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("handler ran") })
	// requireAuth's own 401 for the same request (stale before the handler
	// runs): the reference for the stale cases.
	ref := createTestUser(t, "guard-ref-"+uuid.NewString()[:8]+"@example.com")
	execT(t, `UPDATE users SET session_generation = 1 WHERE id = $1`, ref)
	want401 := guardRequest(t, guardChain(t, never), sessionCookie(t, ref, 0))
	assert401(t, want401)
	// requireAuth's own 500 when the generation cannot be read (AC-48): the
	// reference for the unavailable case.
	want500 := guardRequest(t, guardChainOn(closedDB(t), never), sessionCookie(t, ref, 0))
	if want500.Code != http.StatusInternalServerError {
		t.Fatalf("reference 500 = %d %q", want500.Code, want500.Body.String())
	}
	for _, w := range []*httptest.ResponseRecorder{want401, want500} {
		if w.Header().Get("Access-Control-Allow-Origin") != guardOrigin {
			t.Fatalf("reference %d lacks CORS: %v", w.Code, w.Header())
		}
	}

	type guardCase struct {
		handler func(t *testing.T, a uuid.UUID) http.HandlerFunc
		want    *httptest.ResponseRecorder
	}
	cases := map[string]guardCase{
		"writes a redirect": {func(t *testing.T, a uuid.UUID) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				handlerHeaders(w)
				moveGeneration(t, r, a)
				w.WriteHeader(http.StatusFound)
			}
		}, want401},
		"writes a body": {func(t *testing.T, a uuid.UUID) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				handlerHeaders(w)
				moveGeneration(t, r, a)
				_, _ = w.Write([]byte("<html>private</html>"))
			}
		}, want401},
		"returns without writing": {func(t *testing.T, a uuid.UUID) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				handlerHeaders(w)
				moveGeneration(t, r, a)
			}
		}, want401},
		// An in-request re-check that cannot read the generation (the guard
		// marked unavailable) is requireAuth's own 500, CORS included.
		"re-check cannot read the generation, then writes": {func(t *testing.T, _ uuid.UUID) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				handlerHeaders(w)
				storage.SessionGuardFrom(r.Context()).MarkUnavailable()
				w.WriteHeader(http.StatusFound)
				_, _ = w.Write([]byte("<html>private</html>"))
			}
		}, want500},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			a := createTestUser(t, "guard-"+uuid.NewString()[:8]+"@example.com")
			rec := guardRequest(t, guardChain(t, c.handler(t, a)), sessionCookie(t, a, 0))
			if rec.Code != c.want.Code || rec.Body.String() != c.want.Body.String() {
				t.Fatalf("= %d %q, want requireAuth's own %d %q", rec.Code, rec.Body.String(), c.want.Code, c.want.Body.String())
			}
			if c.want == want401 {
				assert401(t, rec)
			}
			if !reflect.DeepEqual(rec.Header(), c.want.Header()) {
				t.Fatalf("headers of the guarded %d:\n got %v\nwant %v (requireAuth's own)", rec.Code, rec.Header(), c.want.Header())
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
