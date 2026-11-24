// PAC-106: the public booking's settings re-check at the commit point.
//
// POST /api/book/{slug} judges the requested start against a link row read
// before the host calendars are fetched, which can take seconds. The save's
// transaction already waits for an owner's PATCH in progress (it locks the
// link FOR SHARE) but re-checked only that the link is still active and not
// deleted. A check carried here runs inside that transaction, against the row
// read under that lock: a PATCH that committed in between is seen, and the
// booking is refused instead of stored on a slot the new settings would not
// offer.
//
// The rule itself stays with the listing it has to agree with (the PAC-72
// offerable-start rule in the api package); it is never copied into storage.

package storage

import (
	"context"
	"errors"
)

// BookingSettingsCheck re-decides a booking against the link row the save
// read under its FOR SHARE lock. nil lets the save go on; any other error
// ends the save with nothing written, so a refusal must unwrap to
// ErrLinkSettingsChanged to be told apart from a storage failure.
type BookingSettingsCheck func(link *SchedulingLink) error

// ErrLinkSettingsChanged is what a BookingSettingsCheck refusal unwraps to:
// the link's settings changed between the request's read and the save, and
// the slot is no longer one the link offers. Nothing was written. The
// concrete error travels verbatim out of the save, so the caller reads its
// own refusal reason back from it.
var ErrLinkSettingsChanged = errors.New("link settings changed since the request read the link")

type bookingSettingsCheckKey struct{}

// WithBookingSettingsCheck returns ctx carrying check, which
// SaveBookingOnLiveLink applies inside the save transaction (the PAC-71
// pattern: a request-scoped re-check the storage runs under its locks).
func WithBookingSettingsCheck(ctx context.Context, check BookingSettingsCheck) context.Context {
	return context.WithValue(ctx, bookingSettingsCheckKey{}, check)
}

// BookingSettingsCheckFrom returns the check ctx carries, or nil when it
// carries none: a save with no check behaves as before PAC-106 (fixtures and
// the storage tests save without one).
func BookingSettingsCheckFrom(ctx context.Context) BookingSettingsCheck {
	check, _ := ctx.Value(bookingSettingsCheckKey{}).(BookingSettingsCheck)
	return check
}
