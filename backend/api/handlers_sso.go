package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/domain"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
)

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
	http.Redirect(w, r, client.AuthURL(state), http.StatusFound)
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
		writeServerErrorText(w, r, "OIDC setup", err, http.StatusInternalServerError)
		return
	}

	userInfo, _, err := client.Exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		writeServerErrorText(w, r, "OIDC exchange", err, http.StatusInternalServerError)
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
		writeServerErrorText(w, r, "upsert user", err, http.StatusInternalServerError)
		return
	}
	_ = storage.AssociateUserWithOrg(h.ah.db, user.ID, userInfo.Email)

	h.ah.issueJWT(w, user)
	http.Redirect(w, r, h.ah.frontendURL+"/auth/callback?provider=sso", http.StatusFound)
}

// --- Admin SSO CRUD (protected) -------------------------------------------

// POST /api/admin/sso
func (h *ssoHandlers) createSSOProvider(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	user, err := storage.GetUserByID(h.ah.db, userID)
	if err != nil || user == nil || user.OrgID == nil {
		writeError(w, "org membership required", http.StatusForbidden)
		return
	}

	var body struct {
		Domain           string `json:"domain"`
		ProviderName     string `json:"provider_name"`
		ProviderType     string `json:"provider_type"`
		OIDCIssuer       string `json:"oidc_issuer"`
		OIDCClientID     string `json:"oidc_client_id"`
		OIDCClientSecret string `json:"oidc_client_secret"`
		SAMLEntryPoint   string `json:"saml_entry_point"`
		SAMLIssuer       string `json:"saml_issuer"`
		SAMLCert         string `json:"saml_cert"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.Domain == "" || body.ProviderName == "" || body.ProviderType == "" {
		writeError(w, "domain, provider_name and provider_type are required", http.StatusBadRequest)
		return
	}
	if body.ProviderType != "oidc" && body.ProviderType != "saml" {
		writeError(w, "provider_type must be 'oidc' or 'saml'", http.StatusBadRequest)
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

	p, err := storage.UpsertSSOProvider(h.ah.db, &storage.SSOProvider{
		Domain:           body.Domain,
		ProviderName:     body.ProviderName,
		ProviderType:     body.ProviderType,
		Enabled:          true,
		OIDCIssuer:       body.OIDCIssuer,
		OIDCClientID:     body.OIDCClientID,
		OIDCClientSecret: body.OIDCClientSecret,
		SAMLEntryPoint:   body.SAMLEntryPoint,
		SAMLIssuer:       body.SAMLIssuer,
		SAMLCert:         body.SAMLCert,
	})
	if err != nil {
		writeServerError(w, r, "save failed", err, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(p)
}

// GET /api/admin/sso
func (h *ssoHandlers) listSSOProviders(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	user, err := storage.GetUserByID(h.ah.db, userID)
	if err != nil || user == nil || user.OrgID == nil {
		writeError(w, "org membership required", http.StatusForbidden)
		return
	}

	providers, err := storage.ListSSOProvidersByOrg(h.ah.db, *user.OrgID)
	if err != nil {
		writeServerError(w, r, "listSSOProviders", err, http.StatusInternalServerError)
		return
	}
	if providers == nil {
		providers = []*storage.SSOProvider{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(providers)
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
