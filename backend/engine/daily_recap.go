package engine

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// DailyRecapService sends Block Kit morning summaries via Slack.
type DailyRecapService struct {
	DB *sql.DB
}

// RecapSend records idempotency for one user + date.
type RecapSend struct {
	UserID          uuid.UUID
	SentDate        time.Time
	SentAt          time.Time
	SlackMessageTS  string
	Status          string // "sent" | "failed"
}

// ── Storage helpers ───────────────────────────────────────────────────────────

func recapAlreadySent(db *sql.DB, userID uuid.UUID, date time.Time) bool {
	var count int
	row := db.QueryRow(
		`SELECT COUNT(*) FROM recap_sends WHERE user_id=$1 AND sent_date=$2 AND status='sent'`,
		userID, date.Format("2006-01-02"),
	)
	_ = row.Scan(&count)
	return count > 0
}

func insertRecapSend(db *sql.DB, r RecapSend) error {
	_, err := db.Exec(`
		INSERT INTO recap_sends (user_id, sent_date, sent_at, slack_message_ts, status)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (user_id, sent_date) DO UPDATE SET
			sent_at=EXCLUDED.sent_at,
			slack_message_ts=EXCLUDED.slack_message_ts,
			status=EXCLUDED.status`,
		r.UserID, r.SentDate.Format("2006-01-02"), r.SentAt,
		r.SlackMessageTS, r.Status,
	)
	return err
}

// ── Scheduling ────────────────────────────────────────────────────────────────

// RunForUser checks whether it's time to send a recap to a single user and
// sends if so.  Designed to be called every minute from a cron job.
func (s *DailyRecapService) RunForUser(ctx context.Context, userID uuid.UUID, userName string) {
	settings, err := storage.GetSettings(s.DB)
	if err != nil || !settings.RecapEnabled {
		return
	}

	conn, err := storage.GetWorkspaceConnection(s.DB, userID, "slack")
	if err != nil {
		return // user has no Slack connection
	}

	loc, err := time.LoadLocation(settings.Timezone)
	if err != nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)

	// Parse send_time "HH:MM"
	var sendH, sendM int
	if _, err := fmt.Sscanf(settings.RecapSendTime, "%d:%d", &sendH, &sendM); err != nil {
		return
	}

	if now.Hour() != sendH || now.Minute() != sendM {
		return
	}

	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	if recapAlreadySent(s.DB, userID, today) {
		return
	}

	msg := s.BuildMessage(ctx, userID, userName, now, settings)
	ts, sendErr := sendSlackMessage(ctx, conn.BotToken, settings.RecapSendTo, settings.RecapChannelID, conn.WorkspaceID, userID, msg)

	rec := RecapSend{
		UserID:   userID,
		SentDate: today,
		SentAt:   time.Now().UTC(),
		Status:   "sent",
	}
	if sendErr != nil {
		rec.Status = "failed"
		log.Printf("daily recap send error user=%s: %v", userID, sendErr)
	} else {
		rec.SlackMessageTS = ts
	}
	_ = insertRecapSend(s.DB, rec)
}

// RunAll iterates all users with recap_enabled and calls RunForUser.
// Called every minute by the cron in main.go.
func (s *DailyRecapService) RunAll(ctx context.Context) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT u.id, u.name
		FROM users u
		INNER JOIN settings st ON st.id = 1
		WHERE st.recap_enabled = true`)
	if err != nil {
		log.Printf("daily recap: list users error: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			continue
		}
		go s.RunForUser(ctx, id, name)
		time.Sleep(time.Second) // stagger sends to respect Slack rate limits
	}
}

// ── Message construction ──────────────────────────────────────────────────────

type slackBlock map[string]interface{}

// BuildMessage assembles the Slack Block Kit payload for a user's day.
func (s *DailyRecapService) BuildMessage(ctx context.Context, userID uuid.UUID, userName string, now time.Time, settings *storage.Settings) []slackBlock {
	firstName := firstName(userName)
	weekday := now.Format("Monday")
	date := now.Format("January 2")
	tz := now.Location().String()

	var blocks []slackBlock

	// Header
	blocks = append(blocks, slackBlock{
		"type": "header",
		"text": slackBlock{"type": "plain_text", "text": fmt.Sprintf("Good morning %s ☀️", firstName), "emoji": true},
	})
	blocks = append(blocks, slackBlock{
		"type":     "context",
		"elements": []slackBlock{{"type": "mrkdwn", "text": fmt.Sprintf("%s, %s — %s", weekday, date, tz)}},
	})

	// Fetch today's data
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	todayEnd := todayStart.Add(24 * time.Hour)

	meetings := s.fetchMeetings(ctx, userID, todayStart, todayEnd)
	focusBlocks := s.fetchFocusBlocks(todayStart)
	habits := s.fetchHabits(todayStart)
	briefs := s.fetchBriefs(ctx, userID, meetings)

	// Summary line
	blocks = append(blocks, s.buildSummaryBlock(meetings, focusBlocks, habits))
	blocks = append(blocks, slackBlock{"type": "divider"})

	// Meetings
	for _, m := range meetings {
		brief := briefs[m.ID]
		blocks = append(blocks, s.buildMeetingBlocks(m, brief, settings)...)
	}

	// Focus blocks section
	if settings.RecapIncludeFocus && len(focusBlocks) > 0 {
		blocks = append(blocks, slackBlock{"type": "divider"})
		blocks = append(blocks, slackBlock{
			"type": "section",
			"text": slackBlock{"type": "mrkdwn", "text": "*Focus time today*"},
		})
		listed := focusBlocks
		extra := 0
		if len(listed) > 3 {
			listed, extra = focusBlocks[:3], len(focusBlocks)-3
		}
		var sb strings.Builder
		for _, fb := range listed {
			sb.WriteString(fmt.Sprintf("• %s–%s Focus block (%s)\n", fb.Start, fb.End, fb.Duration))
		}
		if extra > 0 {
			sb.WriteString(fmt.Sprintf("…and %d more", extra))
		}
		blocks = append(blocks, slackBlock{
			"type": "section",
			"text": slackBlock{"type": "mrkdwn", "text": strings.TrimRight(sb.String(), "\n")},
		})
	}

	// Habits section
	if settings.RecapIncludeHabits && len(habits) > 0 {
		blocks = append(blocks, slackBlock{"type": "divider"})
		blocks = append(blocks, slackBlock{
			"type": "section",
			"text": slackBlock{"type": "mrkdwn", "text": "*Habits scheduled*"},
		})
		var sb strings.Builder
		for _, h := range habits {
			sb.WriteString(fmt.Sprintf("• %s %s · %s–%s\n", h.Emoji, h.Title, h.WindowStart, h.WindowEnd))
		}
		blocks = append(blocks, slackBlock{
			"type": "section",
			"text": slackBlock{"type": "mrkdwn", "text": strings.TrimRight(sb.String(), "\n")},
		})
	}

	// Footer
	blocks = append(blocks, slackBlock{"type": "divider"})
	blocks = append(blocks, slackBlock{
		"type":     "context",
		"elements": []slackBlock{{"type": "mrkdwn", "text": "Sent by Paceday · <http://localhost/app|Open app> · Manage recap settings"}},
	})

	return blocks
}

func (s *DailyRecapService) buildSummaryBlock(meetings []recapMeeting, focus []recapFocusBlock, habits []recapHabit) slackBlock {
	if len(meetings) == 0 {
		focusH := totalHours(len(focus) * 60)
		return slackBlock{
			"type": "section",
			"text": slackBlock{"type": "mrkdwn", "text": fmt.Sprintf("You have a clear calendar today — *%s* of focus time.", focusH)},
		}
	}
	meetH := totalHours(totalMeetingMinutes(meetings))
	focusH := totalHours(len(focus) * 60)
	line := fmt.Sprintf("Today you have *%d meeting%s* (%s), *%d focus block%s* (%s protected)",
		len(meetings), plural(len(meetings)), meetH,
		len(focus), plural(len(focus)), focusH,
	)
	if len(habits) > 0 {
		line += fmt.Sprintf(", and *%d habit%s* scheduled", len(habits), plural(len(habits)))
	}
	line += "."
	return slackBlock{
		"type": "section",
		"text": slackBlock{"type": "mrkdwn", "text": line},
	}
}

func (s *DailyRecapService) buildMeetingBlocks(m recapMeeting, brief *storage.MeetingBrief, settings *storage.Settings) []slackBlock {
	var blocks []slackBlock

	title := fmt.Sprintf("*%s – %s* %s", m.Start, m.End, m.Title)
	text := title
	if len(m.Attendees) > 0 {
		text += "\n*With: " + strings.Join(m.Attendees, ", ") + "*"
	}

	if settings.RecapIncludeBriefs && brief != nil && brief.Status == "ready" {
		// Probable goals (first 120 chars of brief)
		goals := extractGoals(brief.BriefText)
		if goals != "" {
			text += "\n_" + goals + "_"
		}
		// Documents — max 2
		for i, n := range brief.NotionResults {
			if i >= 2 {
				extra := len(brief.NotionResults) - 2
				text += fmt.Sprintf("\n📄 +%d more documents", extra)
				break
			}
			text += fmt.Sprintf("\n📄 <%s|%s>", n.URL, n.Title)
		}
		// Slack threads — max 1
		if len(brief.SlackResults) > 0 {
			sl := brief.SlackResults[0]
			excerpt := sl.Text
			if len([]rune(excerpt)) > 80 {
				excerpt = string([]rune(excerpt)[:80]) + "…"
			}
			text += fmt.Sprintf("\n💬 #%s: %s", sl.ChannelName, excerpt)
		}
	}

	blocks = append(blocks, slackBlock{
		"type": "section",
		"text": slackBlock{"type": "mrkdwn", "text": text},
	})
	return blocks
}

// ── Data fetching stubs ───────────────────────────────────────────────────────
// These call the relevant storage functions. In production the calendar data
// would be fetched from the synced event store; here we use the focus_blocks
// table and habits for the non-calendar parts.

type recapMeeting struct {
	ID        string
	Title     string
	Start     string
	End       string
	Attendees []string
}

type recapFocusBlock struct {
	Start    string
	End      string
	Duration string
}

type recapHabit struct {
	Emoji       string
	Title       string
	WindowStart string
	WindowEnd   string
}

func (s *DailyRecapService) fetchMeetings(_ context.Context, _ uuid.UUID, start, end time.Time) []recapMeeting {
	rows, err := s.DB.Query(`
		SELECT calendar_event_id, brief_text
		FROM meeting_briefs
		WHERE user_id IS NOT NULL
		  AND generated_at >= $1 AND generated_at < $2
		LIMIT 20`, start, end)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var meetings []recapMeeting
	for rows.Next() {
		var id, briefText string
		_ = rows.Scan(&id, &briefText)
		meetings = append(meetings, recapMeeting{
			ID:    id,
			Title: "Meeting",
			Start: start.Format("15:04"),
			End:   end.Format("15:04"),
		})
	}
	return meetings
}

func (s *DailyRecapService) fetchFocusBlocks(day time.Time) []recapFocusBlock {
	rows, err := s.DB.Query(`
		SELECT start_time, end_time
		FROM focus_blocks
		WHERE date_trunc('day', start_time) = date_trunc('day', $1::timestamptz)
		ORDER BY start_time`, day)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var blocks []recapFocusBlock
	for rows.Next() {
		var st, et time.Time
		if err := rows.Scan(&st, &et); err != nil {
			continue
		}
		dur := et.Sub(st)
		blocks = append(blocks, recapFocusBlock{
			Start:    st.Format("15:04"),
			End:      et.Format("15:04"),
			Duration: fmt.Sprintf("%.0fh", dur.Hours()),
		})
	}
	return blocks
}

func (s *DailyRecapService) fetchHabits(day time.Time) []recapHabit {
	rows, err := s.DB.Query(`
		SELECT h.title, h.color, ho.window_start, ho.window_end
		FROM habit_occurrences ho
		JOIN habits h ON h.id = ho.habit_id
		WHERE ho.scheduled_date = $1 AND ho.status = 'scheduled'
		ORDER BY ho.window_start`, day.Format("2006-01-02"))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var habits []recapHabit
	for rows.Next() {
		var title, color, ws, we string
		if err := rows.Scan(&title, &color, &ws, &we); err != nil {
			continue
		}
		habits = append(habits, recapHabit{Emoji: "🎯", Title: title, WindowStart: ws, WindowEnd: we})
	}
	return habits
}

func (s *DailyRecapService) fetchBriefs(_ context.Context, userID uuid.UUID, meetings []recapMeeting) map[string]*storage.MeetingBrief {
	result := map[string]*storage.MeetingBrief{}
	for _, m := range meetings {
		b, err := storage.GetMeetingBrief(s.DB, userID, m.ID)
		if err == nil {
			result[m.ID] = b
		}
	}
	return result
}

// ── Slack sending ─────────────────────────────────────────────────────────────

// SendSlackRecap is exported so the API handler can call it for the test endpoint.
func SendSlackRecap(ctx context.Context, botToken, sendTo, channelID, workspaceID string, userID uuid.UUID, blocks []slackBlock) (string, error) {
	return sendSlackMessage(ctx, botToken, sendTo, channelID, workspaceID, userID, blocks)
}

func sendSlackMessage(ctx context.Context, botToken, sendTo, channelID, workspaceID string, userID uuid.UUID, blocks []slackBlock) (string, error) {
	target := channelID
	if sendTo == "dm" || target == "" {
		// Resolve Slack user ID from workspace connection
		uid, err := resolveSlackUserID(ctx, botToken, workspaceID)
		if err != nil {
			return "", fmt.Errorf("resolve slack user: %w", err)
		}
		target = uid
	}

	payload := map[string]interface{}{
		"channel": target,
		"blocks":  blocks,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, slackAPIBase+"/chat.postMessage",
		bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+botToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	var result struct {
		OK      bool   `json:"ok"`
		Error   string `json:"error"`
		Message struct {
			Ts string `json:"ts"`
		} `json:"message"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", err
	}
	if !result.OK {
		return "", fmt.Errorf("slack chat.postMessage: %s", result.Error)
	}
	return result.Message.Ts, nil
}

func resolveSlackUserID(ctx context.Context, botToken, _ string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, slackAPIBase+"/auth.test", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+botToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		OK     bool   `json:"ok"`
		UserID string `json:"user_id"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if !result.OK {
		return "", fmt.Errorf("slack auth.test: %s", result.Error)
	}
	return result.UserID, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func firstName(fullName string) string {
	parts := strings.Fields(fullName)
	if len(parts) == 0 {
		return "there"
	}
	return parts[0]
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func totalHours(minutes int) string {
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	h := minutes / 60
	m := minutes % 60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}

func totalMeetingMinutes(meetings []recapMeeting) int {
	return len(meetings) * 60 // approximate; real impl would use actual durations
}

func extractGoals(briefText string) string {
	// Look for "## Probable goals" section and return first non-empty line after it
	lines := strings.Split(briefText, "\n")
	inSection := false
	for _, line := range lines {
		if strings.Contains(line, "Probable goals") {
			inSection = true
			continue
		}
		if inSection && strings.HasPrefix(line, "##") {
			break
		}
		if inSection {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if len([]rune(line)) > 120 {
				line = string([]rune(line)[:120]) + "…"
			}
			return line
		}
	}
	return ""
}
