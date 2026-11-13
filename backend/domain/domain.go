package domain

import (
	"sort"
	"strings"
)

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

// GenericDomains returns a sorted copy of the consumer list, for tests that
// must cover every entry (PAC-98 AC-3, AC-21). Changing the copy changes
// nothing.
func GenericDomains() []string {
	out := make([]string, 0, len(genericDomains))
	for d := range genericDomains {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
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
// case-insensitively and as sent: no whitespace is trimmed, so " acme.com "
// is another domain (PAC-50 spec §9 D-9). Subdomains do NOT match (PAC-50
// §4.7, ex-PAC-46): sign-in detection routes a domain to its provider row by
// that exact domain, so allowing eu.acme.com for a member of acme.com would let
// them configure login for a domain nobody showed they administer. An empty
// domain never matches.
func DomainMatchesOrg(d, orgDomain string) bool {
	return d != "" && strings.EqualFold(d, orgDomain)
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

// IsQueryableDNSName reports whether d is a name sign-in detection may send to
// a DNS resolver (PAC-97 AC-13, D-7): ASCII letters, digits and hyphens only,
// at least two labels, each label 1-63 characters that neither starts nor
// ends with a hyphen, and at most 253 characters in all. A trailing dot, an
// empty label, a single label ("intranet"), whitespace, an underscore and any
// non-ASCII character are refused, so a name that is not a public host name
// never leaves the server.
func IsQueryableDNSName(d string) bool {
	if d == "" || len(d) > 253 {
		return false
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if len(l) == 0 || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for i := 0; i < len(l); i++ {
			c := l[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
