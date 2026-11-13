package main

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// PAC-81 Part C (owner decision Q-O1, spec revision 3.3): behind nginx at
// /mcp/ on the public origin, the SSE server must serve <base>/sse and
// <base>/message and announce a message endpoint the client can reach on the
// same origin, never http://0.0.0.0:<port>.

func TestSSEBasePathFromEnv(t *testing.T) {
	for in, want := range map[string]string{"": "", "/mcp": "/mcp", "/mcp/": "/mcp", "/a/b": "/a/b"} {
		got, err := sseBasePath(func(string) string { return in })
		if err != nil || got != want {
			t.Errorf("MCP_BASE_PATH=%q: got %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"mcp", "http://x/mcp", "/mcp?x=1", "/mc p", "/../x", "/"} {
		if _, err := sseBasePath(func(string) string { return bad }); err == nil {
			t.Errorf("MCP_BASE_PATH=%q accepted", bad)
		} else if !strings.Contains(err.Error(), "MCP_BASE_PATH") {
			t.Errorf("MCP_BASE_PATH=%q: error does not name the setting: %v", bad, err)
		}
	}
}

// serveWith starts routes(newSSE(...)) on a loopback port.
func serveWith(t *testing.T, basePath string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	sse := newSSE(buildServer(newClient("http://127.0.0.1:1")), port, basePath)
	srv := newServer(ln.Addr().String(), routes(sse, "secret"), defaultTimeouts)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String()
}

// endpointEvent opens path as an SSE stream and returns the announced
// message endpoint and the open stream.
func endpointEvent(t *testing.T, addr, path string) (string, *bufio.Reader, int) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	_, _ = c.Write([]byte("GET " + path + " HTTP/1.1\r\nHost: " + addr + "\r\nAuthorization: Bearer secret\r\nAccept: text/event-stream\r\n\r\n"))
	br := bufio.NewReader(c)
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		return "", br, resp.StatusCode
	}
	r := bufio.NewReader(resp.Body)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("GET %s: no endpoint event: %v", path, err)
		}
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, "sessionId") {
			return strings.TrimSpace(strings.TrimPrefix(line, "data: ")), r, resp.StatusCode
		}
	}
}

func TestSSEBasePath(t *testing.T) {
	addr := serveWith(t, "/mcp")

	endpoint, stream, code := endpointEvent(t, addr, "/mcp/sse")
	if code != http.StatusOK {
		t.Fatalf("GET /mcp/sse: %d", code)
	}
	if !strings.HasPrefix(endpoint, "/mcp/message?sessionId=") {
		t.Fatalf("endpoint event %q, want the relative /mcp/message?sessionId=…", endpoint)
	}
	// A round trip on the announced endpoint, resolved against the stream's
	// origin as a client does.
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://"+addr+endpoint,
		strings.NewReader(`{"jsonrpc":"2.0","id":9,"method":"ping"}`))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST %s: %d, want 202", endpoint, resp.StatusCode)
	}
	for {
		line, err := stream.ReadString('\n')
		if err != nil {
			t.Fatalf("no ping response on the stream: %v", err)
		}
		if strings.Contains(line, `"id":9`) {
			break
		}
	}

	// The root paths are gone; liveness stays at /health; the token is
	// still required under the base path.
	if _, _, code := endpointEvent(t, addr, "/sse"); code != http.StatusNotFound {
		t.Errorf("GET /sse with a base path: %d, want 404", code)
	}
	if code := statusOf(t, "http://"+addr+"/health"); code != http.StatusOK {
		t.Errorf("/health: %d", code)
	}
	if code := statusOf(t, "http://"+addr+"/mcp/sse"); code != http.StatusUnauthorized {
		t.Errorf("/mcp/sse without the token: %d, want 401", code)
	}
	// Review of #248: a valid session id is not a credential. The message
	// endpoint of a live session refuses a request without the token.
	noTok, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://"+addr+endpoint,
		strings.NewReader(`{"jsonrpc":"2.0","id":10,"method":"ping"}`))
	noTok.Header.Set("Content-Type", "application/json")
	r, err := http.DefaultClient.Do(noTok)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Errorf("POST %s without the token: %d, want 401", endpoint, r.StatusCode)
	}
}

func TestSSENoBasePathUnchanged(t *testing.T) {
	addr := serveWith(t, "")
	endpoint, _, code := endpointEvent(t, addr, "/sse")
	if code != http.StatusOK {
		t.Fatalf("GET /sse: %d", code)
	}
	_, port, _ := net.SplitHostPort(addr)
	if want := "http://0.0.0.0:" + port + "/message?sessionId="; !strings.HasPrefix(endpoint, want) {
		t.Errorf("endpoint %q, want today's %q…", endpoint, want)
	}
}

func statusOf(t *testing.T, url string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	_ = r.Body.Close()
	return r.StatusCode
}
