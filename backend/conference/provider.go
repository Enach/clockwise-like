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
