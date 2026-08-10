package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/conference"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"golang.org/x/oauth2"
	googlecalendar "google.golang.org/api/calendar/v3"
)

type conferencingHandlers struct {
	db          *sql.DB
	oauthConfig *oauth2.Config
}

type conferenceProviderStatus struct {
	Provider  string `json:"provider"`
	Connected bool   `json:"connected"`
	Email     string `json:"email,omitempty"`
	Enabled   bool   `json:"enabled,omitempty"`
	AutoWith  string `json:"auto_with,omitempty"`
}

type conferenceLinkResponse struct {
	Provider string `json:"provider"`
	URL      string `json:"url"`
	Label    string `json:"label,omitempty"`
}

const (
	conferenceProviderKey = "paceday_conference_provider"
	conferenceURLKey      = "paceday_conference_url"
)

// GET /api/auth/zoom
func (h *conferencingHandlers) startZoomOAuth(w http.ResponseWriter, r *http.Request) {
	clientID := os.Getenv("ZOOM_CLIENT_ID")
	if clientID == "" {
		writeError(w, "Zoom OAuth not configured", http.StatusServiceUnavailable)
		return
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	state := hex.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{
		Name: "zoom_oauth_state", Value: state, Path: "/", MaxAge: 300,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
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
		r.Context(), os.Getenv("ZOOM_CLIENT_ID"), os.Getenv("ZOOM_CLIENT_SECRET"),
		os.Getenv("ZOOM_REDIRECT_URL"), r.URL.Query().Get("code"),
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
	_ = json.NewEncoder(w).Encode(details)
}

func (h *conferencingHandlers) providers(w http.ResponseWriter, r *http.Request) {
	settings, err := storage.GetSettings(h.db)
	if err != nil {
		writeError(w, "settings: "+err.Error(), http.StatusInternalServerError)
		return
	}
	userID := userIDFromCtx(r.Context())
	googleToken, _ := auth.LoadUserToken(h.db, userID)
	microsoftToken, _ := auth.LoadMicrosoftToken(h.db)
	out := []conferenceProviderStatus{
		{Provider: "google_meet", Connected: googleToken != nil, Email: settings.CalendarEmail, AutoWith: "google"},
		{Provider: "zoom", Connected: strings.TrimSpace(settings.ZoomTokens) != ""},
		{Provider: "teams", Connected: microsoftToken != nil, Enabled: microsoftToken != nil, AutoWith: "outlook"},
		{Provider: "custom", Connected: true},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (h *conferencingHandlers) disconnectZoom(w http.ResponseWriter, r *http.Request) {
	if err := storage.ClearZoomTokens(h.db); err != nil {
		writeError(w, "disconnect zoom: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func eventTime(value *googlecalendar.EventDateTime) (time.Time, error) {
	if value == nil {
		return time.Time{}, fmt.Errorf("event has no time")
	}
	if value.DateTime != "" {
		return time.Parse(time.RFC3339, value.DateTime)
	}
	if value.Date != "" {
		return time.ParseInLocation("2006-01-02", value.Date, time.UTC)
	}
	return time.Time{}, fmt.Errorf("event has no time")
}

func conferenceFromEvent(event *googlecalendar.Event) (string, string) {
	if event == nil {
		return "", ""
	}
	if event.HangoutLink != "" {
		return "google_meet", event.HangoutLink
	}
	if url := calendar.ConferenceEntryURL(event); url != "" {
		return "google_meet", url
	}
	if event.ExtendedProperties != nil {
		private := event.ExtendedProperties.Private
		return private[conferenceProviderKey], private[conferenceURLKey]
	}
	return "", ""
}

func setExternalConference(event *googlecalendar.Event, provider, url string) {
	if event.ExtendedProperties == nil {
		event.ExtendedProperties = &googlecalendar.EventExtendedProperties{}
	}
	if event.ExtendedProperties.Private == nil {
		event.ExtendedProperties.Private = map[string]string{}
	}
	event.ExtendedProperties.Private[conferenceProviderKey] = provider
	event.ExtendedProperties.Private[conferenceURLKey] = url
}

func removeExternalConference(event *googlecalendar.Event) bool {
	if event == nil || event.ExtendedProperties == nil || event.ExtendedProperties.Private == nil {
		return false
	}
	private := event.ExtendedProperties.Private
	_, hadProvider := private[conferenceProviderKey]
	_, hadURL := private[conferenceURLKey]
	delete(private, conferenceProviderKey)
	delete(private, conferenceURLKey)
	if len(private) == 0 {
		event.ExtendedProperties = nil
		event.NullFields = append(event.NullFields, "ExtendedProperties")
	}
	return hadProvider || hadURL
}

func (h *conferencingHandlers) addEventConference(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "id")
	var req struct {
		Provider string `json:"provider"`
		URL      string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid body", http.StatusBadRequest)
		return
	}
	req.Provider = strings.ToLower(strings.TrimSpace(req.Provider))
	if req.Provider == "" {
		writeError(w, "provider is required", http.StatusBadRequest)
		return
	}

	eh := &eventHandlers{db: h.db, oauthConfig: h.oauthConfig}
	client, err := eh.calClient(r.Context())
	if err != nil {
		writeError(w, err.Error(), http.StatusUnauthorized)
		return
	}
	event, err := client.GetEvent(r.Context(), client.CalendarID, eventID)
	if err != nil {
		writeError(w, "event not found: "+err.Error(), http.StatusNotFound)
		return
	}
	if provider, url := conferenceFromEvent(event); url != "" {
		if provider == req.Provider || req.Provider == "custom" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(conferenceLinkResponse{Provider: provider, URL: url, Label: provider})
			return
		}
		writeError(w, "event already has a conference", http.StatusConflict)
		return
	}

	if req.Provider == "custom" {
		req.URL = strings.TrimSpace(req.URL)
		if req.URL == "" {
			writeError(w, "url is required for custom conferences", http.StatusBadRequest)
			return
		}
		setExternalConference(event, req.Provider, req.URL)
		if _, err := client.UpdateEvent(r.Context(), client.CalendarID, eventID, event); err != nil {
			writeError(w, "update failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(conferenceLinkResponse{Provider: req.Provider, URL: req.URL, Label: "Custom link"})
		return
	}

	if req.Provider == "google_meet" {
		updated, err := client.AddGoogleMeet(r.Context(), client.CalendarID, eventID, event)
		if err != nil {
			writeError(w, "create Google Meet: "+err.Error(), http.StatusInternalServerError)
			return
		}
		url := updated.HangoutLink
		if url == "" {
			url = calendar.ConferenceEntryURL(updated)
		}
		if url == "" {
			writeError(w, "Google Meet creation is still pending", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(conferenceLinkResponse{Provider: req.Provider, URL: url, Label: "Google Meet"})
		return
	}

	settings, err := storage.GetSettings(h.db)
	if err != nil {
		writeError(w, "settings: "+err.Error(), http.StatusInternalServerError)
		return
	}
	start, err := eventTime(event.Start)
	if err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}
	end, err := eventTime(event.End)
	if err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}
	var provider conference.Provider
	switch req.Provider {
	case "zoom":
		copy := *settings
		copy.ConferencingProvider = "zoom"
		provider, err = conference.NewProvider(&copy)
	case "teams":
		msToken, tokenErr := auth.LoadMicrosoftToken(h.db)
		if tokenErr != nil || msToken == nil {
			err = fmt.Errorf("teams: not connected — visit /api/auth/microsoft to authenticate")
		} else {
			provider = conference.NewTeamsProvider(msToken.AccessToken)
		}
	default:
		err = fmt.Errorf("unsupported conference provider %q", req.Provider)
	}
	if err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}
	details, err := provider.CreateMeeting(r.Context(), event.Summary, start, end)
	if err != nil {
		writeError(w, "create meeting: "+err.Error(), http.StatusBadGateway)
		return
	}
	if details == nil || details.JoinURL == "" {
		writeError(w, "conference provider returned no join URL", http.StatusBadGateway)
		return
	}
	setExternalConference(event, req.Provider, details.JoinURL)
	if _, err := client.UpdateEvent(r.Context(), client.CalendarID, eventID, event); err != nil {
		writeError(w, "update failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(conferenceLinkResponse{Provider: req.Provider, URL: details.JoinURL, Label: strings.ToUpper(req.Provider)})
}

func (h *conferencingHandlers) removeEventConference(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "id")
	eh := &eventHandlers{db: h.db, oauthConfig: h.oauthConfig}
	client, err := eh.calClient(r.Context())
	if err != nil {
		writeError(w, err.Error(), http.StatusUnauthorized)
		return
	}
	event, err := client.GetEvent(r.Context(), client.CalendarID, eventID)
	if err != nil {
		writeError(w, "event not found: "+err.Error(), http.StatusNotFound)
		return
	}

	if event.ConferenceData != nil || event.HangoutLink != "" {
		event, err = client.ClearGoogleMeet(r.Context(), client.CalendarID, eventID, event)
		if err != nil {
			writeError(w, "remove Google Meet: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if removeExternalConference(event) {
		if _, err := client.UpdateEvent(r.Context(), client.CalendarID, eventID, event); err != nil {
			writeError(w, "remove conference: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
