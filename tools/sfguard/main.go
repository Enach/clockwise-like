// Command sfguard refuses new blank-assigned errors from storage, auth and
// calendar calls in non-test backend code (PAC-122, silent-failure register
// docs/factory/silent-failures.md §8).
//
// golangci-lint's errcheck runs with check-blank: false, so it accepts every
// `_ = storage.X(...)` and `v, _ := storage.Y(...)`. That shape — an error
// thrown away, the zero value then read as "absent" or "none" — is the most
// common silent failure in the register. This guard is type-aware (go/types,
// not grep): it resolves each call, so formatting, aliases and calls through
// interfaces do not hide one, and `_ = json.NewEncoder(w).Encode(...)` or any
// other call outside the targeted set is never flagged.
//
// It ratchets against a baseline of accepted occurrences (baseline.txt), keyed
// by file, enclosing function and callee — never line numbers. The baseline
// grows only with a named PAC issue, reviewer-confirmed: the guard checks the
// issue is named and prints the growth, the reviewer checks the verdict.
//
//	go run . -root ../.. -baseline baseline.txt backend mcp
//	go run . -root ../.. -baseline baseline.txt -update backend mcp > new.txt
//
// Exit status: 0 clean; 1 a new occurrence, a stale or malformed baseline
// entry, or an unlisted seam; 2 the packages could not be loaded (never
// treated as a pass).
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
)

const help = `
A blank-assigned error from a storage, auth.Load* or calendar call is the
register's most common silent failure: the zero value becomes "absent", "none"
or "not connected" and the user is told something false
(docs/factory/silent-failures.md, the SF rows; §8 is this guard).

Fix it: handle the error — return it, or log it and answer with an error the
caller can see. If discarding it really is right, the baseline is the only
exception mechanism (no //nolint): add the line printed by
  cd tools/sfguard && go run . -root ../.. -baseline baseline.txt -update backend mcp
to tools/sfguard/baseline.txt with a comment naming the PAC issue that removes
it (or that records why it is fine), and say so in the PR. The baseline grows
only with a named PAC issue, reviewer-confirmed: the reviewer treats every
added line as a finding unless its issue and verdict hold up.`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sfguard", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root; reported paths are relative to it")
	baselinePath := fs.String("baseline", "", "baseline file (required)")
	update := fs.Bool("update", false, "print the current occurrences in baseline format instead of checking")
	baseRef := fs.String("base-ref", "", "git ref to report baseline growth against, e.g. origin/main (informational)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *baselinePath == "" || fs.NArg() == 0 {
		fmt.Fprintln(stderr, "usage: sfguard -root DIR -baseline FILE [-update] [-base-ref REF] MODULE_DIR...")
		return 2
	}
	absRoot, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintln(stderr, "sfguard:", err)
		return 2
	}

	var findings []Finding
	var seamProblems []string
	for _, dir := range fs.Args() {
		fsx, sp, err := scanModule(absRoot, dir)
		if err != nil {
			fmt.Fprintf(stderr, "sfguard: %s: %v\n", dir, err)
			return 2
		}
		findings = append(findings, fsx...)
		seamProblems = append(seamProblems, sp...)
	}

	raw, err := os.ReadFile(*baselinePath)
	if err != nil {
		fmt.Fprintln(stderr, "sfguard:", err)
		return 2
	}
	baseline, problems, err := ParseBaseline(bytes.NewReader(raw))
	if err != nil {
		fmt.Fprintln(stderr, "sfguard:", err)
		return 2
	}

	if *update {
		Render(stdout, findings, baseline)
		return 0
	}

	res := Ratchet(findings, baseline)
	for _, p := range problems {
		fmt.Fprintln(stderr, "BASELINE", p)
	}
	for _, f := range res.New {
		fmt.Fprintln(stderr, "NEW  ", f.String())
	}
	for _, s := range res.Stale {
		fmt.Fprintln(stderr, "STALE", s, "— the occurrence is gone: delete this baseline line")
	}
	for _, s := range seamProblems {
		fmt.Fprintln(stderr, "SEAM ", s)
	}
	if *baseRef != "" {
		reportGrowth(stdout, stderr, absRoot, *baselinePath, *baseRef, baseline)
	}
	if !res.OK() || len(problems) > 0 || len(seamProblems) > 0 {
		if len(res.New) > 0 {
			fmt.Fprintln(stderr, help)
		}
		return 1
	}
	fmt.Fprintf(stdout, "sfguard OK: %d occurrence(s), all baselined; nothing new, nothing stale\n", len(findings))
	return 0
}

// scanModule returns the module's targeted discards and the seam audit's
// problems (seams.go).
func scanModule(root, dir string) ([]Finding, []string, error) {
	pkgs, err := loadModule(filepath.Join(root, dir))
	if err != nil {
		return nil, nil, err
	}
	var out []Finding
	for _, p := range pkgs {
		out = append(out, checkPackage(p, root)...)
	}
	_, seamProblems := auditSeams(pkgs)
	return out, seamProblems, nil
}

// loadModule type-checks every non-test package of the module in modDir. Any
// package error fails the load: a partial scan is never a pass.
func loadModule(modDir string) ([]*packages.Package, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Dir:   modDir,
		Tests: false,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, err
	}
	var errs []string
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			errs = append(errs, e.Error())
		}
	})
	if len(errs) > 0 {
		if len(errs) > 10 {
			errs = append(errs[:10], fmt.Sprintf("... and %d more", len(errs)-10))
		}
		return nil, fmt.Errorf("could not type-check (a partial scan is not a pass):\n  %s", strings.Join(errs, "\n  "))
	}
	return pkgs, nil
}

// reportGrowth prints what this branch adds to the baseline relative to ref.
// It cannot fail the run: every entry already has to name a PAC issue, and the
// growth is there for the reviewer to see. When ref cannot be read it says so.
func reportGrowth(stdout, stderr io.Writer, root, baselinePath, ref string, cur *Baseline) {
	absBase, _ := filepath.Abs(baselinePath)
	rel, err := filepath.Rel(root, absBase)
	if err != nil {
		return
	}
	out, err := exec.Command("git", "-C", root, "show", ref+":"+filepath.ToSlash(rel)).Output()
	if err != nil {
		fmt.Fprintf(stderr, "note: cannot read %s:%s, baseline growth not reported\n", ref, filepath.ToSlash(rel))
		return
	}
	base, _, _ := ParseBaseline(bytes.NewReader(out))
	grown := Grown(cur, base)
	if len(grown) == 0 {
		fmt.Fprintf(stdout, "sfguard: baseline does not grow relative to %s\n", ref)
		return
	}
	fmt.Fprintf(stdout, "::warning title=sfguard::baseline grows relative to %s by %d entr(y/ies); each names a PAC issue, reviewer confirms:\n", ref, len(grown))
	for _, g := range grown {
		fmt.Fprintln(stdout, "  ADDED", g)
	}
}
