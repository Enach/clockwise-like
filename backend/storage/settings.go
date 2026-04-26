package storage

import (
	"database/sql"
	"time"
)

type Settings struct {
	ID                      int64     `json:"-"`
	WorkStart               string    `json:"workStart"`
	WorkEnd                 string    `json:"workEnd"`
	Timezone                string    `json:"timezone"`
	FocusMinBlockMinutes    int       `json:"focusMinBlockMinutes"`
	FocusMaxBlockMinutes    int       `json:"focusMaxBlockMinutes"`
	FocusDailyTargetMinutes int       `json:"focusDailyTargetMinutes"`
	FocusLabel              string    `json:"focusLabel"`
	FocusColor              string    `json:"focusColor"`
	LunchStart              string    `json:"lunchStart"`
	LunchEnd                string    `json:"lunchEnd"`
	ProtectLunch            bool      `json:"protectLunch"`
	BufferBeforeMinutes     int       `json:"bufferBeforeMinutes"`
	BufferAfterMinutes      int       `json:"bufferAfterMinutes"`
	CompressionEnabled      bool      `json:"compressionEnabled"`
	AutoScheduleEnabled     bool      `json:"autoScheduleEnabled"`
	AutoScheduleCron        string    `json:"autoScheduleCron"`
	LLMProvider             string    `json:"llmProvider"`
	LLMModel                string    `json:"llmModel"`
	LLMAPIKey               string    `json:"llmApiKey"`
	LLMBaseURL              string    `json:"llmBaseUrl"`
	UpdatedAt               time.Time `json:"updatedAt"`
}

func GetSettings(db *sql.DB) (*Settings, error) {
	row := db.QueryRow(`SELECT
		id, work_start, work_end, timezone,
		focus_min_block_minutes, focus_max_block_minutes, focus_daily_target_minutes,
		focus_label, focus_color, lunch_start, lunch_end, protect_lunch,
		buffer_before_minutes, buffer_after_minutes, compression_enabled,
		auto_schedule_enabled, auto_schedule_cron,
		llm_provider, llm_model, llm_api_key, llm_base_url, updated_at
		FROM settings WHERE id = 1`)

	s := &Settings{}
	var protectLunch, compressionEnabled, autoScheduleEnabled int
	var updatedAt string

	err := row.Scan(
		&s.ID, &s.WorkStart, &s.WorkEnd, &s.Timezone,
		&s.FocusMinBlockMinutes, &s.FocusMaxBlockMinutes, &s.FocusDailyTargetMinutes,
		&s.FocusLabel, &s.FocusColor, &s.LunchStart, &s.LunchEnd, &protectLunch,
		&s.BufferBeforeMinutes, &s.BufferAfterMinutes, &compressionEnabled,
		&autoScheduleEnabled, &s.AutoScheduleCron,
		&s.LLMProvider, &s.LLMModel, &s.LLMAPIKey, &s.LLMBaseURL, &updatedAt,
	)
	if err == sql.ErrNoRows {
		return insertDefaultSettings(db)
	}
	if err != nil {
		return nil, err
	}

	s.ProtectLunch = protectLunch != 0
	s.CompressionEnabled = compressionEnabled != 0
	s.AutoScheduleEnabled = autoScheduleEnabled != 0
	s.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return s, nil
}

func insertDefaultSettings(db *sql.DB) (*Settings, error) {
	_, err := db.Exec(`INSERT OR IGNORE INTO settings (id) VALUES (1)`)
	if err != nil {
		return nil, err
	}
	return GetSettings(db)
}

func SaveSettings(db *sql.DB, s *Settings) error {
	_, err := db.Exec(`
		INSERT INTO settings (
			id, work_start, work_end, timezone,
			focus_min_block_minutes, focus_max_block_minutes, focus_daily_target_minutes,
			focus_label, focus_color, lunch_start, lunch_end, protect_lunch,
			buffer_before_minutes, buffer_after_minutes, compression_enabled,
			auto_schedule_enabled, auto_schedule_cron,
			llm_provider, llm_model, llm_api_key, llm_base_url, updated_at
		) VALUES (1,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			work_start=excluded.work_start, work_end=excluded.work_end,
			timezone=excluded.timezone,
			focus_min_block_minutes=excluded.focus_min_block_minutes,
			focus_max_block_minutes=excluded.focus_max_block_minutes,
			focus_daily_target_minutes=excluded.focus_daily_target_minutes,
			focus_label=excluded.focus_label, focus_color=excluded.focus_color,
			lunch_start=excluded.lunch_start, lunch_end=excluded.lunch_end,
			protect_lunch=excluded.protect_lunch,
			buffer_before_minutes=excluded.buffer_before_minutes,
			buffer_after_minutes=excluded.buffer_after_minutes,
			compression_enabled=excluded.compression_enabled,
			auto_schedule_enabled=excluded.auto_schedule_enabled,
			auto_schedule_cron=excluded.auto_schedule_cron,
			llm_provider=excluded.llm_provider, llm_model=excluded.llm_model,
			llm_api_key=excluded.llm_api_key, llm_base_url=excluded.llm_base_url,
			updated_at=excluded.updated_at`,
		s.WorkStart, s.WorkEnd, s.Timezone,
		s.FocusMinBlockMinutes, s.FocusMaxBlockMinutes, s.FocusDailyTargetMinutes,
		s.FocusLabel, s.FocusColor, s.LunchStart, s.LunchEnd, boolToInt(s.ProtectLunch),
		s.BufferBeforeMinutes, s.BufferAfterMinutes, boolToInt(s.CompressionEnabled),
		boolToInt(s.AutoScheduleEnabled), s.AutoScheduleCron,
		s.LLMProvider, s.LLMModel, s.LLMAPIKey, s.LLMBaseURL,
	)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
