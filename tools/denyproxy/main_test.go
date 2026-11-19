package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A plain request and a CONNECT through the proxy are both refused with 403
// and both recorded, so a client using the environment's proxy fails loudly.
func TestDeniesAndRecordsPlainAndConnect(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "denied.log")
	out, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	proxy := httptest.NewServer(&handler{out: out})
	defer proxy.Close()
	pu, _ := url.Parse(proxy.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}

	get := func(u string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u, nil)
		if err != nil {
			t.Fatal(err)
		}
		return client.Do(req)
	}

	resp, err := get("http://slack.example/api/oauth.v2.access")
	if err != nil {
		t.Fatalf("plain request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("plain request: status %d, want 403", resp.StatusCode)
	}

	// An https URL goes through CONNECT; Go reports the refusal as an error.
	resp, err = get("https://slack.example/api/oauth.v2.access")
	if err == nil {
		resp.Body.Close()
		t.Fatalf("CONNECT: status %d, want a Forbidden proxy error", resp.StatusCode)
	}
	if !strings.Contains(err.Error(), "Forbidden") {
		t.Fatalf("CONNECT: err = %v, want a Forbidden proxy error", err)
	}

	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "GET slack.example\nCONNECT slack.example:443\n"
	if string(got) != want {
		t.Fatalf("log = %q, want %q", got, want)
	}
}
