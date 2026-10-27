package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
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
	// Endpoint overrides Microsoft's authorization and token URLs when set;
	// the zero value means Microsoft's common endpoint. Tests point it at a
	// local fake provider.
	Endpoint oauth2.Endpoint
}

func (c *MicrosoftConfig) oauthConfig() *oauth2.Config {
	endpoint := microsoftEndpoint
	if c.Endpoint.AuthURL != "" {
		endpoint = c.Endpoint
	}
	return &oauth2.Config{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		RedirectURL:  c.RedirectURL,
		Scopes:       []string{"Calendars.ReadWrite", "User.Read", "offline_access"},
		Endpoint:     endpoint,
	}
}

// GetMicrosoftAuthURL builds Microsoft's consent URL. A non-empty loginHint
// (already checked with ValidLoginHint) is sent as login_hint (PAC-35).
func GetMicrosoftAuthURL(cfg *MicrosoftConfig, state, loginHint string) string {
	opts := append([]oauth2.AuthCodeOption{oauth2.AccessTypeOffline}, loginHintOption(loginHint)...)
	return cfg.oauthConfig().AuthCodeURL(state, opts...)
}

func ExchangeMicrosoftCode(ctx context.Context, cfg *MicrosoftConfig, code string) (*oauth2.Token, error) {
	return cfg.oauthConfig().Exchange(ctx, code)
}

type microsoftTokenRecord struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry"`
}

// SaveMicrosoftToken stores userID's Outlook credential on their own settings
// row and makes Outlook their calendar provider (PAC-24: it was one shared
// credential for every user).
func SaveMicrosoftToken(db *sql.DB, userID uuid.UUID, token *oauth2.Token) error {
	if userID == uuid.Nil {
		return fmt.Errorf("SaveMicrosoftToken: userID is required")
	}
	rec := microsoftTokenRecord{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		Expiry:       token.Expiry.UTC(),
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if _, err := db.Exec(`INSERT INTO settings (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, userID); err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE settings SET microsoft_tokens = $2, calendar_provider = 'outlook' WHERE user_id = $1`, userID, string(data))
	return err
}

// LoadMicrosoftToken returns userID's Outlook credential, or nil when they
// have none.
func LoadMicrosoftToken(db *sql.DB, userID uuid.UUID) (*oauth2.Token, error) {
	row := db.QueryRow(`SELECT microsoft_tokens FROM settings WHERE user_id = $1`, userID)
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
