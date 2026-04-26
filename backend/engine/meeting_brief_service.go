package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	googlecalendar "google.golang.org/api/calendar/v3"
)

// LLMCompleter is a narrow interface so the engine package avoids importing nlp.
type LLMCompleter interface {
	Complete(ctx context.Context, systemPrompt, userMessage string) (string, error)
}

const (
	briefCacheTTL    = 24 * time.Hour
	slackAPIBase     = "https://slack.com/api"
	notionAPIBase    = "https://api.notion.com/v1"
	maxSlackMessages = 10
	maxNotionPages   = 8
	maxTerms         = 5
	maxTermLen       = 50
	msgExcerptLen    = 200
	slackLookback    = 30 * 24 * time.Hour
	notionLookback   = 90 * 24 * time.Hour
)

type MeetingBriefService struct {
	DB  *sql.DB
	LLM LLMCompleter // optional; if nil, brief_text is left empty
}

// Generate fetches Slack + Notion context and writes an LLM brief for a meeting.
// If force=false and a ready brief younger than 24h exists, the cached one is returned.
func (s *MeetingBriefService) Generate(ctx context.Context, userID uuid.UUID, event *googlecalendar.Event, force bool) (*storage.MeetingBrief, error) {
	if !force {
		existing, err := storage.GetMeetingBrief(s.DB, userID, event.Id)
		if err == nil && existing.Status == "ready" && time.Since(existing.GeneratedAt) < briefCacheTTL {
			return existing, nil
		}
	}

	brief := &storage.MeetingBrief{
		CalendarEventID: event.Id,
		UserID:          userID,
		GeneratedAt:     time.Now().UTC(),
		Status:          "pending",
	}
	_ = storage.UpsertMeetingBrief(s.DB, brief)

	terms := buildSearchTerms(event)

	conn, err := storage.GetWorkspaceConnection(s.DB, userID, "slack")
	if err == nil {
		brief.SlackResults, _ = fetchSlackMessages(ctx, conn.BotToken, terms)
	}

	notionConn, err := storage.GetWorkspaceConnection(s.DB, userID, "notion")
	if err == nil {
		brief.NotionResults, _ = fetchNotionPages(ctx, notionConn.AccessToken, terms)
	}

	if s.LLM == nil {
		brief.Status = "failed"
		brief.BriefText = "LLM not configured"
		_ = storage.UpsertMeetingBrief(s.DB, brief)
		return brief, nil
	}

	prompt := buildBriefPrompt(event, brief.SlackResults, brief.NotionResults)
	text, err := s.LLM.Complete(ctx, briefSystemPrompt, prompt)
	if err != nil {
		brief.Status = "failed"
		brief.BriefText = fmt.Sprintf("LLM error: %v", err)
	} else {
		brief.Status = "ready"
		brief.BriefText = text
	}
	brief.GeneratedAt = time.Now().UTC()
	_ = storage.UpsertMeetingBrief(s.DB, brief)
	return brief, nil
}

// buildSearchTerms extracts terms from meeting title and attendee names (not emails).
func buildSearchTerms(event *googlecalendar.Event) []string {
	seen := map[string]bool{}
	var terms []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if len(s) <= 3 || seen[s] {
			return
		}
		seen[s] = true
		if utf8.RuneCountInString(s) > maxTermLen {
			runes := []rune(s)
			s = string(runes[:maxTermLen])
		}
		terms = append(terms, s)
	}
	add(event.Summary)
	for _, a := range event.Attendees {
		if a.DisplayName != "" {
			add(a.DisplayName)
		}
	}
	if len(terms) > maxTerms {
		terms = terms[:maxTerms]
	}
	return terms
}

// ── Slack ────────────────────────────────────────────────────────────────────

type slackSearchResp struct {
	OK       bool `json:"ok"`
	Messages struct {
		Matches []struct {
			Channel struct {
				Name string `json:"name"`
			} `json:"channel"`
			Username  string `json:"username"`
			Text      string `json:"text"`
			Ts        string `json:"ts"`
			Permalink string `json:"permalink"`
		} `json:"matches"`
	} `json:"messages"`
}

func fetchSlackMessages(ctx context.Context, botToken string, terms []string) ([]storage.SlackMessage, error) {
	cutoff := time.Now().Add(-slackLookback)
	seen := map[string]bool{}
	var results []storage.SlackMessage

	client := &http.Client{Timeout: 5 * time.Second}
	for _, term := range terms {
		if len(results) >= maxSlackMessages {
			break
		}
		q := url.Values{}
		q.Set("query", term)
		q.Set("count", "5")
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			slackAPIBase+"/search.messages?"+q.Encode(), nil)
		if err != nil {
			continue
		}
		req.Header.Set("Authorization", "Bearer "+botToken)

		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var sr slackSearchResp
		if err := json.Unmarshal(body, &sr); err != nil || !sr.OK {
			continue
		}
		for _, m := range sr.Messages.Matches {
			key := m.Ts + m.Channel.Name
			if seen[key] {
				continue
			}
			// ts is Unix seconds as string; parse for age check
			ts := m.Ts
			if idx := strings.Index(ts, "."); idx >= 0 {
				ts = ts[:idx]
			}
			var sec int64
			_, _ = fmt.Sscanf(ts, "%d", &sec)
			if sec > 0 && time.Unix(sec, 0).Before(cutoff) {
				continue
			}
			seen[key] = true
			excerpt := m.Text
			if len([]rune(excerpt)) > msgExcerptLen {
				excerpt = string([]rune(excerpt)[:msgExcerptLen])
			}
			results = append(results, storage.SlackMessage{
				ChannelName: m.Channel.Name,
				AuthorName:  m.Username,
				Text:        excerpt,
				Timestamp:   m.Ts,
				Permalink:   m.Permalink,
			})
			if len(results) >= maxSlackMessages {
				break
			}
		}
	}
	return results, nil
}

// ── Notion ───────────────────────────────────────────────────────────────────

type notionSearchResp struct {
	Results []struct {
		ID         string `json:"id"`
		URL        string `json:"url"`
		LastEdited string `json:"last_edited_time"`
		Properties map[string]struct {
			Title []struct {
				PlainText string `json:"plain_text"`
			} `json:"title"`
		} `json:"properties"`
		Parent struct {
			DatabaseID string `json:"database_id"`
			PageID     string `json:"page_id"`
		} `json:"parent"`
	} `json:"results"`
}

func fetchNotionPages(ctx context.Context, accessToken string, terms []string) ([]storage.NotionPage, error) {
	cutoff := time.Now().Add(-notionLookback)
	seen := map[string]bool{}
	var results []storage.NotionPage

	client := &http.Client{Timeout: 5 * time.Second}
	for _, term := range terms {
		if len(results) >= maxNotionPages {
			break
		}
		payload := map[string]interface{}{
			"query":     term,
			"filter":    map[string]string{"value": "page", "property": "object"},
			"page_size": 5,
		}
		body, _ := json.Marshal(payload)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			notionAPIBase+"/search", strings.NewReader(string(body)))
		if err != nil {
			continue
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Notion-Version", "2022-06-28")

		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var nr notionSearchResp
		if err := json.Unmarshal(respBody, &nr); err != nil {
			continue
		}
		for _, p := range nr.Results {
			if seen[p.ID] {
				continue
			}
			if p.LastEdited != "" {
				t, err := time.Parse(time.RFC3339, p.LastEdited)
				if err == nil && t.Before(cutoff) {
					continue
				}
			}
			title := extractNotionTitle(p.Properties)
			if title == "" {
				continue
			}
			seen[p.ID] = true
			results = append(results, storage.NotionPage{
				Title:        title,
				URL:          p.URL,
				LastEditedAt: p.LastEdited,
			})
			if len(results) >= maxNotionPages {
				break
			}
		}
	}
	return results, nil
}

func extractNotionTitle(props map[string]struct {
	Title []struct {
		PlainText string `json:"plain_text"`
	} `json:"title"`
}) string {
	for _, v := range props {
		for _, t := range v.Title {
			if t.PlainText != "" {
				return t.PlainText
			}
		}
	}
	return ""
}

// ── LLM prompt ───────────────────────────────────────────────────────────────

const briefSystemPrompt = `You are a professional meeting prep assistant. Given a meeting and relevant context found in the user workspace, write a concise pre-meeting brief (max 200 words) that:
1. Lists 2-5 Notion documents most likely relevant to read before this meeting (title + URL)
2. Summarizes 1-3 key Slack threads related to the meeting topic
3. States in 2-3 sentences the probable goals or agenda of the meeting based on the context
Format: use markdown with three sections: "## Documents to review", "## Recent conversations", "## Probable goals".`

func buildBriefPrompt(event *googlecalendar.Event, slack []storage.SlackMessage, notion []storage.NotionPage) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Meeting: %s\n", event.Summary))

	var names []string
	for _, a := range event.Attendees {
		if a.DisplayName != "" {
			names = append(names, a.DisplayName)
		} else if a.Email != "" {
			names = append(names, a.Email)
		}
	}
	if len(names) > 0 {
		sb.WriteString(fmt.Sprintf("Attendees: %s\n", strings.Join(names, ", ")))
	}
	if event.Start != nil && event.Start.DateTime != "" {
		sb.WriteString(fmt.Sprintf("Time: %s\n", event.Start.DateTime))
	}

	sb.WriteString("\nNotion pages found:\n")
	if len(notion) == 0 {
		sb.WriteString("(none)\n")
	}
	for _, p := range notion {
		sb.WriteString(fmt.Sprintf("- %s (last edited: %s) %s\n", p.Title, p.LastEditedAt, p.URL))
	}

	sb.WriteString("\nSlack messages found:\n")
	if len(slack) == 0 {
		sb.WriteString("(none)\n")
	}
	for _, m := range slack {
		sb.WriteString(fmt.Sprintf("- #%s | %s: %s\n", m.ChannelName, m.AuthorName, m.Text))
	}

	sb.WriteString("\nWrite the meeting brief.")
	return sb.String()
}
