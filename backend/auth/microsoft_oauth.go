package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"golang.org/x/oauth2"
)

var microsoftEndpoint = oauth2.Endpoint{
	AuthURL:  "https://login.microsoftonline.com/common/oauth2/v2.0/authorize",
	TokenURL: "https://login.microsoftonline.com/common/oauth2/v2.0/token",
}

type MicrosoftConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

func (c *MicrosoftConfig) oauthConfig() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		RedirectURL:  c.RedirectURL,
		Scopes:       []string{"Calendars.ReadWrite", "User.Read", "offline_access"},
		Endpoint:     microsoftEndpoint,
	}
}

func GetMicrosoftAuthURL(cfg *MicrosoftConfig, state string) string {
	return cfg.oauthConfig().AuthCodeURL(state, oauth2.AccessTypeOffline)
}

func ExchangeMicrosoftCode(ctx context.Context, cfg *MicrosoftConfig, code string) (*oauth2.Token, error) {
	return cfg.oauthConfig().Exchange(ctx, code)
}

type microsoftTokenRecord struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry"`
}

func SaveMicrosoftToken(db *sql.DB, token *oauth2.Token) error {
	rec := microsoftTokenRecord{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		Expiry:       token.Expiry.UTC(),
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE settings SET microsoft_tokens = $1, calendar_provider = 'outlook' WHERE id = 1`, string(data))
	return err
}

func LoadMicrosoftToken(db *sql.DB) (*oauth2.Token, error) {
	row := db.QueryRow(`SELECT microsoft_tokens FROM settings WHERE id = 1`)
	var raw sql.NullString
	if err := row.Scan(&raw); err != nil || !raw.Valid || raw.String == "" {
		return nil, nil
	}
	var rec microsoftTokenRecord
	if err := json.Unmarshal([]byte(raw.String), &rec); err != nil {
		return nil, err
	}
	return &oauth2.Token{
		AccessToken:  rec.AccessToken,
		RefreshToken: rec.RefreshToken,
		Expiry:       rec.Expiry,
	}, nil
}
