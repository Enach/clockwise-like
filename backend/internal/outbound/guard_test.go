package outbound

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// AC-A3 wiring guard: no backend code calls out through http.DefaultClient
// (no overall timeout), and every http.Client literal sets a Timeout. The
// clients built here enforce the limits (TestClientFailsWithinLimit); the
// per-site tests in calendar, conference, nlp, auth and api exercise the
// sites that have a test seam.
func TestNoUnboundedHTTPClient(t *testing.T) {
	root := filepath.Join("..", "..")
	literal := regexp.MustCompile(`http\.Client\{`)
	// Package-level helpers on http.DefaultClient, a zero client, and
	// golang.org/x/oauth2 clients (whose Timeout is not set by the library).
	forbidden := regexp.MustCompile(`http\.(Get|Post|Head|PostForm)\(|new\(http\.Client\)`)
	oauthClient := regexp.MustCompile(`oauth2\.NewClient\(|\.Client\(ctx`)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == "gen" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.Contains(path, "outboundtest") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(b)
		if strings.Contains(src, "http.DefaultClient") {
			t.Errorf("%s uses http.DefaultClient (no overall timeout)", path)
		}
		for _, m := range forbidden.FindAllString(src, -1) {
			t.Errorf("%s: %s has no overall timeout", path, m)
		}
		for _, loc := range oauthClient.FindAllStringIndex(src, -1) {
			rest := src[loc[1]:]
			if n := strings.Index(rest, "\n"); n >= 0 {
				// The statement after the one building the client must set
				// its Timeout.
				next := rest[n+1:]
				if end := strings.Index(next, "\n"); end < 0 || !strings.Contains(next[:end], ".Timeout =") {
					t.Errorf("%s: an oauth2 client without a Timeout set on the next line", path)
				}
			}
		}
		for _, loc := range literal.FindAllStringIndex(src, -1) {
			end := strings.Index(src[loc[1]:], "}")
			body := src[loc[1] : loc[1]+end]
			if !strings.Contains(body, "Timeout") {
				t.Errorf("%s: an http.Client without a Timeout: %q", path, body)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
