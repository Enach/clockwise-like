// Package safetext holds the one rule every line-oriented composer needs: a
// value interpolated into a line must not be able to end that line.
//
// Mail header lines end at the first CRLF (RFC 5322 §2.2) and iCalendar
// content lines at the first CRLF (RFC 5545 §3.1). A value carrying CR, LF or
// NUL therefore closes the line it was interpolated into and starts whatever
// the rest of that value says: a header of the sender's choosing on a message
// the deployment's relay signs as ours, a MIME part, or a second ATTENDEE or
// replacement ORGANIZER on an invitation the recipient's calendar client
// parses without asking.
//
// The two layers are deliberate. The request path refuses those characters
// (HasControl) because that is the user-facing contract: a name with a line
// break is a 400, not a silently rewritten name. The composers call Label or
// Address on every value they interpolate, so a caller added later cannot
// reintroduce the injection by skipping the entry check.
//
// Labels and addresses are treated differently, and the difference matters
// (docs/specs/NEW-request-field-length-limits.md §3.2, whose reason 2 is this
// one, recorded for addresses as decision D-15). A label — a team name, a
// person's name, a meeting title — is prose: cutting it short
// is ugly but harmless, so Label repairs and caps it. An address is an
// identifier: a truncated address is a different person's valid address, and a
// truncated mailto: is a different recipient, so Address never repairs or cuts
// one. It refuses, and the caller fails the send. A confirmation that does not
// arrive is recoverable; one delivered to a stranger is not.
package safetext

import (
	"errors"
	"fmt"
	"strings"
)

// MaxLen caps one interpolated label, in runes. RFC 5322 §2.1.1 allows 998
// octets in a line and RFC 5545 §3.1 folds at 75 octets: a team name, a
// person's name or a meeting title longer than this is not a legitimate
// value, and leaving it uncapped lets unbounded input produce a line no
// relay will accept.
//
// It is a render guard, not an entry gate: it shortens the outgoing header or
// content line and never the stored value, which keeps whatever was written.
//
// There is no entry limit on these fields today. Nothing in the backend
// bounds a team name, a link title or a person's name, and the contract
// declares no maxLength on any of them, so a value of roughly a megabyte (the
// body cap) can be stored and arrive here. That is what this cap is for, and
// it is why it truncates instead of refusing: refusing at this point would
// drop a message over data that was already accepted and stored, long after
// anyone could be told. Declaring and enforcing entry limits is separate,
// unapproved work (docs/specs/NEW-request-field-length-limits.md); its rule
// for a request value is refuse, never truncate, which is the right rule at
// that boundary and not at this one.
const MaxLen = 200

// MaxAddressLen is the longest address a forward path may hold (RFC 5321
// §4.5.3.1.3). Addresses are bounded here and at every entry gate, never cut.
const MaxAddressLen = 254

// ErrUnsafeAddress is returned by Address for an address that cannot be put
// in a header as it is. The send fails with it rather than delivering to an
// address nobody asked for.
var ErrUnsafeAddress = errors.New("address cannot be used in a mail header")

// HasControl reports whether s holds a character that ends a header or
// content line: CR, LF or NUL.
func HasControl(s string) bool {
	return strings.ContainsAny(s, "\r\n\x00")
}

// Label returns s fit to interpolate into one header or content line as prose:
// each run of CR, LF and NUL becomes a single space (so a CRLF pair does not
// leave two), the result is trimmed, and it is cut to MaxLen runes. It is
// idempotent, so a composer may call it on a value the request path already
// checked.
//
// Never pass an address to Label: use Address.
func Label(s string) string {
	if HasControl(s) {
		var b strings.Builder
		b.Grow(len(s))
		breaking := false
		for _, r := range s {
			if r == '\r' || r == '\n' || r == 0 {
				if !breaking {
					b.WriteRune(' ')
					breaking = true
				}
				continue
			}
			breaking = false
			b.WriteRune(r)
		}
		s = b.String()
	}
	s = strings.TrimSpace(s)
	if runes := []rune(s); len(runes) > MaxLen {
		s = strings.TrimSpace(string(runes[:MaxLen]))
	}
	return s
}

// Address returns addr fit to interpolate into a recipient header or a
// mailto: value — unchanged but for surrounding space — or ErrUnsafeAddress
// when it is not: an address holding CR, LF or NUL, or longer than
// MaxAddressLen octets. It repairs nothing and truncates nothing, because
// every repair of an address names somebody else; the caller fails the send
// and reports it (the address itself is never logged).
func Address(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	switch {
	case addr == "":
		return "", fmt.Errorf("%w: empty", ErrUnsafeAddress)
	case HasControl(addr):
		return "", fmt.Errorf("%w: it holds a line break", ErrUnsafeAddress)
	case len(addr) > MaxAddressLen:
		return "", fmt.Errorf("%w: %d octets, over the %d-octet limit", ErrUnsafeAddress, len(addr), MaxAddressLen)
	}
	return addr, nil
}
