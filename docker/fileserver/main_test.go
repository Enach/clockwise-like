package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PAC-81 Part B (AC-B3(b), spec Q-P2): the API base is delivered by a
// same-origin script, never by an inline <script>, so nginx's CSP needs no
// 'unsafe-inline' and no hash.

const indexHTML = `<!doctype html><html><head><title>t</title></head><body><div id="root"></div><script type="module" src="/assets/index.js"></script></body></html>`

func newTestRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(indexHTML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "index.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// result is what a client would see: status, headers and body.
type result struct {
	StatusCode int
	Header     http.Header
}

func get(t *testing.T, h http.Handler, path string) (result, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	body, _ := io.ReadAll(rec.Body)
	return result{StatusCode: rec.Code, Header: rec.Header()}, string(body)
}

// inlineScripts returns every <script> tag in html that has no src attribute.
func inlineScripts(html string) []string {
	var out []string
	rest := html
	for {
		i := strings.Index(rest, "<script")
		if i < 0 {
			return out
		}
		j := strings.Index(rest[i:], ">")
		if j < 0 {
			return out
		}
		tag := rest[i : i+j+1]
		if !strings.Contains(tag, " src=") {
			out = append(out, tag)
		}
		rest = rest[i+j+1:]
	}
}

func TestIndexHasNoInlineScriptWhenAPIBaseIsSet(t *testing.T) {
	h, err := newHandler(newTestRoot(t), "https://api.example.test/api")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/", "/index.html", "/app/some/deep/link"} {
		res, body := get(t, h, p)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d", p, res.StatusCode)
		}
		if got := inlineScripts(body); len(got) != 0 {
			t.Errorf("%s: inline script(s) present: %q", p, got)
		}
		tag := `<script src="` + runtimeConfigPath + `"></script></head>`
		if !strings.Contains(body, tag) {
			t.Errorf("%s: runtime config script tag not injected before </head>; body=%s", p, body)
		}
		// Classic script before the module bundle, so it runs first.
		if strings.Index(body, runtimeConfigPath) > strings.Index(body, "/assets/index.js") {
			t.Errorf("%s: runtime config is loaded after the bundle", p)
		}
	}
}

func TestRuntimeConfigScript(t *testing.T) {
	h, err := newHandler(newTestRoot(t), "https://api.example.test/api")
	if err != nil {
		t.Fatal(err)
	}
	res, body := get(t, h, runtimeConfigPath)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := res.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q", cc)
	}
	want := `window.__API_BASE_URL__="https://api.example.test/api";` + "\n"
	if body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
}

// The value used to be concatenated raw into an inline script. It is now a
// JSON string literal, so it cannot end the string or the script element.
func TestRuntimeConfigEscapesTheValue(t *testing.T) {
	h, err := newHandler(newTestRoot(t), `x";alert(1);//</script><script>`)
	if err != nil {
		t.Fatal(err)
	}
	_, body := get(t, h, runtimeConfigPath)
	bs := string(rune(92)) // a backslash, spelled out so no tool can unescape it
	want := `window.__API_BASE_URL__="x` + bs + `";alert(1);//` + bs + "u003c/script" + bs + "u003e" + bs + "u003cscript" + bs + `u003e";` + "\n"
	if body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
	_, html := get(t, h, "/")
	if strings.Contains(html, "alert(1)") {
		t.Errorf("the setting's value leaked into the HTML: %s", html)
	}
}

func TestNoInjectionWhenAPIBaseIsUnset(t *testing.T) {
	h, err := newHandler(newTestRoot(t), "")
	if err != nil {
		t.Fatal(err)
	}
	_, body := get(t, h, "/")
	if body != indexHTML {
		t.Errorf("index changed although API_BASE_URL is unset: %s", body)
	}
	res, js := get(t, h, runtimeConfigPath)
	if res.StatusCode != http.StatusOK || strings.Contains(js, "__API_BASE_URL__=") {
		t.Errorf("runtime config with no setting: status %d body %q", res.StatusCode, js)
	}
}

func TestStaticAssetAndFallback(t *testing.T) {
	h, err := newHandler(newTestRoot(t), "https://api.example.test/api")
	if err != nil {
		t.Fatal(err)
	}
	res, body := get(t, h, "/assets/index.js")
	if res.StatusCode != http.StatusOK || body != "console.log(1)" {
		t.Errorf("asset: status %d body %q", res.StatusCode, body)
	}
	res, _ = get(t, h, "/no/such/route")
	if ct := res.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("fallback Content-Type = %q", ct)
	}
	if cc := res.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("fallback Cache-Control = %q", cc)
	}
}

func TestMissingIndexIsAnError(t *testing.T) {
	if _, err := newHandler(t.TempDir(), ""); err == nil {
		t.Fatal("expected an error for a root without index.html")
	}
}

// Without </head> the API-base script tag cannot be injected; the server
// must refuse to start rather than serve a page that silently ignores
// API_BASE_URL (review of #245, non-blocking d).
func TestIndexWithoutHeadIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html><body>no head</body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, apiBase := range []string{"", "https://api.example.test/api"} {
		if _, err := newHandler(dir, apiBase); err == nil {
			t.Errorf("API_BASE_URL=%q: expected an error for an index.html without </head>", apiBase)
		}
	}
}
