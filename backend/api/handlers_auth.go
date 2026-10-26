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

	setCookie(w, &http.Cookie{
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
	setCookie(w, &http.Cookie{Name: "oauth_state", MaxAge: -1, Path: "/"})

	token, err := auth.ExchangeCode(r.Context(), h.oauthConfig, r.URL.Query().Get("code"))
	if err != nil {
		writeServerError(w, r, "token exchange failed", err, http.StatusInternalServerError)
		return
	}

	// Fetch user profile from Google
	info, err := fetchGoogleUserInfo(r.Context(), token, h.oauthConfig)
	if err != nil {
		writeServerError(w, r, "userinfo failed", err, http.StatusInternalServerError)
		return
	}

	// Upsert user row
	user, err := storage.UpsertUser(h.db, info.Email, info.Name, info.Picture, "google", info.ID)
	if err != nil {
		writeServerError(w, r, "upsert user failed", err, http.StatusInternalServerError)
		return
	}

	// Store OAuth token associated with user
	if err := auth.UpsertUserToken(h.db, user.ID, token); err != nil {
		writeServerError(w, r, "save token failed", err, http.StatusInternalServerError)
		return
	}

	// Auto-associate with org based on email domain
	_ = storage.AssociateUserWithOrg(h.db, user.ID, info.Email)

	h.issueJWT(w, user)
	http.Redirect(w, r, h.frontendURL+"/auth/callback", http.StatusFound)
}

func (h *authHandlers) status(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	userID := userIDFromCtx(r.Context())
	s, err := storage.GetOrCreateSettingsByUser(h.db, userID)
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
		msToken, _ := auth.LoadMicrosoftToken(h.db, userID)
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
		token, err := auth.LoadUserToken(h.db, userID)
		if err != nil || token == nil {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"connected": false, "provider": "google", "email": ""})
			return
		}
		// The Google token is stored by the sign-in callback against the user it
		// upserted by that same Google email, so the connected account's email
		// is the user's own. Answer from it: this endpoint runs on every
		// dashboard load, and asking Google's userinfo endpoint each time sent
		// the user's access token outside the calendar seam (PAC-65).
		email := ""
		if u, err := storage.GetUserByID(h.db, userID); err == nil && u != nil {
			email = u.Email
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"connected": true,
			"provider":  "google",
			"email":     email,
		})
	}
}

func (h *authHandlers) disconnect(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	if err := auth.DeleteUserToken(h.db, userID); err != nil {
		writeServerError(w, r, "disconnect", err, http.StatusInternalServerError)
		return
	}
	// Clear auth cookie
	setCookie(w, &http.Cookie{Name: "auth_token", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}

func (h *authHandlers) issueJWT(w http.ResponseWriter, user *storage.User) {
	if h.jwtSecret == "" {
		return
	}
	jwtToken, err := auth.GenerateToken(user.ID, user.Email, h.jwtSecret)
	if err != nil {
		return
	}
	setCookie(w, &http.Cookie{
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
