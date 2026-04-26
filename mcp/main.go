package main

import (
	"fmt"
	"log"
	"os"

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
		port := os.Getenv("MCP_PORT")
		if port == "" {
			port = "3001"
		}
		sseServer := server.NewSSEServer(s, server.WithBaseURL(fmt.Sprintf("http://0.0.0.0:%s", port)))
		log.Printf("MCP SSE server listening on :%s", port)
		if err := sseServer.Start(":" + port); err != nil {
			log.Fatal(err)
		}
	} else {
		if err := server.ServeStdio(s); err != nil {
			log.Fatal(err)
		}
	}
}
