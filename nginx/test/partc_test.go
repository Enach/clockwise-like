package nginxtest

// PAC-81 Part C — the nginx half of the owner decisions of 2026-11-01
// (docs/specs/NEW-deploy-hardening.md revision 3.3, docs/plans/PAC-81-part-c.md):
//   - Q-O2: only the public booking page /book/{slug} may be framed;
//   - Q-O1: MCP is served on the public origin at /mcp/, streamed, when the
//     operator enables it, and is a 404 otherwise.

import (
	"regexp"
	"strings"
	"testing"
)

// ── Q-O2 ────────────────────────────────────────────────────────────────────

func TestFramingOnlyBookingPage(t *testing.T) {
	t.Parallel()
	stub := startStub(t)
	n := startNginx(t, stub, nil)
	cases := []struct {
		path     string
		framable bool
	}{
		{"/book/abc123", true},
		{"/book/abc123/", true},
		{"/book/abc123?utm=x", true},
		{"/book", false},
		{"/book/", false},
		{"/book/abc123/extra", false},
		{"/bookings/abc123", false},
		{"/app/book/abc123", false},
		{"/", false},
		{"/app", false},
		{"/app/links", false},
		{"/settings", false},
		{"/signin", false},
		{"/api/book/abc123", false},
		{"/api/book/abc123/slots", false},
		{"/assets/index-abc.js", false},
		{"/nginx-health", false},
		// Review of #248: encodings, case and dot segments. $uri is decoded
		// and normalised (%2F is a slash, dot segments resolved) and matching
		// is case-sensitive; since PAC-91 the raw path must match too.
		{"/book/x%2Fapp", false},     // $uri /book/x/app: two segments
		{"/BOOK/x", false},           // case-sensitive
		{"/Book/x", false},           // case-sensitive
		{"/book/..", false},          // normalised to /
		{"/book/x/..", false},        // normalised to /book/
		{"/book/x/../../app", false}, // normalised to /app
		// PAC-91 (spec D-3) reverses the PAC-81 review here: framability is
		// decided on the raw path as well as $uri, and the raw path has a
		// literal dot segment, so it is refused even though $uri is
		// /book/app. Browsers resolve literal dot segments before sending,
		// so no visitor requests it. Full table in pac91_test.go.
		{"/book/x/../app", false},
		{"/book/%61bc", true}, // $uri /book/abc, raw segment plain
	}
	for _, c := range cases {
		r := sendOne(t, "", "http://"+n+c.path)
		csp := parseCSP(t, one(t, c.path, r, "Content-Security-Policy"))
		xfo, hasXFO := r.Header["X-Frame-Options"]
		if c.framable {
			if got := csp["frame-ancestors"]; !sameSet(got, []string{"*"}) {
				t.Errorf("%s: frame-ancestors %q, want *", c.path, got)
			}
			if hasXFO {
				t.Errorf("%s: X-Frame-Options %q present; the booking page must be framable", c.path, xfo)
			}
		} else {
			if got := csp["frame-ancestors"]; !sameSet(got, []string{"'none'"}) {
				t.Errorf("%s: frame-ancestors %q, want 'none'", c.path, got)
			}
			if v := one(t, c.path, r, "X-Frame-Options"); v != "DENY" {
				t.Errorf("%s: X-Frame-Options %q, want DENY", c.path, v)
			}
		}
		// Everything but frame-ancestors is the same policy on every route.
		delete(csp, "frame-ancestors")
		for d, want := range map[string][]string{
			"default-src": {"'self'"}, "script-src": {"'self'"}, "object-src": {"'none'"},
			"base-uri": {"'self'"}, "form-action": {"'self'"},
		} {
			if !sameSet(csp[d], want) {
				t.Errorf("%s: %s = %q, want %q", c.path, d, csp[d], want)
			}
		}
		if v := one(t, c.path, r, "X-Content-Type-Options"); v != "" && v != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options %q", c.path, v)
		}
		if v := one(t, c.path, r, "Referrer-Policy"); v != "" && v != "strict-origin-when-cross-origin" {
			t.Errorf("%s: Referrer-Policy %q", c.path, v)
		}
	}
	// The booking page is still the SPA, proxied to the frontend.
	if got := lastFor(t, stub, "/book/abc123"); got.Upstream != "frontend" {
		t.Errorf("/book/abc123 went to %s, want the frontend", got.Upstream)
	}
}

// ── Q-O1 ────────────────────────────────────────────────────────────────────

func TestMCPRouteDisabledByDefault(t *testing.T) {
	t.Parallel()
	stub := startStub(t)
	n := startNginx(t, stub, nil)
	for _, path := range []string{"/mcp/sse", "/mcp/message?sessionId=x", "/mcp/"} {
		r := sendOne(t, "", "http://"+n+path, "-H", "Authorization: Bearer whatever")
		if r.Status != 404 {
			t.Errorf("%s with MCP off: status %d, want 404", path, r.Status)
		}
		if ct := one(t, path, r, "Content-Type"); ct != "application/json" || r.Body != `{"error":"not found"}`+"\n" {
			t.Errorf("%s with MCP off: %q %q, want the JSON 404", path, ct, r.Body)
		}
		if v := one(t, path, r, "X-Frame-Options"); v != "DENY" {
			t.Errorf("%s: X-Frame-Options %q", path, v)
		}
	}
	for _, rec := range requestsSeen(t, stub) {
		if strings.HasPrefix(rec.URI, "/mcp") {
			t.Errorf("an upstream (%s) received %s while MCP is off", rec.Upstream, rec.URI)
		}
	}
}

func TestMCPRouteProxiedAndStreamed(t *testing.T) {
	t.Parallel()
	stub := startStub(t)
	proxy := fixedIP()
	n := startNginx(t, stub, map[string]string{
		"NGINX_MCP_UPSTREAM":    stub + ":8082",
		"NGINX_TRUSTED_PROXIES": proxy,
	})

	// Proxied without rewriting the path, the bearer token and the client
	// identity forwarded, the query string kept.
	r := sendOne(t, proxy, "http://"+n+"/mcp/message?sessionId=s1", "-method", "POST", "-body-size", "64",
		"-H", "Authorization: Bearer tok-123", "-H", "X-Forwarded-For: "+realClient, "-H", "X-Forwarded-Proto: https")
	if r.Status != 200 {
		t.Fatalf("POST /mcp/message: status %d", r.Status)
	}
	got := lastFor(t, stub, "/mcp/message?sessionId=s1")
	if got.Upstream != "mcp" {
		t.Errorf("/mcp/message went to %s, want mcp", got.Upstream)
	}
	if v := header(got, "Authorization"); v != "Bearer tok-123" {
		t.Errorf("Authorization upstream = %q", v)
	}
	if v := header(got, "X-Real-Ip"); v != realClient {
		t.Errorf("X-Real-IP upstream = %q, want %q", v, realClient)
	}
	if v := header(got, "X-Forwarded-Proto"); v != "https" {
		t.Errorf("X-Forwarded-Proto upstream = %q", v)
	}
	if got.BodyLen != 64 {
		t.Errorf("body %d bytes upstream, want 64", got.BodyLen)
	}
	// The session id in the query string is not written to the access log.
	if logs := nginxLogs(t, n); strings.Contains(logs, "sessionId") || !strings.Contains(logs, "/mcp/message") {
		t.Errorf("access log for /mcp/message is missing or carries the query string:\n%s", logs)
	}

	// SSE is streamed: the first event arrives at once, not when the
	// response ends 3 s later.
	s := sendOne(t, "", "http://"+n+"/mcp/stream", "-timeout", "20s")
	if s.Status != 200 || !strings.Contains(s.Body, "event: second") {
		t.Fatalf("/mcp/stream: status %d body %q", s.Status, s.Body)
	}
	if s.TotalMs < 2500 {
		t.Fatalf("/mcp/stream ended after %d ms; the stub waits 3 s, so timing is not measurable", s.TotalMs)
	}
	if s.FirstByteMs > 1500 {
		t.Errorf("/mcp/stream: first byte after %d ms (total %d ms): nginx buffered the event stream", s.FirstByteMs, s.TotalMs)
	}
	if v := one(t, "/mcp/stream", s, "Content-Security-Policy"); !strings.Contains(v, "frame-ancestors 'none'") {
		t.Errorf("/mcp/stream: not the app's CSP: %q", v)
	}

	// Everything else still goes where it went.
	sendOne(t, "", "http://"+n+"/api/partc")
	if lastFor(t, stub, "/api/partc").Upstream != "backend" {
		t.Error("/api/ no longer reaches the backend with MCP on")
	}
	sendOne(t, "", "http://"+n+"/mcpx")
	if lastFor(t, stub, "/mcpx").Upstream != "frontend" {
		t.Error("/mcpx (not /mcp/) did not go to the SPA")
	}
}

func TestMCPUpstreamSettingIsValidated(t *testing.T) {
	t.Parallel()
	stub := startStub(t)
	nl := string(rune(10))
	for what, v := range map[string]string{
		"no port":    "mcp-server",
		"a URL":      "http://mcp-server:3001",
		"injection":  "mcp-server:3001; server evil:1",
		"newline":    "mcp-server:3001" + nl,
		"whitespace": "mcp-server :3001",
	} {
		out, err := runNginxOnce(t, stub, map[string]string{"NGINX_MCP_UPSTREAM": v}, "nginx", "-t")
		if err == nil {
			t.Errorf("%s: NGINX_MCP_UPSTREAM=%q accepted\n%s", what, v, out)
		} else if !strings.Contains(out, "NGINX_MCP_UPSTREAM") {
			t.Errorf("%s: refusal does not name NGINX_MCP_UPSTREAM:\n%s", what, out)
		}
	}
	out, err := runNginxOnce(t, stub, map[string]string{"NGINX_MCP_UPSTREAM": stub + ":8082"}, "nginx", "-t")
	if err != nil || !strings.Contains(out, "test is successful") {
		t.Errorf("a valid NGINX_MCP_UPSTREAM did not load: %v\n%s", err, out)
	}
}

// An SSE stream may be idle for longer than the 130 s that bounds every other
// upstream read, and the /mcp/ location keeps buffering off. (The timing check
// above also passes with buffering on in this nginx version, so the directive
// itself is asserted here.)
func TestMCPStreamIdleLongerThanAPIBound(t *testing.T) {
	t.Parallel()
	conf := stripComments(readRepoFile(t, "nginx/nginx.conf"))
	loc := regexp.MustCompile(`(?s)location /mcp/ \{.*?\n    \}`).FindString(conf)
	for _, want := range []string{"proxy_buffering off;", "proxy_cache off;", "proxy_read_timeout 1h;", "proxy_pass http://paceday_mcp;"} {
		if !strings.Contains(loc, want) {
			t.Errorf("location /mcp/ lacks %q:\n%s", want, loc)
		}
	}
	if strings.Contains(loc, "add_header") || strings.Contains(loc, "proxy_set_header") {
		t.Error("location /mcp/ sets its own headers, which would drop the server-level ones")
	}
	stub := startStub(t)
	n := startNginx(t, stub, map[string]string{"NGINX_MCP_UPSTREAM": stub + ":8082"})
	r := sendOne(t, "", "http://"+n+"/mcp/sleep/135s", "-timeout", "200s")
	if r.Status != 200 || !strings.Contains(r.Body, "event: late") {
		t.Errorf("an SSE stream idle 135 s: status %d body %q, want the late event", r.Status, r.Body)
	}
}
