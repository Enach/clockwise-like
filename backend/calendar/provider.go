package calendar

import (
	"context"
	"errors"
	"time"
)

// ErrReadOnly is returned by write methods on read-only providers (e.g. WebCal).
var ErrReadOnly = errors.New("calendar: provider is read-only")

// GenericEvent is the common event representation across all calendar providers.
type GenericEvent struct {
	ID    string    `json:"id"`
	Title string    `json:"title"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	// unreadable marks a feed event whose time or recurrence cannot be read
	// (PAC-122 SF-044). Unexported, so it is never on the wire (every
	// exported field of this struct is a required key, PAC-26).
	unreadable bool
}

// Unreadable reports whether e stands for a feed event whose time or
// recurrence cannot be read (PAC-122 SF-044). Only ID is set (the event's
// UID, which also covers its instances): callers must not treat it as an
// event, nor its absence as the event being gone.
func (e GenericEvent) Unreadable() bool { return e.unreadable }

// Provider is the unified calendar client interface used for multi-provider support.
type Provider interface {
	ListEvents(ctx context.Context, start, end time.Time) ([]GenericEvent, error)
	GetFreeBusy(ctx context.Context, emails []string, start, end time.Time) (map[string][]TimeSlot, error)
	CreateEvent(ctx context.Context, e GenericEvent) (string, error)
}
