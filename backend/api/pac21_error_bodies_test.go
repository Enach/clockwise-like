package api

// PAC-21: no response body carries a raw Go, driver or provider error string,
// at any status, and every 5xx is the JSON {"error": "..."} envelope. The
// detail goes to the server log and to Sentry only.

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
		// want is the fixed message of the JSON envelope. Every 5xx is JSON
		// since the second half of PAC-21; existing fixed literals are kept.
		want string
	}{
		{"personal list (writeError err.Error)", http.MethodGet, "/api/personal-calendars", "", newPersonalHandlersWithBlocker(db, &fakePersonalBlocker{}).list, "internal error"},
		{"audit (writeError prefix+err)", http.MethodGet, "/api/audit", "", auditHandler(db), "internal error"},
		{"settings get (was text/plain)", http.MethodGet, "/api/settings", "", (&settingsHandlers{db: db}).getSettings, "internal error"},
		{"focus blocks (was text/plain)", http.MethodGet, "/api/focus/blocks", "", (&focusHandlers{db: db}).listBlocks, "internal error"},
		{"teams create (was text/plain)", http.MethodPost, "/api/teams", `{"name":"x"}`, newTeamHandlersWithAvailabilityEngine(db, nil).createTeam, "internal error"},
		{"teams list (was text/plain)", http.MethodGet, "/api/teams", "", newTeamHandlersWithAvailabilityEngine(db, nil).listTeams, "internal error"},
		{"manager profile (existing literal db error, was text/plain)", http.MethodGet, "/api/manager/profile", "", (&managerHandlers{db: db}).getProfile, "db error"},
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
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
					t.Fatalf("Content-Type = %q, want application/json", ct)
				}
				var body map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatalf("body is not a JSON object: %q (%v)", rec.Body.String(), err)
				}
			if len(body) != 1 || body["error"] != tc.want {
				t.Fatalf("body = %v, want exactly {\"error\":%q}", body, tc.want)
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

// http4xx names every net/http 4xx status constant.
var http4xx = map[string]bool{
	"StatusBadRequest": true, "StatusUnauthorized": true, "StatusPaymentRequired": true,
	"StatusForbidden": true, "StatusNotFound": true, "StatusMethodNotAllowed": true,
	"StatusNotAcceptable": true, "StatusProxyAuthRequired": true, "StatusRequestTimeout": true,
	"StatusConflict": true, "StatusGone": true, "StatusLengthRequired": true,
	"StatusPreconditionFailed": true, "StatusRequestEntityTooLarge": true,
	"StatusRequestURITooLong": true, "StatusUnsupportedMediaType": true,
	"StatusRequestedRangeNotSatisfiable": true, "StatusExpectationFailed": true,
	"StatusTeapot": true, "StatusMisdirectedRequest": true, "StatusUnprocessableEntity": true,
	"StatusLocked": true, "StatusFailedDependency": true, "StatusTooEarly": true,
	"StatusUpgradeRequired": true, "StatusPreconditionRequired": true,
	"StatusTooManyRequests": true, "StatusRequestHeaderFieldsTooLarge": true,
	"StatusUnavailableForLegalReasons": true,
}

// statusKind classifies a status argument known at compile time — an integer
// literal or a net/http constant — as "4xx", "5xx" or "other", and anything
// computed at run time as "unknown".
func statusKind(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.BasicLit:
		n, err := strconv.Atoi(v.Value)
		switch {
		case err != nil:
			return "unknown"
		case n >= 500 && n <= 599:
			return "5xx"
		case n >= 400 && n <= 499:
			return "4xx"
		}
		return "other"
	case *ast.SelectorExpr:
		if pkg, ok := v.X.(*ast.Ident); ok && pkg.Name == "http" {
			switch {
			case http5xx[v.Sel.Name]:
				return "5xx"
			case http4xx[v.Sel.Name]:
				return "4xx"
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

func isStringLit(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	return ok && lit.Kind == token.STRING
}

// scanExemption names one fixed-sentence validator call site the scan allows
// (AC-1, revision 4): in File, inside the function or method Func, a
// writeError(w, err.Error(), <a 4xx status>) whose err is the result of a call
// to Validator — the nearest assignment to that err before the write. The same
// shape anywhere else, at any other status, or with err from anything else is
// a violation. Adding an entry requires showing that Validator returns only
// fixed sentences (TestPAC21ExemptedValidatorsReturnFixedSentences).
type scanExemption struct {
	File, Func, Validator string
}

// pac21Exemptions is the complete table: exactly the ten sites the contract
// names (NEW-pac21-error-leaks-remaining AC-1 and §5).
var pac21Exemptions = []scanExemption{
	{"handlers_habits.go", "create", "validateHabitFields"},
	{"handlers_habits.go", "update", "validateHabitFields"},
	{"handlers_manager.go", "confirmDetection", "normalizeManagerEmail"},
	{"handlers_manager.go", "addMember", "normalizeManagerEmail"},
	{"handlers_manager.go", "addMember", "validateManagerCadence"},
	{"handlers_manager.go", "patchMember", "validateManagerCadence"},
	{"handlers_teams.go", "inviteMember", "normalizeManagerEmail"},
	{"handlers_teams.go", "createZone", "validateNoMeetingZone"},
	{"handlers_teams.go", "updateZone", "validateNoMeetingZone"},
	{"handlers_settings.go", "patchSettings", "validateChangedSettings"},
}

// errSource returns the name of the function whose call last assigned the
// identifier name before pos inside fn, or "" when that assignment is not a
// call (or there is none).
func errSource(fn *ast.FuncDecl, name string, pos token.Pos) string {
	var last *ast.AssignStmt
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || as.Pos() >= pos {
			return true
		}
		for _, lhs := range as.Lhs {
			if id, ok := lhs.(*ast.Ident); ok && id.Name == name {
				if last == nil || as.Pos() > last.Pos() {
					last = as
				}
			}
		}
		return true
	})
	if last == nil || len(last.Rhs) != 1 {
		return ""
	}
	call, ok := last.Rhs[0].(*ast.CallExpr)
	if !ok {
		return ""
	}
	switch f := call.Fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

// exempted reports whether call — writeError(w, err.Error(), status) inside
// fn in file — is one of the named validator sites.
func exempted(exemptions []scanExemption, file string, fn *ast.FuncDecl, call *ast.CallExpr) bool {
	if fn == nil || statusKind(call.Args[2]) != "4xx" {
		return false
	}
	errCall, ok := call.Args[1].(*ast.CallExpr)
	if !ok || len(errCall.Args) != 0 {
		return false
	}
	sel, ok := errCall.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Error" {
		return false
	}
	errIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	source := errSource(fn, errIdent.Name, call.Pos())
	for _, ex := range exemptions {
		if ex.File == file && ex.Func == fn.Name.Name && ex.Validator == source {
			return true
		}
	}
	return false
}

// scanErrorBodies returns one violation per response write that can put an
// error's text into a body at any status, or that writes a 5xx (or a status
// computed at run time) as anything but the JSON envelope.
func scanErrorBodies(fset *token.FileSet, file *ast.File, exemptions []scanExemption) []string {
	var out []string
	for _, decl := range file.Decls {
		ast.Inspect(decl, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
			if !ok {
			return true
		}
		pos := fset.Position(call.Pos())
			base := filepath.Base(pos.Filename)
			where := fmt.Sprintf("%s:%d", base, pos.Line)
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "writeServerErrorText" {
				out = append(out, where+": writeServerErrorText writes a text/plain 5xx; use writeServerError (JSON)")
				return true
			}
			if len(call.Args) != 3 {
				return true
			}
		msg, status := call.Args[1], call.Args[2]
		switch fn := call.Fun.(type) {
		case *ast.SelectorExpr: // http.Error
				pkg, ok := fn.X.(*ast.Ident)
				if !ok || pkg.Name != "http" || fn.Sel.Name != "Error" {
					return true
				}
				switch kind := statusKind(status); {
				case kind == "5xx" || kind == "unknown":
					out = append(out, where+": http.Error on a 5xx (or computed) status writes text/plain; use writeServerError or writeError")
				case carriesError(msg):
					out = append(out, where+": http.Error with an error-derived message; write a fixed message")
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
				default:
					if carriesError(msg) && !exempted(exemptions, base, fnDecl(decl), call) {
						out = append(out, where+": writeError with an error-derived message; write a fixed message")
				}
			}
		}
		return true
	})
	}
	return out
}

func fnDecl(d ast.Decl) *ast.FuncDecl {
	fn, _ := d.(*ast.FuncDecl)
	return fn
}

func parseAPISources(t *testing.T) (*token.FileSet, map[string]*ast.File) {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	out := map[string]*ast.File{}
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
		out[name] = f
	}
	return fset, out
}

// AC-1 and AC-3, source half: no response write in backend/api can echo an
// error's text at any status, and no 5xx is written as text/plain.
func TestPAC21NoRawErrorsInResponseBodies(t *testing.T) {
	fset, files := parseAPISources(t)
	var violations []string
	for _, f := range files {
		violations = append(violations, scanErrorBodies(fset, f, pac21Exemptions)...)
	}
	sort.Strings(violations)
	if len(violations) > 0 {
		t.Fatalf("%d response write(s) can echo an error into a body, or write a non-JSON 5xx (PAC-21):\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}

// Every exemption must still match a real write, so the table cannot keep an
// entry for a site that has gone (and that a later, different write could
// silently inherit).
func TestPAC21EveryExemptionMatchesASite(t *testing.T) {
	_, files := parseAPISources(t)
	used := map[scanExemption]int{}
	for name, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) != 3 {
					return true
				}
				if id, ok := call.Fun.(*ast.Ident); !ok || id.Name != "writeError" {
					return true
				}
				for _, ex := range pac21Exemptions {
					if exempted([]scanExemption{ex}, name, fn, call) {
						used[ex]++
					}
				}
				return true
			})
		}
	}
	for _, ex := range pac21Exemptions {
		if used[ex] == 0 {
			t.Errorf("exemption %+v matches no write in backend/api", ex)
		}
	}
}

// AC-1: the four fmt.Errorf validators the scan exempts return only fixed
// sentences — every non-nil error they return is fmt.Errorf or errors.New
// with a single string literal and nothing else.
func TestPAC21ExemptedValidatorsReturnFixedSentences(t *testing.T) {
	_, files := parseAPISources(t)
	want := map[string]bool{"validateHabitFields": false, "normalizeManagerEmail": false, "validateManagerCadence": false, "validateNoMeetingZone": false}
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if _, tracked := want[fn.Name.Name]; !tracked || fn.Recv != nil {
				continue
			}
			want[fn.Name.Name] = true
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if _, ok := n.(*ast.FuncLit); ok {
					return false
				}
				ret, ok := n.(*ast.ReturnStmt)
				if !ok || len(ret.Results) == 0 {
					return true
				}
				last := ret.Results[len(ret.Results)-1]
				if id, ok := last.(*ast.Ident); ok && id.Name == "nil" {
					return true
				}
				call, ok := last.(*ast.CallExpr)
				fixed := false
				if ok && len(call.Args) == 1 && isStringLit(call.Args[0]) {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
						if pkg, ok := sel.X.(*ast.Ident); ok {
							fixed = (pkg.Name == "fmt" && sel.Sel.Name == "Errorf") || (pkg.Name == "errors" && sel.Sel.Name == "New")
						}
					}
				}
				if !fixed {
					t.Errorf("%s returns an error that is not a single fixed string literal", fn.Name.Name)
				}
				return true
			})
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("validator %s not found in backend/api", name)
		}
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
	http.Error(w, "not connected: "+err.Error(), http.StatusUnauthorized)
	writeError(w, "event not found: "+err.Error(), 404)
	writeServerErrorText(w, r, "op", err, http.StatusInternalServerError)
}
func exempted(w http.ResponseWriter, raw []byte) {
	if err := validateHabitFields(raw); err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
	}
	if err := validateHabitFields(raw); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
	}
	_ = validateHabitFields(raw)
	err := json.Unmarshal(raw, &x)
	writeError(w, err.Error(), http.StatusBadRequest)
}
func notExempted(w http.ResponseWriter, raw []byte) {
	if err := validateHabitFields(raw); err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
	}
}`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "fixture.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := scanErrorBodies(fset, f, []scanExemption{{"fixture.go", "exempted", "validateHabitFields"}})
	// Allowed: 12, 14-17, 20 and 27 (the exempted validator at a 4xx).
	wantLines := []int{3, 4, 5, 6, 7, 8, 9, 10, 13, 18, 19, 21, 22, 23, 30, 34, 38}
	if len(got) != len(wantLines) {
		t.Fatalf("got %d violations, want %d:\n%s", len(got), len(wantLines), strings.Join(got, "\n"))
	}
	for i, line := range wantLines {
		if !strings.HasPrefix(got[i], fmt.Sprintf("fixture.go:%d:", line)) {
			t.Fatalf("violation %d = %q, want line %d\nall:\n%s", i, got[i], line, strings.Join(got, "\n"))
		}
	}
}

// The out-of-hours refusal is a fixed policy message the contract documents as
// the 500 body of POST /api/schedule/create and /api/nlp/confirm (API-036
// tracks its status code). It is not a raw error and is kept, now as the JSON
// envelope; any other CreateMeeting failure is the generic body.
func TestPAC21CreateMeetingFailureBodies(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{fmt.Errorf("wrapped: %w", engine.ErrOutOfHoursAllowance), "out-of-hours meeting allowance reached"},
		{errors.New(`googleapi: Error 403: Request had insufficient authentication scopes., insufficientPermissions`), "internal error"},
	}
	check := func(t *testing.T, path string, rec *httptest.ResponseRecorder, want string) {
		t.Helper()
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s: status %d, want 500; body %q", path, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("%s: Content-Type = %q, want application/json", path, ct)
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body) != 1 || body["error"] != want {
			t.Fatalf("%s: body %q, want exactly {\"error\":%q}", path, rec.Body.String(), want)
		}
	}
	for _, tc := range cases {
		_, _ = captureServerErrors(t)
		h := &scheduleHandlers{eng: &mockCompressor{}, smart: &mockScheduler{createErr: tc.err}, db: openTestDB(t)}
		body := `{"title":"T","start":"2030-01-01T10:00:00Z","end":"2030-01-01T11:00:00Z"}`
		rec := httptest.NewRecorder()
		h.createMeeting(rec, httptest.NewRequest(http.MethodPost, "/api/schedule/create", strings.NewReader(body)))
		check(t, "schedule create", rec, tc.want)

		rec = httptest.NewRecorder()
		writeCreateMeetingFailure(rec, httptest.NewRequest(http.MethodPost, "/api/nlp/confirm", nil), "confirm", tc.err)
		check(t, "nlp confirm", rec, tc.want)
	}
}
