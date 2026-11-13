package auth

import (
	"context"
	"testing"

	"github.com/Enach/paceday/backend/domain"
	"github.com/Enach/paceday/backend/storage"
)

// PAC-98 (docs/specs/PAC-98.md §4 AC-1..AC-5, contracts/features/PAC-98.yaml):
// Paceday has no password sign-in. A consumer-list address is answered google
// (gmail.com, googlemail.com) or generic (a personal address; the client offers
// the providers that accept personal accounts); an address without a domain is
// choose. None of them reads the SSO table or queries DNS.

// pac98Detect runs detection with an SSO-row lookup that fails the test if it
// is called, and returns the answer.
func pac98Detect(t *testing.T, d *Detector, email string) *DetectResult {
	t.Helper()
	res, err := d.detect(context.Background(), func(string) (*storage.SSOProvider, error) {
		t.Errorf("%s: the SSO row was read", email)
		return nil, nil
	}, email)
	if err != nil {
		t.Fatalf("detect(%q): %v", email, err)
	}
	if res.ProviderName != "" || res.RedirectURL != "" {
		t.Errorf("detect(%q) = %+v: carries a provider name or a redirect path", email, res)
	}
	return res
}

// AC-1: gmail.com and googlemail.com, in any case, are google with no query
// and no SSO read.
func TestPAC98_AC1_GmailIsGoogleWithoutAQuery(t *testing.T) {
	f := newFixtureFake()
	d := newTestDetector(f)
	for _, email := range []string{"carol@gmail.com", "Carol@GMail.com", "dan@googlemail.com"} {
		if got := pac98Detect(t, d, email).Type; got != DetectGoogle {
			t.Errorf("%s = %q, want %q", email, got, DetectGoogle)
		}
	}
	if n := f.count(); n != 0 {
		t.Errorf("queries = %d, want 0", n)
	}
}

// AC-2: the four Microsoft consumer domains are the personal-address answer,
// not microsoft: Paceday refuses personal Microsoft accounts until PAC-77.
// PAC-77 changes this expectation to microsoft.
func TestPAC98_AC2_MicrosoftConsumerDomainsStayGenericUntilPAC77(t *testing.T) {
	f := newFixtureFake()
	d := newTestDetector(f)
	for _, dom := range []string{"outlook.com", "hotmail.com", "live.com", "msn.com"} {
		if got := pac98Detect(t, d, "erin@"+dom).Type; got != DetectGeneric {
			t.Errorf("%s = %q, want %q (until PAC-77)", dom, got, DetectGeneric)
		}
	}
	if n := f.count(); n != 0 {
		t.Errorf("queries = %d, want 0", n)
	}
}

// pac98ConsumerExpectations is AC-3's table. It is checked against the list
// itself, so a new consumer-list entry without a row here fails the test.
var pac98ConsumerExpectations = map[string]string{
	"gmail.com":      DetectGoogle,
	"googlemail.com": DetectGoogle,
	"outlook.com":    DetectGeneric, // PAC-77 flips the four Microsoft domains
	"hotmail.com":    DetectGeneric,
	"live.com":       DetectGeneric,
	"msn.com":        DetectGeneric,
	"yahoo.com":      DetectGeneric,
	"yahoo.fr":       DetectGeneric,
	"yahoo.co.uk":    DetectGeneric,
	"icloud.com":     DetectGeneric,
	"me.com":         DetectGeneric,
	"mac.com":        DetectGeneric,
	"protonmail.com": DetectGeneric,
	"proton.me":      DetectGeneric,
	"aol.com":        DetectGeneric,
	"mail.com":       DetectGeneric,
	"gmx.com":        DetectGeneric,
	"yandex.com":     DetectGeneric,
	"yandex.ru":      DetectGeneric,
	"zoho.com":       DetectGeneric,
	"fastmail.com":   DetectGeneric,
}

// AC-3: every entry of the consumer list, enumerated from the domain package.
func TestPAC98_AC3_EveryConsumerEntry(t *testing.T) {
	f := newFixtureFake()
	d := newTestDetector(f)
	list := domain.GenericDomains()
	if len(list) == 0 {
		t.Fatal("the consumer list is empty")
	}
	seen := map[string]bool{}
	for _, dom := range list {
		seen[dom] = true
		want, ok := pac98ConsumerExpectations[dom]
		if !ok {
			t.Errorf("consumer-list entry %q has no row in the PAC-98 table: decide google or generic", dom)
			continue
		}
		if got := pac98Detect(t, d, "user@"+dom).Type; got != want {
			t.Errorf("%s = %q, want %q", dom, got, want)
		}
	}
	for dom := range pac98ConsumerExpectations {
		if !seen[dom] {
			t.Errorf("the table names %q, which is not on the consumer list", dom)
		}
	}
	for dom := range googleConsumerDomains {
		if !domain.IsGenericDomain(dom) {
			t.Errorf("%q is answered google as a consumer domain but is not on the consumer list", dom)
		}
	}
	if n := f.count(); n != 0 {
		t.Errorf("queries = %d, want 0", n)
	}
}

// AC-4: an address without an extractable domain is choose, never generic.
func TestPAC98_AC4_NoDomainIsChoose(t *testing.T) {
	f := newFixtureFake()
	d := newTestDetector(f)
	for _, email := range []string{"not-an-email", "alice@", "@", ""} {
		if got := pac98Detect(t, d, email).Type; got != DetectChoose {
			t.Errorf("%q = %q, want %q", email, got, DetectChoose)
		}
	}
	if n := f.count(); n != 0 {
		t.Errorf("queries = %d, want 0", n)
	}
}

// AC-5: an SSO row for gmail.com never applies: the consumer rule is checked
// first, as before PAC-98, now with the google answer.
func TestPAC98_AC5_ConsumerRuleBeforeSSO(t *testing.T) {
	db := openTestDB(t)
	if _, err := storage.UpsertSSOProvider(db, &storage.SSOProvider{
		Domain: "gmail.com", ProviderName: "Rogue IdP", ProviderType: "oidc",
		Enabled: true, OIDCIssuer: "https://idp.rogue.test",
	}); err != nil {
		t.Fatalf("UpsertSSOProvider: %v", err)
	}
	f := newFixtureFake()
	res, err := newTestDetector(f).Detect(context.Background(), db, "carol@gmail.com")
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Type != DetectGoogle || res.ProviderName != "" || res.RedirectURL != "" {
		t.Errorf("result = %+v, want google without provider name or redirect path", res)
	}
	if n := f.count(); n != 0 {
		t.Errorf("queries = %d, want 0", n)
	}
}
