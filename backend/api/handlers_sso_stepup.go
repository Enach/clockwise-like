package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/config"
	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// PAC-50 (spec docs/specs/PAC-50.md §4.2–§4.6): who may write SSO
// configuration. A write needs either a live admin step-up grant (a fresh
// Google or Microsoft sign-in, checked for administrator status over the
// caller's org domain) or the deployment's break-glass list with a fresh
// session.

const (
	// EnvAdminEmails is the break-glass list (spec §4.5), read once at startup.
	EnvAdminEmails = "PACEDAY_ADMIN_EMAILS"

	stepupStartPath    = "/api/admin/sso/stepup"
	stepupCallbackPath = "/api/admin/sso/stepup/callback"
	stepupCookie       = "sso_stepup_state"
	stepupCookieTTL    = 10 * time.Minute
	// breakGlassFreshness is how recent a break-glass session must be (§4.5).
	breakGlassFreshness = 10 * time.Minute
)

// The callback's denial reasons (spec §4.6). The same values go into the
// redirect and into the SSO event table.
const (
	reasonStateMismatch = "state_mismatch"
	reasonTokenInvalid  = "token_invalid"
	reasonNoHD          = "no_hd"
	reasonHDMismatch    = "hd_mismatch"
	reasonTIDMismatch   = "tid_mismatch"
	reasonTIDChanged    = "tid_changed"
	reasonEmailMismatch = "email_mismatch"
	reasonNotAdmin      = "not_admin"
)

// ssoStore is the part of storage the SSO write and step-up paths go
// through. It exists so a test can make the store fail after the gate (spec
// AC-3d, D-4).
type ssoStore interface {
	getProvider(ctx context.Context, domain string) (*storage.SSOProvider, error)
	saveProvider(ctx context.Context, w storage.OIDCProviderWrite, actor, org uuid.UUID, via string) (*storage.SSOProvider, error)
	deleteProvider(ctx context.Context, domain string, actor, org uuid.UUID, via string) (bool, error)
	recordStepup(ctx context.Context, o storage.SSOStepupOutcome) (*storage.SSOStepupGrant, error)
	consumeGrant(ctx context.Context, user, org uuid.UUID, now time.Time) (bool, error)
}

type dbSSOStore struct{ ah *authHandlers }

func (s dbSSOStore) getProvider(_ context.Context, d string) (*storage.SSOProvider, error) {
	return storage.GetSSOProviderByDomainAnyState(s.ah.db, d)
}

func (s dbSSOStore) saveProvider(ctx context.Context, w storage.OIDCProviderWrite, actor, org uuid.UUID, via string) (*storage.SSOProvider, error) {
	return storage.SaveOIDCProviderRecorded(ctx, s.ah.db, w, actor, org, via)
}

func (s dbSSOStore) deleteProvider(ctx context.Context, d string, actor, org uuid.UUID, via string) (bool, error) {
	return storage.DeleteSSOProviderRecorded(ctx, s.ah.db, d, actor, org, via)
}

func (s dbSSOStore) recordStepup(ctx context.Context, o storage.SSOStepupOutcome) (*storage.SSOStepupGrant, error) {
	return storage.RecordSSOStepupOutcome(ctx, s.ah.db, o)
}

func (s dbSSOStore) consumeGrant(ctx context.Context, user, org uuid.UUID, now time.Time) (bool, error) {
	// An SSO configuration write consumes only an sso_write grant (contract
	// revision 8.1, N5).
	_, ok, err := storage.ConsumeSSOStepupGrant(ctx, s.ah.db, user, org, storage.SSOGrantPurposeSSOWrite, now)
	return ok, err
}

// stepupRandRead is the state nonce's entropy source; a test replaces it to
// reach the start's 500.
var stepupRandRead = rand.Read

type stepupConfig struct {
	google, microsoft *auth.StepupProvider
	stateKey          []byte
}

func (c stepupConfig) provider(name string) *auth.StepupProvider {
	switch name {
	case auth.StepupGoogle:
		return c.google
	case auth.StepupMicrosoft:
		return c.microsoft
	}
	return nil
}

type ssoHandlers struct {
	ah         *authHandlers
	clock      engine.Clock
	breakGlass map[string]bool
	stepup     stepupConfig
	store      ssoStore
}

// ssoOptions is everything newSSOHandlers needs that is not a handler.
type ssoOptions struct {
	Clock      engine.Clock
	BreakGlass string               // raw PACEDAY_ADMIN_EMAILS: comma-separated
	Google     *auth.StepupProvider // nil: not configured (start answers 503)
	Microsoft  *auth.StepupProvider
}

func newSSOHandlers(ah *authHandlers, o ssoOptions) *ssoHandlers {
	if o.Clock == nil {
		o.Clock = engine.SystemClock
	}
	bg := make(map[string]bool)
	for _, e := range strings.Split(o.BreakGlass, ",") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			bg[e] = true
		}
	}
	// The state is signed with a key derived from the session secret, under
	// its own label so the two are never interchangeable.
	key := sha256.Sum256([]byte("paceday/sso-stepup-state/v1\x00" + ah.jwtSecret))
	h := &ssoHandlers{
		ah: ah, clock: o.Clock, breakGlass: bg,
		stepup: stepupConfig{
			google: o.Google, microsoft: o.Microsoft,
			stateKey: key[:],
		},
	}
	h.store = dbSSOStore{ah: ah}
	return h
}

// stepupEndpoints is the identity-provider decision for this process (see
// ConfigureSSOStepup). nil means "never configured": production addresses.
var stepupEndpoints atomic.Pointer[[2]config.IdPEndpoint]

// ConfigureSSOStepup installs the identity-provider decision. main.go calls it
// once at startup, before RegisterRoutes, in every deployment.
func ConfigureSSOStepup(google, microsoft config.IdPEndpoint) {
	stepupEndpoints.Store(&[2]config.IdPEndpoint{google, microsoft})
}

// ssoOptionsFromEnv reads the step-up configuration once, at route
// registration (startup). A provider is configured when its OAuth client id
// and secret and APP_URL are set; the step-up reuses the sign-in client
// registration with its own scopes and its own redirect URI, which must be
// registered with the provider.
func ssoOptionsFromEnv() ssoOptions {
	eps := [2]config.IdPEndpoint{config.ProductionGoogleIdP(), config.ProductionMicrosoftIdP()}
	if p := stepupEndpoints.Load(); p != nil {
		eps = *p
	}
	appURL := strings.TrimRight(strings.TrimSpace(os.Getenv("APP_URL")), "/")
	// With APP_URL unset no provider is configured, so every start is D-2's
	// 503 (spec §9 D-9): the callback's redirect_uri cannot be built.
	o := ssoOptions{BreakGlass: os.Getenv(EnvAdminEmails)}
	if appURL != "" {
		redirect := appURL + stepupCallbackPath
		if id, sec := os.Getenv("GOOGLE_CLIENT_ID"), os.Getenv("GOOGLE_CLIENT_SECRET"); id != "" && sec != "" {
			o.Google = auth.NewGoogleStepup(id, sec, redirect, eps[0])
		}
		if id, sec := os.Getenv("MICROSOFT_CLIENT_ID"), os.Getenv("MICROSOFT_CLIENT_SECRET"); id != "" && sec != "" {
			o.Microsoft = auth.NewMicrosoftStepup(id, sec, redirect, eps[1])
		}
	}
	return o
}

// mountAdmin mounts /api/admin/sso. The caller has already put the group
// behind requireAuth.
func (h *ssoHandlers) mountAdmin(r chi.Router) {
	r.Post("/", h.createSSOProvider)
	r.Get("/", h.listSSOProviders)
	r.Get("/stepup", h.startStepup)
	r.Get("/stepup/options", h.getStepupOptions)
	r.Get("/stepup/callback", h.stepupCallback)
	r.Delete("/{domain}", h.deleteSSOProvider)
}

// --- the write gate (spec §4.2) ----------------------------------------------

type ssoCaller struct {
	user *storage.User
	org  *storage.Org
}

// callerOrg is gate step 2, membership: it runs before anything else,
// break-glass included, and its 403 carries no stepup_url.
func (h *ssoHandlers) callerOrg(w http.ResponseWriter, r *http.Request) (*ssoCaller, bool) {
	user, err := storage.GetUserByID(h.ah.db, userIDFromCtx(r.Context()))
	if err != nil || user == nil || user.OrgID == nil {
		writeError(w, "org membership required", http.StatusForbidden)
		return nil, false
	}
	org, err := storage.GetOrgByID(h.ah.db, *user.OrgID)
	if err != nil || org == nil {
		writeError(w, "org not found", http.StatusForbidden)
		return nil, false
	}
	return &ssoCaller{user: user, org: org}, true
}

// authorizeWrite is gate step 5. It is the last step before the write: the
// grant is consumed here, so every 400 has already happened and a failure
// after this leaves the grant used (AC-3c, AC-3d). It returns how the write
// was authorized ("stepup" or "breakglass").
func (h *ssoHandlers) authorizeWrite(w http.ResponseWriter, r *http.Request, c *ssoCaller) (string, bool) {
	now := h.clock.Now()
	ok, err := h.store.consumeGrant(r.Context(), c.user.ID, c.org.ID, now)
	if err != nil {
		writeServerError(w, r, "sso grant check", err, http.StatusInternalServerError)
		return "", false
	}
	if ok {
		return "stepup", true
	}
	if h.breakGlass[strings.ToLower(sessionEmail(r.Context()))] {
		iat := sessionIssuedAt(r.Context())
		age := now.Sub(iat)
		// A token stamped in the future (beyond a minute of skew) is not fresh.
		if !iat.IsZero() && age < breakGlassFreshness && age > -time.Minute {
			return "breakglass", true
		}
		writeError(w, "sso_write_requires_fresh_login", http.StatusForbidden)
		return "", false
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(h.stepupRequired())
	return "", false
}

// stepupRequiredBody is SsoWriteStepupRequired (contract revision 6).
type stepupRequiredBody struct {
	Error         string              `json:"error"`
	StepupURL     string              `json:"stepup_url"`
	StepupScopes  map[string][]string `json:"stepup_scopes"`
	WithoutStepup []string            `json:"without_stepup"`
}

// stepupRequired is the step-up 403 (owner, 2026-10-27, spec §9 D-11): what
// the optional step-up would request from each provider configured on this
// deployment (a key only for a provider whose start would not answer 503)
// and what the caller cannot do without it, so the client can explain both
// before navigating anywhere.
func (h *ssoHandlers) stepupRequired() stepupRequiredBody {
	scopes := make(map[string][]string, 2)
	for _, p := range []*auth.StepupProvider{h.stepup.google, h.stepup.microsoft} {
		if p != nil {
			scopes[p.Name] = p.ExtraScopes()
		}
	}
	return stepupRequiredBody{
		Error:         "sso_write_requires_admin_stepup",
		StepupURL:     stepupStartPath,
		StepupScopes:  scopes,
		WithoutStepup: []string{withoutStepupSSOWrites},
	}
}

// --- GET /api/admin/sso/stepup/options ------------------------------------------

// getStepupOptions answers getSsoStepupOptions through the generated strict
// wrapper (factory §4; contracts/openapi/MIGRATION.md).
func (h *ssoHandlers) getStepupOptions(w http.ResponseWriter, r *http.Request) {
	gen.NewStrictHandlerWithOptions(&generatedServer{db: h.ah.db, sso: h},
		[]gen.StrictMiddlewareFunc{withRequest}, strictOptions).GetSsoStepupOptions(w, r)
}

// GetSsoStepupOptions is getSsoStepupOptions (contract revision 8, spec §9
// D-13, AC-26): the step-up 403's stepup_url, stepup_scopes and
// without_stepup, built by the same function, for a page with no write to
// make. Read-only: no grant is consulted, no cookie set, nothing recorded,
// and the answer does not depend on the caller beyond membership.
func (s *generatedServer) GetSsoStepupOptions(ctx context.Context, _ gen.GetSsoStepupOptionsRequestObject) (gen.GetSsoStepupOptionsResponseObject, error) {
	user, err := storage.GetUserByID(s.db, auth.UserIDFromContext(ctx))
	if err != nil || user == nil || user.OrgID == nil {
		return gen.GetSsoStepupOptions403JSONResponse{Error: gen.OrgMembershipRequired}, nil
	}
	if org, err := storage.GetOrgByID(s.db, *user.OrgID); err != nil || org == nil {
		return gen.GetSsoStepupOptions403JSONResponse{Error: gen.OrgNotFound}, nil
	}
	b := s.sso.stepupRequired()
	out := gen.SsoStepupOptions{
		StepupUrl:     gen.SsoStepupUrl(b.StepupURL),
		WithoutStepup: b.WithoutStepup,
	}
	if v, ok := b.StepupScopes[auth.StepupGoogle]; ok {
		out.StepupScopes.Google = &v
	}
	if v, ok := b.StepupScopes[auth.StepupMicrosoft]; ok {
		out.StepupScopes.Microsoft = &v
	}
	return gen.GetSsoStepupOptions200JSONResponse(out), nil
}

// withoutStepupSSOWrites is the one without_stepup code the server sends
// today: no SSO configuration write except by a break-glass operator.
const withoutStepupSSOWrites = "sso_configuration_writes"

// --- the state cookie ----------------------------------------------------------

// stepupState is what the state cookie binds: the caller, their org, the
// provider, a nonce (also the OAuth state and the ID token's nonce) and an
// expiry. It is signed, so it cannot be forged or moved to another user.
type stepupState struct {
	UserID   uuid.UUID `json:"u"`
	OrgID    uuid.UUID `json:"o"`
	Provider string    `json:"p"`
	Nonce    string    `json:"n"`
	Expires  int64     `json:"e"`
}

func (h *ssoHandlers) signState(s stepupState) string {
	payload, _ := json.Marshal(s)
	p := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, h.stepup.stateKey)
	mac.Write([]byte(p))
	return p + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// readState returns the cookie's state when its signature is valid (so the
// provider it names can be recorded), and ok only when every binding holds.
func (h *ssoHandlers) readState(r *http.Request, user *storage.User) (*stepupState, bool) {
	ck, err := r.Cookie(stepupCookie)
	if err != nil {
		return nil, false
	}
	p, sig, found := strings.Cut(ck.Value, ".")
	if !found {
		return nil, false
	}
	want := hmac.New(sha256.New, h.stepup.stateKey)
	want.Write([]byte(p))
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, want.Sum(nil)) {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return nil, false
	}
	var s stepupState
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false
	}
	q := r.URL.Query().Get("state")
	ok := s.UserID == user.ID &&
		user.OrgID != nil && s.OrgID == *user.OrgID &&
		h.clock.Now().Unix() < s.Expires &&
		s.Nonce != "" && hmac.Equal([]byte(q), []byte(s.Nonce))
	return &s, ok
}

// setStateCookie sets or clears the state cookie through the PAC-20 helper,
// so Secure is the deployment's decision (config.DecideSecureCookies; spec
// §9 D-8). A negative maxAge clears it (Max-Age=0 on the wire).
func (h *ssoHandlers) setStateCookie(w http.ResponseWriter, value string, maxAge int) {
	setCookie(w, &http.Cookie{
		Name:     stepupCookie,
		Value:    value,
		Path:     stepupStartPath,
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// --- GET /api/admin/sso/stepup -------------------------------------------------

func (h *ssoHandlers) startStepup(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("provider")
	if name == "" {
		// The contract's text/plain 400 for a missing or empty provider: the
		// body the generated request wrapper's default handler writes for a
		// RequiredParamError (spec §9 D-9). A fixed message, no error text.
		http.Error(w, "Query argument provider is required, but not found", http.StatusBadRequest)
		return
	}
	if name != auth.StepupGoogle && name != auth.StepupMicrosoft {
		writeError(w, "provider must be 'google' or 'microsoft'", http.StatusBadRequest)
		return
	}
	user, err := storage.GetUserByID(h.ah.db, userIDFromCtx(r.Context()))
	if err != nil || user == nil || user.OrgID == nil {
		// Spec Decisions, D-1: no org, no org domain to step up for.
		writeError(w, "org membership required", http.StatusForbidden)
		return
	}
	org, err := storage.GetOrgByID(h.ah.db, *user.OrgID)
	if err != nil || org == nil {
		writeError(w, "org membership required", http.StatusForbidden)
		return
	}
	p := h.stepup.provider(name)
	if p == nil {
		// Spec Decisions, D-2.
		// Literal messages: PAC-21 forbids a computed message on a 5xx.
		if name == auth.StepupGoogle {
			writeError(w, "google step-up not configured", http.StatusServiceUnavailable)
		} else {
			writeError(w, "microsoft step-up not configured", http.StatusServiceUnavailable)
		}
		return
	}
	b := make([]byte, 24)
	if _, err := stepupRandRead(b); err != nil {
		writeServerError(w, r, "sso step-up state", err, http.StatusInternalServerError)
		return
	}
	nonce := hex.EncodeToString(b)
	h.setStateCookie(w, h.signState(stepupState{
		UserID: user.ID, OrgID: org.ID, Provider: name, Nonce: nonce,
		Expires: h.clock.Now().Add(stepupCookieTTL).Unix(),
	}), int(stepupCookieTTL/time.Second))
	email := sessionEmail(r.Context())
	if email == "" {
		email = user.Email
	}
	http.Redirect(w, r, p.AuthURL(nonce, nonce, email, org.Domain), http.StatusFound)
}

// --- GET /api/admin/sso/stepup/callback ------------------------------------------

func (h *ssoHandlers) stepupCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, err := storage.GetUserByID(h.ah.db, userIDFromCtx(ctx))
	if err != nil || user == nil {
		// requireAuth answers a missing session; this is a session whose user
		// no longer exists. Never proceed as nobody (spec §4.3).
		writeError(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// The state is single-use, whatever the outcome.
	h.setStateCookie(w, "", -1)

	st, ok := h.readState(r, user)
	provider := ""
	if st != nil {
		provider = st.Provider
	}
	if !ok {
		h.finishStepup(w, r, user, user.OrgID, provider, reasonStateMismatch, "")
		return
	}
	org, err := storage.GetOrgByID(h.ah.db, st.OrgID)
	if err != nil || org == nil {
		h.finishStepup(w, r, user, user.OrgID, provider, reasonStateMismatch, "")
		return
	}
	reason, pin := h.checkStepup(ctx, r, user, org, st)
	h.finishStepup(w, r, user, &org.ID, provider, reason, pin)
}

// checkStepup runs callback steps 1–4 and returns the denial reason ("" when
// granted) and, for Microsoft, the tenant to pin.
func (h *ssoHandlers) checkStepup(ctx context.Context, r *http.Request, user *storage.User, org *storage.Org, st *stepupState) (string, string) {
	p := h.stepup.provider(st.Provider)
	q := r.URL.Query()
	if p == nil || q.Get("error") != "" || q.Get("code") == "" {
		return reasonTokenInvalid, ""
	}
	id, err := p.Exchange(ctx, q.Get("code"), st.Nonce)
	if err != nil {
		log.Printf("sso step-up: %s exchange for user %s: %v", st.Provider, user.ID, err)
		return reasonTokenInvalid, ""
	}
	sessEmail := sessionEmail(ctx)
	if sessEmail == "" {
		sessEmail = user.Email
	}

	if st.Provider == auth.StepupGoogle {
		if id.HD == "" {
			return reasonNoHD, ""
		}
		if !strings.EqualFold(strings.TrimSpace(id.HD), org.Domain) {
			return reasonHDMismatch, ""
		}
		if !id.EmailVerified || !strings.EqualFold(id.Email, sessEmail) {
			return reasonEmailMismatch, ""
		}
		admin, err := p.GoogleIsAdmin(ctx, id.AccessToken, id.Email)
		if err != nil || !admin {
			return reasonNotAdmin, ""
		}
		return "", ""
	}

	tid, verified, err := p.MicrosoftOrganization(ctx, id.AccessToken)
	if err != nil || tid == "" || tid != id.TenantID || !containsFold(verified, org.Domain) {
		return reasonTIDMismatch, ""
	}
	pinned, err := storage.GetOrgMSTenantID(ctx, h.ah.db, org.ID)
	if err != nil {
		return reasonTIDMismatch, ""
	}
	if pinned != "" && pinned != tid {
		return reasonTIDChanged, ""
	}
	if !strings.EqualFold(id.PreferredUsername, sessEmail) && !strings.EqualFold(id.Email, sessEmail) {
		return reasonEmailMismatch, ""
	}
	roles := id.WIDs
	if !id.WIDsPresent {
		if roles, err = p.MicrosoftDirectoryRoles(ctx, id.AccessToken); err != nil {
			return reasonNotAdmin, ""
		}
	}
	if !auth.HasQualifyingEntraRole(roles) {
		return reasonNotAdmin, ""
	}
	return "", tid
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(strings.TrimSpace(v), s) {
			return true
		}
	}
	return false
}

// finishStepup records the outcome and redirects to the settings page. A
// storage failure is a 500 with no usable grant (spec Decisions, D-4).
func (h *ssoHandlers) finishStepup(w http.ResponseWriter, r *http.Request, user *storage.User, orgID *uuid.UUID, provider, reason, pin string) {
	o := storage.SSOStepupOutcome{
		UserID: user.ID, OrgID: orgID, Provider: provider,
		Granted: reason == "", Reason: reason, PinTenant: pin, Now: h.clock.Now(),
	}
	_, err := h.store.recordStepup(r.Context(), o)
	if errors.Is(err, storage.ErrTenantPinned) {
		// Pinned to another tenant between the check and the write.
		o.Granted, o.Reason, o.PinTenant, reason = false, reasonTIDChanged, "", reasonTIDChanged
		_, err = h.store.recordStepup(r.Context(), o)
	}
	if err != nil {
		writeServerError(w, r, "sso step-up record", err, http.StatusInternalServerError)
		return
	}
	target := h.ah.frontendURL + "/settings?stepup=granted"
	if reason != "" {
		target = h.ah.frontendURL + "/settings?stepup=denied&reason=" + reason
	}
	http.Redirect(w, r, target, http.StatusFound)
}
