package auth

import (
	"context"
	"fmt"
	"sync"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDCClient wraps a discovered OIDC provider ready for the auth-code flow.
type OIDCClient struct {
	provider *gooidc.Provider
	config   *oauth2.Config
}

// OIDCUserInfo holds the claims extracted from the ID token.
type OIDCUserInfo struct {
	Sub   string `json:"sub"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// oidcProviderCache avoids redundant discovery-endpoint fetches per issuer URL.
var oidcProviderCache struct {
	sync.RWMutex
	m map[string]*gooidc.Provider
}

func init() { oidcProviderCache.m = make(map[string]*gooidc.Provider) }

// NewOIDCClient builds (or retrieves from cache) an OIDCClient for issuer.
func NewOIDCClient(ctx context.Context, issuer, clientID, clientSecret, redirectURL string) (*OIDCClient, error) {
	oidcProviderCache.RLock()
	p, ok := oidcProviderCache.m[issuer]
	oidcProviderCache.RUnlock()

	if !ok {
		var err error
		p, err = gooidc.NewProvider(ctx, issuer)
		if err != nil {
			return nil, fmt.Errorf("oidc discovery %q: %w", issuer, err)
		}
		oidcProviderCache.Lock()
		oidcProviderCache.m[issuer] = p
		oidcProviderCache.Unlock()
	}

	cfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Endpoint:     p.Endpoint(),
		Scopes:       []string{gooidc.ScopeOpenID, "profile", "email"},
	}
	return &OIDCClient{provider: p, config: cfg}, nil
}

// AuthURL returns the authorization redirect URL for the given CSRF state.
func (c *OIDCClient) AuthURL(state string) string {
	return c.config.AuthCodeURL(state, oauth2.AccessTypeOnline)
}

// Exchange trades the auth code for tokens and returns verified user claims.
func (c *OIDCClient) Exchange(ctx context.Context, code string) (*OIDCUserInfo, *oauth2.Token, error) {
	token, err := c.config.Exchange(ctx, code)
	if err != nil {
		return nil, nil, fmt.Errorf("token exchange: %w", err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, nil, fmt.Errorf("no id_token in OIDC response")
	}

	verifier := c.provider.Verifier(&gooidc.Config{ClientID: c.config.ClientID})
	idToken, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, nil, fmt.Errorf("id_token verification: %w", err)
	}

	var claims OIDCUserInfo
	if err := idToken.Claims(&claims); err != nil {
		return nil, nil, fmt.Errorf("parse id_token claims: %w", err)
	}

	return &claims, token, nil
}
