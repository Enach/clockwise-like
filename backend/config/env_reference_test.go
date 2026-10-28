package config

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// PAC-81 Part D (spec NEW-deploy-hardening AC-D1, AC-D6, AC-D7). The
// environment reference is .env.example: every variable the backend, the MCP
// server and the file server read is documented there with a status, a
// default and a purpose, and every variable it documents is actually used.
// Files are read from the repository root, which scripts/test-backend.sh
// mounts (backend/config is the working directory, as for
// deployment_files_test.go).

const repoRoot = "../.."

// serviceSourceDirs are the Go programs whose environment the reference covers.
var serviceSourceDirs = []string{"backend", "mcp", "docker/fileserver"}

// serviceShellScripts are the shell entrypoints whose environment the
// reference covers: nginx reads its settings through nginx/paceday-render.sh
// (PAC-81 Part B).
var serviceShellScripts = []string{"nginx/paceday-render.sh"}

var (
	shellVarRef    = regexp.MustCompile(`\$\{?([A-Z][A-Z0-9_]*)`)
	shellAssignVar = regexp.MustCompile(`^\s*(?:readonly\s+|export\s+)?([A-Z][A-Z0-9_]*)=`)
)

// collectShellEnvReads returns the upper-case variables a POSIX shell script
// reads from its environment: every $NAME / ${NAME…} outside comments, minus
// the names the script assigns itself.
func collectShellEnvReads(name, text string) map[string]string {
	assigned := map[string]bool{}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for _, line := range lines {
		if m := shellAssignVar.FindStringSubmatch(line); m != nil {
			assigned[m[1]] = true
		}
	}
	out := map[string]string{}
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		for _, m := range shellVarRef.FindAllStringSubmatch(line, -1) {
			if !assigned[m[1]] {
				if _, seen := out[m[1]]; !seen {
					out[m[1]] = fmt.Sprintf("%s:%d", name, i+1)
				}
			}
		}
	}
	return out
}

// composeFiles are the files that may consume a variable themselves.
var composeFiles = []string{"docker-compose.yml", "docker-compose.prod.yml", "docker-compose.dev.yml", "e2e/docker-compose.e2e.yml"}

// envNotInReference lists variables a service reads that are deliberately not
// in .env.example, each with the reason. Nothing else may be missing.
var envNotInReference = map[string]string{
	// Read only by backend/internal/testdb (the test suite's database), never
	// by a running service.
	"PACEDAY_TEST_DATABASE_URL": "test-only: the backend test suite's database",
	// NEW-llm-bring-your-own retired this variable; the backend reads it only
	// to log that it is no longer used (config.DecideLLMUserDestinations).
	// .env.example describes it in its UPGRADE NOTE without naming it, because
	// deployment_files_test.go (AC-20 of that spec) forbids the provider's
	// name in the deployment files.
	"PACEDAY_LLM_APPROVED_ORIGINS_OLLAMA": "retired: read only to warn at startup",
}

// dynamicEnvReaders are the functions allowed to read a variable whose name
// is not a literal or an Env* constant at the call. Their callers pass string
// literals (counted below) or Env* constants (always counted).
var dynamicEnvReaders = map[string]bool{
	"oauthProviderAvailability": true, // backend/api/integration_availability.go
	"DecideLLMApprovedHosts":    true, // backend/config/llm_hosts.go, over llmApprovedHostsVars
	"DecideLLMUserDestinations": true, // backend/config/llm_user_destinations.go, over Env* consts
}

// notEnvGetters are parameters shaped like os.Getenv that are not
// environment getters, as "Function.param", each with what it is. Any other
// func(string) string parameter is treated as an environment read.
var notEnvGetters = map[string]string{
	"writeProviderFailure.redact": "backend/api/handlers_llm.go: removes secrets from provider error text",
	"extractAttendees.redact":     "backend/nlp/parser.go: removes secrets from model output",
}

var envNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

type envReads struct {
	vars    map[string][]string // name -> positions
	dynamic []string            // positions of reads outside dynamicEnvReaders
}

// collectEnvReads parses Go source files and returns every environment
// variable they read: os.Getenv / os.LookupEnv / any func(string) string (or
// func(string) (string, bool)) parameter, whatever its name, with
// a literal or an Env* constant argument, every Env* string constant, and the
// string-literal arguments of calls to the dynamicEnvReaders.
func collectEnvReads(fset *token.FileSet, files []*ast.File) envReads {
	out := envReads{vars: map[string][]string{}}
	add := func(name string, pos token.Pos) {
		out.vars[name] = append(out.vars[name], fset.Position(pos).String())
	}
	consts := map[string]string{}
	for _, f := range files {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, n := range vs.Names {
					if !strings.HasPrefix(n.Name, "Env") || i >= len(vs.Values) {
						continue
					}
					if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						v, _ := strconv.Unquote(lit.Value)
						if envNamePattern.MatchString(v) {
							consts[n.Name] = v
							add(v, n.Pos())
						}
					}
				}
			}
		}
	}
	for _, f := range files {
		fnName := ""
		getters := map[string]bool{} // func(string) string parameters in the current declaration
		ast.Inspect(f, func(n ast.Node) bool {
			switch d := n.(type) {
			case *ast.FuncDecl:
				fnName, getters = d.Name.Name, map[string]bool{}
				addGetterParams(getters, d.Type)
			case *ast.FuncLit:
				addGetterParams(getters, d.Type)
			}
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			fn := calleeName(call.Fun)
			if dynamicEnvReaders[fn] {
				for _, a := range call.Args {
					if lit, ok := a.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if v, _ := strconv.Unquote(lit.Value); envNamePattern.MatchString(v) {
							add(v, lit.Pos())
						}
					}
				}
			}
			viaOS := isOSEnvRead(call.Fun)
			ident, isIdent := call.Fun.(*ast.Ident)
			viaGetter := isIdent && getters[ident.Name] && notEnvGetters[fnName+"."+ident.Name] == ""
			if (!viaOS && !viaGetter) || len(call.Args) != 1 {
				return true
			}
			switch a := call.Args[0].(type) {
			case *ast.BasicLit:
				if v, err := strconv.Unquote(a.Value); err == nil {
					if viaOS || envNamePattern.MatchString(v) {
						add(v, a.Pos())
					}
					return true
				}
			case *ast.Ident:
				if v, ok := consts[a.Name]; ok {
					add(v, a.Pos())
					return true
				}
			case *ast.SelectorExpr:
				if v, ok := consts[a.Sel.Name]; ok {
					add(v, a.Pos())
					return true
				}
			}
			if !dynamicEnvReaders[fnName] {
				out.dynamic = append(out.dynamic, fmt.Sprintf("%s (in %s)", fset.Position(call.Pos()), fnName))
			}
			return true
		})
	}
	return out
}

func calleeName(e ast.Expr) string {
	switch f := e.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

func isOSEnvRead(e ast.Expr) bool {
	f, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := f.X.(*ast.Ident)
	return ok && pkg.Name == "os" && (f.Sel.Name == "Getenv" || f.Sel.Name == "LookupEnv")
}

// addGetterParams records the parameters shaped like os.Getenv or
// os.LookupEnv — func(string) string or func(string) (string, bool) —
// whatever they are named: a call through one is an environment read.
func addGetterParams(getters map[string]bool, ft *ast.FuncType) {
	if ft == nil || ft.Params == nil {
		return
	}
	for _, field := range ft.Params.List {
		fnType, ok := field.Type.(*ast.FuncType)
		if !ok || !isGetterShape(fnType) {
			continue
		}
		for _, name := range field.Names {
			getters[name.Name] = true
		}
	}
}

func isGetterShape(ft *ast.FuncType) bool {
	isIdent := func(e ast.Expr, name string) bool {
		id, ok := e.(*ast.Ident)
		return ok && id.Name == name
	}
	types := func(fl *ast.FieldList) []ast.Expr {
		var out []ast.Expr
		if fl == nil {
			return out
		}
		for _, f := range fl.List {
			n := len(f.Names)
			if n == 0 {
				n = 1
			}
			for i := 0; i < n; i++ {
				out = append(out, f.Type)
			}
		}
		return out
	}
	in, res := types(ft.Params), types(ft.Results)
	if len(in) != 1 || !isIdent(in[0], "string") || len(res) == 0 || !isIdent(res[0], "string") {
		return false
	}
	return len(res) == 1 || (len(res) == 2 && isIdent(res[1], "bool"))
}

// parseServiceSources parses every non-test .go file under the service dirs.
func parseServiceSources(t *testing.T) (*token.FileSet, []*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	var files []*ast.File
	for _, dir := range serviceSourceDirs {
		root := filepath.Join(repoRoot, dir)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if name := d.Name(); name == "node_modules" || name == "vendor" || strings.HasPrefix(name, ".") && path != root {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			files = append(files, f)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	if len(files) == 0 {
		t.Fatal("no Go source found; is the repository root mounted?")
	}
	return fset, files
}

type envDoc struct {
	status, def, purpose string
	line                 int
}

var (
	docLinePattern    = regexp.MustCompile(`^#: ([A-Z][A-Z0-9_]*) \| (required|optional|test-only|deprecated) \| default: ([^|]+?) \| (.+)$`)
	assignLinePattern = regexp.MustCompile(`^([A-Z][A-Z0-9_]*)=`)
)

// parseEnvReference reads the "#: NAME | status | default: … | purpose" lines
// and the NAME= assignments of an .env.example text. Malformed "#:" lines and
// duplicates are returned as problems.
func parseEnvReference(text string) (docs map[string]envDoc, assigned map[string]int, problems []string) {
	docs, assigned = map[string]envDoc{}, map[string]int{}
	for i, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		n := i + 1
		if strings.HasPrefix(line, "#:") {
			m := docLinePattern.FindStringSubmatch(strings.TrimRight(line, " "))
			if m == nil {
				problems = append(problems, fmt.Sprintf("line %d: malformed reference entry: %s", n, line))
				continue
			}
			if _, dup := docs[m[1]]; dup {
				problems = append(problems, fmt.Sprintf("line %d: %s documented twice", n, m[1]))
			}
			docs[m[1]] = envDoc{status: m[2], def: strings.TrimSpace(m[3]), purpose: strings.TrimSpace(m[4]), line: n}
			continue
		}
		if m := assignLinePattern.FindStringSubmatch(line); m != nil {
			if _, dup := assigned[m[1]]; dup {
				problems = append(problems, fmt.Sprintf("line %d: %s assigned twice", n, m[1]))
			}
			assigned[m[1]] = n
		}
	}
	return docs, assigned, problems
}

// composeUses returns the variables a compose text interpolates (${NAME…})
// and, separately, those it only passes through to a service under the same
// name (NAME: ${NAME…} or - NAME=${NAME…}).
func composeUses(text string) (used, passthroughOnly map[string]bool) {
	used, passthrough, other := map[string]bool{}, map[string]bool{}, map[string]bool{}
	interp := regexp.MustCompile(`\$\{([A-Z][A-Z0-9_]*)`)
	pass := regexp.MustCompile(`^\s*-?\s*([A-Z][A-Z0-9_]*)\s*[:=]\s*\$\{([A-Z][A-Z0-9_]*)(:?-[^}]*)?\}\s*(#.*)?$`)
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		ms := interp.FindAllStringSubmatch(line, -1)
		if len(ms) == 0 {
			continue
		}
		p := pass.FindStringSubmatch(line)
		for _, m := range ms {
			used[m[1]] = true
			if p != nil && p[1] == m[1] && p[2] == m[1] {
				passthrough[m[1]] = true
			} else {
				other[m[1]] = true
			}
		}
	}
	passthroughOnly = map[string]bool{}
	for v := range passthrough {
		if !other[v] {
			passthroughOnly[v] = true
		}
	}
	return used, passthroughOnly
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(raw)
}

// AC-D1: every variable read is documented; every documented variable is used.
func TestEnvReferenceIsComplete(t *testing.T) {
	fset, files := parseServiceSources(t)
	reads := collectEnvReads(fset, files)
	for _, script := range serviceShellScripts {
		for name, pos := range collectShellEnvReads(script, readRepoFile(t, script)) {
			reads.vars[name] = append(reads.vars[name], pos)
		}
	}
	for _, d := range reads.dynamic {
		t.Errorf("environment read with a computed name outside the reviewed readers %v: %s — read a literal or an Env* constant, or add the function to dynamicEnvReaders with a reason", keys(dynamicEnvReaders), d)
	}

	docs, assigned, problems := parseEnvReference(readRepoFile(t, ".env.example"))
	for _, p := range problems {
		t.Errorf(".env.example %s", p)
	}

	composeUsed, composePassOnly := map[string]bool{}, map[string]bool{}
	composeOther := map[string]bool{}
	for _, cf := range composeFiles {
		used, passOnly := composeUses(readRepoFile(t, cf))
		for v := range used {
			composeUsed[v] = true
			if !passOnly[v] {
				composeOther[v] = true
			}
		}
		for v := range passOnly {
			composePassOnly[v] = true
		}
	}

	for _, name := range keys(reads.vars) {
		if _, ok := docs[name]; ok {
			continue
		}
		if _, ok := envNotInReference[name]; ok {
			continue
		}
		t.Errorf("%s is read (%s) but not documented in .env.example (add a \"#: %s | status | default: … | purpose\" line)", name, reads.vars[name][0], name)
	}
	for _, name := range keys(envNotInReference) {
		if _, ok := reads.vars[name]; !ok {
			t.Errorf("envNotInReference lists %s, which no service reads any more; remove it", name)
		}
	}

	for name, line := range assigned {
		if _, ok := docs[name]; !ok {
			t.Errorf(".env.example:%d assigns %s without a reference entry", line, name)
		}
	}
	for _, name := range keys(docs) {
		d := docs[name]
		readByService := len(reads.vars[name]) > 0
		switch {
		case d.status == "deprecated":
			if strings.TrimSpace(d.purpose) == "" {
				t.Errorf(".env.example:%d %s is deprecated without saying why", d.line, name)
			}
		case readByService:
		case composeOther[name]:
			// Used by a compose file itself (a container setting, a URL it
			// builds, an image tag): documented, which is what is required.
		case composePassOnly[name]:
			t.Errorf(".env.example:%d %s is passed to a service by a compose file but no service reads it: mark it deprecated or remove it", d.line, name)
		default:
			t.Errorf(".env.example:%d documents %s, which no service and no compose file reads: remove it or mark it deprecated", d.line, name)
		}
		if d.def == "" || d.purpose == "" {
			t.Errorf(".env.example:%d %s needs a default and a purpose", d.line, name)
		}
	}
	// Reverse direction for the compose files: every variable they
	// interpolate is documented, so an operator reading .env.example sees
	// every knob the stack has.
	for _, name := range keys(composeUsed) {
		if _, ok := docs[name]; !ok {
			t.Errorf("%s is interpolated by a compose file (%v) but not documented in .env.example", name, composeFiles)
		}
	}
}

// AC-D7: what the reference says about monitoring, and no shipped DSN.
func TestEnvReferenceSentryStatements(t *testing.T) {
	text := readRepoFile(t, ".env.example")
	for _, want := range []string{
		"SENTRY_RELEASE should be the deployed image version",
		"monitoring is off until SENTRY_DSN is set",
	} {
		if !strings.Contains(text, want) {
			t.Errorf(".env.example does not state %q", want)
		}
	}
	// While the backend still falls back to a built-in DSN (removed by Part A,
	// AC-A14), the reference may state "off until set" only as Part A's
	// behaviour, never as today's.
	if strings.Contains(readRepoFile(t, "backend/sentry.go"), "defaultBackendDSN") {
		for _, line := range strings.Split(text, "\n") {
			if strings.Contains(line, "monitoring is off until SENTRY_DSN is set") && !strings.Contains(line, "Part A") {
				t.Errorf(".env.example states %q unconditionally, but the backend still has its built-in DSN fallback", strings.TrimSpace(line))
			}
		}
	}
	if regexp.MustCompile(`(?m)^SENTRY_DSN=\S`).MatchString(text) {
		t.Error(".env.example ships a SENTRY_DSN value; it must be empty")
	}
	if strings.Contains(text, "ingest.") && strings.Contains(text, "sentry.io/") {
		t.Error(".env.example contains a Sentry ingest DSN")
	}
}

// AC-D6: every relative link in docs/contributing.md resolves.
func TestContributingLinksResolve(t *testing.T) {
	text := readRepoFile(t, "docs/contributing.md")
	links := regexp.MustCompile(`\]\(([^)#\s]+)(#[^)]*)?\)`).FindAllStringSubmatch(text, -1)
	checked := 0
	for _, m := range links {
		target := m[1]
		if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
			continue
		}
		checked++
		if _, err := os.Stat(filepath.Join(repoRoot, "docs", filepath.FromSlash(target))); err != nil {
			t.Errorf("docs/contributing.md links %s, which does not exist", target)
		}
	}
	if checked == 0 {
		t.Error("docs/contributing.md has no relative link; the runbook link is gone")
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- the checkers themselves, on fixtures ---------------------------------

func TestCollectEnvReadsFindsEveryForm(t *testing.T) {
	src := `package p
import "os"
const EnvFoo = "FOO_VAR"
const NotEnv = "IGNORED_VAR"
func c(lookup func(string) string, find func(string) (string, bool), redactLike func(int) string) {
	_ = lookup("LOOKUP_PARAM_VAR")
	_, _ = find("FIND_PARAM_VAR")
	_ = redactLike(1)
}
func writeProviderFailure(redact func(string) string, e string) { _ = redact(e) }
func d(transform func(string) string, k string) { _ = transform(k) }
func a(getenv func(string) string) {
	_ = os.Getenv("LITERAL_VAR")
	_, _ = os.LookupEnv("LOOKUP_VAR")
	_ = getenv(EnvFoo)
	_ = getenv("PARAM_VAR")
	k := "COMPUTED_VAR"
	_ = os.Getenv(k)
}
func oauthProviderAvailability(k string) { _ = os.Getenv(k) }
func b() { oauthProviderAvailability("VIA_READER_VAR") }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := collectEnvReads(fset, []*ast.File{f})
	for _, want := range []string{"FOO_VAR", "LITERAL_VAR", "LOOKUP_VAR", "PARAM_VAR", "VIA_READER_VAR", "LOOKUP_PARAM_VAR", "FIND_PARAM_VAR"} {
		if got.vars[want] == nil {
			t.Errorf("collector missed %s; got %v", want, keys(got.vars))
		}
	}
	for _, unwanted := range []string{"IGNORED_VAR", "COMPUTED_VAR"} {
		if got.vars[unwanted] != nil {
			t.Errorf("collector reported %s", unwanted)
		}
	}
	if all := strings.Join(got.dynamic, ";"); len(got.dynamic) != 2 || !strings.Contains(all, "(in a)") || !strings.Contains(all, "(in d)") {
		t.Errorf("dynamic reads = %v, want the ones in a and d (a getter-shaped parameter of any name; the reviewed reader and notEnvGetters are allowed)", got.dynamic)
	}
}

func TestParseEnvReferenceRejectsMalformedEntries(t *testing.T) {
	docs, assigned, problems := parseEnvReference(strings.Join([]string{
		"#: GOOD_VAR | optional | default: empty | does a thing",
		"GOOD_VAR=",
		"#: NO_STATUS_VAR | default: empty | missing status",
		"#: BAD_STATUS_VAR | sometimes | default: x | wrong status",
		"#: GOOD_VAR | required | default: none | duplicate",
		"UNDOCUMENTED_VAR=1",
		"# a plain comment",
	}, "\n"))
	if docs["GOOD_VAR"].status != "required" && docs["GOOD_VAR"].status != "optional" {
		t.Errorf("GOOD_VAR not parsed: %+v", docs)
	}
	if assigned["UNDOCUMENTED_VAR"] != 6 || assigned["GOOD_VAR"] != 2 {
		t.Errorf("assignments = %v", assigned)
	}
	if len(problems) != 3 {
		t.Errorf("problems = %v, want 3 (two malformed, one duplicate)", problems)
	}
}

func TestComposeUsesSeparatesPassThrough(t *testing.T) {
	used, passOnly := composeUses(strings.Join([]string{
		"      SESSION_SECRET: ${SESSION_SECRET}                # required",
		"      - JWT_SECRET=${JWT_SECRET:-}",
		"      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}",
		"      DATABASE_URL: postgres://u:${POSTGRES_PASSWORD}@db/x",
		"      - ${TLS_CERT_DIR:-/etc/letsencrypt}:/etc/letsencrypt:ro",
		"    # ${COMMENTED_VAR}",
	}, "\n"))
	for _, v := range []string{"SESSION_SECRET", "JWT_SECRET", "POSTGRES_PASSWORD", "TLS_CERT_DIR"} {
		if !used[v] {
			t.Errorf("%s not seen as used", v)
		}
	}
	if used["COMMENTED_VAR"] {
		t.Error("a commented line counted")
	}
	if !passOnly["SESSION_SECRET"] || !passOnly["JWT_SECRET"] || passOnly["POSTGRES_PASSWORD"] || passOnly["TLS_CERT_DIR"] {
		t.Errorf("passthrough-only = %v", passOnly)
	}
}

func TestCollectShellEnvReads(t *testing.T) {
	got := collectShellEnvReads("x.sh", strings.Join([]string{
		"#!/bin/sh",
		"# ${COMMENTED_VAR} is documentation only",
		"OUT=/tmp/x",
		"readonly LOCAL_CONST=1",
		`proxies="${NGINX_TRUSTED_PROXIES:-}"`,
		`echo "$API_BASE_URL $OUT $LOCAL_CONST $lower"`,
	}, "\n"))
	for _, want := range []string{"NGINX_TRUSTED_PROXIES", "API_BASE_URL"} {
		if got[want] == "" {
			t.Errorf("missed %s; got %v", want, got)
		}
	}
	for _, unwanted := range []string{"COMMENTED_VAR", "OUT", "LOCAL_CONST"} {
		if got[unwanted] != "" {
			t.Errorf("reported %s", unwanted)
		}
	}
}
