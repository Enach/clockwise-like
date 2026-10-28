package conference

import (
	"errors"
	"fmt"

	"github.com/Enach/paceday/backend/storage"
)

// The refusals NewProvider returns for a Zoom configuration. Their text is a
// fixed sentence the HTTP layer may show the user (PAC-21): it never carries
// the decoder's or any other error's text.
var (
	ErrZoomNotConnected      = errors.New("zoom: not connected — visit /api/auth/zoom to authenticate")
	ErrZoomInvalidCredential = errors.New("zoom: stored credential is invalid — reconnect at /api/auth/zoom")
)

// NewProvider returns the conferencing provider configured in settings.
func NewProvider(s *storage.Settings) (Provider, error) {
	switch s.ConferencingProvider {
	case "zoom":
		if s.ZoomTokens == "" {
			return nil, ErrZoomNotConnected
		}
		tok, err := storage.DecodeZoomCredential(s.ZoomTokens)
		if err != nil {
			return nil, ErrZoomInvalidCredential
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
