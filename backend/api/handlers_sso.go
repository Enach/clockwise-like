package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/domain"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
)

// --- Rate limiter (sliding 1-minute window per IP) -------------------------

type ipBucket struct {
	count     int
	windowEnd time.Time
}

var (
	detectLimiterMu sync.Mutex
	detectLimiter   = make(map[string]*ipBucket)
)

const detectRateLimit = 20

func allowDetect(ip string) bool {
	detectLimiterMu.Lock()
	defer detectLimiterMu.Unlock()

	now := time.Now()
	b, ok := detectLimiter[ip]
	if !ok || now.After(b.windowEnd) {
		detectLimiter[ip] = &ipBucket{count: 1, windowEnd: now.Add(time.Minute)}
		return true
	}
	if b.count >= detectRateLimit {
		return false
	}
	b.count++
	return true
}

func realIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return strings.SplitN(fwd, ",", 2)[0]
	}
	return r.RemoteAddr
}

// --- POST /api/auth/detect (public) ----------------------------------------

func (h *ssoHandlers) detect(w http.ResponseWriter, r *http.Request) {
	if !allowDetect(realIP(r)) {
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
		writeError(w, "detection failed: "+err.Error(), http.StatusInternalServerError)
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
		writeError(w, "OIDC setup: "+err.Error(), http.StatusInternalServerError)
		return
	}

	b := make([]byte, 16)
	_, _ = rand.Read(b)
	state := hex.EncodeToString(b)

	http.SetCookie(w, &http.Cookie{
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
	http.SetCookie(w, &http.Cookie{Name: "sso_state", MaxAge: -1, Path: "/"})

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
		http.Error(w, "OIDC setup: "+err.Error(), http.StatusInternalServerError)
		return
	}

	userInfo, _, err := client.Exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		http.Error(w, "OIDC exchange: "+err.Error(), http.StatusInternalServerError)
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
		http.Error(w, "upsert user: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_ = storage.AssociateUserWithOrg(h.ah.db, user.ID, userInfo.Email)

	h.ah.issueJWT(w, user)
	http.Redirect(w, r, h.ah.frontendURL+"/auth/callback?provider=sso", http.StatusFound)
}

// --- Admin SSO CRUD (protected) -------------------------------------------
//
// PAC-50 (spec docs/specs/PAC-50.md §4.2): every write passes the gate in
// writeGate, in the spec's order, before the provider store is touched.

// POST /api/admin/sso
func (h *ssoHandlers) createSSOProvider(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.callerOrg(w, r)
	if !ok {
		return
	}

	body, msg := decodeSSOProviderBody(r)
	if msg != "" {
		writeError(w, msg, http.StatusBadRequest)
		return
	}
	if !domain.DomainMatchesOrg(body.Domain, caller.org.Domain) {
		writeError(w, "provider_domain_mismatch", http.StatusBadRequest)
		return
	}

	via, ok := h.authorizeWrite(w, r, caller)
	if !ok {
		return
	}

	p, err := h.store.upsertProvider(r.Context(), &storage.SSOProvider{
		Domain:           strings.ToLower(strings.TrimSpace(body.Domain)),
		ProviderName:     body.ProviderName,
		ProviderType:     body.ProviderType,
		Enabled:          true,
		OIDCIssuer:       body.OIDCIssuer,
		OIDCClientID:     body.OIDCClientID,
		OIDCClientSecret: body.OIDCClientSecret,
		SAMLEntryPoint:   body.SAMLEntryPoint,
		SAMLIssuer:       body.SAMLIssuer,
		SAMLCert:         body.SAMLCert,
	}, caller.user.ID, caller.org.ID, via)
	if err != nil {
		writeError(w, "save failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(p)
}

// ssoProviderBody is SsoProviderCreateRequest.
type ssoProviderBody struct {
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

var ssoProviderBodyProperties = map[string]bool{
	"domain": true, "provider_name": true, "provider_type": true,
	"oidc_issuer": true, "oidc_client_id": true, "oidc_client_secret": true,
	"saml_entry_point": true, "saml_issuer": true, "saml_cert": true,
}

// decodeSSOProviderBody validates the body's shape (spec §4.2 step 3). It
// returns the 400 message, or "" when the body is acceptable. An undeclared
// property is refused, never dropped (the contract's additionalProperties:
// false; the PAC-24 "unknown property: <name>" convention).
func decodeSSOProviderBody(r *http.Request) (*ssoProviderBody, string) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, "invalid JSON"
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, "invalid JSON"
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !ssoProviderBodyProperties[name] {
			return nil, "unknown property: " + name
		}
	}
	var body ssoProviderBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, "invalid JSON"
	}
	if body.Domain == "" || body.ProviderName == "" || body.ProviderType == "" {
		return nil, "domain, provider_name and provider_type are required"
	}
	if body.ProviderType != "oidc" && body.ProviderType != "saml" {
		return nil, "provider_type must be 'oidc' or 'saml'"
	}
	return &body, ""
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
		writeError(w, err.Error(), http.StatusInternalServerError)
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
	caller, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	// The segment is a target, never an authorization: lower-cased, and it
	// must be the caller's own org domain exactly.
	d := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "domain")))
	if !domain.DomainMatchesOrg(d, caller.org.Domain) {
		writeError(w, "provider_domain_mismatch", http.StatusBadRequest)
		return
	}

	via, ok := h.authorizeWrite(w, r, caller)
	if !ok {
		return
	}

	found, err := h.store.deleteProvider(r.Context(), d, caller.user.ID, caller.org.ID, via)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		// The grant (if one was used) is consumed: spec Decisions, D-5.
		writeError(w, "sso provider not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
