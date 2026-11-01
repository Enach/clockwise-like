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
	"sort"
	"strings"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/domain"
	"github.com/Enach/paceday/backend/internal/budget"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
)

// ssoBeforeStoreHook, when set by a test, runs between createSsoProvider's
// checks and its store, to land another write in that gap (PAC-22 AC-27,
// AC-28). Always nil outside tests.
var ssoBeforeStoreHook func()

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
	// PAC-81 AC-A2: at the budget, the sign-in failure redirect.
	budget.OnExpiry(r.Context(), h.ah.signInExpired)
	db := storage.Bind(r.Context(), h.ah.db)
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

	provider, err := storage.GetSSOProviderByDomain(db, d)
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

	user, err := storage.UpsertUser(db, userInfo.Email, userInfo.Name, "", "sso", userInfo.Sub)
	if err != nil {
		writeServerError(w, r, "upsert user", err, http.StatusInternalServerError)
		return
	}
	_ = storage.AssociateUserWithOrg(db, user.ID, userInfo.Email)

	if err := h.ah.issueJWT(w, user); err != nil {
		h.ah.signInFailed(w, r, "issue session", err)
		return
	}
	http.Redirect(w, r, h.ah.frontendURL+"/auth/callback?provider=sso", http.StatusFound)
}

// --- Admin SSO CRUD (protected) -------------------------------------------
//
// PAC-50 (spec docs/specs/PAC-50.md §4.2, contract revision 8.1): every SSO
// configuration write passes the gate in this order before the provider
// store is written: membership (callerOrg), the body's 400s, the exact
// domain 403, PAC-22's stored-row check (create only), authorization
// (authorizeWrite, which consumes the grant), then the store.

// POST /api/admin/sso
//
// Kept hand-written (contracts/openapi/MIGRATION.md): the generated binder
// cannot tell an absent oidc_client_secret from a null one (both are a nil
// pointer), never sees the SAML properties the contract refuses by name, and
// answers an undecodable body with its own message, not "invalid JSON".
func (h *ssoHandlers) createSSOProvider(w http.ResponseWriter, r *http.Request) {
	// Step 1: membership, both messages before the body is read.
	caller, ok := h.callerOrg(w, r)
	if !ok {
		return
	}

	// Step 2: the body (PAC-22 checks 1-5 and PAC-50's unknown property).
	body, msg := decodeSSOProviderWrite(r)
	if msg != "" {
		writeError(w, msg, http.StatusBadRequest)
		return
	}

	// Step 3, owner 2026-10-27 (spec §9 D-10): a subdomain of, or a domain
	// foreign to, the caller's org domain is understood and not permitted:
	// 403 (RFC 9110 §15.5.4). Untrimmed; before the grant is touched (AC-3c).
	if !domain.DomainMatchesOrg(body.Domain, caller.org.Domain) {
		writeError(w, "provider_domain_mismatch", http.StatusForbidden)
		return
	}
	body.Domain = strings.ToLower(body.Domain)

	// Step 4, PAC-22's check 6: a secret left out is kept only from the row
	// this write would be stored under, and only while the issuer and client
	// id are unchanged (PAC-22 spec D-13). Before authorization, so a
	// mistyped issuer does not burn a grant (spec §4.2 step 4b, AC-3c).
	if body.ClientSecret == nil {
		current, err := h.store.getProvider(r.Context(), body.Domain)
		if err != nil {
			writeServerError(w, r, "read sso provider", err, http.StatusInternalServerError)
			return
		}
		if refusal := ssoKeepRefusal(storage.KeepableClientSecret(current, body.Issuer, body.ClientID)); refusal != "" {
			writeError(w, refusal, http.StatusBadRequest)
			return
		}
	}

	// Step 5: authorization; the grant, if any, is consumed here.
	via, ok := h.authorizeWrite(w, r, caller)
	if !ok {
		return
	}

	if ssoBeforeStoreHook != nil {
		ssoBeforeStoreHook()
	}

	// Step 6: store and record. What a replace keeps is decided again inside
	// the store's transaction (PAC-22 D-14); its refusal comes after the
	// consume and spends the grant (spec §9 D-14(2)).
	p, err := h.store.saveProvider(r.Context(), body, caller.user.ID, caller.org.ID, via)
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
// message (contract check 6), and storage's own refusal of an empty supplied
// secret to check 5's message, or "" for any other outcome.
func ssoKeepRefusal(err error) string {
	switch {
	case errors.Is(err, storage.ErrSSOClientSecretEmpty):
		return "oidc_client_secret must not be empty"
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

// ssoProviderWriteProperties are SsoProviderCreateRequest's declared
// properties (the SAML ones are refused by name before this set is used).
var ssoProviderWriteProperties = map[string]bool{
	"domain": true, "provider_name": true, "provider_type": true,
	"oidc_issuer": true, "oidc_client_id": true, "oidc_client_secret": true,
}

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
	// Every property is read from props by its exact name. JSON names are
	// case sensitive, but decoding into a struct matches field names case
	// insensitively, so a struct would take "OIDC_CLIENT_SECRET" as the
	// secret while the presence check below, on props, did not see it, and an
	// empty secret could be stored (review finding on PAC-22). Reading the
	// presence and the value from the same map makes a case variant what the
	// contract says it is: an undeclared property, refused since PAC-50.
	// null for a declared property is not a type error: it decodes as empty
	// and is refused by the required checks below (contract revision 8.1,
	// N1).
	var body struct {
		Domain, ProviderName, ProviderType, OIDCIssuer, OIDCClientID string
		OIDCClientSecret                                             *string
	}
	for name, dst := range map[string]any{
		"domain":             &body.Domain,
		"provider_name":      &body.ProviderName,
		"provider_type":      &body.ProviderType,
		"oidc_issuer":        &body.OIDCIssuer,
		"oidc_client_id":     &body.OIDCClientID,
		"oidc_client_secret": &body.OIDCClientSecret,
	} {
		if v, present := props[name]; present {
			if err := json.Unmarshal(v, dst); err != nil {
				return storage.OIDCProviderWrite{}, "invalid JSON"
			}
		}
	}
	for _, name := range samlProperties {
		if _, present := props[name]; present {
			return storage.OIDCProviderWrite{}, name + " is not supported"
		}
	}
	// PAC-50 (contract step 2c, spec AC-16a): any other property outside the
	// schema is refused, never ignored; the first in byte order of the
	// names is named. After the SAML refusals, so a SAML property is always
	// named, and before the required-field check.
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !ssoProviderWriteProperties[name] {
			return storage.OIDCProviderWrite{}, "unknown property: " + name
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

// listSSOProvidersByOrg is the list query; a test replaces it to reach the
// PAC-21 500 (PAC-50 mergeConditions).
var listSSOProvidersByOrg = storage.ListSSOProvidersByOrg

// ListSsoProviders answers listSsoProviders: the caller's organisation's
// configurations as an SsoProviderList, never carrying either secret.
func (s *generatedServer) ListSsoProviders(ctx context.Context, _ gen.ListSsoProvidersRequestObject) (gen.ListSsoProvidersResponseObject, error) {
	user, err := storage.GetUserByID(s.db, auth.UserIDFromContext(ctx))
	if err != nil || user == nil || user.OrgID == nil {
		return gen.ListSsoProviders403JSONResponse{Error: "org membership required"}, nil
	}
	providers, err := listSSOProvidersByOrg(s.db, *user.OrgID)
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
	caller, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	// The segment is a target, never an authorization: lower-cased, and it
	// must be the caller's own org domain exactly, as sent (no trimming:
	// spec §9 D-9).
	d := strings.ToLower(chi.URLParam(r, "domain"))
	if !domain.DomainMatchesOrg(d, caller.org.Domain) {
		writeError(w, "provider_domain_mismatch", http.StatusForbidden) // spec §9 D-10
		return
	}

	via, ok := h.authorizeWrite(w, r, caller)
	if !ok {
		return
	}

	found, err := h.store.deleteProvider(r.Context(), d, caller.user.ID, caller.org.ID, via)
	if err != nil {
		writeServerError(w, r, "deleteSSOProvider", err, http.StatusInternalServerError)
		return
	}
	if !found {
		// The grant (if one was used) is consumed: spec Decisions, D-5.
		writeError(w, "sso provider not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
