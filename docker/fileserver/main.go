package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
)

func main() {
	const root = "/srv"
	fs := http.FileServer(http.Dir(root))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(root, filepath.Clean("/"+r.URL.Path))
		if _, err := os.Stat(p); os.IsNotExist(err) {
			http.ServeFile(w, r, filepath.Join(root, "index.html"))
			return
		}
		fs.ServeHTTP(w, r)
	})
	log.Fatal(http.ListenAndServe(":80", nil))
}
