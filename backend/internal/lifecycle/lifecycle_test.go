package lifecycle

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// startServer serves h with t on a loopback listener.
func startServer(t *testing.T, h http.Handler, tm Timeouts, base context.Context) (*http.Server, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(ln.Addr().String(), h, tm, base)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return srv, ln.Addr().String()
}

// closedWithin reads from c until the server closes it and returns how long
// that took from start.
func closedWithin(t *testing.T, c net.Conn, start time.Time, limit time.Duration) time.Duration {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(limit + 2*time.Second))
	_, err := io.Copy(io.Discard, c)
	took := time.Since(start)
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatalf("server did not close the connection within %s", limit+2*time.Second)
	}
	return took
}

var small = Timeouts{ReadHeader: 300 * time.Millisecond, Read: 700 * time.Millisecond, Write: 2 * time.Second, Idle: 400 * time.Millisecond}

func TestDefaultTimeoutsAreTheSpecValues(t *testing.T) {
	srv := NewServer(":0", http.NotFoundHandler(), DefaultTimeouts, nil)
	if srv.ReadHeaderTimeout != 10*time.Second || srv.ReadTimeout != 30*time.Second ||
		srv.WriteTimeout != 120*time.Second || srv.IdleTimeout != 120*time.Second {
		t.Fatalf("got %+v", srv)
	}
	if DefaultPhases != (Phases{5 * time.Second, 30 * time.Second, 5 * time.Second}) {
		t.Fatalf("phases %+v", DefaultPhases)
	}
}

// AC-A1: headers sent at one byte per interval are cut at the header bound.
func TestServerClosesSlowHeaders(t *testing.T) {
	_, addr := startServer(t, http.NotFoundHandler(), small, nil)
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	start := time.Now()
	go func() {
		for _, b := range []byte("GET / HTTP/1.1\r\nHost: x\r\nX-Slow: aaaaaaaaaaaaaaaaaaaa") {
			if _, err := c.Write([]byte{b}); err != nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
	if took := closedWithin(t, c, start, small.ReadHeader); took > small.ReadHeader+time.Second {
		t.Fatalf("closed after %s, want <= %s", took, small.ReadHeader+time.Second)
	}
}

// AC-A1: headers then a stalled body are cut at the whole-request bound.
func TestServerClosesStalledBody(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	_, addr := startServer(t, h, small, nil)
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	start := time.Now()
	_, _ = c.Write([]byte("POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 100\r\n\r\nabc"))
	if took := closedWithin(t, c, start, small.Read); took > small.Read+time.Second {
		t.Fatalf("closed after %s, want <= %s", took, small.Read+time.Second)
	}
}

// AC-A5: an idle keep-alive connection is closed at the idle bound.
func TestServerClosesIdleKeepAlive(t *testing.T) {
	_, addr := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}), small, nil)
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, _ = c.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n"))
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	start := time.Now()
	_ = c.SetReadDeadline(time.Now().Add(small.Idle + 2*time.Second))
	_, err = br.ReadByte()
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("idle connection was never closed")
	}
	if took := time.Since(start); took > small.Idle+time.Second {
		t.Fatalf("closed after %s", took)
	}
}

type logSink struct {
	mu    sync.Mutex
	lines []string
}

func (s *logSink) logf(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, fmt.Sprintf(format, args...))
}

func (s *logSink) has(sub string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range s.lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

// AC-A6: readiness 503 from T0 while new requests are served; the listener
// closes at T0 + notice; the in-flight request and the running unit finish;
// the sequence ends clean.
func TestShutdownSequence(t *testing.T) {
	sink := &logSink{}
	defer SetLogf(sink.logf)()
	g := NewGroup()
	mux := http.NewServeMux()
	mux.HandleFunc("/ready", func(w http.ResponseWriter, _ *http.Request) {
		if g.ShuttingDown() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/fast", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(900 * time.Millisecond)
		_, _ = w.Write([]byte("slow done"))
	})
	base, cancelBase := context.WithCancel(context.Background())
	defer cancelBase()
	srv, addr := startServer(t, mux, Timeouts{ReadHeader: time.Second, Read: 2 * time.Second, Write: 5 * time.Second, Idle: 5 * time.Second}, base)
	url := "http://" + addr

	slow := make(chan string, 1)
	go func() {
		resp, err := http.Get(url + "/slow")
		if err != nil {
			slow <- err.Error()
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		slow <- string(b)
	}()
	unitDone := make(chan struct{})
	g.Go(Unit{Kind: "cron", ID: "personal-blocker"}, func(context.Context) {
		time.Sleep(800 * time.Millisecond)
		close(unitDone)
	})
	stopped := make(chan struct{})
	time.Sleep(50 * time.Millisecond)

	ph := Phases{Notice: 400 * time.Millisecond, Drain: 2 * time.Second, Grace: 300 * time.Millisecond}
	t0 := time.Now()
	result := make(chan bool, 1)
	go func() { result <- g.Shutdown(srv, ph, func() { close(stopped) }, cancelBase) }()

	time.Sleep(100 * time.Millisecond)
	select {
	case <-stopped:
	default:
		t.Fatal("crons were not told to stop scheduling at T0")
	}
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Get(url + "/ready")
	if err != nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("readiness at T0+100ms: %v %v", resp, err)
	}
	resp.Body.Close()
	resp, err = client.Get(url + "/fast")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("new request during the notice: %v %v", resp, err)
	}
	resp.Body.Close()

	time.Sleep(time.Until(t0.Add(ph.Notice + 100*time.Millisecond)))
	if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
		c.Close()
		t.Fatal("connection accepted after the notice")
	}
	if got := <-slow; got != "slow done" {
		t.Fatalf("in-flight request: %q", got)
	}
	<-unitDone
	if !<-result {
		t.Fatal("sequence not clean although everything finished in the drain")
	}
	if !sink.has("drained cleanly") {
		t.Fatalf("log %v", sink.lines)
	}
}

// AC-A7: work still running at the end of the drain is cancelled, waited for
// the grace, logged one line per abandoned unit, and the result is not clean.
func TestShutdownAbandonsAndLogs(t *testing.T) {
	sink := &logSink{}
	defer SetLogf(sink.logf)()
	g := NewGroup()
	cancelled := make(chan struct{})
	g.Go(Unit{Kind: "analytics-recompute", ID: "u1"}, func(ctx context.Context) {
		<-ctx.Done()
		close(cancelled)
	})
	block := make(chan struct{})
	defer close(block)
	g.Go(Unit{Kind: "booking-completion", ID: "b-42", Detail: func() string { return "hosts done: h1; not done: h2" }},
		func(context.Context) { <-block })

	srv := &http.Server{}
	ph := Phases{Notice: 50 * time.Millisecond, Drain: 200 * time.Millisecond, Grace: 200 * time.Millisecond}
	start := time.Now()
	if g.Shutdown(srv, ph, nil, nil) {
		t.Fatal("clean with abandoned work")
	}
	if took := time.Since(start); took > ph.Notice+ph.Drain+ph.Grace+3*time.Second {
		t.Fatalf("took %s", took)
	}
	<-cancelled
	if !sink.has("shutdown: abandoned booking-completion b-42 (hosts done: h1; not done: h2)") {
		t.Fatalf("log %v", sink.lines)
	}
	if sink.has("abandoned analytics-recompute") {
		t.Fatal("a unit that returned within the grace was logged as abandoned")
	}
	if g.DrainDeadline().IsZero() || !g.ShuttingDown() {
		t.Fatal("drain deadline / shutting flag not set")
	}
}

func TestGroupTrackWaitPending(t *testing.T) {
	g := NewGroup()
	if !g.Wait(context.Background()) {
		t.Fatal("empty group not idle")
	}
	done := g.Track(Unit{Kind: "request", ID: "GET /x"})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if g.Wait(ctx) {
		t.Fatal("busy group reported idle")
	}
	if p := g.Pending(); len(p) != 1 || p[0].String() != "request GET /x" {
		t.Fatalf("pending %v", p)
	}
	done()
	done() // idempotent
	if !g.Wait(context.Background()) || len(g.Pending()) != 0 {
		t.Fatal("not idle after done")
	}
	// A panicking unit is recovered and released.
	sink := &logSink{}
	defer SetLogf(sink.logf)()
	g.Go(Unit{Kind: "k"}, func(context.Context) { panic("x") })
	if !g.Wait(context.Background()) || !sink.has("k panicked") {
		t.Fatal("panicking unit not released")
	}
}

func TestDefaultGroupSwap(t *testing.T) {
	g := NewGroup()
	restore := SetDefault(g)
	ran := make(chan struct{})
	Go(Unit{Kind: "x"}, func(context.Context) { close(ran) })
	<-ran
	restore()
	if Default() == g {
		t.Fatal("default not restored")
	}
}

// AC-A16: the health probe succeeds only on a 200 within the timeout.
func TestHealthProbe(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer ok.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer bad.Close()
	hang := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-hang }))
	defer slow.Close()
	defer close(hang)
	if !Probe(ok.URL, time.Second) || Probe(bad.URL, time.Second) {
		t.Fatal("probe misjudged 200/503")
	}
	start := time.Now()
	if Probe(slow.URL, 200*time.Millisecond) || time.Since(start) > time.Second {
		t.Fatal("slow server not refused within the timeout")
	}
	if Probe("http://127.0.0.1:1/", 200*time.Millisecond) {
		t.Fatal("refused connection reported healthy")
	}
}

type valKey struct{}

func TestGoFromKeepsValuesAndDetaches(t *testing.T) {
	g := NewGroup()
	defer SetDefault(g)()
	parent, cancelParent := context.WithCancel(context.WithValue(context.Background(), valKey{}, "u1"))
	got := make(chan string, 1)
	release := make(chan struct{})
	GoFrom(parent, Unit{Kind: "habit-reoptimize", ID: "u1"}, func(ctx context.Context) {
		<-release
		if ctx.Err() != nil {
			got <- "cancelled"
			return
		}
		got <- ctx.Value(valKey{}).(string)
	})
	cancelParent() // the request ended
	close(release)
	if v := <-got; v != "u1" {
		t.Fatalf("got %q", v)
	}
	// The group's cancellation reaches it.
	stopped := make(chan struct{})
	GoFrom(context.Background(), Unit{Kind: "k"}, func(ctx context.Context) { <-ctx.Done(); close(stopped) })
	g.cancel()
	<-stopped
}
