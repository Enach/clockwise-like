package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/conference"
	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"golang.org/x/oauth2"
)

// PAC-67: the operations this feature adds or modifies are implemented
// through the generated StrictServerInterface (factory section 4) and mounted
// through the generated wrapper. Only the methods below are ever mounted; the
// embedded interface is nil and stands for every operation that is still
// hand-written (contracts/openapi/MIGRATION.md).
type generatedServer struct {
	gen.StrictServerInterface
	db  *sql.DB
	ch  *conferencingHandlers
	sso *ssoHandlers // getSsoStepupOptions only (PAC-50)
	ah  *authHandlers // startGoogleConnect, startMicrosoftConnect (PAC-71)
	// orgAI serves the organisation AI operations (PAC-73 Part 2).
	orgAI *orgAIHandlers
}

type requestKey struct{}

// withRequest makes the *http.Request available to a strict handler, which
// otherwise sees only a context: server failures are logged and reported
// against the request (logServerError), exactly as hand-written handlers do.
func withRequest(f gen.StrictHandlerFunc, _ string) gen.StrictHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request interface{}) (interface{}, error) {
		return f(context.WithValue(ctx, requestKey{}, r), w, r, request)
	}
}

func requestFrom(ctx context.Context) *http.Request {
	r, _ := ctx.Value(requestKey{}).(*http.Request)
	return r
}

// strictOptions keeps the hand-written error bodies: a body that does not
// decode is the 400 "invalid body", and a response that cannot be written is
// the generic 500 envelope (PAC-21).
var strictOptions = gen.StrictHTTPServerOptions{
	RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, _ error) {
		writeError(w, "invalid body", http.StatusBadRequest)
	},
	ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
		writeServerError(w, r, "write response", err, http.StatusInternalServerError)
	},
}

func newGeneratedHandler(db *sql.DB, ch *conferencingHandlers) gen.ServerInterface {
	return gen.NewStrictHandlerWithOptions(&generatedServer{db: db, ch: ch}, []gen.StrictMiddlewareFunc{withRequest}, strictOptions)
}

// serverFailure logs a 5xx's detail and returns its fixed body text.
func serverFailure(ctx context.Context, op string, err error, code int) string {
	if r := requestFrom(ctx); r != nil {
		logServerError(r, op, err, code)
	}
	return genericServerMessage(code)
}

// The reconnect sentences of meeting creation (PAC-67, spec AC-8).
var (
	errTeamsConnectionLost = errors.New("teams: connection lost — reconnect Outlook in Settings (POST /api/auth/microsoft/connect)")
	errZoomConnectionLost  = errors.New("zoom: connection lost — reconnect at /api/auth/zoom")
)

// connectionLostSentence is the 400 sentence for a Teams or Zoom credential
// that cannot be renewed; ok is false for any other error. A Google loss is
// never answered here: on the on-event path it is the 404 (spec D-10).
func connectionLostSentence(err error) (string, bool) {
	var lost *auth.LostError
	if !errors.As(err, &lost) {
		return "", false
	}
	switch lost.Provider {
	case storage.CredentialOutlook:
		return errTeamsConnectionLost.Error(), true
	case storage.CredentialZoom:
		return errZoomConnectionLost.Error(), true
	}
	return "", false
}

// --- disconnectMicrosoft (POST /api/auth/microsoft/disconnect) ---------------

func (s *generatedServer) DisconnectMicrosoft(ctx context.Context, _ gen.DisconnectMicrosoftRequestObject) (gen.DisconnectMicrosoftResponseObject, error) {
	userID := auth.UserIDFromContext(ctx)
	removed, err := storage.RemoveMicrosoftCredentialCtx(ctx, s.db, userID)
	if err != nil {
		return gen.DisconnectMicrosoft500JSONResponse{Error: serverFailure(ctx, "disconnect microsoft", err, http.StatusInternalServerError)}, nil
	}
	if removed == "" {
		return gen.DisconnectMicrosoft200JSONResponse{Provider: gen.ProviderDisconnectResultProviderOutlook, Revocation: gen.NothingStored}, nil
	}
	// Nothing is sent to Microsoft (OQ-1, spec D-5): no single-grant revocation
	// exists that the user's own token may call.
	storage.WriteAuditLogCtx(ctx, s.db, userID, "provider_disconnected", disconnectDetails(storage.CredentialOutlook, gen.NotOffered))
	return gen.DisconnectMicrosoft200JSONResponse{Provider: gen.ProviderDisconnectResultProviderOutlook, Revocation: gen.NotOffered}, nil
}

// --- disconnectZoom (POST /api/conference/zoom/disconnect) --------------------

func (s *generatedServer) DisconnectZoom(ctx context.Context, _ gen.DisconnectZoomRequestObject) (gen.DisconnectZoomResponseObject, error) {
	userID := auth.UserIDFromContext(ctx)
	// (1) Local removal, atomic: whatever happens next, the credential is gone (D-2).
	removed, err := storage.RemoveZoomCredentialCtx(ctx, s.db, userID)
	if err != nil {
		return gen.DisconnectZoom500JSONResponse{Error: serverFailure(ctx, "disconnect zoom", err, http.StatusInternalServerError)}, nil
	}
	if removed == "" {
		return gen.DisconnectZoom200JSONResponse{Provider: gen.ProviderDisconnectResultProviderZoom, Revocation: gen.NothingStored}, nil
	}
	// (2) and (3): renew in memory when needed, then revoke.
	outcome := gen.Unconfirmed
	if revokeZoom(ctx, removed) {
		outcome = gen.Confirmed
	}
	// (4) Entries, best-effort, after the outcome is known.
	storage.WriteAuditLogCtx(ctx, s.db, userID, "provider_disconnected", disconnectDetails(storage.CredentialZoom, outcome))
	if outcome == gen.Unconfirmed {
		storage.WriteAuditLogCtx(ctx, s.db, userID, "revocation_unconfirmed", providerOnlyDetails(storage.CredentialZoom))
	}
	return gen.DisconnectZoom200JSONResponse{Provider: gen.ProviderDisconnectResultProviderZoom, Revocation: outcome}, nil
}

// revokeZoom asks Zoom to revoke the removed credential and reports whether
// Zoom confirmed it. An access token that is empty, expired or of unknown
// expiry is first renewed with the removed refresh token; the renewal is held
// in memory only and never stored, and its failure is not a loss (the
// credential no longer exists). An undecodable credential cannot be revoked.
func revokeZoom(ctx context.Context, removed string) bool {
	cred, err := storage.DecodeZoomCredential(removed)
	if err != nil {
		return false
	}
	clientID, secret := os.Getenv("ZOOM_CLIENT_ID"), os.Getenv("ZOOM_CLIENT_SECRET")
	access := cred.AccessToken
	if access == "" || cred.Expiry == nil || !time.Now().Add(time.Minute).Before(*cred.Expiry) {
		if cred.RefreshToken == "" || clientID == "" {
			return false
		}
		renewed, err := conference.ZoomOAuthConfig(clientID, secret).TokenSource(ctx, &oauth2.Token{RefreshToken: cred.RefreshToken}).Token()
		if err != nil {
			if r := requestFrom(ctx); r != nil {
				logServerError(r, "disconnect zoom: renew for revocation", err, http.StatusOK)
			}
			return false
		}
		access = renewed.AccessToken
	}
	if err := conference.RevokeZoomToken(ctx, clientID, secret, access); err != nil {
		if r := requestFrom(ctx); r != nil {
			logServerError(r, "disconnect zoom: revocation not confirmed", err, http.StatusOK)
		}
		return false
	}
	return true
}

func disconnectDetails(provider string, revocation gen.ProviderDisconnectResultRevocation) string {
	data, _ := json.Marshal(struct {
		Provider   string `json:"provider"`
		Revocation string `json:"revocation"`
	}{provider, string(revocation)})
	return string(data)
}

func providerOnlyDetails(provider string) string {
	data, _ := json.Marshal(struct {
		Provider string `json:"provider"`
	}{provider})
	return string(data)
}

// --- createConferenceLink (POST /api/conference/create) -----------------------

// ecmaSpace reports whether r is in ECMA-262's \s set (WhiteSpace and
// LineTerminator), the set the contract's `pattern: '\S'` uses. Neither Go's
// unicode.IsSpace (it adds U+0085 and lacks U+FEFF) nor regexp's ASCII-only \s
// is that set.
func ecmaSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', '\u00a0', '\u1680', '\u2028', '\u2029', '\u202f', '\u205f', '\u3000', '\ufeff':
		return true
	}
	return unicode.Is(unicode.Zs, r)
}

func validConferenceRequest(b *gen.CreateConferenceRequest) bool {
	if b == nil || strings.IndexFunc(b.Title, func(r rune) bool { return !ecmaSpace(r) }) < 0 {
		return false
	}
	return !b.Start.IsZero() && !b.End.IsZero() && b.End.After(b.Start)
}

func (s *generatedServer) CreateConferenceLink(ctx context.Context, req gen.CreateConferenceLinkRequestObject) (gen.CreateConferenceLinkResponseObject, error) {
	// API-052: validated before any credential is read or provider called.
	if !validConferenceRequest(req.Body) {
		return gen.CreateConferenceLink400JSONResponse{Error: "invalid body"}, nil
	}
	settings, err := storage.GetOrCreateSettingsByUserCtx(ctx, s.db, auth.UserIDFromContext(ctx))
	if err != nil {
		return gen.CreateConferenceLink500JSONResponse{Error: serverFailure(ctx, "settings", err, http.StatusInternalServerError)}, nil
	}
	var provider conference.Provider
	switch settings.ConferencingProvider {
	case "teams", "zoom":
		provider, err = s.ch.providerFactory.ProviderForRequest(ctx, settings.ConferencingProvider, settings)
	default:
		provider, err = conference.NewProvider(settings)
	}
	if err != nil {
		if msg, ok := conferenceRefusal(err, settings.ConferencingProvider); ok {
			return gen.CreateConferenceLink400JSONResponse{Error: msg}, nil
		}
		return gen.CreateConferenceLink500JSONResponse{Error: serverFailure(ctx, "conference provider", err, http.StatusInternalServerError)}, nil
	}
	details, err := provider.CreateMeeting(ctx, req.Body.Title, req.Body.Start, req.Body.End)
	if err != nil {
		if msg, ok := connectionLostSentence(err); ok {
			return gen.CreateConferenceLink400JSONResponse{Error: msg}, nil
		}
		return gen.CreateConferenceLink500JSONResponse{Error: serverFailure(ctx, "create meeting", err, http.StatusInternalServerError)}, nil
	}
	out := gen.CreateConferenceLink200JSONResponse{Provider: gen.ConferenceMeetingDetailsProvider(details.Provider), JoinUrl: details.JoinURL}
	if details.MeetingID != "" {
		out.MeetingId = &details.MeetingID
	}
	if details.Password != "" {
		out.Password = &details.Password
	}
	return out, nil
}

// --- addEventConference (POST /api/events/{id}/conference) --------------------

func eventLink(provider, url, label string) gen.AddEventConference200JSONResponse {
	out := gen.AddEventConference200JSONResponse{Provider: provider, Url: url}
	if label != "" {
		out.Label = &label
	}
	return out
}

func (s *generatedServer) AddEventConference(ctx context.Context, req gen.AddEventConferenceRequestObject) (gen.AddEventConferenceResponseObject, error) {
	h := s.ch
	eventID := req.Id
	requested := ""
	customURL := ""
	if req.Body != nil {
		requested = strings.ToLower(strings.TrimSpace(string(req.Body.Provider)))
		if req.Body.Url != nil {
			customURL = *req.Body.Url
		}
	}
	if requested == "" {
		return gen.AddEventConference400JSONResponse{Error: "provider is required"}, nil
	}

	// The event is read with the caller's Google credential first (spec §2.8):
	// a refused Google renewal is recorded by the token source and answered
	// with the 404 below, and Teams or Zoom is never reached (AC-23, D-10).
	client, err := h.loadEventClient(ctx)
	if err != nil {
		if errors.Is(err, engine.ErrCalendarNotConnected) {
			return gen.AddEventConference401JSONResponse{Error: gen.CalendarNotConnectedErrorError(calendarNotConnectedMessage)}, nil
		}
		return gen.AddEventConference500JSONResponse{Error: serverFailure(ctx, "addEventConference", err, http.StatusInternalServerError)}, nil
	}
	event, err := client.GetEvent(ctx, client.CurrentCalendarID(), eventID)
	if err != nil {
		return gen.AddEventConference404JSONResponse{Error: "event not found"}, nil
	}
	if provider, url := conferenceFromEvent(event); url != "" {
		if provider == requested || requested == "custom" {
			return eventLink(provider, url, provider), nil
		}
		return gen.AddEventConference409JSONResponse{Error: "event already has a conference"}, nil
	}

	if requested == "custom" {
		customURL = strings.TrimSpace(customURL)
		if customURL == "" {
			return gen.AddEventConference400JSONResponse{Error: "url is required for custom conferences"}, nil
		}
		setExternalConference(event, requested, customURL)
		if _, err := client.UpdateEvent(ctx, client.CurrentCalendarID(), eventID, event); err != nil {
			return gen.AddEventConference500JSONResponse{Error: serverFailure(ctx, "update failed", err, http.StatusInternalServerError)}, nil
		}
		return eventLink(requested, customURL, "Custom link"), nil
	}

	if requested == "google_meet" {
		updated, err := client.AddGoogleMeet(ctx, client.CurrentCalendarID(), eventID, event)
		if err != nil {
			return gen.AddEventConference500JSONResponse{Error: serverFailure(ctx, "create Google Meet", err, http.StatusInternalServerError)}, nil
		}
		url := updated.HangoutLink
		if url == "" {
			url = calendar.ConferenceEntryURL(updated)
		}
		if url == "" {
			return gen.AddEventConference502JSONResponse{Error: "Google Meet creation is still pending"}, nil
		}
		return eventLink(requested, url, "Google Meet"), nil
	}

	settings, err := storage.GetOrCreateSettingsByUserCtx(ctx, h.db, auth.UserIDFromContext(ctx))
	if err != nil {
		return gen.AddEventConference500JSONResponse{Error: serverFailure(ctx, "settings", err, http.StatusInternalServerError)}, nil
	}
	start, startErr := eventTime(event.Start)
	end, endErr := eventTime(event.End)
	if startErr != nil || endErr != nil {
		return gen.AddEventConference400JSONResponse{Error: "event has no time"}, nil
	}
	provider, err := h.providerFactory.ProviderForRequest(ctx, requested, settings)
	if err != nil {
		if msg, ok := conferenceRefusal(err, requested); ok {
			return gen.AddEventConference400JSONResponse{Error: msg}, nil
		}
		return gen.AddEventConference500JSONResponse{Error: serverFailure(ctx, "conference provider", err, http.StatusInternalServerError)}, nil
	}
	details, err := provider.CreateMeeting(ctx, event.Summary, start, end)
	if err != nil {
		if msg, ok := connectionLostSentence(err); ok {
			return gen.AddEventConference400JSONResponse{Error: msg}, nil
		}
		return gen.AddEventConference502JSONResponse{Error: serverFailure(ctx, "create meeting", err, http.StatusBadGateway)}, nil
	}
	if details == nil || details.JoinURL == "" {
		return gen.AddEventConference502JSONResponse{Error: "conference provider returned no join URL"}, nil
	}
	setExternalConference(event, requested, details.JoinURL)
	if _, err := client.UpdateEvent(ctx, client.CurrentCalendarID(), eventID, event); err != nil {
		return gen.AddEventConference500JSONResponse{Error: serverFailure(ctx, "update failed", err, http.StatusInternalServerError)}, nil
	}
	return eventLink(requested, details.JoinURL, strings.ToUpper(requested)), nil
}
