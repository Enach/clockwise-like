package nlp

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/internal/outbound"
	"github.com/Enach/paceday/backend/storage"
	"golang.org/x/oauth2"
)

type LLMClient interface {
	Complete(ctx context.Context, systemPrompt, userMessage string) (string, error)
}

type OpenAIClient struct {
	APIKey  string
	Model   string
	BaseURL string
}

func (c *OpenAIClient) Complete(ctx context.Context, system, user string) (string, error) {
	// PAC-66-B: only the chat operation of an approved destination.
	target, refusal := c.requestURL()
	if refusal != nil {
		return "", refusal
	}
	payload := map[string]interface{}{
		"model": c.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	}
	return postJSONWith(ctx, userHTTPClient(ProviderOpenAI), target,
		map[string]string{"Authorization": "Bearer " + c.APIKey}, payload,
		func(body []byte) (string, error) {
			var resp struct {
				Choices []struct {
					Message struct{ Content string } `json:"message"`
				} `json:"choices"`
			}
			if err := json.Unmarshal(body, &resp); err != nil || len(resp.Choices) == 0 {
				return "", fmt.Errorf("openai: unexpected response")
			}
			return resp.Choices[0].Message.Content, nil
		})
}

type AnthropicClient struct {
	APIKey  string
	Model   string
	BaseURL string
}

func (c *AnthropicClient) Complete(ctx context.Context, system, user string) (string, error) {
	// PAC-66-B: only the chat operation of an approved destination.
	target, refusal := c.requestURL()
	if refusal != nil {
		return "", refusal
	}
	payload := map[string]interface{}{
		"model":      c.Model,
		"max_tokens": 1024,
		"system":     system,
		"messages": []map[string]string{
			{"role": "user", "content": user},
		},
	}
	return postJSONWith(ctx, userHTTPClient(ProviderAnthropic), target,
		map[string]string{
			"x-api-key":         c.APIKey,
			"anthropic-version": "2023-06-01",
		}, payload,
		func(body []byte) (string, error) {
			var resp struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			}
			if err := json.Unmarshal(body, &resp); err != nil || len(resp.Content) == 0 {
				return "", fmt.Errorf("anthropic: unexpected response")
			}
			return resp.Content[0].Text, nil
		})
}

func postJSON(ctx context.Context, url string, headers map[string]string, payload interface{}, extract func([]byte) (string, error)) (string, error) {
	return postJSONWith(ctx, outbound.LLMClient(), url, headers, payload, extract)
}

// postJSONWith is postJSON through client.
func postJSONWith(ctx context.Context, client *http.Client, url string, headers map[string]string, payload interface{}, extract func([]byte) (string, error)) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("LLM API error %d: %s", resp.StatusCode, string(respBody))
	}
	return extract(respBody)
}

type ParseResult struct {
	Intent          string   `json:"intent"`
	Title           string   `json:"title,omitempty"`
	DurationMinutes int      `json:"duration_minutes,omitempty"`
	Attendees       []string `json:"attendees,omitempty"`
	// RangeStart and RangeEnd are absent when there is no range, never the
	// zero time (API-080, NEW-llm-bring-your-own AC-35).
	RangeStart       time.Time                  `json:"range_start,omitzero"`
	RangeEnd         time.Time                  `json:"range_end,omitzero"`
	PreferredTimes   []string                   `json:"preferred_times,omitempty"`
	AvoidTimes       []string                   `json:"avoid_times,omitempty"`
	Constraints      string                     `json:"constraints,omitempty"`
	TimezoneNotes    string                     `json:"timezone_notes,omitempty"`
	Error            string                     `json:"error,omitempty"`
	SuggestedSlots   []engine.SuggestedSlot     `json:"suggested_slots,omitempty"`
	ParticipantInfos []calendar.ParticipantInfo `json:"participant_infos,omitempty"`
	// NotConfigured is present only when AI is not configured for the caller
	// (NEW-llm-bring-your-own AC-13); then nothing was sent.
	NotConfigured *storage.AINotConfigured `json:"not_configured,omitempty"`
}

type NLPService struct {
	DB          *sql.DB
	OAuthConfig *oauth2.Config
	// Source is where the AI decision reads from; nil means DB (PAC-73
	// Part 2: a test replaces it, P2-AC-37).
	Source AISource
}

func (s *NLPService) source() AISource {
	if s.Source != nil {
		return s.Source
	}
	return DBSource{DB: s.DB}
}

const attendeeExtractionPrompt = `Extract email addresses from the user's text. Return ONLY JSON: {"attendees": ["email@example.com"]}. If none found, return {"attendees": []}.`

func (s *NLPService) Parse(ctx context.Context, text string) (*ParseResult, error) {
	userID := auth.UserIDFromContext(ctx)
	st, err := LoadAIState(ctx, s.source(), userID)
	if err != nil {
		return nil, err
	}
	settings := st.Settings

	// NEW-llm-bring-your-own: decide the configuration before anything is
	// sent. Not configured is a 200 answer with not_configured (AC-13). The
	// decision reads one consistent state (Part 2, P2-AC-37), and the client
	// carries only that state's values.
	decision := DecideState(st)
	if nc := decision.NotConfigured; nc != nil {
		return &ParseResult{Intent: "unknown", Error: nc.Message, NotConfigured: nc}, nil
	}
	client := decision.Client
	// PAC-66: refuse before the first Complete (attendee extraction), in the
	// existing "LLM unusable" shape. Other errors surface from Complete as
	// before.
	var refusal *DestinationRefusal
	if err := CheckDestination(ctx, client); errors.As(err, &refusal) {
		if decision.Organization() {
			refusal = OrganizationDestinationRefusal(refusal)
		}
		return &ParseResult{Intent: "unknown", Error: refusal.Message}, nil
	}

	// Pass 1: Extract attendee emails with a lightweight prompt
	attendees := s.extractAttendees(ctx, client, text, decision.Redact)

	// Resolve participant info (timezone, working hours)
	participants := calendar.ResolveParticipants(attendees)

	// Pass 2: Build full timezone-aware prompt and get structured result
	promptData := BuildSchedulingPromptData(settings, participants, time.Now())
	sysPrompt, err := renderSchedulingPrompt(promptData)
	if err != nil {
		return nil, err
	}

	rawJSON, err := client.Complete(ctx, sysPrompt, text)
	if err != nil {
		// Part 2 (spec §3.1a): the provider text goes to the log and Sentry
		// only (the handler's fixed 500), redacted of every key sent and the
		// caller's own keys.
		return nil, errors.New(decision.Redact("LLM error: " + err.Error()))
	}

	result := parseRawJSON(decision.Redact(rawJSON))
	result.ParticipantInfos = participants

	if result.Intent == "schedule_meeting" && !result.RangeStart.IsZero() {
		scheduler := &engine.SmartScheduler{DB: s.DB, OAuthConfig: s.OAuthConfig}
		req := engine.ScheduleRequest{
			DurationMinutes: result.DurationMinutes,
			Attendees:       result.Attendees,
			RangeStart:      result.RangeStart,
			RangeEnd:        result.RangeEnd.Add(24 * time.Hour),
			Title:           result.Title,
			PreferredTimes:  result.PreferredTimes,
			AvoidTimes:      result.AvoidTimes,
		}
		suggestions, err := scheduler.Suggest(ctx, req)
		if err == nil {
			result.SuggestedSlots = suggestions.Slots
		}
	}

	storage.WriteAuditLog(s.DB, auth.UserIDFromContext(ctx), "nlp_parsed", fmt.Sprintf(`{"text":%q,"intent":%q}`, text, result.Intent))
	return result, nil
}

func (s *NLPService) extractAttendees(ctx context.Context, client LLMClient, text string, redact func(string) string) []string {
	rawJSON, err := client.Complete(ctx, attendeeExtractionPrompt, text)
	if err != nil {
		return nil
	}
	rawJSON = cleanJSON(redact(rawJSON))
	var result struct {
		Attendees []string `json:"attendees"`
	}
	_ = json.Unmarshal([]byte(rawJSON), &result)
	return result.Attendees
}

func parseRawJSON(rawJSON string) *ParseResult {
	rawJSON = cleanJSON(rawJSON)

	var raw struct {
		Intent          string   `json:"intent"`
		Title           string   `json:"title"`
		DurationMinutes int      `json:"duration_minutes"`
		Attendees       []string `json:"attendees"`
		RangeStart      string   `json:"range_start"`
		RangeEnd        string   `json:"range_end"`
		PreferredTimes  []string `json:"preferred_times"`
		AvoidTimes      []string `json:"avoid_times"`
		Constraints     string   `json:"constraints"`
		TimezoneNotes   string   `json:"timezone_notes"`
		Error           string   `json:"error"`
	}
	if err := json.Unmarshal([]byte(rawJSON), &raw); err != nil {
		return &ParseResult{Intent: "unknown", Error: "LLM returned invalid JSON"}
	}

	result := &ParseResult{
		Intent:          raw.Intent,
		Title:           raw.Title,
		DurationMinutes: raw.DurationMinutes,
		Attendees:       raw.Attendees,
		PreferredTimes:  raw.PreferredTimes,
		AvoidTimes:      raw.AvoidTimes,
		Constraints:     raw.Constraints,
		TimezoneNotes:   raw.TimezoneNotes,
		Error:           raw.Error,
	}
	if raw.RangeStart != "" {
		result.RangeStart, _ = time.Parse("2006-01-02", raw.RangeStart)
	}
	if raw.RangeEnd != "" {
		result.RangeEnd, _ = time.Parse("2006-01-02", raw.RangeEnd)
	}
	return result
}

func cleanJSON(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.Index(s, "{"); idx > 0 {
		s = s[idx:]
	}
	return s
}

// startOfWeekLocal is kept for backward compatibility.
func startOfWeekLocal(t time.Time) time.Time {
	return startOfWeek(t)
}

// buildSystemPrompt builds a scheduling prompt from settings alone (no participants).
func buildSystemPrompt(s *storage.Settings) (string, error) {
	data := BuildSchedulingPromptData(s, nil, time.Now())
	return renderSchedulingPrompt(data)
}
