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
	UpdatedAt        time.Time `json:"updatedAt"`
}

func GetSettings(db *sql.DB) (*Settings, error) {
	row := db.QueryRow(`SELECT
		id, work_start, work_end, timezone,
		focus_min_block_minutes, focus_max_block_minutes, focus_daily_target_minutes,
		focus_label, focus_color, lunch_start, lunch_end, protect_lunch,
		buffer_before_minutes, buffer_after_minutes, compression_enabled,
		auto_schedule_enabled, auto_schedule_cron,
		llm_provider, llm_model, llm_api_key, llm_base_url,
		aws_region, aws_profile, bedrock_model,
		azure_endpoint, azure_deployment, azure_api_version,
		gcp_project, gcp_location, vertex_model,
		ollama_base_url, ollama_model,
		calendar_provider, COALESCE(microsoft_tokens,''), webcal_url, calendar_email,
		updated_at
		FROM settings WHERE id = 1`)

	s := &Settings{}
	err := row.Scan(
		&s.ID, &s.WorkStart, &s.WorkEnd, &s.Timezone,
		&s.FocusMinBlockMinutes, &s.FocusMaxBlockMinutes, &s.FocusDailyTargetMinutes,
		&s.FocusLabel, &s.FocusColor, &s.LunchStart, &s.LunchEnd, &s.ProtectLunch,
		&s.BufferBeforeMinutes, &s.BufferAfterMinutes, &s.CompressionEnabled,
		&s.AutoScheduleEnabled, &s.AutoScheduleCron,
		&s.LLMProvider, &s.LLMModel, &s.LLMAPIKey, &s.LLMBaseURL,
		&s.AWSRegion, &s.AWSProfile, &s.BedrockModel,
		&s.AzureEndpoint, &s.AzureDeployment, &s.AzureAPIVersion,
		&s.GCPProject, &s.GCPLocation, &s.VertexModel,
		&s.OllamaBaseURL, &s.OllamaModel,
		&s.CalendarProvider, &s.MicrosoftTokens, &s.WebcalURL, &s.CalendarEmail,
		&s.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return insertDefaultSettings(db)
	}
	if err != nil {
		return nil, err
	}
	return s, nil
}

func insertDefaultSettings(db *sql.DB) (*Settings, error) {
	_, err := db.Exec(`INSERT INTO settings (id) VALUES (1) ON CONFLICT (id) DO NOTHING`)
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
			llm_provider, llm_model, llm_api_key, llm_base_url,
			aws_region, aws_profile, bedrock_model,
			azure_endpoint, azure_deployment, azure_api_version,
			gcp_project, gcp_location, vertex_model,
			ollama_base_url, ollama_model,
			calendar_provider, webcal_url, calendar_email, updated_at
		) VALUES (1,$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,NOW())
		ON CONFLICT (id) DO UPDATE SET
			work_start=EXCLUDED.work_start, work_end=EXCLUDED.work_end,
			timezone=EXCLUDED.timezone,
			focus_min_block_minutes=EXCLUDED.focus_min_block_minutes,
			focus_max_block_minutes=EXCLUDED.focus_max_block_minutes,
			focus_daily_target_minutes=EXCLUDED.focus_daily_target_minutes,
			focus_label=EXCLUDED.focus_label, focus_color=EXCLUDED.focus_color,
			lunch_start=EXCLUDED.lunch_start, lunch_end=EXCLUDED.lunch_end,
			protect_lunch=EXCLUDED.protect_lunch,
			buffer_before_minutes=EXCLUDED.buffer_before_minutes,
			buffer_after_minutes=EXCLUDED.buffer_after_minutes,
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
			updated_at=NOW()`,
		s.WorkStart, s.WorkEnd, s.Timezone,
		s.FocusMinBlockMinutes, s.FocusMaxBlockMinutes, s.FocusDailyTargetMinutes,
		s.FocusLabel, s.FocusColor, s.LunchStart, s.LunchEnd, s.ProtectLunch,
		s.BufferBeforeMinutes, s.BufferAfterMinutes, s.CompressionEnabled,
		s.AutoScheduleEnabled, s.AutoScheduleCron,
		s.LLMProvider, s.LLMModel, s.LLMAPIKey, s.LLMBaseURL,
		s.AWSRegion, s.AWSProfile, s.BedrockModel,
		s.AzureEndpoint, s.AzureDeployment, s.AzureAPIVersion,
		s.GCPProject, s.GCPLocation, s.VertexModel,
		s.OllamaBaseURL, s.OllamaModel,
		s.CalendarProvider, s.WebcalURL, s.CalendarEmail,
	)
	return err
}
