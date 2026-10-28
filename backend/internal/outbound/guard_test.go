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
	literal := regexp.MustCompile(`&http\.Client\{`)
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
