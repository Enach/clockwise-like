package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/conference"
	"github.com/Enach/paceday/backend/storage"
	"golang.org/x/oauth2"
)

type conferencingHandlers struct {
	db          *sql.DB
	oauthConfig *oauth2.Config
}

// GET /api/auth/zoom
func (h *conferencingHandlers) startZoomOAuth(w http.ResponseWriter, r *http.Request) {
	clientID := os.Getenv("ZOOM_CLIENT_ID")
	if clientID == "" {
		writeError(w, "Zoom OAuth not configured", http.StatusServiceUnavailable)
		return
	}
	b := make([]byte, 16)
	rand.Read(b)
	state := hex.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{
		Name:     "zoom_oauth_state",
		Value:    state,
		Path:     "/",
		MaxAge:   300,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, conference.ZoomAuthURL(clientID, os.Getenv("ZOOM_REDIRECT_URL"), state), http.StatusFound)
}

// GET /api/auth/zoom/callback
func (h *conferencingHandlers) zoomCallback(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie("zoom_oauth_state")
	if err != nil || stateCookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "zoom_oauth_state", MaxAge: -1, Path: "/"})

	tok, err := conference.ZoomExchangeCode(
		r.Context(),
		os.Getenv("ZOOM_CLIENT_ID"),
		os.Getenv("ZOOM_CLIENT_SECRET"),
		os.Getenv("ZOOM_REDIRECT_URL"),
		r.URL.Query().Get("code"),
	)
	if err != nil {
		http.Error(w, "zoom token exchange: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := storage.SaveZoomTokens(h.db, tok.AccessToken, tok.RefreshToken); err != nil {
		http.Error(w, "save zoom token: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/?connected=true&provider=zoom", http.StatusFound)
}

// POST /api/conference/create
func (h *conferencingHandlers) createConference(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title string    `json:"title"`
		Start time.Time `json:"start"`
		End   time.Time `json:"end"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid body", http.StatusBadRequest)
		return
	}

	s, err := storage.GetSettings(h.db)
	if err != nil {
		writeError(w, "settings: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var provider conference.Provider
	if s.ConferencingProvider == "teams" {
		msTok, _ := auth.LoadMicrosoftToken(h.db)
		if msTok == nil {
			writeError(w, "teams: not connected — visit /api/auth/microsoft to authenticate", http.StatusBadRequest)
			return
		}
		provider = conference.NewTeamsProvider(msTok.AccessToken)
	} else {
		provider, err = conference.NewProvider(s)
		if err != nil {
			writeError(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	details, err := provider.CreateMeeting(r.Context(), req.Title, req.Start, req.End)
	if err != nil {
		writeError(w, "create meeting: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(details)
}
