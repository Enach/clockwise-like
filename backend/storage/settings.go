package storage

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type DaySchedule struct {
	Enabled bool   `json:"enabled"`
	Start   string `json:"start"`
	End     string `json:"end"`
}

type WorkingHoursSchedule struct {
	Mode    string                 `json:"mode"`
	Default DaySchedule            `json:"default"`
	Days    map[string]DaySchedule `json:"days"`
}

type LunchBreakSchedule map[string]DaySchedule

func (w *WorkingHoursSchedule) Scan(src any) error {
	if w == nil {
		return fmt.Errorf("WorkingHoursSchedule.Scan on nil receiver")
	}
	*w = WorkingHoursSchedule{}
	return scanJSONB(src, w)
}

func (w WorkingHoursSchedule) Value() (driver.Value, error) {
	return json.Marshal(w)
}

func (l *LunchBreakSchedule) Scan(src any) error {
	if l == nil {
		return fmt.Errorf("LunchBreakSchedule.Scan on nil receiver")
	}
	*l = LunchBreakSchedule{}
	return scanJSONB(src, l)
}

func (l LunchBreakSchedule) Value() (driver.Value, error) {
	if l == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(l)
}

func scanJSONB(src any, dst any) error {
	switch value := src.(type) {
	case nil:
		return nil
	case []byte:
		if len(value) == 0 {
			return nil
		}
		return json.Unmarshal(value, dst)
	case string:
		if value == "" {
			return nil
		}
		return json.Unmarshal([]byte(value), dst)
	default:
		return fmt.Errorf("cannot scan %T as JSONB", src)
	}
}

func defaultWorkingHours(workStart, workEnd string) WorkingHoursSchedule {
	if workStart == "" {
		workStart = "09:00"
	}
	if workEnd == "" {
		workEnd = "18:00"
	}
	return WorkingHoursSchedule{
		Mode: "all_days",
		Default: DaySchedule{
			Enabled: true,
			Start:   workStart,
			End:     workEnd,
		},
		Days: map[string]DaySchedule{},
	}
}

func (s *Settings) normalizeSchedules() {
	if s.WorkingHours.Mode == "" {
		s.WorkingHours = defaultWorkingHours(s.WorkStart, s.WorkEnd)
	} else if s.WorkingHours.Mode == "all_days" &&
		s.WorkingHours.Default.Start == "" && s.WorkingHours.Default.End == "" &&
		!s.WorkingHours.Default.Enabled {
		s.WorkingHours.Default = defaultWorkingHours(s.WorkStart, s.WorkEnd).Default
	}
	if s.WorkingHours.Days == nil {
		s.WorkingHours.Days = map[string]DaySchedule{}
	}
	if s.LunchBreaks == nil {
		s.LunchBreaks = LunchBreakSchedule{}
	}
}

// WorkWindow returns the effective working window for the local calendar day.
// The legacy global fields remain the fallback for settings written by older clients.
func (s *Settings) WorkWindow(day time.Time) (start, end string, enabled bool) {
	if s == nil {
		return "09:00", "18:00", true
	}
	copy := *s
	copy.normalizeSchedules()
	if strings.EqualFold(copy.WorkingHours.Mode, "by_day") {
		entry, ok := copy.WorkingHours.Days[strings.ToLower(day.Weekday().String())]
		if !ok {
			return "", "", false
		}
		return entry.Start, entry.End, entry.Enabled && entry.Start != "" && entry.End != ""
	}
	entry := copy.WorkingHours.Default
	if entry.Start == "" || entry.End == "" {
		entry = defaultWorkingHours(copy.WorkStart, copy.WorkEnd).Default
	}
	return entry.Start, entry.End, entry.Enabled && entry.Start != "" && entry.End != ""
}

// LunchWindow returns the effective lunch block for the local calendar day.
// A per-day entry overrides the legacy global lunch settings, including disabling it.
func (s *Settings) LunchWindow(day time.Time) (start, end string, enabled bool) {
	if s == nil {
		return "", "", false
	}
	copy := *s
	copy.normalizeSchedules()
	if entry, ok := copy.LunchBreaks[strings.ToLower(day.Weekday().String())]; ok {
		return entry.Start, entry.End, entry.Enabled && entry.Start != "" && entry.End != ""
	}
	return copy.LunchStart, copy.LunchEnd,
		copy.ProtectLunch && copy.LunchStart != "" && copy.LunchEnd != ""
}

type Settings struct {
	ID                      int64                `json:"-"`
	WorkStart               string               `json:"workStart"`
	WorkEnd                 string               `json:"workEnd"`
	Timezone                string               `json:"timezone"`
	FocusMinBlockMinutes    int                  `json:"focusMinBlockMinutes"`
	FocusMaxBlockMinutes    int                  `json:"focusMaxBlockMinutes"`
	FocusDailyTargetMinutes int                  `json:"focusDailyTargetMinutes"`
	OutOfHoursMeetingsPerWeek int                `json:"outOfHoursMeetingsPerWeek"`
	AutoDeclineOutsideWorkingHours bool           `json:"autoDeclineOutsideWorkingHours"`
	FocusLabel              string               `json:"focusLabel"`
	FocusColor              string               `json:"focusColor"`
	LunchStart              string               `json:"lunchStart"`
	LunchEnd                string               `json:"lunchEnd"`
	ProtectLunch            bool                 `json:"protectLunch"`
	BufferBeforeMinutes     int                  `json:"bufferBeforeMinutes"`
	BufferAfterMinutes      int                  `json:"bufferAfterMinutes"`
	BufferEnabled           bool                 `json:"bufferEnabled"`
	BufferMinMeetingMinutes int                  `json:"bufferMinMeetingMinutes"`
	BufferSkipBackToBack    bool                 `json:"bufferSkipBackToBack"`
	WorkingHours            WorkingHoursSchedule `json:"workingHours"`
	LunchBreaks             LunchBreakSchedule   `json:"lunchBreaks"`
	CompressionEnabled      bool                 `json:"compressionEnabled"`
	AutoScheduleEnabled     bool                 `json:"autoScheduleEnabled"`
	AutoScheduleCron        string               `json:"autoScheduleCron"`
	LLMProvider             string               `json:"llmProvider"`
	LLMModel                string               `json:"llmModel"`
	// LLMAPIKey is write-only (PAC-24): never encoded in a response. Writes go
	// through the settings handler, which reads it from the request itself.
	LLMAPIKey               string               `json:"-"`
	// LLMAPIKeySet reports whether a key is on file. Read-only; never stored.
	LLMAPIKeySet            bool                 `json:"llmApiKeySet"`
	LLMBaseURL              string               `json:"llmBaseUrl"`
	// AWS Bedrock
	AWSRegion    string `json:"awsRegion"`
	AWSProfile   string `json:"awsProfile"`
	BedrockModel string `json:"bedrockModel"`
	// Azure OpenAI
	AzureEndpoint   string `json:"azureEndpoint"`
	AzureDeployment string `json:"azureDeployment"`
	AzureAPIVersion string `json:"azureApiVersion"`
	// Google Vertex AI
	GCPProject  string `json:"gcpProject"`
	GCPLocation string `json:"gcpLocation"`
	VertexModel string `json:"vertexModel"`
	// Ollama
	OllamaBaseURL string `json:"ollamaBaseUrl"`
	OllamaModel   string `json:"ollamaModel"`
	// Calendar providers
	CalendarProvider string `json:"calendarProvider"`
	MicrosoftTokens  string `json:"-"` // JSON blob, not exposed in API
	WebcalURL        string `json:"webcalUrl"`
	CalendarEmail    string `json:"calendarEmail"`
	// Conferencing
	ConferencingProvider string `json:"conferencingProvider"`
	ZoomTokens           string `json:"-"` // JSON blob, not exposed in API
	// Daily recap
	RecapEnabled       bool      `json:"recapEnabled"`
	RecapSendTime      string    `json:"recapSendTime"` // "HH:MM"
	RecapSendTo        string    `json:"recapSendTo"`   // "dm" or "channel"
	RecapChannelID     string    `json:"recapChannelId"`
	RecapIncludeBriefs bool      `json:"recapIncludeBriefs"`
	RecapIncludeFocus  bool      `json:"recapIncludeFocus"`
	RecapIncludeHabits bool      `json:"recapIncludeHabits"`
	// TimeFormat is the user's time-of-day display: "12h" (default) or "24h" (PAC-55).
	TimeFormat         string    `json:"timeFormat"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

// settingsColumns is the one column list every settings read uses.
// recap_send_time is rendered HH:MM (PAC-24, U-19), never Postgres's HH:MM:SS.
const settingsColumns = `
		id, work_start, work_end, timezone,
		focus_min_block_minutes, focus_max_block_minutes, focus_daily_target_minutes,
		out_of_hours_meetings_per_week, auto_decline_outside_working_hours,
		focus_label, focus_color, lunch_start, lunch_end, protect_lunch,
		buffer_before_minutes, buffer_after_minutes,
		buffer_enabled, buffer_min_meeting_minutes, buffer_skip_back_to_back,
		compression_enabled, auto_schedule_enabled, auto_schedule_cron,
		llm_provider, llm_model, llm_api_key, llm_base_url,
		aws_region, aws_profile, bedrock_model,
		azure_endpoint, azure_deployment, azure_api_version,
		gcp_project, gcp_location, vertex_model,
		ollama_base_url, ollama_model,
		calendar_provider, COALESCE(microsoft_tokens,''), webcal_url, calendar_email,
		COALESCE(conferencing_provider,'meet'), COALESCE(zoom_tokens,''),
		recap_enabled, to_char(recap_send_time, 'HH24:MI'), COALESCE(recap_send_to,''),
		COALESCE(recap_channel_id,''), recap_include_briefs, recap_include_focus, recap_include_habits,
		COALESCE(working_hours, '{}'::jsonb), COALESCE(lunch_breaks, '{}'::jsonb),
		time_format,
		updated_at`

// GetOrCreateSettingsByUser returns userID's settings, creating the row with
// the product defaults the first time (PAC-24). Settings always belong to a
// user: there is no shared row and no way to read one without a user id.
func GetOrCreateSettingsByUser(db *sql.DB, userID uuid.UUID) (*Settings, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("GetOrCreateSettingsByUser: userID is required")
	}
	// ON CONFLICT makes two concurrent first reads safe.
	if _, err := db.Exec(`INSERT INTO settings (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, userID); err != nil {
		return nil, err
	}
	s := &Settings{}
	err := db.QueryRow(`SELECT`+settingsColumns+` FROM settings WHERE user_id = $1`, userID).Scan(
		&s.ID, &s.WorkStart, &s.WorkEnd, &s.Timezone,
		&s.FocusMinBlockMinutes, &s.FocusMaxBlockMinutes, &s.FocusDailyTargetMinutes,
		&s.OutOfHoursMeetingsPerWeek, &s.AutoDeclineOutsideWorkingHours,
		&s.FocusLabel, &s.FocusColor, &s.LunchStart, &s.LunchEnd, &s.ProtectLunch,
		&s.BufferBeforeMinutes, &s.BufferAfterMinutes,
		&s.BufferEnabled, &s.BufferMinMeetingMinutes, &s.BufferSkipBackToBack,
		&s.CompressionEnabled, &s.AutoScheduleEnabled, &s.AutoScheduleCron,
		&s.LLMProvider, &s.LLMModel, &s.LLMAPIKey, &s.LLMBaseURL,
		&s.AWSRegion, &s.AWSProfile, &s.BedrockModel,
		&s.AzureEndpoint, &s.AzureDeployment, &s.AzureAPIVersion,
		&s.GCPProject, &s.GCPLocation, &s.VertexModel,
		&s.OllamaBaseURL, &s.OllamaModel,
		&s.CalendarProvider, &s.MicrosoftTokens, &s.WebcalURL, &s.CalendarEmail,
		&s.ConferencingProvider, &s.ZoomTokens,
		&s.RecapEnabled, &s.RecapSendTime, &s.RecapSendTo,
		&s.RecapChannelID, &s.RecapIncludeBriefs, &s.RecapIncludeFocus, &s.RecapIncludeHabits,
		&s.WorkingHours, &s.LunchBreaks,
		&s.TimeFormat,
		&s.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	s.LLMAPIKeySet = s.LLMAPIKey != ""
	s.UpdatedAt = s.UpdatedAt.UTC()
	s.normalizeSchedules()
	return s, nil
}

// SaveSettingsForUser writes every user-editable column of userID's row and
// no other user's. The calendar and conferencing credentials
// (microsoft_tokens, zoom_tokens) are not written here: they have their own
// writers.
func SaveSettingsForUser(db *sql.DB, userID uuid.UUID, s *Settings) error {
	return SaveSettingsForUserAt(db, userID, s, time.Now())
}

// SaveSettingsForUserAt is SaveSettingsForUser with the save instant given.
//
// It never writes a watermark the caller supplies (PAC-49 §4.1): the
// auto-schedule watermark is set to now when this save turns auto-schedule on,
// or changes the schedule or the timezone while it stays on, and is otherwise
// left exactly as stored. The comparison is against the stored row inside the
// upsert, so a save built from a stale read cannot rewind it.
func SaveSettingsForUserAt(db *sql.DB, userID uuid.UUID, s *Settings, now time.Time) error {
	if userID == uuid.Nil {
		return fmt.Errorf("SaveSettingsForUser: userID is required")
	}
	s.normalizeSchedules()
	_, err := db.Exec(`
		INSERT INTO settings (
			user_id, work_start, work_end, timezone,
			focus_min_block_minutes, focus_max_block_minutes, focus_daily_target_minutes,
			focus_label, focus_color, lunch_start, lunch_end, protect_lunch,
			buffer_before_minutes, buffer_after_minutes,
			buffer_enabled, buffer_min_meeting_minutes, buffer_skip_back_to_back,
			compression_enabled, auto_schedule_enabled, auto_schedule_cron,
			llm_provider, llm_model, llm_api_key, llm_base_url,
			aws_region, aws_profile, bedrock_model,
			azure_endpoint, azure_deployment, azure_api_version,
			gcp_project, gcp_location, vertex_model,
			ollama_base_url, ollama_model,
			calendar_provider, webcal_url, calendar_email,
			conferencing_provider,
			recap_enabled, recap_send_time, recap_send_to, recap_channel_id,
			recap_include_briefs, recap_include_focus, recap_include_habits,
			out_of_hours_meetings_per_week, auto_decline_outside_working_hours,
			working_hours, lunch_breaks,
			time_format,
			auto_schedule_last_run,
			updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,
			$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38,$39,
			$40,$41,$42,$43,$44,$45,$46,$47,$48,$49,$50,$51,
			CASE WHEN $19::boolean THEN $52::timestamptz END,
			NOW()
		)
		ON CONFLICT (user_id) DO UPDATE SET
			work_start=EXCLUDED.work_start, work_end=EXCLUDED.work_end,
			timezone=EXCLUDED.timezone,
			focus_min_block_minutes=EXCLUDED.focus_min_block_minutes,
			focus_max_block_minutes=EXCLUDED.focus_max_block_minutes,
			focus_daily_target_minutes=EXCLUDED.focus_daily_target_minutes,
			out_of_hours_meetings_per_week=EXCLUDED.out_of_hours_meetings_per_week,
			auto_decline_outside_working_hours=EXCLUDED.auto_decline_outside_working_hours,
			focus_label=EXCLUDED.focus_label, focus_color=EXCLUDED.focus_color,
			lunch_start=EXCLUDED.lunch_start, lunch_end=EXCLUDED.lunch_end,
			protect_lunch=EXCLUDED.protect_lunch,
			buffer_before_minutes=EXCLUDED.buffer_before_minutes,
			buffer_after_minutes=EXCLUDED.buffer_after_minutes,
			buffer_enabled=EXCLUDED.buffer_enabled,
			buffer_min_meeting_minutes=EXCLUDED.buffer_min_meeting_minutes,
			buffer_skip_back_to_back=EXCLUDED.buffer_skip_back_to_back,
			compression_enabled=EXCLUDED.compression_enabled,
			auto_schedule_enabled=EXCLUDED.auto_schedule_enabled,
			auto_schedule_cron=EXCLUDED.auto_schedule_cron,
			llm_provider=EXCLUDED.llm_provider, llm_model=EXCLUDED.llm_model,
			llm_api_key=EXCLUDED.llm_api_key, llm_base_url=EXCLUDED.llm_base_url,
			aws_region=EXCLUDED.aws_region, aws_profile=EXCLUDED.aws_profile,
			bedrock_model=EXCLUDED.bedrock_model,
			azure_endpoint=EXCLUDED.azure_endpoint, azure_deployment=EXCLUDED.azure_deployment,
			azure_api_version=EXCLUDED.azure_api_version,
			gcp_project=EXCLUDED.gcp_project, gcp_location=EXCLUDED.gcp_location,
			vertex_model=EXCLUDED.vertex_model,
			ollama_base_url=EXCLUDED.ollama_base_url, ollama_model=EXCLUDED.ollama_model,
			calendar_provider=EXCLUDED.calendar_provider,
			webcal_url=EXCLUDED.webcal_url, calendar_email=EXCLUDED.calendar_email,
			conferencing_provider=EXCLUDED.conferencing_provider,
			recap_enabled=EXCLUDED.recap_enabled,
			recap_send_time=EXCLUDED.recap_send_time,
			recap_send_to=EXCLUDED.recap_send_to,
			recap_channel_id=EXCLUDED.recap_channel_id,
			recap_include_briefs=EXCLUDED.recap_include_briefs,
			recap_include_focus=EXCLUDED.recap_include_focus,
			recap_include_habits=EXCLUDED.recap_include_habits,
			working_hours=EXCLUDED.working_hours,
			lunch_breaks=EXCLUDED.lunch_breaks,
			time_format=EXCLUDED.time_format,
			auto_schedule_last_run = CASE
				WHEN EXCLUDED.auto_schedule_enabled AND (
					NOT settings.auto_schedule_enabled
					OR settings.auto_schedule_cron IS DISTINCT FROM EXCLUDED.auto_schedule_cron
					OR settings.timezone IS DISTINCT FROM EXCLUDED.timezone)
				THEN $52::timestamptz
				ELSE settings.auto_schedule_last_run
			END,
			updated_at=NOW()`,
		userID,
		s.WorkStart, s.WorkEnd, s.Timezone,
		s.FocusMinBlockMinutes, s.FocusMaxBlockMinutes, s.FocusDailyTargetMinutes,
		s.FocusLabel, s.FocusColor, s.LunchStart, s.LunchEnd, s.ProtectLunch,
		s.BufferBeforeMinutes, s.BufferAfterMinutes,
		s.BufferEnabled, s.BufferMinMeetingMinutes, s.BufferSkipBackToBack,
		s.CompressionEnabled, s.AutoScheduleEnabled, s.AutoScheduleCron,
		s.LLMProvider, s.LLMModel, s.LLMAPIKey, s.LLMBaseURL,
		s.AWSRegion, s.AWSProfile, s.BedrockModel,
		s.AzureEndpoint, s.AzureDeployment, s.AzureAPIVersion,
		s.GCPProject, s.GCPLocation, s.VertexModel,
		s.OllamaBaseURL, s.OllamaModel,
		s.CalendarProvider, s.WebcalURL, s.CalendarEmail,
		s.ConferencingProvider,
		s.RecapEnabled, recapSendTimeOrDefault(s.RecapSendTime), recapSendToOrDefault(s.RecapSendTo), s.RecapChannelID,
		s.RecapIncludeBriefs, s.RecapIncludeFocus, s.RecapIncludeHabits,
		s.OutOfHoursMeetingsPerWeek, s.AutoDeclineOutsideWorkingHours,
		s.WorkingHours, s.LunchBreaks,
		timeFormatOrDefault(s.TimeFormat),
		now,
	)
	return err
}

// SaveRecapSettingsForUser writes only the seven recap columns of userID's
// row (PAC-24): the recap PATCH no longer rewrites the rest of the row.
func SaveRecapSettingsForUser(db *sql.DB, userID uuid.UUID, s *Settings) error {
	if userID == uuid.Nil {
		return fmt.Errorf("SaveRecapSettingsForUser: userID is required")
	}
	_, err := db.Exec(`UPDATE settings SET
			recap_enabled = $2, recap_send_time = $3, recap_send_to = $4, recap_channel_id = $5,
			recap_include_briefs = $6, recap_include_focus = $7, recap_include_habits = $8,
			updated_at = NOW()
		WHERE user_id = $1`,
		userID, s.RecapEnabled, recapSendTimeOrDefault(s.RecapSendTime), recapSendToOrDefault(s.RecapSendTo),
		s.RecapChannelID, s.RecapIncludeBriefs, s.RecapIncludeFocus, s.RecapIncludeHabits)
	return err
}

// timeFormatOrDefault keeps a struct built in Go without the field (tests,
// fixtures) valid against the column's CHECK; the handler never sends "".
func timeFormatOrDefault(v string) string {
	if v == "" {
		return "12h"
	}
	return v
}

func recapSendTimeOrDefault(v string) string {
	if v == "" {
		return "08:00"
	}
	return v
}

func recapSendToOrDefault(v string) string {
	if v == "" {
		return "dm"
	}
	return v
}

// AutoScheduleCandidate is one user the focus scheduler considers on a tick
// (PAC-49 §4.2): a user whose own settings have auto-schedule on, with what
// the scheduler needs to decide whether this tick is their time.
type AutoScheduleCandidate struct {
	UserID   uuid.UUID
	CronExpr string
	Timezone string
	// LastRun is the watermark (auto_schedule_last_run): the user's next run
	// is their first slot after it. nil only after a direct database edit.
	LastRun *time.Time
}

// ListAutoScheduleCandidates returns exactly the users whose own settings row
// has auto_schedule_enabled, in a stable order (the row's creation order).
// Values are returned as stored; the scheduler validates them.
func ListAutoScheduleCandidates(db *sql.DB) ([]AutoScheduleCandidate, error) {
	rows, err := db.Query(`SELECT user_id, auto_schedule_cron, timezone, auto_schedule_last_run
		FROM settings
		WHERE auto_schedule_enabled = TRUE
		ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AutoScheduleCandidate, 0)
	for rows.Next() {
		var c AutoScheduleCandidate
		var last sql.NullTime
		if err := rows.Scan(&c.UserID, &c.CronExpr, &c.Timezone, &last); err != nil {
			return nil, err
		}
		if last.Valid {
			t := last.Time
			c.LastRun = &t
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetAutoScheduleLastRun returns userID's watermark, or nil when it is null or
// the user has no settings row.
func GetAutoScheduleLastRun(db *sql.DB, userID uuid.UUID) (*time.Time, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("GetAutoScheduleLastRun: userID is required")
	}
	var last sql.NullTime
	err := db.QueryRow(`SELECT auto_schedule_last_run FROM settings WHERE user_id = $1`, userID).Scan(&last)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !last.Valid) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t := last.Time
	return &t, nil
}

// AdvanceAutoScheduleWatermark moves userID's watermark to "to", but only if
// auto-schedule is still on and the watermark is still the one the caller
// observed (nil meaning null). It reports whether it did. The scheduler calls
// it before every run, so a save that disabled the feature or reset the
// watermark between the listing and the run wins, and no slot runs twice.
func AdvanceAutoScheduleWatermark(db *sql.DB, userID uuid.UUID, observed *time.Time, to time.Time) (bool, error) {
	if userID == uuid.Nil {
		return false, fmt.Errorf("AdvanceAutoScheduleWatermark: userID is required")
	}
	var obs sql.NullTime
	if observed != nil {
		obs = sql.NullTime{Time: *observed, Valid: true}
	}
	res, err := db.Exec(`UPDATE settings SET auto_schedule_last_run = $3
		WHERE user_id = $1
		  AND auto_schedule_enabled = TRUE
		  AND auto_schedule_last_run IS NOT DISTINCT FROM $2::timestamptz`,
		userID, obs, to)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// ListUsersWithAutoDecline returns users whose automatic outside-hours decline is enabled.
func ListUsersWithAutoDecline(db *sql.DB) ([]uuid.UUID, error) {
	rows, err := db.Query(`SELECT user_id FROM settings WHERE user_id IS NOT NULL AND auto_decline_outside_working_hours = TRUE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var userID uuid.UUID
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		out = append(out, userID)
	}
	return out, rows.Err()
}
