package auth

import (
	"context"
	"database/sql"

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

func GetAuthURL(config *oauth2.Config, state string) string {
	return config.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
}

func ExchangeCode(ctx context.Context, config *oauth2.Config, code string) (*oauth2.Token, error) {
	return config.Exchange(ctx, code)
}

func TokenFromDB(db *sql.DB) (*oauth2.Token, error) {
	return LoadToken(db)
}

func SaveToken(db *sql.DB, token *oauth2.Token) error {
	return UpsertToken(db, token)
}

func TokenSource(ctx context.Context, config *oauth2.Config, token *oauth2.Token) oauth2.TokenSource {
	return config.TokenSource(ctx, token)
}
