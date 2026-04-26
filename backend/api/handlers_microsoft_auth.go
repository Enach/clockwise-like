package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"

	"github.com/Enach/clockwise-like/backend/auth"
	"github.com/Enach/clockwise-like/backend/storage"
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

	http.Redirect(w, r, "/?connected=true&provider=outlook", http.StatusFound)
}

func (h *authHandlers) statusWithProvider(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	s, err := storage.GetSettings(h.db)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"connected": false, "provider": "google", "email": ""})
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
		json.NewEncoder(w).Encode(map[string]interface{}{
			"connected": connected,
			"provider":  "outlook",
			"email":     s.CalendarEmail,
		})
	case "webcal":
		json.NewEncoder(w).Encode(map[string]interface{}{
			"connected": s.WebcalURL != "",
			"provider":  "webcal",
			"email":     s.CalendarEmail,
		})
	default: // google
		token, err := auth.TokenFromDB(h.db)
		if err != nil || token == nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"connected": false, "provider": "google", "email": ""})
			return
		}
		ts := auth.TokenSource(r.Context(), h.oauthConfig, token)
		email := fetchUserEmail(r.Context(), ts)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"connected": true,
			"provider":  "google",
			"email":     email,
		})
	}
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
