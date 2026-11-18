package conference

import (
	"context"
	"time"
)

// Details holds the result of creating a conference meeting link.
type Details struct {
	Provider  string `json:"provider"`
	JoinURL   string `json:"joinUrl"`
	MeetingID string `json:"meetingId,omitempty"`
	Password  string `json:"password,omitempty"`
}

// Provider can create conference meeting links.
type Provider interface {
	CreateMeeting(ctx context.Context, title string, start, end time.Time) (*Details, error)
}

// MeetingDeleter is a Provider that can delete a meeting it created, by
// Details.MeetingID. A caller that could not attach a new meeting uses it so
// the meeting is not left orphaned at the provider (PAC-122 SF-069).
type MeetingDeleter interface {
	DeleteMeeting(ctx context.Context, meetingID string) error
}
