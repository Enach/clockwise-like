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
	ID    string
	Title string
	Start time.Time
	End   time.Time
}

// Provider is the unified calendar client interface used for multi-provider support.
type Provider interface {
	ListEvents(ctx context.Context, start, end time.Time) ([]GenericEvent, error)
	GetFreeBusy(ctx context.Context, emails []string, start, end time.Time) (map[string][]TimeSlot, error)
	CreateEvent(ctx context.Context, e GenericEvent) (string, error)
}
