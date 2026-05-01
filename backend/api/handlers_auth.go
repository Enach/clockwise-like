package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/storage"
	"golang.org/x/oauth2"
)

type authHandlers struct {
	oauthConfig *oauth2.Config
	db          *sql.DB
	jwtSecret   string
	frontendURL string
}

func (h *authHandlers) startOAuth(w http.ResponseWriter, r *http.Request) {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	state := hex.EncodeToString(b)

	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		Path:     "/",
		MaxAge:   300,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, auth.GetAuthURL(h.oauthConfig, state), http.StatusFound)
}

func (h *authHandlers) callback(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie("oauth_state")
	if err != nil || stateCookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "oauth_state", MaxAge: -1, Path: "/"})

	token, err := auth.ExchangeCode(r.Context(), h.oauthConfig, r.URL.Query().Get("code"))
	if err != nil {
		http.Error(w, "token exchange failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Fetch user profile from Google
	info, err := fetchGoogleUserInfo(r.Context(), token, h.oauthConfig)
	if err != nil {
		http.Error(w, "userinfo failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Upsert user row
	user, err := storage.UpsertUser(h.db, info.Email, info.Name, info.Picture, "google", info.ID)
	if err != nil {
		http.Error(w, "upsert user failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Store OAuth token associated with user
	if err := auth.UpsertUserToken(h.db, user.ID, token); err != nil {
		http.Error(w, "save token failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Auto-associate with org based on email domain
	_ = storage.AssociateUserWithOrg(h.db, user.ID, info.Email)

	h.issueJWT(w, user)
	http.Redirect(w, r, h.frontendURL+"/auth/callback", http.StatusFound)
}

func (h *authHandlers) status(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	s, err := storage.GetSettings(h.db)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"connected": false, "provider": "google", "email": ""})
		return
	}

	provider := s.CalendarProvider
	if provider == "" {
		provider = "google"
	}

	switch provider {
	case "outlook":
		msToken, _ := auth.LoadMicrosoftToken(h.db)
		connected := msToken != nil && msToken.RefreshToken != ""
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"connected": connected,
			"provider":  "outlook",
			"email":     s.CalendarEmail,
		})
	case "webcal":
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"connected": s.WebcalURL != "",
			"provider":  "webcal",
			"email":     s.CalendarEmail,
		})
	default: // google
		token, err := auth.TokenFromDB(h.db)
		if err != nil || token == nil {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"connected": false, "provider": "google", "email": ""})
			return
		}
		ts := auth.TokenSource(r.Context(), h.oauthConfig, token)
		email := fetchUserEmail(r.Context(), ts)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"connected": true,
			"provider":  "google",
			"email":     email,
		})
	}
}

func (h *authHandlers) disconnect(w http.ResponseWriter, r *http.Request) {
	if err := auth.DeleteToken(h.db); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Clear auth cookie
	http.SetCookie(w, &http.Cookie{Name: "auth_token", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusOK)
}

func (h *authHandlers) issueJWT(w http.ResponseWriter, user *storage.User) {
	if h.jwtSecret == "" {
		return
	}
	jwtToken, err := auth.GenerateToken(user.ID, user.Email, h.jwtSecret)
	if err != nil {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "auth_token",
		Value:    jwtToken,
		Path:     "/",
		MaxAge:   7 * 24 * 3600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

type googleUserInfo struct {
	ID      string `json:"id"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
}

func fetchGoogleUserInfo(ctx context.Context, token *oauth2.Token, cfg *oauth2.Config) (*googleUserInfo, error) {
	ts := auth.TokenSource(ctx, cfg, token)
	t, err := ts.Token()
	if err != nil {
		return nil, err
	}
	client := oauth2.NewClient(ctx, oauth2.StaticTokenSource(t))
	client.Timeout = 5 * time.Second
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/oauth2/v2/userinfo", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var info googleUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}
	return &info, nil
}

func fetchUserEmail(ctx context.Context, ts oauth2.TokenSource) string {
	token, err := ts.Token()
	if err != nil {
		return ""
	}
	client := oauth2.NewClient(ctx, oauth2.StaticTokenSource(token))
	client.Timeout = 5 * time.Second
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/oauth2/v2/userinfo", nil)
	if err != nil {
		return ""
	}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var info struct {
		Email string `json:"email"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&info)
	return info.Email
}
