package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
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

	http.Redirect(w, r, auth.GetAuthURL(h.oauthConfig, state, loginHintFrom(r)), http.StatusFound)
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

	if err := h.issueJWT(w, user); err != nil {
		h.signInFailed(w, r, "issue session", err)
		return
	}
	http.Redirect(w, r, h.frontendURL+"/auth/callback", http.StatusFound)
}

// signInFailurePath is the web app's sign-in failure state (PAC-35, API-011):
// a fixed literal that carries no cause, provider or detail.
const signInFailurePath = "/?error=auth_failed"

// errNoSigningSecret is why a session cannot be issued without a secret.
var errNoSigningSecret = errors.New("session signing secret is empty")

// signInFailed ends a sign-in callback on the app's failure state. The cause
// goes to the server log and Sentry only. The auth_token cookie is neither set
// nor expired, so a session the browser already holds is kept.
func (h *authHandlers) signInFailed(w http.ResponseWriter, r *http.Request, op string, err error) {
	logServerError(r, op, err, http.StatusFound)
	http.Redirect(w, r, h.frontendURL+signInFailurePath, http.StatusFound)
}

// loginHintFrom returns the request's login_hint when it passes the LoginHint
// rule, else "" (dropped, never a 400). Only the first value counts.
func loginHintFrom(r *http.Request) string {
	if v := r.URL.Query().Get("login_hint"); auth.ValidLoginHint(v) {
		return v
	}
	return ""
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
		// PAC-67: connected means usable — stored, renewable and not lost.
		msToken, _ := auth.LoadMicrosoftToken(h.db, userID)
		lost, _ := storage.IsCredentialLost(h.db, userID, storage.CredentialOutlook)
		connected := msToken != nil && msToken.RefreshToken != "" && !lost
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
		// PAC-67: a credential recorded as lost is kept (D-1) but not connected.
		lost, lostErr := storage.IsCredentialLost(h.db, userID, storage.CredentialGoogle)
		if err != nil || token == nil || lostErr != nil || lost {
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

// calendarDisconnectUnsupportedMessage is the fixed error of the 409
// CalendarDisconnectUnsupported (PAC-35, spec D-8).
const calendarDisconnectUnsupportedMessage = "disconnecting this kind of calendar is not available yet"

type calendarDisconnectUnsupported struct {
	Error    string `json:"error"`
	Provider string `json:"provider"`
}

// disconnect removes the caller's Google calendar credential. It never touches
// the session (PAC-35, API-061). It classifies the caller exactly as status
// does: outlook and webcal are answered 409 and nothing is deleted (D-8);
// anything else is Google. A settings read failure deletes nothing (D-9).
func (h *authHandlers) disconnect(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	s, err := storage.GetOrCreateSettingsByUser(h.db, userID)
	if err != nil {
		writeServerError(w, r, "disconnect: read settings", err, http.StatusInternalServerError)
		return
	}
	switch s.CalendarProvider {
	case "outlook", "webcal":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(calendarDisconnectUnsupported{Error: calendarDisconnectUnsupportedMessage, Provider: s.CalendarProvider})
		return
	}
	if err := auth.DeleteUserToken(h.db, userID); err != nil {
		writeServerError(w, r, "disconnect", err, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// disconnectMicrosoft removes the caller's Outlook (Microsoft) credential
// (PAC-67). It is implemented through the generated interface: see
// generatedServer.DisconnectMicrosoft.
func (h *authHandlers) disconnectMicrosoft(w http.ResponseWriter, r *http.Request) {
	newGeneratedHandler(h.db, nil).DisconnectMicrosoft(w, r)
}

// issueJWT sets the auth_token session cookie for user. It returns an error,
// and sets nothing, when the session cannot be issued (PAC-35, API-011).
func (h *authHandlers) issueJWT(w http.ResponseWriter, user *storage.User) error {
	if h.jwtSecret == "" {
		return errNoSigningSecret
	}
	jwtToken, err := generateSessionToken(user.ID, user.Email, h.jwtSecret)
	if err != nil {
		return err
	}
	setCookie(w, &http.Cookie{
		Name:     "auth_token",
		Value:    jwtToken,
		Path:     "/",
		MaxAge:   7 * 24 * 3600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// Test seams: the session-token generator and the Google profile URL.
var (
	generateSessionToken = auth.GenerateToken
	googleUserInfoURL    = "https://www.googleapis.com/oauth2/v2/userinfo"
)

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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleUserInfoURL, nil)
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
