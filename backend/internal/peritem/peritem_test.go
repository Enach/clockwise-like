package peritem

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/lifecycle"
)

// stub answers each call after delay, or when the call's context ends,
// recording when each call started and the peak concurrency.
type stub struct {
	delay  time.Duration
	mu     sync.Mutex
	starts []time.Time
	live   atomic.Int32
	peak   atomic.Int32
}

func (s *stub) call(ctx context.Context, _ int) error {
	s.mu.Lock()
	s.starts = append(s.starts, time.Now())
	s.mu.Unlock()
	n := s.live.Add(1)
	defer s.live.Add(-1)
	for {
		p := s.peak.Load()
		if n <= p || s.peak.CompareAndSwap(p, n) {
			break
		}
	}
	select {
	case <-time.After(s.delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func budgetCtx(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

func TestDefaults(t *testing.T) {
	if Default.Workers != 4 || Default.Margin != 10*time.Second {
		t.Fatalf("%+v", Default)
	}
}

// 5 items at the limit on 4 workers: two waves, complete.
func TestTwoWavesComplete(t *testing.T) {
	defer SetForTest(Settings{Workers: 4, Margin: 50 * time.Millisecond})()
	ctx, cancel := budgetCtx(2 * time.Second)
	defer cancel()
	s := &stub{delay: 100 * time.Millisecond}
	start := time.Now()
	if err := Run(ctx, 5, Read, 100*time.Millisecond, s.call); err != nil {
		t.Fatalf("got %v", err)
	}
	if s.peak.Load() != 4 || len(s.starts) != 5 {
		t.Fatalf("peak %d starts %d", s.peak.Load(), len(s.starts))
	}
	if took := time.Since(start); took < 190*time.Millisecond || took > 600*time.Millisecond {
		t.Fatalf("took %s, want about two waves", took)
	}
}

// Too many items: the budget cuts, and no call starts after the margin.
func TestCutNoStartAfterMargin(t *testing.T) {
	margin := 100 * time.Millisecond
	defer SetForTest(Settings{Workers: 4, Margin: margin})()
	b := 450 * time.Millisecond
	ctx, cancel := budgetCtx(b)
	defer cancel()
	deadline, _ := ctx.Deadline()
	s := &stub{delay: 100 * time.Millisecond}
	if err := Run(ctx, 20, Read, 100*time.Millisecond, s.call); !IsCut(err) {
		t.Fatalf("got %v, want a budget cut", err)
	}
	if len(s.starts) >= 20 {
		t.Fatal("every item started")
	}
	for _, st := range s.starts {
		if !st.Before(deadline.Add(-margin)) {
			t.Fatalf("a call started %s before the end, inside the margin", deadline.Sub(st))
		}
	}
}

// A read failing on its own limit (deadline not shortened) is not a cut.
func TestOwnLimitIsNotACut(t *testing.T) {
	defer SetForTest(Settings{Workers: 4, Margin: 50 * time.Millisecond})()
	ctx, cancel := budgetCtx(5 * time.Second)
	defer cancel()
	s := &stub{delay: time.Hour}
	if err := Run(ctx, 2, Read, 80*time.Millisecond, s.call); err != nil {
		t.Fatalf("own-limit timeout reported as %v", err)
	}
}

// A provider error is not a cut either.
func TestProviderErrorIsNotACut(t *testing.T) {
	defer SetForTest(Settings{Workers: 4, Margin: 50 * time.Millisecond})()
	ctx, cancel := budgetCtx(5 * time.Second)
	defer cancel()
	err := Run(ctx, 3, Read, time.Second, func(context.Context, int) error { return errors.New("500 from provider") })
	if err != nil {
		t.Fatalf("got %v", err)
	}
}

// AC-A18 rev 2: a read whose deadline the budget shortened, and that hits it,
// is a budget cut.
func TestShortenedDeadlineIsCut(t *testing.T) {
	margin := 50 * time.Millisecond
	defer SetForTest(Settings{Workers: 4, Margin: margin})()
	ctx, cancel := budgetCtx(200 * time.Millisecond) // remaining - margin = 150ms < limit
	defer cancel()
	s := &stub{delay: time.Hour}
	if err := Run(ctx, 1, Read, time.Second, s.call); !IsCut(err) {
		t.Fatalf("got %v", err)
	}
}

// Creates start only with limit + margin left, and are not shortened.
func TestCreateNeedsFullLimit(t *testing.T) {
	margin := 50 * time.Millisecond
	defer SetForTest(Settings{Workers: 1, Margin: margin})()
	ctx, cancel := budgetCtx(400 * time.Millisecond)
	defer cancel()
	s := &stub{delay: 100 * time.Millisecond}
	// 100 + 50 needed each: items at 0 and ~100 ms and ~200 ms start; the one
	// at ~300 ms (100 left) does not.
	err := Run(ctx, 6, Create, 100*time.Millisecond, s.call)
	if !IsCut(err) {
		t.Fatalf("got %v", err)
	}
	if n := len(s.starts); n < 2 || n > 3 {
		t.Fatalf("%d creates started", n)
	}
	// A started create runs to its own limit even if the request ends.
	ctx2, cancel2 := context.WithCancel(context.Background())
	var finished atomic.Bool
	go func() { time.Sleep(20 * time.Millisecond); cancel2() }()
	_ = Run(ctx2, 1, Create, time.Second, func(c context.Context, _ int) error {
		select {
		case <-time.After(80 * time.Millisecond):
			finished.Store(true)
		case <-c.Done():
		}
		return nil
	})
	if !finished.Load() {
		t.Fatal("a started create was cut by the request's cancellation")
	}
}

// Without a budget, only the limit applies.
func TestUnboundedContext(t *testing.T) {
	if _, ok := Remaining(context.Background()); ok {
		t.Fatal("background context reported bounded")
	}
	s := &stub{delay: 10 * time.Millisecond}
	if err := Run(context.Background(), 6, Create, time.Second, s.call); err != nil {
		t.Fatal(err)
	}
}

// Once shutdown has begun, remaining time is measured to the drain end.
func TestRemainingIncludesDrain(t *testing.T) {
	g := lifecycle.NewGroup()
	defer lifecycle.SetDefault(g)()
	srv := nopServer{}
	done := make(chan struct{})
	block := make(chan struct{})
	g.Go(lifecycle.Unit{Kind: "x"}, func(context.Context) { <-block })
	go func() {
		g.Shutdown(srv, lifecycle.Phases{Drain: 300 * time.Millisecond, Grace: 10 * time.Millisecond}, nil, nil)
		close(done)
	}()
	for g.DrainDeadline().IsZero() {
		time.Sleep(time.Millisecond)
	}
	r, ok := Remaining(context.Background())
	if !ok || r > 300*time.Millisecond {
		t.Fatalf("remaining %s %v", r, ok)
	}
	close(block)
	<-done
}

func TestCancelledContextIsCut(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, 2, Read, time.Second, func(context.Context, int) error { return nil }); !IsCut(err) {
		t.Fatalf("got %v", err)
	}
}

type nopServer struct{}

func (nopServer) Shutdown(context.Context) error { return nil }
func (nopServer) Close() error                   { return nil }
