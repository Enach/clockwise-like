package storage

import "time"

type OAuthToken struct {
	ID           int64
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
	CalendarID   string
	UpdatedAt    time.Time
}

type FocusBlock struct {
	ID            int64
	GoogleEventID string
	StartTime     time.Time
	EndTime       time.Time
	Date          string
	CreatedAt     time.Time
}

type AuditLog struct {
	ID        int64
	Action    string
	Details   string
	CreatedAt time.Time
}
