package auth

import (
	"net/mail"
	"regexp"
	"strings"

	"golang.org/x/oauth2"
)

// maxLoginHintLen is the longest hint forwarded: the practical maximum length
// of an email address.
const maxLoginHintLen = 254

var (
	loginHintLocalPart = regexp.MustCompile(`^[A-Za-z0-9._+'-]+$`)
	loginHintLabel     = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?$`)
	containsLetter     = regexp.MustCompile(`[A-Za-z]`)
)

// ValidLoginHint reports whether v may be forwarded to an identity provider as
// the OAuth/OIDC login_hint: the LoginHint rule of the contract
// (components.parameters.LoginHint, PAC-35). A bare email address only: at
// most 254 characters, exactly one "@", accepted by net/mail.ParseAddress as
// itself with no display name, a local part of [A-Za-z0-9._+'-], and a domain
// that is a DNS hostname of two or more labels whose last label has a letter.
// The hint is advisory; it never selects a Paceday account.
func ValidLoginHint(v string) bool {
	if v == "" || len(v) > maxLoginHintLen || strings.Count(v, "@") != 1 {
		return false
	}
	addr, err := mail.ParseAddress(v)
	if err != nil || addr.Name != "" || addr.Address != v {
		return false
	}
	at := strings.IndexByte(v, '@')
	local, host := v[:at], v[at+1:]
	if !loginHintLocalPart.MatchString(local) {
		return false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if !loginHintLabel.MatchString(l) {
			return false
		}
	}
	return containsLetter.MatchString(labels[len(labels)-1])
}

// loginHintOption returns the auth-code option carrying hint, or nothing for
// an empty hint. The caller has already applied ValidLoginHint; the OAuth
// library URL-encodes the value, so it cannot add a parameter.
func loginHintOption(hint string) []oauth2.AuthCodeOption {
	if hint == "" {
		return nil
	}
	return []oauth2.AuthCodeOption{oauth2.SetAuthURLParam("login_hint", hint)}
}
