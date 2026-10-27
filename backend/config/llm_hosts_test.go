package config

import (
	"reflect"
	"strings"
	"testing"
)

// PAC-66 AC-12, D-6, D-10: the operator's approved inference hosts.

func TestParseApprovedHosts_EntryForms(t *testing.T) {
	hosts, malformed := ParseApprovedHosts(EnvLLMApprovedHostsAzureOpenAI,
		" my-resource.openai.azure.com , https://Other.OpenAI.Azure.com/ ,, https://third.openai.azure.com ")
	want := []string{"my-resource.openai.azure.com", "other.openai.azure.com", "third.openai.azure.com"}
	if !reflect.DeepEqual(hosts, want) {
		t.Errorf("hosts = %v, want %v", hosts, want)
	}
	if len(malformed) != 0 {
		t.Errorf("malformed = %v, want none", malformed)
	}
}

func TestParseApprovedHosts_MalformedEntriesApproveNothing(t *testing.T) {
	for _, entry := range []string{
		"*",
		"*.openai.azure.com",
		"http://my-resource.openai.azure.com",
		"https://my-resource.openai.azure.com:443",
		"my-resource.openai.azure.com:8443",
		"https://my-resource.openai.azure.com/openai",
		"https://my-resource.openai.azure.com//",
		"https://user@my-resource.openai.azure.com",
		"user@my-resource.openai.azure.com",
		"10.0.0.1",
		"[::1]",
		"::1",
		"my-resource.openai.azure.com.",
		"-bad.example",
		"bad-.example",
		"bad..example",
		"under_score.example",
		"https://",
		"ftp://my-resource.openai.azure.com",
		strings.Repeat("a", 64) + ".example",
	} {
		hosts, malformed := ParseApprovedHosts(EnvLLMApprovedHostsVertex, "good.example,"+entry)
		if !reflect.DeepEqual(hosts, []string{"good.example"}) {
			t.Errorf("%q: hosts = %v, want only the well-formed sibling", entry, hosts)
		}
		if len(malformed) != 1 || !strings.Contains(malformed[0], EnvLLMApprovedHostsVertex) || !strings.Contains(malformed[0], entry) {
			t.Errorf("%q: malformed notice = %v, want one naming the variable and the entry", entry, malformed)
		}
	}
}

func TestParseApprovedHosts_UnsetOrEmptyApprovesNothing(t *testing.T) {
	for _, raw := range []string{"", "   ", ",,", " , "} {
		hosts, malformed := ParseApprovedHosts(EnvLLMApprovedHostsBedrock, raw)
		if len(hosts) != 0 || len(malformed) != 0 {
			t.Errorf("%q: hosts %v malformed %v, want nothing", raw, hosts, malformed)
		}
	}
}

func TestDecideLLMApprovedHosts_PerProvider(t *testing.T) {
	env := map[string]string{
		EnvLLMApprovedHostsAzureOpenAI: "az.openai.azure.com,*",
		EnvLLMApprovedHostsVertex:      "europe-west4-aiplatform.googleapis.com",
	}
	got, notices := DecideLLMApprovedHosts(func(k string) string { return env[k] })
	if !got.Approved("azure_openai", "az.openai.azure.com") || !got.Approved("azure_openai", "AZ.openai.azure.com") {
		t.Error("the Azure entry is not approved for azure_openai")
	}
	if got.Approved("vertex", "az.openai.azure.com") || got.Approved("bedrock", "az.openai.azure.com") {
		t.Error("a host approved for azure_openai is approved for another provider (D-8)")
	}
	if !got.Enabled("azure_openai") || !got.Enabled("vertex") || got.Enabled("bedrock") {
		t.Errorf("enabled = azure %v vertex %v bedrock %v, want true true false",
			got.Enabled("azure_openai"), got.Enabled("vertex"), got.Enabled("bedrock"))
	}
	if got.Approved("azure_openai", "*") || got.Approved("azure_openai", "anything.example") {
		t.Error("a malformed entry approved something")
	}
	if len(notices) != 1 || !strings.Contains(notices[0], EnvLLMApprovedHostsAzureOpenAI) {
		t.Errorf("notices = %v, want one for the malformed Azure entry", notices)
	}
	if got.Enabled("openai") || got.Approved("openai", "az.openai.azure.com") {
		t.Error("a user-key provider has an approved set")
	}
}

func TestLLMApprovedHosts_ZeroValueApprovesNothing(t *testing.T) {
	var none LLMApprovedHosts
	for _, p := range []string{"azure_openai", "vertex", "bedrock"} {
		if none.Enabled(p) || none.Approved(p, "x.example") || none.Approved(p, "") {
			t.Errorf("%s: the zero value approves something", p)
		}
	}
}
