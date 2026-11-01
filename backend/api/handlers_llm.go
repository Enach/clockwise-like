package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/nlp"
	"github.com/Enach/paceday/backend/storage"
)

type llmHandlers struct {
	db *sql.DB
	// source is where the AI decision reads from; nil means db (PAC-73
	// Part 2, P2-AC-37).
	source nlp.AISource
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
	src := h.source
	if src == nil {
		src = nlp.DBSource{DB: h.db}
	}
	st, err := nlp.LoadAIState(r.Context(), src, userID)
	if err != nil {
		writeServerError(w, r, "testLLM", err, http.StatusInternalServerError)
		return
	}
	s := st.Settings
	// No status's body may carry the submitted key or any stored key
	// (AC-14), and nothing logged carries a key sent (Part 2 §3.1a).
	keys := append(s.LLMAPIKeys.All(), s.LLMUnboundAPIKey)
	if body.LLMAPIKey != nil {
		keys = append(keys, *body.LLMAPIKey)
	}

	d, bad := nlp.DecideProbeState(st, nlp.ProbeRequest{Provider: body.LLMProvider, Model: body.LLMModel, Key: body.LLMAPIKey})
	if bad != "" {
		writeError(w, bad, http.StatusBadRequest)
		return
	}
	if d.NotConfigured != nil {
		writeNotConfigured(w, d.NotConfigured)
		return
	}
	keys = append(keys, d.Secrets...)
	redact := func(msg string) string { return nlp.RedactSecrets(msg, keys) }

	// PAC-66 / PAC-66-B: the provider the probe uses sends nothing to a
	// destination not approved for it.
	if err := nlp.CheckDestination(r.Context(), d.Client); err != nil {
		writeProviderFailure(w, r, err, redact, d.Organization())
		return
	}
	response, err := d.Client.Complete(r.Context(), "You are a helpful assistant.", "Say hello")
	if err != nil {
		writeProviderFailure(w, r, err, redact, d.Organization())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":            true,
		"response":      redact(response),
		"provider":      d.Provider,
		"configuration": d.Configuration,
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
// the 502 it has always been. org says the refused destination is the
// organisation's (Part 2, P2-AC-41): the sentence names its administrator
// and the body carries configuration: organization; for the caller's own
// destination configuration is absent, never "own".
func writeProviderFailure(w http.ResponseWriter, r *http.Request, err error, redact func(string) string, org bool) {
	var refusal *nlp.DestinationRefusal
	if !errors.As(err, &refusal) {
		writeServerError(w, r, "LLM provider call", errors.New(redact(err.Error())), http.StatusBadGateway)
		return
	}
	if org {
		refusal = nlp.OrganizationDestinationRefusal(refusal)
	}
	body := gen.LLMDestinationRefusal{
		Error:  refusal.Message,
		Reason: gen.LLMDestinationRefusalReason(refusal.Reason),
	}
	if org {
		c := gen.LLMDestinationRefusalConfigurationOrganization
		body.Configuration = &c
	}
	if refusal.Property != "" {
		p := gen.LLMDestinationRefusalProperty(refusal.Property)
		body.Property = &p
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = json.NewEncoder(w).Encode(body)
}
