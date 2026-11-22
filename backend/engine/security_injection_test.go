package engine

import (
	"errors"
	"fmt"
	"net/smtp"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/safetext"
)

// Read-only security audit, finding 1 (HIGH) and its ICS rider. The booking
// confirmation is composed by hand — header lines joined with "\r\n", then the
// multipart body and the iCalendar attachment — and three of its values come
// from whoever books the link (the link's title, the booker's name and the
// booker's address) while a fourth comes from the hosts' identity provider
// (their names). A CR or LF in any of them ends the line it was interpolated
// into, so the rest of that value becomes a header of the attacker's choosing
// on a message the deployment's relay signs as ours, a MIME part of their own,
// or an extra ATTENDEE / replacement ORGANIZER / ATTACH on the METHOD:REQUEST
// invitation the recipient's calendar client parses.
//
// These tests assert on the composed bytes, not on the sender returning nil.

// captureSentMail replaces the SMTP sender with one that records the message
// it was handed, and turns outbound email on for the test.
func captureSentMail(t *testing.T) *[]byte {
	t.Helper()
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_PORT", "587")
	t.Setenv("SMTP_FROM", "paceday@example.com")
	t.Setenv("SMTP_USER", "")
	t.Setenv("SMTP_PASS", "")
	var msg []byte
	prev := sendMail
	sendMail = func(_ string, _ smtp.Auth, _ string, _ []string, m []byte) error {
		msg = m
		return nil
	}
	t.Cleanup(func() { sendMail = prev })
	return &msg
}

const (
	hostileTitle      = "Standup\r\nBcc: victim@example.com\r\nContent-Type: text/html"
	hostileBookerName = "Mallory\r\n--boundary-paceday-ics\r\nContent-Type: text/html\r\n\r\n<h1>pwned</h1>"
	hostileHostNames  = "Bob\r\nX-Injected: yes"
)

func TestConfirmationEmailHeadersCannotBeInjected(t *testing.T) {
	msg := captureSentMail(t)
	start := time.Date(2026, 6, 15, 14, 0, 0, 0, time.UTC)

	if err := sendConfirmationEmail(hostileTitle, hostileBookerName, "booker@example.com",
		hostileHostNames, start, start.Add(30*time.Minute), time.UTC); err != nil {
		t.Fatalf("send: %v", err)
	}
	raw := string(*msg)

	// The header block is everything before the first empty line; it must be
	// exactly the five headers the composer writes, each on one line.
	block, _, found := strings.Cut(raw, "\r\n\r\n")
	if !found {
		t.Fatalf("no header block in the message:\n%s", raw)
	}
	want := []string{
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="boundary-paceday-ics"`,
		"From: Paceday <paceday@example.com>",
		"To: booker@example.com",
		"Subject: Confirmed — " + safetext.Label(hostileTitle) + " on Monday, June 15",
	}
	got := strings.Split(block, "\r\n")
	if len(got) != len(want) {
		t.Fatalf("header block has %d lines, want %d:\n%s", len(got), len(want), block)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("header %d = %q, want %q", i, got[i], want[i])
		}
	}

	// Nothing the attacker wrote became a header or a MIME part anywhere in
	// the message, body and attachment included.
	for _, forbidden := range []string{"\r\nBcc:", "\r\nContent-Type: text/html", "\r\nX-Injected:"} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("the message holds an injected line %q:\n%s", forbidden, raw)
		}
	}
	lines := strings.Split(raw, "\r\n")
	parts, closing := 0, 0
	for _, l := range lines {
		switch l {
		case "--boundary-paceday-ics":
			parts++
		case "--boundary-paceday-ics--":
			closing++
		}
	}
	if parts != 2 || closing != 1 {
		t.Errorf("%d part boundaries and %d closing boundaries, want 2 and 1 (the booker's name opened a part of its own):\n%s", parts, closing, raw)
	}
}

// The confirmation is addressed to the booker: an address carrying CR/LF would
// write the rest of the To header itself. createBooking refuses such an
// address, and the composer is the layer under it — it fails the send rather
// than repairing the address, because every repair of an address names
// somebody else (D-15).
func TestConfirmationEmailFailsOnAnUnusableAddress(t *testing.T) {
	msg := captureSentMail(t)
	start := time.Date(2026, 6, 15, 14, 0, 0, 0, time.UTC)

	err := sendConfirmationEmail("Standup", "Alice", "booker@example.com\r\nBcc: victim@example.com",
		"Bob", start, start.Add(30*time.Minute), time.UTC)
	if !errors.Is(err, safetext.ErrUnsafeAddress) {
		t.Fatalf("err = %v, want ErrUnsafeAddress", err)
	}
	if len(*msg) != 0 {
		t.Fatalf("a message was handed to the sender anyway:\n%s", *msg)
	}
}

// D-15, the other half: a legitimate address at the longest length a forward
// path allows is sent whole. Cutting it to the label cap would deliver this
// confirmation, and the ICS invitation, to a different, valid address.
func TestConfirmationEmailKeepsALongAddressWhole(t *testing.T) {
	msg := captureSentMail(t)
	start := time.Date(2026, 6, 15, 14, 0, 0, 0, time.UTC)
	long := strings.Repeat("a", safetext.MaxAddressLen-len("@example.com")) + "@example.com"
	if len(long) != safetext.MaxAddressLen || len(long) <= safetext.MaxLen {
		t.Fatalf("test address is %d octets; want %d, above the %d-rune label cap",
			len(long), safetext.MaxAddressLen, safetext.MaxLen)
	}

	if err := sendConfirmationEmail("Standup", "Alice", long, "Bob", start, start.Add(30*time.Minute), time.UTC); err != nil {
		t.Fatalf("send: %v", err)
	}
	raw := string(*msg)
	if !strings.Contains(raw, "\r\nTo: "+long+"\r\n") {
		t.Errorf("the To header does not carry the address whole:\n%s", raw)
	}
	if !strings.Contains(raw, ":mailto:"+long+"\r\n") {
		t.Errorf("the ICS ATTENDEE does not carry the address whole:\n%s", raw)
	}
	// And no shortened form of it appears anywhere.
	if cut := long[:safetext.MaxLen]; strings.Contains(raw, cut+"\r\n") {
		t.Errorf("a truncated address (%q) appears in the message:\n%s", cut, raw)
	}
}

func TestGenerateICSPropertyLinesCannotBeInjected(t *testing.T) {
	start := time.Date(2026, 6, 15, 14, 0, 0, 0, time.UTC)
	end := start.Add(30 * time.Minute)
	hostileName := "Mallory\r\nATTENDEE;CN=Ghost:mailto:ghost@example.com\r\nATTACH:https://evil.example/x"
	hostileSummary := "Sync\r\nORGANIZER;CN=Evil:mailto:evil@example.com"
	hostileHosts := "Bob\r\nDESCRIPTION:owned"

	ics, err := generateICS(hostileSummary, hostileName, "mallory@example.com", hostileHosts, start, end)
	if err != nil {
		t.Fatalf("generateICS: %v", err)
	}

	want := []string{
		"BEGIN:VCALENDAR",
		"VERSION:2.0",
		"PRODID:-//Paceday//Paceday//EN",
		"METHOD:REQUEST",
		"BEGIN:VEVENT",
		fmt.Sprintf("UID:%d@paceday", start.UnixNano()),
		"DTSTART:20260615T140000Z",
		"DTEND:20260615T143000Z",
		"SUMMARY:" + safetext.Label(hostileSummary),
		"DESCRIPTION:Meeting with " + safetext.Label(hostileHosts),
		"ORGANIZER;CN=Paceday:mailto:noreply@paceday.app",
		"ATTENDEE;CN=" + safetext.Label(hostileName) + ":mailto:mallory@example.com",
		"END:VEVENT",
		"END:VCALENDAR",
	}
	got := strings.Split(ics, "\r\n")
	if len(got) != len(want) {
		t.Fatalf("the ICS has %d property lines, want exactly %d:\n%s", len(got), len(want), ics)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("property %d = %q, want %q", i, got[i], want[i])
		}
	}

	// One ORGANIZER, one ATTENDEE, one DESCRIPTION, no ATTACH: the calendar
	// client is never handed a second organiser or an attachment.
	counts := map[string]int{}
	for _, l := range got {
		name, _, _ := strings.Cut(l, ":")
		name, _, _ = strings.Cut(name, ";")
		counts[name]++
	}
	for name, want := range map[string]int{"ORGANIZER": 1, "ATTENDEE": 1, "DESCRIPTION": 1, "SUMMARY": 1, "ATTACH": 0} {
		if counts[name] != want {
			t.Errorf("%d %s properties, want %d:\n%s", counts[name], name, want, ics)
		}
	}
}
