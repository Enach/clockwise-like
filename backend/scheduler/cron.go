package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"golang.org/x/oauth2"
)

// RunFunc runs focus-time scheduling for one user for the week containing
// week. In production it is FocusTimeEngine.RunForUser, the same per-user run
// the manual request reaches.
type RunFunc func(ctx context.Context, userID uuid.UUID, week time.Time) (*engine.FocusRunResult, error)

// FocusCron is the automatic focus-time scheduler (PAC-49 spec v3.1 §4.3).
//
// Once a minute it re-reads who has auto-schedule on — so enabling, editing
// or disabling takes effect at the next minute, never at a restart — and runs
// each user whose next slot after their watermark has come, evaluated in that
// user's own timezone. The watermark is advanced to the tick instant before
// the run, whatever the run's outcome, so a slot never runs twice and a
// missed slot is caught up exactly once.
type FocusCron struct {
	db  *sql.DB
	run RunFunc

	// now is the loop's clock and logf its log; both are replaced by tests.
	now  func() time.Time
	logf func(format string, args ...any)
	// interval is how often the loop ticks; runTimeout bounds one user's run.
	interval   time.Duration
	runTimeout time.Duration

	// ticking is held for the length of a tick; a tick that cannot take it is
	// dropped, never queued (§4.3 item 2).
	ticking sync.Mutex

	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}
}

func NewFocusCron(db *sql.DB, oauthConfig *oauth2.Config) *FocusCron {
	eng := &engine.FocusTimeEngine{DB: db, OAuthConfig: oauthConfig}
	return &FocusCron{
		db:         db,
		run:        eng.RunForUser,
		now:        time.Now,
		logf:       log.Printf,
		interval:   time.Minute,
		runTimeout: 10 * time.Minute,
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
}

// Start begins ticking in the background. Each tick runs on its own goroutine
// so that a tick still in progress causes the next one to be dropped.
func (fc *FocusCron) Start() {
	go func() {
		defer close(fc.done)
		t := time.NewTicker(fc.interval)
		defer t.Stop()
		for {
			select {
			case <-fc.stop:
				return
			case <-t.C:
				go fc.tick(fc.now())
			}
		}
	}()
}

// Stop ends the ticking. A tick already in progress finishes on its own.
func (fc *FocusCron) Stop() {
	fc.stopOnce.Do(func() {
		close(fc.stop)
		select {
		case <-fc.done:
		case <-time.After(time.Second): // Start was never called
		}
	})
}

// tick runs every due user once for the instant at. It reports false when it
// was dropped because the previous tick was still running.
func (fc *FocusCron) tick(at time.Time) bool {
	if !fc.ticking.TryLock() {
		fc.logf("focus scheduler: tick at %s dropped, previous tick still running", at.Format(time.RFC3339))
		return false
	}
	defer fc.ticking.Unlock()

	candidates, err := storage.ListAutoScheduleCandidates(fc.db)
	if err != nil {
		fc.logf("focus scheduler: listing auto-schedule users: %v", err)
		return true
	}
	for _, c := range candidates {
		fc.consider(c, at)
	}
	return true
}

// consider decides whether c is due at the tick instant and, if so, advances
// the watermark and runs them. Nothing here can stop the loop.
func (fc *FocusCron) consider(c storage.AutoScheduleCandidate, at time.Time) {
	sched, err := engine.ParseAutoScheduleCron(c.CronExpr)
	if err != nil {
		fc.logf("focus scheduler: skipping user %s: stored schedule %q is not usable: %v", c.UserID, c.CronExpr, err)
		return
	}
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		fc.logf("focus scheduler: skipping user %s: stored timezone %q is not usable: %v", c.UserID, c.Timezone, err)
		return
	}

	if c.LastRun == nil {
		// Only a direct database edit produces this: treat it as "now".
		if _, err := storage.AdvanceAutoScheduleWatermark(fc.db, c.UserID, nil, at); err != nil {
			fc.logf("focus scheduler: setting the watermark for user %s: %v", c.UserID, err)
		}
		return
	}
	if nextSlot(sched, *c.LastRun, loc).After(at) {
		return
	}

	advanced, err := storage.AdvanceAutoScheduleWatermark(fc.db, c.UserID, c.LastRun, at)
	if err != nil {
		fc.logf("focus scheduler: advancing the watermark for user %s: %v", c.UserID, err)
		return
	}
	if !advanced {
		// A save disabled the feature or reset the watermark since the
		// listing; the next tick decides afresh.
		return
	}
	fc.runOne(c.UserID, at.In(loc))
}

// runOne runs one user's focus scheduling, abandoning it after runTimeout.
func (fc *FocusCron) runOne(userID uuid.UUID, week time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), fc.runTimeout)
	defer cancel()

	type outcome struct {
		result *engine.FocusRunResult
		err    error
	}
	out := make(chan outcome, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				out <- outcome{err: fmt.Errorf("panic: %v", r)}
			}
		}()
		res, err := fc.run(ctx, userID, week)
		out <- outcome{res, err}
	}()

	select {
	case o := <-out:
		switch {
		case o.err != nil:
			fc.logf("focus scheduler: run for user %s failed: %v", userID, o.err)
		case o.result != nil:
			fc.logf("focus scheduler: run for user %s created %d blocks (%d min), %d day errors",
				userID, len(o.result.CreatedBlocks), o.result.TotalMinutes, len(o.result.Errors))
		}
	case <-ctx.Done():
		fc.logf("focus scheduler: run for user %s abandoned after %s", userID, fc.runTimeout)
	}
}

// nextSlot is the user's first slot strictly after the watermark, in loc.
//
// It steps through wall-clock time rather than letting the library step
// through loc's offsets, which across a daylight-saving change would skip a
// slot that falls in the spring-forward gap for the whole day and run a slot
// in the repeated autumn hour twice (PAC-49 OQ-6). Wall-clock stepping gives
// each calendar day's slot exactly once; a slot inside the gap maps to the
// instant time.Date gives that wall time (one hour later, 03:30 for 02:30 in
// Europe/Paris), and a slot in the repeated hour runs once.
func nextSlot(sched cron.Schedule, watermark time.Time, loc *time.Location) time.Time {
	w := watermark.In(loc)
	wall := time.Date(w.Year(), w.Month(), w.Day(), w.Hour(), w.Minute(), w.Second(), w.Nanosecond(), time.UTC)
	n := sched.Next(wall)
	return time.Date(n.Year(), n.Month(), n.Day(), n.Hour(), n.Minute(), 0, 0, loc)
}
