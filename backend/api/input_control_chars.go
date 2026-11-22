package api

import (
	"github.com/Enach/paceday/backend/internal/safetext"
	"github.com/Enach/paceday/backend/storage"
)

// The request-path half of the 2026-11-22 security audit's finding 1 (mail
// header and iCalendar injection). The hand-written handlers below accept
// values that a composer later interpolates into one header line or one
// iCalendar content line, where a CR, LF or NUL ends the line and hands the
// rest of the value to the recipient's mail or calendar client as a line of
// its own. Those values are refused here, at the point of entry, because that
// is the user-facing contract: a team name with a line break is a 400, not a
// name silently rewritten on its way out. The composers guard what they
// interpolate as well (internal/safetext: a label is fitted to one line, an
// address that cannot be rendered fails the send), so a caller added later
// cannot reintroduce the injection by skipping this check.
//
// Deliberately narrow: control characters on the named fields, plus an
// address shape and a length ceiling where an unbounded value reaches a
// header. The contract's per-field maxLength is enforced elsewhere, by the
// generated strict wrapper.

const (
	// msgNoLineBreaks refuses a name — a team's or a booker's — that would
	// end the header line it is interpolated into.
	msgNoLineBreaks = "name must not contain line breaks"
	// msgTitleNoLineBreaks refuses a scheduling link's title, which reaches
	// the confirmation's Subject and the invitation's SUMMARY.
	msgTitleNoLineBreaks = "title must not contain line breaks"
	// msgInvalidBookerEmail refuses a booker address that is not exactly one
	// address: it becomes the confirmation's To header and the invitation's
	// ATTENDEE.
	msgInvalidBookerEmail = "email must be a valid email address"
)

// maxAddressLen is the longest address a forward path may hold (RFC 5321
// §4.5.3.1.3). A sanity ceiling, not the contract's maxLength. It is the
// composers' own limit (safetext.MaxAddressLen) so the two cannot drift: an
// address this gate lets through is one they can render whole, and neither
// layer ever shortens one (D-15 of
// docs/specs/NEW-request-field-length-limits.md).
const maxAddressLen = safetext.MaxAddressLen

// validBookerAddress reports whether s is exactly one address and nothing
// else: no display name, no second address, no line break, and no surrounding
// space of any kind. The booker's address is stored and answered as it was
// sent and is the To header of the confirmation email, so it is checked as
// sent rather than as it would look trimmed and lower-cased — which is why
// this is not storage.ValidAddress alone, though the shape it accepts is the
// same one every team address goes through.
//
// The shape is net/mail's, so a non-ASCII local part ("josé@example.com") is
// accepted: this refuses what cannot go in a header, not what looks unusual.
func validBookerAddress(s string) bool {
	if s == "" || len(s) > maxAddressLen || safetext.HasControl(s) || s != storage.TrimAddressSpace(s) {
		return false
	}
	_, ok := storage.ValidAddress(s)
	return ok
}
