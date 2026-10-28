package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// runtimeConfigPath is the same-origin script that carries runtime settings to
// the SPA (PAC-81 Part B, spec Q-P2). nginx's CSP is `script-src 'self'` with
// no 'unsafe-inline', so the API base cannot be an inline <script>; a hash
// would have to be recomputed byte-for-byte in the nginx container, while a
// same-origin script needs no coupling at all.
const runtimeConfigPath = "/runtime-config.js"

func main() {
	// Health mode (PAC-81 Part A, AC-A16): `fileserver health` asks the
	// running server for / on the local port; it reads no file and no setting.
	if len(os.Args) > 1 && os.Args[1] == "health" {
		os.Exit(healthMode("http://127.0.0.1:80/"))
	}
	h, err := newHandler("/srv", os.Getenv("API_BASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	log.Fatal(newServer(":80", h).ListenAndServe())
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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

// newHandler serves the SPA in root. When apiBase is non-empty, every HTML
// response loads runtimeConfigPath before the bundle, and that script sets
// window.__API_BASE_URL__ so the SPA reads it at runtime without a rebuild.
func newHandler(root, apiBase string) (http.Handler, error) {
	raw, err := os.ReadFile(filepath.Join(root, "index.html"))
	if err != nil {
		return nil, err
	}
	// The script tag goes before </head>. A bundle without one would serve a
	// page that silently ignores API_BASE_URL, so refuse to start instead.
	if !bytes.Contains(raw, []byte("</head>")) {
		return nil, fmt.Errorf("%s has no </head>: cannot inject the runtime configuration script", filepath.Join(root, "index.html"))
	}

	rootIndex := raw
	runtimeConfig := []byte("// no runtime configuration\n")
	if apiBase != "" {
		// json.Marshal escapes quotes, backslashes and <, >, & — the value
		// cannot end the string literal or the script element.
		v, err := json.Marshal(apiBase)
		if err != nil {
			return nil, err
		}
		runtimeConfig = []byte(fmt.Sprintf("window.__API_BASE_URL__=%s;\n", v))
		// A classic script in <head> runs before the module bundle, exactly
		// as the inline script it replaces did.
		tag := `<script src="` + runtimeConfigPath + `"></script>`
		rootIndex = bytes.Replace(raw, []byte("</head>"), []byte(tag+"</head>"), 1)
	}

	writeIndex := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// The HTML carries the runtime setting; never serve a stale copy.
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(rootIndex)
	}

	fs := http.FileServer(http.Dir(root))
	mux := http.NewServeMux()

	mux.HandleFunc(runtimeConfigPath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(runtimeConfig)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			writeIndex(w)
			return
		}

		p := filepath.Join(root, filepath.Clean("/"+r.URL.Path))
		if _, err := os.Stat(p); os.IsNotExist(err) {
			// SPA fallback — unknown path, let the client router handle it
			writeIndex(w)
			return
		}

		fs.ServeHTTP(w, r)
	})

	return mux, nil
}
