package api

// PAC-21: a 5xx body never carries a raw Go, driver or provider error string.
// The detail goes to the server log and to Sentry; the client gets a fixed
// JSON {"error": "..."} envelope.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/engine"
	"github.com/google/uuid"
)

// closedTestDB returns a *sql.DB whose every query fails with the driver's
// "sql: database is closed" — a forced storage error with a recognisable raw
// string that must never reach a response body.
func closedTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", "postgres://pac21:pac21@127.0.0.1:1/pac21?sslmode=disable")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = db.Close()
	return db
}

// captureServerErrors swaps the Sentry reporter and the std logger for the
// duration of the test and returns what each received.
func captureServerErrors(t *testing.T) (reported *[]error, logs *bytes.Buffer) {
	t.Helper()
	var got []error
	prevReport := reportServerError
	reportServerError = func(_ *http.Request, err error) { got = append(got, err) }
	buf := &bytes.Buffer{}
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(buf)
	t.Cleanup(func() {
		reportServerError = prevReport
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})
	return &got, buf
}

func TestPAC21ForcedStorageErrorReturnsGenericJSON(t *testing.T) {
	db := closedTestDB(t)
	cases := []struct {
		name    string
		method  string
		target  string
		body    string
		handler http.HandlerFunc
		// json: the contract documents the JSON envelope for this 500;
		// otherwise it documents text/plain, which is kept (see server_errors.go).
		json bool
	}{
		{"personal list (writeError err.Error)", http.MethodGet, "/api/personal-calendars", "", newPersonalHandlersWithBlocker(db, &fakePersonalBlocker{}).list, true},
		{"audit (writeError prefix+err)", http.MethodGet, "/api/audit", "", auditHandler(db), true},
		{"settings get (http.Error err.Error)", http.MethodGet, "/api/settings", "", (&settingsHandlers{db: db}).getSettings, false},
		{"focus blocks (http.Error err.Error)", http.MethodGet, "/api/focus/blocks", "", (&focusHandlers{db: db}).listBlocks, false},
		{"teams create (http.Error err.Error)", http.MethodPost, "/api/teams", `{"name":"x"}`, newTeamHandlersWithAvailabilityEngine(db, nil).createTeam, false},
		{"teams list (http.Error err.Error)", http.MethodGet, "/api/teams", "", newTeamHandlersWithAvailabilityEngine(db, nil).listTeams, false},
		{"manager profile (http.Error \"db error\", detail was dropped)", http.MethodGet, "/api/manager/profile", "", (&managerHandlers{db: db}).getProfile, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reported, logs := captureServerErrors(t)
			req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			req = req.WithContext(context.WithValue(req.Context(), auth.UserIDKey, uuid.New()))
			rec := httptest.NewRecorder()
			tc.handler(rec, req)

			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500; body %q", rec.Code, rec.Body.String())
			}
			ct := rec.Header().Get("Content-Type")
			if tc.json {
				if !strings.HasPrefix(ct, "application/json") {
					t.Fatalf("Content-Type = %q, want application/json", ct)
				}
				var body map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatalf("body is not a JSON object: %q (%v)", rec.Body.String(), err)
				}
				if len(body) != 1 || body["error"] != "internal error" {
					t.Fatalf("body = %v, want exactly {\"error\":\"internal error\"}", body)
				}
			} else {
				if !strings.HasPrefix(ct, "text/plain") {
					t.Fatalf("Content-Type = %q, want the documented text/plain", ct)
				}
				if got := strings.TrimSpace(rec.Body.String()); got != "internal error" && got != "db error" {
					t.Fatalf("body = %q, want a fixed message", got)
				}
			}
			if strings.Contains(rec.Body.String(), "closed") || strings.Contains(rec.Body.String(), "sql") {
				t.Fatalf("body leaks the storage error: %q", rec.Body.String())
			}
			if !strings.Contains(logs.String(), "database is closed") {
				t.Fatalf("server log does not carry the detail; log = %q", logs.String())
			}
			if len(*reported) != 1 || !strings.Contains((*reported)[0].Error(), "database is closed") {
				t.Fatalf("Sentry did not receive the detail; reported = %v", *reported)
			}
		})
	}
}

func TestPAC21WriteServerErrorGenericPerStatus(t *testing.T) {
	cases := map[int]string{
		http.StatusInternalServerError: "internal error",
		http.StatusBadGateway:          "upstream provider error",
		http.StatusServiceUnavailable:  "service unavailable",
		http.StatusGatewayTimeout:      "internal error",
	}
	for code, want := range cases {
		reported, logs := captureServerErrors(t)
		req := httptest.NewRequest(http.MethodGet, "/x?code=secret-oauth-code", nil)
		rec := httptest.NewRecorder()
		writeServerError(rec, req, "token exchange failed", errors.New("oauth2: invalid_grant user 1234"), code)
		if rec.Code != code {
			t.Fatalf("code %d: status = %d", code, rec.Code)
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] != want || len(body) != 1 {
			t.Fatalf("code %d: body = %q, want {\"error\":%q}", code, rec.Body.String(), want)
		}
		if !strings.Contains(logs.String(), "token exchange failed: oauth2: invalid_grant user 1234") {
			t.Fatalf("code %d: log = %q", code, logs.String())
		}
		if strings.Contains(logs.String(), "secret-oauth-code") {
			t.Fatalf("code %d: log carries the query string: %q", code, logs.String())
		}
		if len(*reported) != 1 || !strings.Contains((*reported)[0].Error(), "token exchange failed: oauth2: invalid_grant") {
			t.Fatalf("code %d: reported = %v", code, *reported)
		}
	}
}

func TestPAC21WriteServerErrorTextKeepsPlainContentType(t *testing.T) {
	reported, logs := captureServerErrors(t)
	rec := httptest.NewRecorder()
	writeServerErrorText(rec, httptest.NewRequest(http.MethodGet, "/api/settings", nil), "getSettings", errors.New("pq: relation \"settings\" does not exist"), http.StatusInternalServerError)
	if rec.Code != http.StatusInternalServerError || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("status %d, Content-Type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if strings.TrimSpace(rec.Body.String()) != "internal error" {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if !strings.Contains(logs.String(), `getSettings: pq: relation "settings" does not exist`) || len(*reported) != 1 {
		t.Fatalf("log = %q, reported = %v", logs.String(), *reported)
	}
}

func TestPAC21WriteServerErrorNilErrStillReports(t *testing.T) {
	reported, _ := captureServerErrors(t)
	rec := httptest.NewRecorder()
	writeServerError(rec, httptest.NewRequest(http.MethodGet, "/x", nil), "user not found", nil, http.StatusInternalServerError)
	if rec.Code != http.StatusInternalServerError || len(*reported) != 1 || (*reported)[0].Error() != "user not found" {
		t.Fatalf("status %d, reported %v", rec.Code, *reported)
	}
}

func TestPAC21DefaultReporterDoesNotPanicWithoutSentry(t *testing.T) {
	reportServerError(httptest.NewRequest(http.MethodGet, "/x", nil), errors.New("boom"))
}

// --- the source guard -------------------------------------------------------

// http5xx names every net/http 5xx status constant.
var http5xx = map[string]bool{
	"StatusInternalServerError": true, "StatusNotImplemented": true,
	"StatusBadGateway": true, "StatusServiceUnavailable": true,
	"StatusGatewayTimeout": true, "StatusHTTPVersionNotSupported": true,
	"StatusVariantAlsoNegotiates": true, "StatusInsufficientStorage": true,
	"StatusLoopDetected": true, "StatusNotExtended": true,
	"StatusNetworkAuthenticationRequired": true,
}

// statusKind classifies a status argument: "5xx", "other" (a known non-5xx
// constant) or "unknown" (computed at run time).
func statusKind(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.BasicLit:
		if n, err := strconv.Atoi(v.Value); err == nil && n >= 500 && n <= 599 {
			return "5xx"
		}
		return "other"
	case *ast.SelectorExpr:
		if pkg, ok := v.X.(*ast.Ident); ok && pkg.Name == "http" {
			if http5xx[v.Sel.Name] {
				return "5xx"
			}
			return "other"
		}
	}
	return "unknown"
}

// carriesError reports whether e may embed an error's text: an err.Error()
// call, a fmt.Sprint*/Errorf call, or a bare identifier named like an error
// (a field selected off one, such as validationErr.Msg, does not count).
func carriesError(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.SelectorExpr:
			// A field of an error value (validationErr.Msg) is not its text;
			// only a call to .Error() is, and that is matched below.
			if _, ok := v.X.(*ast.Ident); ok && v.Sel.Name != "Error" {
				return false
			}
		case *ast.CallExpr:
			if sel, ok := v.Fun.(*ast.SelectorExpr); ok {
				if sel.Sel.Name == "Error" && len(v.Args) == 0 {
					found = true
				}
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "fmt" {
					found = true
				}
			}
		case *ast.Ident:
			if v.Name == "err" || strings.HasSuffix(v.Name, "Err") {
				found = true
			}
		}
		return !found
	})
	return found
}

// isGenericMessage matches genericServerMessage(...), the helpers' own body.
func isGenericMessage(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	id, ok := call.Fun.(*ast.Ident)
	return ok && id.Name == "genericServerMessage"
}

func isStringLit(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	return ok && lit.Kind == token.STRING
}

// scanErrorBodies returns one violation per response write that can put an
// error's text, or a non-JSON body, into a 5xx response.
func scanErrorBodies(fset *token.FileSet, file *ast.File) []string {
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 3 {
			return true
		}
		pos := fset.Position(call.Pos())
		where := fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line)
		msg, status := call.Args[1], call.Args[2]
		switch fn := call.Fun.(type) {
		case *ast.SelectorExpr: // http.Error
			if pkg, ok := fn.X.(*ast.Ident); ok && pkg.Name == "http" && fn.Sel.Name == "Error" {
				if statusKind(status) != "other" && !isStringLit(msg) && !isGenericMessage(msg) {
					out = append(out, where+": http.Error on a 5xx (or computed) status with a non-literal message; use writeServerErrorText")
				}
			}
		case *ast.Ident: // writeError
			if fn.Name != "writeError" {
				return true
			}
			switch statusKind(status) {
			case "5xx":
				if !isStringLit(msg) {
					out = append(out, where+": writeError on a 5xx with a non-literal message; use writeServerError")
				}
			case "unknown":
				if carriesError(msg) {
					out = append(out, where+": writeError with a computed status and an error-derived message")
				}
			}
		}
		return true
	})
	return out
}

func TestPAC21NoRawErrorsIn5xxBodies(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var violations []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		violations = append(violations, scanErrorBodies(fset, f)...)
	}
	sort.Strings(violations)
	if len(violations) > 0 {
		t.Fatalf("%d response write(s) can echo an error into a 5xx body (PAC-21):\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}

// TestPAC21GuardCatchesKnownBadPatterns proves the guard fails on the shapes it
// exists to stop, and stays quiet on the allowed ones.
func TestPAC21GuardCatchesKnownBadPatterns(t *testing.T) {
	src := `package api
func f(w http.ResponseWriter, r *http.Request, err error, e *httpError) {
	writeError(w, err.Error(), http.StatusInternalServerError)
	writeError(w, "update failed: "+err.Error(), 500)
	writeError(w, redact(err.Error()), http.StatusBadGateway)
	writeError(w, fmt.Sprintf("x %v", err), http.StatusServiceUnavailable)
	http.Error(w, err.Error(), http.StatusInternalServerError)
	http.Error(w, "send error: "+err.Error(), http.StatusBadGateway)
	writeError(w, err.Error(), code)
	http.Error(w, err.Error(), code)

	writeError(w, "internal error", http.StatusInternalServerError)
	writeError(w, err.Error(), http.StatusBadRequest)
	writeError(w, e.Msg, e.Code)
	writeError(w, validationErr.Msg, validationErr.Code)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
	writeServerError(w, r, "update failed", err, http.StatusInternalServerError)
	http.Error(w, "db error", http.StatusInternalServerError)
	http.Error(w, genericServerMessage(code), code)
	writeError(w, genericServerMessage(code), code)
}`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "fixture.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := scanErrorBodies(fset, f)
	wantLines := []int{3, 4, 5, 6, 7, 8, 9, 10}
	if len(got) != len(wantLines) {
		t.Fatalf("got %d violations, want %d:\n%s", len(got), len(wantLines), strings.Join(got, "\n"))
	}
	for i, line := range wantLines {
		if !strings.HasPrefix(got[i], fmt.Sprintf("fixture.go:%d:", line)) {
			t.Fatalf("violation %d = %q, want line %d", i, got[i], line)
		}
	}
}

// The out-of-hours refusal is a fixed policy message the contract documents as
// the 500 body of POST /api/schedule/create (API-036 tracks its status code).
// It is not a raw error and is kept; any other CreateMeeting failure is not.
func TestPAC21CreateMeetingKeepsPolicyRefusalTextOnly(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{fmt.Errorf("wrapped: %w", engine.ErrOutOfHoursAllowance), "out-of-hours meeting allowance reached"},
		{errors.New(`googleapi: Error 403: Request had insufficient authentication scopes., insufficientPermissions`), "internal error"},
	}
	for _, tc := range cases {
		_, _ = captureServerErrors(t)
		h := &scheduleHandlers{eng: &mockCompressor{}, smart: &mockScheduler{createErr: tc.err}, db: openTestDB(t)}
		body := `{"title":"T","start":"2030-01-01T10:00:00Z","end":"2030-01-01T11:00:00Z"}`
		rec := httptest.NewRecorder()
		h.createMeeting(rec, httptest.NewRequest(http.MethodPost, "/api/schedule/create", strings.NewReader(body)))
		if rec.Code != http.StatusInternalServerError || strings.TrimSpace(rec.Body.String()) != tc.want {
			t.Fatalf("err %v: status %d body %q, want 500 %q", tc.err, rec.Code, rec.Body.String(), tc.want)
		}

		rec = httptest.NewRecorder()
		writeCreateMeetingFailure(rec, httptest.NewRequest(http.MethodPost, "/api/nlp/confirm", nil), "confirm", tc.err)
		if strings.TrimSpace(rec.Body.String()) != tc.want {
			t.Fatalf("nlp confirm path, err %v: body %q, want %q", tc.err, rec.Body.String(), tc.want)
		}
	}
}
