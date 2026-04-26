package conference

import (
	"encoding/json"
	"fmt"

	"github.com/Enach/clockwise-like/backend/storage"
)

// NewProvider returns the conferencing provider configured in settings.
func NewProvider(s *storage.Settings) (Provider, error) {
	switch s.ConferencingProvider {
	case "zoom":
		if s.ZoomTokens == "" {
			return nil, fmt.Errorf("zoom: not connected — visit /api/auth/zoom to authenticate")
		}
		var tok ZoomTokens
		if err := json.Unmarshal([]byte(s.ZoomTokens), &tok); err != nil {
			return nil, fmt.Errorf("zoom: invalid token: %w", err)
		}
		return &ZoomProvider{AccessToken: tok.AccessToken}, nil
	case "teams":
		// Teams uses the Microsoft OAuth token stored separately; caller must inject it.
		return nil, fmt.Errorf("teams: use NewTeamsProvider(accessToken) directly")
	default: // "meet"
		return &MeetProvider{}, nil
	}
}

func NewTeamsProvider(accessToken string) Provider {
	return &TeamsProvider{AccessToken: accessToken}
}
