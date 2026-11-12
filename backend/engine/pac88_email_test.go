package engine

import (
	"bytes"
	"errors"
	"log"
	"net/smtp"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
)

// PAC-88 contract section 4 (owner OQ-4): the confirmation email gives the
// link's zone (name and offset), then UTC; UTC only when the link's zone is
// UTC or cannot be read, which is logged with the booking id. ICS unchanged.

// pac88Mail sends one confirmation through sendBookingEmail with the owner's
// zone read answering tz/found/err, and returns the raw message and the log.
func pac88Mail(t *testing.T, tz string, found bool, readErr error, start, end time.Time) (msg, logged string) {
	t.Helper()
	var sent []byte
	swapSendMail(t, func(_ string, _ smtp.Auth, _ string, _ []string, body []byte) error {
		sent = body
		return nil
	})
	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(prevOut); log.SetFlags(prevFlags) })

	owner := uuid.New()
	e := &BookingEngine{Reads: BookingReads{OwnerZone: func(_ storage.DBTX, id uuid.UUID) (string, bool, error) {
		if id != owner {
			t.Errorf("zone read for %s, want the link owner %s", id, owner)
		}
		return tz, found, readErr
	}}}
	link := &storage.SchedulingLink{OwnerUserID: owner, Title: "Intro call"}
	booking := &storage.Booking{ID: uuid.MustParse("88888888-8888-4888-8888-888888888888"), BookerName: "Ana", BookerEmail: "ana@example.com", StartTime: start, EndTime: end}
	if err := e.sendBookingEmail(nil, link, booking, "Host One"); err != nil {
		t.Fatalf("sendBookingEmail: %v", err)
	}
	if sent == nil {
		t.Fatal("no email sent")
	}
	return string(sent), buf.String()
}

func pac88Line(t *testing.T, msg, prefix string) string {
	t.Helper()
	for _, l := range strings.Split(strings.ReplaceAll(msg, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	return ""
}

// E-1 / AC-22: Los Angeles owner, 2027-01-15 03:30Z, 30 minutes.
func TestPAC88_E1_EmailInLinkZoneThenUTC(t *testing.T) {
	start := time.Date(2027, 1, 15, 3, 30, 0, 0, time.UTC)
	msg, logged := pac88Mail(t, "America/Los_Angeles", true, nil, start, start.Add(30*time.Minute))
	for prefix, want := range map[string]string{
		"Subject: ": "Subject: Confirmed — Intro call on Thursday, January 14",
		"Date: ":    "Date: Thursday, January 14, 2027",
		"Time: ":    "Time: 7:30 PM – 8:00 PM, America/Los_Angeles (UTC−08:00)",
		"In UTC: ":  "In UTC: 03:30 – 04:00 UTC on Friday, January 15, 2027",
		"DTSTART:":  "DTSTART:20270115T033000Z",
		"DTEND:":    "DTEND:20270115T040000Z",
	} {
		if got := pac88Line(t, msg, prefix); got != want {
			t.Errorf("%q line = %q, want %q", prefix, got, want)
		}
	}
	if logged != "" {
		t.Errorf("unexpected log: %q", logged)
	}
	// The order: the link zone's lines, then the UTC line.
	if strings.Index(msg, "Time: ") > strings.Index(msg, "In UTC: ") {
		t.Error("the UTC line comes before the link zone's time")
	}
}

// E-2 / A-4: an owner without a zone (no row, empty, Local) and an owner in
// UTC: UTC only, labelled, no "In UTC" line, no warning.
func TestPAC88_E2_UTCOwnerIsUTCOnly(t *testing.T) {
	start := time.Date(2027, 1, 15, 3, 30, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		tz    string
		found bool
	}{{"no settings row", "", false}, {"empty", "", true}, {"Local", "Local", true}, {"UTC", "UTC", true}, {"unloadable", "Not/AZone", true}} {
		t.Run(tc.name, func(t *testing.T) {
			msg, logged := pac88Mail(t, tc.tz, tc.found, nil, start, start.Add(30*time.Minute))
			if got := pac88Line(t, msg, "Subject: "); got != "Subject: Confirmed — Intro call on Friday, January 15" {
				t.Errorf("subject = %q", got)
			}
			if got := pac88Line(t, msg, "Date: "); got != "Date: Friday, January 15, 2027" {
				t.Errorf("date = %q", got)
			}
			if got := pac88Line(t, msg, "Time: "); got != "Time: 03:30 – 04:00 UTC" {
				t.Errorf("time = %q", got)
			}
			if strings.Contains(msg, "In UTC:") {
				t.Error("a UTC link's email repeats the time in UTC")
			}
			if logged != "" {
				t.Errorf("unexpected log: %q", logged)
			}
		})
	}
}

// E-3 / AC-23: the zone cannot be read at compose time: still sent, in UTC,
// labelled UTC, and a WARN line with the booking id (addresses redacted).
func TestPAC88_E3_UnreadableZoneSendsUTCAndWarns(t *testing.T) {
	start := time.Date(2027, 1, 15, 3, 30, 0, 0, time.UTC)
	msg, logged := pac88Mail(t, "", false, errors.New("settings read failed for owner@example.com"), start, start.Add(30*time.Minute))
	if got := pac88Line(t, msg, "Time: "); got != "Time: 03:30 – 04:00 UTC" {
		t.Errorf("time = %q, want UTC labelled UTC", got)
	}
	if got := pac88Line(t, msg, "DTSTART:"); got != "DTSTART:20270115T033000Z" {
		t.Errorf("ICS start = %q", got)
	}
	want := "WARN: booking confirmation email for booking 88888888-8888-4888-8888-888888888888: link zone unreadable, sent in UTC: "
	if !strings.HasPrefix(logged, want) {
		t.Errorf("log = %q, want prefix %q", logged, want)
	}
	if strings.Contains(logged, "owner@example.com") {
		t.Errorf("log carries an address: %q", logged)
	}
}

// E-4: a meeting spanning a change of offset gives each end its offset.
func TestPAC88_E4_MeetingSpanningTransition(t *testing.T) {
	// 2027-11-07 05:30Z = 1:30 AM EDT; +45 min = 06:15Z = 1:15 AM EST.
	start := time.Date(2027, 11, 7, 5, 30, 0, 0, time.UTC)
	msg, _ := pac88Mail(t, "America/New_York", true, nil, start, start.Add(45*time.Minute))
	if got := pac88Line(t, msg, "Time: "); got != "Time: 1:30 AM (UTC−04:00) – 1:15 AM (UTC−05:00), America/New_York" {
		t.Errorf("time = %q", got)
	}
	if got := pac88Line(t, msg, "In UTC: "); got != "In UTC: 05:30 – 06:15 UTC on Sunday, November 7, 2027" {
		t.Errorf("UTC line = %q", got)
	}
	// A positive offset uses "+".
	msg, _ = pac88Mail(t, "Asia/Kolkata", true, nil, start, start.Add(30*time.Minute))
	if got := pac88Line(t, msg, "Time: "); got != "Time: 11:00 AM – 11:30 AM, Asia/Kolkata (UTC+05:30)" {
		t.Errorf("Kolkata time = %q", got)
	}
}
