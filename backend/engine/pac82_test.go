package engine

// PAC-82 (contracts/features/PAC-82.md section 3): a save that loses, or a
// replay, starts no completion unit: no host event and no email. Two saves of
// one slot released together at afterBookingCommit store one booking, one
// host event and one email (spec AC-1, AC-9, D-10).

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
)

func (w *pac101World) bookKeyed(ctx context.Context, start time.Time, email, key string) (*storage.Booking, error) {
	return (&BookingEngine{DB: w.db}).ConfirmBooking(ctx, w.link, "B", email, start, start.Add(30*time.Minute), "", key)
}

func (w *pac101World) settle(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w.group.Wait(ctx)
}

func TestPAC82_LoserStartsNoCompletionUnit(t *testing.T) {
	w := pac101Fixture(t, "pac82-ac1")
	var arrived atomic.Int32
	all := make(chan struct{})
	afterBookingCommit = func() {
		if arrived.Add(1) == 2 {
			close(all)
		}
		<-all
	}
	defer func() { afterBookingCommit = nil }()
	start := time.Date(2031, 3, 3, 10, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = w.bookKeyed(context.Background(), start, "b@pac82.test", "")
		}(i)
	}
	wg.Wait()
	w.settle(t)
	saved, taken := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			saved++
		case errors.Is(err, ErrSlotTaken):
			taken++
		default:
			t.Fatalf("unexpected: %v", err)
		}
	}
	if saved != 1 || taken != 1 {
		t.Fatalf("saved %d, taken %d", saved, taken)
	}
	if c, _ := w.google.createCount(); c != 1 {
		t.Errorf("%d host events, want 1", c)
	}
	if m := w.mails.Load(); m != 1 {
		t.Errorf("%d emails, want 1", m)
	}
}

func TestPAC82_ReplaySendsNothing(t *testing.T) {
	w := pac101Fixture(t, "pac82-ac9")
	start := time.Date(2031, 3, 3, 11, 0, 0, 0, time.UTC)
	key := "pac82-engine-replay-key-0001"
	first, err := w.bookKeyed(context.Background(), start, "k@pac82.test", key)
	if err != nil || first.Replayed {
		t.Fatalf("first: %+v %v", first, err)
	}
	w.settle(t)
	again, err := w.bookKeyed(context.Background(), start, "k@pac82.test", key)
	if err != nil || !again.Replayed || again.ID != first.ID {
		t.Fatalf("replay: %+v %v", again, err)
	}
	if _, err := w.bookKeyed(context.Background(), start, "other@pac82.test", key); !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Fatalf("other details: %v, want ErrIdempotencyKeyReused", err)
	}
	w.settle(t)
	if c, _ := w.google.createCount(); c != 1 {
		t.Errorf("%d host events, want 1", c)
	}
	if m := w.mails.Load(); m != 1 {
		t.Errorf("%d emails, want 1", m)
	}
	if p := w.group.Pending(); len(p) != 0 {
		t.Errorf("completion units pending: %v", p)
	}
}
