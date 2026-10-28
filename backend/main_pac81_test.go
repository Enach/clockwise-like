package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/lifecycle"
)

// AC-A16: health mode reads only PORT (no database URL, no secret), asks
// /api/ready on the local port and exits 0 only on a 200 within 2 s.
func TestPAC81_HealthMode(t *testing.T) {
	status := http.StatusOK
	var path string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.WriteHeader(status)
	}))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv.Listener = ln
	srv.Start()
	defer srv.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	read := map[string]bool{}
	env := func(k string) string {
		read[k] = true
		if k == "PORT" {
			return port
		}
		return ""
	}
	if code := healthMode(env); code != 0 || path != "/api/ready" {
		t.Fatalf("ready server: exit %d, path %q", code, path)
	}
	status = http.StatusServiceUnavailable
	if code := healthMode(env); code != 1 {
		t.Fatalf("not ready: exit %d", code)
	}
	for k := range read {
		if k != "PORT" {
			t.Fatalf("health mode read %s", k)
		}
	}
	srv.Close()
	start := time.Now()
	if code := healthMode(env); code != 1 || time.Since(start) > 3*time.Second {
		t.Fatalf("no server: exit %d after %s", code, time.Since(start))
	}
}

// AC-A6: a cron run is a tracked unit with the group's context, and no new
// run starts once shutdown has begun.
func TestPAC81_CronJobTrackedAndStopped(t *testing.T) {
	g := lifecycle.NewGroup()
	defer lifecycle.SetDefault(g)()
	release := make(chan struct{})
	started := make(chan struct{})
	job := cronJob("personal-blocker", func(ctx context.Context) {
		close(started)
		<-release
	})
	go job()
	<-started
	if p := g.Pending(); len(p) != 1 || p[0].String() != "cron personal-blocker" {
		t.Fatalf("pending %v", p)
	}
	close(release)
	if !g.Wait(context.Background()) {
		t.Fatal("not idle")
	}
	g.BeginShutdown()
	ran := false
	cronJob("x", func(context.Context) { ran = true })()
	if ran {
		t.Fatal("a cron run started after shutdown began")
	}
}
