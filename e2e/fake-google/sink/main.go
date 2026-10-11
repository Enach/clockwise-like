// Command sink is the e2e stack's recording sink (PAC-43 AC-23, AC-11).
//
// Inside the backend container every real provider host name
// (www.googleapis.com, oauth2.googleapis.com, accounts.google.com, ...) maps to
// this service's fixed address. It listens where those hosts would be — :80
// and :443 — records every connection it is offered, and closes it. It serves
// nothing. A run whose sink log is non-empty is a run in which something tried
// to reach a real provider, and the log says what and from where.
//
// For :443 it records the TLS server name the client asked for (it never
// completes a handshake). For :80 it reads the request head, so it can record
// whether a credential was presented, but never the credential itself.
//
// The log is read from the host on the control port (:9090, published on
// 127.0.0.1 only): GET /connections.
package main

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

// Connection is one connection the sink was offered.
type Connection struct {
	At       time.Time `json:"at"`
	Listener string    `json:"listener"`
	Peer     string    `json:"peer"`
	// SNI is the server name of a TLS ClientHello, when one arrived.
	SNI string `json:"sni,omitempty"`
	// Method, Host and Path describe a plain-HTTP request head, when one arrived.
	Method string `json:"method,omitempty"`
	Host   string `json:"host,omitempty"`
	Path   string `json:"path,omitempty"`
	// CredentialPresented is true when a plain-HTTP request carried an
	// Authorization header. The value is never recorded.
	CredentialPresented bool `json:"credential_presented"`
}

type recorder struct {
	mu    sync.Mutex
	conns []Connection
}

func (r *recorder) add(c Connection) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.conns = append(r.conns, c)
	log.Printf("sink: connection on %s from %s sni=%q host=%q path=%q credential=%v",
		c.Listener, c.Peer, c.SNI, c.Host, c.Path, c.CredentialPresented)
}

func (r *recorder) all() []Connection {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Connection, len(r.conns))
	copy(out, r.conns)
	return out
}

var errSink = errors.New("sink: recorded and refused")

func main() {
	rec := &recorder{}
	httpAddr := envOr("SINK_HTTP_ADDR", ":80")
	tlsAddr := envOr("SINK_TLS_ADDR", ":443")
	ctlAddr := envOr("SINK_CONTROL_ADDR", ":9090")
	// FROM scratch: the compose healthcheck runs `sink -healthcheck`. It asks
	// the control port, never :80/:443, so it never appears in the log.
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		os.Exit(healthcheck("http://127.0.0.1" + ctlAddr + "/health"))
	}
	// `sink -probe URL` sends one credentialed GET to URL and exits. AC-23's
	// positive control runs it from a throwaway container mapped like the
	// backend, to prove the sink records what reaches it.
	if len(os.Args) > 2 && os.Args[1] == "-probe" {
		os.Exit(probe(os.Args[2]))
	}

	go serve(httpAddr, func(c net.Conn) { handlePlain(rec, httpAddr, c) })
	go serve(tlsAddr, func(c net.Conn) { handleTLS(rec, tlsAddr, c) })

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("GET /connections", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rec.all())
	})
	srv := &http.Server{Addr: ctlAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("sink: recording on %s and %s, control on %s", httpAddr, tlsAddr, ctlAddr)
	log.Fatal(srv.ListenAndServe())
}

func healthcheck(url string) int {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return 1
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func probe(url string) int {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		log.Printf("sink probe: %v", err)
		return 2
	}
	req.Header.Set("Authorization", "Bearer sink-probe-not-a-credential")
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		log.Printf("sink probe: %v", err)
		return 1
	}
	_ = resp.Body.Close()
	log.Printf("sink probe: %s answered %d", url, resp.StatusCode)
	return 0
}

func serve(addr string, handle func(net.Conn)) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("sink: listen %s: %v", addr, err)
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			log.Printf("sink: accept on %s: %v", addr, err)
			continue
		}
		go handle(c)
	}
}

func handlePlain(rec *recorder, listener string, c net.Conn) {
	defer c.Close()
	conn := Connection{At: time.Now().UTC(), Listener: listener, Peer: c.RemoteAddr().String()}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if req, err := http.ReadRequest(bufio.NewReader(c)); err == nil {
		conn.Method = req.Method
		conn.Host = req.Host
		conn.Path = req.URL.Path
		conn.CredentialPresented = req.Header.Get("Authorization") != ""
	}
	rec.add(conn)
	_, _ = c.Write([]byte("HTTP/1.1 503 Service Unavailable\r\nConnection: close\r\nContent-Length: 0\r\n\r\n"))
}

func handleTLS(rec *recorder, listener string, c net.Conn) {
	defer c.Close()
	conn := Connection{At: time.Now().UTC(), Listener: listener, Peer: c.RemoteAddr().String()}
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	srv := tls.Server(c, &tls.Config{
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			conn.SNI = hello.ServerName
			return nil, errSink
		},
	})
	_ = srv.Handshake()
	rec.add(conn)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
