package main

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"
)

// serve starts routes(...) with t on a loopback port and returns host:port.
func serve(t *testing.T, tm timeouts) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sse := server.NewSSEServer(buildServer(newClient("http://127.0.0.1:1")), server.WithBaseURL("http://"+ln.Addr().String()))
	srv := newServer(ln.Addr().String(), routes(sse, "secret"), tm)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String()
}

func TestDefaultTimeouts(t *testing.T) {
	s := newServer(":0", http.NotFoundHandler(), defaultTimeouts)
	if s.ReadHeaderTimeout != 10*time.Second || s.ReadTimeout != 30*time.Second || s.WriteTimeout != 120*time.Second || s.IdleTimeout != 120*time.Second {
		t.Fatalf("%+v", s)
	}
}

// AC-A16: health mode needs no token and exits 0 only on a 200 from /health.
func TestHealthMode(t *testing.T) {
	addr := serve(t, defaultTimeouts)
	_, port, _ := net.SplitHostPort(addr)
	env := func(k string) string {
		if k == "MCP_PORT" {
			return port
		}
		return ""
	}
	if code := healthMode(env); code != 0 {
		t.Fatalf("exit %d", code)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	_, closedPort, _ := net.SplitHostPort(ln.Addr().String())
	ln.Close()
	if code := healthMode(func(string) string { return closedPort }); code != 1 {
		t.Fatalf("no server: exit %d", code)
	}
	// /health is open; everything else still needs the token.
	resp, err := http.Get("http://" + addr + "/message")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /message: %d", resp.StatusCode)
	}
}

// AC-A16: slow headers are cut at the header bound.
func TestSlowHeadersClosed(t *testing.T) {
	tm := timeouts{readHeader: 300 * time.Millisecond, read: time.Second, write: time.Second, idle: time.Second}
	addr := serve(t, tm)
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	start := time.Now()
	go func() {
		for _, b := range []byte("GET /health HTTP/1.1\r\nHost: x\r\nX-Slow: aaaaaaaaaaaaaaaaaaaaaaa") {
			if _, err := c.Write([]byte{b}); err != nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _ = io.Copy(io.Discard, c)
	if took := time.Since(start); took > tm.readHeader+time.Second {
		t.Fatalf("closed after %s", took)
	}
}

// AC-A16: an open, idle SSE stream is not closed by the write or read bound:
// past both, a message still reaches the client over the stream.
func TestSSEStreamSurvivesWriteTimeout(t *testing.T) {
	tm := timeouts{readHeader: time.Second, read: 300 * time.Millisecond, write: 300 * time.Millisecond, idle: time.Second}
	addr := serve(t, tm)
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, _ = c.Write([]byte("GET /sse HTTP/1.1\r\nHost: " + addr + "\r\nAuthorization: Bearer secret\r\nAccept: text/event-stream\r\n\r\n"))
	br := bufio.NewReader(c)
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	endpoint := ""
	for endpoint == "" {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("no endpoint event: %v", err)
		}
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, "sessionId") {
			endpoint = strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		}
	}
	if !strings.HasPrefix(endpoint, "http") {
		endpoint = "http://" + addr + endpoint
	}
	time.Sleep(4 * tm.write) // idle past both bounds
	req, _ := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(`{"jsonrpc":"2.0","id":7,"method":"ping"}`))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("the stream was cut: %v", err)
		}
		if strings.Contains(line, `"id":7`) {
			return
		}
	}
}
