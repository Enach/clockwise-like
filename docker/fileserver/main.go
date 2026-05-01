package main

import (
	"bytes"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

func main() {
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

	log.Fatal(http.ListenAndServe(":80", nil))
}
