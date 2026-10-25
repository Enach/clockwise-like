package domain

import "strings"

var genericDomains = map[string]bool{
	"gmail.com": true, "googlemail.com": true,
	"outlook.com": true, "hotmail.com": true, "live.com": true, "msn.com": true,
	"yahoo.com": true, "yahoo.fr": true, "yahoo.co.uk": true,
	"icloud.com": true, "me.com": true, "mac.com": true,
	"protonmail.com": true, "proton.me": true,
	"aol.com": true, "mail.com": true, "gmx.com": true,
	"yandex.com": true, "yandex.ru": true,
	"zoho.com": true, "fastmail.com": true,
}

func ExtractDomain(email string) string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(parts[1]))
}

func IsGenericDomain(domain string) bool {
	return genericDomains[domain]
}

// EmailBelongsToDomain reports whether email's domain is exactly d, compared
// case-insensitively. Subdomains do not match: an SSO provider row is keyed by
// one exact domain, and sign-in detection routes a user to the provider for
// their exact email domain, so an assertion for any other domain did not come
// from that domain's IdP. An address with more than one "@" never matches.
func EmailBelongsToDomain(email, d string) bool {
	if strings.Count(email, "@") != 1 {
		return false
	}
	emailDomain := ExtractDomain(email)
	d = strings.ToLower(strings.TrimSpace(d))
	return emailDomain != "" && emailDomain == d
}

// DomainMatchesOrg reports whether d is exactly orgDomain, compared
// case-insensitively after trimming spaces. Subdomains do NOT match (PAC-50
// §4.7, ex-PAC-46): sign-in detection routes a domain to its provider row by
// that exact domain, so allowing eu.acme.com for a member of acme.com would let
// them configure login for a domain nobody showed they administer. An empty
// domain never matches.
func DomainMatchesOrg(d, orgDomain string) bool {
	d = strings.ToLower(strings.TrimSpace(d))
	orgDomain = strings.ToLower(strings.TrimSpace(orgDomain))
	return d != "" && d == orgDomain
}

// DeriveOrgName turns a domain base segment into a display name.
// "gorgias.com" → "Gorgias", "acme-corp.io" → "Acme Corp"
func DeriveOrgName(d string) string {
	base := strings.SplitN(d, ".", 2)[0]
	words := strings.Split(base, "-")
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}
