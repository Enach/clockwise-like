package nginxtest

// Harness for PAC-81 Part B's nginx integration tests (spec §4 test level
// [integration]: "nginx with the shipped config and a stub upstream").
//
// Every test gets its own stub upstream container and its own nginx container
// running the SHIPPED nginx/nginx.conf through the SHIPPED
// nginx/paceday-render.sh, on a throwaway Docker network. Requests are sent
// from client containers whose addresses the test chooses, so "a request from
// the trusted proxy P" and "from an untrusted peer Q" are real socket peers,
// not headers. Nothing is published on a host port. Every container and the
// network carry a run label and are removed at the end of the run.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const labelKey = "paceday-nginx-test"

var (
	setupOnce sync.Once
	setupErr  error

	runID      string
	netName    string
	netPrefix  string // first three octets of the test subnet
	stubDir    string // host directory holding the linux stub binary
	repoRoot   string
	nginxImage string
	ipCounter  atomic.Int32
	nameSeq    atomic.Int32
)

func TestMain(m *testing.M) {
	code := m.Run()
	teardown()
	os.Exit(code)
}

func docker(ctx context.Context, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

func mustDocker(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, errOut, err := docker(ctx, args...)
	if err != nil {
		t.Fatalf("docker %s: %v\n%s%s", strings.Join(args, " "), err, out, errOut)
	}
	return out
}

// harness prepares the shared pieces once: the stub binary and the network.
// Docker is required; its absence is a failure, not a skip (factory README
// §3: a check that did not run must not look like a pass).
func harness(t *testing.T) {
	t.Helper()
	setupOnce.Do(func() { setupErr = setup() })
	if setupErr != nil {
		t.Fatalf("nginx integration harness: %v", setupErr)
	}
}

func setup() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	arch, errOut, err := docker(ctx, "version", "--format", "{{.Server.Arch}}")
	if err != nil {
		return fmt.Errorf("docker is required for these tests and is not usable: %v %s", err, errOut)
	}
	arch = strings.TrimSpace(arch)

	_, file, _, _ := runtime.Caller(0)
	here := filepath.Dir(file)
	repoRoot = filepath.Clean(filepath.Join(here, "..", ".."))
	// A bind mount of a missing source makes Docker Desktop create a
	// directory in its place; refuse before that can happen.
	for _, f := range []string{"nginx.conf", "paceday-render.sh"} {
		if st, err := os.Stat(filepath.Join(repoRoot, "nginx", f)); err != nil || !st.Mode().IsRegular() {
			return fmt.Errorf("nginx/%s is missing or not a regular file", f)
		}
	}

	nginxImage = os.Getenv("PACEDAY_NGINX_IMAGE")
	if nginxImage == "" {
		nginxImage = "nginx:alpine"
	}

	stubDir, err = os.MkdirTemp("", "paceday-nginx-stub-")
	if err != nil {
		return err
	}
	build := exec.CommandContext(ctx, "go", "build", "-o", filepath.Join(stubDir, "stub"), "./stub")
	build.Dir = here
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("building the stub: %v\n%s", err, out)
	}

	runID = fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano()%1_000_000)
	netName = "paceday-nginx-test-" + runID
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for attempt := 0; attempt < 10; attempt++ {
		prefix := fmt.Sprintf("10.231.%d", 1+r.Intn(250))
		_, errOut, err := docker(ctx, "network", "create",
			"--label", labelKey+"="+runID,
			"--subnet", prefix+".0/24",
			"--ip-range", prefix+".0/25", // dynamic addresses stay below .128
			netName)
		if err == nil {
			netPrefix = prefix
			return nil
		}
		if !strings.Contains(errOut, "overlap") {
			return fmt.Errorf("creating the test network: %v %s", err, errOut)
		}
	}
	return fmt.Errorf("no free 10.231.x.0/24 subnet for the test network")
}

func teardown() {
	if runID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	ids, _, _ := docker(ctx, "ps", "-aq", "--filter", "label="+labelKey+"="+runID)
	if f := strings.Fields(ids); len(f) > 0 {
		_, _, _ = docker(ctx, append([]string{"rm", "-f"}, f...)...)
	}
	_, _, _ = docker(ctx, "network", "rm", netName)
	if stubDir != "" {
		_ = os.RemoveAll(stubDir)
	}
}

// fixedIP hands out a unique address above the dynamic range.
func fixedIP() string {
	return fmt.Sprintf("%s.%d", netPrefix, 128+ipCounter.Add(1))
}

func uniqueName(kind string) string {
	return fmt.Sprintf("pnt-%s-%s-%d", runID, kind, nameSeq.Add(1))
}

func stubMount() []string {
	return []string{"--mount", "type=bind,source=" + stubDir + ",target=/stub,readonly"}
}

func nginxMounts() []string {
	return []string{
		"--mount", "type=bind,source=" + filepath.Join(repoRoot, "nginx", "nginx.conf") + ",target=/etc/nginx/conf.d/default.conf,readonly",
		"--mount", "type=bind,source=" + filepath.Join(repoRoot, "nginx", "paceday-render.sh") + ",target=/paceday/render.sh,readonly",
	}
}

// startStub runs a recording upstream and returns its container name.
func startStub(t *testing.T) string {
	t.Helper()
	harness(t)
	name := uniqueName("stub")
	args := []string{"run", "-d", "--name", name, "--label", labelKey + "=" + runID, "--network", netName}
	args = append(args, stubMount()...)
	args = append(args, "--entrypoint", "/stub/stub", nginxImage, "upstream")
	mustDocker(t, args...)
	t.Cleanup(func() { _, _, _ = docker(context.Background(), "rm", "-f", name) })
	deadline := time.Now().Add(30 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, _, err := docker(ctx, clientArgs("", "http://"+name+":9000/conns")...)
		cancel()
		if err == nil {
			return name
		}
		if time.Now().After(deadline) {
			t.Fatalf("stub %s did not come up: %v", name, err)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// nginxArgs builds `docker run` arguments for the shipped config and render
// step, with the stub as both upstreams, plus the given environment.
func nginxArgs(name, stub string, env map[string]string, detach bool, cmd ...string) []string {
	args := []string{"run", "--name", name, "--label", labelKey + "=" + runID, "--network", netName}
	if detach {
		args = append(args, "-d")
	} else {
		args = append(args, "--rm")
	}
	args = append(args, nginxMounts()...)
	full := map[string]string{
		"NGINX_BACKEND_UPSTREAM":  stub + ":8080",
		"NGINX_FRONTEND_UPSTREAM": stub + ":8081",
	}
	for k, v := range env {
		full[k] = v
	}
	for k, v := range full {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, "--entrypoint", "/bin/sh", nginxImage, "/paceday/render.sh")
	return append(args, cmd...)
}

// startNginx runs the shipped nginx in front of stub and returns its name.
func startNginx(t *testing.T, stub string, env map[string]string) string {
	t.Helper()
	name := uniqueName("nginx")
	mustDocker(t, nginxArgs(name, stub, env, true, "nginx", "-g", "daemon off;")...)
	t.Cleanup(func() { _, _, _ = docker(context.Background(), "rm", "-f", name) })
	deadline := time.Now().Add(30 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, _, err := docker(ctx, "exec", name, "sh", "-c", "test -s /run/nginx.pid || test -s /var/run/nginx.pid")
		cancel()
		if err == nil {
			return name
		}
		if time.Now().After(deadline) {
			logs, errLogs, _ := docker(context.Background(), "logs", name)
			t.Fatalf("nginx %s did not start: %v\n%s%s", name, err, logs, errLogs)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// runNginxOnce runs the render step followed by cmd (for example `nginx -t`)
// in a throwaway container and returns its combined output and error.
func runNginxOnce(t *testing.T, stub string, env map[string]string, cmd ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, errOut, err := docker(ctx, nginxArgs(uniqueName("once"), stub, env, false, cmd...)...)
	return out + errOut, err
}

func clientArgs(ip, url string, flags ...string) []string {
	args := []string{"run", "--rm", "--label", labelKey + "=" + runID, "--network", netName}
	if ip != "" {
		args = append(args, "--ip", ip)
	}
	args = append(args, stubMount()...)
	args = append(args, "--entrypoint", "/stub/stub", nginxImage, "client")
	args = append(args, flags...)
	return append(args, url)
}

// Response mirrors stub.Response.
type Response struct {
	Status int                 `json:"status"`
	Header map[string][]string `json:"header"`
	Body   string              `json:"body"`
	Error  string              `json:"error,omitempty"`
}

// Recorded mirrors stub.Recorded.
type Recorded struct {
	Upstream string              `json:"upstream"`
	Method   string              `json:"method"`
	URI      string              `json:"uri"`
	Header   map[string][]string `json:"header"`
	BodyLen  int                 `json:"body_len"`
}

// send makes requests from a client container at ip ("" = any address) and
// returns the responses. flags are stub client flags (-H, -method, -n, ...).
func send(t *testing.T, ip, url string, flags ...string) []Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out, errOut, err := docker(ctx, clientArgs(ip, url, flags...)...)
	if err != nil {
		t.Fatalf("client %s: %v\n%s%s", url, err, out, errOut)
	}
	var res []Response
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("client output: %v\n%s", err, out)
	}
	for _, r := range res {
		if r.Error != "" {
			t.Fatalf("client %s: %s", url, r.Error)
		}
	}
	return res
}

func sendOne(t *testing.T, ip, url string, flags ...string) Response {
	t.Helper()
	return send(t, ip, url, flags...)[0]
}

func requestsSeen(t *testing.T, stub string) []Recorded {
	t.Helper()
	var recs []Recorded
	if err := json.Unmarshal([]byte(sendOne(t, "", "http://"+stub+":9000/requests").Body), &recs); err != nil {
		t.Fatal(err)
	}
	return recs
}

func connsSeen(t *testing.T, stub string) map[string]int {
	t.Helper()
	var c map[string]int
	if err := json.Unmarshal([]byte(sendOne(t, "", "http://"+stub+":9000/conns").Body), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func nginxLogs(t *testing.T, name string) string {
	t.Helper()
	out, errOut, err := docker(context.Background(), "logs", name)
	if err != nil {
		t.Fatalf("docker logs %s: %v", name, err)
	}
	return out + errOut
}
