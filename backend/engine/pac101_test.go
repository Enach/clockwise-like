package engine

// PAC-101 (contracts/features/PAC-101.md §3.1): the booking save at the commit
// point re-checks the link under a FOR SHARE lock. A link deleted or paused
// after the request read it is the distinct "link gone" outcome: nothing is
// stored, no completion unit starts, and the reserved connection goes back.

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/budget"
	"github.com/Enach/paceday/backend/internal/lifecycle"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// pac101Twin opens an independent pool on db's database, so a test can write
// while db's pool of one is held by the booking.
func pac101Twin(t *testing.T, db *sql.DB) *sql.DB {
	t.Helper()
	var name string
	if err := db.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	dsn := os.Getenv("PACEDAY_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://test:test@127.0.0.1:15432/testdb?sslmode=disable"
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	twin, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = twin.Close() })
	return twin
}

// pac101WithinBound runs f on its own goroutine and fails the test fast,
// instead of hanging, when f does not return within the (shortened) link-lock
// bound plus 5 s: a write that ignores the bound waits forever on a held lock.
// The caller's deferred rollback of the holder then releases the goroutine.
func pac101WithinBound[T any](t *testing.T, what string, f func() T) T {
	t.Helper()
	done := make(chan T, 1)
	go func() { done <- f() }()
	select {
	case v := <-done:
		return v
	case <-time.After(storage.LinkLockTimeout + 5*time.Second):
		t.Fatalf("%s did not return within the bound + 5 s: the link-lock bound is not applied", what)
		panic("unreachable")
	}
}

type pac101World struct {
	db     *sql.DB
	twin   *sql.DB
	link   *storage.SchedulingLink
	google *googleStub
	mails  *atomic.Int32
	group  *lifecycle.Group
}

func pac101Fixture(t *testing.T, label string) *pac101World {
	t.Helper()
	pac81Scale(t)
	g := lifecycle.NewGroup()
	t.Cleanup(lifecycle.SetDefault(g))
	db := openTestDB(t)
	stub := newGoogleStub(t)
	var mails atomic.Int32
	swapSendMail(t, func(string, smtp.Auth, string, []string, []byte) error { mails.Add(1); return nil })
	host, tok := pac81User(t, db, label+"@pac101.test")
	stub.set(tok, stubBehaviour{})
	return &pac101World{db: db, twin: pac101Twin(t, db), link: pac81Link(t, db, []uuid.UUID{host}), google: stub, mails: &mails, group: g}
}

func (w *pac101World) book(ctx context.Context, start time.Time) (*storage.Booking, error) {
	return (&BookingEngine{DB: w.db}).ConfirmBooking(ctx, w.link, "B", "b@pac101.test", start, start.Add(30*time.Minute), "")
}

// assertNothing: no booking row, no completion unit (no host event, no email,
// nothing pending in the shutdown group).
func (w *pac101World) assertNothing(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	w.group.Wait(ctx)
	if p := w.group.Pending(); len(p) != 0 {
		t.Errorf("completion units pending: %v", p)
	}
	var n int
	_ = w.twin.QueryRow(`SELECT COUNT(*) FROM bookings WHERE link_id = $1`, w.link.ID).Scan(&n)
	if n != 0 {
		t.Errorf("%d bookings stored, want 0", n)
	}
	if c, _ := w.google.createCount(); c != 0 {
		t.Errorf("%d host events created, want 0", c)
	}
	if m := w.mails.Load(); m != 0 {
		t.Errorf("%d emails sent, want 0", m)
	}
}

// pac101NextAcquire: with a pool of one, the next acquire is served (the
// save's connection went back).
func pac101NextAcquire(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("pool of 1 did not serve the next acquire within 1 s: %v", err)
	}
	_ = c.Close()
	if s := db.Stats(); s.InUse != 0 {
		t.Fatalf("%d connection(s) still in use", s.InUse)
	}
}

// AC-2 and AC-3: a delete (or a pause) committed after budget.Commit and
// before the save is sent: ErrLinkGone, nothing written, no completion unit,
// and the connection is back in the pool of one.
func TestPAC101_LinkGoneAtTheCommitPoint(t *testing.T) {
	for name, stmt := range map[string]string{
		"delete": `UPDATE scheduling_links SET deleted_at = NOW(), active = false WHERE id = $1`,
		"pause":  `UPDATE scheduling_links SET active = false WHERE id = $1`,
	} {
		t.Run(name, func(t *testing.T) {
			w := pac101Fixture(t, "commit-"+name)
			w.db.SetMaxOpenConns(1)
			afterBookingCommit = func() {
				if _, err := w.twin.Exec(stmt, w.link.ID); err != nil {
					t.Errorf("inject %s: %v", name, err)
				}
			}
			defer func() { afterBookingCommit = nil }()
			b, err := w.book(context.Background(), nextWeekday(time.Now()).Add(10*time.Hour))
			if !errors.Is(err, ErrLinkGone) || b != nil {
				t.Fatalf("ConfirmBooking = %+v, %v; want ErrLinkGone", b, err)
			}
			pac101NextAcquire(t, w.db)
			w.assertNothing(t)
		})
	}
}

// AC-6: the request budget runs out while the save waits for a delete held
// open; the delete then commits. The answer is the handler's own 404, never
// the budget's 503, and nothing is written.
func TestPAC101_BudgetOutWhileWaitingIsStillThe404(t *testing.T) {
	w := pac101Fixture(t, "budget")
	holder, err := w.twin.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback() }()
	var waited atomic.Bool
	afterBookingCommit = func() {
		if _, err := holder.Exec(`UPDATE scheduling_links SET deleted_at = NOW(), active = false WHERE id = $1`, w.link.ID); err != nil {
			t.Errorf("held delete: %v", err)
		}
		go func() {
			// Commit the delete only once the save waits on it and the
			// budget (300 ms) has run out.
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				var n int
				_ = w.twin.QueryRow(`SELECT COUNT(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&n)
				if n > 0 {
					waited.Store(true)
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			time.Sleep(600 * time.Millisecond)
			_ = holder.Commit()
		}()
	}
	defer func() { afterBookingCommit = nil }()
	h := budget.Middleware(300*time.Millisecond, nil)(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		_, err := w.book(r.Context(), nextWeekday(time.Now()).Add(11*time.Hour))
		switch {
		case errors.Is(err, ErrLinkGone):
			rw.WriteHeader(http.StatusNotFound)
		case err != nil:
			rw.WriteHeader(http.StatusInternalServerError)
		default:
			rw.WriteHeader(http.StatusCreated)
		}
	}))
	rec := httptest.NewRecorder()
	began := time.Now()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/book/x", nil))
	if !waited.Load() {
		t.Fatal("the save never waited on the held delete")
	}
	if took := time.Since(began); took < 300*time.Millisecond {
		t.Fatalf("answered after %s, before the budget ran out: the case is not exercised", took)
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want the handler's 404 (never the budget's 503)", rec.Code)
	}
	w.assertNothing(t)
}

// AC-7 (engine half): a lock on the link held past the bound fails the save
// (SQLSTATE 55P03, mapped by the handler to its generic 500), with nothing
// stored and the connection back.
func TestPAC101_SavePastTheBoundFailsAndStoresNothing(t *testing.T) {
	prev := storage.LinkLockTimeout
	storage.LinkLockTimeout = 300 * time.Millisecond
	defer func() { storage.LinkLockTimeout = prev }()
	w := pac101Fixture(t, "bound")
	w.db.SetMaxOpenConns(1)
	holder, err := w.twin.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback() }()
	if _, err := holder.Exec(`SELECT 1 FROM scheduling_links WHERE id = $1 FOR UPDATE`, w.link.ID); err != nil {
		t.Fatal(err)
	}
	type result struct {
		b   *storage.Booking
		err error
	}
	r := pac101WithinBound(t, "the booking save", func() result {
		b, err := w.book(context.Background(), nextWeekday(time.Now()).Add(12*time.Hour))
		return result{b, err}
	})
	b, err := r.b, r.err
	if err == nil || errors.Is(err, ErrLinkGone) || b != nil {
		t.Fatalf("ConfirmBooking = %+v, %v; want a save error (the 500)", b, err)
	}
	_ = holder.Rollback()
	pac101NextAcquire(t, w.db)
	w.assertNothing(t)
}
