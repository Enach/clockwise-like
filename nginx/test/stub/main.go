// Command stub is the test double for PAC-81 Part B's nginx integration tests.
//
// It runs inside a container on the test network, in one of two modes:
//
//	stub upstream   two recording upstreams (backend :8080, frontend :8081) and
//	                a control port (:9000) that reports what they received and
//	                how many TCP connections each accepted.
//	stub client     sends HTTP requests from the container's own address (so
//	                the test controls the peer address nginx sees) and prints
//	                the responses as JSON on stdout.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: stub upstream|client [flags]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "upstream":
		runUpstream()
	case "client":
		os.Exit(runClient(os.Args[2:]))
	default:
		fmt.Fprintln(os.Stderr, "unknown mode", os.Args[1])
		os.Exit(2)
	}
}

// Recorded is one request as an upstream received it.
type Recorded struct {
	Upstream string              `json:"upstream"`
	Method   string              `json:"method"`
	URI      string              `json:"uri"`
	Proto    string              `json:"proto"`
	Header   map[string][]string `json:"header"`
	BodyLen  int                 `json:"body_len"`
}

type recorder struct {
	mu   sync.Mutex
	reqs []Recorded
}

func (r *recorder) add(rec Recorded) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = append(r.reqs, rec)
}

func (r *recorder) snapshot() []Recorded {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Recorded{}, r.reqs...)
}

func runUpstream() {
	rec := &recorder{}
	var backendConns, frontendConns atomic.Int64

	serve := func(addr string, conns *atomic.Int64, h http.Handler) {
		srv := &http.Server{
			Addr:    addr,
			Handler: h,
			ConnState: func(_ net.Conn, s http.ConnState) {
				if s == http.StateNew {
					conns.Add(1)
				}
			},
		}
		go func() {
			if err := srv.ListenAndServe(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}()
	}

	record := func(name string, r *http.Request) {
		n, _ := io.Copy(io.Discard, r.Body)
		rec.add(Recorded{
			Upstream: name, Method: r.Method, URI: r.RequestURI, Proto: r.Proto,
			Header: r.Header.Clone(), BodyLen: int(n),
		})
	}

	serve(":8080", &backendConns, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		record("backend", r)
		path := r.URL.Path
		switch {
		case strings.HasPrefix(path, "/api/sleep/"):
			d, err := time.ParseDuration(strings.TrimPrefix(path, "/api/sleep/"))
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			time.Sleep(d)
			writeJSON(w, http.StatusOK, map[string]string{"slept": d.String()})
		case strings.HasPrefix(path, "/api/status/"):
			code, _ := strconv.Atoi(strings.TrimPrefix(path, "/api/status/"))
			writeJSON(w, code, map[string]string{"error": "stub error"})
		case path == "/api/leaky":
			// An upstream that sets its own copies of the edge headers; nginx
			// must replace them, not add a second copy.
			w.Header().Set("X-Frame-Options", "SAMEORIGIN")
			w.Header().Set("Content-Security-Policy", "default-src *")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Referrer-Policy", "unsafe-url")
			w.Header().Set("Strict-Transport-Security", "max-age=1")
			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		case strings.HasPrefix(path, "/api/redirect"):
			http.Redirect(w, r, "/api/health", http.StatusFound)
		default:
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		}
	}))

	serve(":8081", &frontendConns, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		record("frontend", r)
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = io.WriteString(w, "console.log('stub asset');\n")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><html><head></head><body><div id="root"></div></body></html>`)
	}))

	ctl := http.NewServeMux()
	ctl.HandleFunc("/requests", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, rec.snapshot())
	})
	ctl.HandleFunc("/conns", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]int64{
			"backend": backendConns.Load(), "frontend": frontendConns.Load(),
		})
	})
	if err := http.ListenAndServe(":9000", ctl); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// Response is one response as the client saw it.
type Response struct {
	Status int                 `json:"status"`
	Header map[string][]string `json:"header"`
	Body   string              `json:"body"`
	Error  string              `json:"error,omitempty"`
}

type headerFlags []string

func (h *headerFlags) String() string     { return strings.Join(*h, ", ") }
func (h *headerFlags) Set(v string) error { *h = append(*h, v); return nil }

func runClient(args []string) int {
	fs := flag.NewFlagSet("client", flag.ContinueOnError)
	method := fs.String("method", http.MethodGet, "request method")
	bodySize := fs.Int("body-size", -1, "send a body of this many bytes (-1: none)")
	count := fs.Int("n", 1, "send the request this many times, sequentially")
	timeout := fs.Duration("timeout", 30*time.Second, "per-request timeout")
	var headers headerFlags
	fs.Var(&headers, "H", "header 'Name: value' (repeatable)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: stub client [flags] URL")
		return 2
	}
	url := fs.Arg(0)

	client := &http.Client{
		Timeout: *timeout,
		// Report redirects as they are; never follow them.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	out := make([]Response, 0, *count)
	for i := 0; i < *count; i++ {
		var body io.Reader
		if *bodySize >= 0 {
			body = bytes.NewReader(bytes.Repeat([]byte("a"), *bodySize))
		}
		req, err := http.NewRequestWithContext(context.Background(), *method, url, body)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		for _, h := range headers {
			k, v, ok := strings.Cut(h, ":")
			if !ok {
				fmt.Fprintln(os.Stderr, "bad header", h)
				return 2
			}
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			if strings.EqualFold(k, "Host") {
				req.Host = v
				continue
			}
			req.Header.Add(k, v)
		}
		res, err := client.Do(req)
		if err != nil {
			out = append(out, Response{Error: err.Error()})
			continue
		}
		b, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		out = append(out, Response{Status: res.StatusCode, Header: res.Header, Body: string(b)})
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
