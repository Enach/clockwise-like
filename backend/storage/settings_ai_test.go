package storage

import (
	"reflect"
	"testing"

	"github.com/google/uuid"
)

// NEW-llm-bring-your-own (spec D-5, D-10; contract §4): the read value of a
// stored provider, keys bound per provider, and the read-only flags.

func TestReadLLMProvider_UnsupportedForAnythingRemoved(t *testing.T) {
	for stored, want := range map[string]string{
		"": "", "organization": "organization", "openai": "openai", "anthropic": "anthropic",
		"azure_openai": "azure_openai", "bedrock": "bedrock", "vertex": "vertex",
		"ollama": "unsupported", "skynet": "unsupported", "unsupported": "unsupported",
	} {
		if got := ReadLLMProvider(stored); got != want {
			t.Errorf("ReadLLMProvider(%q) = %q, want %q", stored, got, want)
		}
	}
}

func TestStoredProvider_UnsupportedWritesBackTheStoredValue(t *testing.T) {
	s := &Settings{LLMProvider: "unsupported", StoredLLMProvider: "ollama"}
	if got := s.storedProvider(); got != "ollama" {
		t.Errorf("unsupported read: storedProvider = %q, want ollama (never rewritten, D-10)", got)
	}
	s.LLMProvider = "openai"
	if got := s.storedProvider(); got != "openai" {
		t.Errorf("a chosen provider: storedProvider = %q, want openai", got)
	}
}

func TestLLMAPIKeys_BoundPerProvider(t *testing.T) {
	var k LLMAPIKeys
	for _, p := range []string{"openai", "anthropic", "azure_openai", "bedrock", "vertex"} {
		if !k.Set(p, "key-"+p) || k.For(p) != "key-"+p {
			t.Errorf("Set/For(%s) did not round-trip", p)
		}
	}
	for _, p := range []string{"", "organization", "unsupported", "ollama"} {
		if k.Set(p, "x") || k.For(p) != "" {
			t.Errorf("%q is not a provider a key can be bound to", p)
		}
	}
	want := []string{"key-openai", "key-anthropic", "key-azure_openai", "key-bedrock", "key-vertex"}
	if got := k.All(); !reflect.DeepEqual(got, want) {
		t.Errorf("All = %v, want %v", got, want)
	}
	k.Set("anthropic", "")
	if len(k.All()) != 4 {
		t.Errorf("All includes an empty key: %v", k.All())
	}
}

func TestRefreshKeyFlags_SelectedProviderOnly(t *testing.T) {
	s := &Settings{LLMProvider: "anthropic", LLMUnboundAPIKey: "legacy"}
	s.LLMAPIKeys.Set("openai", "k")
	s.RefreshKeyFlags()
	if s.LLMAPIKeySet || !s.LLMAPIKeysSet.OpenAI || s.LLMAPIKeysSet.Anthropic || !s.LLMUnboundAPIKeySet {
		t.Errorf("flags = set %v, per provider %+v, unbound %v", s.LLMAPIKeySet, s.LLMAPIKeysSet, s.LLMUnboundAPIKeySet)
	}
	s.LLMProvider = "openai"
	s.RefreshKeyFlags()
	if !s.LLMAPIKeySet {
		t.Error("llmApiKeySet must report the key bound to the selected provider")
	}
}

func TestUserInOrganization(t *testing.T) {
	db := openTestDB(t)
	member := settingsTestUser(t, db, "settings-ai-member@example.com")
	loner := settingsTestUser(t, db, "settings-ai-loner@example.com")
	org, err := UpsertOrg(db, "settings-ai.example", "Settings AI")
	if err != nil {
		t.Fatal(err)
	}
	if err := SetUserOrg(db, member, org.ID); err != nil {
		t.Fatal(err)
	}
	for user, want := range map[uuid.UUID]bool{member: true, loner: false, uuid.New(): false} {
		got, err := UserInOrganization(db, user)
		if err != nil || got != want {
			t.Errorf("UserInOrganization(%s) = %v, %v; want %v", user, got, err, want)
		}
	}
	db.Close()
	if _, err := UserInOrganization(db, member); err == nil {
		t.Error("a database error must be reported, not read as 'no organisation'")
	}
}
