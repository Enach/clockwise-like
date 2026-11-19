package api

import (
	"testing"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// PAC-71 AC-38's break-glass rule, unit-tested directly (PAC-148 review,
// non-blocking 1): since PAC-148 an unconfirmed holder of a listed address
// is refused at gate step 1 (membership) before breakGlassAccount runs, so
// TestPAC71_AC38 no longer reaches it for that case. The rule itself still
// authorises only the account that holds the listed address
// domain-confirmed.
func TestPAC148_BreakGlassAccountNeedsADomainConfirmedHolder(t *testing.T) {
	h := &ssoHandlers{breakGlass: map[string]bool{"admin@acme.example": true}}
	for _, tc := range []struct {
		name string
		user *storage.User
		want bool
	}{
		{"no user", nil, false},
		{"listed, domain-confirmed", &storage.User{ID: uuid.New(), Email: "admin@acme.example", AddressStanding: storage.AddressDomainConfirmed}, true},
		{"listed, case and white space, domain-confirmed", &storage.User{ID: uuid.New(), Email: " Admin@ACME.example ", AddressStanding: storage.AddressDomainConfirmed}, true},
		{"listed, unconfirmed", &storage.User{ID: uuid.New(), Email: "admin@acme.example", AddressStanding: storage.AddressUnconfirmed}, false},
		{"listed, no standing read", &storage.User{ID: uuid.New(), Email: "admin@acme.example"}, false},
		{"not listed, domain-confirmed", &storage.User{ID: uuid.New(), Email: "other@acme.example", AddressStanding: storage.AddressDomainConfirmed}, false},
		{"freed address", &storage.User{ID: uuid.New(), Email: storage.FreedAddress(uuid.New()), AddressStanding: storage.AddressUnconfirmed}, false},
	} {
		if got := h.breakGlassAccount(tc.user); got != tc.want {
			t.Errorf("%s: breakGlassAccount = %v, want %v", tc.name, got, tc.want)
		}
	}
}
