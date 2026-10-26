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
	ID            int64
	GoogleEventID string
	StartTime     time.Time
	EndTime       time.Time
	Date          string
	CreatedAt     time.Time
}

type AuditLog struct {
	ID        int64
	UserID    uuid.NullUUID // nil for entries recorded before PAC-23
	Action    string
	Details   string
	CreatedAt time.Time
}
