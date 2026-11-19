package engine

import (
	"sync/atomic"

	"github.com/google/uuid"
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
// DecideMember and MemberWeek (PAC-130) and of the gap check's
// future-occurrence count. A non-nil
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
	// PAC-130 (contract md §6): the Manager visibility statement and the
	// confirmed-holder lookup of DecideMember.
	StepVisibilityRead = "visibility-read"
	StepHolderLookup   = "holder-lookup"
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

// PAC-136: habitRunLocked fires once a habit reoptimize run holds its user's
// lock, before it reads anything. A test blocks in it to hold the lock while
// a second run starts.
var habitRunLocked seam[func(userID uuid.UUID)]

// InstallHabitRunLocked installs the habit-run-locked seam.
func InstallHabitRunLocked(tb TB, fn func(userID uuid.UUID)) (uninstall func()) {
	tb.Helper()
	return habitRunLocked.install(tb, fn)
}

// PAC-136 review N2: habitGateLeaving fires while a habit run leaves the
// gate, between its two steps (pending decremented, user token released).
var habitGateLeaving seam[func(userID uuid.UUID)]

// InstallHabitGateLeaving installs the habit-gate-leaving seam.
func InstallHabitGateLeaving(tb TB, fn func(userID uuid.UUID)) (uninstall func()) {
	tb.Helper()
	return habitGateLeaving.install(tb, fn)
}

// slackAPIOverride replaces slackAPIBase for every Slack Web API call when a
// test installs it (PAC-141 review): packages outside engine, such as the
// api handlers' recap test send, can then point Slack at a stub and never
// reach slack.com. Engine tests may still assign slackAPIBase directly.
var slackAPIOverride seam[string]

// InstallSlackAPIBase points every Slack Web API call at url until the test
// ends.
func InstallSlackAPIBase(tb TB, url string) (uninstall func()) {
	tb.Helper()
	return slackAPIOverride.install(tb, url)
}

// slackAPI is the Slack Web API base: the installed override, else
// slackAPIBase.
func slackAPI() string {
	if u, ok := slackAPIOverride.get(); ok {
		return u
	}
	return slackAPIBase
}
