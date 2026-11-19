// Command denyproxy is the deny-all HTTP proxy the backend test suite runs
// behind (PAC-139). scripts/test-backend.sh points HTTP_PROXY and HTTPS_PROXY
// at it for the `go test` invocation only, with NO_PROXY covering loopback, so
// that a test whose code reaches a real host (a token exchange, a webhook, a
// provider API) fails loudly instead of quietly calling the internet.
//
// Every request, plain or CONNECT, is answered 403 and recorded as one line,
// "<METHOD> <host>", in the -log file. The script fails the run when that file
// is not empty after the suite, even if every test passed: a test that
// swallows the error would otherwise hide the call.
//
// Clients whose transport sets Proxy to nil (the calendar transport, the
// step-up client) dial directly and are not seen here; they carry their own
// peer guards.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

// handler answers every request 403 and appends "<METHOD> <host>" to out.
// A request it cannot record calls fatal (os.Exit in main): an unrecorded
// request would let "0 denied" pass vacuously, and the script's liveness
// check after the suite catches a dead proxy.
type handler struct {
	mu    sync.Mutex
	out   *os.File
	fatal func(error)
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if host == "" && r.URL != nil {
		host = r.URL.Host
	}
	line := fmt.Sprintf("%s %s\n", r.Method, host)
	h.mu.Lock()
	_, err := h.out.WriteString(line)
	h.mu.Unlock()
	if err != nil {
		h.fatal(fmt.Errorf("could not record %q: %w", line, err))
		return
	}
	log.Printf("denyproxy: denied %s %s", r.Method, host)
	http.Error(w, "denyproxy: outbound calls are not allowed from the test suite (PAC-139): "+host, http.StatusForbidden)
}

func run(addrFile, logFile string) error {
	out, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	// Written after Listen succeeds, so a reader that sees the file can connect.
	if err := os.WriteFile(addrFile, []byte(ln.Addr().String()), 0o644); err != nil {
		return err
	}
	fatal := func(err error) {
		log.Printf("denyproxy: %v", err)
		os.Exit(3)
	}
	srv := &http.Server{Handler: &handler{out: out, fatal: fatal}, ReadHeaderTimeout: 10 * time.Second}
	return srv.Serve(ln)
}

func main() {
	addrFile := flag.String("addr-file", "", "file the listening address (host:port) is written to")
	logFile := flag.String("log", "", "file every denied request is appended to, one line each")
	flag.Parse()
	if *addrFile == "" || *logFile == "" {
		fmt.Fprintln(os.Stderr, "usage: denyproxy -addr-file FILE -log FILE")
		os.Exit(2)
	}
	if err := run(*addrFile, *logFile); err != nil {
		log.Fatalf("denyproxy: %v", err)
	}
}
