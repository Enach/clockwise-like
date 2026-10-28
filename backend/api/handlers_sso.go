package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/domain"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
)

// ssoBeforeStoreHook, when set by a test, runs between createSsoProvider's
// checks and its store, to land another write in that gap (PAC-22 AC-27,
// AC-28). Always nil outside tests.
var ssoBeforeStoreHook func()

type ssoHandlers struct {
	ah *authHandlers
}

// --- POST /api/auth/detect (public) ----------------------------------------

func (h *ssoHandlers) detect(w http.ResponseWriter, r *http.Request) {
	if !allowDetect(clientIP(r, trustedProxies)) {
		writeError(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	var body struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Email == "" {
		writeError(w, "email required", http.StatusBadRequest)
		return
	}

	result, err := auth.DetectAuthProvider(r.Context(), h.ah.db, body.Email)
	if err != nil {
		writeServerError(w, r, "detection failed", err, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// --- GET /api/auth/sso/{domain} (public) ------------------------------------

func (h *ssoHandlers) startSSO(w http.ResponseWriter, r *http.Request) {
	d := chi.URLParam(r, "domain")
	provider, err := storage.GetSSOProviderByDomain(h.ah.db, d)
	if err != nil || provider == nil {
		writeError(w, "SSO not configured for this domain", http.StatusNotFound)
		return
	}
	if provider.ProviderType != "oidc" {
		writeError(w, "SAML not yet supported", http.StatusNotImplemented)
		return
	}

	appURL := os.Getenv("APP_URL")
	redirectURL := appURL + "/api/auth/callback/oidc/" + d

	client, err := auth.NewOIDCClient(
		r.Context(),
		provider.OIDCIssuer, provider.OIDCClientID, provider.OIDCClientSecret,
		redirectURL,
	)
	if err != nil {
		writeServerError(w, r, "OIDC setup", err, http.StatusInternalServerError)
		return
	}

	b := make([]byte, 16)
	_, _ = rand.Read(b)
	state := hex.EncodeToString(b)

	setCookie(w, &http.Cookie{
		Name:     "sso_state",
		Value:    state + "|" + d,
		Path:     "/",
		MaxAge:   300,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	// A hint is forwarded only when it belongs to this domain, so one
	// organisation's IdP is never sent an outside address (PAC-35, D-10).
	hint := loginHintFrom(r)
	if hint != "" && !domain.EmailBelongsToDomain(hint, d) {
		hint = ""
	}
	http.Redirect(w, r, client.AuthURL(state, hint), http.StatusFound)
}

// --- GET /api/auth/callback/oidc/{domain} (public) --------------------------

func (h *ssoHandlers) oidcCallback(w http.ResponseWriter, r *http.Request) {
	d := chi.URLParam(r, "domain")

	stateCookie, err := r.Cookie("sso_state")
	if err != nil {
		http.Error(w, "missing state cookie", http.StatusBadRequest)
		return
	}
	parts := strings.SplitN(stateCookie.Value, "|", 2)
	if len(parts) != 2 || parts[0] != r.URL.Query().Get("state") || parts[1] != d {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	setCookie(w, &http.Cookie{Name: "sso_state", MaxAge: -1, Path: "/"})

	provider, err := storage.GetSSOProviderByDomain(h.ah.db, d)
	if err != nil || provider == nil {
		writeError(w, "SSO not configured for this domain", http.StatusNotFound)
		return
	}

	appURL := os.Getenv("APP_URL")
	redirectURL := appURL + "/api/auth/callback/oidc/" + d

	client, err := auth.NewOIDCClient(
		r.Context(),
		provider.OIDCIssuer, provider.OIDCClientID, provider.OIDCClientSecret,
		redirectURL,
	)
	if err != nil {
		writeServerError(w, r, "OIDC setup", err, http.StatusInternalServerError)
		return
	}

	userInfo, _, err := client.Exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		writeServerError(w, r, "OIDC exchange", err, http.StatusInternalServerError)
		return
	}
	if userInfo.Email == "" {
		http.Error(w, "no email in OIDC token claims", http.StatusBadRequest)
		return
	}
	// The provider row was chosen by {domain}, and that row's issuer and client
	// credentials are writable by org members. Without this check an IdP
	// configured for one domain could assert any email and UpsertUser would
	// hand back the existing account for it (PAC-45).
	if !domain.EmailBelongsToDomain(userInfo.Email, d) {
		http.Error(w, "email domain does not match SSO provider domain", http.StatusForbidden)
		return
	}

	user, err := storage.UpsertUser(h.ah.db, userInfo.Email, userInfo.Name, "", "sso", userInfo.Sub)
	if err != nil {
		writeServerError(w, r, "upsert user", err, http.StatusInternalServerError)
		return
	}
	_ = storage.AssociateUserWithOrg(h.ah.db, user.ID, userInfo.Email)

	if err := h.ah.issueJWT(w, user); err != nil {
		h.ah.signInFailed(w, r, "issue session", err)
		return
	}
	http.Redirect(w, r, h.ah.frontendURL+"/auth/callback?provider=sso", http.StatusFound)
}

// --- Admin SSO CRUD (protected) -------------------------------------------

// POST /api/admin/sso
//
// Kept hand-written (contracts/openapi/MIGRATION.md): the generated binder
// cannot tell an absent oidc_client_secret from a null one (both are a nil
// pointer), never sees the SAML properties the contract refuses by name, and
// answers an undecodable body with its own message, not "invalid JSON".
func (h *ssoHandlers) createSSOProvider(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	user, err := storage.GetUserByID(h.ah.db, userID)
	if err != nil || user == nil || user.OrgID == nil {
		writeError(w, "org membership required", http.StatusForbidden)
		return
	}

	body, msg := decodeSSOProviderWrite(r)
	if msg != "" {
		writeError(w, msg, http.StatusBadRequest)
		return
	}

	// Only allow configuring SSO for the user's own org domain.
	org, err := storage.GetOrgByID(h.ah.db, *user.OrgID)
	if err != nil || org == nil {
		writeError(w, "org not found", http.StatusForbidden)
		return
	}
	if !domain.DomainMatchesOrg(body.Domain, org.Domain) {
		writeError(w, "can only configure SSO for your own domain", http.StatusForbidden)
		return
	}

	// Check 6: a secret left out is kept only from the row this write would
	// be stored under, and only while the issuer and client id are unchanged
	// (PAC-22 spec D-13). Decided again when the write is stored (D-14).
	if body.ClientSecret == nil {
		current, err := storage.GetSSOProviderByDomainAnyState(h.ah.db, body.Domain)
		if err != nil {
			writeServerError(w, r, "read sso provider", err, http.StatusInternalServerError)
			return
		}
		if refusal := ssoKeepRefusal(storage.KeepableClientSecret(current, body.Issuer, body.ClientID)); refusal != "" {
			writeError(w, refusal, http.StatusBadRequest)
			return
		}
	}

	if ssoBeforeStoreHook != nil {
		ssoBeforeStoreHook()
	}

	p, err := storage.SaveOIDCProvider(h.ah.db, body)
	if refusal := ssoKeepRefusal(err); refusal != "" {
		writeError(w, refusal, http.StatusBadRequest)
		return
	}
	if err != nil {
		writeServerError(w, r, "save failed", err, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(ssoProviderView(p))
}

// ssoKeepRefusal maps a refusal to keep the client secret to its fixed 400
// message (contract check 6), or "" for any other outcome.
func ssoKeepRefusal(err error) string {
	switch {
	case errors.Is(err, storage.ErrSSOClientSecretTargetChanged):
		return "oidc_client_secret is required when oidc_issuer or oidc_client_id changes"
	case errors.Is(err, storage.ErrSSOClientSecretRequired):
		return "oidc_client_secret is required"
	}
	return ""
}

// samlProperties are refused by name, in this order, whatever their value
// (PAC-22 spec D-8, AC-18).
var samlProperties = []string{"saml_entry_point", "saml_issuer", "saml_cert"}

// decodeSSOProviderWrite applies createSsoProvider's checks 1 to 5 to the
// request body (PAC-22 contract, in that order) and returns the write, or the
// 400 message of the first check that fails. No message echoes a value.
func decodeSSOProviderWrite(r *http.Request) (storage.OIDCProviderWrite, string) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return storage.OIDCProviderWrite{}, "invalid JSON"
	}
	var props map[string]json.RawMessage
	if err := json.Unmarshal(raw, &props); err != nil || props == nil {
		return storage.OIDCProviderWrite{}, "invalid JSON"
	}
	var body struct {
		Domain           string  `json:"domain"`
		ProviderName     string  `json:"provider_name"`
		ProviderType     string  `json:"provider_type"`
		OIDCIssuer       string  `json:"oidc_issuer"`
		OIDCClientID     string  `json:"oidc_client_id"`
		OIDCClientSecret *string `json:"oidc_client_secret"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return storage.OIDCProviderWrite{}, "invalid JSON"
	}
	for _, name := range samlProperties {
		if _, present := props[name]; present {
			return storage.OIDCProviderWrite{}, name + " is not supported"
		}
	}
	if body.Domain == "" || body.ProviderName == "" || body.ProviderType == "" {
		return storage.OIDCProviderWrite{}, "domain, provider_name and provider_type are required"
	}
	switch body.ProviderType {
	case "oidc":
	case "saml":
		return storage.OIDCProviderWrite{}, "provider_type 'saml' is not supported"
	default:
		return storage.OIDCProviderWrite{}, "provider_type must be 'oidc'"
	}
	if body.OIDCIssuer == "" {
		return storage.OIDCProviderWrite{}, "oidc_issuer is required"
	}
	if body.OIDCClientID == "" {
		return storage.OIDCProviderWrite{}, "oidc_client_id is required"
	}
	if _, present := props["oidc_client_secret"]; present && (body.OIDCClientSecret == nil || *body.OIDCClientSecret == "") {
		return storage.OIDCProviderWrite{}, "oidc_client_secret must not be empty"
	}
	return storage.OIDCProviderWrite{
		Domain:       body.Domain,
		ProviderName: body.ProviderName,
		Issuer:       body.OIDCIssuer,
		ClientID:     body.OIDCClientID,
		ClientSecret: body.OIDCClientSecret,
	}, ""
}

// ssoProviderView is the only shape an SSO configuration leaves the server
// in (PAC-22, gen.SsoProvider). The client secret and the signing
// certificate are never copied into it; each is reduced to whether it is on
// file, and nothing else derived from either is carried.
func ssoProviderView(p *storage.SSOProvider) gen.SsoProvider {
	state := gen.Suspended
	if p.Enabled {
		state = gen.Active
	}
	return gen.SsoProvider{
		Id:                  p.ID,
		Domain:              p.Domain,
		ProviderName:        p.ProviderName,
		ProviderType:        gen.SsoProviderProviderType(p.ProviderType),
		State:               state,
		OidcIssuer:          p.OIDCIssuer,
		OidcClientId:        p.OIDCClientID,
		HasOidcClientSecret: p.OIDCClientSecret != "",
		SamlEntryPoint:      p.SAMLEntryPoint,
		SamlIssuer:          p.SAMLIssuer,
		HasSamlCert:         p.SAMLCert != "",
		CreatedAt:           p.CreatedAt,
		UpdatedAt:           p.UpdatedAt,
	}
}

// GET /api/admin/sso: implemented by generatedServer.ListSsoProviders through
// the generated strict wrapper (PAC-22, factory section 4).
func (h *ssoHandlers) listSSOProviders(w http.ResponseWriter, r *http.Request) {
	newGeneratedHandler(h.ah.db, nil).ListSsoProviders(w, r)
}

// ListSsoProviders answers listSsoProviders: the caller's organisation's
// configurations as an SsoProviderList, never carrying either secret.
func (s *generatedServer) ListSsoProviders(ctx context.Context, _ gen.ListSsoProvidersRequestObject) (gen.ListSsoProvidersResponseObject, error) {
	user, err := storage.GetUserByID(s.db, auth.UserIDFromContext(ctx))
	if err != nil || user == nil || user.OrgID == nil {
		return gen.ListSsoProviders403JSONResponse{Error: "org membership required"}, nil
	}
	providers, err := storage.ListSSOProvidersByOrg(s.db, *user.OrgID)
	if err != nil {
		return gen.ListSsoProviders500JSONResponse{Error: serverFailure(ctx, "listSSOProviders", err, http.StatusInternalServerError)}, nil
	}
	out := gen.SsoProviderList{Providers: make([]gen.SsoProvider, 0, len(providers))}
	for _, p := range providers {
		out.Providers = append(out.Providers, ssoProviderView(p))
	}
	return gen.ListSsoProviders200JSONResponse(out), nil
}

// DELETE /api/admin/sso/{domain}
func (h *ssoHandlers) deleteSSOProvider(w http.ResponseWriter, r *http.Request) {
	d := chi.URLParam(r, "domain")

	userID := userIDFromCtx(r.Context())
	user, err := storage.GetUserByID(h.ah.db, userID)
	if err != nil || user == nil || user.OrgID == nil {
		writeError(w, "org membership required", http.StatusForbidden)
		return
	}

	org, err := storage.GetOrgByID(h.ah.db, *user.OrgID)
	if err != nil || org == nil {
		writeError(w, "org not found", http.StatusForbidden)
		return
	}
	if !domain.DomainMatchesOrg(d, org.Domain) {
		writeError(w, "can only delete SSO for your own domain", http.StatusForbidden)
		return
	}

	if err := storage.DeleteSSOProvider(h.ah.db, d); err != nil {
		writeServerError(w, r, "deleteSSOProvider", err, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
