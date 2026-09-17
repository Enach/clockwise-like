package domain

import "testing"

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
		{"sub.acme.com", "acme.com", true},
		{"deep.sub.acme.com", "acme.com", true},
		{"notacme.com", "acme.com", false},
		{"acmeevil.com", "acme.com", false},
		{"ACME.COM", "acme.com", true},
		{"", "acme.com", false},
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
