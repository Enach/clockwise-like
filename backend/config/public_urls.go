package config

import (
	"fmt"
	"net/url"
	"strings"
)

// InsecurePublicURLs returns one startup warning per configured public URL
// that is http:// on a host other than localhost, 127.0.0.1 or [::1] while
// cookies are Secure: browsers then drop the session cookie (AC-A15). It
// never refuses startup.
func InsecurePublicURLs(getenv func(string) string, secureCookies bool) []string {
	if !secureCookies {
		return nil
	}
	// The public URLs the backend puts in redirects and links (PAC-81 F-A9),
	// each read by its literal name (the env reference test reads them).
	settings := []struct{ name, value string }{
		{"FRONTEND_URL", getenv("FRONTEND_URL")},
		{"APP_URL", getenv("APP_URL")},
		{"BASE_URL", getenv("BASE_URL")},
	}
	var out []string
	for _, st := range settings {
		name := st.name
		raw := strings.TrimSpace(st.value)
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || !strings.EqualFold(u.Scheme, "http") {
			continue
		}
		switch strings.ToLower(u.Hostname()) {
		case "localhost", "127.0.0.1", "::1":
			continue
		}
		out = append(out, fmt.Sprintf("%s is %s (http://) while cookies are Secure: browsers will drop the session cookie on this origin; use https:// behind a TLS-terminating proxy, or set %s=true for plain-HTTP development only", name, raw, EnvInsecureCookies))
	}
	return out
}
