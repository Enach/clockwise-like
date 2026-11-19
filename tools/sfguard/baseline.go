package main

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// Baseline is the accepted set of occurrences, keyed by the line-insensitive
// fingerprint `file func callee`. An occurrence listed twice is allowed twice.
//
// File format, one occurrence per line:
//
//	backend/api/x.go  (*Handler).Create  storage.GetUser  # PAC-122 SF-012: why
//
// Blank lines and lines starting with # are commentary. Every entry must carry
// a `# ...` comment that names a PAC issue (PAC-NN): accepting an occurrence
// has to name the issue that will remove it.
type Baseline struct {
	Count    map[string]int
	Comments map[string][]string // every entry's comment per key, in file order, for -update
	Order    []string            // keys in file order, unique
}

// pacID is a real issue id: a positive number, so PAC-0 and PAC-007 are refused.
var pacID = regexp.MustCompile(`\bPAC-[1-9][0-9]*\b`)

// ParseBaseline reads a baseline. Malformed lines and entries without a PAC
// issue are returned as problems; they never count as accepted.
func ParseBaseline(r io.Reader) (*Baseline, []string, error) {
	b := &Baseline{Count: map[string]int{}, Comments: map[string][]string{}}
	var problems []string
	sc := bufio.NewScanner(r)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		body, comment, hasComment := strings.Cut(line, "#")
		fields := strings.Fields(body)
		if len(fields) != 3 {
			problems = append(problems, fmt.Sprintf("baseline line %d: want `file func callee  # PAC-NN ...`, got %q", n, line))
			continue
		}
		comment = strings.TrimSpace(comment)
		if !hasComment || !pacID.MatchString(comment) {
			problems = append(problems, fmt.Sprintf("baseline line %d: entry %q has no PAC issue in its comment (`# PAC-NN ...`)", n, strings.Join(fields, " ")))
			continue
		}
		key := strings.Join(fields, " ")
		if b.Count[key] == 0 {
			b.Order = append(b.Order, key)
		}
		b.Comments[key] = append(b.Comments[key], comment)
		b.Count[key]++
	}
	return b, problems, sc.Err()
}

// Result is the ratchet's verdict.
type Result struct {
	New   []Finding // occurrences over the baselined count
	Stale []string  // baseline keys with more entries than occurrences
}

func (r Result) OK() bool { return len(r.New) == 0 && len(r.Stale) == 0 }

// Ratchet compares findings to the baseline. A key found more often than it is
// baselined is NEW (every occurrence of it is reported, since lines are not
// part of the key). A key baselined more often than found is STALE: the fix
// landed, so the entry must go: nothing fixed stays accepted.
func Ratchet(findings []Finding, b *Baseline) Result {
	byKey := map[string][]Finding{}
	for _, f := range findings {
		byKey[f.Key()] = append(byKey[f.Key()], f)
	}
	var r Result
	for key, fs := range byKey {
		if len(fs) > b.Count[key] {
			r.New = append(r.New, fs...)
		}
	}
	for _, key := range b.Order {
		if got := len(byKey[key]); got < b.Count[key] {
			r.Stale = append(r.Stale, fmt.Sprintf("%s  (baselined %d, found %d)", key, b.Count[key], got))
		}
	}
	sort.Slice(r.New, func(i, j int) bool {
		if r.New[i].File != r.New[j].File {
			return r.New[i].File < r.New[j].File
		}
		return r.New[i].Line < r.New[j].Line
	})
	return r
}

// Grown lists the keys whose count is higher in cur than in base: what a
// branch added to the baseline relative to origin/main.
func Grown(cur, base *Baseline) []string {
	var out []string
	for _, key := range cur.Order {
		if cur.Count[key] > base.Count[key] {
			out = append(out, fmt.Sprintf("%s  (%d -> %d)  # %s", key, base.Count[key], cur.Count[key], strings.Join(cur.Comments[key], " | ")))
		}
	}
	return out
}

// Render writes findings as baseline lines. The k-th occurrence of a key
// reuses the k-th comment the existing baseline carries for it, so distinct
// verdicts on one key survive; an occurrence beyond those gets a placeholder
// that fails the PAC check until someone names the issue.
func Render(w io.Writer, findings []Finding, old *Baseline) {
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Key() != findings[j].Key() {
			return findings[i].Key() < findings[j].Key()
		}
		return findings[i].Line < findings[j].Line
	})
	used := map[string]int{}
	for _, f := range findings {
		k := f.Key()
		c := "TODO name the PAC issue that removes this (" + f.Why + ")"
		if cs := old.Comments[k]; used[k] < len(cs) {
			c = cs[used[k]]
		}
		used[k]++
		fmt.Fprintf(w, "%s  %s  %s  # %s\n", f.File, f.Func, f.calleeField(), c)
	}
}
