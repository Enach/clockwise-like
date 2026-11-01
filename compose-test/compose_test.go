package composetest

// PAC-81 Part C — compose integration tests. Each test names the acceptance
// criterion of docs/specs/NEW-deploy-hardening.md (revision 3.3) it
// falsifies.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const mcpToken = "compose-test-mcp-token-0123456789abcdef"

// The spec requires these tests to run under Compose v2 and to record the
// version.
func TestComposeVersion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, errOut, err := run(ctx, "", nil, "docker", "compose", "version", "--short")
	if err != nil {
		t.Fatalf("docker compose version: %v %s", err, errOut)
	}
	v := strings.TrimPrefix(strings.TrimSpace(out), "v")
	t.Logf("Docker Compose version %s", v)
	major, err := strconv.Atoi(strings.SplitN(v, ".", 2)[0])
	if err != nil || major < 2 {
		t.Fatalf("Docker Compose %q: these tests need the v2 (Go) implementation or later", v)
	}
}

type rendered struct {
	Services map[string]struct {
		Image string `json:"image"`
		Ports []struct {
			HostIP    string `json:"host_ip"`
			Published string `json:"published"`
			Target    int    `json:"target"`
		} `json:"ports"`
		Environment map[string]*string `json:"environment"`
	} `json:"services"`
}

func render(t *testing.T, p *project) (rendered, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, errOut, err := p.compose(ctx, "config", "--format", "json")
	var r rendered
	if err == nil {
		if jerr := json.Unmarshal([]byte(out), &r); jerr != nil {
			t.Fatalf("config json: %v", jerr)
		}
	}
	return r, errOut, err
}

// AC-C2, AC-C4 (MCP off renders without its token), AC-C9.
func TestRenderRequiresVersion(t *testing.T) {
	s := baseSettings(80)
	delete(s, "NGINX_BIND_ADDRESS")
	delete(s, "NGINX_HTTP_PORT")

	noVersion := baseSettings(80)
	delete(noVersion, "PACEDAY_VERSION")
	if _, errOut, err := render(t, newProject(t, noVersion)); err == nil || !strings.Contains(errOut, "PACEDAY_VERSION") {
		t.Errorf("rendering without PACEDAY_VERSION: err=%v, want a failure naming it; stderr:\n%s", err, errOut)
	}
	empty := baseSettings(80)
	empty["PACEDAY_VERSION"] = str("")
	if _, errOut, err := render(t, newProject(t, empty)); err == nil || !strings.Contains(errOut, "PACEDAY_VERSION") {
		t.Errorf("rendering with an empty PACEDAY_VERSION: err=%v, want a failure naming it", err)
	}

	r, errOut, err := render(t, newProject(t, s))
	if err != nil {
		t.Fatalf("rendering with PACEDAY_VERSION and no MCP_AUTH_TOKEN: %v\n%s", err, errOut)
	}
	exact := regexp.MustCompile(`^(nginx|postgres):\d+\.\d+(\.\d+)?-alpine$`)
	for name, svc := range r.Services {
		switch {
		case strings.HasPrefix(svc.Image, "enach/paceday-"):
			if !strings.HasSuffix(svc.Image, ":"+version) {
				t.Errorf("%s: image %s, want tag %s", name, svc.Image, version)
			}
		case exact.MatchString(svc.Image):
		default:
			t.Errorf("%s: image %s is not an exact version", name, svc.Image)
		}
	}
	if _, ok := r.Services["mcp-server"]; ok {
		t.Error("an MCP service is rendered without docker-compose.mcp.yml")
	}
	if _, ok := r.Services["docs"]; ok {
		t.Error("a docs service is rendered (AC-C8)")
	}
	if got := r.Services["backend"].Environment["SENTRY_RELEASE"]; got == nil || *got != version {
		t.Errorf("SENTRY_RELEASE = %v, want the image version %s", got, version)
	}

	// AC-C9: default every interface on port 80; the operator's address.
	ports := r.Services["nginx"].Ports
	if len(ports) != 1 || ports[0].HostIP != "" || ports[0].Published != "80" || ports[0].Target != 80 {
		t.Errorf("nginx ports by default %+v, want 80 on every interface", ports)
	}
	for name, svc := range r.Services {
		if name != "nginx" && len(svc.Ports) > 0 {
			t.Errorf("%s publishes %+v", name, svc.Ports)
		}
	}
	bound := baseSettings(18555)
	r2, errOut, err := render(t, newProject(t, bound))
	if err != nil {
		t.Fatalf("render: %v %s", err, errOut)
	}
	if p := r2.Services["nginx"].Ports; len(p) != 1 || p[0].HostIP != "127.0.0.1" || p[0].Published != "18555" {
		t.Errorf("nginx ports with NGINX_BIND_ADDRESS=127.0.0.1 NGINX_HTTP_PORT=18555: %+v", p)
	}
	// Review of #248: an IPv6 address is written in brackets
	// (.env.example), which every Compose v2 parses as the host address.
	for addr, want := range map[string]string{"[::1]": "::1", "[2001:db8::5]": "2001:db8::5", "[::]": "::"} {
		v6 := baseSettings(18556)
		v6["NGINX_BIND_ADDRESS"] = str(addr)
		r3, errOut, err := render(t, newProject(t, v6))
		if err != nil {
			t.Fatalf("render with NGINX_BIND_ADDRESS=%s: %v %s", addr, err, errOut)
		}
		if p := r3.Services["nginx"].Ports; len(p) != 1 || p[0].HostIP != want || p[0].Published != "18556" || p[0].Target != 80 {
			t.Errorf("NGINX_BIND_ADDRESS=%s: nginx ports %+v, want %s:18556->80", addr, p, want)
		}
	}
}

// AC-C5: MCP enabled without a token fails at config and at up, naming the
// variable, and creates no container that could restart in a loop.
func TestMCPTokenRequired(t *testing.T) {
	images(t)
	for what, token := range map[string]*string{"unset": nil, "empty": str("")} {
		s := baseSettings(freePort(t))
		s["MCP_AUTH_TOKEN"] = token
		p := newProject(t, s, "docker-compose.mcp.yml")
		if _, errOut, err := render(t, p); err == nil || !strings.Contains(errOut, "MCP_AUTH_TOKEN") {
			t.Errorf("%s token: config err=%v, want a failure naming MCP_AUTH_TOKEN\n%s", what, err, errOut)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		_, errOut, err := p.compose(ctx, "up", "-d")
		cancel()
		if err == nil || !strings.Contains(errOut, "MCP_AUTH_TOKEN") {
			t.Errorf("%s token: up err=%v, want a failure naming MCP_AUTH_TOKEN\n%s", what, err, errOut)
		}
		t.Logf("%s token: up refused: %s", what, strings.TrimSpace(errOut))
		if c := p.containers(t); len(c) != 0 {
			t.Errorf("%s token: containers were created: %v", what, c)
		}
	}
}

func get(t *testing.T, url string, header ...string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	c := &http.Client{Timeout: 30 * time.Second}
	res, err := c.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

// AC-C3, AC-C4 (MCP off), AC-C6 (stop during a request), Q-O2 on the real
// stack.
func TestProductionStack(t *testing.T) {
	images(t)
	port := freePort(t)
	p := newProject(t, baseSettings(port))
	start := time.Now()
	p.mustCompose(t, 6*time.Minute, "up", "-d", "--wait", "--wait-timeout", "300")
	t.Logf("stack healthy after %s", time.Since(start).Round(time.Second))
	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	c := p.containers(t)
	t.Run("every service healthy, no MCP", func(t *testing.T) {
		for _, svc := range []string{"postgres", "backend", "frontend", "nginx"} {
			id, ok := c[svc]
			if !ok {
				t.Errorf("no %s container", svc)
				continue
			}
			if h := health(t, id); h != "healthy" {
				t.Errorf("%s: health %s", svc, h)
			}
		}
		if _, ok := c["mcp-server"]; ok {
			t.Error("an MCP container runs without docker-compose.mcp.yml")
		}
		if len(c) != 4 {
			t.Errorf("containers %v, want exactly postgres, backend, frontend, nginx", c)
		}
	})

	t.Run("through nginx", func(t *testing.T) {
		if res, _ := get(t, base+"/nginx-health"); res.StatusCode != 200 {
			t.Errorf("/nginx-health: %d", res.StatusCode)
		}
		if res, body := get(t, base+"/api/ready"); res.StatusCode != 200 || !strings.Contains(body, "ready") {
			t.Errorf("/api/ready: %d %s", res.StatusCode, body)
		}
		if res, body := get(t, base+"/mcp/sse", "Authorization", "Bearer "+mcpToken); res.StatusCode != 404 || !strings.Contains(body, "not found") {
			t.Errorf("/mcp/sse with MCP off: %d %s, want 404", res.StatusCode, body)
		}
		res, body := get(t, base+"/book/some-link")
		if res.StatusCode != 200 || !strings.Contains(body, "compose-test") {
			t.Errorf("/book/some-link: %d %q, want the frontend's page", res.StatusCode, body)
		}
		if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors *") || res.Header.Get("X-Frame-Options") != "" {
			t.Errorf("/book/some-link is not framable: CSP %q, X-Frame-Options %q", csp, res.Header.Get("X-Frame-Options"))
		}
		res, _ = get(t, base+"/app")
		if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") || res.Header.Get("X-Frame-Options") != "DENY" {
			t.Errorf("/app is framable: CSP %q, X-Frame-Options %q", csp, res.Header.Get("X-Frame-Options"))
		}
	})

	// AC-C6: a request in flight when the backend is stopped completes. It
	// is held 15 s (a table lock), longer than Docker's default 10 s grace,
	// so only the 50 s stop_grace_period lets it finish.
	t.Run("stop during a request", func(t *testing.T) {
		code, body, took := requestDuringStop(t, p, base, "")
		t.Logf("request across `compose stop backend`: %d %q after %s", code, body, took.Round(100*time.Millisecond))
		if code != 404 || !strings.Contains(body, `"error":"not found"`) {
			t.Errorf("the in-flight request got %d %q, want the backend's own 404", code, body)
		}
		if took < 12*time.Second {
			t.Errorf("the request took %s; the lock should have held it ~15 s", took)
		}
		p.mustCompose(t, 4*time.Minute, "up", "-d", "--wait", "--wait-timeout", "200", "backend")

		// Control: with a 3 s grace the same request is cut, so the check
		// above depends on the grace period.
		code, body, took = requestDuringStop(t, p, base, "3")
		t.Logf("control with `stop -t 3`: %d %q after %s", code, body, took.Round(100*time.Millisecond))
		if code == 404 {
			t.Errorf("control: the request completed even with a 3 s grace; the test does not discriminate")
		}
		p.mustCompose(t, 4*time.Minute, "up", "-d", "--wait", "--wait-timeout", "200", "backend")
	})

	// AC-C3: the backend's readiness check turns unhealthy within three
	// intervals when it cannot serve (its database is gone), and recovers.
	t.Run("unready backend turns unhealthy", func(t *testing.T) {
		backend := p.containers(t)["backend"]
		waitHealth(t, backend, "healthy", 2*time.Minute)
		p.mustCompose(t, 2*time.Minute, "stop", "postgres")
		took := waitHealth(t, backend, "unhealthy", 2*time.Minute)
		t.Logf("backend unhealthy %s after its database stopped", took.Round(time.Second))
		if limit := 3*10*time.Second + 10*time.Second; took > limit {
			t.Errorf("unhealthy after %s; want within 3 intervals of 10 s (allowing %s)", took, limit)
		}
		p.mustCompose(t, 2*time.Minute, "start", "postgres")
		waitHealth(t, backend, "healthy", 2*time.Minute)
	})
}

// requestDuringStop holds GET /api/book/<unknown> ~15 s behind a table lock,
// stops the backend 3 s into it (with stop -t grace when grace is set), and
// returns what the client got.
func requestDuringStop(t *testing.T, p *project, base, grace string) (int, string, time.Duration) {
	t.Helper()
	lockDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, errOut, err := p.compose(ctx, "exec", "-T", "postgres", "psql", "-U", "clockwise", "-d", "clockwise", "-v", "ON_ERROR_STOP=1", "-c",
			"BEGIN; LOCK TABLE scheduling_links IN ACCESS EXCLUSIVE MODE; SELECT pg_sleep(15); COMMIT;")
		if err != nil {
			err = fmt.Errorf("%v %s", err, errOut)
		}
		lockDone <- err
	}()
	// Wait until the lock is held.
	deadline := time.Now().Add(30 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		out, _, _ := p.compose(ctx, "exec", "-T", "postgres", "psql", "-U", "clockwise", "-d", "clockwise", "-tAc",
			"SELECT count(*) FROM pg_locks l JOIN pg_class c ON c.oid = l.relation WHERE c.relname = 'scheduling_links' AND l.mode = 'AccessExclusiveLock' AND l.granted")
		cancel()
		if strings.TrimSpace(out) == "1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the table lock was never taken")
		}
		time.Sleep(200 * time.Millisecond)
	}
	type result struct {
		code int
		body string
		err  error
		took time.Duration
	}
	done := make(chan result, 1)
	go func() {
		start := time.Now()
		c := &http.Client{Timeout: 90 * time.Second}
		res, err := c.Get(base + "/api/book/compose-test-no-such-link")
		if err != nil {
			done <- result{err: err, took: time.Since(start)}
			return
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		done <- result{code: res.StatusCode, body: string(b), took: time.Since(start)}
	}()
	time.Sleep(3 * time.Second)
	args := []string{"stop"}
	if grace != "" {
		args = append(args, "-t", grace)
	}
	p.mustCompose(t, 2*time.Minute, append(args, "backend")...)
	r := <-done
	if err := <-lockDone; err != nil {
		t.Logf("lock session: %v", err)
	}
	if r.err != nil {
		return 0, r.err.Error(), r.took
	}
	return r.code, r.body, r.took
}

// AC-C3: a backend that takes 90 s to start listening (a long migration)
// still comes up, and nginx is not started before it is healthy.
func TestSlowStartStillComesUp(t *testing.T) {
	images(t)
	port := freePort(t)
	override := filepath.Join(tmpDir, fmt.Sprintf("slow-%d.yml", port))
	if err := os.WriteFile(override, []byte("services:\n  backend:\n    image: enach/paceday-backend:"+version+"-slow\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := newProject(t, baseSettings(port), override)
	upDone := make(chan error, 1)
	start := time.Now()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
		defer cancel()
		_, errOut, err := p.compose(ctx, "up", "-d", "--wait", "--wait-timeout", "300")
		if err != nil {
			err = fmt.Errorf("%v %s", err, errOut)
		}
		upDone <- err
	}()
	sawStarting := false
	for {
		select {
		case err := <-upDone:
			took := time.Since(start)
			if err != nil {
				logs, _, _ := p.compose(context.Background(), "logs", "--no-color", "--tail", "40")
				t.Fatalf("up with a 90 s startup: %v\n%s", err, logs)
			}
			t.Logf("stack healthy %s after up, with a backend listening 90 s late", took.Round(time.Second))
			if took < 90*time.Second {
				t.Errorf("came up after %s; the backend should have needed at least 90 s", took)
			}
			if !sawStarting {
				t.Error("never observed the backend starting while nginx waited")
			}
			for svc, id := range p.containers(t) {
				if h := health(t, id); h != "healthy" {
					t.Errorf("%s: %s", svc, h)
				}
			}
			return
		case <-time.After(2 * time.Second):
		}
		c := p.containers(t)
		b, ok := c["backend"]
		if !ok {
			continue
		}
		if health(t, b) == "healthy" {
			continue
		}
		if n, ok := c["nginx"]; ok {
			if st := inspect(t, n).State.Status; st == "running" {
				t.Fatalf("nginx is %s while the backend is %s", st, health(t, b))
			}
		}
		if health(t, b) == "starting" {
			sawStarting = true
		}
	}
}

// AC-C4/AC-C5 with the token set, owner decision Q-O1: MCP starts, stays up,
// publishes no port, and answers through nginx on the public origin.
func TestMCPThroughNginx(t *testing.T) {
	images(t)
	port := freePort(t)
	s := baseSettings(port)
	s["MCP_AUTH_TOKEN"] = str(mcpToken)
	p := newProject(t, s, "docker-compose.mcp.yml")
	p.mustCompose(t, 6*time.Minute, "up", "-d", "--wait", "--wait-timeout", "300")
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	c := p.containers(t)
	id, ok := c["mcp-server"]
	if !ok {
		t.Fatalf("no MCP container: %v", c)
	}
	info := inspect(t, id)
	if len(info.HostConfig.PortBindings) != 0 {
		t.Errorf("MCP publishes ports: %+v", info.HostConfig.PortBindings)
	}
	for k, v := range info.NetworkSettings.Ports {
		if len(v) != 0 {
			t.Errorf("MCP port %s bound on the host: %+v", k, v)
		}
	}

	if res, _ := get(t, base+"/mcp/sse"); res.StatusCode != 401 {
		t.Errorf("/mcp/sse without the token: %d, want 401", res.StatusCode)
	}
	if res, _ := get(t, base+"/mcp/sse", "Authorization", "Bearer wrong"); res.StatusCode != 401 {
		t.Errorf("/mcp/sse with a wrong token: %d, want 401", res.StatusCode)
	}

	// An MCP session through nginx: the SSE stream announces a relative
	// message endpoint on the same origin; initialize is posted there and its
	// answer arrives on the stream.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/mcp/sse", nil)
	req.Header.Set("Authorization", "Bearer "+mcpToken)
	req.Header.Set("Accept", "text/event-stream")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("/mcp/sse: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	stream := bufio.NewReader(res.Body)
	endpoint := ""
	for endpoint == "" {
		line, err := stream.ReadString('\n')
		if err != nil {
			t.Fatalf("no endpoint event through nginx: %v", err)
		}
		if strings.HasPrefix(line, "data: ") {
			endpoint = strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		}
	}
	t.Logf("endpoint event: %s", endpoint)
	if !strings.HasPrefix(endpoint, "/mcp/message?sessionId=") {
		t.Fatalf("endpoint %q, want /mcp/message?sessionId=… on the public origin", endpoint)
	}
	init := `{"jsonrpc":"2.0","id":42,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"compose-test","version":"1"}}}`
	post, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+endpoint, strings.NewReader(init))
	post.Header.Set("Authorization", "Bearer "+mcpToken)
	post.Header.Set("Content-Type", "application/json")
	pres, err := http.DefaultClient.Do(post)
	if err != nil {
		t.Fatal(err)
	}
	pres.Body.Close()
	if pres.StatusCode != http.StatusAccepted && pres.StatusCode != http.StatusOK {
		t.Fatalf("POST %s: %d", endpoint, pres.StatusCode)
	}
	for {
		line, err := stream.ReadString('\n')
		if err != nil {
			t.Fatalf("no initialize answer on the stream: %v", err)
		}
		if strings.Contains(line, `"id":42`) {
			if !strings.Contains(line, "serverInfo") {
				t.Errorf("initialize answer: %s", line)
			}
			break
		}
	}

	// It stays up: 60 s after it started, no restart and still healthy.
	started, err := time.Parse(time.RFC3339Nano, info.State.StartedAt)
	if err != nil {
		t.Fatal(err)
	}
	if wait := time.Until(started.Add(60 * time.Second)); wait > 0 {
		time.Sleep(wait)
	}
	after := inspect(t, id)
	if after.RestartCount != 0 || after.State.Health == nil || after.State.Health.Status != "healthy" {
		t.Errorf("60 s after start: restarts %d, health %+v", after.RestartCount, after.State.Health)
	}
}
