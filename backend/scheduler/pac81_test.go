package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/lifecycle"
)

type nopSrv struct{}

func (nopSrv) Shutdown(context.Context) error { return nil }
func (nopSrv) Close() error                   { return nil }

// PAC-81 (review of #247): once shutdown has cancelled the work, a tick runs
// nobody and advances no watermark, so the user stays due after restart.
func TestPAC81_FocusTickAfterShutdownAdvancesNothing(t *testing.T) {
	db := openTestDB(t)
	a := seedUser(t, db, "pac81-focus@example.com")
	enable(t, db, a, at(6, 30), "0 7 * * 1-5", "Europe/Paris")
	g := lifecycle.NewGroup()
	defer lifecycle.SetDefault(g)()
	block := make(chan struct{})
	g.Go(lifecycle.Unit{Kind: "x"}, func(context.Context) { <-block })
	g.Shutdown(nopSrv{}, lifecycle.Phases{Drain: 10 * time.Millisecond, Grace: 10 * time.Millisecond}, nil, nil)
	close(block)
	rec := &recorder{}
	fc, logs := newTestCron(db, rec.run)
	fc.tick(at(7, 0))
	if n := len(rec.runsFor(a)); n != 0 {
		t.Fatalf("ran %d times after shutdown", n)
	}
	if w := watermark(t, db, a); w == nil || !w.Equal(at(6, 30)) {
		t.Fatalf("watermark moved to %v", w)
	}
	if logs.mentioning("stopped by shutdown") != 1 {
		t.Fatal("no shutdown line")
	}
}
