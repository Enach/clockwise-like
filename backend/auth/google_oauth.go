package auth

import (
	"context"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	googlecalendar "google.golang.org/api/calendar/v3"
)

func NewGoogleOAuthConfig(clientID, clientSecret, redirectURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Scopes: []string{
			googlecalendar.CalendarScope,
			"https://www.googleapis.com/auth/userinfo.email",
		},
		Endpoint: google.Endpoint,
	}
}

// GetAuthURL builds Google's consent URL. A non-empty loginHint (already
// checked with ValidLoginHint) is sent as login_hint (PAC-35, API-064).
func GetAuthURL(config *oauth2.Config, state, loginHint string) string {
	opts := append([]oauth2.AuthCodeOption{oauth2.AccessTypeOffline, oauth2.ApprovalForce}, loginHintOption(loginHint)...)
	return config.AuthCodeURL(state, opts...)
}

func ExchangeCode(ctx context.Context, config *oauth2.Config, code string) (*oauth2.Token, error) {
	return config.Exchange(ctx, code)
}

func TokenSource(ctx context.Context, config *oauth2.Config, token *oauth2.Token) oauth2.TokenSource {
	return config.TokenSource(ctx, token)
}
