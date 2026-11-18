package engine

import (
	"sync/atomic"
)

// The engine package's deterministic test seams, on the PAC-80 pattern of
// storage/test_seams.go: each is held in a synchronised holder, not an
// exported variable, so production code cannot set one by assignment, and a
// test installs one through its Install function, which clears it when the
// test ends. Production installs none: every seam is a no-op until a test
// installs it.

// TB is the part of testing.TB the Install functions use.
type TB interface {
	Helper()
	Cleanup(func())
}

// seam holds one hook of function type F.
type seam[F any] struct{ p atomic.Pointer[F] }

func (s *seam[F]) get() (F, bool) {
	if p := s.p.Load(); p != nil {
		return *p, true
	}
	var zero F
	return zero, false
}

func (s *seam[F]) install(tb TB, f F) (uninstall func()) {
	tb.Helper()
	p := &f
	s.p.Store(p)
	uninstall = func() { s.p.CompareAndSwap(p, nil) }
	tb.Cleanup(uninstall)
	return uninstall
}

// PAC-122 batch 4 (docs/factory/silent-failures.md SF-040): the manager
// engine's storage steps. managerStep fires at each named step of
// GetMemberWeek and of the gap check's future-occurrence count. A non-nil
// error replaces that step's error, so it takes the production error path:
// after a read (the read ran; its result is treated as failed), and before
// the cache write (nothing is written).
var managerStep seam[func(step, memberEmail string) error]

// Manager step names, as managerStep receives them.
const (
	StepOwnerCheck        = "owner-check"
	StepCacheRead         = "cache-read"
	StepConsentRead       = "consent-read"
	StepAnalyticsWeekRead = "analytics-week-read"
	StepFreeBusy          = "freebusy"
	StepCacheWrite        = "cache-write"
	StepFutureOccurrences = "future-occurrences"
)

// InstallManagerStep installs the manager-engine step seam.
func InstallManagerStep(tb TB, fn func(step, memberEmail string) error) (uninstall func()) {
	tb.Helper()
	return managerStep.install(tb, fn)
}

// managerStepErr returns the installed step hook's error for step when it
// fails, and err otherwise.
func managerStepErr(step, memberEmail string, err error) error {
	if hook, ok := managerStep.get(); ok {
		if hookErr := hook(step, memberEmail); hookErr != nil {
			return hookErr
		}
	}
	return err
}
