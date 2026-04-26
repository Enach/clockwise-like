package nlp

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/Enach/clockwise-like/backend/engine"
	"github.com/Enach/clockwise-like/backend/storage"
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
	base := c.BaseURL
	if base == "" {
		base = "https://api.openai.com"
	}
	payload := map[string]interface{}{
		"model": c.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	}
	return postJSON(ctx, base+"/v1/chat/completions",
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
	base := c.BaseURL
	if base == "" {
		base = "https://api.anthropic.com"
	}
	payload := map[string]interface{}{
		"model":      c.Model,
		"max_tokens": 1024,
		"system":     system,
		"messages": []map[string]string{
			{"role": "user", "content": user},
		},
	}
	return postJSON(ctx, base+"/v1/messages",
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

type OllamaClient struct {
	BaseURL string
	Model   string
}

func (c *OllamaClient) Complete(ctx context.Context, system, user string) (string, error) {
	base := c.BaseURL
	if base == "" {
		base = "http://host.docker.internal:11434"
	}
	payload := map[string]interface{}{
		"model":  c.Model,
		"stream": false,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	}
	return postJSON(ctx, base+"/api/chat", nil, payload,
		func(body []byte) (string, error) {
			var resp struct {
				Message struct{ Content string } `json:"message"`
			}
			if err := json.Unmarshal(body, &resp); err != nil {
				return "", fmt.Errorf("ollama: unexpected response")
			}
			return resp.Message.Content, nil
		})
}

func postJSON(ctx context.Context, url string, headers map[string]string, payload interface{}, extract func([]byte) (string, error)) (string, error) {
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
	resp, err := http.DefaultClient.Do(req)
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

const systemPromptTpl = `You are a calendar scheduling assistant. Parse the user's scheduling request and return ONLY valid JSON, no prose.

Today is {{.Date}}. The user's timezone is {{.Timezone}}. Working hours: {{.WorkStart}}–{{.WorkEnd}}.

Interpret relative dates:
- "this week" = Monday {{.WeekStart}} to Friday {{.WeekEnd}}
- "next week" = following week (Mon–Fri)
- "tomorrow" = {{.Tomorrow}}
- "today" = {{.Date}}

Return one of these schemas:

For meeting scheduling:
{
  "intent": "schedule_meeting",
  "title": "<meeting title or null>",
  "duration_minutes": <integer>,
  "attendees": ["email@example.com"],
  "range_start": "<YYYY-MM-DD>",
  "range_end": "<YYYY-MM-DD>",
  "constraints": "<any extra constraints or null>"
}

For focus time requests:
{
  "intent": "schedule_focus",
  "duration_minutes": <integer>,
  "range_start": "<YYYY-MM-DD>",
  "range_end": "<YYYY-MM-DD>"
}

If unclear: { "intent": "unknown", "error": "<reason>" }`

type ParseResult struct {
	Intent          string                    `json:"intent"`
	Title           string                    `json:"title,omitempty"`
	DurationMinutes int                       `json:"duration_minutes,omitempty"`
	Attendees       []string                  `json:"attendees,omitempty"`
	RangeStart      time.Time                 `json:"range_start,omitempty"`
	RangeEnd        time.Time                 `json:"range_end,omitempty"`
	Constraints     string                    `json:"constraints,omitempty"`
	Error           string                    `json:"error,omitempty"`
	SuggestedSlots  []engine.SuggestedSlot    `json:"suggested_slots,omitempty"`
}

type NLPService struct {
	DB          *sql.DB
	OAuthConfig *oauth2.Config
}

func (s *NLPService) Parse(ctx context.Context, text string) (*ParseResult, error) {
	settings, err := storage.GetSettings(s.DB)
	if err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}

	client, err := s.buildLLMClient(settings)
	if err != nil {
		return &ParseResult{Intent: "unknown", Error: err.Error()}, nil
	}

	sysPrompt, err := buildSystemPrompt(settings)
	if err != nil {
		return nil, err
	}

	rawJSON, err := client.Complete(ctx, sysPrompt, text)
	if err != nil {
		rawJSON2, err2 := client.Complete(ctx, sysPrompt, text)
		if err2 != nil {
			return nil, fmt.Errorf("LLM error: %w", err)
		}
		rawJSON = rawJSON2
	}

	rawJSON = strings.TrimSpace(rawJSON)
	if idx := strings.Index(rawJSON, "{"); idx > 0 {
		rawJSON = rawJSON[idx:]
	}

	var raw struct {
		Intent          string   `json:"intent"`
		Title           string   `json:"title"`
		DurationMinutes int      `json:"duration_minutes"`
		Attendees       []string `json:"attendees"`
		RangeStart      string   `json:"range_start"`
		RangeEnd        string   `json:"range_end"`
		Constraints     string   `json:"constraints"`
		Error           string   `json:"error"`
	}
	if err := json.Unmarshal([]byte(rawJSON), &raw); err != nil {
		return &ParseResult{Intent: "unknown", Error: "LLM returned invalid JSON"}, nil
	}

	result := &ParseResult{
		Intent:          raw.Intent,
		Title:           raw.Title,
		DurationMinutes: raw.DurationMinutes,
		Attendees:       raw.Attendees,
		Constraints:     raw.Constraints,
		Error:           raw.Error,
	}
	if raw.RangeStart != "" {
		result.RangeStart, _ = time.Parse("2006-01-02", raw.RangeStart)
	}
	if raw.RangeEnd != "" {
		result.RangeEnd, _ = time.Parse("2006-01-02", raw.RangeEnd)
	}

	if raw.Intent == "schedule_meeting" && !result.RangeStart.IsZero() {
		scheduler := &engine.SmartScheduler{DB: s.DB, OAuthConfig: s.OAuthConfig}
		suggestions, err := scheduler.Suggest(ctx, engine.ScheduleRequest{
			DurationMinutes: result.DurationMinutes,
			Attendees:       result.Attendees,
			RangeStart:      result.RangeStart,
			RangeEnd:        result.RangeEnd.Add(24 * time.Hour),
			Title:           result.Title,
		})
		if err == nil {
			result.SuggestedSlots = suggestions.Slots
		}
	}

	storage.WriteAuditLog(s.DB, "nlp_parsed", fmt.Sprintf(`{"text":%q,"intent":%q}`, text, result.Intent))
	return result, nil
}

func (s *NLPService) buildLLMClient(settings *storage.Settings) (LLMClient, error) {
	return NewLLMClientFromSettings(settings)
}

func buildSystemPrompt(s *storage.Settings) (string, error) {
	now := time.Now()
	monday := startOfWeekLocal(now)
	friday := monday.AddDate(0, 0, 4)

	data := map[string]string{
		"Date":      now.Format("2006-01-02"),
		"Timezone":  s.Timezone,
		"WorkStart": s.WorkStart,
		"WorkEnd":   s.WorkEnd,
		"WeekStart": monday.Format("2006-01-02"),
		"WeekEnd":   friday.Format("2006-01-02"),
		"Tomorrow":  now.AddDate(0, 0, 1).Format("2006-01-02"),
	}

	tpl, err := template.New("sys").Parse(systemPromptTpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func startOfWeekLocal(t time.Time) time.Time {
	wd := int(t.Weekday())
	if wd == 0 {
		wd = 7
	}
	return time.Date(t.Year(), t.Month(), t.Day()-wd+1, 0, 0, 0, 0, t.Location())
}
