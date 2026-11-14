package auth

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// PAC-80 item 5 (spec docs/specs/PAC-80.md AC-20..AC-22): a panic in the
// Microsoft key fetch ends the single flight for everyone with an error,
// caches nothing, is logged once, and the next sign-in starts a fresh fetch.

// panickingTransport blocks every request until release is closed, then
// panics with value.
type panickingTransport struct {
	entered chan struct{}
	release chan struct{}
	value   string
	once    sync.Once
}

func (p *panickingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	p.once.Do(func() { close(p.entered) })
	<-p.release
	panic(p.value)
}

func TestPAC80_KeyFetchPanicEndsTheFlightForEveryone(t *testing.T) {
	b := newMSVerifierBench(t)
	tid := guid(80)
	addr := b.v.keysURL(tid)
	var logs bytes.Buffer
	var logMu sync.Mutex
	prevOut := log.Writer()
	log.SetOutput(lockedWriter{&logMu, &logs})
	t.Cleanup(func() { log.SetOutput(prevOut) })

	pt := &panickingTransport{entered: make(chan struct{}), release: make(chan struct{}), value: "pac80 transport exploded"}
	b.v.HTTPClient = &http.Client{Transport: pt}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	type outcome struct {
		err      error
		panicked any
		took     time.Duration
	}
	verify := func(out chan<- outcome) {
		var o outcome
		start := time.Now()
		defer func() {
			o.panicked = recover()
			o.took = time.Since(start)
			out <- o
		}()
		_, o.err = b.v.Verify(ctx, b.token(t, tid, nil), "client", "n")
	}
	leader, waiter := make(chan outcome, 1), make(chan outcome, 1)
	go verify(leader)
	<-pt.entered // the leader's fetch is in flight
	go verify(waiter)
	b.v.mu.Lock()
	_, inFlight := b.v.flights[addr]
	b.v.mu.Unlock()
	if !inFlight {
		t.Fatal("the leader's flight is not registered")
	}
	time.Sleep(150 * time.Millisecond) // let the waiter find the flight and block on it
	released := time.Now()
	close(pt.release)

	for name, ch := range map[string]chan outcome{"leader": leader, "waiter": waiter} {
		select {
		case o := <-ch:
			if o.panicked != nil {
				t.Errorf("%s: the panic escaped the verifier: %v", name, o.panicked)
			}
			if o.err == nil || !errors.Is(o.err, ErrMicrosoftIDToken) {
				t.Errorf("%s: err = %v, want an ID-token error", name, o.err)
			}
		case <-time.After(time.Until(released.Add(time.Second))):
			t.Fatalf("%s did not return within 1 s of the panic (it waits for its own deadline)", name)
		}
	}
	// AC-20: nothing cached; the flight is gone.
	if b.v.Cached(tid) {
		t.Fatal("keys cached after a failed fetch")
	}
	b.v.mu.Lock()
	_, stillFlying := b.v.flights[addr]
	fetches := b.v.fetches
	b.v.mu.Unlock()
	if stillFlying {
		t.Fatal("the flight is still registered")
	}
	// AC-22: logged once, with the key address and the panic value, no token.
	logMu.Lock()
	out := logs.String()
	logMu.Unlock()
	if n := strings.Count(out, "panicked"); n != 1 {
		t.Fatalf("panic logged %d times: %q", n, out)
	}
	if !strings.Contains(out, addr) || !strings.Contains(out, pt.value) {
		t.Fatalf("log line lacks the key address or the panic value: %q", out)
	}
	if strings.Contains(out, "eyJ") {
		t.Fatalf("log line carries token material: %q", out)
	}

	// AC-21: with a working client the next verification fetches afresh.
	b.v.HTTPClient = nil
	if _, err := b.v.Verify(context.Background(), b.token(t, tid, nil), "client", "n"); err != nil {
		t.Fatalf("verification after the panic: %v", err)
	}
	b.v.mu.Lock()
	after := b.v.fetches
	b.v.mu.Unlock()
	if after != fetches+1 {
		t.Fatalf("fetches = %d -> %d, want one new fetch", fetches, after)
	}
	if !b.v.Cached(tid) {
		t.Fatal("the fresh fetch was not cached")
	}
}
