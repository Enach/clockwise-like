package engine

// PAC-122 phase 2, batch 1: the booking-completion unit no longer passes a
// skipped email (SMTP unset, SF-066) or a lost booking_events row (SF-067)
// off as done.

import (
	"context"
	"fmt"
	"net/smtp"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/lifecycle"
	"github.com/google/uuid"
)

// SF-066: with SMTP_HOST/SMTP_FROM unset the confirmation is skipped, nothing
// is sent, and the skip is logged with the booking id (never the address).
func TestPAC122_SF066_BookingEmailSkipIsLogged(t *testing.T) {
	pac81Scale(t)
	db := openTestDB(t)
	newGoogleStub(t)
	calls := 0
	swapSendMail(t, func(string, smtp.Auth, string, []string, []byte) error { calls++; return nil })
	t.Setenv("SMTP_HOST", "")
	t.Setenv("SMTP_FROM", "")
	host, _ := pac81User(t, db, "sf066host@pac122.test")
	link := pac81Link(t, db, []uuid.UUID{host})
	logged := captureStdLog(t)
	start := nextWeekday(time.Now()).Add(14 * time.Hour)
	booking, err := (&BookingEngine{DB: db}).ConfirmBooking(context.Background(), link, "B", "sf066booker@pac122.test", start, start.Add(30*time.Minute), "", "")
	if err != nil {
		t.Fatal(err)
	}
	lifecycle.Default().Wait(context.Background())
	out := logged()
	if !strings.Contains(out, "WARN: booking confirmation email for booking "+booking.ID.String()+" not sent") {
		t.Fatalf("no skip line for the booking in %q", out)
	}
	if calls != 0 {
		t.Fatalf("%d sends with SMTP unset", calls)
	}
	if strings.Contains(out, "@pac122.test") {
		t.Fatalf("an address was logged: %q", out)
	}
}

// SF-067: a host event created on the calendar whose booking_events row
// cannot be saved is logged and reported with the booking and host ids.
func TestPAC122_SF067_BookingEventSaveFailureReported(t *testing.T) {
	pac81Scale(t)
	db := openTestDB(t)
	newGoogleStub(t)
	swapSendMail(t, func(string, smtp.Auth, string, []string, []byte) error { return nil })
	host, _ := pac81User(t, db, "sf067host@pac122.test")
	link := pac81Link(t, db, []uuid.UUID{host})

	// Refuse booking_events inserts for this host only.
	fn := "pac122_sf067_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := db.Exec(fmt.Sprintf(`CREATE FUNCTION %[1]s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.user_id = '%[2]s' THEN RAISE EXCEPTION 'pac122 forced booking_events failure'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER %[1]s BEFORE INSERT ON booking_events FOR EACH ROW EXECUTE FUNCTION %[1]s();`, fn, host)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf(`DROP TRIGGER IF EXISTS %[1]s ON booking_events; DROP FUNCTION IF EXISTS %[1]s();`, fn))
	})
	var reported []error
	prev := captureBookingFailure
	captureBookingFailure = func(err error) { reported = append(reported, err) }
	t.Cleanup(func() { captureBookingFailure = prev })

	logged := captureStdLog(t)
	start := nextWeekday(time.Now()).Add(15 * time.Hour)
	booking, err := (&BookingEngine{DB: db}).ConfirmBooking(context.Background(), link, "B", "sf067booker@pac122.test", start, start.Add(30*time.Minute), "", "")
	if err != nil {
		t.Fatal(err)
	}
	lifecycle.Default().Wait(context.Background())
	want := fmt.Sprintf("booking %s: host %s calendar event created but not recorded", booking.ID, host)
	if out := logged(); !strings.Contains(out, "ERROR: "+want) || strings.Contains(out, "@pac122.test") {
		t.Fatalf("log %q: want the ERROR line with the ids and no address", out)
	}
	if len(reported) != 1 || !strings.Contains(reported[0].Error(), want) {
		t.Fatalf("reported %v, want one report naming the ids", reported)
	}
}
