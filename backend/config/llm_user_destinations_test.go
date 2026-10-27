package config

import (
	"reflect"
	"strings"
	"testing"
)

// PAC-66-B AC-B15, D-B1 to D-B3, OQ-B6: the operator's approved destinations
// for the user-key providers (openai, anthropic) and the keyless one (ollama).

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDecideLLMUserDestinations_PublicHostsAreBuiltIn(t *testing.T) { // D-B2, AC-B5
	d, notices := DecideLLMUserDestinations(env(nil))
	if len(notices) != 0 {
		t.Errorf("notices = %v, want none", notices)
	}
	if !d.HostApproved("openai", "api.openai.com") || !d.HostApproved("anthropic", "API.Anthropic.com") {
		t.Error("the vendors' public hosts must be approved with no configuration")
	}
	if d.HostApproved("openai", "api.anthropic.com") || d.HostApproved("anthropic", "api.openai.com") {
		t.Error("a public host is approved only for its own vendor (D-B2)")
	}
	if d.HostApproved("ollama", "api.openai.com") || d.OllamaEnabled() {
		t.Error("nothing configured must leave ollama not enabled (D-B1)")
	}
	for _, h := range []string{"api.openai.com.", "evil.api.openai.com", "openai.com", ""} {
		if d.HostApproved("openai", h) {
			t.Errorf("%q approved for openai; want exact host only (OQ-B6)", h)
		}
	}
}

func TestDecideLLMUserDestinations_PerProviderNeverTransfers(t *testing.T) { // AC-B11, D-B5
	d, notices := DecideLLMUserDestinations(env(map[string]string{
		EnvLLMApprovedHostsOpenAI:    "gateway.example",
		EnvLLMApprovedHostsAnthropic: "https://Proxy.Example/",
		EnvLLMApprovedOriginsOllama:  "http://ollama:11434, https://gateway.example",
	}))
	if len(notices) != 0 {
		t.Fatalf("notices = %v, want none", notices)
	}
	if !d.HostApproved("openai", "gateway.example") || d.HostApproved("anthropic", "gateway.example") {
		t.Error("an openai entry approves only openai")
	}
	if !d.HostApproved("anthropic", "proxy.example") || d.HostApproved("openai", "proxy.example") {
		t.Error("an anthropic entry approves only anthropic")
	}
	if !d.OllamaApproved(Origin{Scheme: "http", Host: "ollama", Port: "11434"}) {
		t.Error("the ollama origin is not approved")
	}
	if d.HostApproved("openai", "ollama") {
		t.Error("an ollama origin approves nothing for openai")
	}
	// The same host listed for ollama does not change what openai may reach,
	// and an openai host does not approve it for ollama on another scheme.
	if d.OllamaApproved(Origin{Scheme: "http", Host: "gateway.example", Port: "80"}) {
		t.Error("ollama origin matching must include the scheme")
	}
}

func TestParseApprovedOrigins_EntryForms(t *testing.T) { // OQ-B6
	origins, malformed := ParseApprovedOrigins(EnvLLMApprovedOriginsOllama,
		" http://ollama:11434 , HTTP://LocalHost:11434/ ,, https://ollama.internal.example , http://10.0.0.5:11434 , http://[fd00::1]:11434 , http://plain.example ")
	want := []Origin{
		{"http", "ollama", "11434"},
		{"http", "localhost", "11434"},
		{"https", "ollama.internal.example", "443"},
		{"http", "10.0.0.5", "11434"},
		{"http", "fd00::1", "11434"},
		{"http", "plain.example", "80"},
	}
	if !reflect.DeepEqual(origins, want) {
		t.Errorf("origins = %v, want %v", origins, want)
	}
	if len(malformed) != 0 {
		t.Errorf("malformed = %v, want none", malformed)
	}
}

func TestParseApprovedOrigins_MalformedEntriesApproveNothing(t *testing.T) { // AC-B15
	for _, entry := range []string{
		"ollama:11434",
		"ollama",
		"ftp://ollama:11434",
		"http://ollama:11434/api",
		"http://ollama:11434//",
		"http://ollama:11434?x=1",
		"http://ollama:11434#",
		"http://user@ollama:11434",
		"http://*:11434",
		"http://*.example",
		"http://ollama.:11434",
		"http://:11434",
		"http://",
		"http://ollama:0",
		"http://ollama:65536",
		"http://ollama:",
		"http://ollama:011434",
		"http://010.0.0.5:11434",
		"http://fd00::1:11434",
		"http://under_score:11434",
	} {
		origins, malformed := ParseApprovedOrigins(EnvLLMApprovedOriginsOllama, "http://good:11434,"+entry)
		if !reflect.DeepEqual(origins, []Origin{{"http", "good", "11434"}}) {
			t.Errorf("%q: origins = %v, want only the well-formed sibling", entry, origins)
		}
		if len(malformed) != 1 || !strings.Contains(malformed[0], EnvLLMApprovedOriginsOllama) || !strings.Contains(malformed[0], entry) {
			t.Errorf("%q: malformed notice = %v, want one naming the variable and the entry", entry, malformed)
		}
	}
}

func TestDecideLLMUserDestinations_MalformedNeverApprovesEverythingNorRemovesPublicHosts(t *testing.T) { // AC-B15, OQ-B4
	d, notices := DecideLLMUserDestinations(env(map[string]string{
		EnvLLMApprovedHostsOpenAI:    "*, http://evil.example, good-gateway.example",
		EnvLLMApprovedHostsAnthropic: "-api.anthropic.com",
		EnvLLMApprovedOriginsOllama:  "*",
	}))
	if len(notices) != 4 {
		t.Errorf("notices = %v, want four (one per malformed entry)", notices)
	}
	if !d.HostApproved("openai", "good-gateway.example") {
		t.Error("a well-formed sibling must stay approved")
	}
	for _, h := range []string{"evil.example", "attacker.example", "*"} {
		if d.HostApproved("openai", h) {
			t.Errorf("%q approved after a malformed entry", h)
		}
	}
	if !d.HostApproved("openai", "api.openai.com") || !d.HostApproved("anthropic", "api.anthropic.com") {
		t.Error("a malformed entry must never remove a public host")
	}
	if d.OllamaEnabled() {
		t.Error("a malformed ollama entry must approve nothing")
	}
}

func TestParseOrigin_Shape(t *testing.T) { // D-B8, AC-B18, AC-B19
	for raw, want := range map[string]Origin{
		"https://api.openai.com":        {"https", "api.openai.com", ""},
		"https://api.openai.com/":       {"https", "api.openai.com", ""},
		"HTTPS://API.OpenAI.com:443":    {"https", "api.openai.com", "443"},
		"http://ollama:11434":           {"http", "ollama", "11434"},
		"http://[fd00::1]:11434/":       {"http", "fd00::1", "11434"},
		"http://169.254.169.254":        {"http", "169.254.169.254", ""},
		"http://localhost:11434":        {"http", "localhost", "11434"},
		"gopher://ollama:70":            {"gopher", "ollama", "70"},
		"https://api.openai.com.":       {"https", "api.openai.com.", ""},
		"https://xn--bcher-kva.example": {"https", "xn--bcher-kva.example", ""},
	} {
		got, ok := ParseOrigin(raw)
		if !ok || got != want {
			t.Errorf("ParseOrigin(%q) = %v %v, want %v true", raw, got, ok, want)
		}
	}
	for _, raw := range []string{
		"",
		" https://api.openai.com",
		"https://api.openai.com ",
		"https://api.openai.com\n",
		"https://api.openai.com/v1",
		"https://api.openai.com//",
		"https://api.openai.com?",
		"https://api.openai.com/?x=1",
		"https://api.openai.com#",
		"https://api.openai.com/#frag",
		"https://user@api.openai.com",
		"https://user:pw@api.openai.com",
		"http://ollama:11434/api/pull?x=",
		"http://ollama:11434/x/../api/chat",
		"http://ollama:11434/%2e%2e",
		"http:/",
		"http://",
		"http://:80",
		"http://ollama:",
		"http://ollama:0",
		"http://ollama:99999",
		"http://ollama:08",
		"api.openai.com",
		"//api.openai.com",
		"mailto:x@api.openai.com",
		"https:\\\\api.openai.com",
		"https://api.openai.com\\@evil.example",
	} {
		if got, ok := ParseOrigin(raw); ok {
			t.Errorf("ParseOrigin(%q) = %v, want refused", raw, got)
		}
	}
}

func TestOrigin_CanonicalAppliesDefaultPort(t *testing.T) { // OQ-B6
	for in, want := range map[Origin]Origin{
		{"http", "h", ""}:      {"http", "h", "80"},
		{"https", "h", ""}:     {"https", "h", "443"},
		{"http", "h", "11434"}: {"http", "h", "11434"},
	} {
		if got := in.Canonical(); got != want {
			t.Errorf("%v.Canonical() = %v, want %v", in, got, want)
		}
	}
	if got := (Origin{"http", "fd00::1", "11434"}).URL(); got != "http://[fd00::1]:11434" {
		t.Errorf("URL() = %q", got)
	}
	if got := (Origin{"https", "api.openai.com", ""}).URL(); got != "https://api.openai.com" {
		t.Errorf("URL() = %q", got)
	}
}
