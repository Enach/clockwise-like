package lifecycle

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// fakeReporter is installed per test through SetReporter (PAC-80: no
// exported func var).
type fakeReporter struct {
	mu   sync.Mutex
	got  []Unit
	errs []error
}

func installFake(t *testing.T) *fakeReporter {
	t.Helper()
	f := &fakeReporter{}
	t.Cleanup(SetReporter(func(u Unit, err error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.got = append(f.got, u)
		f.errs = append(f.errs, err)
	}))
	return f
}

func (f *fakeReporter) snapshot() ([]Unit, []error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Unit(nil), f.got...), append([]error(nil), f.errs...)
}

// PAC-122 SF-096: a recovered panic of a tracked unit reaches the reporter
// with the unit's kind and id, not only the log.
func TestPAC122_PanicIsReported(t *testing.T) {
	f := installFake(t)
	defer SetLogf(func(string, ...any) {})()
	g := NewGroup()
	g.Go(Unit{Kind: "habit-reoptimize", ID: "u1"}, func(context.Context) { panic("boom") })
	g.Wait(context.Background())
	units, errs := f.snapshot()
	if len(units) != 1 || units[0].Kind != "habit-reoptimize" || units[0].ID != "u1" || !strings.Contains(errs[0].Error(), "boom") {
		t.Fatalf("reports %v %v", units, errs)
	}
}

// PAC-122 SF-096: an error-returning unit's error is logged and reported;
// a nil error is neither.
func TestPAC122_GoErrReportsTheError(t *testing.T) {
	f := installFake(t)
	sink := &logSink{}
	defer SetLogf(sink.logf)()
	g := NewGroup()
	defer SetDefault(g)()
	cause := errors.New("list habits: db down")
	GoErr(Unit{Kind: "analytics-recompute", ID: "u2"}, func(context.Context) error { return cause })
	GoErr(Unit{Kind: "analytics-recompute", ID: "u3"}, func(context.Context) error { return nil })
	g.Wait(context.Background())
	units, errs := f.snapshot()
	if len(units) != 1 || units[0].ID != "u2" || !errors.Is(errs[0], cause) {
		t.Fatalf("reports %v %v", units, errs)
	}
	if !sink.has("analytics-recompute u2 failed: list habits: db down") || sink.has("u3") {
		t.Fatalf("log %v", sink.lines)
	}
}

// PAC-122 SF-096: GoFromErr keeps GoFrom's values and reports the error.
func TestPAC122_GoFromErrReportsTheError(t *testing.T) {
	f := installFake(t)
	defer SetLogf(func(string, ...any) {})()
	g := NewGroup()
	defer SetDefault(g)()
	type key struct{}
	parent := context.WithValue(context.Background(), key{}, "v")
	var seen any
	GoFromErr(parent, Unit{Kind: "manager-detection", ID: "u4"}, func(ctx context.Context) error {
		seen = ctx.Value(key{})
		return errors.New("detect failed")
	})
	g.Wait(context.Background())
	units, _ := f.snapshot()
	if seen != "v" || len(units) != 1 || units[0].Kind != "manager-detection" {
		t.Fatalf("seen %v reports %v", seen, units)
	}
}

// PAC-122 SF-097: Recover deferred by a goroutine that is not a Group.Go
// unit (a cron job) recovers the panic and reports it.
func TestPAC122_RecoverReports(t *testing.T) {
	f := installFake(t)
	defer SetLogf(func(string, ...any) {})()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer Recover(Unit{Kind: "cron", ID: "auto-decline"})
		panic("nil map")
	}()
	<-done
	units, errs := f.snapshot()
	if len(units) != 1 || units[0].String() != "cron auto-decline" || !strings.Contains(errs[0].Error(), "nil map") {
		t.Fatalf("reports %v %v", units, errs)
	}
}

// SetReporter's restore puts the previous reporter back; nil removes it.
func TestPAC122_SetReporterRestore(t *testing.T) {
	var n int
	restore := SetReporter(func(Unit, error) { n++ })
	inner := SetReporter(nil)
	Report(Unit{Kind: "k"}, errors.New("x"))
	inner()
	Report(Unit{Kind: "k"}, errors.New("x"))
	Report(Unit{Kind: "k"}, nil)
	restore()
	Report(Unit{Kind: "k"}, errors.New("x"))
	if n != 1 {
		t.Fatalf("reported %d times, want 1", n)
	}
}
