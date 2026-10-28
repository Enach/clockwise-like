// Package outboundtest provides a stub dependency that accepts connections
// and never answers (PAC-81 AC-A3), for tests in any package.
package outboundtest

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Hang is a TCP server that accepts every connection, reads what it is sent
// and never writes. It counts connections still open.
type Hang struct {
	ln       net.Listener
	open     atomic.Int32
	accepted atomic.Int32
	wg       sync.WaitGroup
}

// NewHang starts a hanging server; it is closed with the test.
func NewHang(t *testing.T) *Hang {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := &Hang{ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			h.open.Add(1)
			h.accepted.Add(1)
			h.wg.Add(1)
			go func() {
				defer h.wg.Done()
				defer h.open.Add(-1)
				defer c.Close()
				buf := make([]byte, 4096)
				for {
					if _, err := c.Read(buf); err != nil {
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return h
}

// Addr is host:port.
func (h *Hang) Addr() string { return h.ln.Addr().String() }

// URL is http://host:port.
func (h *Hang) URL() string { return "http://" + h.Addr() }

// Accepted is the number of connections accepted so far.
func (h *Hang) Accepted() int { return int(h.accepted.Load()) }

// AssertClosed fails the test unless every accepted connection was closed by
// the client within a second.
func (h *Hang) AssertClosed(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for h.open.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := h.open.Load(); n != 0 {
		t.Fatalf("%d connection(s) to the stub still open after the call failed", n)
	}
	if h.accepted.Load() == 0 {
		t.Fatal("the stub was never called")
	}
}

// Within fails the test unless call returns an error within limit + 1 s,
// and then that the stub's connections were closed.
func (h *Hang) Within(t *testing.T, limit time.Duration, call func() error) {
	t.Helper()
	start := time.Now()
	err := call()
	took := time.Since(start)
	if err == nil {
		t.Fatal("call against a stub that never answers succeeded")
	}
	if took > limit+time.Second {
		t.Fatalf("call failed after %s, want <= %s (limit %s + 1 s): %v", took, limit+time.Second, limit, err)
	}
	h.AssertClosed(t)
}
