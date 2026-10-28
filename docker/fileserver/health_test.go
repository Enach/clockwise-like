package main

import (
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServerTimeouts(t *testing.T) {
	s := newServer(":0", nil)
	if s.ReadHeaderTimeout != 10*time.Second || s.ReadTimeout != 30*time.Second || s.WriteTimeout != 120*time.Second || s.IdleTimeout != 120*time.Second {
		t.Fatalf("%+v", s)
	}
}

// AC-A16: slow headers are cut at the header bound.
func TestSlowHeadersClosed(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := newServer(ln.Addr().String(), http.NotFoundHandler())
	s.ReadHeaderTimeout = 300 * time.Millisecond
	go func() { _ = s.Serve(ln) }()
	defer s.Close()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	start := time.Now()
	go func() {
		for _, b := range []byte("GET / HTTP/1.1\r\nHost: x\r\nX-Slow: aaaaaaaaaaaaaaaaaaaaaaa") {
			if _, err := c.Write([]byte{b}); err != nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _ = io.Copy(io.Discard, c)
	if took := time.Since(start); took > s.ReadHeaderTimeout+time.Second {
		t.Fatalf("closed after %s", took)
	}
}

// AC-A16: health mode exits 0 only on a 200 within 2 s.
func TestHealthMode(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	status := http.StatusOK
	s := newServer(ln.Addr().String(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
	go func() { _ = s.Serve(ln) }()
	defer s.Close()
	url := "http://" + ln.Addr().String() + "/"
	if code := healthMode(url); code != 0 {
		t.Fatalf("exit %d", code)
	}
	status = http.StatusInternalServerError
	if code := healthMode(url); code != 1 {
		t.Fatalf("500: exit %d", code)
	}
	if code := healthMode("http://127.0.0.1:1/"); code != 1 {
		t.Fatalf("no server: exit %d", code)
	}
}
