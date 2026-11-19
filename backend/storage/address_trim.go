package storage

import (
	"net/mail"
	"strings"
)

// addressSpace is the address trim set of PAC-129 contract §2.2 (revision
// 1.2, P2-B1, P2-M1): exactly these 26 code points, the union of ECMAScript
// WhiteSpace and LineTerminator and of Go's unicode.IsSpace (which adds
// U+0085; ECMAScript adds U+FEFF). Its SQL twin is migration 041's
// paceday_trim_ecma, built from chr() of the same list. Both are pinned by
// enumeration tests against one table (pac129_trim_test.go).
var addressSpace = map[rune]bool{
	0x0009: true, 0x000A: true, 0x000B: true, 0x000C: true, 0x000D: true,
	0x0020: true, 0x0085: true, 0x00A0: true, 0x1680: true,
	0x2000: true, 0x2001: true, 0x2002: true, 0x2003: true, 0x2004: true,
	0x2005: true, 0x2006: true, 0x2007: true, 0x2008: true, 0x2009: true,
	0x200A: true, 0x2028: true, 0x2029: true, 0x202F: true, 0x205F: true,
	0x3000: true, 0xFEFF: true,
}

// IsAddressSpace reports whether r is in the address trim set.
func IsAddressSpace(r rune) bool { return addressSpace[r] }

// TrimAddressSpace removes the address trim set (contract §2.2) from both
// ends of s, and nothing else.
func TrimAddressSpace(s string) string {
	return strings.TrimFunc(s, IsAddressSpace)
}

// NormaliseStoredAddress is the form every team address is stored in:
// lower(TrimAddressSpace(s)), the Go twin of the CHECKs of migration 041
// (PAC-129 contract §2.2, challenge M-2). Storage applies it on every write
// whatever the caller passed; on the request path the one address normaliser
// has already produced this form, so it is a no-op there.
func NormaliseStoredAddress(s string) string {
	return strings.ToLower(TrimAddressSpace(s))
}

// ValidAddress is the validity rule of the one address normaliser (PAC-129
// contract §3.0, spec PAC-134 §2.3): t is already trimmed of the address
// set; l = strings.ToLower(t) must be non-empty and net/mail must parse l to
// exactly l. It returns l and whether t is valid. The Manager/team
// normaliser (api normalizeManagerEmail) and paceday-operator erase-address
// (PAC-134 contract md §2.1 item 3) both call it, so the two cannot drift.
func ValidAddress(t string) (string, bool) {
	l := strings.ToLower(t)
	if l == "" {
		return "", false
	}
	parsed, err := mail.ParseAddress(l)
	if err != nil || parsed.Address != l {
		return l, false
	}
	return l, true
}
