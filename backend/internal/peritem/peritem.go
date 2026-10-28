// Package peritem runs a handler's per-item outbound calls (one per host,
// member or event) on a bounded pool of workers under the request budget
// (PAC-81 Part A, AC-A17, AC-A18, AC-A19; spec F-9, D-4).
//
// Reads get min(their limit, remaining − margin) as their deadline. Creates
// start only with their full limit plus the margin remaining and are never
// shortened (a cut create risks a duplicate). No call starts once the
// remaining time is at or below the margin. "Remaining" is measured to the
// earlier of the context's deadline (the request budget) and, once shutdown
// has begun, the end of the drain.
//
// Run reports budget.ErrExpired when an item was not started, or when a read
// was cut by the budget (its deadline was shortened by the budget, or the
// request itself ended). A read that fails on its own limit, or with a
// provider error, is the caller's to tolerate ("treated as free").
package peritem

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Enach/paceday/backend/internal/budget"
	"github.com/Enach/paceday/backend/internal/lifecycle"
)

// Settings are the per-request worker count and the stop margin.
type Settings struct {
	Workers int
	Margin  time.Duration
}

// Default: 4 workers, 10 s margin (spec section 3).
var Default = Settings{Workers: 4, Margin: 10 * time.Second}

var (
	mu      sync.RWMutex
	current = Default
)

// Current returns the settings in force.
func Current() Settings {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// SetForTest replaces the settings and returns a restore function.
func SetForTest(s Settings) (restore func()) {
	mu.Lock()
	prev := current
	current = s
	mu.Unlock()
	return func() {
		mu.Lock()
		current = prev
		mu.Unlock()
	}
}

// Mode is how an item's call is bounded.
type Mode int

const (
	// Read calls get a deadline shortened by the budget when needed.
	Read Mode = iota
	// Create calls need their full limit plus the margin to start, and then
	// run to their own limit, detached from the request's cancellation.
	Create
)

// Remaining is the time left before the budget or the drain ends; ok is false
// when neither bounds ctx.
func Remaining(ctx context.Context) (time.Duration, bool) {
	end, ok := ctx.Deadline()
	if d := lifecycle.Default().DrainDeadline(); !d.IsZero() && (!ok || d.Before(end)) {
		end, ok = d, true
	}
	if !ok {
		return 0, false
	}
	return time.Until(end), true
}

// Run calls fn for items 0..n-1 on at most Workers goroutines, in order. fn's
// own error is not returned: the caller decides what a failed item means.
func Run(ctx context.Context, n int, mode Mode, limit time.Duration, fn func(ctx context.Context, i int) error) error {
	s := Current()
	workers := s.Workers
	if workers < 1 {
		workers = 1
	}
	var (
		cutMu sync.Mutex
		cut   bool
	)
	markCut := func() {
		cutMu.Lock()
		cut = true
		cutMu.Unlock()
	}
	isCut := func() bool {
		cutMu.Lock()
		defer cutMu.Unlock()
		return cut
	}

	next := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers && w < n; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				if !runOne(ctx, mode, limit, s.Margin, i, fn) {
					markCut()
				}
			}
		}()
	}
	for i := 0; i < n; i++ {
		if isCut() || ctx.Err() != nil {
			markCut()
			break
		}
		next <- i
	}
	close(next)
	wg.Wait()
	if isCut() {
		return budget.ErrExpired
	}
	return nil
}

// runOne runs item i and reports false when it was not started or was cut
// by the budget.
func runOne(ctx context.Context, mode Mode, limit, margin time.Duration, i int, fn func(context.Context, int) error) bool {
	if ctx.Err() != nil {
		return false
	}
	remaining, bounded := Remaining(ctx)
	if bounded && remaining <= margin {
		return false
	}
	if mode == Create {
		if bounded && remaining < limit+margin {
			return false
		}
		callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), limit)
		defer cancel()
		_ = fn(callCtx, i)
		return true
	}
	d := limit
	shortened := false
	if bounded && remaining-margin < limit {
		d, shortened = remaining-margin, true
	}
	callCtx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	_ = fn(callCtx, i)
	if callCtx.Err() != nil && (shortened || ctx.Err() != nil) {
		return false
	}
	return true
}

// IsCut reports whether err is a budget cut.
func IsCut(err error) bool { return errors.Is(err, budget.ErrExpired) }
