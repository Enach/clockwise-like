package config

import (
	"strings"
	"testing"
)

func TestInsecurePublicURLs(t *testing.T) {
	env := envOf(map[string]string{
		"FRONTEND_URL": "http://app.example.com",
		"APP_URL":      "https://api.example.com",
		"BASE_URL":     "http://10.0.0.5:8080",
	})
	got := InsecurePublicURLs(env, true)
	if len(got) != 2 || !strings.HasPrefix(got[0], "FRONTEND_URL ") || !strings.HasPrefix(got[1], "BASE_URL ") {
		t.Fatalf("got %v", got)
	}
	if InsecurePublicURLs(env, false) != nil {
		t.Fatal("warned with insecure cookies enabled")
	}
	for _, local := range []string{"http://localhost:5173", "http://127.0.0.1", "http://[::1]:8080"} {
		if w := InsecurePublicURLs(envOf(map[string]string{"FRONTEND_URL": local}), true); len(w) != 0 {
			t.Fatalf("%s warned: %v", local, w)
		}
	}
	if w := InsecurePublicURLs(envOf(map[string]string{"FRONTEND_URL": "https://a", "APP_URL": "https://b", "BASE_URL": "https://c"}), true); len(w) != 0 {
		t.Fatalf("https warned: %v", w)
	}
}
