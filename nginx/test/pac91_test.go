package nginxtest

// PAC-91 (docs/specs/PAC-91.md revision 2, contracts/features/PAC-91.md
// section 2): framability is decided on the raw path too. A response is
// framable only when BOTH the normalised $uri and the raw path ($request_uri
// cut at its first '?', escapes compared case-insensitively, query ignored)
// are /book/<one plain segment>[/]. Otherwise a hostile page could frame an
// address that nginx normalises to the booking page while the browser and the
// app see (and render) another page.
//
// The paths are sent exactly as written: the stub client is Go's net/http,
// which sends a URL's escaped path as given when it is a valid encoding, and
// never resolves dot segments or merges slashes. Each 200 case also checks
// that the frontend received the raw path unchanged, which proves it.

import (
	"strings"
	"testing"
)

type framingCase struct {
	path     string
	framable bool
	status   int    // expected status; 0 means 200 through the SPA location
	why      string // the decision-table row or the probe it pins
}

// pac91DecisionTable is contract section 2.4, row for row.
var pac91DecisionTable = []framingCase{
	// ── Refused, AC-12: normalises to a booking path, browser/app see another.
	{"/app/team/invite/..%2F..%2F..%2Fbook%2Fx", false, 0, "AC-12: app shows team invite"},
	{"/settings/..%2Fbook/x", false, 0, "AC-12: app shows not-found"},
	{"/xyz/..%2Fbook%2Fabc", false, 0, "AC-12"},
	{"/xyz/..%2fbook%2fabc", false, 0, "AC-12: lower-case escapes"},
	{"/xyz/%2E%2E%2Fbook%2Fabc", false, 0, "AC-12: encoded dots"},
	{"/%2Fbook/x", false, 0, "AC-12: encoded slash outside the segment"},
	{"/book%2Fx", false, 0, "AC-12: encoded slash outside the segment"},
	{"/book%2fx", false, 0, "AC-12: encoded slash outside the segment"},
	{"/book//abc", false, 0, "AC-12: empty segment, nginx merges slashes"},
	{"//book/abc", false, 0, "AC-12: empty segment, nginx merges slashes"},
	{"/book/x/../app", false, 0, "AC-12: literal dot segment (reverses PAC-81 partc_test row)"},
	{"/book/./abc", false, 0, "AC-12: literal dot segment"},
	{"/book/abc/.", false, 0, "AC-12: literal dot segment"},
	// ── Refused, AC-13: the segment itself.
	{"/book/x%2F", false, 0, "AC-13: encoded slash in the segment"},
	{"/book/x%2f", false, 0, "AC-13: encoded slash in the segment"},
	{"/book/x%5C..%5Capp", false, 0, "AC-13: encoded backslash (fail closed)"},
	{"/book/x%5c..%5capp", false, 0, "AC-13: encoded backslash (fail closed)"},
	{"/book/..", false, 0, "AC-13: dot segment"},
	{"/book/.", false, 0, "AC-13: dot segment"},
	{"/book/%2e", false, 0, "AC-13: dot segment, encoded"},
	{"/book/%2E%2e/", false, 0, "AC-13: dot segment, encoded, trailing slash"},
	{"/book/.%2e?x", false, 0, "AC-13: dot segment, mixed, with a query"},
	// ── Refused before routing.
	{"/book/abc%00", false, 400, "nginx answers 400 before routing"},

	// ── Framable, AC-14 (guard: framable today and must stay so).
	{"/book/abc123", true, 0, "AC-14"},
	{"/book/abc123/", true, 0, "AC-14"},
	{"/book/my-slug-30", true, 0, "AC-14"},
	{"/book/abc123?utm=x", true, 0, "AC-14: query ignored"},
	{"/book/abc123?ref=https%3A%2F%2Fexample.com%2F..%2Fapp", true, 0, "AC-14: escapes in the query ignored"},
	{"/book/abc123/?a=/../app", true, 0, "AC-14: dot segments in the query ignored"},
	{"/book/..x", true, 0, "AC-14: a segment that merely contains dots"},
	{"/book/%61bc", true, 0, "AC-14: percent-encoded letter (PAC-81 row kept)"},
	{"/book/caf%C3%A9", true, 0, "AC-14: percent-encoded UTF-8"},
	{"/book/caf%c3%a9", true, 0, "AC-14: percent-encoded UTF-8, lower case"},
}

// pac91Probes go beyond the table (stage 4 security probes). Each row records
// what nginx decides and why that matches what the browser and the app see:
// the app (React Router 7) decodes each path segment once, keeps an encoded
// slash inside its segment, and leaves a segment it cannot decode as is.
var pac91Probes = []framingCase{
	// Double encoding: nginx decodes once, so %252F stays %2F in $uri.
	{"/xyz/..%252Fbook%252Fabc", false, 0, "double encoding outside /book/: $uri is not a booking path"},
	{"/book/x%252F..%252F..%252Fapp", true, 0, "double encoding inside the slug: one segment for nginx and the app (slug x%2F..%2F..%2Fapp)"},
	{"/book/abc%252F", true, 0, "double-encoded slash in the slug: one segment everywhere"},
	{"/book/%252e%252e", true, 0, "double-encoded dots: not a dot segment for nginx, the browser or the app"},
	// Mixed case.
	{"/BOOK/abc", false, 0, "upper-case prefix: refused (the app would show booking; safe direction)"},
	{"/bOOk/abc", false, 0, "mixed-case prefix: refused (safe direction)"},
	{"/b%6Fok/abc", false, 0, "encoded letter in the prefix: raw path is not /book/ (safe direction)"},
	{"/book/ABC", true, 0, "upper-case slug: the app shows the booking page"},
	{"/book/x%2F%2e%2E%2Fapp", false, 0, "mixed-case encoded slash and dots"},
	{"/book/%2e%2E", false, 0, "mixed-case encoded dot segment"},
	// Unicode slash look-alikes: never a separator for nginx, the browser
	// or the app, so the slug is one segment and the app shows the booking page.
	{"/book/x%E2%88%95app", true, 0, "U+2215 DIVISION SLASH in the slug"},
	{"/book/x%EF%BC%8Fapp", true, 0, "U+FF0F FULLWIDTH SOLIDUS in the slug"},
	{"/book/x%E2%81%84app", true, 0, "U+2044 FRACTION SLASH in the slug"},
	{"/settings%EF%BC%8F..%EF%BC%8Fbook/x", false, 0, "fullwidth solidus outside the slug: $uri is not a booking path"},
	// Overlong UTF-8 (invalid): nginx does not decode it to '/' or '.', and
	// the app cannot decode it at all (it keeps the raw segment).
	{"/book/x%C0%AFapp", true, 0, "overlong '/' in the slug: one segment everywhere"},
	{"/book/%C0%AE%C0%AE", true, 0, "overlong '..': not a dot segment anywhere"},
	{"/xyz/%C0%AE%C0%AE%C0%AFbook%C0%AFabc", false, 0, "overlong traversal outside the slug: $uri is not a booking path"},
	// ';' path parameters: nginx, the file server and the app keep ';' as a
	// plain character.
	{"/book/x;y", true, 0, "';' inside the slug"},
	{"/book/..;", true, 0, "'..;' is not a dot segment"},
	{"/xyz/..;/book/abc", false, 0, "'..;' does not climb in nginx: $uri is not a booking path"},
	{"/book/x;/../app", false, 0, "a literal dot segment after ';'"},
	// Trailing and lone spaces.
	{"/book/abc%20", true, 0, "trailing %20: the slug 'abc ' on the booking page"},
	{"/book/%20", true, 0, "slug ' ': the booking page (not found state)"},
	{"/book/..%20", true, 0, "'.. ' is not a dot segment anywhere"},
	{"/book/abc%2F%20", false, 0, "encoded slash then %20"},
	// A framed path inside the query: the app routes on the path only.
	{"/book/x?", true, 0, "empty query"},
	{"/book/x?/../../app", true, 0, "dot segments in the query: the app routes on /book/x"},
	{"/book/x?%2F..%2F..%2Fapp", true, 0, "escaped traversal in the query"},
	{"/book/x?/book/y", true, 0, "a second booking path in the query"},
	{"/app?/book/x", false, 0, "a booking path only in the query"},
	{"/app?x=/book/abc", false, 0, "a booking path only in a parameter"},
	{"/xyz/..%2Fbook%2Fabc?", false, 0, "crafted path with an empty query"},
	{"/book/x%3F..%2Fapp", false, 0, "encoded '?' does not end the raw path"},
	{"/book/%3F", true, 0, "slug '?': one segment everywhere"},
	// Trailing slashes and dot segments after the slug.
	{"/book/abc//", false, 0, "two trailing slashes (React Router would still show booking; safe direction)"},
	{"/book/abc/./", false, 0, "trailing dot segment and slash"},
	{"/book/abc/%2e", false, 0, "trailing encoded dot segment"},
	{"/book/...", true, 0, "three dots is not a dot segment"},
	{"/book/%2e%2e%2e", true, 0, "three encoded dots is not a dot segment"},
}

// assertFraming checks one response against the framability it should have:
// frame-ancestors and X-Frame-Options, and every other CSP directive and
// security header unchanged.
func assertFraming(t *testing.T, path string, r Response, framable bool) {
	t.Helper()
	csp := parseCSP(t, one(t, path, r, "Content-Security-Policy"))
	xfo, hasXFO := r.Header["X-Frame-Options"]
	if framable {
		if got := csp["frame-ancestors"]; !sameSet(got, []string{"*"}) {
			t.Errorf("%s: frame-ancestors %q, want *", path, got)
		}
		if hasXFO {
			t.Errorf("%s: X-Frame-Options %q present; the booking page must be framable", path, xfo)
		}
	} else {
		if got := csp["frame-ancestors"]; !sameSet(got, []string{"'none'"}) {
			t.Errorf("%s: frame-ancestors %q, want 'none'", path, got)
		}
		if v := one(t, path, r, "X-Frame-Options"); v != "DENY" {
			t.Errorf("%s: X-Frame-Options %q, want DENY", path, v)
		}
	}
	delete(csp, "frame-ancestors")
	for d, want := range map[string][]string{
		"default-src": {"'self'"}, "script-src": {"'self'"}, "object-src": {"'none'"},
		"base-uri": {"'self'"}, "form-action": {"'self'"},
	} {
		if !sameSet(csp[d], want) {
			t.Errorf("%s: %s = %q, want %q", path, d, csp[d], want)
		}
	}
	if v := one(t, path, r, "X-Content-Type-Options"); v != "nosniff" {
		t.Errorf("%s: X-Content-Type-Options %q", path, v)
	}
	if v := one(t, path, r, "Referrer-Policy"); v != "strict-origin-when-cross-origin" {
		t.Errorf("%s: Referrer-Policy %q", path, v)
	}
}

func runFramingCases(t *testing.T, cases []framingCase) {
	t.Helper()
	stub := startStub(t)
	n := startNginx(t, stub, nil)
	for _, c := range cases {
		c := c
		t.Run(c.path, func(t *testing.T) {
			t.Parallel()
			r := sendOne(t, "", "http://"+n+c.path)
			want := c.status
			if want == 0 {
				want = 200
			}
			if r.Status != want {
				t.Errorf("%s (%s): status %d, want %d", c.path, c.why, r.Status, want)
			}
			assertFraming(t, c.path+" ("+c.why+")", r, c.framable)
			if c.status == 0 {
				// Reached the SPA with the raw request target unchanged, so the
				// path under test is the one the browser would have sent.
				got := lastFor(t, stub, c.path)
				if got.Upstream != "frontend" {
					t.Errorf("%s went to %s, want the frontend", c.path, got.Upstream)
				}
			}
		})
	}
}

// AC-12, AC-13, AC-14: contract section 2.4.
func TestPAC91FramingDecisionTable(t *testing.T) {
	t.Parallel()
	runFramingCases(t, pac91DecisionTable)
}

// Stage 4 probes beyond the table.
func TestPAC91FramingProbes(t *testing.T) {
	t.Parallel()
	runFramingCases(t, pac91Probes)
}

// The rule lives in the framability maps only (contract allowedPaths): the
// headers are still set once, at server level, keyed on $paceday_framable.
func TestPAC91RuleUsesRawAndNormalisedPath(t *testing.T) {
	t.Parallel()
	conf := stripComments(readRepoFile(t, "nginx/nginx.conf"))
	for _, want := range []string{"map $request_uri ", "$uri", "$paceday_framable", "add_header X-Frame-Options $paceday_frame_options always;"} {
		if !strings.Contains(conf, want) {
			t.Errorf("nginx.conf lacks %q", want)
		}
	}
}
