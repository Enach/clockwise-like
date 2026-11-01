package config

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// PAC-50 (spec docs/specs/PAC-50.md §4.1): the identity-provider addresses
// the SSO admin step-up talks to. Production uses the providers' published
// addresses; a deployment may redirect them to a stand-in only when it also
// declares test mode (EnvAllowStandins), exactly like the calendar address.

const (
	// EnvGoogleIdPBaseURL redirects every Google step-up address (authorize,
	// token, keys, issuer, Directory API) to one stand-in base.
	EnvGoogleIdPBaseURL = "PACEDAY_GOOGLE_IDP_BASE_URL"
	// EnvMicrosoftIdPBaseURL does the same for Microsoft (authorize, token,
	// keys, issuer, Graph).
	EnvMicrosoftIdPBaseURL = "PACEDAY_MICROSOFT_IDP_BASE_URL"

	// TenantPlaceholder stands for the token's tid in a Microsoft issuer.
	TenantPlaceholder = "{tenantid}"
)

// IdPEndpoint is the set of addresses for one identity provider.
type IdPEndpoint struct {
	AuthURL  string // browser-facing authorization endpoint
	TokenURL string
	JWKSURL  string
	// Issuer is the exact expected `iss`. For Microsoft it contains
	// TenantPlaceholder, replaced by the token's `tid` before comparison.
	Issuer string
	// APIBase is the Google Directory API base (…/admin/directory/v1) or the
	// Microsoft Graph base (…/v1.0), without a trailing slash.
	APIBase string
	// Guard is on exactly when the addresses point at a stand-in: every
	// connection is then limited to the permitted ranges (PermittedPeer).
	Guard bool
	// Notice, when non-empty, must be printed at startup.
	Notice string
}

// ProductionGoogleIdP is Google's published addresses.
func ProductionGoogleIdP() IdPEndpoint {
	return IdPEndpoint{
		AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL: "https://oauth2.googleapis.com/token",
		JWKSURL:  "https://www.googleapis.com/oauth2/v3/certs",
		Issuer:   "https://accounts.google.com",
		APIBase:  "https://admin.googleapis.com/admin/directory/v1",
	}
}

// ProductionMicrosoftIdP is Microsoft's published addresses. The
// `organizations` authority admits work and school accounts only.
func ProductionMicrosoftIdP() IdPEndpoint {
	return IdPEndpoint{
		AuthURL:  "https://login.microsoftonline.com/organizations/oauth2/v2.0/authorize",
		TokenURL: "https://login.microsoftonline.com/organizations/oauth2/v2.0/token",
		JWKSURL:  "https://login.microsoftonline.com/common/discovery/v2.0/keys",
		Issuer:   "https://login.microsoftonline.com/" + TenantPlaceholder + "/v2.0",
		APIBase:  "https://graph.microsoft.com/v1.0",
	}
}

// GoogleIdPAt lays Google's paths out under one stand-in base. It is not a
// decision: DecideGoogleIdP is the only way a deployment gets it.
func GoogleIdPAt(base string) IdPEndpoint {
	base = strings.TrimRight(base, "/")
	return IdPEndpoint{
		AuthURL:  base + "/o/oauth2/v2/auth",
		TokenURL: base + "/token",
		JWKSURL:  base + "/oauth2/v3/certs",
		Issuer:   base,
		APIBase:  base + "/admin/directory/v1",
	}
}

// MicrosoftIdPAt lays Microsoft's paths out under one stand-in base.
func MicrosoftIdPAt(base string) IdPEndpoint {
	base = strings.TrimRight(base, "/")
	return IdPEndpoint{
		AuthURL:  base + "/organizations/oauth2/v2.0/authorize",
		TokenURL: base + "/organizations/oauth2/v2.0/token",
		JWKSURL:  base + "/common/discovery/v2.0/keys",
		Issuer:   base + "/" + TenantPlaceholder + "/v2.0",
		APIBase:  base + "/v1.0",
	}
}

// DecideGoogleIdP evaluates EnvGoogleIdPBaseURL against the declaration.
func DecideGoogleIdP(baseURL, allowStandins string) (IdPEndpoint, error) {
	return decideIdP(EnvGoogleIdPBaseURL, baseURL, allowStandins, ProductionGoogleIdP, GoogleIdPAt)
}

// DecideMicrosoftIdP evaluates EnvMicrosoftIdPBaseURL against the declaration.
func DecideMicrosoftIdP(baseURL, allowStandins string) (IdPEndpoint, error) {
	return decideIdP(EnvMicrosoftIdPBaseURL, baseURL, allowStandins, ProductionMicrosoftIdP, MicrosoftIdPAt)
}

func decideIdP(env, baseURL, allowStandins string, prod func() IdPEndpoint, at func(string) IdPEndpoint) (IdPEndpoint, error) {
	if baseURL == "" {
		return prod(), nil
	}
	if declared, _ := strconv.ParseBool(allowStandins); !declared {
		return IdPEndpoint{}, fmt.Errorf(
			"%s is set but %s is not true: an identity-provider redirection is only allowed in a deployment that declares test mode; unset %s, or set %s=true if this is not production",
			env, EnvAllowStandins, env, EnvAllowStandins)
	}
	refuseIdP := func(value, why string) error { return fmt.Errorf("%s=%q refused: %s", env, value, why) }
	u, err := url.Parse(baseURL)
	if err != nil {
		return IdPEndpoint{}, refuseIdP(baseURL, "it cannot be parsed as a URL")
	}
	if u.User != nil {
		return IdPEndpoint{}, refuseIdP(u.Redacted(), "it carries user information; remove it rather than expecting it to be stripped")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return IdPEndpoint{}, refuseIdP(baseURL, "the scheme must be http or https")
	}
	if u.Hostname() == "" {
		return IdPEndpoint{}, refuseIdP(baseURL, "it has no host")
	}
	if err := checkLiteralHost(u); err != nil {
		return IdPEndpoint{}, refuseIdP(baseURL, err.Error())
	}
	ep := at(baseURL)
	ep.Guard = true
	ep.Notice = fmt.Sprintf("identity-provider traffic is redirected to %s (%s, %s=true): this is NOT a production deployment",
		baseURL, env, EnvAllowStandins)
	return ep, nil
}
