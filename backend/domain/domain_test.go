package domain

import (
	"strings"
	"testing"
)

func TestExtractDomain(t *testing.T) {
	cases := []struct {
		email string
		want  string
	}{
		{"alice@example.com", "example.com"},
		{"bob@ACME.IO", "acme.io"},
		{"no-at-sign", ""},
		{"", ""},
		{"user@sub.domain.co.uk", "sub.domain.co.uk"},
	}
	for _, c := range cases {
		got := ExtractDomain(c.email)
		if got != c.want {
			t.Errorf("ExtractDomain(%q) = %q, want %q", c.email, got, c.want)
		}
	}
}

func TestIsGenericDomain(t *testing.T) {
	for _, d := range []string{"gmail.com", "outlook.com", "yahoo.com", "icloud.com", "protonmail.com"} {
		if !IsGenericDomain(d) {
			t.Errorf("IsGenericDomain(%q) = false, want true", d)
		}
	}
	for _, d := range []string{"acme.com", "gorgias.com", "example.io"} {
		if IsGenericDomain(d) {
			t.Errorf("IsGenericDomain(%q) = true, want false", d)
		}
	}
}

func TestDomainMatchesOrg(t *testing.T) {
	cases := []struct {
		d, org string
		want   bool
	}{
		{"acme.com", "acme.com", true},
		// PAC-50 AC-15 (ex-PAC-46): the rule is exact. A subdomain is a
		// domain nobody in the org was shown to administer.
		{"evil-acme.com", "acme.com", false},
		{"eu.acme.com", "acme.com", false},
		{"ACME.com", "acme.com", true},
		{"sub.acme.com", "acme.com", false},
		{"deep.sub.acme.com", "acme.com", false},
		{"notacme.com", "acme.com", false},
		{"acmeevil.com", "acme.com", false},
		{"ACME.COM", "acme.com", true},
		// PAC-50 D-9: compared as sent, no trimming.
		{" acme.com ", "ACME.com", false},
		{"", "acme.com", false},
		{"", "", false},
		// PAC-75 AC-6 (contract md 2.1): ASCII letters fold, nothing else.
		{"acmeſ.com", "acmes.com", false},           // U+017F long s
		{"ACMEſ.COM", "acmes.com", false},           // U+017F long s
		{"Kcme.com", "kcme.com", false},        // U+212A Kelvin sign
		{"ａｃｍｅ.com", "acme.com", false},             // fullwidth letters
		{"ａcme.com", "acme.com", false},              // one fullwidth letter
		{"İstanbul.com", "istanbul.com", false},     // U+0130
		{"ıstanbul.com", "istanbul.com", false},     // U+0131
		{"ISTANBUL.COM", "istanbul.com", true},      // ASCII
		{"ıstanbul.com", "ıstanbul.com", true},      // non-ASCII org, same bytes
		{"Istanbul.com", "ıstanbul.com", false},     // ı is not i
		{"istanbul.com", "ıstanbul.com", false},     // ı is not i
		{"STRAßE.DE", "straße.de", true},            // ASCII fold, ß same bytes
		{"STRASSE.DE", "straße.de", false},          // no full case folding
		{"straẞe.de", "straße.de", false},           // U+1E9E
		{"ÉCOLE.fr", "école.fr", false},             // non-ASCII upper case of the org's letter
		{"école.fr", "école.fr", true},              // exact bytes
		{"XN--BCHER-KVA.EXAMPLE", "xn--bcher-kva.example", true},
		{"bücher.example", "xn--bcher-kva.example", false}, // no IDNA
		{"acme.com.", "acme.com", false},
	}
	for _, c := range cases {
		got := DomainMatchesOrg(c.d, c.org)
		if got != c.want {
			t.Errorf("DomainMatchesOrg(%q, %q) = %v, want %v", c.d, c.org, got, c.want)
		}
	}
}

func TestEmailBelongsToDomain(t *testing.T) {
	cases := []struct {
		email, d string
		want     bool
	}{
		{"alice@acme.com", "acme.com", true},
		{"Alice@ACME.com", "acme.com", true},
		{"alice@acme.com", " ACME.COM ", true},
		// An IdP configured for acme.com asserting another domain's account.
		{"victim@example.com", "acme.com", false},
		// Subdomains and look-alikes are separate provider rows.
		{"alice@eu.acme.com", "acme.com", false},
		{"alice@acme.com", "eu.acme.com", false},
		{"alice@notacme.com", "acme.com", false},
		// Smuggling a second address past the domain extraction.
		{"victim@example.com@acme.com", "acme.com", false},
		{"victim@acme.com@example.com", "acme.com", false},
		{"acme.com", "acme.com", false},
		{"", "acme.com", false},
		{"alice@", "", false},
	}
	for _, c := range cases {
		got := EmailBelongsToDomain(c.email, c.d)
		if got != c.want {
			t.Errorf("EmailBelongsToDomain(%q, %q) = %v, want %v", c.email, c.d, got, c.want)
		}
	}
}

func TestDeriveOrgName(t *testing.T) {
	cases := []struct {
		domain string
		want   string
	}{
		{"gorgias.com", "Gorgias"},
		{"acme-corp.io", "Acme Corp"},
		{"my-big-co.com", "My Big Co"},
		{"simple.net", "Simple"},
	}
	for _, c := range cases {
		got := DeriveOrgName(c.domain)
		if got != c.want {
			t.Errorf("DeriveOrgName(%q) = %q, want %q", c.domain, got, c.want)
		}
	}
}

// TestIsQueryableDNSName is PAC-97 AC-13's name rule, on its own.
func TestIsQueryableDNSName(t *testing.T) {
	long := strings.Repeat("a", 63)
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"gorgias.com", true},
		{"GOOGLE-CASE.TEST", true},
		{"a.b", true},
		{"xn--exmple-cua.com", true}, // punycode is ASCII and may be queried
		{long + ".test", true},
		{strings.Repeat(long+".", 3) + strings.Repeat("a", 61), true},  // 253
		{strings.Repeat(long+".", 3) + strings.Repeat("a", 62), false}, // 254
		{long + "a.test", false}, // 64-char label
		{"", false},
		{"intranet", false},
		{"exa mple.com", false},
		{"-bad-.test", false},
		{"bad-.test", false},
		{"-bad.test", false},
		{"exämple.com", false},
		{"under_score.test", false},
		{"gorgias.com.", false},
		{".gorgias.com", false},
		{"a..b", false},
		{"a@b.test", false},
	} {
		if got := IsQueryableDNSName(tc.in); got != tc.want {
			t.Errorf("IsQueryableDNSName(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// PAC-98: GenericDomains enumerates exactly the consumer list, sorted, and
// returns a copy.
func TestGenericDomains(t *testing.T) {
	list := GenericDomains()
	if len(list) != len(genericDomains) {
		t.Fatalf("len = %d, want %d", len(list), len(genericDomains))
	}
	for i, d := range list {
		if !IsGenericDomain(d) {
			t.Errorf("%q is not on the consumer list", d)
		}
		if i > 0 && list[i-1] >= d {
			t.Errorf("not sorted at %d: %q >= %q", i, list[i-1], d)
		}
	}
	list[0] = "evil.example"
	if IsGenericDomain("evil.example") {
		t.Error("changing the returned slice changed the consumer list")
	}
}
