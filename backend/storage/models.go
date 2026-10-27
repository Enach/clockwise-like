package storage

import (
	"time"

	"github.com/google/uuid"
)

type OAuthToken struct {
	ID           int64
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
	CalendarID   string
	UpdatedAt    time.Time
}

type FocusBlock struct {
	ID            int64     `json:"id"`
	GoogleEventID string    `json:"google_event_id"`
	StartTime     time.Time `json:"start_time"`
	EndTime       time.Time `json:"end_time"`
	Date          string    `json:"date"`
	CreatedAt     time.Time `json:"created_at"`
}

type AuditLog struct {
	ID        int64
	UserID    uuid.NullUUID // nil for entries recorded before PAC-23
	Action    string
	Details   string
	CreatedAt time.Time
}
