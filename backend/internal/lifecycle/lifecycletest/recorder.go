// Package lifecycletest records what background work reports through
// lifecycle's reporter hook (PAC-122 §6), for tests in any package.
package lifecycletest

import (
	"strings"
	"sync"
	"testing"

	"github.com/Enach/paceday/backend/internal/lifecycle"
)

// Report is one reported failure.
type Report struct {
	Unit lifecycle.Unit
	Err  error
}

// Recorder collects the reports made while it is installed.
type Recorder struct {
	mu      sync.Mutex
	reports []Report
}

// Record installs a Recorder as lifecycle's reporter for the rest of t (the
// previous reporter is restored at cleanup). Tests that use it must not run
// in parallel with other tests of their package that report.
func Record(t testing.TB) *Recorder {
	t.Helper()
	r := &Recorder{}
	restore := lifecycle.SetReporter(func(u lifecycle.Unit, err error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.reports = append(r.reports, Report{Unit: u, Err: err})
	})
	t.Cleanup(restore)
	return r
}

// All returns a copy of the reports so far.
func (r *Recorder) All() []Report {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Report(nil), r.reports...)
}

// Matching returns the reports of unit kind whose error text contains sub.
func (r *Recorder) Matching(kind, sub string) []Report {
	var out []Report
	for _, rep := range r.All() {
		if rep.Unit.Kind == kind && strings.Contains(rep.Err.Error(), sub) {
			out = append(out, rep)
		}
	}
	return out
}
