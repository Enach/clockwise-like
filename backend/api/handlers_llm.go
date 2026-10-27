package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/nlp"
	"github.com/Enach/paceday/backend/storage"
)

type llmHandlers struct {
	db *sql.DB
}

// llmTestRequest is the optional body of POST /api/llm/test (PAC-24). It names
// a provider, a model and a key to try. It cannot carry an endpoint: every
// value that reaches a URL comes from the caller's own stored settings, so a
// credential is never sent to a destination the caller chose.
type llmTestRequest struct {
	LLMProvider *string `json:"llmProvider"`
	LLMModel    *string `json:"llmModel"`
	LLMAPIKey   *string `json:"llmApiKey"`
}

func (h *llmHandlers) testLLM(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	// The body is required; {} probes the stored configuration (PAC-24).
	var body llmTestRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		writeError(w, "invalid request: send a JSON object with only llmProvider, llmModel and llmApiKey ({} to probe your saved settings)", http.StatusBadRequest)
		return
	}

	s, err := storage.GetOrCreateSettingsByUser(h.db, userIDFromCtx(r.Context()))
	if err != nil {
		writeServerError(w, r, "testLLM", err, http.StatusInternalServerError)
		return
	}
	storedKey := s.LLMAPIKey
	if body.LLMProvider != nil {
		if !validLLMProviders[*body.LLMProvider] {
			writeError(w, "llmProvider must be one of: openai, anthropic, ollama, bedrock, azure_openai, vertex", http.StatusBadRequest)
			return
		}
		s.LLMProvider = *body.LLMProvider
	}
	if body.LLMModel != nil {
		s.LLMModel = *body.LLMModel
	}
	if body.LLMAPIKey != nil {
		s.LLMAPIKey = *body.LLMAPIKey
	}
	// No status's body may carry the submitted or the stored key (AC-14).
	redact := func(msg string) string {
		for _, key := range []string{s.LLMAPIKey, storedKey} {
			if key != "" {
				msg = strings.ReplaceAll(msg, key, "[redacted]")
			}
		}
		return msg
	}

	client, err := nlp.NewLLMClientFromSettings(s)
	if err != nil {
		// The detail is logged with both keys redacted (AC-14); the body is generic (PAC-21).
		writeServerError(w, r, "build LLM client", errors.New(redact(err.Error())), http.StatusInternalServerError)
		return
	}

	// PAC-66: an ambient-credential provider (named in the body or stored)
	// sends nothing to a destination the operator has not approved. The check
	// needs no credential, so the refusal is reported even where none exists.
	if err := nlp.CheckDestination(r.Context(), client); err != nil {
		writeProviderFailure(w, r, err, redact)
		return
	}
	response, err := client.Complete(r.Context(), "You are a helpful assistant.", "Say hello")
	if err != nil {
		writeProviderFailure(w, r, err, redact)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":       true,
		"response": redact(response),
		"provider": s.LLMProvider,
	})
}

// writeProviderFailure answers a failed provider call: a PAC-66 destination
// refusal is a 422 LLMDestinationRefusal (nothing was sent); anything else is
// the 502 it has always been.
func writeProviderFailure(w http.ResponseWriter, r *http.Request, err error, redact func(string) string) {
	var refusal *nlp.DestinationRefusal
	if !errors.As(err, &refusal) {
		writeServerError(w, r, "LLM provider call", errors.New(redact(err.Error())), http.StatusBadGateway)
		return
	}
	body := gen.LLMDestinationRefusal{
		Error:  refusal.Message,
		Reason: gen.LLMDestinationRefusalReason(refusal.Reason),
	}
	if refusal.Property != "" {
		p := gen.LLMDestinationRefusalProperty(refusal.Property)
		body.Property = &p
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = json.NewEncoder(w).Encode(body)
}
