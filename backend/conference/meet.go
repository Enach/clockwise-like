package conference

import (
	"context"
	"time"
)

// MeetProvider returns an empty placeholder — Google Meet links are provisioned
// automatically by the Google Calendar API when conferenceDataVersion=1 is passed
// on event creation. No out-of-band API call is needed.
type MeetProvider struct{}

func (m *MeetProvider) CreateMeeting(_ context.Context, _ string, _, _ time.Time) (*Details, error) {
	return &Details{Provider: "meet"}, nil
}
