package engine

import "time"

// Clock returns the current time. Inject a fake clock in tests to control
// time-dependent behaviour (cadence math, recap windows, freebusy TTL,
// booking slug suffix, etc.). Implementations must be safe for concurrent use.
type Clock interface {
	Now() time.Time
}

// realClock is the production implementation that delegates to time.Now.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// SystemClock is the package-default Clock. Engines that have no Clock
// configured fall back to it so existing call sites continue to work.
var SystemClock Clock = realClock{}

// FixedClock is a test helper that always returns T.
type FixedClock struct{ T time.Time }

func (f FixedClock) Now() time.Time { return f.T }
