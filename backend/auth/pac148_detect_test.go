package auth

import (
	"net"
	"testing"

	"github.com/Enach/paceday/backend/domain"
)

// pac148AddedDomains are the 17 Microsoft personal-account domains PAC-148
// adds to the consumer list (contract md §4).
var pac148AddedDomains = []string{
	"hotmail.fr", "hotmail.co.uk", "hotmail.de", "hotmail.it", "hotmail.es", "hotmail.be",
	"live.fr", "live.co.uk", "live.de", "live.it", "live.nl", "live.be",
	"outlook.fr", "outlook.de", "outlook.it", "outlook.es",
	"windowslive.com",
}

// PAC-148 AC-11 (detection half): each added domain is on the consumer
// list and answers generic with no SSO read and no MX query, even when the
// resolver would answer with the Outlook.com consumer host (which the MX
// path answers choose: before PAC-148, these domains took that path).
func TestPAC148_AC11_MicrosoftPersonalDomainsAreGeneric(t *testing.T) {
	f := newFakeMX()
	for _, dom := range pac148AddedDomains {
		f.set(dom, fakeAnswer{records: []*net.MX{{Host: "eur.olc.protection.outlook.com.", Pref: 10}}})
	}
	d := newTestDetector(f)
	for _, dom := range pac148AddedDomains {
		if !domain.IsGenericDomain(dom) {
			t.Errorf("%s is not on the consumer list", dom)
		}
		if got := pac98Detect(t, d, "erin@"+dom).Type; got != DetectGeneric {
			t.Errorf("%s = %q, want %q", dom, got, DetectGeneric)
		}
	}
	if n := f.count(); n != 0 {
		t.Errorf("MX queries = %d, want 0", n)
	}
	// The same answer for an unlisted domain takes the MX path (choose), so
	// the generic answers above come from the list, not from the resolver.
	f.set("olc-unlisted.test", fakeAnswer{records: []*net.MX{{Host: "eur.olc.protection.outlook.com.", Pref: 10}}})
	if got := detectType(t, d, t.Context(), "erin@olc-unlisted.test"); got != DetectChoose {
		t.Errorf("an unlisted domain on the consumer host = %q, want %q", got, DetectChoose)
	}
}
