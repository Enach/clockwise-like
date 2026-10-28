// Package lifecycle tracks the backend's background work and runs the
// shutdown sequence (PAC-81 Part A, AC-A6, AC-A7).
//
// Every piece of work that can outlive the request or cron tick that started
// it is a unit: a kind (request, cron job, recap, analytics recompute,
// booking completion, email...), an identifier that never contains an email
// address, and optionally a detail evaluated when the unit is abandoned
// (booking completion names the hosts done and not done). Units run on a
// context that is cancelled at the end of the drain.
package lifecycle

import (
	"context"
	"log"
	"net"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Unit names a tracked piece of work.
type Unit struct {
	Kind string
	ID   string
	// Detail, when set, is called once if the unit is abandoned and its
	// result is appended to the log line.
	Detail func() string
}

func (u Unit) String() string {
	s := u.Kind
	if u.ID != "" {
		s += " " + u.ID
	}
	return s
}

// Group is a set of tracked units.
type Group struct {
	mu     sync.Mutex
	next   uint64
	units  map[uint64]Unit
	idle   chan struct{} // closed while no unit runs
	ctx    context.Context
	cancel context.CancelFunc

	shutting      atomic.Bool
	drainDeadline atomic.Int64 // unix nanoseconds; 0 until the drain starts
}

// NewGroup returns an empty group.
func NewGroup() *Group {
	ctx, cancel := context.WithCancel(context.Background())
	idle := make(chan struct{})
	close(idle)
	return &Group{units: map[uint64]Unit{}, idle: idle, ctx: ctx, cancel: cancel}
}

var (
	defaultMu    sync.RWMutex
	defaultGroup = NewGroup()
)

// Default is the process-wide group used by handlers, engines and crons.
func Default() *Group {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultGroup
}

// SetDefault replaces the process-wide group and returns a function that
// restores the previous one (tests).
func SetDefault(g *Group) (restore func()) {
	defaultMu.Lock()
	prev := defaultGroup
	defaultGroup = g
	defaultMu.Unlock()
	return func() {
		defaultMu.Lock()
		defaultGroup = prev
		defaultMu.Unlock()
	}
}

// Go runs fn on its own goroutine as a tracked unit of the default group.
func Go(u Unit, fn func(ctx context.Context)) { Default().Go(u, fn) }

// Context is the work context of the group: cancelled at the end of the drain.
func (g *Group) Context() context.Context { return g.ctx }

// Track registers a unit run by the caller's own goroutine. The returned
// function must be called when the work returns.
func (g *Group) Track(u Unit) (done func()) {
	g.mu.Lock()
	id := g.next
	g.next++
	if len(g.units) == 0 {
		g.idle = make(chan struct{})
	}
	g.units[id] = u
	g.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			delete(g.units, id)
			if len(g.units) == 0 {
				close(g.idle)
			}
			g.mu.Unlock()
		})
	}
}

// Go runs fn on its own goroutine as a tracked unit. A panic in fn is
// recovered and logged, so background work never takes the process down.
func (g *Group) Go(u Unit, fn func(ctx context.Context)) {
	done := g.Track(u)
	go func() {
		defer done()
		defer func() {
			if p := recover(); p != nil {
				logf("lifecycle: %s panicked: %v", u, p)
			}
		}()
		fn(g.ctx)
	}()
}

// Wait blocks until no unit runs or ctx is done; it reports whether the
// group became idle.
func (g *Group) Wait(ctx context.Context) bool {
	for {
		g.mu.Lock()
		idle := g.idle
		g.mu.Unlock()
		select {
		case <-idle:
			g.mu.Lock()
			n := len(g.units)
			g.mu.Unlock()
			if n == 0 {
				return true
			}
		case <-ctx.Done():
			return false
		}
	}
}

// Pending lists the units still running, oldest first.
func (g *Group) Pending() []Unit {
	g.mu.Lock()
	defer g.mu.Unlock()
	ids := make([]uint64, 0, len(g.units))
	for id := range g.units {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]Unit, 0, len(ids))
	for _, id := range ids {
		out = append(out, g.units[id])
	}
	return out
}

// BeginShutdown marks the start of the shutdown sequence (readiness answers
// 503 from now on).
func (g *Group) BeginShutdown() { g.shutting.Store(true) }

// ShuttingDown reports whether the shutdown sequence has begun.
func (g *Group) ShuttingDown() bool { return g.shutting.Load() }

// DrainDeadline is the end of the drain once it has started, else zero.
func (g *Group) DrainDeadline() time.Time {
	n := g.drainDeadline.Load()
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}

func (g *Group) setDrainDeadline(t time.Time) { g.drainDeadline.Store(t.UnixNano()) }

// logf is the logger of this package (a test seam).
var logf = log.Printf

// SetLogf replaces the logger and returns a restore function (tests, main).
func SetLogf(f func(format string, args ...any)) (restore func()) {
	prev := logf
	logf = f
	return func() { logf = prev }
}

// Timeouts are the server-side bounds of spec section 3 (D-4).
type Timeouts struct {
	ReadHeader time.Duration
	Read       time.Duration
	Write      time.Duration
	Idle       time.Duration
}

// DefaultTimeouts: headers 10 s, whole request 30 s, write backstop 120 s,
// idle keep-alive 120 s.
var DefaultTimeouts = Timeouts{ReadHeader: 10 * time.Second, Read: 30 * time.Second, Write: 120 * time.Second, Idle: 120 * time.Second}

// NewServer returns an http.Server with the timeouts set. Its request
// contexts derive from base, which the shutdown sequence cancels at the end
// of the drain.
func NewServer(addr string, h http.Handler, t Timeouts, base context.Context) *http.Server {
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: t.ReadHeader,
		ReadTimeout:       t.Read,
		WriteTimeout:      t.Write,
		IdleTimeout:       t.Idle,
	}
	if base != nil {
		srv.BaseContext = func(_ net.Listener) context.Context { return base }
	}
	return srv
}

// Phases are the shutdown phases (D-5): unready notice 5 s, drain 30 s,
// cancel grace 5 s.
type Phases struct {
	Notice, Drain, Grace time.Duration
}

// DefaultPhases is spec section 3's shutdown timing.
var DefaultPhases = Phases{Notice: 5 * time.Second, Drain: 30 * time.Second, Grace: 5 * time.Second}

// Server is what the sequence needs from an http.Server.
type Server interface {
	Shutdown(ctx context.Context) error
	Close() error
}

// Shutdown runs the sequence of AC-A6/AC-A7 and reports whether everything
// finished within the drain (exit 0) or something was cancelled or
// abandoned (exit non-zero):
//
//  1. readiness answers 503 and stopScheduling stops every cron from
//     starting a new run; the server keeps accepting and serving for Notice;
//  2. the listener closes (Server.Shutdown);
//  3. in-flight requests and every tracked unit are awaited until the end of
//     the drain;
//  4. if anything is left, request contexts (cancelRequests) and units are
//     cancelled, the grace is waited, each unit still running is logged as
//     abandoned with its kind, id and detail, and connections are closed.
//
// The caller closes the database pool and flushes error reports after it
// returns, so the pool is closed only after every unit returned or was
// abandoned.
func (g *Group) Shutdown(srv Server, ph Phases, stopScheduling func(), cancelRequests func()) (clean bool) {
	g.BeginShutdown()
	if stopScheduling != nil {
		stopScheduling()
	}
	logf("shutdown: not ready; serving for %s before closing the listener", ph.Notice)
	time.Sleep(ph.Notice)

	drainEnd := time.Now().Add(ph.Drain)
	g.setDrainDeadline(drainEnd)
	drainCtx, cancelDrain := context.WithDeadline(context.Background(), drainEnd)
	defer cancelDrain()
	logf("shutdown: listener closed; draining for %s", ph.Drain)
	srvDone := make(chan error, 1)
	go func() { srvDone <- srv.Shutdown(drainCtx) }()
	unitsIdle := g.Wait(drainCtx)
	srvErr := <-srvDone
	if unitsIdle && srvErr == nil {
		logf("shutdown: drained cleanly")
		return true
	}

	logf("shutdown: drain ended with work still running; cancelling")
	if cancelRequests != nil {
		cancelRequests()
	}
	g.cancel()
	graceCtx, cancelGrace := context.WithTimeout(context.Background(), ph.Grace)
	defer cancelGrace()
	g.Wait(graceCtx)
	for _, u := range g.Pending() {
		line := "shutdown: abandoned " + u.String()
		if u.Detail != nil {
			if d := u.Detail(); d != "" {
				line += " (" + d + ")"
			}
		}
		logf("%s", line)
	}
	_ = srv.Close()
	return false
}

// Probe is a health mode's single request (AC-A16): GET url, success only
// on a 200 within timeout.
func Probe(url string, timeout time.Duration) bool {
	c := &http.Client{Timeout: timeout}
	resp, err := c.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// GoFrom is Go for work started by a request: fn's context carries parent's
// values (the user id), is not cancelled when the request ends, and is
// cancelled at the end of the drain like every unit.
func GoFrom(parent context.Context, u Unit, fn func(ctx context.Context)) {
	g := Default()
	g.Go(u, func(work context.Context) {
		ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
		defer cancel()
		stop := context.AfterFunc(work, cancel)
		defer stop()
		fn(ctx)
	})
}
