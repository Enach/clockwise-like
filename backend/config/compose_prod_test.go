package config

import (
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// PAC-81 Part C (spec NEW-deploy-hardening AC-C1, C2, C4, C6..C9, revision
// 3.3): static checks of the production compose files. What only a running
// Docker can show (rendering, health, start order, MCP opt-in, stop during a
// request) is in compose-test/. The files are read from the repository root,
// as in deployment_files_test.go.

// composeServices splits a compose file into its services' text blocks, keyed
// by name (two-space indented keys under the top-level `services:`).
func composeServices(t *testing.T, text string) map[string]string {
	t.Helper()
	text = strings.ReplaceAll(text, "\r\n", "\n")
	out := map[string]string{}
	in, cur := false, ""
	var b strings.Builder
	flush := func() {
		if cur != "" {
			out[cur] = b.String()
		}
		b.Reset()
	}
	svc := regexp.MustCompile(`^  ([a-z][a-z0-9_-]*):\s*$`)
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, " ") && strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "#") {
			flush()
			cur = ""
			in = strings.HasPrefix(line, "services:")
			continue
		}
		if !in {
			continue
		}
		if m := svc.FindStringSubmatch(line); m != nil {
			flush()
			cur = m[1]
			continue
		}
		if cur != "" {
			b.WriteString(line + "\n")
		}
	}
	flush()
	if len(out) == 0 {
		t.Fatal("no services found")
	}
	return out
}

// uncommented drops YAML comments (outside quotes is all these files need).
func uncommented(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if i := strings.Index(line, " #"); i >= 0 {
			line = line[:i]
		}
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func prodCompose(t *testing.T) (string, map[string]string) {
	t.Helper()
	text := uncommented(readRepoFile(t, "docker-compose.prod.yml"))
	return text, composeServices(t, text)
}

// AC-C1 (F-B1): no 443, no certificate mount, nothing about them in .env.example.
func TestProdComposeNoTLS(t *testing.T) {
	text, _ := prodCompose(t)
	for _, bad := range []string{"443", "letsencrypt", "TLS_CERT_DIR", "/etc/ssl", "certs"} {
		if strings.Contains(text, bad) {
			t.Errorf("docker-compose.prod.yml mentions %q (TLS is the proxy's job)", bad)
		}
	}
	env := readRepoFile(t, ".env.example")
	if regexp.MustCompile(`(?m)^(#: )?TLS_CERT_DIR\b`).MatchString(env) {
		t.Error(".env.example still documents TLS_CERT_DIR")
	}
}

// AC-C2 (F-C1, D-11): every image is an exact version; the Paceday images'
// version is required with no default.
func TestProdComposeImagesPinned(t *testing.T) {
	files := map[string]string{
		"docker-compose.prod.yml": uncommented(readRepoFile(t, "docker-compose.prod.yml")),
		"docker-compose.mcp.yml":  uncommented(readRepoFile(t, "docker-compose.mcp.yml")),
	}
	required := regexp.MustCompile(`^enach/paceday-(backend|frontend|mcp):\$\{PACEDAY_VERSION:\?[^}]*PACEDAY_VERSION is required[^}]*\}$`)
	exact := regexp.MustCompile(`^(nginx|postgres):\d+\.\d+(\.\d+)?-alpine$`)
	imageLine := regexp.MustCompile(`(?m)^\s+image:\s*(\S.*?)\s*$`)
	seen := map[string]bool{}
	for name, text := range files {
		for _, m := range imageLine.FindAllStringSubmatch(text, -1) {
			ref := m[1]
			switch {
			case strings.HasPrefix(ref, "enach/paceday-"):
				if !required.MatchString(ref) {
					t.Errorf("%s: %s is not tagged ${PACEDAY_VERSION:?…} (required, no default)", name, ref)
				}
				seen[strings.SplitN(strings.TrimPrefix(ref, "enach/paceday-"), ":", 2)[0]] = true
			case exact.MatchString(ref):
				seen[strings.SplitN(ref, ":", 2)[0]] = true
			default:
				t.Errorf("%s: image %s is not an exact version", name, ref)
			}
			if strings.HasSuffix(ref, ":latest") || regexp.MustCompile(`:\d+(-alpine)?$`).MatchString(ref) || !strings.Contains(ref, ":") {
				t.Errorf("%s: image %s floats", name, ref)
			}
		}
	}
	for _, want := range []string{"backend", "frontend", "mcp", "nginx", "postgres"} {
		if !seen[want] {
			t.Errorf("no pinned %s image found", want)
		}
	}
	if !strings.Contains(readRepoFile(t, ".env.example"), "#: PACEDAY_VERSION | required |") {
		t.Error(".env.example does not document PACEDAY_VERSION as required")
	}
}

// AC-C3 (static half): health checks call the binaries, start order waits
// for health, the backend's start period covers migrations.
func TestProdComposeHealthChecks(t *testing.T) {
	_, svcs := prodCompose(t)
	mcp := composeServices(t, uncommented(readRepoFile(t, "docker-compose.mcp.yml")))
	for name, want := range map[string]string{
		"backend":  `test: ["CMD", "/server", "health"]`,
		"frontend": `test: ["CMD", "/fileserver", "health"]`,
		"nginx":    `http://127.0.0.1/nginx-health"]`,
		"postgres": `pg_isready`,
	} {
		if !strings.Contains(svcs[name], want) {
			t.Errorf("%s: health check %q missing", name, want)
		}
	}
	if !strings.Contains(mcp["mcp-server"], `test: ["CMD", "/mcp-server", "health"]`) {
		t.Error("mcp-server: health check missing")
	}
	if !regexp.MustCompile(`start_period:\s*120s`).MatchString(svcs["backend"]) {
		t.Error("backend: start_period is not 120s")
	}
	for _, dep := range []string{"backend", "frontend"} {
		if !regexp.MustCompile(`(?s)depends_on:.*\n      ` + dep + `:\n        condition: service_healthy`).MatchString(svcs["nginx"]) {
			t.Errorf("nginx does not wait for %s to be healthy", dep)
		}
	}
	if !strings.Contains(mcp["nginx"], "mcp-server:\n        condition: service_healthy") {
		t.Error("with MCP, nginx does not wait for mcp-server to be healthy")
	}
}

// AC-C4 (static half, Q-O1): MCP is not in the production file; when enabled
// it publishes no port, is routed by nginx, and its token is required.
func TestMCPOptInThroughNginx(t *testing.T) {
	_, svcs := prodCompose(t)
	if _, ok := svcs["mcp-server"]; ok {
		t.Error("docker-compose.prod.yml runs an MCP server; MCP is opt-in through docker-compose.mcp.yml")
	}
	if strings.Contains(svcs["nginx"]+svcs["backend"], "MCP_AUTH_TOKEN") {
		t.Error("docker-compose.prod.yml reads MCP_AUTH_TOKEN; it must render without it")
	}
	mcp := composeServices(t, uncommented(readRepoFile(t, "docker-compose.mcp.yml")))
	m := mcp["mcp-server"]
	if strings.Contains(m, "ports:") || strings.Contains(m, "3001:") {
		t.Error("mcp-server publishes a port; it is reachable only through nginx (Q-O1)")
	}
	if !regexp.MustCompile(`MCP_AUTH_TOKEN: \$\{MCP_AUTH_TOKEN:\?[^}]*MCP_AUTH_TOKEN is required`).MatchString(m) {
		t.Error("MCP_AUTH_TOKEN is not required (${MCP_AUTH_TOKEN:?…}) in docker-compose.mcp.yml")
	}
	for _, want := range []string{"MCP_TRANSPORT:  sse", "MCP_BASE_PATH:  /mcp"} {
		if !strings.Contains(m, want) {
			t.Errorf("mcp-server lacks %q", want)
		}
	}
	if !strings.Contains(mcp["nginx"], "NGINX_MCP_UPSTREAM: mcp-server:3001") {
		t.Error("docker-compose.mcp.yml does not point nginx's /mcp/ at the MCP server")
	}
	readme := readRepoFile(t, "README.md")
	for _, want := range []string{"docker-compose.mcp.yml", "/mcp/sse", "Authorization", "Bearer"} {
		if !strings.Contains(readme, want) {
			t.Errorf("README's MCP section does not mention %q", want)
		}
	}
	if strings.Contains(readme, `"url": "http://localhost:3001/sse"`+"\n    }") {
		t.Error("README shows an MCP client configuration without the bearer header")
	}
	if !strings.Contains(readRepoFile(t, ".env.example"), "required when MCP is enabled") {
		t.Error(".env.example does not say MCP_AUTH_TOKEN is required when MCP is enabled")
	}
}

// AC-C6 (F-C5): grace period >= 5+30+5+10 s on every Paceday service; every
// service's logs size-capped and rotated.
func TestProdComposeGraceAndLogs(t *testing.T) {
	text, svcs := prodCompose(t)
	mcpText := uncommented(readRepoFile(t, "docker-compose.mcp.yml"))
	mcp := composeServices(t, mcpText)
	all := map[string]string{}
	for k, v := range svcs {
		all[k] = v
	}
	all["mcp-server"] = mcp["mcp-server"]
	min := (5 + 30 + 5 + 10) * time.Second
	grace := regexp.MustCompile(`stop_grace_period:\s*(\S+)`)
	for name, block := range all {
		m := grace.FindStringSubmatch(block)
		if m == nil {
			t.Errorf("%s: no stop_grace_period", name)
			continue
		}
		d, err := time.ParseDuration(m[1])
		if err != nil || d < min {
			t.Errorf("%s: stop_grace_period %s, want >= %s", name, m[1], min)
		}
		if !strings.Contains(block, "logging: *logging") {
			t.Errorf("%s: logs not rotated (logging: *logging)", name)
		}
	}
	for file, txt := range map[string]string{"docker-compose.prod.yml": text, "docker-compose.mcp.yml": mcpText} {
		if !regexp.MustCompile(`x-logging: &logging\n  driver: json-file\n  options:\n    max-size: "\d+m"\n    max-file: "\d+"`).MatchString(txt) {
			t.Errorf("%s: the logging anchor is not a size-capped, rotated json-file", file)
		}
	}
}

// AC-C7 (F-A10, F-C6): the backend gets exactly the settings it reads in
// production, and no SESSION_SECRET.
func TestProdComposeBackendEnv(t *testing.T) {
	_, svcs := prodCompose(t)
	got := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^      ([A-Z][A-Z0-9_]*):\s*(.*?)\s*$`).FindAllStringSubmatch(svcs["backend"], -1) {
		got[m[1]] = m[2]
	}
	before := []string{
		"PORT", "DATABASE_URL", "JWT_SECRET", "TRUSTED_PROXIES", "ALLOWED_ORIGIN", "FRONTEND_URL",
		"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "GOOGLE_REDIRECT_URL",
		"MICROSOFT_CLIENT_ID", "MICROSOFT_CLIENT_SECRET", "MICROSOFT_REDIRECT_URL",
		"ZOOM_CLIENT_ID", "ZOOM_CLIENT_SECRET", "ZOOM_REDIRECT_URL",
		"SLACK_CLIENT_ID", "SLACK_CLIENT_SECRET", "SLACK_REDIRECT_URI",
		"NOTION_CLIENT_ID", "NOTION_CLIENT_SECRET", "NOTION_REDIRECT_URI",
		"PACEDAY_LLM_APPROVED_HOSTS_AZURE_OPENAI", "PACEDAY_LLM_APPROVED_HOSTS_VERTEX",
		"PACEDAY_LLM_APPROVED_HOSTS_BEDROCK", "PACEDAY_LLM_APPROVED_HOSTS_OPENAI",
		"PACEDAY_LLM_APPROVED_HOSTS_ANTHROPIC",
	}
	added := []string{
		"APP_URL", "BASE_URL", "PACEDAY_ADMIN_EMAILS",
		"SMTP_HOST", "SMTP_PORT", "SMTP_USER", "SMTP_PASS", "SMTP_FROM",
		"SENTRY_DSN", "SENTRY_ENVIRONMENT", "SENTRY_RELEASE",
		EnvDBMaxOpenConns, EnvDBMaxIdleConns, EnvDBConnMaxLifetime, EnvDBConnMaxIdleTime,
	}
	want := append(append([]string{}, before...), added...)
	sort.Strings(want)
	have := keys(got)
	if strings.Join(have, ",") != strings.Join(want, ",") {
		t.Errorf("backend environment\n got %v\nwant %v", have, want)
	}
	if _, ok := got["SESSION_SECRET"]; ok {
		t.Error("SESSION_SECRET is still passed; nothing reads it")
	}
	if v := got["SENTRY_ENVIRONMENT"]; v != "${SENTRY_ENVIRONMENT:-production}" {
		t.Errorf("SENTRY_ENVIRONMENT = %s, want default production", v)
	}
	if v := got["SENTRY_RELEASE"]; v != "${SENTRY_RELEASE:-${PACEDAY_VERSION}}" {
		t.Errorf("SENTRY_RELEASE = %s, want the image version by default", v)
	}
	for _, name := range added {
		if !strings.HasPrefix(got[name], "${"+name) {
			t.Errorf("%s = %s, want a pass-through of the operator's %s", name, got[name], name)
		}
	}
}

// AC-C8 (F-B6, D-16): no docs service.
func TestProdComposeNoDocs(t *testing.T) {
	text, svcs := prodCompose(t)
	if _, ok := svcs["docs"]; ok {
		t.Error("docker-compose.prod.yml has a docs service")
	}
	if strings.Contains(text, "paceday-docs") {
		t.Error("docker-compose.prod.yml references the docs image")
	}
}

// AC-C9 (F-C8): nginx's host bind address is an operator setting, default all
// interfaces; nothing else publishes a port.
func TestProdComposeNginxBind(t *testing.T) {
	_, svcs := prodCompose(t)
	want := `- "${NGINX_BIND_ADDRESS:+${NGINX_BIND_ADDRESS}:}${NGINX_HTTP_PORT:-80}:80"`
	if !strings.Contains(svcs["nginx"], "ports:\n      "+want) {
		t.Errorf("nginx ports are not %s:\n%s", want, svcs["nginx"])
	}
	for name, block := range svcs {
		if name != "nginx" && strings.Contains(block, "ports:") {
			t.Errorf("%s publishes a port", name)
		}
	}
	env := readRepoFile(t, ".env.example")
	for _, v := range []string{"NGINX_BIND_ADDRESS", "NGINX_HTTP_PORT"} {
		if !strings.Contains(env, "#: "+v+" | optional |") {
			t.Errorf(".env.example does not document %s", v)
		}
	}
}
