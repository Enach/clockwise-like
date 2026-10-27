package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	db              *sql.DB
	oauthConfig     *oauth2.Config
	loadEventClient func(ctx context.Context) (ConferenceEventClient, error)
	providerFactory ConferenceProviderFactory
}

type defaultConferenceProviderFactory struct{ db *sql.DB }

// The provider refusals a ConferenceProviderFactory returns. Together with
// conference.ErrZoomNotConnected and conference.ErrZoomInvalidCredential they
// are the closed list conferenceRefusal turns into a 400 (PAC-21).
var (
	errTeamsNotConnected             = errors.New("teams: not connected — visit /api/auth/microsoft to authenticate")
	errUnsupportedConferenceProvider = errors.New("unsupported conference provider")
)

func (f defaultConferenceProviderFactory) ProviderForRequest(ctx context.Context, providerName string, settings *storage.Settings) (conference.Provider, error) {
	switch providerName {
	case "zoom":
		copy := *settings
		copy.ConferencingProvider = "zoom"
		return conference.NewProvider(&copy)
	case "teams":
		msToken, tokenErr := auth.LoadMicrosoftToken(f.db, auth.UserIDFromContext(ctx))
		if tokenErr != nil || msToken == nil {
			return nil, errTeamsNotConnected
		}
		return conference.NewTeamsProvider(msToken.AccessToken), nil
	default:
		return nil, errUnsupportedConferenceProvider
	}
}

// conferenceRefusal maps a provider-construction error to the fixed 400
// sentence the contract lists for it, echoing only the request's own provider
// name. ok is false for any other error, which is a server failure.
func conferenceRefusal(err error, provider string) (msg string, ok bool) {
	switch {
	case errors.Is(err, conference.ErrZoomNotConnected):
		return conference.ErrZoomNotConnected.Error(), true
	case errors.Is(err, conference.ErrZoomInvalidCredential):
		return conference.ErrZoomInvalidCredential.Error(), true
	case errors.Is(err, errTeamsNotConnected):
		return errTeamsNotConnected.Error(), true
	case errors.Is(err, errUnsupportedConferenceProvider):
		return fmt.Sprintf("unsupported conference provider %q", provider), true
	}
	return "", false
}

func newConferencingHandlers(db *sql.DB, oauthConfig *oauth2.Config) *conferencingHandlers {
	h := &conferencingHandlers{db: db, oauthConfig: oauthConfig, providerFactory: defaultConferenceProviderFactory{db: db}}
	h.loadEventClient = func(ctx context.Context) (ConferenceEventClient, error) {
		eh := &eventHandlers{db: db, oauthConfig: oauthConfig}
		return eh.calClient(ctx)
	}
	return h
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
	setCookie(w, &http.Cookie{
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
	setCookie(w, &http.Cookie{Name: "zoom_oauth_state", MaxAge: -1, Path: "/"})

	tok, err := conference.ZoomExchangeCode(
		r.Context(), os.Getenv("ZOOM_CLIENT_ID"), os.Getenv("ZOOM_CLIENT_SECRET"),
		os.Getenv("ZOOM_REDIRECT_URL"), r.URL.Query().Get("code"),
	)
	if err != nil {
		writeServerError(w, r, "zoom token exchange", err, http.StatusInternalServerError)
		return
	}
	if err := storage.SaveZoomTokens(h.db, userIDFromCtx(r.Context()), tok.AccessToken, tok.RefreshToken); err != nil {
		writeServerError(w, r, "save zoom token", err, http.StatusInternalServerError)
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
	userID := userIDFromCtx(r.Context())
	s, err := storage.GetOrCreateSettingsByUser(h.db, userID)
	if err != nil {
		writeServerError(w, r, "settings", err, http.StatusInternalServerError)
		return
	}

	var provider conference.Provider
	if s.ConferencingProvider == "teams" {
		msTok, _ := auth.LoadMicrosoftToken(h.db, userID)
		if msTok == nil {
			writeError(w, "teams: not connected — visit /api/auth/microsoft to authenticate", http.StatusBadRequest)
			return
		}
		provider = conference.NewTeamsProvider(msTok.AccessToken)
	} else {
		provider, err = conference.NewProvider(s)
		if err != nil {
			msg, ok := conferenceRefusal(err, s.ConferencingProvider)
			if !ok {
				writeServerError(w, r, "conference provider", err, http.StatusInternalServerError)
				return
			}
			writeError(w, msg, http.StatusBadRequest)
			return
		}
	}
	details, err := provider.CreateMeeting(r.Context(), req.Title, req.Start, req.End)
	if err != nil {
		writeServerError(w, r, "create meeting", err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(details)
}

func (h *conferencingHandlers) providers(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	settings, err := storage.GetOrCreateSettingsByUser(h.db, userID)
	if err != nil {
		writeServerError(w, r, "settings", err, http.StatusInternalServerError)
		return
	}
	googleToken, _ := auth.LoadUserToken(h.db, userID)
	microsoftToken, _ := auth.LoadMicrosoftToken(h.db, userID)
	configured := integrationAvailabilitySnapshot()
	out := make([]conferenceProviderStatus, 0, 4)
	if configured["google"].Available {
		out = append(out, conferenceProviderStatus{Provider: "google_meet", Connected: googleToken != nil, Email: settings.CalendarEmail, AutoWith: "google"})
	}
	if configured["zoom"].Available {
		out = append(out, conferenceProviderStatus{Provider: "zoom", Connected: strings.TrimSpace(settings.ZoomTokens) != ""})
	}
	if configured["microsoft"].Available {
		out = append(out, conferenceProviderStatus{Provider: "teams", Connected: microsoftToken != nil, Enabled: microsoftToken != nil, AutoWith: "outlook"})
	}
	out = append(out, conferenceProviderStatus{Provider: "custom", Connected: true})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (h *conferencingHandlers) disconnectZoom(w http.ResponseWriter, r *http.Request) {
	if err := storage.ClearZoomTokens(h.db, userIDFromCtx(r.Context())); err != nil {
		writeServerError(w, r, "disconnect zoom", err, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// errEventHasNoTime is the one error eventTime returns: the event has no
// start or end, or Google's value does not parse. Its text is the fixed 400
// body (PAC-21: before, a parse failure returned time.Parse's text).
var errEventHasNoTime = errors.New("event has no time")

func eventTime(value *googlecalendar.EventDateTime) (time.Time, error) {
	if value == nil {
		return time.Time{}, errEventHasNoTime
	}
	if value.DateTime != "" {
		if t, err := time.Parse(time.RFC3339, value.DateTime); err == nil {
			return t, nil
		}
		return time.Time{}, errEventHasNoTime
	}
	if value.Date != "" {
		if t, err := time.ParseInLocation("2006-01-02", value.Date, time.UTC); err == nil {
			return t, nil
		}
		return time.Time{}, errEventHasNoTime
	}
	return time.Time{}, errEventHasNoTime
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

	client, err := h.loadEventClient(r.Context())
	if err != nil {
		writeCalendarFailure(w, r, "addEventConference", err)
		return
	}
	event, err := client.GetEvent(r.Context(), client.CurrentCalendarID(), eventID)
	if err != nil {
		writeError(w, "event not found", http.StatusNotFound)
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
		if _, err := client.UpdateEvent(r.Context(), client.CurrentCalendarID(), eventID, event); err != nil {
			writeServerError(w, r, "update failed", err, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(conferenceLinkResponse{Provider: req.Provider, URL: req.URL, Label: "Custom link"})
		return
	}

	if req.Provider == "google_meet" {
		updated, err := client.AddGoogleMeet(r.Context(), client.CurrentCalendarID(), eventID, event)
		if err != nil {
			writeServerError(w, r, "create Google Meet", err, http.StatusInternalServerError)
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

	settings, err := storage.GetOrCreateSettingsByUser(h.db, userIDFromCtx(r.Context()))
	if err != nil {
		writeServerError(w, r, "settings", err, http.StatusInternalServerError)
		return
	}
	start, startErr := eventTime(event.Start)
	end, endErr := eventTime(event.End)
	if startErr != nil || endErr != nil {
		writeError(w, "event has no time", http.StatusBadRequest)
		return
	}
	provider, err := h.providerFactory.ProviderForRequest(r.Context(), req.Provider, settings)
	if err != nil {
		msg, ok := conferenceRefusal(err, req.Provider)
		if !ok {
			writeServerError(w, r, "conference provider", err, http.StatusInternalServerError)
		return
	}
		writeError(w, msg, http.StatusBadRequest)
		return
	}
	details, err := provider.CreateMeeting(r.Context(), event.Summary, start, end)
	if err != nil {
		writeServerError(w, r, "create meeting", err, http.StatusBadGateway)
		return
	}
	if details == nil || details.JoinURL == "" {
		writeError(w, "conference provider returned no join URL", http.StatusBadGateway)
		return
	}
	setExternalConference(event, req.Provider, details.JoinURL)
	if _, err := client.UpdateEvent(r.Context(), client.CurrentCalendarID(), eventID, event); err != nil {
		writeServerError(w, r, "update failed", err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(conferenceLinkResponse{Provider: req.Provider, URL: details.JoinURL, Label: strings.ToUpper(req.Provider)})
}

func (h *conferencingHandlers) removeEventConference(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "id")
	client, err := h.loadEventClient(r.Context())
	if err != nil {
		writeCalendarFailure(w, r, "removeEventConference", err)
		return
	}
	event, err := client.GetEvent(r.Context(), client.CurrentCalendarID(), eventID)
	if err != nil {
		writeError(w, "event not found", http.StatusNotFound)
		return
	}

	if event.ConferenceData != nil || event.HangoutLink != "" {
		event, err = client.ClearGoogleMeet(r.Context(), client.CurrentCalendarID(), eventID, event)
		if err != nil {
			writeServerError(w, r, "remove Google Meet", err, http.StatusInternalServerError)
			return
		}
	}
	if removeExternalConference(event) {
		if _, err := client.UpdateEvent(r.Context(), client.CurrentCalendarID(), eventID, event); err != nil {
			writeServerError(w, r, "remove conference", err, http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
