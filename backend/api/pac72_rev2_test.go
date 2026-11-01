package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/engine"
	"github.com/Enach/paceday/backend/internal/budget"
	"github.com/Enach/paceday/backend/internal/peritem"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// PAC-72 revision 2 (owner answers 2026-11-01) and 2.1: a per-link horizon
// (D-1), the link owner's profile zone (D-9) with the daylight-saving rules,
// the link owner's refusal setting (D-2/D-5), a failed read of the owner's
// settings as a server error, and the order of evaluation. Same bench as
// pac72_offerable_start_test.go: real handlers, real engine, test database,
// the recording Google Calendar stand-in, a fixed clock.

// pac72Texts is the contract's (error, reason) pairing (BookingSlotUnavailableError).
var pac72Texts = map[string]string{
	"too_soon":           "slot is too soon",
	"outside_link_hours": "slot is outside the link's days or hours",
	"beyond_horizon":     "slot is beyond the link's booking horizon",
	"host_busy":          "a host is busy at that time",
	"taken":              "slot is already booked",
}

// setZone stores user's profile zone directly (as a settings row).
func (b *pac72Bench) setZone(user uuid.UUID, tz string) {
	b.t.Helper()
	if _, err := b.db.Exec(`INSERT INTO settings (user_id, timezone) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET timezone = EXCLUDED.timezone`, user, tz); err != nil {
		b.t.Fatalf("set zone: %v", err)
	}
}

// refusedAs asserts a 409 whose body is exactly the generic one (reason "")
// or exactly the pair {error: text of reason, reason}, and nothing written.
func (b *pac72Bench) refusedAs(what string, code int, body string, before pac72Written, reason string) {
	b.t.Helper()
	if code != http.StatusConflict {
		b.t.Errorf("%s: answer = %d %s, want 409", what, code, body)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		b.t.Fatalf("%s: body %q: %v", what, body, err)
	}
	want := map[string]any{"error": "slot is no longer available"}
	if reason != "" {
		want = map[string]any{"error": pac72Texts[reason], "reason": reason}
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		b.t.Errorf("%s: body = %v, want exactly %v", what, got, want)
	}
	if after := b.written(); after != before {
		b.t.Errorf("%s: something was written: before %+v, after %+v", what, before, after)
	}
}

func (b *pac72Bench) bookOK(what, slug string, start time.Time, duration int) {
	b.t.Helper()
	if code, body := b.book(slug, start, duration); code != http.StatusCreated {
		b.t.Errorf("%s: booking %s = %d %s, want 201", what, start.UTC().Format(time.RFC3339), code, body)
	}
}

func (b *pac72Bench) info(slug string) (int, publicLinkDTO) {
	b.t.Helper()
	rec := httptest.NewRecorder()
	b.r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/book/"+slug, nil))
	var dto publicLinkDTO
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
			b.t.Fatal(err)
		}
	}
	return rec.Code, dto
}

// linkRoutes is the owner's scheduling-link API on the bench's database.
func (b *pac72Bench) linkRoutes() (*chi.Mux, *schedulingLinkHandlers) {
	h := newSchedulingLinkHandlers(b.db, nil)
	r := chi.NewRouter()
	r.Post("/api/scheduling-links", h.createLink)
	r.Get("/api/scheduling-links", h.listLinks)
	r.Get("/api/scheduling-links/{id}", h.getLink)
	r.Patch("/api/scheduling-links/{id}", h.updateLink)
	return r, h
}

func ownerCall(r http.Handler, user uuid.UUID, method, path, body string) (int, string) {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, withUser(req, user))
	return rec.Code, strings.TrimSpace(rec.Body.String())
}

func decodeLink(t *testing.T, body string) schedulingLinkDTO {
	t.Helper()
	var dto schedulingLinkDTO
	if err := json.Unmarshal([]byte(body), &dto); err != nil {
		t.Fatalf("decode link %q: %v", body, err)
	}
	return dto
}

func everyDay(l *storage.SchedulingLink) { l.DaysOfWeek = []int{0, 1, 2, 3, 4, 5, 6} }

// AC-24: the horizon is the link's own setting: 10, 1 and 365 days.
func TestPAC72_HorizonPerLink(t *testing.T) {
	now := pac72At(pac72Mon, "06:00")
	for _, n := range []int{10, 1, 365} {
		t.Run(fmt.Sprintf("horizon %d", n), func(t *testing.T) {
			b := newPAC72Bench(t, now)
			owner, _ := b.host(fmt.Sprintf("pac72-h%d@example.com", n))
			link := b.link(fmt.Sprintf("pac72-h%d", n), owner, func(l *storage.SchedulingLink) {
				everyDay(l)
				l.BookingHorizonDays = n
			}, owner)
			last := pac72Mon.AddDate(0, 0, n-1)
			past := pac72Mon.AddDate(0, 0, n)
			if got := b.slots(link.Slug, last.Format(time.DateOnly), 30); got.code != http.StatusOK || !got.offers(pac72At(last, "10:00")) {
				t.Errorf("D+%d listing = %d %v, want 10:00 offered", n-1, got.code, got.starts())
			}
			if got := b.slots(link.Slug, past.Format(time.DateOnly), 30); got.code != http.StatusOK || len(got.slots) != 0 {
				t.Errorf("D+%d listing = %d %v, want 200 with no slot", n, got.code, got.starts())
			}
			before := b.written()
			code, body := b.book(link.Slug, pac72At(past, "10:00"), 30)
			b.refusedAs(fmt.Sprintf("D+%d", n), code, body, before, "")
			b.bookOK(fmt.Sprintf("D+%d", n-1), link.Slug, pac72At(last, "10:00"), 30)
			all := b.slots(link.Slug, "", 30)
			if all.code != http.StatusOK || len(all.dates) != n || all.dates[len(all.dates)-1] != last.Format(time.DateOnly) {
				t.Errorf("date-less listing = %d, %d dates, want %d ending %s", all.code, len(all.dates), n, last.Format(time.DateOnly))
			}
			if code, info := b.info(link.Slug); code != http.StatusOK || info.BookingHorizonDays != n {
				t.Errorf("link info horizon = %d (%d), want %d", info.BookingHorizonDays, code, n)
			}
		})
	}
}

// AC-25 (API half; the migration half is storage's
// TestMigrationSchedulingLinkHorizonRefusal), AC-26, AC-36: the two settings
// on create and update.
func TestPAC72_LinkSettingsCreateAndUpdate(t *testing.T) {
	b := newPAC72Bench(t, pac72At(pac72Mon, "06:00"))
	r, _ := b.linkRoutes()
	owner, _ := b.host("pac72-settings-owner@example.com")
	co, _ := b.host("pac72-settings-co@example.com")

	// AC-25: created without either setting (and with null): 60, generic.
	for i, body := range []string{`{"title":"Plain"}`, `{"title":"Nulls","booking_horizon_days":null,"refusal_message":null}`} {
		code, resp := ownerCall(r, owner, http.MethodPost, "/api/scheduling-links", strings.Replace(body, `"title"`, fmt.Sprintf(`"slug":"pac72-set-plain-%d","title"`, i), 1))
		if code != http.StatusCreated {
			t.Fatalf("create %s = %d %s", body, code, resp)
		}
		if dto := decodeLink(t, resp); dto.BookingHorizonDays != 60 || dto.RefusalMessage != "generic" || dto.TimeZone != "UTC" {
			t.Errorf("create %s = %d/%q/%q, want 60/generic/UTC", body, dto.BookingHorizonDays, dto.RefusalMessage, dto.TimeZone)
		}
	}

	// AC-26 / AC-36 on create.
	for _, tc := range []struct {
		body    string
		code    int
		horizon int
		refusal string
		msg     string
	}{
		{`"booking_horizon_days":1`, http.StatusCreated, 1, "generic", ""},
		{`"booking_horizon_days":365,"refusal_message":"specific"`, http.StatusCreated, 365, "specific", ""},
		{`"refusal_message":"generic"`, http.StatusCreated, 60, "generic", ""},
		{`"booking_horizon_days":0`, http.StatusUnprocessableEntity, 0, "", "booking_horizon_days must be between 1 and 365"},
		{`"booking_horizon_days":366`, http.StatusUnprocessableEntity, 0, "", "booking_horizon_days must be between 1 and 365"},
		{`"booking_horizon_days":-3`, http.StatusUnprocessableEntity, 0, "", "booking_horizon_days must be between 1 and 365"},
		{`"refusal_message":"loud"`, http.StatusUnprocessableEntity, 0, "", "refusal_message must be generic or specific"},
	} {
		slug := "pac72-set-" + strings.NewReplacer(`"`, "", ":", "-", ",", "-", "_", "").Replace(tc.body)
		code, resp := ownerCall(r, owner, http.MethodPost, "/api/scheduling-links", fmt.Sprintf(`{"title":"T","slug":%q,%s}`, slug, tc.body))
		if code != tc.code {
			t.Errorf("create {%s} = %d %s, want %d", tc.body, code, resp, tc.code)
			continue
		}
		if tc.code != http.StatusCreated {
			if resp != fmt.Sprintf(`{"error":%q}`, tc.msg) {
				t.Errorf("create {%s} body = %s, want %q", tc.body, resp, tc.msg)
			}
			if l, _ := storage.GetSchedulingLinkBySlug(b.db, normalizeSlug(slug)); l != nil {
				t.Errorf("create {%s} was refused but created a link", tc.body)
			}
			continue
		}
		if dto := decodeLink(t, resp); dto.BookingHorizonDays != tc.horizon || dto.RefusalMessage != tc.refusal {
			t.Errorf("create {%s} = %d/%q, want %d/%q", tc.body, dto.BookingHorizonDays, dto.RefusalMessage, tc.horizon, tc.refusal)
		}
	}

	// Update: stored, refused with the link unchanged, unchanged when absent
	// or null, owner-only.
	link := b.link("pac72-set-update", owner, nil, owner, co)
	path := "/api/scheduling-links/" + link.ID.String()
	read := func() (int, string) {
		l, err := storage.GetSchedulingLinkByID(b.db, link.ID)
		if err != nil {
			t.Fatal(err)
		}
		return l.BookingHorizonDays, l.RefusalMessage
	}
	for _, tc := range []struct {
		body    string
		code    int
		horizon int
		refusal string
	}{
		{`{"booking_horizon_days":1}`, http.StatusOK, 1, "generic"},
		{`{"booking_horizon_days":365,"refusal_message":"specific"}`, http.StatusOK, 365, "specific"},
		{`{"title":"Renamed"}`, http.StatusOK, 365, "specific"},
		{`{"booking_horizon_days":null,"refusal_message":null}`, http.StatusOK, 365, "specific"},
		{`{"booking_horizon_days":0}`, http.StatusUnprocessableEntity, 365, "specific"},
		{`{"booking_horizon_days":366}`, http.StatusUnprocessableEntity, 365, "specific"},
		{`{"booking_horizon_days":-1,"title":"Nope"}`, http.StatusUnprocessableEntity, 365, "specific"},
		{`{"refusal_message":"loud"}`, http.StatusUnprocessableEntity, 365, "specific"},
		{`{"refusal_message":"generic"}`, http.StatusOK, 365, "generic"},
	} {
		code, resp := ownerCall(r, owner, http.MethodPatch, path, tc.body)
		if code != tc.code {
			t.Errorf("update %s = %d %s, want %d", tc.body, code, resp, tc.code)
		}
		if h, m := read(); h != tc.horizon || m != tc.refusal {
			t.Errorf("after update %s: stored %d/%q, want %d/%q", tc.body, h, m, tc.horizon, tc.refusal)
		}
		if code == http.StatusOK {
			if dto := decodeLink(t, resp); dto.BookingHorizonDays != tc.horizon || dto.RefusalMessage != tc.refusal || dto.TimeZone != "UTC" {
				t.Errorf("update %s answered %d/%q/%q", tc.body, dto.BookingHorizonDays, dto.RefusalMessage, dto.TimeZone)
			}
		}
	}
	if code, resp := ownerCall(r, co, http.MethodPatch, path, `{"booking_horizon_days":5,"refusal_message":"specific"}`); code != http.StatusForbidden {
		t.Errorf("co-host update = %d %s, want 403", code, resp)
	}
	if h, m := read(); h != 365 || m != "generic" {
		t.Errorf("a co-host changed the settings: %d/%q", h, m)
	}

	// AC-36: switching to specific changes the very next refusal.
	before := b.written()
	code, body := b.book(link.Slug, pac72At(pac72Sat, "10:00"), 30)
	b.refusedAs("generic", code, body, before, "")
	if code, resp := ownerCall(r, owner, http.MethodPatch, path, `{"refusal_message":"specific"}`); code != http.StatusOK {
		t.Fatalf("switch to specific = %d %s", code, resp)
	}
	code, body = b.book(link.Slug, pac72At(pac72Sat, "10:00"), 30)
	b.refusedAs("after the switch", code, body, before, "outside_link_hours")
}

// AC-27: shortening the horizon keeps a booking beyond it (still busy for
// its host) and refuses a new one there.
func TestPAC72_ShorterHorizonKeepsBookings(t *testing.T) {
	b := newPAC72Bench(t, pac72At(pac72Mon, "06:00"))
	r, _ := b.linkRoutes()
	owner, _ := b.host("pac72-ac27@example.com")
	link := b.link("pac72-ac27", owner, everyDay, owner)
	other := b.link("pac72-ac27-other", owner, everyDay, owner)
	d50 := pac72Mon.AddDate(0, 0, 50)
	b.bookOK("D+50 before the change", link.Slug, pac72At(d50, "10:00"), 30)

	if code, resp := ownerCall(r, owner, http.MethodPatch, "/api/scheduling-links/"+link.ID.String(), `{"booking_horizon_days":30}`); code != http.StatusOK {
		t.Fatalf("shorten = %d %s", code, resp)
	}
	rows, err := storage.GetBookingsByLink(b.db, link.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("bookings after shortening = %v %v, want the D+50 one kept", rows, err)
	}
	if got := b.slots(other.Slug, d50.Format(time.DateOnly), 30); got.code != http.StatusOK || got.offers(pac72At(d50, "10:00")) || !got.offers(pac72At(d50, "11:00")) {
		t.Errorf("the kept booking is not busy time for its host: %v", got.starts())
	}
	before := b.written()
	code, body := b.book(link.Slug, pac72At(d50, "11:00"), 30)
	b.refusedAs("D+50 after shortening", code, body, before, "")
}

// AC-28 (revision 2.1): process in UTC+10, owner in America/New_York.
func TestPAC72_OwnersZone(t *testing.T) {
	prev := time.Local
	time.Local = time.FixedZone("UTC+10", 10*60*60)
	defer func() { time.Local = prev }()

	summer := time.Date(2030, 7, 2, 2, 30, 0, 0, time.UTC) // Monday 2030-07-01 22:30 EDT
	b := newPAC72Bench(t, summer)
	owner, _ := b.host("pac72-ny@example.com")
	b.setZone(owner, "America/New_York")
	link := b.link("pac72-ny", owner, func(l *storage.SchedulingLink) { l.DurationOptions = []int{30} }, owner)

	got := b.slots(link.Slug, "2030-07-02", 30)
	if got.code != http.StatusOK || len(got.slots) != 31 || got.slots[0].Start.UTC().Format("15:04") != "13:00" || got.slots[30].Start.UTC().Format("15:04") != "20:30" {
		t.Errorf("(a) 2030-07-02 = %d %v, want 13:00Z..20:30Z", got.code, got.starts())
	}
	b.bookOK("(b) 13:00Z", link.Slug, time.Date(2030, 7, 2, 13, 0, 0, 0, time.UTC), 30)
	before := b.written()
	code, body := b.book(link.Slug, time.Date(2030, 7, 2, 9, 0, 0, 0, time.UTC), 30)
	b.refusedAs("(c) 09:00Z (05:00 EDT)", code, body, before, "")
	b.bookOK("(d) Friday 20:30Z (16:30 EDT)", link.Slug, time.Date(2030, 7, 5, 20, 30, 0, 0, time.UTC), 30)
	all := b.slots(link.Slug, "", 30)
	if all.code != http.StatusOK || len(all.dates) == 0 || all.dates[0] != "2030-07-02" {
		t.Errorf("(e) date-less first date = %v (%d), want 2030-07-02", all.dates, all.code)
	}
	for _, d := range all.dates {
		if d == "2030-07-06" || d == "2030-07-07" || d == "2030-07-01" {
			t.Errorf("(e) date-less lists %s (a weekend in New York, or a day whose window has passed)", d)
		}
	}
	if code, info := b.info(link.Slug); code != http.StatusOK || info.TimeZone != "America/New_York" {
		t.Errorf("AC-32 link info zone = %q (%d), want America/New_York", info.TimeZone, code)
	}

	winter := time.Date(2030, 1, 8, 3, 30, 0, 0, time.UTC) // Monday 2030-01-07 22:30 EST
	w := newPAC72Bench(t, winter)
	wowner, _ := w.host("pac72-ny-winter@example.com")
	w.setZone(wowner, "America/New_York")
	wlink := w.link("pac72-ny-winter", wowner, func(l *storage.SchedulingLink) { l.DurationOptions = []int{30} }, wowner)
	got = w.slots(wlink.Slug, "2030-01-08", 30)
	if got.code != http.StatusOK || len(got.slots) != 31 || got.slots[0].Start.UTC().Format("15:04") != "14:00" || got.slots[30].Start.UTC().Format("15:04") != "21:30" {
		t.Errorf("(f) 2030-01-08 = %d %v, want 14:00Z..21:30Z", got.code, got.starts())
	}
	before = w.written()
	code, body = w.book(wlink.Slug, time.Date(2030, 1, 8, 9, 0, 0, 0, time.UTC), 30)
	w.refusedAs("(f) 09:00Z (04:00 EST)", code, body, before, "")
}

// AC-29: a collective link uses the owner's zone; a co-host's zone changes
// nothing; the owner's zone change moves the window from the next request.
func TestPAC72_CollectiveLinkUsesTheOwnersZone(t *testing.T) {
	b := newPAC72Bench(t, time.Date(2030, 1, 6, 12, 0, 0, 0, time.UTC))
	owner, _ := b.host("pac72-ac29-owner@example.com")
	co, _ := b.host("pac72-ac29-co@example.com")
	b.setZone(owner, "Europe/Paris")
	b.setZone(co, "Asia/Tokyo")
	link := b.link("pac72-ac29", owner, func(l *storage.SchedulingLink) {
		everyDay(l)
		l.WindowStart, l.WindowEnd = "09:00", "12:00"
	}, owner, co)
	window := func() string {
		got := b.slots(link.Slug, "2030-01-08", 30)
		if got.code != http.StatusOK || len(got.slots) == 0 {
			t.Fatalf("listing = %d %s", got.code, got.body)
		}
		return got.slots[0].Start.UTC().Format("15:04") + "-" + got.slots[len(got.slots)-1].End.UTC().Format("15:04")
	}
	if w := window(); w != "08:00-11:00" {
		t.Errorf("Paris owner: window %s, want 08:00-11:00 UTC (09:00-12:00 CET)", w)
	}
	b.setZone(co, "America/Los_Angeles")
	if w := window(); w != "08:00-11:00" {
		t.Errorf("after the co-host's zone change: window %s, want unchanged", w)
	}
	b.setZone(owner, "Asia/Tokyo")
	if w := window(); w != "00:00-03:00" {
		t.Errorf("after the owner's change to Tokyo: window %s, want 00:00-03:00 UTC (09:00-12:00 JST)", w)
	}
}

// AC-30: no settings row, or a zone name that does not load, is UTC; the
// public read reports UTC; no settings row is created.
func TestPAC72_NoProfileZoneIsUTC(t *testing.T) {
	for _, stored := range []string{"", "Mars/Olympus_Mons", "Local"} {
		t.Run("stored "+stored, func(t *testing.T) {
			prev := time.Local
			time.Local = time.FixedZone("UTC+10", 10*60*60)
			defer func() { time.Local = prev }()
			b := newPAC72Bench(t, pac72At(pac72Mon, "06:00"))
			tag := strings.ToLower(strings.NewReplacer("/", "-", "_", "-").Replace(stored))
			owner, _ := b.host("pac72-ac30-" + tag + "@example.com")
			if stored != "" {
				b.setZone(owner, stored)
			}
			link := b.link("pac72-ac30-"+tag, owner, nil, owner)
			if code, info := b.info(link.Slug); code != http.StatusOK || info.TimeZone != "UTC" {
				t.Errorf("link info zone = %q (%d), want UTC", info.TimeZone, code)
			}
			if got := b.slots(link.Slug, "2030-01-08", 30); got.code != http.StatusOK || got.slots[0].Start.UTC().Format("15:04") != "09:00" {
				t.Errorf("listing = %d %v, want UTC hours", got.code, got.starts())
			}
			b.bookOK("09:00Z", link.Slug, pac72At(pac72Tue, "09:00"), 30)
			var rows int
			if err := b.db.QueryRow(`SELECT count(*) FROM settings WHERE user_id = $1`, owner).Scan(&rows); err != nil {
				t.Fatal(err)
			}
			if want := map[bool]int{true: 0, false: 1}[stored == ""]; rows != want {
				t.Errorf("settings rows for the owner = %d, want %d (none created)", rows, want)
			}
		})
	}
}

// AC-31 (revision 2.1): Europe/Paris, 01:00-04:00, 15-minute slots of 15
// minutes, on both daylight-saving change days, and the window edges.
func TestPAC72_DaylightSaving(t *testing.T) {
	utc := func(s string) time.Time {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			panic(err)
		}
		return t
	}
	setup := func(t *testing.T, now time.Time, tag, open, close string) (*pac72Bench, *storage.SchedulingLink) {
		b := newPAC72Bench(t, now)
		owner, _ := b.host("pac72-dst-" + tag + "@example.com")
		b.setZone(owner, "Europe/Paris")
		link := b.link("pac72-dst-"+tag, owner, func(l *storage.SchedulingLink) {
			everyDay(l)
			l.DurationOptions = []int{15}
			l.WindowStart, l.WindowEnd = open, close
		}, owner)
		return b, link
	}
	bookAll := func(b *pac72Bench, link *storage.SchedulingLink, got pac72SlotsAnswer) {
		for _, s := range got.slots {
			code, body := b.book(link.Slug, s.Start, 15)
			if code != http.StatusCreated {
				b.t.Errorf("listed %s refused %d %s", s.Start.UTC().Format(time.RFC3339), code, body)
				continue
			}
			var conf bookingConfirmationDTO
			_ = json.Unmarshal([]byte(body), &conf)
			if _, err := b.db.Exec(`DELETE FROM bookings WHERE id = $1`, conf.ID); err != nil {
				b.t.Fatal(err)
			}
		}
	}

	t.Run("spring 2030-03-31", func(t *testing.T) {
		b, link := setup(t, utc("2030-03-29T12:00:00Z"), "spring", "01:00", "04:00")
		got := b.slots(link.Slug, "2030-03-31", 15)
		want := "2030-03-31T00:00:00Z/2030-03-31T00:15:00Z 2030-03-31T00:15:00Z/2030-03-31T00:30:00Z 2030-03-31T00:30:00Z/2030-03-31T00:45:00Z 2030-03-31T00:45:00Z/2030-03-31T01:00:00Z " +
			"2030-03-31T01:00:00Z/2030-03-31T01:15:00Z 2030-03-31T01:15:00Z/2030-03-31T01:30:00Z 2030-03-31T01:30:00Z/2030-03-31T01:45:00Z 2030-03-31T01:45:00Z/2030-03-31T02:00:00Z"
		if got.code != http.StatusOK || strings.Join(got.starts(), " ") != want {
			t.Errorf("spring listing = %d %v", got.code, got.starts())
		}
		if !got.offers(utc("2030-03-31T00:45:00Z")) || !got.offers(utc("2030-03-31T01:00:00Z")) {
			t.Errorf("01:45 CET (00:45Z) and 03:00 CEST (01:00Z) must be listed")
		}
		paris, _ := time.LoadLocation("Europe/Paris")
		for _, s := range got.slots {
			if s.Start.In(paris).Hour() == 2 {
				t.Errorf("a skipped wall time is listed: %v", s.Start.In(paris))
			}
		}
		before := b.written()
		code, body := b.book(link.Slug, utc("2030-03-31T00:50:00Z"), 15)
		b.refusedAs("00:50Z (01:50 CET, off the grid)", code, body, before, "")
		bookAll(b, link, got)
	})
	t.Run("autumn 2030-10-27", func(t *testing.T) {
		b, link := setup(t, utc("2030-10-25T12:00:00Z"), "autumn", "01:00", "04:00")
		got := b.slots(link.Slug, "2030-10-27", 15)
		counts := map[string]int{}
		paris, _ := time.LoadLocation("Europe/Paris")
		for _, s := range got.slots {
			counts[s.Start.In(paris).Format("15:04")]++
		}
		for _, wall := range []string{"02:00", "02:15", "02:30", "02:45"} {
			if counts[wall] != 1 {
				t.Errorf("%s listed %d times, want once", wall, counts[wall])
			}
		}
		for _, first := range []string{"00:00", "00:15", "00:30", "00:45"} {
			if !got.offers(utc("2030-10-27T" + first + ":00Z")) {
				t.Errorf("first occurrence %sZ not listed", first)
			}
		}
		if got.offers(utc("2030-10-27T01:00:00Z")) {
			t.Errorf("the second occurrence of 02:00 (01:00Z) is listed")
		}
		before := b.written()
		code, body := b.book(link.Slug, utc("2030-10-27T01:00:00Z"), 15)
		b.refusedAs("01:00Z (second 02:00)", code, body, before, "")
		bookAll(b, link, got)
	})
	t.Run("window edges on the spring day", func(t *testing.T) {
		b, late := setup(t, utc("2030-03-29T12:00:00Z"), "edge-open", "02:30", "04:00")
		if got := b.slots(late.Slug, "2030-03-31", 15); len(got.slots) == 0 || !got.slots[0].Start.Equal(utc("2030-03-31T01:00:00Z")) {
			t.Errorf("02:30-04:00 first slot = %v, want 01:00Z (03:00 CEST)", got.starts())
		}
		b2, early := setup(t, utc("2030-03-29T12:00:00Z"), "edge-close", "01:00", "02:30")
		got := b2.slots(early.Slug, "2030-03-31", 15)
		if len(got.slots) != 4 || !got.slots[3].Start.Equal(utc("2030-03-31T00:45:00Z")) {
			t.Errorf("01:00-02:30 slots = %v, want the last at 00:45Z (01:45 CET)", got.starts())
		}
		before := b2.written()
		code, body := b2.book(early.Slug, utc("2030-03-31T01:00:00Z"), 15)
		b2.refusedAs("01:00-02:30 at 01:00Z (after the close)", code, body, before, "")
	})
}

// AC-32: the public read and the owner's read report the same zone and the
// horizon; the owner's read reports the refusal setting.
func TestPAC72_ReadsReportZoneHorizonAndRefusal(t *testing.T) {
	b := newPAC72Bench(t, pac72At(pac72Mon, "06:00"))
	r, _ := b.linkRoutes()
	owner, _ := b.host("pac72-ac32@example.com")
	b.setZone(owner, "Europe/Paris")
	link := b.link("pac72-ac32", owner, func(l *storage.SchedulingLink) {
		l.BookingHorizonDays = 45
		l.RefusalMessage = "specific"
	}, owner)
	code, info := b.info(link.Slug)
	if code != http.StatusOK || info.TimeZone != "Europe/Paris" || info.BookingHorizonDays != 45 {
		t.Errorf("public read = %d %q/%d, want Europe/Paris/45", code, info.TimeZone, info.BookingHorizonDays)
	}
	code, body := ownerCall(r, owner, http.MethodGet, "/api/scheduling-links/"+link.ID.String(), "")
	if dto := decodeLink(t, body); code != http.StatusOK || dto.TimeZone != info.TimeZone || dto.BookingHorizonDays != 45 || dto.RefusalMessage != "specific" {
		t.Errorf("owner read = %d %+v", code, dto)
	}
	code, body = ownerCall(r, owner, http.MethodGet, "/api/scheduling-links", "")
	got := decodeOwnedShared(t, bytes.NewBufferString(body))
	if code != http.StatusOK || len(got["owned"]) != 1 || got["owned"][0].TimeZone != "Europe/Paris" || got["owned"][0].RefusalMessage != "specific" {
		t.Errorf("owner list = %d %+v", code, got)
	}
	// The listing used that zone: 09:00 Paris is 08:00Z in January.
	if l := b.slots(link.Slug, "2030-01-08", 30); l.code != http.StatusOK || l.slots[0].Start.UTC().Format("15:04") != "08:00" {
		t.Errorf("listing first slot = %v, want 08:00Z", l.starts())
	}
}

// AC-33: an explicit date is a calendar day in the link's zone; "today" in
// the zone is in the horizon even when it is another day in UTC.
func TestPAC72_ExplicitDateIsADayInTheLinksZone(t *testing.T) {
	t.Run("ahead of UTC", func(t *testing.T) {
		// 2030-01-07 11:00Z is 2030-01-08 01:00 at UTC+14.
		b := newPAC72Bench(t, time.Date(2030, 1, 7, 11, 0, 0, 0, time.UTC))
		owner, _ := b.host("pac72-ac33-kiri@example.com")
		b.setZone(owner, "Pacific/Kiritimati")
		link := b.link("pac72-ac33-kiri", owner, func(l *storage.SchedulingLink) { everyDay(l); l.BookingHorizonDays = 1 }, owner)
		if got := b.slots(link.Slug, "2030-01-08", 30); got.code != http.StatusOK || len(got.slots) == 0 || !got.slots[0].Start.Equal(time.Date(2030, 1, 7, 19, 0, 0, 0, time.UTC)) {
			t.Errorf("2030-01-08 (today in the zone, tomorrow in UTC) = %d %v, want slots from 19:00Z", got.code, got.starts())
		}
		if got := b.slots(link.Slug, "2030-01-07", 30); got.code != http.StatusOK || len(got.slots) != 0 {
			t.Errorf("2030-01-07 (yesterday in the zone) = %v, want none", got.starts())
		}
	})
	t.Run("behind UTC", func(t *testing.T) {
		// 2030-01-08 03:00Z is 2030-01-07 19:00 PST.
		b := newPAC72Bench(t, time.Date(2030, 1, 8, 3, 0, 0, 0, time.UTC))
		owner, _ := b.host("pac72-ac33-la@example.com")
		b.setZone(owner, "America/Los_Angeles")
		link := b.link("pac72-ac33-la", owner, func(l *storage.SchedulingLink) {
			everyDay(l)
			l.BookingHorizonDays = 1
			l.WindowStart, l.WindowEnd = "09:00", "23:00"
		}, owner)
		got := b.slots(link.Slug, "2030-01-07", 30)
		if got.code != http.StatusOK || !got.offers(time.Date(2030, 1, 8, 3, 15, 0, 0, time.UTC)) {
			t.Errorf("2030-01-07 (today in the zone, yesterday in UTC) = %d %v, want 19:15 PST offered", got.code, got.starts())
		}
		b.bookOK("19:15 PST", link.Slug, time.Date(2030, 1, 8, 3, 15, 0, 0, time.UTC), 30)
		if got := b.slots(link.Slug, "2030-01-08", 30); len(got.slots) != 0 {
			t.Errorf("2030-01-08 (tomorrow in the zone, beyond a 1-day horizon) = %v", got.starts())
		}
	})
}

// AC-34, AC-35, AC-37: every refusal cause under both settings, with the
// exact body; the precedence pairs; a past-day start.
func TestPAC72_RefusalMessages(t *testing.T) {
	type cause struct {
		name   string
		now    time.Time
		start  time.Time
		raw    string // overrides start
		mod    func(*storage.SchedulingLink)
		setup  func(b *pac72Bench, owner uuid.UUID, bearer string, link *storage.SchedulingLink)
		noHost bool
		reason string
	}
	mon6 := pac72At(pac72Mon, "06:00")
	tue10 := pac72At(pac72Tue, "10:00")
	seedThis := func(status string) func(*pac72Bench, uuid.UUID, string, *storage.SchedulingLink) {
		return func(b *pac72Bench, _ uuid.UUID, _ string, link *storage.SchedulingLink) {
			b.booking(link, tue10, tue10.Add(30*time.Minute), status)
		}
	}
	meeting := func(b *pac72Bench, _ uuid.UUID, bearer string, _ *storage.SchedulingLink) {
		b.cal.addEvent(bearer, tue10, tue10.Add(30*time.Minute))
	}
	causes := []cause{
		{name: "inside the notice", now: mon6, start: pac72At(pac72Mon, "06:45"), reason: "too_soon",
			mod: func(l *storage.SchedulingLink) { l.WindowStart = "06:00"; l.MinNoticeMinutes = 60 }},
		{name: "a non-link day", now: mon6, start: pac72At(pac72Sat, "10:00"), reason: "outside_link_hours"},
		{name: "before the window", now: mon6, start: pac72At(pac72Tue, "08:45"), reason: "outside_link_hours"},
		{name: "an end after the window", now: mon6, start: pac72At(pac72Tue, "16:45"), reason: "outside_link_hours"},
		{name: "off the grid", now: mon6, start: pac72At(pac72Tue, "09:05"), reason: "outside_link_hours"},
		{name: "start with seconds", now: mon6, raw: "2030-01-08T10:00:30Z", reason: "outside_link_hours"},
		{name: "beyond the horizon", now: mon6, start: pac72At(pac72Mon.AddDate(0, 0, 60), "10:00"), reason: "beyond_horizon"},
		{name: "a confirmed booking on this link", now: mon6, start: tue10, setup: seedThis("confirmed"), reason: "taken"},
		{name: "a pending booking on this link", now: mon6, start: tue10, setup: seedThis("pending"), reason: "taken"},
		{name: "a host calendar event", now: mon6, start: tue10, setup: meeting, reason: "host_busy"},
		{name: "a focus block", now: mon6, start: tue10, reason: "host_busy",
			setup: func(b *pac72Bench, owner uuid.UUID, _ string, _ *storage.SchedulingLink) {
				if err := storage.SaveFocusBlock(b.db, owner, "pac72-r-focus-"+uuid.NewString()[:6], "2030-01-08", tue10, tue10.Add(time.Hour)); err != nil {
					b.t.Fatal(err)
				}
			}},
		{name: "a booking on another link of a host", now: mon6, start: tue10, reason: "host_busy",
			setup: func(b *pac72Bench, owner uuid.UUID, _ string, _ *storage.SchedulingLink) {
				other := b.link("pac72-r-other-"+uuid.NewString()[:6], owner, nil, owner)
				b.booking(other, tue10, tue10.Add(30*time.Minute), "confirmed")
			}},
		{name: "an unreadable host calendar", now: mon6, start: tue10, reason: "host_busy",
			setup: func(b *pac72Bench, _ uuid.UUID, bearer string, _ *storage.SchedulingLink) { b.cal.setFailing(bearer) }},
		{name: "no accepted host", now: mon6, start: tue10, noHost: true, reason: "host_busy"},
		// Precedence pairs (AC-35, revision 2.1).
		{name: "beyond the horizon and a non-link day", now: mon6, start: pac72At(pac72Mon.AddDate(0, 0, 61), "10:00"), reason: "beyond_horizon"},
		{name: "outside the hours and inside the notice", now: pac72At(pac72Tue, "08:00"), start: pac72At(pac72Tue, "08:30"), reason: "outside_link_hours",
			mod: func(l *storage.SchedulingLink) { l.MinNoticeMinutes = 120 }},
		{name: "inside the notice and taken", now: mon6, start: pac72At(pac72Mon, "06:45"), reason: "too_soon",
			mod: func(l *storage.SchedulingLink) { l.WindowStart = "06:00"; l.MinNoticeMinutes = 60 },
			setup: func(b *pac72Bench, _ uuid.UUID, _ string, link *storage.SchedulingLink) {
				b.booking(link, pac72At(pac72Mon, "06:45"), pac72At(pac72Mon, "07:15"), "confirmed")
			}},
		{name: "taken and a host meeting", now: mon6, start: tue10, reason: "taken",
			setup: func(b *pac72Bench, owner uuid.UUID, bearer string, link *storage.SchedulingLink) {
				seedThis("confirmed")(b, owner, bearer, link)
				meeting(b, owner, bearer, link)
			}},
		// AC-37: a past day is never beyond the horizon.
		{name: "yesterday on a link day inside the window", now: pac72At(pac72Tue, "06:00"), start: pac72At(pac72Mon, "10:00"), reason: "too_soon"},
		{name: "yesterday on a non-link day", now: pac72At(pac72Mon, "06:00"), start: pac72At(pac72Mon.AddDate(0, 0, -1), "10:00"), reason: "outside_link_hours"},
	}
	for _, setting := range []string{"generic", "specific"} {
		for i, c := range causes {
			t.Run(setting+"/"+c.name, func(t *testing.T) {
				b := newPAC72Bench(t, c.now)
				tag := fmt.Sprintf("pac72-r-%s-%d", setting, i)
				owner, bearer := b.host(tag + "@example.com")
				hosts := []uuid.UUID{owner}
				if c.noHost {
					hosts = nil
				}
				link := b.link(tag, owner, func(l *storage.SchedulingLink) {
					if c.mod != nil {
						c.mod(l)
					}
					l.RefusalMessage = setting
				}, hosts...)
				if c.setup != nil {
					c.setup(b, owner, bearer, link)
				}
				raw := c.raw
				if raw == "" {
					raw = c.start.Format(time.RFC3339)
				}
				before := b.written()
				code, body := b.post(link.Slug, fmt.Sprintf(`{"name":"V","email":"v@example.com","start":%q,"duration":30}`, raw))
				want := c.reason
				if setting == "generic" {
					want = "" // AC-34: exactly the generic body, no reason
				}
				b.refusedAs(c.name, code, body, before, want)
			})
		}
	}
}

// AC-38: a failed read of the owner's settings is a server error everywhere,
// never UTC; with no settings row the same start is evaluated in UTC.
func TestPAC72_SettingsReadFailure(t *testing.T) {
	boom := errors.New("injected settings read failure")
	b := newPAC72Bench(t, pac72At(pac72Mon, "06:00"))
	owner, _ := b.host("pac72-ac38-owner@example.com")
	b.setZone(owner, "Asia/Tokyo")
	link := b.link("pac72-ac38", owner, nil, owner)
	tokyo9 := time.Date(2030, 1, 8, 0, 0, 0, 0, time.UTC) // Tuesday 09:00 JST

	// Sanity: in Tokyo, the start is offered.
	if got := b.slots(link.Slug, "2030-01-08", 30); !got.offers(tokyo9) {
		t.Fatalf("Tokyo listing does not offer 09:00 JST: %v", got.starts())
	}
	failFor := func(victim uuid.UUID) ownerZoneReader {
		return func(db storage.DBTX, user uuid.UUID) (string, bool, error) {
			if user == victim {
				return "", false, boom
			}
			return storage.GetUserTimezone(db, user)
		}
	}
	b.h.ownerZone = failFor(owner)

	before := b.written()
	if code, body := b.book(link.Slug, tokyo9, 30); code != http.StatusInternalServerError || body != `{"error":"booking conflict check failed"}` {
		t.Errorf("booking = %d %s, want 500 booking conflict check failed", code, body)
	}
	// Also for a start the cheap checks would refuse: the zone comes first.
	if code, body := b.book(link.Slug, pac72At(pac72Mon.AddDate(0, 0, 90), "01:00"), 30); code != http.StatusInternalServerError {
		t.Errorf("beyond-horizon booking = %d %s, want 500 (the zone is read first)", code, body)
	}
	if after := b.written(); after != before {
		t.Errorf("something was written: %+v -> %+v", before, after)
	}
	for _, date := range []string{"2030-01-08", ""} {
		if got := b.slots(link.Slug, date, 30); got.code != http.StatusInternalServerError || got.body != `{"error":"internal error"}` {
			t.Errorf("listing date=%q = %d %s, want 500 internal error", date, got.code, got.body)
		}
	}
	if code, _ := b.info(link.Slug); code != http.StatusInternalServerError {
		t.Errorf("public link read = %d, want 500", code)
	}

	// The owner's reads.
	r, lh := b.linkRoutes()
	lh.ownerZone = failFor(owner)
	path := "/api/scheduling-links/" + link.ID.String()
	if code, body := ownerCall(r, owner, http.MethodGet, path, ""); code != http.StatusInternalServerError {
		t.Errorf("get = %d %s, want 500", code, body)
	}
	if code, body := ownerCall(r, owner, http.MethodGet, "/api/scheduling-links", ""); code != http.StatusInternalServerError {
		t.Errorf("list = %d %s, want 500", code, body)
	}
	if code, body := ownerCall(r, owner, http.MethodPatch, path, `{"title":"Changed","booking_horizon_days":5}`); code != http.StatusInternalServerError {
		t.Errorf("update = %d %s, want 500", code, body)
	}
	if l, _ := storage.GetSchedulingLinkByID(b.db, link.ID); l.Title == "Changed" || l.BookingHorizonDays != 60 {
		t.Errorf("a failed update changed the link: %+v", l)
	}
	if code, body := ownerCall(r, owner, http.MethodPost, "/api/scheduling-links", `{"title":"New","slug":"pac72-ac38-new"}`); code != http.StatusInternalServerError {
		t.Errorf("create = %d %s, want 500", code, body)
	}
	if l, _ := storage.GetSchedulingLinkBySlug(b.db, "pac72-ac38-new"); l != nil {
		t.Errorf("a failed create created a link")
	}
	// A shared link whose owner's read fails fails the co-host's list too.
	co, _ := b.host("pac72-ac38-co@example.com")
	if _, err := storage.AddLinkHost(b.db, link.ID, co, "accepted"); err != nil {
		t.Fatal(err)
	}
	if code, body := ownerCall(r, co, http.MethodGet, "/api/scheduling-links", ""); code != http.StatusInternalServerError {
		t.Errorf("co-host list with the shared link = %d %s, want 500", code, body)
	}
	lh.ownerZone = nil
	if code, _ := ownerCall(r, co, http.MethodGet, "/api/scheduling-links", ""); code != http.StatusOK {
		t.Errorf("co-host list without the failure = %d, want 200", code)
	}

	// No settings row: the same start is evaluated in UTC (00:00Z is before
	// a 09:00-17:00 UTC window).
	b.h.ownerZone = nil
	plain, _ := b.host("pac72-ac38-plain@example.com")
	other := b.link("pac72-ac38-plain", plain, nil, plain)
	before = b.written()
	code, body := b.book(other.Slug, tokyo9, 30)
	b.refusedAs("no settings row: 00:00Z in UTC", code, body, before, "")
	if code, info := b.info(other.Slug); code != http.StatusOK || info.TimeZone != "UTC" {
		t.Errorf("no settings row: zone %q (%d), want UTC", info.TimeZone, code)
	}
}

// AC-39: a 365-day horizon whose date-less listing outlasts the request
// budget answers PAC-81's 503, never a shorter 200.
func TestPAC72_LongHorizonDatelessListingIs503(t *testing.T) {
	b := newPAC72Bench(t, pac72At(pac72Mon, "06:00"))
	owner, _ := b.host("pac72-ac39@example.com")
	link := b.link("pac72-ac39", owner, func(l *storage.SchedulingLink) { everyDay(l); l.BookingHorizonDays = 365 }, owner)
	b.cal.mu.Lock()
	b.cal.delay = 20 * time.Millisecond
	b.cal.mu.Unlock()
	// A small stop margin, so per-host reads start until the budget is
	// nearly spent: the listing runs out partway through the 365 days
	// (365 x 20 ms is far beyond the 1 s budget), not before its first day.
	t.Cleanup(peritem.SetForTest(peritem.Settings{Workers: 4, Margin: 50 * time.Millisecond}))
	r := chi.NewRouter()
	r.Use(budget.Middleware(time.Second, nil))
	r.Get("/api/book/{slug}/slots", b.h.getSlots)
	rec := httptest.NewRecorder()
	began := time.Now()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/book/"+link.Slug+"/slots?duration=30", nil))
	if rec.Code != http.StatusServiceUnavailable || strings.TrimSpace(rec.Body.String()) != `{"error":"request timed out"}` {
		t.Fatalf("date-less listing = %d %s, want 503 request timed out", rec.Code, rec.Body.String())
	}
	if took := time.Since(began); took < 500*time.Millisecond {
		t.Fatalf("the listing answered after %v: it did not run partway before the budget ended", took)
	}
	// The same listing for one date fits in the budget.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/book/"+link.Slug+"/slots?duration=30&date=2030-06-03", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("one date = %d %s, want 200", rec.Code, rec.Body.String())
	}
}

// AC-40: with specific refusals and busy-time storage failing, a start the
// cheap checks refuse gets its 409 reason, not the 500 (order of
// evaluation); an otherwise offerable start is the 500.
func TestPAC72_CheapRefusalsBeforeBusyTime(t *testing.T) {
	boom := errors.New("injected storage failure")
	b := newPAC72Bench(t, pac72At(pac72Mon, "06:00"))
	owner, _ := b.host("pac72-ac40@example.com")
	link := b.link("pac72-ac40", owner, func(l *storage.SchedulingLink) {
		l.RefusalMessage = "specific"
		l.WindowStart = "06:00"
		l.MinNoticeMinutes = 60
	}, owner)
	b.flow.Reads = engine.BookingReads{
		FocusBlocks: func(storage.DBTX, uuid.UUID, time.Time) ([]storage.FocusBlock, error) { return nil, boom },
		HostBookings: func(storage.DBTX, uuid.UUID, time.Time, time.Time) ([]time.Time, error) {
			return nil, boom
		},
	}
	for _, tc := range []struct {
		name   string
		start  time.Time
		reason string
	}{
		{"beyond the horizon", pac72At(pac72Mon.AddDate(0, 0, 70), "10:00"), "beyond_horizon"},
		{"outside the hours", pac72At(pac72Tue, "05:00"), "outside_link_hours"},
		{"inside the notice", pac72At(pac72Mon, "06:30"), "too_soon"},
	} {
		before := b.written()
		code, body := b.book(link.Slug, tc.start, 30)
		b.refusedAs(tc.name, code, body, before, tc.reason)
	}
	before := b.written()
	if code, body := b.book(link.Slug, pac72At(pac72Tue, "10:00"), 30); code != http.StatusInternalServerError {
		t.Errorf("offerable start with busy time unreadable = %d %s, want 500", code, body)
	}
	if after := b.written(); after != before {
		t.Errorf("something was written")
	}
}
