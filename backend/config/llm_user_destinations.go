package config

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// PAC-66-B, as changed by NEW-llm-bring-your-own: a user-set base URL for
// openai or anthropic (which carry the user's own key) is sent to only if it
// names a destination approved for that provider. The vendors' public hosts
// are approved by the product, each for its own vendor; anything else is
// approved by the operator, per provider, optionally with a path prefix for
// an OpenAI-compatible gateway (owner decision O-10). Format and matching are
// fixed by contracts/features/NEW-llm-bring-your-own.yaml
// (operatorConfiguration).
const (
	EnvLLMApprovedHostsOpenAI    = "PACEDAY_LLM_APPROVED_HOSTS_OPENAI"
	EnvLLMApprovedHostsAnthropic = "PACEDAY_LLM_APPROVED_HOSTS_ANTHROPIC"
	// EnvLLMApprovedOriginsOllama is retired: Ollama was removed from the
	// product (owner decision O-4). A value is logged once at startup and
	// approves nothing (spec AC-19).
	EnvLLMApprovedOriginsOllama = "PACEDAY_LLM_APPROVED_ORIGINS_OLLAMA"

	// OpenAIPublicHost and AnthropicPublicHost are approved for their own
	// vendor, over https on the default port and with no path prefix, on
	// every deployment. No variable value removes them.
	OpenAIPublicHost    = "api.openai.com"
	AnthropicPublicHost = "api.anthropic.com"
)

// OllamaRetiredNotice is the startup notice for a deployment that still sets
// EnvLLMApprovedOriginsOllama.
const OllamaRetiredNotice = EnvLLMApprovedOriginsOllama + " is no longer used: Ollama was removed from Paceday, so this setting approves nothing; remove it from the environment"

// Gateway is an approved destination: a lower-cased DNS host and a path
// prefix, "" or "/seg[/seg...]" with no trailing "/".
type Gateway struct {
	Host   string
	Prefix string
}

// LLMUserDestinations is the operator's approved destinations for openai and
// anthropic. Its zero value approves only the two public hosts, each for its
// own vendor and without a prefix.
type LLMUserDestinations struct {
	OpenAI    map[Gateway]bool
	Anthropic map[Gateway]bool
}

// Approved reports whether g is a destination approved for provider (openai
// or anthropic): its host equal, ASCII case-insensitive, to the built-in
// public host with no prefix, or to an entry for that provider with exactly
// that prefix (case-sensitive). No suffix, subdomain or wildcard matching.
func (d LLMUserDestinations) Approved(provider string, g Gateway) bool {
	g.Host = strings.ToLower(g.Host)
	if g.Host == "" {
		return false
	}
	switch provider {
	case "openai":
		return (g.Host == OpenAIPublicHost && g.Prefix == "") || d.OpenAI[g]
	case "anthropic":
		return (g.Host == AnthropicPublicHost && g.Prefix == "") || d.Anthropic[g]
	}
	return false
}

// DecideLLMUserDestinations reads the openai and anthropic variables through
// getenv. It never fails: a malformed entry is reported in notices (to be
// logged at startup) and approves nothing, while well-formed entries beside it
// stay approved. A value for the retired Ollama variable adds one notice and
// approves nothing (spec AC-19).
func DecideLLMUserDestinations(getenv func(string) string) (LLMUserDestinations, []string) {
	var notices []string
	set := func(variable string) map[Gateway]bool {
		entries, malformed := ParseGatewayEntries(variable, getenv(variable))
		notices = append(notices, malformed...)
		out := map[Gateway]bool{}
		for _, g := range entries {
			out[g] = true
		}
		return out
	}
	d := LLMUserDestinations{
		OpenAI:    set(EnvLLMApprovedHostsOpenAI),
		Anthropic: set(EnvLLMApprovedHostsAnthropic),
	}
	if strings.TrimSpace(getenv(EnvLLMApprovedOriginsOllama)) != "" {
		notices = append(notices, OllamaRetiredNotice)
	}
	return d, notices
}

// ParseGatewayEntries splits one variable's value into approved gateways and
// notices naming each malformed entry. An entry is a bare DNS host name,
// https://<host>, or https://<host>/<prefix>, each with an optional single
// trailing "/". <prefix> is one or more "/"-separated segments of
// [A-Za-z0-9._~-], none of them "." or "..", with no percent-encoding.
// Anything else (another scheme, a port, query, fragment, userinfo, wildcard,
// IP literal, trailing dot on the host) is malformed.
func ParseGatewayEntries(variable, raw string) (entries []Gateway, malformed []string) {
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		g, ok := gatewayEntry(entry)
		if !ok {
			malformed = append(malformed, fmt.Sprintf(
				"%s: entry %q ignored: it must be a bare DNS host name, https://<host> or https://<host>/<path prefix> (no port, query, userinfo, wildcard, IP address or trailing dot), so it approves nothing",
				variable, entry))
			continue
		}
		entries = append(entries, g)
	}
	return entries, malformed
}

func gatewayEntry(entry string) (Gateway, bool) {
	rest := entry
	if i := strings.Index(entry, "://"); i >= 0 {
		if !strings.EqualFold(entry[:i], "https") {
			return Gateway{}, false
		}
		rest = entry[i+3:]
	} else if strings.Contains(entry, "/") {
		return Gateway{}, false
	}
	host, path := rest, ""
	if i := strings.Index(rest, "/"); i >= 0 {
		host, path = rest[:i], rest[i:]
	}
	if !ValidDNSHostName(host) {
		return Gateway{}, false
	}
	prefix, ok := gatewayPrefix(path)
	if !ok {
		return Gateway{}, false
	}
	return Gateway{Host: strings.ToLower(host), Prefix: prefix}, true
}

// gatewayPrefix validates a path ("" or starting with "/") and returns it
// without its single optional trailing "/".
func gatewayPrefix(path string) (string, bool) {
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		return "", true
	}
	for _, seg := range strings.Split(path[1:], "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", false
		}
		for i := 0; i < len(seg); i++ {
			c := seg[i]
			ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
				c == '.' || c == '_' || c == '~' || c == '-'
			if !ok {
				return "", false
			}
		}
	}
	return path, true
}

// BaseURL is a user-set base URL taken apart: scheme (lower-cased), host
// (lower-cased, as url.URL.Hostname returns it), port as written ("" when
// absent) and path prefix ("" or "/seg[/seg...]").
type BaseURL struct {
	Scheme string
	Host   string
	Port   string
	Prefix string
}

// ParseBaseURL accepts exactly <scheme>://<host>[:<port>][/<prefix>] with at
// most one trailing "/", where <prefix> follows the gateway-entry segment
// rule. Any "?" or "#" (even with nothing after it), userinfo, a backslash,
// whitespace or a control character anywhere, percent-encoding, an empty
// host, an empty port or one outside 1-65535 (or with a leading zero) is
// refused. It says nothing about approval.
func ParseBaseURL(raw string) (BaseURL, bool) {
	if raw == "" || strings.ContainsAny(raw, "?#\\@%") {
		return BaseURL{}, false
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] <= ' ' || raw[i] == 0x7f {
			return BaseURL{}, false
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Opaque != "" || u.User != nil || u.Host == "" ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
		return BaseURL{}, false
	}
	// The value must be exactly scheme://host[:port] followed by the path:
	// this refuses anything url.Parse tolerated and re-encoded.
	if !strings.EqualFold(raw[:len(u.Scheme)], u.Scheme) || raw[len(u.Scheme):] != "://"+u.Host+u.Path {
		return BaseURL{}, false
	}
	prefix, ok := gatewayPrefix(u.Path)
	if !ok {
		return BaseURL{}, false
	}
	host := u.Hostname()
	if host == "" || strings.HasSuffix(u.Host, ":") {
		return BaseURL{}, false
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || port[0] == '0' {
			return BaseURL{}, false
		}
	}
	return BaseURL{Scheme: strings.ToLower(u.Scheme), Host: strings.ToLower(host), Port: port, Prefix: prefix}, true
}
