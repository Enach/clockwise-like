package auth

import (
	"net/url"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

// PAC-35 (API-064): the LoginHint rule of components.parameters.LoginHint.
func TestValidLoginHint(t *testing.T) {
	forwarded := []string{
		"alice@acme.com",
		"o'brien@acme.com",
		"first.last+tag@eu.acme.co.uk",
		"a_b-c@x1.example",
	}
	for _, v := range forwarded {
		if !ValidLoginHint(v) {
			t.Errorf("ValidLoginHint(%q) = false, want true", v)
		}
	}
	dropped := []string{
		"",
		"alice",
		"alice@acme.com&prompt=none",
		"alice&prompt=none@acme.com",
		"alice@acme.com?x=y",
		"alice@acme.com#frag",
		"alice@[127.0.0.1]",
		"alice@127.0.0.1",
		"alice@localhost",
		"alice@acme",
		"alice@@acme.com",
		"a@b@acme.com",
		"Alice <alice@acme.com>",
		" alice@acme.com",
		"alice@acme.com ",
		"alice(comment)@acme.com",
		"\"quoted\"@acme.com",
		"alice@-acme.com",
		"alice@acme-.com",
		"alice@acme.123",
		"alice%40x@acme.com",
		strings.Repeat("a", 64) + "@" + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 63) + ".com",
	}
	for _, v := range dropped {
		if ValidLoginHint(v) {
			t.Errorf("ValidLoginHint(%q) = true, want false", v)
		}
	}
}

func authURLQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u.Query()
}

func TestGetAuthURL_LoginHint(t *testing.T) {
	cfg := NewGoogleOAuthConfig("client-id", "secret", "http://localhost/callback")
	q := authURLQuery(t, GetAuthURL(cfg, "s", "o'brien@acme.com"))
	if got := q.Get("login_hint"); got != "o'brien@acme.com" {
		t.Errorf("login_hint = %q, want o'brien@acme.com", got)
	}
	if q.Get("state") != "s" {
		t.Errorf("state = %q", q.Get("state"))
	}
	q = authURLQuery(t, GetAuthURL(cfg, "s", ""))
	if _, ok := q["login_hint"]; ok {
		t.Errorf("an empty hint must not be sent: %v", q)
	}
}

func TestGetMicrosoftAuthURL_LoginHint(t *testing.T) {
	cfg := &MicrosoftConfig{ClientID: "id", RedirectURL: "http://localhost/cb"}
	raw := GetMicrosoftAuthURL(cfg, "s", "alice@acme.com")
	if !strings.HasPrefix(raw, microsoftEndpoint.AuthURL) {
		t.Errorf("default endpoint not used: %s", raw)
	}
	if got := authURLQuery(t, raw).Get("login_hint"); got != "alice@acme.com" {
		t.Errorf("login_hint = %q", got)
	}
	if _, ok := authURLQuery(t, GetMicrosoftAuthURL(cfg, "s", ""))["login_hint"]; ok {
		t.Error("an empty hint must not be sent")
	}
	cfg.Endpoint = oauth2.Endpoint{AuthURL: "http://idp.test/authorize", TokenURL: "http://idp.test/token"}
	if raw := GetMicrosoftAuthURL(cfg, "s", ""); !strings.HasPrefix(raw, "http://idp.test/authorize") {
		t.Errorf("endpoint override not used: %s", raw)
	}
}
