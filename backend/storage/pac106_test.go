package storage

// PAC-106 (issue #339): the booking save re-decides the slot against the link
// row it reads under its FOR SHARE lock, so an owner's PATCH that commits
// while the host calendars are read cannot leave a booking stored on a slot
// the new settings would not offer. These tests hold the PATCH's transaction
// open and observe the save waiting for it in pg_stat_activity, never by
// sleeping alone.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// pac106Save is the save with ctx (the check it carries included) on its own
// pooled connection.
func pac106Save(ctx context.Context, db *sql.DB, linkID uuid.UUID, start time.Time, key string) (*Booking, error) {
	conn, err := db.Conn(context.Background())
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	return SaveBookingOnLiveLink(ctx, conn, &Booking{LinkID: linkID, BookerName: "Bo", BookerEmail: "bo@booker.test",
		StartTime: start, EndTime: start.Add(30 * time.Minute), IdempotencyKey: key}, nil)
}

// pac106Refusal is a refusal shaped as the api package's: it wraps
// ErrLinkSettingsChanged so the save returns it verbatim.
func pac106Refusal() error {
	return fmt.Errorf("pac106 test refusal: %w", ErrLinkSettingsChanged)
}

// The check is applied to the row read under the save's own lock, not to the
// row as it was when the booking request read it: a PATCH held open makes the
// save wait, and once it commits the check is handed its settings. The
// refusal then ends the save with nothing written.
func TestPAC106_CheckSeesTheSettingsAPatchCommittedWhileTheSaveWaited(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "pac106-locked-row@example.com")
	link := pac40Link(t, db, owner, "pac106-locked-row")

	patch, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = patch.Rollback() }()
	if _, err := patch.Exec(`UPDATE scheduling_links
		SET window_start_time = '13:00', duration_options = '{60}', booking_horizon_days = 1, min_notice_minutes = 90
		WHERE id = $1`, link.ID); err != nil {
		t.Fatal(err)
	}

	// The row the check is handed travels on a channel: the check runs on
	// the save's goroutine while this one watches the lock waiter.
	seenCh := make(chan *SchedulingLink, 1)
	ctx := WithBookingSettingsCheck(context.Background(), func(l *SchedulingLink) error {
		seenCh <- l
		return pac106Refusal()
	})
	done := make(chan error, 1)
	go func() { _, err := pac106Save(ctx, db, link.ID, pac101Start(), ""); done <- err }()
	pac101AwaitWaiters(t, db, 1) // the save waits for the PATCH's row lock
	select {
	case l := <-seenCh:
		t.Fatalf("the check ran before the save had the link locked: %+v", l)
	default:
	}
	if err := patch.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrLinkSettingsChanged) {
		t.Fatalf("save under a refusing check: %v; want an error wrapping ErrLinkSettingsChanged", err)
	}
	var seen *SchedulingLink
	select {
	case seen = <-seenCh:
	default:
		t.Fatal("the check never ran")
	}
	if !strings.HasPrefix(seen.WindowStart, "13:00") || seen.BookingHorizonDays != 1 || seen.MinNoticeMinutes != 90 ||
		len(seen.DurationOptions) != 1 || seen.DurationOptions[0] != 60 {
		t.Errorf("the check saw window %q, horizon %d, notice %d, durations %v; want the settings the PATCH committed",
			seen.WindowStart, seen.BookingHorizonDays, seen.MinNoticeMinutes, seen.DurationOptions)
	}
	if seen.DaysOfWeek == nil {
		t.Error("the check saw no days of the week: the locked read must carry every setting the rule reads")
	}
	if n := pac101Bookings(t, db, link.ID); n != 0 {
		t.Errorf("%d bookings stored though the check refused", n)
	}
}

// A check that passes changes nothing: the booking is saved as without one.
func TestPAC106_APassingCheckSavesTheBooking(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "pac106-passing@example.com")
	link := pac40Link(t, db, owner, "pac106-passing")
	ran := 0
	ctx := WithBookingSettingsCheck(context.Background(), func(l *SchedulingLink) error {
		ran++
		if l.ID != link.ID {
			t.Errorf("the check saw link %s, want %s", l.ID, link.ID)
		}
		return nil
	})
	b, err := pac106Save(ctx, db, link.ID, pac101Start(), "")
	if err != nil || b == nil {
		t.Fatalf("save under a passing check: %+v, %v", b, err)
	}
	if ran != 1 {
		t.Errorf("the check ran %d times, want once", ran)
	}
	if n := pac101Bookings(t, db, link.ID); n != 1 {
		t.Errorf("%d bookings stored, want 1", n)
	}
}

// The check comes after the idempotency key is decided (step 6), so a keyed
// retry of a booking already stored replays it instead of being refused by
// settings that changed after it was saved. Nothing is written either way.
func TestPAC106_AStoredKeyStillReplaysUnderARefusingCheck(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "pac106-replay@example.com")
	link := pac40Link(t, db, owner, "pac106-replay")
	key := pac82Key("pac106-replay")
	start := pac101Start()
	first, err := pac106Save(context.Background(), db, link.ID, start, key)
	if err != nil || first == nil {
		t.Fatalf("first save: %+v, %v", first, err)
	}
	refusing := WithBookingSettingsCheck(context.Background(), func(*SchedulingLink) error { return pac106Refusal() })
	replay, err := pac106Save(refusing, db, link.ID, start, key)
	if err != nil {
		t.Fatalf("keyed retry under a refusing check: %v; want the replay", err)
	}
	if replay == nil || !replay.Replayed || replay.ID != first.ID {
		t.Fatalf("keyed retry answered %+v; want the stored booking replayed", replay)
	}
	if n := pac101Bookings(t, db, link.ID); n != 1 {
		t.Errorf("%d bookings stored, want 1", n)
	}
}

// A save with no check behaves exactly as before PAC-106 (every fixture and
// the storage's own tests save without one).
func TestPAC106_ASaveWithoutACheckIsUnchanged(t *testing.T) {
	db := openTestDB(t)
	owner := mustUser(t, db, "pac106-no-check@example.com")
	link := pac40Link(t, db, owner, "pac106-no-check")
	if BookingSettingsCheckFrom(context.Background()) != nil {
		t.Fatal("a plain context carries a check")
	}
	b, err := pac106Save(context.Background(), db, link.ID, pac101Start(), "")
	if err != nil || b == nil {
		t.Fatalf("save without a check: %+v, %v", b, err)
	}
	if n := pac101Bookings(t, db, link.ID); n != 1 {
		t.Errorf("%d bookings stored, want 1", n)
	}
}
