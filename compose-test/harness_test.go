package composetest

// Harness for PAC-81 Part C's compose integration tests (spec
// NEW-deploy-hardening §4, level [integration]; docs/plans/PAC-81-part-c.md §3).
//
// The tests run the SHIPPED docker-compose.prod.yml (and docker-compose.mcp.yml)
// with Docker Compose v2. Images: the backend and the MCP server are built
// from this checkout; the frontend is a test image of the shipped file server
// (docker/fileserver) with a placeholder page, because the real SPA build
// needs the web repository. They are tagged enach/paceday-*:<throwaway
// version>, which is exactly what PACEDAY_VERSION selects. Each scenario is
// its own compose project; nginx is published on 127.0.0.1 at a port in
// 18500-18599 through the shipped NGINX_BIND_ADDRESS / NGINX_HTTP_PORT
// settings. Projects, volumes and images are removed at the end of the run.
//
// Docker is required; its absence is a failure, not a skip (factory README §3).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	repoRoot string
	version  string // the throwaway PACEDAY_VERSION
	runID    string
	tmpDir   string

	buildOnce sync.Once
	buildErr  error

	projectsMu sync.Mutex
	projects   []*project

	portMu   sync.Mutex
	nextPort = 18500 + rand.Intn(40)
)

func TestMain(m *testing.M) {
	_, file, _, _ := runtime.Caller(0)
	repoRoot = filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
	runID = fmt.Sprintf("%d%d", os.Getpid()%10000, time.Now().UnixNano()%100000)
	version = "0.0.0-composetest-" + runID
	var err error
	tmpDir, err = os.MkdirTemp("", "paceday-compose-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	teardown()
	os.Exit(code)
}

func teardown() {
	projectsMu.Lock()
	defer projectsMu.Unlock()
	for _, p := range projects {
		p.down()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, img := range []string{"backend", "backend-slow", "frontend", "mcp"} {
		tag := "enach/paceday-" + img + ":" + version
		if img == "backend-slow" {
			tag = "enach/paceday-backend:" + version + "-slow"
		}
		_, _, _ = run(ctx, "", nil, "docker", "image", "rm", "-f", tag)
	}
	_ = os.RemoveAll(tmpDir)
}

func run(ctx context.Context, dir string, env []string, name string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

// images builds the test images once.
func images(t *testing.T) {
	t.Helper()
	buildOnce.Do(func() { buildErr = buildImages() })
	if buildErr != nil {
		t.Fatalf("building test images: %v", buildErr)
	}
}

func buildImages() error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if _, errOut, err := run(ctx, "", nil, "docker", "version"); err != nil {
		return fmt.Errorf("docker is required for these tests and is not usable: %v %s", err, errOut)
	}
	build := func(tag, contextDir string, dockerfile string) error {
		args := []string{"build", "-q", "-t", tag}
		var stdin string
		if dockerfile != "" {
			args = append(args, "-f", "-")
			stdin = dockerfile
		}
		args = append(args, contextDir)
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("docker build %s: %v\n%s", tag, err, out)
		}
		return nil
	}
	if err := build("enach/paceday-backend:"+version, filepath.Join(repoRoot, "backend"), ""); err != nil {
		return err
	}
	if err := build("enach/paceday-mcp:"+version, filepath.Join(repoRoot, "mcp"), ""); err != nil {
		return err
	}
	// The shipped file server, built as docker/frontend.dockerfile builds it,
	// serving a placeholder page instead of the SPA bundle.
	frontend := `FROM golang:1.25-alpine AS server-builder
WORKDIR /build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o fileserver . && mkdir -p /srv && \
    printf '<!doctype html><html><head></head><body><div id="root">compose-test</div></body></html>' > /srv/index.html
FROM scratch
COPY --from=server-builder /build/fileserver /fileserver
COPY --from=server-builder /srv /srv
EXPOSE 80
CMD ["/fileserver"]
`
	if err := build("enach/paceday-frontend:"+version, filepath.Join(repoRoot, "docker", "fileserver"), frontend); err != nil {
		return err
	}
	// The backend, listening 90 s late (a long startup migration).
	slowDir := filepath.Join(tmpDir, "slow")
	if err := os.MkdirAll(slowDir, 0o755); err != nil {
		return err
	}
	arch, _, err := run(ctx, "", nil, "docker", "version", "--format", "{{.Server.Arch}}")
	if err != nil {
		return err
	}
	gb := exec.CommandContext(ctx, "go", "build", "-o", filepath.Join(slowDir, "delay"), "./delay")
	gb.Dir = filepath.Join(repoRoot, "compose-test")
	gb.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+strings.TrimSpace(arch), "CGO_ENABLED=0")
	if out, err := gb.CombinedOutput(); err != nil {
		return fmt.Errorf("building delay: %v\n%s", err, out)
	}
	slow := "FROM enach/paceday-backend:" + version + "\nCOPY delay /delay\nENTRYPOINT [\"/delay\", \"90s\"]\nCMD [\"/server\"]\n"
	return build("enach/paceday-backend:"+version+"-slow", slowDir, slow)
}

// freePort returns a free 127.0.0.1 port in 18500-18599.
func freePort(t *testing.T) int {
	t.Helper()
	portMu.Lock()
	defer portMu.Unlock()
	for i := 0; i < 100; i++ {
		p := nextPort
		nextPort++
		if nextPort > 18599 {
			nextPort = 18500
		}
		if ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p)); err == nil {
			_ = ln.Close()
			return p
		}
	}
	t.Fatal("no free port in 18500-18599")
	return 0
}

// project is one compose project of the shipped files.
type project struct {
	name    string
	files   []string // compose files, relative to the repo root or absolute
	envFile string
	port    int
}

// settings is the .env of a project; a nil value leaves the name out.
type settings map[string]*string

func str(s string) *string { return &s }

// baseSettings is a working production .env for a test.
func baseSettings(port int) settings {
	origin := fmt.Sprintf("http://127.0.0.1:%d", port)
	return settings{
		"PACEDAY_VERSION":    str(version),
		"POSTGRES_PASSWORD":  str("compose-test-db-password"),
		"JWT_SECRET":         str("compose-test-jwt-secret-0123456789abcdef0123456789abcdef"),
		"FRONTEND_URL":       str(origin),
		"APP_URL":            str(origin),
		"NGINX_BIND_ADDRESS": str("127.0.0.1"),
		"NGINX_HTTP_PORT":    str(fmt.Sprint(port)),
	}
}

// newProject writes the env file and registers the project for cleanup.
func newProject(t *testing.T, s settings, files ...string) *project {
	t.Helper()
	projectsMu.Lock()
	seq := len(projects)
	projectsMu.Unlock()
	p := &project{name: fmt.Sprintf("pac81c-%s-%d-%d", runID, seq, rand.Intn(1000)), files: append([]string{"docker-compose.prod.yml"}, files...)}
	if v := s["NGINX_HTTP_PORT"]; v != nil {
		_, _ = fmt.Sscan(*v, &p.port)
	}
	var b strings.Builder
	for k, v := range s {
		if v != nil {
			b.WriteString(k + "=" + *v + "\n")
		}
	}
	p.envFile = filepath.Join(tmpDir, p.name+".env")
	if err := os.WriteFile(p.envFile, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	projectsMu.Lock()
	projects = append(projects, p)
	projectsMu.Unlock()
	t.Cleanup(p.down)
	return p
}

// composeEnv is the process environment without anything the compose files
// read, so the test's own settings are the only ones.
func composeEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch {
		case strings.HasPrefix(k, "COMPOSE_"), strings.HasPrefix(k, "NGINX_"), strings.HasPrefix(k, "SENTRY_"),
			strings.HasPrefix(k, "PACEDAY_"), strings.HasPrefix(k, "MCP_"), k == "POSTGRES_PASSWORD", k == "JWT_SECRET":
			continue
		}
		out = append(out, kv)
	}
	return out
}

// compose runs `docker compose` for the project and returns stdout, stderr.
func (p *project) compose(ctx context.Context, args ...string) (string, string, error) {
	full := []string{"compose", "-p", p.name}
	for _, f := range p.files {
		full = append(full, "-f", f)
	}
	full = append(full, "--env-file", p.envFile)
	full = append(full, args...)
	return run(ctx, repoRoot, composeEnv(), "docker", full...)
}

func (p *project) mustCompose(t *testing.T, timeout time.Duration, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, errOut, err := p.compose(ctx, args...)
	if err != nil {
		logs, _, _ := p.compose(context.Background(), "logs", "--no-color", "--tail", "60")
		ps, _, _ := p.compose(context.Background(), "ps", "-a")
		t.Fatalf("docker compose %s: %v\n%s%s\n--- ps\n%s\n--- logs\n%s", strings.Join(args, " "), err, out, errOut, ps, logs)
	}
	return out
}

func (p *project) down() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	_, _, _ = p.compose(ctx, "down", "-v", "--remove-orphans", "-t", "5")
}

// containers lists the project's containers (all states) by service.
func (p *project) containers(t *testing.T) map[string]string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, errOut, err := run(ctx, "", nil, "docker", "ps", "-a", "--filter", "label=com.docker.compose.project="+p.name,
		"--format", `{{.ID}} {{.Label "com.docker.compose.service"}}`)
	if err != nil {
		t.Fatalf("docker ps: %v %s", err, errOut)
	}
	res := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if f := strings.Fields(line); len(f) == 2 {
			res[f[1]] = f[0]
		}
	}
	return res
}

// inspect returns docker inspect's JSON for one container.
type containerInfo struct {
	RestartCount int `json:"RestartCount"`
	State        struct {
		Status    string `json:"Status"`
		StartedAt string `json:"StartedAt"`
		Health    *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
	HostConfig struct {
		PortBindings map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"PortBindings"`
	} `json:"HostConfig"`
	NetworkSettings struct {
		Ports map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"Ports"`
	} `json:"NetworkSettings"`
}

func inspect(t *testing.T, id string) containerInfo {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, errOut, err := run(ctx, "", nil, "docker", "inspect", id)
	if err != nil {
		t.Fatalf("docker inspect %s: %v %s", id, err, errOut)
	}
	var infos []containerInfo
	if err := json.Unmarshal([]byte(out), &infos); err != nil || len(infos) != 1 {
		t.Fatalf("docker inspect %s: %v", id, err)
	}
	return infos[0]
}

func health(t *testing.T, id string) string {
	t.Helper()
	i := inspect(t, id)
	if i.State.Health == nil {
		return "none"
	}
	return i.State.Health.Status
}

// waitHealth polls until the container's health is want or the timeout ends,
// and returns how long it took.
func waitHealth(t *testing.T, id, want string, timeout time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	for {
		if h := health(t, id); h == want {
			return time.Since(start)
		}
		if time.Since(start) > timeout {
			t.Fatalf("container %s not %s after %s (is %s)", id, want, timeout, health(t, id))
		}
		time.Sleep(time.Second)
	}
}
