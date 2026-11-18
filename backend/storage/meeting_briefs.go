package storage

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type SlackMessage struct {
	ChannelName string `json:"channel_name"`
	AuthorName  string `json:"author_name"`
	Text        string `json:"text"`
	Timestamp   string `json:"timestamp"`
	Permalink   string `json:"permalink"`
}

type NotionPage struct {
	Title        string `json:"title"`
	URL          string `json:"url"`
	LastEditedAt string `json:"last_edited_time"`
	ParentName   string `json:"parent_name"`
}

type MeetingBrief struct {
	ID              uuid.UUID
	CalendarEventID string
	UserID          uuid.UUID
	GeneratedAt     time.Time
	SlackResults    []SlackMessage
	NotionResults   []NotionPage
	BriefText       string
	Status          string // pending | ready | failed
}

func UpsertMeetingBrief(db Querier, b *MeetingBrief) error {
	slack, err := json.Marshal(b.SlackResults)
	if err != nil {
		return err
	}
	notion, err := json.Marshal(b.NotionResults)
	if err != nil {
		return err
	}
	_, err = db.Exec(`
		INSERT INTO meeting_briefs
			(calendar_event_id, user_id, generated_at, slack_results, notion_results, brief_text, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (user_id, calendar_event_id) DO UPDATE SET
			generated_at   = EXCLUDED.generated_at,
			slack_results  = EXCLUDED.slack_results,
			notion_results = EXCLUDED.notion_results,
			brief_text     = EXCLUDED.brief_text,
			status         = EXCLUDED.status`,
		b.CalendarEventID, b.UserID, b.GeneratedAt,
		slack, notion, b.BriefText, b.Status,
	)
	return err
}

func GetMeetingBrief(db DBTX, userID uuid.UUID, calendarEventID string) (*MeetingBrief, error) {
	row := db.QueryRow(`
		SELECT id, calendar_event_id, user_id, generated_at,
		       slack_results, notion_results, brief_text, status
		FROM meeting_briefs
		WHERE user_id = $1 AND calendar_event_id = $2`, userID, calendarEventID)

	var b MeetingBrief
	var slackJSON, notionJSON []byte
	if err := row.Scan(&b.ID, &b.CalendarEventID, &b.UserID, &b.GeneratedAt,
		&slackJSON, &notionJSON, &b.BriefText, &b.Status); err != nil {
		return nil, err
	}
	if err := decodeBriefResults(&b, slackJSON, notionJSON); err != nil {
		return nil, err
	}
	return &b, nil
}

func ListMeetingBriefsForUser(db DBTX, userID uuid.UUID) ([]*MeetingBrief, error) {
	rows, err := db.Query(`
		SELECT id, calendar_event_id, user_id, generated_at,
		       slack_results, notion_results, brief_text, status
		FROM meeting_briefs
		WHERE user_id = $1
		ORDER BY generated_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var briefs []*MeetingBrief
	for rows.Next() {
		var b MeetingBrief
		var slackJSON, notionJSON []byte
		if err := rows.Scan(&b.ID, &b.CalendarEventID, &b.UserID, &b.GeneratedAt,
			&slackJSON, &notionJSON, &b.BriefText, &b.Status); err != nil {
			return nil, err
		}
		if err := decodeBriefResults(&b, slackJSON, notionJSON); err != nil {
			return nil, err
		}
		briefs = append(briefs, &b)
	}
	return briefs, rows.Err()
}

// decodeBriefResults decodes a brief's stored Slack and Notion results. A
// value that does not decode is an error naming the brief's row id (PAC-122
// SF-080); it was discarded and the brief read back with no sources.
func decodeBriefResults(b *MeetingBrief, slackJSON, notionJSON []byte) error {
	if err := json.Unmarshal(slackJSON, &b.SlackResults); err != nil {
		return fmt.Errorf("meeting brief %s: decode slack_results: %w", b.ID, err)
	}
	if err := json.Unmarshal(notionJSON, &b.NotionResults); err != nil {
		return fmt.Errorf("meeting brief %s: decode notion_results: %w", b.ID, err)
	}
	return nil
}
