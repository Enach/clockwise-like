package config

import (
	"reflect"
	"strings"
	"testing"
)

// PAC-66-B AC-B15, D-B1 to D-B3, as changed by NEW-llm-bring-your-own: the
// operator's approved destinations for the user-key providers (openai,
// anthropic), optionally with a gateway path prefix (owner decision O-10,
// spec AC-18). Ollama is removed (O-4): its variable approves nothing and is
// reported once at startup (AC-19). The Ollama-origin tests
// (TestParseApprovedOrigins_*) are replaced by
// TestDecideLLMUserDestinations_OllamaVariableIsRetired.

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func gw(host, prefix string) Gateway { return Gateway{Host: host, Prefix: prefix} }

func TestDecideLLMUserDestinations_PublicHostsAreBuiltIn(t *testing.T) { // D-B2, AC-B5
	d, notices := DecideLLMUserDestinations(env(nil))
	if len(notices) != 0 {
		t.Errorf("notices = %v, want none", notices)
	}
	if !d.Approved("openai", gw("api.openai.com", "")) || !d.Approved("anthropic", gw("API.Anthropic.com", "")) {
		t.Error("the vendors' public hosts must be approved with no configuration")
	}
	if d.Approved("openai", gw("api.anthropic.com", "")) || d.Approved("anthropic", gw("api.openai.com", "")) {
		t.Error("a public host is approved only for its own vendor (D-B2)")
	}
	if d.Approved("openai", gw("api.openai.com", "/v1")) {
		t.Error("a public host is approved with no prefix only")
	}
	if d.Approved("ollama", gw("api.openai.com", "")) {
		t.Error("ollama is not a provider any more")
	}
	for _, h := range []string{"api.openai.com.", "evil.api.openai.com", "openai.com", ""} {
		if d.Approved("openai", gw(h, "")) {
			t.Errorf("%q approved for openai; want exact host only (OQ-B6)", h)
		}
	}
}

func TestDecideLLMUserDestinations_PerProviderNeverTransfers(t *testing.T) { // AC-B11, D-B5
	d, notices := DecideLLMUserDestinations(env(map[string]string{
		EnvLLMApprovedHostsOpenAI:    "gateway.example, https://gw.example/team/openai/",
		EnvLLMApprovedHostsAnthropic: "https://Proxy.Example/",
	}))
	if len(notices) != 0 {
		t.Fatalf("notices = %v, want none", notices)
	}
	if !d.Approved("openai", gw("gateway.example", "")) || d.Approved("anthropic", gw("gateway.example", "")) {
		t.Error("an openai entry approves only openai")
	}
	if !d.Approved("anthropic", gw("proxy.example", "")) || d.Approved("openai", gw("proxy.example", "")) {
		t.Error("an anthropic entry approves only anthropic")
	}
	if !d.Approved("openai", gw("GW.example", "/team/openai")) {
		t.Error("the gateway prefix entry is not approved")
	}
	for _, p := range []string{"", "/team", "/team/openai/x", "/Team/openai"} {
		if d.Approved("openai", gw("gw.example", p)) {
			t.Errorf("prefix %q approved; want exactly /team/openai (AC-18)", p)
		}
	}
}

func TestParseGatewayEntries_EntryForms(t *testing.T) { // AC-18, O-10
	entries, malformed := ParseGatewayEntries(EnvLLMApprovedHostsOpenAI,
		" gateway.example , HTTPS://GW.Example/ ,, https://gw.example/openai , https://gw.example/a/b-c_d.e~f/ ")
	want := []Gateway{
		{"gateway.example", ""},
		{"gw.example", ""},
		{"gw.example", "/openai"},
		{"gw.example", "/a/b-c_d.e~f"},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Errorf("entries = %v, want %v", entries, want)
	}
	if len(malformed) != 0 {
		t.Errorf("malformed = %v, want none", malformed)
	}
}

func TestParseGatewayEntries_MalformedEntriesApproveNothing(t *testing.T) { // AC-B15, AC-18
	for _, entry := range []string{
		"http://gw.example",
		"https://gw.example:443",
		"https://gw.example:8443/openai",
		"https://gw.example/openai?x=1",
		"https://gw.example/openai#",
		"https://user@gw.example",
		"https://*.example",
		"*",
		"https://gw.example./openai",
		"https://10.0.0.5",
		"https://gw.example//openai",
		"https://gw.example/openai//",
		"https://gw.example/./openai",
		"https://gw.example/../openai",
		"https://gw.example/%6Fpenai",
		"https://gw.example/open ai",
		"gw.example/openai",
		"https://",
	} {
		entries, malformed := ParseGatewayEntries(EnvLLMApprovedHostsOpenAI, "good.example,"+entry)
		if !reflect.DeepEqual(entries, []Gateway{{"good.example", ""}}) {
			t.Errorf("%q: entries = %v, want only the well-formed sibling", entry, entries)
		}
		if len(malformed) != 1 || !strings.Contains(malformed[0], EnvLLMApprovedHostsOpenAI) || !strings.Contains(malformed[0], entry) {
			t.Errorf("%q: malformed notice = %v, want one naming the variable and the entry", entry, malformed)
		}
	}
}

func TestDecideLLMUserDestinations_MalformedNeverApprovesEverythingNorRemovesPublicHosts(t *testing.T) { // AC-B15, OQ-B4
	d, notices := DecideLLMUserDestinations(env(map[string]string{
		EnvLLMApprovedHostsOpenAI:    "*, http://evil.example, good-gateway.example",
		EnvLLMApprovedHostsAnthropic: "-api.anthropic.com",
	}))
	if len(notices) != 3 {
		t.Errorf("notices = %v, want three (one per malformed entry)", notices)
	}
	if !d.Approved("openai", gw("good-gateway.example", "")) {
		t.Error("a well-formed sibling must stay approved")
	}
	for _, h := range []string{"evil.example", "attacker.example", "*"} {
		if d.Approved("openai", gw(h, "")) {
			t.Errorf("%q approved after a malformed entry", h)
		}
	}
	if !d.Approved("openai", gw("api.openai.com", "")) || !d.Approved("anthropic", gw("api.anthropic.com", "")) {
		t.Error("a malformed entry must never remove a public host")
	}
}

// NEW-llm-bring-your-own AC-19: a deployment that still sets the Ollama
// variable starts, is told once that it is no longer used, and approves
// nothing from it, whatever it lists.
func TestDecideLLMUserDestinations_OllamaVariableIsRetired(t *testing.T) { // AC-19
	d, notices := DecideLLMUserDestinations(env(map[string]string{
		EnvLLMApprovedOriginsOllama: "http://ollama:11434, https://api.openai.com, https://gw.example",
	}))
	if len(notices) != 1 || notices[0] != OllamaRetiredNotice || !strings.Contains(notices[0], "no longer used") {
		t.Errorf("notices = %v, want exactly the retirement notice", notices)
	}
	for _, p := range []string{"openai", "anthropic"} {
		for _, h := range []string{"ollama", "gw.example"} {
			if d.Approved(p, gw(h, "")) {
				t.Errorf("%s approved for %s by the retired variable", h, p)
			}
		}
	}
	if _, notices := DecideLLMUserDestinations(env(map[string]string{EnvLLMApprovedOriginsOllama: "  "})); len(notices) != 0 {
		t.Errorf("a blank value: notices = %v, want none", notices)
	}
}

func TestParseBaseURL_Shape(t *testing.T) { // D-B8, AC-B18, AC-B19, AC-18
	for raw, want := range map[string]BaseURL{
		"https://api.openai.com":        {"https", "api.openai.com", "", ""},
		"https://api.openai.com/":       {"https", "api.openai.com", "", ""},
		"HTTPS://API.OpenAI.com:443":    {"https", "api.openai.com", "443", ""},
		"https://gw.example/openai":     {"https", "gw.example", "", "/openai"},
		"https://gw.example/a/b/":       {"https", "gw.example", "", "/a/b"},
		"https://gw.example/V1":         {"https", "gw.example", "", "/V1"},
		"http://ollama:11434":           {"http", "ollama", "11434", ""},
		"http://[fd00::1]:11434/":       {"http", "fd00::1", "11434", ""},
		"http://169.254.169.254":        {"http", "169.254.169.254", "", ""},
		"https://api.openai.com.":       {"https", "api.openai.com.", "", ""},
		"https://xn--bcher-kva.example": {"https", "xn--bcher-kva.example", "", ""},
	} {
		got, ok := ParseBaseURL(raw)
		if !ok || got != want {
			t.Errorf("ParseBaseURL(%q) = %v %v, want %v true", raw, got, ok, want)
		}
	}
	for _, raw := range []string{
		"",
		" https://api.openai.com",
		"https://api.openai.com ",
		"https://api.openai.com\n",
		"https://api.openai.com//",
		"https://api.openai.com/v1//",
		"https://api.openai.com?",
		"https://api.openai.com/?x=1",
		"https://api.openai.com#",
		"https://api.openai.com/#frag",
		"https://user@api.openai.com",
		"https://user:pw@api.openai.com",
		"http://ollama:11434/api/pull?x=",
		"http://ollama:11434/x/../api/chat",
		"http://ollama:11434/./x",
		"http://ollama:11434/%2e%2e",
		"https://gw.example/a%2Fb",
		"https://gw.example/a b",
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
		if got, ok := ParseBaseURL(raw); ok {
			t.Errorf("ParseBaseURL(%q) = %v, want refused", raw, got)
		}
	}
}
