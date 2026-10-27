package config

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// PAC-66: the deployment's cloud identity (Azure OpenAI, Vertex, Bedrock) is
// sent only to inference hosts the operator approved, separately per provider
// (spec D-8). Unset or empty approves nothing: the provider is not enabled on
// this deployment (D-1, fail closed). Format and matching are fixed by
// contracts/features/PAC-66.md §4.
const (
	EnvLLMApprovedHostsAzureOpenAI = "PACEDAY_LLM_APPROVED_HOSTS_AZURE_OPENAI"
	EnvLLMApprovedHostsVertex      = "PACEDAY_LLM_APPROVED_HOSTS_VERTEX"
	EnvLLMApprovedHostsBedrock     = "PACEDAY_LLM_APPROVED_HOSTS_BEDROCK"
)

// llmApprovedHostsVars maps each ambient-credential provider to its variable.
var llmApprovedHostsVars = map[string]string{
	"azure_openai": EnvLLMApprovedHostsAzureOpenAI,
	"vertex":       EnvLLMApprovedHostsVertex,
	"bedrock":      EnvLLMApprovedHostsBedrock,
}

// LLMApprovedHosts is the operator's approved inference hosts, per provider,
// lower-cased. Its zero value approves nothing.
type LLMApprovedHosts map[string]map[string]bool

// Enabled reports whether the operator approved at least one host for provider.
func (a LLMApprovedHosts) Enabled(provider string) bool {
	return len(a[provider]) > 0
}

// Approved reports whether host is approved for provider: an exact,
// ASCII case-insensitive match. No suffix, subdomain or wildcard matching
// (D-2), and a trailing dot is not stripped.
func (a LLMApprovedHosts) Approved(provider, host string) bool {
	if host == "" {
		return false
	}
	return a[provider][strings.ToLower(host)]
}

// DecideLLMApprovedHosts reads the three variables through getenv. It never
// fails: a malformed entry is reported in notices (to be logged at startup)
// and approves nothing, while well-formed entries beside it stay approved
// (spec AC-12, D-6).
func DecideLLMApprovedHosts(getenv func(string) string) (LLMApprovedHosts, []string) {
	out := LLMApprovedHosts{}
	var notices []string
	providers := make([]string, 0, len(llmApprovedHostsVars))
	for p := range llmApprovedHostsVars {
		providers = append(providers, p)
	}
	sort.Strings(providers)
	for _, provider := range providers {
		variable := llmApprovedHostsVars[provider]
		hosts, malformed := ParseApprovedHosts(variable, getenv(variable))
		notices = append(notices, malformed...)
		if len(hosts) == 0 {
			continue
		}
		set := map[string]bool{}
		for _, h := range hosts {
			set[h] = true
		}
		out[provider] = set
	}
	return out, notices
}

// ParseApprovedHosts splits one variable's value into approved hosts and
// notices naming each malformed entry.
func ParseApprovedHosts(variable, raw string) (hosts []string, malformed []string) {
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		host, ok := approvedHostEntry(entry)
		if !ok {
			malformed = append(malformed, fmt.Sprintf(
				"%s: entry %q ignored: it must be a bare DNS host name or https://<host> (no port, path, userinfo, wildcard, IP address or trailing dot), so it approves nothing",
				variable, entry))
			continue
		}
		hosts = append(hosts, host)
	}
	return hosts, malformed
}

// approvedHostEntry reduces one entry to its host. The URL form accepts only
// https:// followed by the host and at most one trailing slash, because
// operators copy the Azure endpoint from the portal (round-3 challenge NB-5).
func approvedHostEntry(entry string) (string, bool) {
	host := entry
	if i := strings.Index(entry, "://"); i >= 0 {
		if !strings.EqualFold(entry[:i], "https") {
			return "", false
		}
		host = strings.TrimSuffix(entry[i+3:], "/")
	}
	if !ValidDNSHostName(host) {
		return "", false
	}
	return strings.ToLower(host), true
}

// ValidDNSHostName reports whether s is a DNS host name: dot-separated labels
// of letters, digits and hyphens, each 1-63 characters and not starting or
// ending with a hyphen, at most 253 characters, no trailing dot, and not an IP
// address literal.
func ValidDNSHostName(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	if _, err := netip.ParseAddr(s); err == nil {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if !ValidDNSLabel(label) {
			return false
		}
	}
	return true
}

// ValidDNSLabel reports whether s is one DNS label (see ValidDNSHostName).
func ValidDNSLabel(s string) bool {
	if len(s) == 0 || len(s) > 63 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		isAlnum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if !isAlnum && c != '-' {
			return false
		}
	}
	return true
}
