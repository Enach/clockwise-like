package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/auth"
	"github.com/go-chi/chi/v5"
	"golang.org/x/oauth2"
)

// PAC-53: GET /api/audit refuses a `limit` it cannot read with one fixed JSON
// 400 (docs/specs/PAC-53.md §3.2, contracts/features/PAC-53.md). The rows of
// the rule are C-1..C-10 in the `limit` description of listAuditEntries.

const pac53Body = `{"error":"limit must be an integer from 1 to 500"}` + "\n"

// pac53Refused is every refused request of AC-3..AC-6, by criterion.
var pac53Refused = []struct{ ac, query string }{
	// AC-3: C-2 and C-3.
	{"AC-3", "?limit="},
	{"AC-3", "?limit"},
	{"AC-3", "?limit=abc"},
	{"AC-3", "?limit=50.5"},
	{"AC-3", "?limit=1e2"},
	{"AC-3", "?limit=0x10"},
	{"AC-3", "?limit=1_0"},
	{"AC-3", "?limit=+50"},
	{"AC-3", "?limit=%2050"},
	{"AC-3", "?limit=50%20"},
	// AC-4: C-5 and C-7, overflow on both sides included.
	{"AC-4", "?limit=0"},
	{"AC-4", "?limit=-0"},
	{"AC-4", "?limit=-5"},
	{"AC-4", "?limit=501"},
	{"AC-4", "?limit=10000"},
	{"AC-4", "?limit=9223372036854775807"},
	{"AC-4", "?limit=99999999999999999999"},
	{"AC-4", "?limit=-9223372036854775809"},
	// AC-5: C-8, identical values included.
	{"AC-5", "?limit=10&limit=500"},
	{"AC-5", "?limit=10&limit=10"},
	// AC-6: C-9, under any name, as separator or inside a value.
	{"AC-6", "?limit=%zz"},
	{"AC-6", "?limit=%zz&limit=10"},
	{"AC-6", "?x=%zz&limit=10"},
	{"AC-6", "?limit=10;x=1"},
	{"AC-6", "?x=a;b&limit=10"},
}

// pac53AssertRefused: status 400, application/json, exactly the fixed body
// (so no entries and no echo of the input), and the body is the contract's
// ErrorResponse with no other key.
func pac53AssertRefused(t *testing.T, where string, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusBadRequest {
		t.Errorf("%s: status %d body %q, want 400", where, w.Code, w.Body.String())
		return
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("%s: Content-Type %q, want application/json", where, ct)
	}
	if got := w.Body.String(); got != pac53Body {
		t.Errorf("%s: body %q, want %q", where, got, pac53Body)
	}
	dec := json.NewDecoder(bytes.NewReader(w.Body.Bytes()))
	dec.DisallowUnknownFields()
	var e gen.ErrorResponse
	if err := dec.Decode(&e); err != nil || e.Error == "" {
		t.Errorf("%s: body %q is not the contract's ErrorResponse: %v", where, w.Body.String(), err)
	}
}

// The rule itself, on the raw query, with no database involved.
func TestPAC53_AuditLimitRule(t *testing.T) {
	accepted := map[string]int{
		"":                           50, // C-1
		"x=1":                        50, // C-1, C-10
		"LIMIT=abc":                  50, // C-10: case-sensitive name
		"Limit=7":                    50, // C-10
		"limit=1":                    1,  // C-6
		"limit=10":                   10,
		"limit=500":                  500,
		"limit=%2B10":                10, // C-4: decoded sign
		"limit=010":                  10, // C-4: leading zeros
		"limit=0000000000000000050":  50,
		"%6Cimit=5":                  5, // matched after decoding
		"limit=10&LIMIT=x&other=%41": 10,
	}
	for q, want := range accepted {
		got, ok := auditLimit(q)
		if !ok || got != want {
			t.Errorf("auditLimit(%q) = %d, %v; want %d, true", q, got, ok, want)
		}
	}
	refused := []string{"limit=-1%30", // "-10"
		"limit=+-5", "limit=--5", "limit=%2B%2B5", "limit=%E2%80%8B5", "limit=５", "%6Cimit=5&limit=5", "limit=5;"}
	for _, c := range pac53Refused {
		refused = append(refused, strings.TrimPrefix(c.query, "?"))
	}
	for _, q := range refused {
		if got, ok := auditLimit(q); ok {
			t.Errorf("auditLimit(%q) = %d, true; want refused", q, got)
		}
	}
}

// AC-1: 120 entries, no count -> exactly 50 (C-1). Also AC-2.
// AC-2: 1, 10, 500 -> at most 1, 10, 120; %2B10 and 010 -> 10 (C-4, C-6).
// C-10: other parameters, LIMIT included, are ignored.
func TestPAC53_AC1_AC2_AcceptedCounts(t *testing.T) {
	u := createTestUser(t, "pac53-accepted@example.com")
	seedAuditRows(t, u, 120)
	for q, want := range map[string]int{
		"":                  50,
		"?limit=1":          1,
		"?limit=10":         10,
		"?limit=500":        120,
		"?limit=%2B10":      10,
		"?limit=010":        10,
		"?LIMIT=abc":        50,
		"?%6Cimit=5":        5,
		"?limit=7&LIMIT=99": 7,
	} {
		if got := len(decodeAudit(t, getAudit(t, u, q))); got != want {
			t.Errorf("%q: got %d entries, want %d", q, got, want)
		}
	}
}

// AC-3..AC-6: every refusal is the same fixed JSON 400 with no entries, even
// for a user who has entries; and no body echoes the submitted text.
func TestPAC53_AC3_AC6_RefusedWithTheFixedBody(t *testing.T) {
	u := createTestUser(t, "pac53-refused@example.com")
	seedAuditRows(t, u, 12)
	for _, c := range pac53Refused {
		w := getAudit(t, u, c.query)
		pac53AssertRefused(t, c.ac+" "+c.query, w)
		if strings.Contains(w.Body.String(), "focus_created") {
			t.Errorf("%s %q: refusal carries entries: %s", c.ac, c.query, w.Body.String())
		}
	}
	// AC-3's no-echo clause, named explicitly.
	for _, q := range []string{"?limit=abc", "?limit=99999999999999999999"} {
		body := getAudit(t, u, q).Body.String()
		if echoed := strings.TrimPrefix(q, "?limit="); strings.Contains(body, echoed) {
			t.Errorf("%q: body %q echoes %q", q, body, echoed)
		}
	}
	// AC-6: the request that used to answer 10 entries.
	if w := getAudit(t, u, "?limit=%zz&limit=10"); strings.HasPrefix(strings.TrimSpace(w.Body.String()), "[") {
		t.Errorf("?limit=%%zz&limit=10 still answers entries: %s", w.Body.String())
	}
}

func pac53RouterRequest(t *testing.T, r http.Handler, query string, user bool, secret string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/audit"+query, nil)
	if user {
		u := createTestUser(t, "pac53-router@example.com")
		tok, err := auth.GenerateToken(u, "pac53-router@example.com", secret)
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(&http.Cookie{Name: "auth_token", Value: tok})
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// AC-7: through the real router (RegisterRoutes), an unauthenticated request
// with an invalid count gets the middleware's 401, not the 400; the same
// request authenticated reaches this handler and gets the 400.
func TestPAC53_AC7_UnauthenticatedBeforeLimit(t *testing.T) {
	const secret = "pac53-router-secret"
	r := chi.NewRouter()
	RegisterRoutes(r, openTestDB(t), &oauth2.Config{}, secret, "http://localhost", "http://localhost")
	for _, q := range []string{"?limit=abc", "?limit=%zz", "?limit=10;x=1", "?limit=10&limit=10"} {
		w := pac53RouterRequest(t, r, q, false, secret)
		if w.Code != http.StatusUnauthorized || w.Body.String() != "{\"error\":\"unauthorized\"}\n" {
			t.Errorf("unauthenticated %q: status %d body %q, want the middleware's 401", q, w.Code, w.Body.String())
		}
		if ct := w.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
			t.Errorf("unauthenticated %q: Content-Type %q", q, ct)
		}
	}
	pac53AssertRefused(t, "authenticated ?limit=abc via the router", pac53RouterRequest(t, r, "?limit=abc", true, secret))
	if w := pac53RouterRequest(t, r, "?limit=5", true, secret); w.Code != http.StatusOK {
		t.Errorf("authenticated ?limit=5 via the router: status %d", w.Code)
	}
}

// AC-8: the 400 matches the response the bundle declares for 400 on this
// operation (status, media type, ErrorResponse = {error: string}); every
// request of AC-1..AC-6 gets a status the operation declares.
func TestPAC53_AC8_ResponsesAreDeclared(t *testing.T) {
	root := parseBundle(t, pac21Bundle)
	responses := root.get("paths", "/api/audit", "get", "responses")
	if responses == nil {
		t.Fatal("bundle has no GET /api/audit responses")
	}
	declared := map[int]bool{}
	for _, code := range responses.order {
		switch strings.Trim(code, `'"`) {
		case "200":
			declared[200] = true
		case "400":
			declared[400] = true
		case "401":
			declared[401] = true
		case "500":
			declared[500] = true
		default:
			t.Errorf("unexpected declared response %s", code)
		}
	}
	if !declared[400] {
		t.Fatal("the bundle does not declare 400 on listAuditEntries")
	}
	r400 := responses.get("'400'")
	if r400 == nil {
		r400 = responses.get("400")
	}
	media := r400.get("content")
	if media == nil || len(media.order) != 1 || media.order[0] != "application/json" {
		t.Fatalf("declared 400 media types: %+v", media)
	}
	schema := root.get("components", "schemas", refName(media.get("application/json", "schema"), "#/components/schemas/"))
	if !isErrorEnvelope(schema) {
		t.Fatal("declared 400 schema is not the {error: string} ErrorResponse")
	}

	u := createTestUser(t, "pac53-declared@example.com")
	seedAuditRows(t, u, 3)
	queries := []string{"", "?limit=1", "?limit=10", "?limit=500", "?limit=%2B10", "?limit=010"}
	for _, c := range pac53Refused {
		queries = append(queries, c.query)
	}
	n400 := 0
	for _, q := range queries {
		w := getAudit(t, u, q)
		if !declared[w.Code] {
			t.Errorf("%q: status %d is not declared on listAuditEntries", q, w.Code)
		}
		if w.Code == http.StatusBadRequest {
			n400++
			if ct := w.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("%q: 400 Content-Type %q, declared application/json", q, ct)
			}
			var body map[string]json.RawMessage
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body) != 1 {
				t.Errorf("%q: 400 body %q is not a one-key ErrorResponse", q, w.Body.String())
			}
			var msg string
			if err := json.Unmarshal(body["error"], &msg); err != nil || msg == "" {
				t.Errorf("%q: 400 `error` is not a non-empty string: %q", q, w.Body.String())
			}
		}
	}
	// Not vacuous: every refused request of AC-3..AC-6 produced a 400 above.
	if n400 != len(pac53Refused) {
		t.Errorf("%d responses were 400, want %d (one per refused request)", n400, len(pac53Refused))
	}
}

// AC-11: the generated ServerInterfaceWrapper (built from the new bundle,
// mounted only here) still refuses with 400 the cases its binder can see:
// C-2, C-3, the overflowing part of C-7 and C-8. A conforming request reaches
// the (unimplemented) handler, so the 400s are the binder's.
func TestPAC53_AC11_GeneratedBinderAgrees(t *testing.T) {
	h := gen.HandlerWithOptions(gen.Unimplemented{}, gen.ChiServerOptions{})
	serve := func(q string) int {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/audit"+q, nil))
		return w.Code
	}
	for _, q := range []string{"?limit=", "?limit=abc", "?limit=99999999999999999999", "?limit=10&limit=10"} {
		if got := serve(q); got != http.StatusBadRequest {
			t.Errorf("wrapper %q: status %d, want 400", q, got)
		}
	}
	if got := serve("?limit=10"); got != http.StatusNotImplemented {
		t.Errorf("wrapper ?limit=10: status %d, want the handler's 501", got)
	}
}

// AC-12: the contract's falsifying command for "which server answers this
// operation" returns nothing (no non-generated, non-test Go file names a
// generated ListAuditEntries symbol), and /api/audit is registered once, to
// the hand-written auditHandler.
func TestPAC53_AC12_OnlyTheHandwrittenHandlerServesIt(t *testing.T) {
	backend, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	var hits []string
	err = filepath.WalkDir(backend, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(backend, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "api/gen" || strings.HasPrefix(d.Name(), ".") && rel != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(src, []byte("ListAuditEntries")) {
			hits = append(hits, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) > 0 {
		t.Errorf("non-generated, non-test Go code names ListAuditEntries: %v", hits)
	}
	routes, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatal(err)
	}
	var regs []string
	for _, line := range strings.Split(string(routes), "\n") {
		if strings.Contains(line, `"/api/audit"`) {
			regs = append(regs, strings.TrimSpace(line))
		}
	}
	if len(regs) != 1 || regs[0] != `r.Get("/api/audit", auditHandler(db))` {
		t.Errorf("/api/audit registrations in routes.go: %q, want exactly the auditHandler one", regs)
	}
}
