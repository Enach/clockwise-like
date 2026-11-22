package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"strings"
	"testing"

	"github.com/Enach/paceday/backend/internal/safetext"
	"github.com/google/uuid"
)

// The two findings of the read-only security audit of 2026-11-22.
//
// Finding 1 (HIGH), mail header injection: the team invitation is composed by
// hand, "From: %s\r\nTo: %s\r\nSubject: %s\r\n\r\n%s", and the team's name and
// the inviter's name reach the Subject. A team name holding CR/LF carried
// headers and a body of the attacker's choosing to everyone they then invited,
// over the deployment's relay, SPF/DKIM-aligned as the deployment's domain.
// The envelope was never the problem (net/smtp's Rcpt refuses a bad
// recipient); the DATA block was.
//
// Finding 2 (MEDIUM, unauthenticated), host-email enumeration: getSlots and
// createBooking both opened with allowPublicBooking, getLinkInfo did not — and
// it is the route that answers with each accepted host's address. With default
// slugs of the shape "{first-name}-{duration}min" a first-name dictionary
// sweep at unlimited rate harvested every host's email, name, avatar and
// calendar coverage. nginx deliberately adds no limit_req, so the application
// limiter is the only control.

// --- Finding 1: the invitation's DATA block ---------------------------------

func TestTeamInviteEmailHeadersCannotBeInjected(t *testing.T) {
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_PORT", "587")
	t.Setenv("SMTP_FROM", "paceday@example.com")
	t.Setenv("SMTP_USER", "")
	t.Setenv("SMTP_PASS", "")
	t.Setenv("BASE_URL", "https://paceday.example")
	var msg []byte
	prev := sendMail
	sendMail = func(_ string, _ smtp.Auth, _ string, _ []string, m []byte) error { msg = m; return nil }
	t.Cleanup(func() { sendMail = prev })

	hostileTeam := "Acme\r\nBcc: victim@example.com\r\nContent-Type: text/html\r\n\r\n<h1>Reset your password</h1>"
	hostileInviter := "Mallory\r\nX-Injected: yes"
	if err := sendTeamInviteEmail("invitee@example.com", hostileInviter, hostileTeam, "tok-123"); err != nil {
		t.Fatalf("send: %v", err)
	}
	raw := string(msg)

	block, body, found := strings.Cut(raw, "\r\n\r\n")
	if !found {
		t.Fatalf("no header block in the message:\n%s", raw)
	}
	want := []string{
		"From: paceday@example.com",
		"To: invitee@example.com",
		fmt.Sprintf("Subject: %s invited you to join the %q team on Paceday",
			safetext.Label(hostileInviter), safetext.Label(hostileTeam)),
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
	for _, forbidden := range []string{"Bcc:", "X-Injected:", "Content-Type:"} {
		if strings.Contains(block, forbidden) {
			t.Errorf("the header block holds an injected %q:\n%s", forbidden, block)
		}
	}
	// The body still carries the invitation's own link, and the hostile value
	// is one line of text in it rather than a part of its own.
	if !strings.Contains(body, "https://paceday.example/invites/tok-123") {
		t.Errorf("the invite link is missing from the body:\n%s", body)
	}
}

// D-15: the recipient is an identifier, not prose. An address that cannot go
// in the To header as it stands fails the send — a repaired or shortened
// address is somebody else's valid address, and this message carries a token
// that joins a team — while a legitimate address at the longest length a
// forward path allows goes out whole.
func TestTeamInviteEmailNeverRepairsTheRecipient(t *testing.T) {
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_FROM", "paceday@example.com")
	t.Setenv("BASE_URL", "https://paceday.example")
	var msg []byte
	prev := sendMail
	sendMail = func(_ string, _ smtp.Auth, _ string, _ []string, m []byte) error { msg = m; return nil }
	t.Cleanup(func() { sendMail = prev })

	err := sendTeamInviteEmail("invitee@example.com\r\nBcc: other@example.com", "Inviter", "Team", "tok")
	if !errors.Is(err, safetext.ErrUnsafeAddress) {
		t.Fatalf("err = %v, want ErrUnsafeAddress", err)
	}
	if len(msg) != 0 {
		t.Fatalf("a message was handed to the sender anyway:\n%s", msg)
	}

	long := strings.Repeat("a", safetext.MaxAddressLen-len("@example.com")) + "@example.com"
	if len(long) <= safetext.MaxLen {
		t.Fatalf("test address is %d octets; it must exceed the %d-rune label cap", len(long), safetext.MaxLen)
	}
	if err := sendTeamInviteEmail(long, "Inviter", "Team", "tok"); err != nil {
		t.Fatalf("a %d-octet address was refused: %v", len(long), err)
	}
	if !strings.Contains(string(msg), "\r\nTo: "+long+"\r\n") {
		t.Errorf("the To header does not carry the address whole:\n%s", msg)
	}
}

// Entry validation: the user-facing half of the fix. A team name with a line
// break is refused where it is accepted, not silently rewritten later.
func TestCreateTeamRefusesControlCharactersInTheName(t *testing.T) {
	r := setupTeamRoutes(t)
	userID := createTestUser(t, "ctrl-team-owner@example.com")

	for _, name := range []string{
		"Acme\r\nBcc: victim@example.com",
		"Acme\nSubject: other",
		"Acme\rX: y",
		"Acme\x00",
	} {
		body, _ := json.Marshal(map[string]string{"name": name})
		req := withUser(httptest.NewRequest(http.MethodPost, "/api/teams", bytes.NewReader(body)), userID)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("name %q: status = %d, want 400; body: %s", name, w.Code, w.Body.String())
		}
		if got := strings.TrimSpace(w.Body.String()); got != `{"error":"`+msgNoLineBreaks+`"}` {
			t.Errorf("name %q: body = %s, want the %q refusal", name, got, msgNoLineBreaks)
		}
	}

	// A legitimate name is unaffected, punctuation and accents included.
	body, _ := json.Marshal(map[string]string{"name": "Équipe Café — 1:1"})
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/teams", bytes.NewReader(body)), userID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("legitimate name: status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
}

func TestPatchTeamRefusesControlCharactersInTheName(t *testing.T) {
	r := setupTeamRoutes(t)
	userID := createTestUser(t, "ctrl-team-patch@example.com")

	body, _ := json.Marshal(map[string]string{"name": "Before"})
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/teams", bytes.NewReader(body)), userID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d; body: %s", w.Code, w.Body.String())
	}
	var created struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	patch, _ := json.Marshal(map[string]string{"name": "After\r\nBcc: victim@example.com"})
	preq := withUser(httptest.NewRequest(http.MethodPatch, "/api/teams/"+created.ID.String(), bytes.NewReader(patch)), userID)
	pw := httptest.NewRecorder()
	r.ServeHTTP(pw, preq)
	if pw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", pw.Code, pw.Body.String())
	}
	// Nothing was renamed.
	var name string
	if err := openTestDB(t).QueryRow(`SELECT name FROM teams WHERE id = $1`, created.ID).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Before" {
		t.Errorf("stored name = %q, want it unchanged", name)
	}
}

// A link title reaches the confirmation's Subject and the invitation's
// SUMMARY. It is a 422, with the other field refusals of the link body.
func TestSchedulingLinkTitleRefusesControlCharacters(t *testing.T) {
	for _, title := range []string{
		"Standup\r\nBcc: victim@example.com",
		"Standup\nX: y",
		"Standup\x00",
	} {
		_, err := schedulingLinkInput{Title: title}.normalized()
		var e *httpError
		if err == nil {
			t.Fatalf("title %q was accepted", title)
		}
		e, ok := err.(*httpError)
		if !ok {
			t.Fatalf("title %q: err = %v (%T), want *httpError", title, err, err)
		}
		if e.Code != http.StatusUnprocessableEntity || e.Msg != msgTitleNoLineBreaks {
			t.Errorf("title %q: %d %q, want 422 %q", title, e.Code, e.Msg, msgTitleNoLineBreaks)
		}
	}
	if _, err := (schedulingLinkInput{Title: "Café — 1:1"}).normalized(); err != nil {
		t.Fatalf("a legitimate title was refused: %v", err)
	}
}

// The booker's name and address are the public, unauthenticated half of the
// mail sink: they reach the To header, the ATTENDEE property and the body.
func TestCreateBookingRefusesControlCharactersAndBadAddresses(t *testing.T) {
	ownerID := createTestUser(t, "sec-booker-owner@example.com")
	link := createPublicBookingLink(t, ownerID, "sec-booker", "Sec Booker", 0, "reusable", nil)
	flow := &countingBookingFlow{}
	r := setupPublicBookingRoutes(t, flow)
	start := futureBookingStart()

	for _, c := range []struct {
		what string
		body string
		want string
	}{
		{"a line break in the name", fmt.Sprintf(`{"name":"Mallory\r\nBcc: victim@example.com","email":"m@example.com","start":"%s","duration_minutes":30}`, start.Format("2006-01-02T15:04:05Z07:00")), msgNoLineBreaks},
		{"a NUL in the name", fmt.Sprintf(`{"name":"Mallory\u0000","email":"m@example.com","start":"%s","duration_minutes":30}`, start.Format("2006-01-02T15:04:05Z07:00")), msgNoLineBreaks},
		{"a line break in the address", fmt.Sprintf(`{"name":"Mallory","email":"m@example.com\r\nBcc: victim@example.com","start":"%s","duration_minutes":30}`, start.Format("2006-01-02T15:04:05Z07:00")), msgInvalidBookerEmail},
		{"a display name in the address", fmt.Sprintf(`{"name":"Mallory","email":"Mallory <m@example.com>","start":"%s","duration_minutes":30}`, start.Format("2006-01-02T15:04:05Z07:00")), msgInvalidBookerEmail},
		{"no address shape at all", fmt.Sprintf(`{"name":"Mallory","email":"not-an-address","start":"%s","duration_minutes":30}`, start.Format("2006-01-02T15:04:05Z07:00")), msgInvalidBookerEmail},
	} {
		got := doPublic(t, r, http.MethodPost, "/api/book/"+link.Slug, c.body, peerA, nil)
		if got.code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400; body: %s", c.what, got.code, got.body)
		}
		if want := `{"error":"` + c.want + `"}`; strings.TrimSpace(string(got.body)) != want {
			t.Errorf("%s: body = %s, want %s", c.what, got.body, want)
		}
	}
	if n := flow.confirmed.Load(); n != 0 {
		t.Fatalf("%d bookings were confirmed; want none", n)
	}

	// A legitimate booker is unaffected: an accented name and a mixed-case
	// address are still accepted, and the address is not rewritten.
	ok := doPublic(t, r, http.MethodPost, "/api/book/"+link.Slug,
		fmt.Sprintf(`{"name":"Zoé Café","email":"Zoe.Cafe@Example.com","start":"%s","duration_minutes":30}`, start.Format("2006-01-02T15:04:05Z07:00")), peerA, nil)
	if ok.code != http.StatusCreated {
		t.Fatalf("legitimate booking: status = %d, want 201; body: %s", ok.code, ok.body)
	}
	var saved struct {
		BookerEmail string `json:"booker_email"`
	}
	if err := json.Unmarshal(ok.body, &saved); err == nil && saved.BookerEmail != "" && saved.BookerEmail != "Zoe.Cafe@Example.com" {
		t.Errorf("booker_email = %q, want it stored as sent", saved.BookerEmail)
	}
}

// --- Finding 2: GET /api/book/{slug} is rate limited ------------------------

func TestGetLinkInfoIsRateLimited(t *testing.T) {
	ownerID := createTestUser(t, "sec-enum-owner@example.com")
	link := createPublicBookingLink(t, ownerID, "sec-enum", "Sec Enum", 0, "reusable", nil)
	r := setupPublicBookingRoutes(t, &countingBookingFlow{})

	// The slots budget: 120 per minute, shared with the slots route, counted
	// whatever the request answers — so a sweep of slugs that do not exist
	// counts too.
	for i := 0; i < defaultBookingSlotsLimit; i++ {
		path := "/api/book/" + link.Slug
		if i%2 == 1 {
			path = fmt.Sprintf("/api/book/does-not-exist-%d", i)
		}
		got := doPublic(t, r, http.MethodGet, path, "", peerA, nil)
		if got.code == http.StatusTooManyRequests {
			t.Fatalf("request %d throttled early", i+1)
		}
	}
	assertRateLimited(t, doPublic(t, r, http.MethodGet, "/api/book/"+link.Slug, "", peerA, nil), 60)

	// The refusal costs the attacker the host roster: the 429 body is the
	// error envelope and nothing else (assertRateLimited), and another client
	// is unaffected.
	if got := doPublic(t, r, http.MethodGet, "/api/book/"+link.Slug, "", peerB, nil); got.code != http.StatusOK {
		t.Fatalf("client B: status = %d, want 200; body: %s", got.code, got.body)
	}
}

// The limiter must be the route's first statement: a refused request learns
// nothing, not even whether the slug exists.
func TestGetLinkInfoRefusalPrecedesTheLookup(t *testing.T) {
	r := setupPublicBookingRoutes(t, &countingBookingFlow{})
	for i := 0; i < defaultBookingSlotsLimit; i++ {
		doPublic(t, r, http.MethodGet, "/api/book/sec-order-missing", "", peerA, nil)
	}
	got := doPublic(t, r, http.MethodGet, "/api/book/sec-order-missing", "", peerA, nil)
	if got.code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 rather than the 404 that confirms the slug is free; body: %s", got.code, got.body)
	}
}
