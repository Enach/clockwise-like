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
	"golang.org/x/oauth2"
)

type authHandlers struct {
	oauthConfig *oauth2.Config
	db          *sql.DB
}

func (h *authHandlers) startOAuth(w http.ResponseWriter, r *http.Request) {
	b := make([]byte, 16)
	rand.Read(b)
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

	if err := auth.SaveToken(h.db, token); err != nil {
		http.Error(w, "save token failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/?connected=true", http.StatusFound)
}

func (h *authHandlers) status(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	token, err := auth.TokenFromDB(h.db)
	if err != nil || token == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"connected": false, "email": ""})
		return
	}

	ts := auth.TokenSource(r.Context(), h.oauthConfig, token)
	email := fetchUserEmail(r.Context(), ts)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"connected": true,
		"email":     email,
	})
}

func (h *authHandlers) disconnect(w http.ResponseWriter, r *http.Request) {
	if err := auth.DeleteToken(h.db); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func fetchUserEmail(ctx context.Context, ts oauth2.TokenSource) string {
	token, err := ts.Token()
	if err != nil {
		return ""
	}
	client := oauth2.NewClient(ctx, oauth2.StaticTokenSource(token))
	client.Timeout = 5 * time.Second
	resp, err := client.Get("https://www.googleapis.com/oauth2/v2/userinfo")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var info struct {
		Email string `json:"email"`
	}
	json.NewDecoder(resp.Body).Decode(&info)
	return info.Email
}
