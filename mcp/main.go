package main

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/server"
)

func main() {
	// Health mode (PAC-81 AC-A16): `mcp health` asks the running SSE
	// server's liveness on the local port; no token, no backend call.
	if len(os.Args) > 1 && os.Args[1] == "health" {
		os.Exit(healthMode(os.Getenv))
	}
	baseURL := os.Getenv("BACKEND_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}

	client := newClient(baseURL)
	s := buildServer(client)

	transport := os.Getenv("MCP_TRANSPORT")
	if transport == "sse" {
		// SSE transport listens on a TCP port; without auth, any process on
		// the network can call MCP tools and proxy through to the backend.
		// Require MCP_AUTH_TOKEN at startup (same fatal-on-empty pattern as
		// JWT_SECRET in the backend). Stdio transport is trust-stdin.
		token := os.Getenv("MCP_AUTH_TOKEN")
		if token == "" {
			log.Fatal("MCP_AUTH_TOKEN environment variable is required when MCP_TRANSPORT=sse")
		}

		port := os.Getenv("MCP_PORT")
		if port == "" {
			port = "3001"
		}
		sseServer := server.NewSSEServer(s, server.WithBaseURL(fmt.Sprintf("http://0.0.0.0:%s", port)))
		log.Printf("MCP SSE server listening on :%s (auth required)", port)
		if err := newServer(":"+port, routes(sseServer, token), defaultTimeouts).ListenAndServe(); err != nil {
			log.Fatal(err)
		}
	} else {
		if err := server.ServeStdio(s); err != nil {
			log.Fatal(err)
		}
	}
}

// bearerAuth wraps next with a constant-time `Authorization: Bearer <token>` check.
// Returns 401 on mismatch. Constant-time compare prevents timing oracles.
func bearerAuth(next http.Handler, token string) http.Handler {
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(got, prefix) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if subtle.ConstantTimeCompare([]byte(got[len(prefix):]), want) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// timeouts are the server bounds of PAC-81 (spec D-4).
type timeouts struct{ readHeader, read, write, idle time.Duration }

var defaultTimeouts = timeouts{readHeader: 10 * time.Second, read: 30 * time.Second, write: 120 * time.Second, idle: 120 * time.Second}

// newServer is the SSE transport's server with its timeouts (AC-A16).
func newServer(addr string, h http.Handler, t timeouts) *http.Server {
	return &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: t.readHeader, ReadTimeout: t.read, WriteTimeout: t.write, IdleTimeout: t.idle}
}

// routes serves an unauthenticated liveness at /health and everything else
// behind the bearer token. The SSE stream clears its own read and write
// deadlines: an open, idle stream is never cut by a server bound.
func routes(sse http.Handler, token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.Handle("/", bearerAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/sse") {
			rc := http.NewResponseController(w)
			_ = rc.SetWriteDeadline(time.Time{})
			_ = rc.SetReadDeadline(time.Time{})
		}
		sse.ServeHTTP(w, r)
	}), token))
	return mux
}

// healthMode is the container health check: GET /health on the local MCP
// port, success only on a 200 within 2 s.
func healthMode(getenv func(string) string) int {
	port := getenv("MCP_PORT")
	if port == "" {
		port = "3001"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/health", nil)
	if err != nil {
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
