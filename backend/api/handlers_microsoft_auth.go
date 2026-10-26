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
	_, _ = rand.Read(b)
	state := hex.EncodeToString(b)

	setCookie(w, &http.Cookie{
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
	setCookie(w, &http.Cookie{Name: "ms_oauth_state", MaxAge: -1, Path: "/"})

	cfg := microsoftConfig()
	if cfg == nil {
		writeError(w, "Microsoft OAuth not configured", http.StatusServiceUnavailable)
		return
	}

	token, err := auth.ExchangeMicrosoftCode(r.Context(), cfg, r.URL.Query().Get("code"))
	if err != nil {
		writeServerError(w, r, "token exchange failed", err, http.StatusInternalServerError)
		return
	}

	// The credential belongs to the user who signed in with it (PAC-24), so
	// the user is resolved first; without one there is nobody to attach it to.
	info, err := fetchMicrosoftUserInfo(r.Context(), token)
	if err != nil || info.Email == "" {
		logServerError(r, "could not read the Microsoft account", err, http.StatusBadGateway)
		writeError(w, "could not read the Microsoft account", http.StatusBadGateway)
		return
	}
	user, err := storage.UpsertUser(h.db, info.Email, info.Name, "", "microsoft", info.ID)
	if err != nil {
		logServerError(r, "save user failed", err, http.StatusInternalServerError)
		writeError(w, "save user failed", http.StatusInternalServerError)
		return
	}
	if err := auth.SaveMicrosoftToken(h.db, user.ID, token); err != nil {
		writeServerError(w, r, "save token failed", err, http.StatusInternalServerError)
		return
	}
	_ = storage.AssociateUserWithOrg(h.db, user.ID, info.Email)
	h.issueJWT(w, user)

	http.Redirect(w, r, h.frontendURL+"/auth/callback?provider=outlook", http.StatusFound)
}

type msUserInfo struct {
	ID    string `json:"id"`
	Email string `json:"mail"`
	Name  string `json:"displayName"`
}

func fetchMicrosoftUserInfo(ctx context.Context, token *oauth2.Token) (*msUserInfo, error) {
	client := oauth2.NewClient(ctx, oauth2.StaticTokenSource(token))
	client.Timeout = 5 * time.Second
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://graph.microsoft.com/v1.0/me?$select=id,mail,displayName", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
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
