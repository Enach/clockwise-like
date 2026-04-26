package auth

import (
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

// DeriveOrgName turns a domain base segment into a display name.
// "gorgias.com" → "Gorgias", "acme-corp.io" → "Acme Corp"
func DeriveOrgName(domain string) string {
	base := strings.SplitN(domain, ".", 2)[0]
	words := strings.Split(base, "-")
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}
