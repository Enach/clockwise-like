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

// llmTestRequest is the body of POST /api/llm/test (PAC-24). It names a
// choice, a model and a key to try. It cannot carry an endpoint: every value
// that reaches a URL comes from the caller's own stored settings, so a key is
// never sent to a destination the caller chose.
type llmTestRequest struct {
	LLMProvider *string `json:"llmProvider"`
	LLMModel    *string `json:"llmModel"`
	LLMAPIKey   *string `json:"llmApiKey"`
}

// testLLM probes one AI configuration. Order of decisions
// (NEW-llm-bring-your-own, contract testLLMConnection): the body's shape
// (400), which configuration and whether it has a key (409, nothing sent),
// the destination (422, nothing sent), then the request (200 or 502).
func (h *llmHandlers) testLLM(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	// The body is required; {} probes the saved choice (PAC-24).
	var body llmTestRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		writeError(w, "invalid request: send a JSON object with only llmProvider, llmModel and llmApiKey ({} to probe your saved settings)", http.StatusBadRequest)
		return
	}

	userID := userIDFromCtx(r.Context())
	s, err := storage.GetOrCreateSettingsByUser(h.db, userID)
	if err != nil {
		writeServerError(w, r, "testLLM", err, http.StatusInternalServerError)
		return
	}
	inOrg, err := storage.UserInOrganization(h.db, userID)
	if err != nil {
		writeServerError(w, r, "testLLM", err, http.StatusInternalServerError)
		return
	}
	// No status's body may carry the submitted key or any stored key (AC-14).
	keys := append(s.LLMAPIKeys.All(), s.LLMUnboundAPIKey)
	if body.LLMAPIKey != nil {
		keys = append(keys, *body.LLMAPIKey)
	}
	redact := func(msg string) string {
		for _, key := range keys {
			if key != "" {
				msg = strings.ReplaceAll(msg, key, "[redacted]")
			}
		}
		return msg
	}

	d, bad := nlp.DecideProbe(s, inOrg, nlp.ProbeRequest{Provider: body.LLMProvider, Model: body.LLMModel, Key: body.LLMAPIKey})
	if bad != "" {
		writeError(w, bad, http.StatusBadRequest)
		return
	}
	if d.NotConfigured != nil {
		writeNotConfigured(w, d.NotConfigured)
		return
	}

	// PAC-66 / PAC-66-B: the provider the probe uses sends nothing to a
	// destination not approved for it.
	if err := nlp.CheckDestination(r.Context(), d.Client); err != nil {
		writeProviderFailure(w, r, err, redact)
		return
	}
	response, err := d.Client.Complete(r.Context(), "You are a helpful assistant.", "Say hello")
	if err != nil {
		writeProviderFailure(w, r, err, redact)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":       true,
		"response": redact(response),
		"provider": d.Provider,
	})
}

// writeNotConfigured answers the 409 LLMNotConfiguredResponse
// (NEW-llm-bring-your-own): AI is not configured and nothing was sent.
func writeNotConfigured(w http.ResponseWriter, nc *storage.AINotConfigured) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(struct {
		Error         string                   `json:"error"`
		NotConfigured *storage.AINotConfigured `json:"notConfigured"`
	}{nc.Message, nc})
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
