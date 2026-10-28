package main

import (
	"bytes"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func main() {
	// Health mode (PAC-81 AC-A16): `fileserver health` asks the running
	// server for / on the local port; it reads no file and no setting.
	if len(os.Args) > 1 && os.Args[1] == "health" {
		os.Exit(healthMode("http://127.0.0.1:80/"))
	}
	const root = "/srv"

	raw, err := os.ReadFile(filepath.Join(root, "index.html"))
	if err != nil {
		log.Fatal(err)
	}

	// Inject API base URL into index.html at startup so the frontend
	// SPA can read window.__API_BASE_URL__ at runtime without a rebuild.
	rootIndex := raw
	if u := os.Getenv("API_BASE_URL"); u != "" {
		script := `<script>window.__API_BASE_URL__="` + u + `";</script>`
		rootIndex = bytes.Replace(raw, []byte("</head>"), []byte(script+"</head>"), 1)
	}

	fs := http.FileServer(http.Dir(root))

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(rootIndex)
			return
		}

		p := filepath.Join(root, filepath.Clean("/"+r.URL.Path))
		if _, err := os.Stat(p); os.IsNotExist(err) {
			// SPA fallback — unknown path, let the client router handle it
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(rootIndex)
			return
		}

		fs.ServeHTTP(w, r)
	})

	log.Fatal(newServer(":80", nil).ListenAndServe())
}

// newServer is the file server with the PAC-81 server timeouts (spec D-4).
func newServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}

// healthMode exits 0 only on a 200 from url within 2 s.
func healthMode(url string) int {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
