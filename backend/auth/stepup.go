package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/Enach/paceday/backend/config"
)

// PAC-50 (spec docs/specs/PAC-50.md §4.3): the admin step-up's conversation
// with Google and Microsoft. It asks only what the gate needs (who signed in,
// for which hosted domain / tenant, and whether they administer it) and keeps
// nothing: the tokens it receives are dropped by the caller after the check.

const (
	// StepupGoogle and StepupMicrosoft are the two providers (SsoStepupProvider).
	StepupGoogle    = "google"
	StepupMicrosoft = "microsoft"

	googleDirectoryScope = "https://www.googleapis.com/auth/admin.directory.user.readonly"
)

// qualifyingEntraRoles are the role template ids that make a Microsoft user
// an administrator for PAC-50 (spec §4.4). Template ids are immutable; the
// list is fixed in the build, not configuration. The ordinary-member template
// b79fbf4d-3ef9-4689-8143-76b194e85509 is deliberately absent.
var qualifyingEntraRoles = map[string]bool{
	"62e90394-69f5-4237-9190-012177145e10": true, // Global Administrator
	"e8611ab8-c189-46e8-94e1-60213ab1f814": true, // Privileged Role Administrator
	"9b895d92-2cd3-44c7-9d02-a6ac2d5ea5c3": true, // Application Administrator
	"158c047a-c907-4556-b7ef-446551a6b5f7": true, // Cloud Application Administrator
}

// HasQualifyingEntraRole reports whether any of ids is a qualifying role
// template id (case-insensitive).
func HasQualifyingEntraRole(ids []string) bool {
	for _, id := range ids {
		if qualifyingEntraRoles[strings.ToLower(strings.TrimSpace(id))] {
			return true
		}
	}
	return false
}

// StepupProvider is one configured identity provider for the step-up.
type StepupProvider struct {
	Name     string
	oauth    *oauth2.Config
	ep       config.IdPEndpoint
	client   *http.Client
	verifier *gooidc.IDTokenVerifier
}

// NewGoogleStepup configures the Google step-up. Its scopes are the identity
// scopes plus the read-only Directory user scope, requested for this
// round-trip only; it never includes the calendar scope.
func NewGoogleStepup(clientID, clientSecret, redirectURL string, ep config.IdPEndpoint) *StepupProvider {
	return newStepup(StepupGoogle, clientID, clientSecret, redirectURL, ep,
		[]string{gooidc.ScopeOpenID, "email", googleDirectoryScope})
}

// NewMicrosoftStepup configures the Microsoft step-up: identity scopes, the
// organisation read (verified domains, spec OQ-2) and the directory-role read
// used when the token carries no `wids`. No calendar scope.
func NewMicrosoftStepup(clientID, clientSecret, redirectURL string, ep config.IdPEndpoint) *StepupProvider {
	return newStepup(StepupMicrosoft, clientID, clientSecret, redirectURL, ep,
		[]string{gooidc.ScopeOpenID, "profile", "email", "User.Read", "Organization.Read.All", "RoleManagement.Read.Directory"})
}

// ExtraScopes is the seam for the step-up 403's stepup_scopes (PAC-50 AC-24).
// Not implemented yet.
func (p *StepupProvider) ExtraScopes() []string { return nil }

func newStepup(name, clientID, clientSecret, redirectURL string, ep config.IdPEndpoint, scopes []string) *StepupProvider {
	client := newIdPHTTPClient(ep.Guard)
	keys := gooidc.NewRemoteKeySet(gooidc.ClientContext(context.Background(), client), ep.JWKSURL)
	return &StepupProvider{
		Name: name,
		oauth: &oauth2.Config{
			ClientID: clientID, ClientSecret: clientSecret, RedirectURL: redirectURL, Scopes: scopes,
			Endpoint: oauth2.Endpoint{AuthURL: ep.AuthURL, TokenURL: ep.TokenURL, AuthStyle: oauth2.AuthStyleInParams},
		},
		ep:     ep,
		client: client,
		// The issuer is checked by hand after verification: Microsoft's
		// depends on the token's tid, and Google publishes two spellings.
		verifier: gooidc.NewVerifier(ep.Issuer, keys, &gooidc.Config{ClientID: clientID, SkipIssuerCheck: true}),
	}
}

// errPeerRefused is returned when a guarded connection would leave the
// permitted ranges.
var errPeerRefused = errors.New("identity provider: peer address refused by the connection guard")

// newIdPHTTPClient never follows redirects and uses no ambient proxy. With
// guard on (a stand-in deployment) it connects only to config.PermittedPeer
// addresses, judged per connection like the calendar guard (PAC-43).
func newIdPHTTPClient(guard bool) *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if guard {
		d.Control = func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil || !config.PermittedPeer(ap.Addr()) {
				return fmt.Errorf("%w: %s", errPeerRefused, address)
			}
			return nil
		}
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.DialContext = d.DialContext
	return &http.Client{
		Transport: t,
		Timeout:   15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// AuthURL is the provider's authorization URL for one step-up. state and
// nonce come from the signed state cookie; loginHint pre-selects the signed-in
// user's account and hd (Google only) the org domain. Both are hints: the
// callback checks the result, not the request.
func (p *StepupProvider) AuthURL(state, nonce, loginHint, hd string) string {
	opts := []oauth2.AuthCodeOption{
		oauth2.SetAuthURLParam("nonce", nonce),
		oauth2.AccessTypeOnline,
	}
	if loginHint != "" {
		opts = append(opts, oauth2.SetAuthURLParam("login_hint", loginHint))
	}
	if p.Name == StepupGoogle {
		opts = append(opts, oauth2.SetAuthURLParam("prompt", "select_account consent"))
		if hd != "" {
			opts = append(opts, oauth2.SetAuthURLParam("hd", hd))
		}
	} else {
		// A fresh sign-in, not a silent SSO reuse.
		opts = append(opts, oauth2.SetAuthURLParam("prompt", "login"))
	}
	return p.oauth.AuthCodeURL(state, opts...)
}

// StepupIdentity is what a verified step-up ID token says.
type StepupIdentity struct {
	Email             string
	EmailVerified     bool
	HD                string
	TenantID          string
	PreferredUsername string
	WIDs              []string
	WIDsPresent       bool
	AccessToken       string
}

type stepupClaims struct {
	Issuer            string          `json:"iss"`
	Nonce             string          `json:"nonce"`
	Email             string          `json:"email"`
	EmailVerified     json.RawMessage `json:"email_verified"`
	HD                string          `json:"hd"`
	TenantID          string          `json:"tid"`
	PreferredUsername string          `json:"preferred_username"`
	WIDs              *[]string       `json:"wids"`
}

// ErrStepupToken is wrapped by every exchange or verification failure.
var ErrStepupToken = errors.New("step-up token invalid")

// Exchange trades the code for tokens and verifies the ID token: signature
// against the provider's keys, audience, expiry, issuer and that its nonce is
// the one this step-up sent.
func (p *StepupProvider) Exchange(ctx context.Context, code, nonce string) (*StepupIdentity, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, p.client)
	tok, err := p.oauth.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("%w: exchange: %v", ErrStepupToken, err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok || raw == "" {
		return nil, fmt.Errorf("%w: no id_token", ErrStepupToken)
	}
	idt, err := p.verifier.Verify(gooidc.ClientContext(ctx, p.client), raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStepupToken, err)
	}
	var c stepupClaims
	if err := idt.Claims(&c); err != nil {
		return nil, fmt.Errorf("%w: claims: %v", ErrStepupToken, err)
	}
	if !p.issuerOK(c.Issuer, c.TenantID) {
		return nil, fmt.Errorf("%w: unexpected issuer %q", ErrStepupToken, c.Issuer)
	}
	if nonce == "" || c.Nonce != nonce {
		return nil, fmt.Errorf("%w: nonce mismatch", ErrStepupToken)
	}
	id := &StepupIdentity{
		Email: c.Email, HD: c.HD, TenantID: c.TenantID, PreferredUsername: c.PreferredUsername,
		EmailVerified: string(c.EmailVerified) == "true" || string(c.EmailVerified) == `"true"`,
		AccessToken:   tok.AccessToken,
	}
	if c.WIDs != nil {
		id.WIDs, id.WIDsPresent = *c.WIDs, true
	}
	return id, nil
}

func (p *StepupProvider) issuerOK(iss, tid string) bool {
	if p.Name == StepupMicrosoft {
		return tid != "" && !strings.ContainsAny(tid, "/?#") && iss == strings.Replace(p.ep.Issuer, config.TenantPlaceholder, tid, 1)
	}
	if iss == p.ep.Issuer {
		return true
	}
	// Google documents both spellings for its own issuer.
	return p.ep.Issuer == config.ProductionGoogleIdP().Issuer && iss == "accounts.google.com"
}

func (p *StepupProvider) getJSON(ctx context.Context, accessToken, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %d", req.URL.Path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// GoogleIsAdmin asks the Directory API whether email administers its domain:
// isAdmin or isDelegatedAdmin (spec §4.3 step 4, OQ-1). Any error is returned
// as such; the caller treats it as "not admin", never "retry".
func (p *StepupProvider) GoogleIsAdmin(ctx context.Context, accessToken, email string) (bool, error) {
	var u struct {
		IsAdmin          bool `json:"isAdmin"`
		IsDelegatedAdmin bool `json:"isDelegatedAdmin"`
	}
	if err := p.getJSON(ctx, accessToken, p.ep.APIBase+"/users/"+url.PathEscape(email)+"?fields=isAdmin,isDelegatedAdmin", &u); err != nil {
		return false, err
	}
	return u.IsAdmin || u.IsDelegatedAdmin, nil
}

// MicrosoftOrganization returns the signed-in user's tenant id and its
// verified domains, from Graph /organization.
func (p *StepupProvider) MicrosoftOrganization(ctx context.Context, accessToken string) (string, []string, error) {
	var r struct {
		Value []struct {
			ID              string `json:"id"`
			VerifiedDomains []struct {
				Name string `json:"name"`
			} `json:"verifiedDomains"`
		} `json:"value"`
	}
	if err := p.getJSON(ctx, accessToken, p.ep.APIBase+"/organization?$select=id,verifiedDomains", &r); err != nil {
		return "", nil, err
	}
	if len(r.Value) != 1 {
		return "", nil, fmt.Errorf("organization: %d entries, want 1", len(r.Value))
	}
	domains := make([]string, 0, len(r.Value[0].VerifiedDomains))
	for _, d := range r.Value[0].VerifiedDomains {
		domains = append(domains, d.Name)
	}
	return r.Value[0].ID, domains, nil
}

// MicrosoftDirectoryRoles returns the role template ids of the signed-in
// user's directory-role memberships (used when the token has no `wids`).
func (p *StepupProvider) MicrosoftDirectoryRoles(ctx context.Context, accessToken string) ([]string, error) {
	var r struct {
		Value []struct {
			RoleTemplateID string `json:"roleTemplateId"`
		} `json:"value"`
	}
	if err := p.getJSON(ctx, accessToken, p.ep.APIBase+"/me/memberOf/microsoft.graph.directoryRole?$select=roleTemplateId", &r); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(r.Value))
	for _, v := range r.Value {
		ids = append(ids, v.RoleTemplateID)
	}
	return ids, nil
}
