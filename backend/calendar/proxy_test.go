package calendar

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Environment contract between TestNoAmbientProxy (parent) and
// TestNoAmbientProxy_Child (child).
const (
	proxyChildEnv     = "PAC43_PROXY_CHILD"
	proxyChildMarker  = "PAC43-POSITIVE-CONTROL-OBSERVED"
	proxyRecorderBody = "via-proxy-recorder"
)

// proxyVars are the conventional proxy variables, upper- and lower-case, for
// plain and TLS traffic (AC-28). On Windows the names are case-insensitive, so
// the pairs collapse into one variable each; setting both is harmless.
var proxyVars = []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"}

// TestNoAmbientProxy is AC-28.
//
// Go's http.ProxyFromEnvironment reads the proxy variables once per process
// and caches them, so setting them with t.Setenv inside this process could
// pass vacuously. Two protections, either of which satisfies spec §V3-7 item 3:
//  1. the variables are set in the child's environment before it starts;
//  2. the child first proves, in the same process and under the same
//     environment, that a client using the default proxy policy does reach
//     the recorder (positive control).
//
// The recorder R lives in this (parent) process, so the parent can see every
// connection it received, whatever the child believes.
func TestNoAmbientProxy(t *testing.T) {
	if os.Getenv(proxyChildEnv) != "" {
		return // we are the child; only TestNoAmbientProxy_Child does work here
	}

	var mu sync.Mutex
	var seen []string // "host path" of every request R received
	var conns atomic.Int32
	r := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		seen = append(seen, req.Host+" "+req.URL.Path)
		mu.Unlock()
		_, _ = io.WriteString(w, proxyRecorderBody)
	}))
	r.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	r.Start()
	defer r.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNoAmbientProxy_Child$", "-test.v", "-test.count=1")
	env := make([]string, 0, len(os.Environ())+len(proxyVars)+1)
	for _, kv := range os.Environ() {
		name := strings.ToUpper(strings.SplitN(kv, "=", 2)[0])
		switch name {
		case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "REQUEST_METHOD", "ALL_PROXY":
			continue
		}
		env = append(env, kv)
	}
	for _, v := range proxyVars {
		env = append(env, v+"="+r.URL)
	}
	env = append(env, proxyChildEnv+"=1")
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	t.Logf("child output:\n%s", out.String())
	if err != nil {
		t.Fatalf("child failed: %v", err)
	}
	if !strings.Contains(out.String(), "--- PASS: TestNoAmbientProxy_Child") {
		t.Fatal("child did not report TestNoAmbientProxy_Child as passed")
	}
	if !strings.Contains(out.String(), proxyChildMarker) {
		t.Fatal("child did not report the positive control as observed; the run is not evidence for AC-28")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || !strings.HasSuffix(seen[0], " /positive-control") {
		t.Errorf("recorder saw %v, want exactly the positive control's request", seen)
	}
	if n := conns.Load(); n != 1 {
		t.Errorf("recorder accepted %d connection(s), want exactly 1 (the positive control)", n)
	}
}

// TestNoAmbientProxy_Child is the re-executed half of TestNoAmbientProxy. Run
// directly, outside that harness, it has nothing to check and returns.
func TestNoAmbientProxy_Child(t *testing.T) {
	if os.Getenv(proxyChildEnv) == "" {
		t.Log("helper process for TestNoAmbientProxy; does work only when re-executed by it")
		return
	}

	var standInHits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasPrefix(req.URL.Path, "/calendar/v3/") {
			standInHits.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":[]}`)
	}))
	defer s.Close()
	port := s.Listener.Addr().(*net.TCPAddr).Port

	dns := newDNSDouble(t)
	// A name, never localhost or a loopback literal: the standard proxy rules
	// exempt both, so addressing S that way would pass even if the proxy were
	// honoured.
	dns.set(standInName, "127.0.0.1")
	standIn := fmt.Sprintf("http://%s:%d", standInName, port)

	// (1) Positive control: the default proxy policy, under this environment,
	// in this process, reaches the recorder.
	pc := &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}, Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, standIn+"/positive-control", nil)
	if err != nil {
		t.Fatalf("positive control request: %v", err)
	}
	resp, err := pc.Do(req)
	if err != nil {
		t.Fatalf("positive control request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != proxyRecorderBody {
		t.Fatalf("positive control did not reach the recorder (body %q); the environment is not proxied", body)
	}
	t.Log(proxyChildMarker)

	// (2) The shared path, guard on then off: S receives each request directly.
	for _, guard := range []bool{true, false} {
		t.Run(fmt.Sprintf("guard=%v", guard), func(t *testing.T) {
			before := standInHits.Load()
			d := guardedStandIn(standIn + "/calendar/v3/")
			d.Guard = guard
			configureForTest(t, d, dialHooks{resolver: dns.resolver()})
			if err := listEvents(t); err != nil {
				t.Fatalf("ListEvents: %v", err)
			}
			if got := standInHits.Load() - before; got != 1 {
				t.Errorf("stand-in received %d request(s) directly, want 1", got)
			}
		})
	}
}
