package nginxtest

// PAC-81 Part B — nginx edge. Each test names the acceptance criterion of
// docs/specs/NEW-deploy-hardening.md (rev 2) it falsifies.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const (
	// The origin of the web's hard-coded Sentry DSN
	// (smart-calendar-flow:src/lib/sentry.ts:19-20), the AC-B4 default.
	defaultSentryOrigin = "https://o4512081160896512.ingest.de.sentry.io"

	forgedClient = "203.0.113.7"   // F: what the client claims
	realClient   = "198.51.100.23" // C: what the trusted proxy appended
)

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// stripComments drops nginx/sh comments so prose cannot satisfy or fail a check.
func stripComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// parseCSP splits a policy into directive -> sources.
func parseCSP(t *testing.T, policy string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, part := range strings.Split(policy, ";") {
		f := strings.Fields(part)
		if len(f) == 0 {
			continue
		}
		if _, dup := out[f[0]]; dup {
			t.Errorf("directive %s appears twice in %q", f[0], policy)
		}
		out[f[0]] = f[1:]
	}
	return out
}

func sameSet(a, b []string) bool {
	a, b = append([]string{}, a...), append([]string{}, b...)
	sort.Strings(a)
	sort.Strings(b)
	return strings.Join(a, " ") == strings.Join(b, " ")
}

func one(t *testing.T, what string, r Response, header string) string {
	t.Helper()
	v := r.Header[header]
	if len(v) != 1 {
		t.Errorf("%s: want exactly one %s, got %q", what, header, v)
		return ""
	}
	return v[0]
}

// ── AC-B1 ───────────────────────────────────────────────────────────────────

func TestConfigHasNoTLSOrHTTPSRedirect(t *testing.T) {
	conf := stripComments(readRepoFile(t, "nginx/nginx.conf"))
	render := stripComments(readRepoFile(t, "nginx/paceday-render.sh"))
	for name, text := range map[string]string{"nginx.conf": conf, "paceday-render.sh": render} {
		for _, re := range []*regexp.Regexp{
			regexp.MustCompile(`listen\s+[^;]*443`),
			regexp.MustCompile(`\bssl\b`),
			regexp.MustCompile(`ssl_certificate`),
			regexp.MustCompile(`return\s+30[1278][^;]*https`),
			regexp.MustCompile(`rewrite[^;]*https://`),
			regexp.MustCompile(`(?i)Strict-Transport-Security"?\s+["']`),
		} {
			if re.MatchString(text) {
				t.Errorf("%s matches %s: TLS and HSTS are the proxy's job (owner decision 2026-10-27)", name, re)
			}
		}
	}
	if !regexp.MustCompile(`listen\s+80;`).MatchString(conf) {
		t.Error("nginx.conf does not listen on plain HTTP port 80")
	}
}

func TestNoRedirectToHTTPS(t *testing.T) {
	t.Parallel()
	stub := startStub(t)
	proxy := fixedIP()
	n := startNginx(t, stub, map[string]string{"NGINX_TRUSTED_PROXIES": proxy})
	for _, path := range []string{"/", "/api/health", "/app/links/some/deep/link", "/api/redirect"} {
		for _, flags := range [][]string{
			nil,
			{"-H", "X-Forwarded-Proto: http", "-H", "X-Forwarded-For: " + realClient},
			{"-H", "X-Forwarded-Proto: https", "-H", "X-Forwarded-For: " + realClient},
		} {
			r := sendOne(t, proxy, "http://"+n+path, flags...)
			if loc := strings.Join(r.Header["Location"], ","); strings.HasPrefix(strings.ToLower(loc), "https://") {
				t.Errorf("%s %v: redirected to %q", path, flags, loc)
			}
			if path != "/api/redirect" && r.Status >= 300 && r.Status < 400 {
				t.Errorf("%s %v: status %d, want no redirect", path, flags, r.Status)
			}
		}
	}
}

// ── AC-B2 / AC-B3 (header half) ─────────────────────────────────────────────

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	t.Parallel()
	stub := startStub(t)
	n := startNginx(t, stub, nil)
	base := "http://" + n
	cases := []struct {
		what  string
		path  string
		flags []string
		code  int
	}{
		{"SPA page", "/", nil, 200},
		{"static asset", "/assets/index-abc.js", nil, 200},
		{"API JSON", "/api/health", nil, 200},
		{"API error 500", "/api/status/500", nil, 500},
		{"API error 404", "/api/status/404", nil, 404},
		{"SPA fallback", "/app/settings/deep", nil, 200},
		{"upstream with its own copies", "/api/leaky", nil, 200},
		{"nginx's own 413", "/api/bookings", []string{"-method", "POST", "-body-size", "1048577"}, 413},
		{"nginx health", "/nginx-health", nil, 200},
	}
	for _, c := range cases {
		r := sendOne(t, "", base+c.path, c.flags...)
		if r.Status != c.code {
			t.Errorf("%s: status %d, want %d", c.what, r.Status, c.code)
		}
		if v := one(t, c.what, r, "X-Content-Type-Options"); v != "" && v != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options %q", c.what, v)
		}
		if v := one(t, c.what, r, "X-Frame-Options"); v != "" && v != "DENY" {
			t.Errorf("%s: X-Frame-Options %q", c.what, v)
		}
		if v := one(t, c.what, r, "Referrer-Policy"); v != "" && v != "strict-origin-when-cross-origin" {
			t.Errorf("%s: Referrer-Policy %q", c.what, v)
		}
		if v := one(t, c.what, r, "Content-Security-Policy"); v != "" && !strings.Contains(v, "frame-ancestors 'none'") {
			t.Errorf("%s: not the app's CSP: %q", c.what, v)
		}
		if v, ok := r.Header["Strict-Transport-Security"]; ok {
			t.Errorf("%s: Strict-Transport-Security present (%q); HSTS belongs on the proxy", c.what, v)
		}
	}
}

func TestCSPDirectives(t *testing.T) {
	t.Parallel()
	stub := startStub(t)
	n := startNginx(t, stub, nil)
	r := sendOne(t, "", "http://"+n+"/")
	csp := parseCSP(t, one(t, "SPA page", r, "Content-Security-Policy"))
	want := map[string][]string{
		"default-src":     {"'self'"},
		"script-src":      {"'self'"}, // no 'unsafe-inline', no 'unsafe-eval'
		"style-src":       {"'self'", "'unsafe-inline'", "https://fonts.googleapis.com"},
		"font-src":        {"'self'", "data:", "https://fonts.gstatic.com"}, // FullCalendar embeds its icon font (fcicons) as data:
		"img-src":         {"'self'", "data:", "https://*.googleusercontent.com"},
		"connect-src":     {"'self'", defaultSentryOrigin},
		"object-src":      {"'none'"},
		"base-uri":        {"'self'"},
		"form-action":     {"'self'"},
		"frame-ancestors": {"'none'"},
	}
	for d, sources := range want {
		if !sameSet(csp[d], sources) {
			t.Errorf("%s = %q, want %q", d, csp[d], sources)
		}
	}
	for d := range csp {
		if _, ok := want[d]; !ok {
			t.Errorf("unexpected directive %s %q", d, csp[d])
		}
	}
}

// ── AC-B4 ───────────────────────────────────────────────────────────────────

func TestCSPConnectSrc(t *testing.T) {
	t.Parallel()
	stub := startStub(t)
	cases := []struct {
		what string
		env  map[string]string
		want []string
	}{
		{"default", nil, []string{"'self'", defaultSentryOrigin}},
		{"sentry origin changed", map[string]string{"NGINX_CSP_SENTRY_ORIGIN": "https://o1.ingest.sentry.example"},
			[]string{"'self'", "https://o1.ingest.sentry.example"}},
		{"sentry origin set empty", map[string]string{"NGINX_CSP_SENTRY_ORIGIN": ""},
			[]string{"'self'", defaultSentryOrigin}},
		{"cross-origin API and extras", map[string]string{
			"API_BASE_URL":              "https://api.example.test/api",
			"NGINX_CSP_CONNECT_ORIGINS": "https://a.example.test, https://b.example.test:8443",
		}, []string{"'self'", defaultSentryOrigin, "https://api.example.test", "https://a.example.test", "https://b.example.test:8443"}},
		{"relative API base adds nothing", map[string]string{"API_BASE_URL": "/api"},
			[]string{"'self'", defaultSentryOrigin}},
	}
	for _, c := range cases {
		n := startNginx(t, stub, c.env)
		r := sendOne(t, "", "http://"+n+"/")
		got := parseCSP(t, one(t, c.what, r, "Content-Security-Policy"))["connect-src"]
		if !sameSet(got, c.want) {
			t.Errorf("%s: connect-src %q, want %q", c.what, got, c.want)
		}
	}
}

func TestInvalidSettingsRefuseToStart(t *testing.T) {
	t.Parallel()
	stub := startStub(t)
	// "STUB" is replaced by the stub's name, so an injected value that the
	// render step wrongly accepts still yields a config nginx -t would load:
	// the refusal must come from the validation, not from a broken config.
	nl, cr, tab := string(rune(10)), string(rune(13)), string(rune(9))
	cases := map[string]map[string]string{
		"directive injection in extras": {"NGINX_CSP_CONNECT_ORIGINS": "https://a.test; script-src *"},
		"wildcard origin":               {"NGINX_CSP_CONNECT_ORIGINS": "https://*.example.test"},
		"origin with a path":            {"NGINX_CSP_SENTRY_ORIGIN": "https://o1.ingest.sentry.io/api"},
		"quote in sentry origin":        {"NGINX_CSP_SENTRY_ORIGIN": "https://x.test'"},
		"proxy that is not an address":  {"NGINX_TRUSTED_PROXIES": "proxy.example.test"},
		"upstream without a port":       {"NGINX_BACKEND_UPSTREAM": "backend"},
		// Review of #245: grep matches line by line, so a valid first line
		// used to carry anything after a newline into the generated config.
		"newline in sentry origin":       {"NGINX_CSP_SENTRY_ORIGIN": "https://ok.test" + nl + "script-src *"},
		"newline in API_BASE_URL":        {"API_BASE_URL": "https://api.test/api" + nl + "; script-src *"},
		"newline in backend upstream":    {"NGINX_BACKEND_UPSTREAM": "STUB:8080" + nl},
		"newline in frontend upstream":   {"NGINX_FRONTEND_UPSTREAM": "STUB:8081" + nl},
		"newline in extras":              {"NGINX_CSP_CONNECT_ORIGINS": "https://a.test" + nl + "https://b.test"},
		"newline in trusted proxies":     {"NGINX_TRUSTED_PROXIES": "10.0.0.1" + nl + "10.0.0.2"},
		"CR then newline, sentry origin": {"NGINX_CSP_SENTRY_ORIGIN": "https://ok.test" + nl + cr},
		"CR then newline, upstream":      {"NGINX_BACKEND_UPSTREAM": "STUB:8080" + nl + cr},
		"CR in sentry origin":            {"NGINX_CSP_SENTRY_ORIGIN": "https://ok.test" + cr},
		"CR in API_BASE_URL":             {"API_BASE_URL": "https://api.test/api" + cr},
		"CR in backend upstream":         {"NGINX_BACKEND_UPSTREAM": "STUB:8080" + cr},
		"CR in frontend upstream":        {"NGINX_FRONTEND_UPSTREAM": "STUB:8081" + cr},
		"CR in extras":                   {"NGINX_CSP_CONNECT_ORIGINS": "https://a.test" + cr},
		"CR in trusted proxies":          {"NGINX_TRUSTED_PROXIES": "10.0.0.1" + cr},
		"tab in sentry origin":           {"NGINX_CSP_SENTRY_ORIGIN": "https://ok.test" + tab},
		"IPv4 /0 trusts everyone":        {"NGINX_TRUSTED_PROXIES": "0.0.0.0/0"},
		"IPv6 /0 trusts everyone":        {"NGINX_TRUSTED_PROXIES": "::/0"},
		"/0 among others":                {"NGINX_TRUSTED_PROXIES": "10.0.0.1, 10.0.0.0/00"},
	}
	for what, env := range cases {
		for k, v := range env {
			env[k] = strings.ReplaceAll(v, "STUB", stub)
		}
		out, err := runNginxOnce(t, stub, env, "nginx", "-t")
		if err == nil {
			t.Errorf("%s: started with %q; want a refusal\n%s", what, env, out)
		}
	}
}

func TestConfigLoads(t *testing.T) {
	t.Parallel()
	stub := startStub(t)
	for what, env := range map[string]map[string]string{
		"defaults": nil,
		"everything set": {
			"NGINX_TRUSTED_PROXIES":     "10.0.0.1, 192.168.0.0/16 2001:db8::/32",
			"NGINX_CSP_SENTRY_ORIGIN":   "https://o1.ingest.sentry.example",
			"NGINX_CSP_CONNECT_ORIGINS": "https://a.example.test",
			"API_BASE_URL":              "https://api.example.test/api",
		},
	} {
		out, err := runNginxOnce(t, stub, env, "nginx", "-t")
		if err != nil || !strings.Contains(out, "test is successful") {
			t.Errorf("%s: nginx -t failed: %v\n%s", what, err, out)
		}
	}
}

// ── AC-B5 / AC-B6 / AC-B7 ───────────────────────────────────────────────────

func lastFor(t *testing.T, stub, uri string) Recorded {
	t.Helper()
	var found *Recorded
	for _, r := range requestsSeen(t, stub) {
		r := r
		if r.URI == uri {
			found = &r
		}
	}
	if found == nil {
		t.Fatalf("upstream never received %s", uri)
	}
	return *found
}

func header(r Recorded, k string) string { return strings.Join(r.Header[k], ",") }

func TestTrustedProxyForwardsRealClient(t *testing.T) {
	t.Parallel()
	stub := startStub(t)
	proxy := fixedIP()
	n := startNginx(t, stub, map[string]string{"NGINX_TRUSTED_PROXIES": proxy})
	sendOne(t, proxy, "http://"+n+"/api/b5",
		"-H", "X-Forwarded-For: "+forgedClient+", "+realClient,
		"-H", "X-Forwarded-Proto: https")
	got := lastFor(t, stub, "/api/b5")
	if v := header(got, "X-Real-Ip"); v != realClient {
		t.Errorf("X-Real-IP = %q, want %q", v, realClient)
	}
	// The chain as P sent it, plus P itself: the client appears once and the
	// proxy hop is kept (review of #245, non-blocking b).
	if v, want := header(got, "X-Forwarded-For"), forgedClient+", "+realClient+", "+proxy; v != want {
		t.Errorf("X-Forwarded-For = %q, want %q", v, want)
	}
	// A trusted proxy that sends no chain: the proxy is the client.
	sendOne(t, proxy, "http://"+n+"/api/b5-nochain")
	nochain := lastFor(t, stub, "/api/b5-nochain")
	if v := header(nochain, "X-Forwarded-For"); v != proxy {
		t.Errorf("no incoming chain: X-Forwarded-For = %q, want %q", v, proxy)
	}
	if v := header(nochain, "X-Real-Ip"); v != proxy {
		t.Errorf("no incoming chain: X-Real-IP = %q, want %q", v, proxy)
	}
	if v := header(got, "X-Forwarded-Proto"); v != "https" {
		t.Errorf("X-Forwarded-Proto = %q, want https", v)
	}
	// The SPA location forwards the same identity.
	sendOne(t, proxy, "http://"+n+"/b5-page", "-H", "X-Forwarded-For: "+realClient)
	if v := header(lastFor(t, stub, "/b5-page"), "X-Real-Ip"); v != realClient {
		t.Errorf("frontend X-Real-IP = %q, want %q", v, realClient)
	}
	logs := nginxLogs(t, n)
	if !regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(realClient) + ` .*/api/b5`).MatchString(logs) {
		t.Errorf("access log has no line for %s with client %s:\n%s", "/api/b5", realClient, logs)
	}
}

func assertPeerIsClient(t *testing.T, stub, n, peer, uri string) {
	t.Helper()
	sendOne(t, peer, "http://"+n+uri,
		"-H", "X-Forwarded-For: "+realClient,
		"-H", "X-Real-IP: "+realClient,
		"-H", "X-Forwarded-Proto: https")
	got := lastFor(t, stub, uri)
	if v := header(got, "X-Real-Ip"); v != peer {
		t.Errorf("X-Real-IP = %q, want the peer %q", v, peer)
	}
	if v := header(got, "X-Forwarded-Proto"); v != "http" {
		t.Errorf("X-Forwarded-Proto = %q, want http", v)
	}
	if v := header(got, "X-Forwarded-For"); v != peer {
		t.Errorf("X-Forwarded-For = %q, want only the peer %q (a forged chain is dropped)", v, peer)
	}
	if !regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(peer) + ` .*` + regexp.QuoteMeta(uri)).MatchString(nginxLogs(t, n)) {
		t.Errorf("access log does not show the peer %s for %s", peer, uri)
	}
}

func TestUntrustedPeerIsTheClient(t *testing.T) {
	t.Parallel()
	stub := startStub(t)
	trusted, other := fixedIP(), fixedIP()
	n := startNginx(t, stub, map[string]string{"NGINX_TRUSTED_PROXIES": trusted})
	assertPeerIsClient(t, stub, n, other, "/api/b6")
}

func TestNoTrustedListByDefault(t *testing.T) {
	t.Parallel()
	stub := startStub(t)
	n := startNginx(t, stub, nil)
	assertPeerIsClient(t, stub, n, fixedIP(), "/api/b7")
	n2 := startNginx(t, stub, map[string]string{"NGINX_TRUSTED_PROXIES": ""})
	assertPeerIsClient(t, stub, n2, fixedIP(), "/api/b7-empty")
}

// ── AC-B8 ───────────────────────────────────────────────────────────────────

func TestSlowUpstreamIsNotCut(t *testing.T) {
	t.Parallel()
	stub := startStub(t)
	n := startNginx(t, stub, nil)
	r := sendOne(t, "", "http://"+n+"/api/sleep/125s", "-timeout", "200s")
	if r.Status != 200 || !strings.Contains(r.Body, "2m5s") {
		t.Errorf("after 125 s: status %d body %q, want the upstream's 200", r.Status, r.Body)
	}
}

func TestOversizedAPIBodyIsJSON413(t *testing.T) {
	t.Parallel()
	stub := startStub(t)
	n := startNginx(t, stub, nil)
	r := sendOne(t, "", "http://"+n+"/api/too-big", "-method", "POST", "-body-size", "1048577")
	if r.Status != 413 {
		t.Fatalf("status %d, want 413", r.Status)
	}
	if ct := one(t, "413", r, "Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type %q, want application/json", ct)
	}
	// Byte-for-byte what the backend's writeError (json.Encoder) writes for
	// its own 413: the body the Part A contract binds nginx to.
	if want := `{"error":"request body too large"}` + "\n"; r.Body != want {
		t.Errorf("body %q, want %q", r.Body, want)
	}
	for _, rec := range requestsSeen(t, stub) {
		if rec.URI == "/api/too-big" {
			t.Errorf("the upstream was contacted for an oversized body")
		}
	}
	// The cap itself still passes.
	ok := sendOne(t, "", "http://"+n+"/api/at-cap", "-method", "POST", "-body-size", "1048576")
	if ok.Status != 200 || lastFor(t, stub, "/api/at-cap").BodyLen != 1048576 {
		t.Errorf("a 1 MiB body was not proxied whole: status %d", ok.Status)
	}
}

// ── AC-B9 ───────────────────────────────────────────────────────────────────

func TestUpstreamKeepAlive(t *testing.T) {
	t.Parallel()
	stub := startStub(t)
	n := startNginx(t, stub, nil)
	for _, r := range send(t, "", "http://"+n+"/api/health", "-n", "20") {
		if r.Status != 200 {
			t.Fatalf("status %d", r.Status)
		}
	}
	c0 := connsSeen(t, stub)
	t.Logf("conns %v", c0)
	if c := c0["backend"]; c >= 20 {
		t.Errorf("upstream accepted %d connections for 20 requests; want fewer (keep-alive)", c)
	}
}

// ── AC-B11 ──────────────────────────────────────────────────────────────────

func TestHealthPath(t *testing.T) {
	t.Parallel()
	stub := startStub(t)
	n := startNginx(t, stub, nil)
	r := sendOne(t, "", "http://"+n+"/nginx-health")
	if r.Status != 200 {
		t.Fatalf("status %d", r.Status)
	}
	if recs := requestsSeen(t, stub); len(recs) != 0 {
		t.Errorf("the health path contacted an upstream: %+v", recs)
	}
	// Control: an ordinary request does produce a log line, so the absence
	// below is not an artefact of logging being off.
	sendOne(t, "", "http://"+n+"/api/health")
	logs := nginxLogs(t, n)
	if !strings.Contains(logs, "/api/health") {
		t.Fatalf("control request not in the access log:\n%s", logs)
	}
	if strings.Contains(logs, "/nginx-health") {
		t.Errorf("the health path wrote an access-log line:\n%s", logs)
	}
}

// ── AC-B12 ──────────────────────────────────────────────────────────────────

func TestNoDocsRoute(t *testing.T) {
	t.Parallel()
	conf := stripComments(readRepoFile(t, "nginx/nginx.conf"))
	if regexp.MustCompile(`location[^{]*docs`).MatchString(conf) {
		t.Error("nginx.conf has a docs location (D-16: the docs are not served by this stack)")
	}
	stub := startStub(t)
	n := startNginx(t, stub, nil)
	r := sendOne(t, "", "http://"+n+"/docs/")
	if r.Status != 200 || !strings.Contains(r.Body, `<div id="root">`) {
		t.Errorf("/docs/: status %d body %q, want the SPA", r.Status, r.Body)
	}
	if got := lastFor(t, stub, "/docs/"); got.Upstream != "frontend" {
		t.Errorf("/docs/ went to %s, want the SPA (frontend)", got.Upstream)
	}
	if !strings.Contains(one(t, "/docs/", r, "Content-Security-Policy"), "script-src 'self';") {
		t.Errorf("/docs/ is not under the app's CSP")
	}
}
