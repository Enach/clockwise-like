package storage

import "time"

type Settings struct {
	ID                      int64
	WorkStart               string
	WorkEnd                 string
	Timezone                string
	FocusMinBlockMinutes    int
	FocusMaxBlockMinutes    int
	FocusDailyTargetMinutes int
	FocusLabel              string
	FocusColor              string
	LunchStart              string
	LunchEnd                string
	ProtectLunch            bool
	BufferBeforeMinutes     int
	BufferAfterMinutes      int
	CompressionEnabled      bool
	AutoScheduleEnabled     bool
	AutoScheduleCron        string
	LLMProvider             string
	LLMModel                string
	LLMAPIKey               string
	LLMBaseURL              string
	UpdatedAt               time.Time
}

type OAuthToken struct {
	ID           int64
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
	CalendarID   string
	UpdatedAt    time.Time
}

type FocusBlock struct {
	ID             int64
	GoogleEventID  string
	StartTime      time.Time
	EndTime        time.Time
	Date           string
	CreatedAt      time.Time
}

type AuditLog struct {
	ID        int64
	Action    string
	Details   string
	CreatedAt time.Time
}
