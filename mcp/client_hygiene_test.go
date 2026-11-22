package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Client hygiene (security audit of 2026-11-22, riders). Neither of these is
// a vulnerability today — every tool targets a requireAuth backend route and
// no PATCH or DELETE route exists outside the authenticated group, so a
// steered request answers 401 — but both are a latent one and cheap to close.

// The backend client is bounded, so a backend that accepts a connection and
// never answers cannot hang a tool call for as long as the MCP client waits.
func TestBackendClientIsBounded(t *testing.T) {
	c := newClient("http://127.0.0.1:1")
	if c.http.Timeout != backendCallTimeout {
		t.Fatalf("client Timeout = %s, want %s", c.http.Timeout, backendCallTimeout)
	}
	if backendCallTimeout <= 0 {
		t.Fatal("backendCallTimeout is not a bound")
	}
}

// A hanging backend fails the call within the bound even with a context that
// never ends.
func TestBackendCallEndsWithoutContextDeadline(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-block
	}))
	defer func() { close(block); srv.Close() }()

	c := newClient(srv.URL)
	c.http.Timeout = 200 * time.Millisecond
	start := time.Now()
	if _, err := c.get(context.Background(), "/api/settings", nil); err == nil {
		t.Fatal("a call to a backend that never answers succeeded")
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("the call took %s, want it bounded by the client", took)
	}
}

// An id is taken from the tool call, so it is escaped before it becomes a
// path segment: "../book/slug" must not steer the request to another route.
func TestEventIDCannotSteerThePath(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.EscapedPath())
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newClient(srv.URL)
	for _, id := range []string{"../book/slug", "..%2fbook", "abc/../../admin", "a b", "x?y=1", "x#y"} {
		if _, err := c.patch(context.Background(), "/api/events/"+pathSegment(id), map[string]string{"title": "t"}); err != nil {
			t.Fatalf("patch with id %q: %v", id, err)
		}
	}
	for _, p := range paths {
		if !strings.HasPrefix(p, "/api/events/") {
			t.Errorf("request path %q left /api/events/", p)
		}
		if strings.Contains(p[len("/api/events/"):], "/") {
			t.Errorf("request path %q has more than one segment after /api/events/", p)
		}
	}
	if len(paths) != 6 {
		t.Fatalf("%d request(s) reached the backend, want 6", len(paths))
	}
}

// TestNoUnboundedHTTPClient (backend/internal/outbound) walks backend/ only,
// which is why the unbounded client here went unnoticed. This is the same
// guard over mcp/.
func TestNoUnboundedHTTPClientInMCP(t *testing.T) {
	literal := regexp.MustCompile(`http\.Client\{`)
	forbidden := regexp.MustCompile(`http\.(Get|Post|Head|PostForm)\(|new\(http\.Client\)`)
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		checked++
		if strings.Contains(src, "http.DefaultClient") {
			t.Errorf("%s uses http.DefaultClient (no overall timeout)", name)
		}
		for _, m := range forbidden.FindAllString(src, -1) {
			t.Errorf("%s: %s has no overall timeout", name, m)
		}
		for _, loc := range literal.FindAllStringIndex(src, -1) {
			end := strings.Index(src[loc[1]:], "}")
			if end < 0 {
				t.Fatalf("%s: unterminated http.Client literal", name)
			}
			if body := src[loc[1] : loc[1]+end]; !strings.Contains(body, "Timeout") {
				t.Errorf("%s: an http.Client without a Timeout: %q", name, body)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no Go source was checked")
	}
}
