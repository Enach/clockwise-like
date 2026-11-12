package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// PAC-90 items 2 and 4: the access log's implicit 200 and flush through the
// production chain.

// --- Item 2: access log ------------------------------------------------------

// pac90Lines returns the captured access-log lines for route.
func pac90Lines(l *logLines, route string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, line := range l.lines {
		if strings.Contains(line, " "+route+" ") {
			out = append(out, line)
		}
	}
	return out
}

// AC-3: production chain, a live request whose handler writes nothing:
// exactly one access line, status 200 (net/http's implicit 200), not "-".
func TestPAC90_AccessLogUnwrittenLiveRequestIs200(t *testing.T) {
	l := captureAccessLog(t)
	r := fullRouter(t)
	r.Get("/api/pac90/silent", func(http.ResponseWriter, *http.Request) {})
	rec := serveAt(r, http.MethodGet, "/api/pac90/silent", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("client got %d", rec.Code)
	}
	lines := pac90Lines(l, "/api/pac90/silent")
	if len(lines) != 1 || !strings.Contains(lines[0], "GET /api/pac90/silent 200 ") {
		t.Fatalf("lines %v", lines)
	}
}

// --- Item 4: flush through the production chain -----------------------------

func pac90Server(t *testing.T, path string, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	r := fullRouter(t)
	r.Get(path, h)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// AC-7: a handler writes a chunk, flushes, and waits for the client to
// confirm it received it: the chunk arrives within 2 s, before the handler
// returns.
func TestPAC90_FlushReachesClientThroughProductionChain(t *testing.T) {
	received := make(chan struct{})
	var returned atomic.Bool
	srv := pac90Server(t, "/api/pac90/stream", func(w http.ResponseWriter, _ *http.Request) {
		defer returned.Store(true)
		_, _ = w.Write([]byte("chunk1"))
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("flush: %v", err)
		}
		select {
		case <-received:
		case <-time.After(5 * time.Second):
		}
		_, _ = w.Write([]byte("end"))
	})
	got := make(chan string, 1)
	go func() {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/api/pac90/stream", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			got <- "error: " + err.Error()
			return
		}
		defer resp.Body.Close()
		buf := make([]byte, len("chunk1"))
		if _, err := io.ReadFull(resp.Body, buf); err != nil {
			got <- "error: " + err.Error()
			return
		}
		got <- string(buf)
		_, _ = io.Copy(io.Discard, resp.Body)
	}()
	select {
	case s := <-got:
		if s != "chunk1" {
			t.Fatalf("client read %q", s)
		}
		if returned.Load() {
			t.Fatal("the chunk arrived only after the handler returned")
		}
		close(received)
	case <-time.After(2 * time.Second):
		close(received)
		t.Fatal("the flushed chunk did not reach the client within 2 s")
	}
}

// AC-8: a flush before any write sends the 200; the client then goes away.
// The access log records 200 from the flush, not "-" from the ended context.
func TestPAC90_FlushBeforeWriteLogs200(t *testing.T) {
	l := captureAccessLog(t)
	handlerDone := make(chan struct{})
	srv := pac90Server(t, "/api/pac90/flush-first", func(w http.ResponseWriter, r *http.Request) {
		defer close(handlerDone)
		_ = http.NewResponseController(w).Flush()
		<-r.Context().Done() // until the client has gone
	})
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/pac90/flush-first", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("client got %d", resp.StatusCode)
	}
	cancel()
	_ = resp.Body.Close()
	select {
	case <-handlerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not see the client leave")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		lines := pac90Lines(l, "/api/pac90/flush-first")
		if len(lines) == 1 {
			if !strings.Contains(lines[0], "GET /api/pac90/flush-first 200 ") {
				t.Fatalf("line %q, want status 200", lines[0])
			}
			return
		}
		if len(lines) > 1 || time.Now().After(deadline) {
			t.Fatalf("lines %v", lines)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
