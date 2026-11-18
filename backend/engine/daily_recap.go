package engine

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/internal/lifecycle"
	"github.com/Enach/paceday/backend/internal/outbound"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// DailyRecapService sends Block Kit morning summaries via Slack.
type DailyRecapService struct {
	DB    *sql.DB
	Clock Clock
}

func (s *DailyRecapService) now() time.Time {
	if s.Clock != nil {
		return s.Clock.Now()
	}
	return SystemClock.Now()
}

// RecapSend records idempotency for one user + date.
type RecapSend struct {
	UserID         uuid.UUID
	SentDate       time.Time
	SentAt         time.Time
	SlackMessageTS string
	Status         string // "sent" | "failed"
}

// ── Storage helpers ───────────────────────────────────────────────────────────

// recapAlreadySent reports whether the user's recap for date was sent. A
// failed read is an error (PAC-122 SF-082): it read as "not sent", so the
// recap could be sent twice.
func recapAlreadySent(db *sql.DB, userID uuid.UUID, date time.Time) (bool, error) {
	var count int
	row := db.QueryRow(
		`SELECT COUNT(*) FROM recap_sends WHERE user_id=$1 AND sent_date=$2 AND status='sent'`,
		userID, date.Format("2006-01-02"),
	)
	if err := row.Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
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
// sends if so.  Designed to be called every minute from a cron job (RunAll
// calls it only at the user's send minute, PAC-136).
//
// PAC-122 SF-082: a failure that stops the recap is logged and reported as
// daily-recap/<user id> (ids only): a failed settings or Slack-connection
// read, a stored send time that is not HH:MM, a failed read of whether
// today's recap was sent (nothing is sent: it read as "not sent" and could
// send twice), and a failed read of the day's content (SF-046: nothing is
// sent; it read as "a clear calendar today"). The recap being off, or no
// Slack connection, is not a failure.
func (s *DailyRecapService) RunForUser(ctx context.Context, userID uuid.UUID, userName string) {
	s.runForUserAt(ctx, userID, userName, s.now())
}

// runForUserAt is RunForUser gated at the instant at: RunAll passes the
// instant its own gate used, so a user whose send starts late in the run
// (sends are staggered) is not re-gated past their minute (PAC-136 review
// L2).
func (s *DailyRecapService) runForUserAt(ctx context.Context, userID uuid.UUID, userName string, at time.Time) {
	unit := lifecycle.Unit{Kind: "daily-recap", ID: userID.String()}
	stop := func(what string, err error) {
		log.Printf("daily recap user=%s: %s: %v", userID, what, err)
		lifecycle.Report(unit, fmt.Errorf("daily recap user=%s: %s: %w", userID, what, err))
	}

	settings, err := storage.GetOrCreateSettingsByUser(s.DB, userID)
	if err != nil {
		stop("read settings", err)
		return
	}
	if !settings.RecapEnabled {
		return
	}

	now, due, err := recapDue(at, settings.Timezone, settings.RecapSendTime)
	if err != nil {
		stop("stored send time is not HH:MM", err)
		return
	}
	if !due {
		return
	}
	loc := now.Location()

	// PAC-136: the Slack connection is read only at the send time (it was
	// read every minute, so an outage reported once per user per minute).
	conn, err := storage.GetWorkspaceConnection(s.DB, userID, "slack")
	if errors.Is(err, sql.ErrNoRows) {
		return // no Slack connection: skip this user
	}
	if err != nil {
		stop("read slack connection", err)
		return
	}
	if conn.TokenErr != nil {
		return // (PAC-84 AC-17) its token cannot be decrypted (the decrypt hook reports it): skip this user
	}

	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	sent, err := recapAlreadySent(s.DB, userID, today)
	if err != nil {
		stop("read whether the recap was sent", err)
		return
	}
	if sent {
		return
	}

	msg, err := s.BuildMessage(ctx, userID, userName, now, settings)
	if err != nil {
		stop("build message", err)
		return
	}
	ts, sendErr := sendSlackMessage(ctx, conn.BotToken, settings.RecapSendTo, settings.RecapChannelID, conn.WorkspaceID, userID, msg)

	rec := RecapSend{
		UserID:   userID,
		SentDate: today,
		SentAt:   s.now().UTC(),
		Status:   "sent",
	}
	// PAC-122 SF-082 (reporting half): a failed send and a failed record
	// reach error monitoring; what is sent and stored is unchanged.
	if sendErr != nil {
		rec.Status = "failed"
		log.Printf("daily recap send error user=%s: %v", userID, sendErr)
		lifecycle.Report(unit, fmt.Errorf("daily recap send error user=%s: %w", userID, sendErr))
	} else {
		rec.SlackMessageTS = ts
	}
	if err := insertRecapSend(s.DB, rec); err != nil {
		log.Printf("daily recap: record %s send for user=%s: %v", rec.Status, userID, err)
		lifecycle.Report(unit, fmt.Errorf("daily recap: record %s send for user=%s: %w", rec.Status, userID, err))
	}
}

// recapDue reports whether a recap with this zone and HH:MM send time is due
// at instant: whether instant, in the zone (UTC when the zone is not one),
// is that minute. It returns instant in that zone. A send time that is not
// HH:MM is the error.
func recapDue(instant time.Time, zone, sendTime string) (time.Time, bool, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		loc = time.UTC
	}
	now := instant.In(loc)
	var sendH, sendM int
	if _, err := fmt.Sscanf(sendTime, "%d:%d", &sendH, &sendM); err != nil {
		return now, false, err
	}
	return now, now.Hour() == sendH && now.Minute() == sendM, nil
}

// recapUserTimeout bounds one user's recap send (a test seam).
var recapUserTimeout = 55 * time.Second

// RunAll iterates all users with recap_enabled and calls RunForUser.
// Called every minute by the cron in main.go.
func (s *DailyRecapService) RunAll(ctx context.Context) {
	users, err := s.recapUsers(ctx)
	if err != nil {
		log.Printf("daily recap: list users error: %v", err)
		return
	}
	// PAC-136: only the users whose send time is this minute are run (by
	// the zone and send time the list read returned), so a failing read of
	// one user's settings or Slack connection is reported at that user's
	// send time, once, not every minute. A send time the gate cannot parse
	// is passed on, for RunForUser to report.
	now := s.now()
	due := users[:0]
	for _, u := range users {
		if _, ok, err := recapDue(now, u.zone, u.sendTime); ok || err != nil {
			due = append(due, u)
		}
	}
	for i, u := range due {
		if i > 0 {
			// Stagger sends to respect Slack rate limits; stop at shutdown
			// or when the run's own deadline ends (PAC-81 AC-A6).
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
				reportRecapUsersNotStarted(due[i:])
				return
			}
		}
		if lifecycle.Default().ShuttingDown() {
			return
		}
		id, name := u.id, u.name
		// Each per-user send is a tracked unit, awaited by the drain.
		// Its own context: the group's (cancelled at the end of the drain)
		// with its own limit, not the cron run's, which ends as soon as
		// RunAll returns, right after the last user's send starts.
		lifecycle.Go(lifecycle.Unit{Kind: "daily-recap", ID: id.String()}, func(work context.Context) {
			runCtx, cancel := context.WithTimeout(work, recapUserTimeout)
			defer cancel()
			s.runForUserAt(runCtx, id, name, now)
		})
	}
}

// reportRecapUsersNotStarted reports the due users whose recap the run's
// deadline ended before it started (PAC-136 review L2: they were dropped
// silently), by count and ids only.
func reportRecapUsersNotStarted(users []recapUser) {
	ids := make([]string, 0, len(users))
	for _, u := range users {
		ids = append(ids, u.id.String())
	}
	err := fmt.Errorf("daily recap: run deadline ended before %d due users started: %s", len(ids), strings.Join(ids, ","))
	log.Print(err)
	lifecycle.Report(lifecycle.Unit{Kind: "daily-recap", ID: "run"}, err)
}

type recapUser struct {
	id       uuid.UUID
	name     string
	zone     string
	sendTime string // HH:MM
}

// recapUsers returns the users who enabled the recap on their own settings
// (PAC-24: this joined every user to the shared row, so one user enabling it
// sent a recap to everyone).
func (s *DailyRecapService) recapUsers(ctx context.Context) ([]recapUser, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT u.id, u.name, COALESCE(st.timezone, ''), COALESCE(to_char(st.recap_send_time, 'HH24:MI'), '')
		FROM users u
		INNER JOIN settings st ON st.user_id = u.id
		WHERE st.recap_enabled = true`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []recapUser
	for rows.Next() {
		var u recapUser
		if err := rows.Scan(&u.id, &u.name, &u.zone, &u.sendTime); err != nil {
			continue
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// ── Message construction ──────────────────────────────────────────────────────

type slackBlock map[string]interface{}

// BuildMessage assembles the Slack Block Kit payload for a user's day. A
// failed read of the day's content is an error (PAC-122 SF-046): it was an
// empty section, and the recap said "a clear calendar today".
func (s *DailyRecapService) BuildMessage(ctx context.Context, userID uuid.UUID, userName string, now time.Time, settings *storage.Settings) ([]slackBlock, error) {
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
	todayEnd := todayStart.AddDate(0, 0, 1) // the calendar day: 23h or 25h on a DST change

	meetings, err := s.fetchMeetings(ctx, userID, todayStart, todayEnd)
	if err != nil {
		return nil, fmt.Errorf("read meetings: %w", err)
	}
	// Times are shown in the zone the recap is built for, the one in its header.
	focusBlocks, err := s.fetchFocusBlocks(userID, todayStart, now.Location())
	if err != nil {
		return nil, fmt.Errorf("read focus blocks: %w", err)
	}
	habits, err := s.fetchHabits(userID, todayStart, now.Location())
	if err != nil {
		return nil, fmt.Errorf("read habits: %w", err)
	}
	briefs, err := s.fetchBriefs(ctx, userID, meetings)
	if err != nil {
		return nil, fmt.Errorf("read briefs: %w", err)
	}

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

	return blocks, nil
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
// table and habits for the non-calendar parts. Every query is scoped to the
// user the recap is for: a recap shows nobody else's data.

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

// PAC-122 SF-046: each fetch returns its read error, a row that does not
// scan and the rows' own error, instead of a short or empty list.

func (s *DailyRecapService) fetchMeetings(_ context.Context, userID uuid.UUID, start, end time.Time) ([]recapMeeting, error) {
	rows, err := s.DB.Query(`
		SELECT calendar_event_id, brief_text
		FROM meeting_briefs
		WHERE user_id = $1
		  AND generated_at >= $2 AND generated_at < $3
		LIMIT 20`, userID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var meetings []recapMeeting
	for rows.Next() {
		var id, briefText string
		if err := rows.Scan(&id, &briefText); err != nil {
			return nil, err
		}
		meetings = append(meetings, recapMeeting{
			ID:    id,
			Title: "Meeting",
			Start: start.Format("15:04"),
			End:   end.Format("15:04"),
		})
	}
	return meetings, rows.Err()
}

// fetchFocusBlocks returns the blocks starting in [day, next local midnight).
// day is the recap's local midnight (SF-046: the day was cut with date_trunc
// in the database session's zone, not the recap's); the end is the next
// calendar day, not day+24h, so a DST change neither drops nor adds an hour.
func (s *DailyRecapService) fetchFocusBlocks(userID uuid.UUID, day time.Time, loc *time.Location) ([]recapFocusBlock, error) {
	rows, err := s.DB.Query(`
		SELECT start_time, end_time
		FROM focus_blocks
		WHERE user_id = $1
		  AND start_time >= $2 AND start_time < $3
		ORDER BY start_time`, userID, day, day.AddDate(0, 0, 1))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var blocks []recapFocusBlock
	for rows.Next() {
		var st, et time.Time
		if err := rows.Scan(&st, &et); err != nil {
			return nil, err
		}
		st, et = st.In(loc), et.In(loc) // never the host's zone
		dur := et.Sub(st)
		blocks = append(blocks, recapFocusBlock{
			Start:    st.Format("15:04"),
			End:      et.Format("15:04"),
			Duration: fmt.Sprintf("%.0fh", dur.Hours()),
		})
	}
	return blocks, rows.Err()
}

func (s *DailyRecapService) fetchHabits(userID uuid.UUID, day time.Time, loc *time.Location) ([]recapHabit, error) {
	rows, err := s.DB.Query(`
		SELECT h.title, ho.start_time, ho.end_time
		FROM habit_occurrences ho
		JOIN habits h ON h.id = ho.habit_id
		WHERE h.user_id = $1 AND ho.scheduled_date = $2 AND ho.status = 'scheduled'
		ORDER BY ho.start_time`, userID, day.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var habits []recapHabit
	for rows.Next() {
		var title string
		var st, et time.Time
		if err := rows.Scan(&title, &st, &et); err != nil {
			return nil, err
		}
		ws, we := st.In(loc).Format("15:04"), et.In(loc).Format("15:04")
		habits = append(habits, recapHabit{Emoji: "🎯", Title: title, WindowStart: ws, WindowEnd: we})
	}
	return habits, rows.Err()
}

// fetchBriefs reads each meeting's brief; a meeting with none has no entry,
// and any other failed read is an error.
func (s *DailyRecapService) fetchBriefs(_ context.Context, userID uuid.UUID, meetings []recapMeeting) (map[string]*storage.MeetingBrief, error) {
	result := map[string]*storage.MeetingBrief{}
	for _, m := range meetings {
		b, err := storage.GetMeetingBrief(s.DB, userID, m.ID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		result[m.ID] = b
	}
	return result, nil
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

	resp, err := outbound.ProviderClient().Do(req)
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
	resp, err := outbound.ProviderClient().Do(req)
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
