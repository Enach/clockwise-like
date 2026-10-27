package config

import (
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// PAC-66-B: a user-set base URL for openai, anthropic or ollama is sent to
// only if it names a destination approved for that provider (spec Part B,
// D-B1 to D-B3). The vendors' public hosts are approved by the product, each
// for its own vendor; anything else is approved by the operator, per
// provider. Format and matching are fixed by
// contracts/features/PAC-70.yaml (operatorConfiguration).
const (
	EnvLLMApprovedHostsOpenAI    = "PACEDAY_LLM_APPROVED_HOSTS_OPENAI"
	EnvLLMApprovedHostsAnthropic = "PACEDAY_LLM_APPROVED_HOSTS_ANTHROPIC"
	EnvLLMApprovedOriginsOllama  = "PACEDAY_LLM_APPROVED_ORIGINS_OLLAMA"

	// OpenAIPublicHost and AnthropicPublicHost are approved for their own
	// vendor, over https on the default port, on every deployment (D-B2). No
	// variable value removes them (AC-B15, OQ-B4).
	OpenAIPublicHost    = "api.openai.com"
	AnthropicPublicHost = "api.anthropic.com"
)

// Origin is a scheme, host and port. Scheme and Host are lower-cased; Host is
// as url.URL.Hostname returns it (an IPv6 literal without brackets); Port is
// as written, "" when absent.
type Origin struct {
	Scheme string
	Host   string
	Port   string
}

// Canonical returns o with the scheme's default port made explicit (80 for
// http, 443 for https), so http://h and http://h:80 compare equal.
func (o Origin) Canonical() Origin {
	if o.Port == "" {
		switch o.Scheme {
		case "http":
			o.Port = "80"
		case "https":
			o.Port = "443"
		}
	}
	return o
}

// URL renders o as <scheme>://<host>[:<port>], bracketing an IPv6 literal.
func (o Origin) URL() string {
	host := o.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if o.Port != "" {
		host += ":" + o.Port
	}
	return o.Scheme + "://" + host
}

// ParseOrigin accepts exactly <scheme>://<host>[:<port>] followed by at most
// one "/" (spec D-B8). Any other path, any "?" or "#" (even with nothing
// after it), userinfo, a backslash, whitespace or a control character
// anywhere, an empty host, an empty port or one outside 1-65535 (or with a
// leading zero) is refused. Nothing is trimmed or normalised beyond
// lower-casing the scheme and host. It says nothing about approval.
func ParseOrigin(raw string) (Origin, bool) {
	if raw == "" || strings.ContainsAny(raw, "?#\\@") {
		return Origin{}, false
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] <= ' ' || raw[i] == 0x7f {
			return Origin{}, false
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Opaque != "" || u.User != nil || u.Host == "" ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
		return Origin{}, false
	}
	if u.Path != "" && u.Path != "/" {
		return Origin{}, false
	}
	// The value must be exactly scheme://host[:port] and an optional "/":
	// this refuses anything url.Parse tolerated and re-encoded.
	rest := raw[len(u.Scheme):]
	if !strings.EqualFold(raw[:len(u.Scheme)], u.Scheme) ||
		(rest != "://"+u.Host && rest != "://"+u.Host+"/") {
		return Origin{}, false
	}
	host := u.Hostname()
	if host == "" || strings.HasSuffix(u.Host, ":") {
		return Origin{}, false
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || port[0] == '0' {
			return Origin{}, false
		}
	}
	return Origin{Scheme: strings.ToLower(u.Scheme), Host: strings.ToLower(host), Port: port}, true
}

// LLMUserDestinations is the operator's extra approved hosts for openai and
// anthropic (lower-cased) and approved Ollama origins (canonical). Its zero
// value approves only the two public hosts, each for its own vendor, and
// leaves ollama not enabled (fail closed, D-B1).
type LLMUserDestinations struct {
	OpenAI    map[string]bool
	Anthropic map[string]bool
	Ollama    map[Origin]bool
}

// HostApproved reports whether host is approved for provider (openai or
// anthropic): equal, ASCII case-insensitive, to the built-in public host or an
// entry for that provider. No suffix, subdomain or wildcard matching, and a
// trailing dot is not stripped. Scheme and port are the caller's to check.
func (d LLMUserDestinations) HostApproved(provider, host string) bool {
	h := strings.ToLower(host)
	if h == "" {
		return false
	}
	switch provider {
	case "openai":
		return h == OpenAIPublicHost || d.OpenAI[h]
	case "anthropic":
		return h == AnthropicPublicHost || d.Anthropic[h]
	}
	return false
}

// OllamaEnabled reports whether the operator approved at least one origin.
func (d LLMUserDestinations) OllamaEnabled() bool { return len(d.Ollama) > 0 }

// OllamaApproved reports whether o (after Canonical) is an approved origin:
// scheme, host (textual, case-insensitive) and port all equal.
func (d LLMUserDestinations) OllamaApproved(o Origin) bool {
	o.Scheme, o.Host = strings.ToLower(o.Scheme), strings.ToLower(o.Host)
	return d.Ollama[o.Canonical()]
}

// DecideLLMUserDestinations reads the three variables through getenv. It
// never fails: a malformed entry is reported in notices (to be logged at
// startup) and approves nothing, while well-formed entries beside it stay
// approved (AC-B15).
func DecideLLMUserDestinations(getenv func(string) string) (LLMUserDestinations, []string) {
	var notices []string
	hostSet := func(variable string) map[string]bool {
		hosts, malformed := ParseApprovedHosts(variable, getenv(variable))
		notices = append(notices, malformed...)
		set := map[string]bool{}
		for _, h := range hosts {
			set[h] = true
		}
		return set
	}
	d := LLMUserDestinations{
		OpenAI:    hostSet(EnvLLMApprovedHostsOpenAI),
		Anthropic: hostSet(EnvLLMApprovedHostsAnthropic),
		Ollama:    map[Origin]bool{},
	}
	origins, malformed := ParseApprovedOrigins(EnvLLMApprovedOriginsOllama, getenv(EnvLLMApprovedOriginsOllama))
	notices = append(notices, malformed...)
	for _, o := range origins {
		d.Ollama[o] = true
	}
	return d, notices
}

// ParseApprovedOrigins splits one variable's value into canonical approved
// origins and notices naming each malformed entry. An entry is
// <scheme>://<host>[:<port>] with an optional single trailing "/", scheme
// http or https, host a DNS name (a single label is allowed), a dotted-decimal
// IPv4 literal or a bracketed IPv6 literal.
func ParseApprovedOrigins(variable, raw string) (origins []Origin, malformed []string) {
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		o, ok := approvedOriginEntry(entry)
		if !ok {
			malformed = append(malformed, fmt.Sprintf(
				"%s: entry %q ignored: it must be http:// or https:// followed by a host name or IP address and an optional port (no path, query, userinfo, wildcard or trailing dot), so it approves nothing",
				variable, entry))
			continue
		}
		origins = append(origins, o)
	}
	return origins, malformed
}

func approvedOriginEntry(entry string) (Origin, bool) {
	o, ok := ParseOrigin(entry)
	if !ok || (o.Scheme != "http" && o.Scheme != "https") {
		return Origin{}, false
	}
	bracketed := strings.Contains(entry, "[")
	if addr, err := netip.ParseAddr(o.Host); err == nil {
		switch {
		case bracketed && addr.Is6() && addr.Zone() == "":
		case !bracketed && addr.Is4() && addr.String() == o.Host:
		default:
			return Origin{}, false
		}
		return o.Canonical(), true
	}
	if bracketed || !ValidDNSHostName(o.Host) || allDigits(o.Host[strings.LastIndex(o.Host, ".")+1:]) {
		return Origin{}, false
	}
	return o.Canonical(), true
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}
