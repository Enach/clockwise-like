package storage

import (
	"github.com/Enach/paceday/backend/internal/secretbox"
	"github.com/google/uuid"
)

// PAC-84: the nine secrets of a settings row (S2, S3, S6, six S8). A read
// opens each one on its own (contract 6.2): one that cannot be decrypted
// leaves its field "" and records the failure, and never fails the read. A
// save writes every secret it did not change byte-identical to what it read,
// so it never re-seals or blanks a value it could not open (K-10).

// The settings secret columns.
const (
	SettingsColMicrosoftTokens = "microsoft_tokens"
	SettingsColZoomTokens      = "zoom_tokens"
	SettingsColWebcalURL       = "webcal_url"
	SettingsColLLMUnboundKey   = "llm_api_key"
)

// LLMKeyColumn is the settings column of provider's key ("" when provider
// is not one of the five).
func LLMKeyColumn(provider string) string {
	switch provider {
	case "openai", "anthropic", "azure_openai", "bedrock", "vertex":
		return "llm_api_key_" + provider
	}
	return ""
}

type settingsSecret struct {
	raw      string // the stored text, exactly as read
	plain    string // the opened value ("" on failure)
	err      error  // the decryption failure, if any
	replaced bool   // a writer replaced the value explicitly
}

type settingsSecrets map[string]*settingsSecret

// fieldOf returns the field of s holding column's plaintext.
func (s *Settings) fieldOf(column string) *string {
	switch column {
	case SettingsColMicrosoftTokens:
		return &s.MicrosoftTokens
	case SettingsColZoomTokens:
		return &s.ZoomTokens
	case SettingsColWebcalURL:
		return &s.WebcalURL
	case SettingsColLLMUnboundKey:
		return &s.LLMUnboundAPIKey
	case "llm_api_key_openai":
		return &s.LLMAPIKeys.OpenAI
	case "llm_api_key_anthropic":
		return &s.LLMAPIKeys.Anthropic
	case "llm_api_key_azure_openai":
		return &s.LLMAPIKeys.AzureOpenAI
	case "llm_api_key_bedrock":
		return &s.LLMAPIKeys.Bedrock
	case "llm_api_key_vertex":
		return &s.LLMAPIKeys.Vertex
	}
	return nil
}

// openSettingsSecrets opens the stored text of each secret column read into
// raw (column -> stored text) for userID's row and sets the fields.
func (s *Settings) openSettingsSecrets(userID uuid.UUID, raw map[string]string) {
	s.secrets = make(settingsSecrets, len(raw))
	for col, stored := range raw {
		plain, err := openSecret(bindSettings(col, userID), stored, userID)
		s.secrets[col] = &settingsSecret{raw: stored, plain: plain, err: err}
		if f := s.fieldOf(col); f != nil {
			*f = plain
		}
	}
}

// SecretErr is the decryption failure of column's stored value, nil when it
// opened (or a writer has replaced it since). Act paths check it before
// treating an empty field as "not configured" (contract 1, K-2).
func (s *Settings) SecretErr(column string) error {
	if s == nil || s.secrets == nil {
		return nil
	}
	if r := s.secrets[column]; r != nil && !r.replaced {
		return r.err
	}
	return nil
}

// MarkSecretReplaced records that the caller explicitly set column's value
// (an empty value included): the next save writes the field as it is, even
// when it equals what was read.
func (s *Settings) MarkSecretReplaced(column string) {
	if s.secrets == nil {
		s.secrets = settingsSecrets{}
	}
	if r := s.secrets[column]; r != nil {
		r.replaced = true
		return
	}
	s.secrets[column] = &settingsSecret{replaced: true}
}

// secretPresent is presence as the stored value being non-empty (D-3,
// AC-16b): a value that cannot be decrypted is still set.
func (s *Settings) secretPresent(column, value string) bool {
	return value != "" || s.SecretErr(column) != nil
}

// storedSecretText is what a save writes to column: the text read, byte for
// byte, when the field still holds what was read and no writer replaced it;
// otherwise the field sealed for userID's row.
func (s *Settings) storedSecretText(column string, userID uuid.UUID) (string, error) {
	f := s.fieldOf(column)
	if r := s.secrets[column]; r != nil && !r.replaced && *f == r.plain {
		return r.raw, nil
	}
	return secretbox.Seal(bindSettings(column, userID), *f)
}
