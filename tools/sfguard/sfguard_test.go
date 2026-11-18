package main

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// fixtureKeys is every occurrence the fixture holds, with its count. Anything
// else the guard reports is a false positive; anything missing, a miss.
var fixtureKeys = map[string]int{
	"api/api.go (*handler).show storage.GetUser":           1,
	"api/api.go (*handler).show storage.Save":              1,
	"api/api.go (*handler).show sql.Row.Scan":              1,
	"api/api.go (*handler).show auth.LoadToken":            1,
	"api/api.go handler.odd storage.Count":                 1,
	"api/api.go handler.odd storage.Store.Get":             2,
	"api/api.go handler.odd storage.Save":                  1,
	"api/api.go twice storage.Save":                        2,
	"engine/engine.go Sync engine.calendarOps.listEvents":  1,
	"engine/engine.go Sync engine.calendarOps.deleteEvent": 1,
	"engine/engine.go Sync calendar.Provider.ListEvents":   1,
	// The phrase "Code generated ... DO NOT EDIT" after the package clause
	// does not make a file generated.
	"api/midgen.go notGenerated storage.Save": 1,
	// Seams declared outside storage and calendar.
	"api/seams.go seamCalls api.orgAIStore.read":                1,
	"api/seams.go seamCalls api.ConferenceEventClient.GetEvent": 1,
	"api/seams.go seamCalls api.dbOrgAIStore.read":              1,
	"auth/credential.go renew auth.credentialStore.load":        1,
	"auth/credential.go renew auth.googleStore.load":            1,
	// A generic storage call, and the sql.Tx and sql.Conn handles.
	"api/seams.go handles storage.Find":         1,
	"api/seams.go handles sql.Tx.Exec":          1,
	"api/seams.go handles sql.Conn.ExecContext": 1,
}

func fixtureFindings(t *testing.T) []Finding {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("testdata", "fixture"))
	if err != nil {
		t.Fatal(err)
	}
	fs, seamProblems, err := scanModule(root, ".")
	if err != nil {
		t.Fatalf("scan fixture: %v", err)
	}
	if len(seamProblems) > 0 {
		t.Fatalf("the fixture's seams are all listed, got %q", seamProblems)
	}
	return fs
}

func TestSeamAuditRefusesAnUnlistedSeam(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("testdata", "seamfixture"))
	if err != nil {
		t.Fatal(err)
	}
	_, problems, err := scanModule(root, ".")
	if err != nil {
		t.Fatal(err)
	}
	// The unlisted concrete type behind a listed seam, then the unlisted seam
	// interface; `pure` (an implementation calling nothing targeted) is neither.
	if len(problems) != 2 ||
		!strings.HasPrefix(problems[0], "auth.otherStore implements the listed seam auth.credentialStore: otherStore.load calls storage.GetWidget") ||
		!strings.HasPrefix(problems[1], "api.widgetStore: implemented by api.dbWidgetStore.get calls storage.GetWidget") {
		t.Fatalf("want the unlisted otherStore implementation and the unlisted widgetStore seam, got %q", problems)
	}
}

// auditedInterfaces is every backend interface declared outside storage and
// calendar that the seam audit matches today, each read and classified (PAC-122
// guardrail review). guarded: in seams, so a blank error through it fails.
// operation: in notSeams, with the reason there.
var auditedInterfaces = map[string]string{
	"engine.calendarOps":            "guarded",
	"engine.outlookFreeBusy":        "guarded",
	"engine.googleFreeBusy":         "guarded",
	"api.ConferenceEventClient":     "guarded",
	"api.orgAIStore":                "guarded",
	"api.ssoStore":                  "guarded",
	"auth.credentialStore":          "guarded",
	"nlp.AISource":                  "guarded",
	".teamDetector":                 "operation",
	"api.BookingFlow":               "operation",
	"api.Compressor":                "operation",
	"api.ConferenceProviderFactory": "operation",
	"api.FocusEngine":               "operation",
	"api.ManagerWorkflow":           "operation",
	"api.NLPParser":                 "operation",
	"api.PersonalCalendarBlocker":   "operation",
	"api.Scheduler":                 "operation",
	"api.TeamAvailability":          "operation",
}

// TestSeamAuditOnTheBackend runs the audit on the real backend module and pins
// its answer: a new seam (or a seam that disappeared) changes this list, and
// the change is reviewed here.
func TestSeamAuditOnTheBackend(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the whole backend module; PAC-122 guardrail: runs in make lint (no -short)")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cfgPkgs, err := loadModule(filepath.Join(root, "backend"))
	if err != nil {
		t.Fatal(err)
	}
	found, problems := auditSeams(cfgPkgs)
	if len(problems) > 0 {
		t.Fatalf("unlisted seams:\n  %s", strings.Join(problems, "\n  "))
	}
	var diffs []string
	for k := range found {
		s := short(k)
		want, ok := auditedInterfaces[s]
		got := "operation"
		if seams[k] != "" {
			got = "guarded"
		}
		if !ok || want != got {
			diffs = append(diffs, s+": audit says "+got+", list says "+want)
		}
	}
	for s := range auditedInterfaces {
		if _, ok := found[backendModule+"/"+s]; !ok && !(strings.HasPrefix(s, ".") && found[backendModule+s] != "") {
			diffs = append(diffs, s+": listed but no longer matched by the audit")
		}
	}
	sort.Strings(diffs)
	if len(diffs) > 0 {
		t.Fatalf("audited interfaces changed:\n  %s", strings.Join(diffs, "\n  "))
	}
}

func TestScanFindsExactlyTheTargetedDiscards(t *testing.T) {
	got := map[string]int{}
	for _, f := range fixtureFindings(t) {
		got[f.Key()]++
	}
	var diffs []string
	for k, n := range fixtureKeys {
		if got[k] != n {
			diffs = append(diffs, "want "+k+" x"+strconv.Itoa(n)+", got x"+strconv.Itoa(got[k]))
		}
	}
	for k, n := range got {
		if _, ok := fixtureKeys[k]; !ok {
			diffs = append(diffs, "unexpected "+k+" x"+strconv.Itoa(n))
		}
	}
	sort.Strings(diffs)
	if len(diffs) > 0 {
		t.Fatalf("scan mismatch:\n  %s", strings.Join(diffs, "\n  "))
	}
}

func TestParseBaselineRequiresAPACIssue(t *testing.T) {
	in := `# header
a.go  F  storage.X  # PAC-12 fine: why

b.go  G  storage.Y  # no issue here
c.go  H  # PAC-1 too few fields
d.go  I  storage.Z
`
	b, problems, err := ParseBaseline(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if b.Count["a.go F storage.X"] != 1 || len(b.Count) != 1 {
		t.Fatalf("accepted %v, want only a.go", b.Count)
	}
	if len(problems) != 3 {
		t.Fatalf("problems = %q, want 3 (no PAC, bad shape, no comment)", problems)
	}
}

func TestRatchet(t *testing.T) {
	f := func(file string, line int) Finding {
		return Finding{File: file, Line: line, Func: "F", Callee: "storage.X"}
	}
	b, _, _ := ParseBaseline(strings.NewReader(
		"a.go F storage.X # PAC-1\na.go F storage.X # PAC-1\nb.go F storage.X # PAC-1\n"))

	if r := Ratchet([]Finding{f("a.go", 1), f("a.go", 9), f("b.go", 3)}, b); !r.OK() {
		t.Fatalf("all baselined, line numbers moved: want OK, got %+v", r)
	}
	if r := Ratchet([]Finding{f("a.go", 1), f("a.go", 2), f("a.go", 3), f("b.go", 3)}, b); len(r.New) != 3 {
		t.Fatalf("a third a.go occurrence: want its 3 occurrences reported, got %+v", r.New)
	}
	if r := Ratchet([]Finding{f("a.go", 1), f("b.go", 3), f("c.go", 3)}, b); len(r.New) != 1 || len(r.Stale) != 1 {
		t.Fatalf("one fixed, one new: want 1 new + 1 stale, got %+v", r)
	}
}

func TestGrownReportsAdditionsOnly(t *testing.T) {
	base, _, _ := ParseBaseline(strings.NewReader("a.go F storage.X # PAC-1\nb.go F storage.X # PAC-1\n"))
	cur, _, _ := ParseBaseline(strings.NewReader("a.go F storage.X # PAC-1\na.go F storage.X # PAC-9 second\nc.go F storage.X # PAC-9\n"))
	g := Grown(cur, base)
	if len(g) != 2 || !strings.HasPrefix(g[0], "a.go F storage.X  (1 -> 2)") || !strings.HasPrefix(g[1], "c.go F storage.X  (0 -> 1)") {
		t.Fatalf("Grown = %q", g)
	}
}

func TestRunExitCodesAndUpdateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join("testdata", "fixture")
	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"-root", root, "-baseline", empty, "."}, &out, &errb); code != 1 {
		t.Fatalf("empty baseline: exit %d, want 1\n%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "docs/factory/silent-failures.md") {
		t.Fatalf("the failure message must point at the register:\n%s", errb.String())
	}

	out.Reset()
	if code := run([]string{"-root", root, "-baseline", empty, "-update", "."}, &out, &errb); code != 0 {
		t.Fatalf("-update: exit %d", code)
	}
	// -update leaves a TODO comment: the PAC check refuses it until named.
	full := filepath.Join(dir, "full.txt")
	if err := os.WriteFile(full, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	errb.Reset()
	if code := run([]string{"-root", root, "-baseline", full, "."}, &out, &errb); code != 1 || !strings.Contains(errb.String(), "no PAC issue") {
		t.Fatalf("unnamed entries: exit %d, want 1 with 'no PAC issue'\n%s", code, errb.String())
	}
	named := strings.ReplaceAll(out.String(), "# TODO", "# PAC-122 TODO")
	if err := os.WriteFile(full, []byte(named), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if code := run([]string{"-root", root, "-baseline", full, "."}, &out, &errb); code != 0 {
		t.Fatalf("full, named baseline: exit %d, want 0\n%s", code, errb.String())
	}
}

func TestRunRefusesAPartialScan(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module github.com/Enach/paceday/backend\n\ngo 1.26.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.go"), []byte("package broken\n\nfunc f() { undefined() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"-root", dir, "-baseline", empty, "."}, &out, &errb); code != 2 {
		t.Fatalf("a package that does not type-check: exit %d, want 2\n%s", code, errb.String())
	}
}

func TestParseBaselineRefusesPACZero(t *testing.T) {
	for _, c := range []string{"PAC-0", "PAC-007", "PAC-", "pac-12"} {
		_, problems, _ := ParseBaseline(strings.NewReader("a.go F storage.X # " + c + " why\n"))
		if len(problems) != 1 {
			t.Errorf("%q: want refused, got %q", c, problems)
		}
	}
	if _, problems, _ := ParseBaseline(strings.NewReader("a.go F storage.X # PAC-140 why\n")); len(problems) != 0 {
		t.Errorf("PAC-140 refused: %q", problems)
	}
}

func TestRenderKeepsDistinctCommentsPerKey(t *testing.T) {
	old, _, _ := ParseBaseline(strings.NewReader(
		"a.go F storage.X # PAC-1 first\na.go F storage.X # PAC-2 second\n"))
	f := func(line int) Finding {
		return Finding{File: "a.go", Line: line, Func: "F", Callee: "storage.X", Why: "storage"}
	}
	var out bytes.Buffer
	Render(&out, []Finding{f(30), f(10), f(20)}, old)
	want := "a.go  F  storage.X  # PAC-1 first\n" +
		"a.go  F  storage.X  # PAC-2 second\n" +
		"a.go  F  storage.X  # TODO name the PAC issue that removes this (storage)\n"
	if out.String() != want {
		t.Fatalf("Render =\n%s\nwant\n%s", out.String(), want)
	}
}
