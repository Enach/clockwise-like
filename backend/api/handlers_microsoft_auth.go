package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/storage"
	"golang.org/x/oauth2"
)

func (h *authHandlers) startMicrosoftOAuth(w http.ResponseWriter, r *http.Request) {
	cfg := microsoftConfig()
	if cfg == nil {
		writeError(w, "Microsoft OAuth not configured", http.StatusServiceUnavailable)
		return
	}

	b := make([]byte, 16)
	rand.Read(b)
	state := hex.EncodeToString(b)

	http.SetCookie(w, &http.Cookie{
		Name:     "ms_oauth_state",
		Value:    state,
		Path:     "/",
		MaxAge:   300,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, auth.GetMicrosoftAuthURL(cfg, state), http.StatusFound)
}

func (h *authHandlers) microsoftCallback(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie("ms_oauth_state")
	if err != nil || stateCookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "ms_oauth_state", MaxAge: -1, Path: "/"})

	cfg := microsoftConfig()
	if cfg == nil {
		writeError(w, "Microsoft OAuth not configured", http.StatusServiceUnavailable)
		return
	}

	token, err := auth.ExchangeMicrosoftCode(r.Context(), cfg, r.URL.Query().Get("code"))
	if err != nil {
		http.Error(w, "token exchange failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Ensure settings row exists before updating
	if _, err := storage.GetSettings(h.db); err != nil {
		http.Error(w, "settings: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err := auth.SaveMicrosoftToken(h.db, token); err != nil {
		http.Error(w, "save token failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Fetch Microsoft user profile and upsert user
	if info, err := fetchMicrosoftUserInfo(r.Context(), token); err == nil && info.Email != "" {
		if user, err := storage.UpsertUser(h.db, info.Email, info.Name, "", "microsoft", info.ID); err == nil {
			h.issueJWT(w, user)
		}
	}

	http.Redirect(w, r, "/?connected=true&provider=outlook", http.StatusFound)
}

type msUserInfo struct {
	ID    string `json:"id"`
	Email string `json:"mail"`
	Name  string `json:"displayName"`
}

func fetchMicrosoftUserInfo(ctx context.Context, token *oauth2.Token) (*msUserInfo, error) {
	client := oauth2.NewClient(ctx, oauth2.StaticTokenSource(token))
	client.Timeout = 5 * time.Second
	resp, err := client.Get("https://graph.microsoft.com/v1.0/me?$select=id,mail,displayName")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var info msUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}
	return &info, nil
}

func microsoftConfig() *auth.MicrosoftConfig {
	clientID := os.Getenv("MICROSOFT_CLIENT_ID")
	clientSecret := os.Getenv("MICROSOFT_CLIENT_SECRET")
	redirectURL := os.Getenv("MICROSOFT_REDIRECT_URL")
	if clientID == "" {
		return nil
	}
	return &auth.MicrosoftConfig{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
	}
}
