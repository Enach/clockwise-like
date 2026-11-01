package storage

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// NEW-llm-bring-your-own, migration 029 (spec §7, D-5, D-10; AC-16, AC-17):
// keys are bound to the provider stored at upgrade when that is openai or
// anthropic and kept unbound otherwise; no stored provider value is
// rewritten; the retired columns are untouched; new rows follow their
// organisation; the down migration loses nothing that existed before the
// upgrade.
func TestMigration029_BindsKeysAndRollsBack(t *testing.T) {
	db, to := migrationTestDB(t)
	to(28)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	type row struct{ provider, key, ollama, profile string }
	seeds := map[string]row{
		"openai":    {"openai", "sk-openai", "http://localhost:11434", ""},
		"anthropic": {"anthropic", "sk-ant", "http://localhost:11434", ""},
		"ollama":    {"ollama", "sk-ollama-era", "http://ollama.internal:11434", ""},
		"bedrock":   {"bedrock", "sk-bedrock-era", "http://localhost:11434", "prod"},
		"off":       {"", "", "http://localhost:11434", ""},
	}
	users := map[string]uuid.UUID{}
	i := 0
	for name, r := range seeds {
		u := seedMigrationUser(t, db, "m29-"+name+"@example.com", base.Add(time.Duration(i)*time.Hour))
		i++
		users[name] = u
		if _, err := db.Exec(`INSERT INTO settings (user_id, llm_provider, llm_api_key, ollama_base_url, aws_profile) VALUES ($1, $2, $3, $4, $5)`,
			u, r.provider, r.key, r.ollama, r.profile); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	to(29)
	type after struct{ provider, unbound, openai, anthropic, ollama, profile string }
	read := func(name string) after {
		t.Helper()
		var a after
		if err := db.QueryRow(`SELECT llm_provider, llm_api_key, llm_api_key_openai, llm_api_key_anthropic, ollama_base_url, aws_profile
			FROM settings WHERE user_id = $1`, users[name]).Scan(&a.provider, &a.unbound, &a.openai, &a.anthropic, &a.ollama, &a.profile); err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return a
	}
	for name, want := range map[string]after{
		"openai":    {"openai", "", "sk-openai", "", "http://localhost:11434", ""},
		"anthropic": {"anthropic", "", "", "sk-ant", "http://localhost:11434", ""},
		"ollama":    {"ollama", "sk-ollama-era", "", "", "http://ollama.internal:11434", ""},
		"bedrock":   {"bedrock", "sk-bedrock-era", "", "", "http://localhost:11434", "prod"},
		"off":       {"", "", "", "", "http://localhost:11434", ""}, // AC-17: still off
	} {
		if got := read(name); got != want {
			t.Errorf("after 029, %s = %+v, want %+v", name, got, want)
		}
	}
	fresh := seedMigrationUser(t, db, "m29-fresh@example.com", base.Add(24*time.Hour))
	s, err := GetOrCreateSettingsByUser(db, fresh)
	if err != nil {
		t.Fatal(err)
	}
	if s.LLMProvider != LLMChoiceOrganization {
		t.Errorf("a new account's provider = %q, want organization (O-5)", s.LLMProvider)
	}
	ol, err := GetOrCreateSettingsByUser(db, users["ollama"])
	if err != nil {
		t.Fatal(err)
	}
	if ol.LLMProvider != LLMChoiceUnsupported || ol.StoredLLMProvider != "ollama" || !ol.LLMUnboundAPIKeySet || ol.LLMAPIKeySet {
		t.Errorf("the ollama row reads %q (stored %q), unbound %v, bound %v", ol.LLMProvider, ol.StoredLLMProvider, ol.LLMUnboundAPIKeySet, ol.LLMAPIKeySet)
	}
	// A save of the document read writes the stored value back unchanged.
	if err := SaveSettingsForUser(db, users["ollama"], ol); err != nil {
		t.Fatal(err)
	}
	if got := read("ollama"); got.provider != "ollama" || got.unbound != "sk-ollama-era" {
		t.Errorf("saving the read document rewrote the row: %+v", got)
	}

	// Choices made after the upgrade, then roll back.
	if _, err := db.Exec(`UPDATE settings SET llm_api_key_bedrock = 'sk-bedrock-new' WHERE user_id = $1`, users["bedrock"]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE settings SET llm_provider = 'azure_openai' WHERE user_id = $1`, users["openai"]); err != nil {
		t.Fatal(err)
	}
	to(28)
	type old struct{ provider, key string }
	readOld := func(id uuid.UUID) old {
		t.Helper()
		var o old
		if err := db.QueryRow(`SELECT llm_provider, llm_api_key FROM settings WHERE user_id = $1`, id).Scan(&o.provider, &o.key); err != nil {
			t.Fatal(err)
		}
		return o
	}
	for name, want := range map[string]old{
		"openai":    {"azure_openai", "sk-openai"}, // its pre-upgrade key is not lost
		"anthropic": {"anthropic", "sk-ant"},
		"ollama":    {"ollama", "sk-ollama-era"},
		"bedrock":   {"bedrock", "sk-bedrock-era"}, // the post-upgrade Bedrock key is lost (spec §7)
		"off":       {"", ""},
	} {
		if got := readOld(users[name]); got != want {
			t.Errorf("after rollback, %s = %+v, want %+v", name, got, want)
		}
	}
	if got := readOld(fresh); got.provider != "ollama" {
		t.Errorf("after rollback, an 'organization' row = %+v, want the old default ollama", got)
	}
	var cols int
	if err := db.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_name = 'settings' AND column_name LIKE 'llm_api_key_%'`).Scan(&cols); err != nil {
		t.Fatal(err)
	}
	if cols != 0 {
		t.Errorf("%d per-provider key columns left after rollback", cols)
	}
	var def string
	if err := db.QueryRow(`SELECT column_default FROM information_schema.columns WHERE table_name = 'settings' AND column_name = 'llm_provider'`).Scan(&def); err != nil {
		t.Fatal(err)
	}
	if def != "'ollama'::text" {
		t.Errorf("default after rollback = %s, want 'ollama'", def)
	}

	// And up again: the migration is repeatable.
	to(29)
	if got := read("anthropic"); got.anthropic != "sk-ant" || got.unbound != "" {
		t.Errorf("after re-upgrade, anthropic = %+v", got)
	}
}
