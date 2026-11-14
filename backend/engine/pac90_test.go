package engine

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/budget"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// PAC-90 item 1: the booking's reserved connection is released on every exit
// path, a panic included, without ever blocking the handler on an operation
// the panic interrupted (spec challenge C-1).

type pac90Panic struct{ where string }

// pac90Book runs one booking behind the request budget, as the booking
// route does. It returns the value the budget middleware re-panicked with
// (nil when none) and when the handler goroutine itself returned.
func pac90Book(t *testing.T, db *sql.DB, link *storage.SchedulingLink, start time.Time) (any, <-chan struct{}, *storage.Booking, error) {
	t.Helper()
	handlerDone := make(chan struct{})
	var booking *storage.Booking
	var engErr error
	h := budget.Middleware(5*time.Second, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(handlerDone)
		booking, engErr = (&BookingEngine{DB: db}).ConfirmBooking(r.Context(), link, "B", "b@pac90.test", start, start.Add(30*time.Minute), "", "")
	}))
	var repanicked any
	func() {
		defer func() { repanicked = recover() }()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/book/x", nil))
	}()
	return repanicked, handlerDone, booking, engErr
}

func pac90WaitInUseZero(t *testing.T, db *sql.DB, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for db.Stats().InUse != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("pool still has %d connection(s) in use %s after the panic", db.Stats().InUse, within)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func pac90Fixture(t *testing.T, label string) (*sql.DB, *storage.SchedulingLink) {
	t.Helper()
	pac81Scale(t)
	db := openTestDB(t)
	g := newGoogleStub(t)
	swapSendMail(t, func(string, smtp.Auth, string, []string, []byte) error { return nil })
	host, tok := pac81User(t, db, label+"@pac90.test")
	g.set(tok, stubBehaviour{})
	return db, pac81Link(t, db, []uuid.UUID{host})
}

// AC-1a: a panic after the commit and before the insert (outside any
// database call on the reserved connection) still reaches the server, and
// the connection goes back: in use 0 within 1 s, and a pool of 1 serves the
// next acquire within 1 s.
func TestPAC90_BookingPanicOutsideQueryReleasesConn(t *testing.T) {
	db, link := pac90Fixture(t, "panic-outside")
	db.SetMaxOpenConns(1)
	want := &pac90Panic{where: "after commit"}
	afterBookingCommit = func() { panic(want) }
	defer func() { afterBookingCommit = nil }()

	start := nextWeekday(time.Now()).Add(10 * time.Hour)
	got, handlerDone, _, _ := pac90Book(t, db, link, start)
	if got != want {
		t.Fatalf("re-panicked with %v, want the save step's panic", got)
	}
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("handler goroutine did not return within 1 s")
	}
	pac90WaitInUseZero(t, db, time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("pool of 1 did not serve the next acquire within 1 s: %v", err)
	}
	defer c.Close()
	var one int
	if err := c.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("query on the released connection: %v (%d)", err, one)
	}
	var n int
	_ = c.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookings WHERE link_id = $1`, link.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("%d bookings written, want 0 (the panic came before the insert)", n)
	}
}

// AC-1b: a panic after the insert's *Row is returned and before it is
// scanned: the handler goroutine returns within 1 s (a plain deferred Close
// would wait forever on the Conn's in-flight query), the panic reaches the
// server, and a pool of 2 completes the next booking within 2 s.
func TestPAC90_BookingPanicMidQueryHandlerReturns(t *testing.T) {
	db, link := pac90Fixture(t, "panic-mid")
	db.SetMaxOpenConns(2)
	want := &pac90Panic{where: "before scan"}
	var fired atomic.Bool
	bookingInsertRowReturned = func() {
		if fired.CompareAndSwap(false, true) {
			panic(want)
		}
	}
	defer func() { bookingInsertRowReturned = nil }()

	start := nextWeekday(time.Now()).Add(11 * time.Hour)
	repanic := make(chan any, 1)
	var handlerDone <-chan struct{}
	ready := make(chan struct{})
	go func() {
		var got any
		got, handlerDone, _, _ = pac90Book(t, db, link, start)
		close(ready)
		repanic <- got
	}()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("the panic did not reach the server within 1 s (handler goroutine hung on the release)")
	}
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("handler goroutine did not return within 1 s")
	}
	// Beyond AC-1b's minimum (review of #258, finding 1): cancelling the
	// insert's context closes the unscanned row, so the interrupted
	// connection itself goes back to the pool.
	pac90WaitInUseZero(t, db, time.Second)
	if got := <-repanic; got != want {
		t.Fatalf("re-panicked with %v, want the save step's panic", got)
	}

	began := time.Now()
	got, _, booking, err := pac90Book(t, db, link, start.Add(time.Hour))
	if got != nil || err != nil || booking == nil {
		t.Fatalf("following booking: panic %v, err %v, booking %v", got, err, booking)
	}
	if took := time.Since(began); took > 2*time.Second {
		t.Fatalf("following booking took %s, want within 2 s", took)
	}
}

// The release is not a function-scoped defer: with the pool capped at 1 a
// normal booking still gives its connection back before the completion unit
// needs one (AC-2's guard at engine level; the api test is unchanged).
func TestPAC90_BookingReleasesBeforeCompletionOnPoolOfOne(t *testing.T) {
	db, link := pac90Fixture(t, "pool-one")
	db.SetMaxOpenConns(1)
	start := nextWeekday(time.Now()).Add(12 * time.Hour)
	began := time.Now()
	got, _, booking, err := pac90Book(t, db, link, start)
	if got != nil || err != nil || booking == nil {
		t.Fatalf("booking: panic %v, err %v", got, err)
	}
	if took := time.Since(began); took > 2*time.Second {
		t.Fatalf("booking on a pool of 1 took %s (connection held across the completion unit)", took)
	}
}
