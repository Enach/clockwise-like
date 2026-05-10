package main

import (
	"crypto/subtle"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/server"
)

func main() {
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
		if err := http.ListenAndServe(":"+port, bearerAuth(sseServer, token)); err != nil {
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
