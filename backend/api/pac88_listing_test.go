package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
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

// PAC-88 contract tests for GET /api/book/{slug}/slots (contract
// contracts/features/PAC-88.md sections 2 and 7): the day zone, the bounded
// overview, validation, the response echo, one calendar read per host, and
// the completeness of that read. Real handlers, real engine, test database,
// a paging calendar stand-in, a fixed clock.

// pac88Now is the fixed clock of most tests: Monday 2027-03-01 06:00 UTC, so
// the NY (03-14) and EU (03-28) spring changes, the EU (10-31) and NY (11-07)
// fall-backs and the southern-summer Mondays are inside a 365-day horizon.
var pac88Now = time.Date(2027, 3, 1, 6, 0, 0, 0, time.UTC)

func pac88Date(s string) time.Time {
	d, _ := time.Parse(time.DateOnly, s)
	return d
}

// pac88Link creates a link owned by owner (whose zone is set to tz, or no
// settings row when tz is empty), one duration of 30, no notice or buffers,
// hosted by hosts.
func (b *pac88Bench) pac88Link(slug, tz string, owner uuid.UUID, days []int, open, close string, horizon int, hosts ...uuid.UUID) *storage.SchedulingLink {
	b.t.Helper()
	if tz != "" {
		b.setZone(owner, tz)
	}
	return b.link(slug, owner, func(l *storage.SchedulingLink) {
		l.DurationOptions = []int{30}
		l.DaysOfWeek = days
		l.WindowStart, l.WindowEnd = open, close
		l.BookingHorizonDays = horizon
	}, hosts...)
}

var pac88EveryDay = []int{0, 1, 2, 3, 4, 5, 6}

// --- C-1: equivalence ---------------------------------------------------------

// pac88Explicit caches PAC-72 explicit-date answers (no display_time_zone) by
// link day.
type pac88Explicit struct {
	b     *pac88Bench
	slug  string
	cache map[string][]engine.AvailableSlot
}

func (e *pac88Explicit) day(d time.Time) []engine.AvailableSlot {
	e.b.t.Helper()
	key := d.Format(time.DateOnly)
	if s, ok := e.cache[key]; ok {
		return s
	}
	a := e.b.get(e.slug, pac88Q("duration", "30", "date", key))
	if a.code != http.StatusOK {
		e.b.t.Fatalf("%s explicit %s = %d %s", e.slug, key, a.code, a.body)
	}
	e.cache[key] = a.slots
	return a.slots
}

// regroup is the contract's expected answer: the explicit-date answers of
// every link day from-2..to+2 (a superset of the needed link days, offsets
// being within +-14 h), each start under its date in z.
func (e *pac88Explicit) regroup(from, to time.Time, z *time.Location) map[string][]string {
	out := map[string][]string{}
	for d := from.AddDate(0, 0, -2); !d.After(to.AddDate(0, 0, 2)); d = d.AddDate(0, 0, 1) {
		for _, s := range e.day(d) {
			key := s.Start.In(z).Format(time.DateOnly)
			if key >= from.Format(time.DateOnly) && key <= to.Format(time.DateOnly) {
				out[key] = append(out[key], s.Start.UTC().Format(time.RFC3339)+"/"+s.End.UTC().Format(time.RFC3339))
			}
		}
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}

// C-1 / AC-11: for every link, day zone, mode and range, the answer is the
// regrouping of the PAC-72 explicit-date answers by the day rule.
func TestPAC88_C1_DayZoneAnswerIsTheRegroupedExplicitDates(t *testing.T) {
	b := newPAC88Bench(t, pac88Now)
	paris, parisBearer := b.host("pac88-c1-paris@example.com")
	ny, nyBearer := b.host("pac88-c1-ny@example.com")
	utc, _ := b.host("pac88-c1-utc@example.com")
	london, _ := b.host("pac88-c1-london@example.com")
	// Busy time, so the answers are not just the grid.
	b.cal88.addEvent(parisBearer, time.Date(2027, 3, 2, 10, 0, 0, 0, time.UTC), time.Date(2027, 3, 2, 11, 0, 0, 0, time.UTC))
	b.cal88.addEvent(nyBearer, time.Date(2027, 3, 14, 7, 0, 0, 0, time.UTC), time.Date(2027, 3, 14, 7, 30, 0, 0, time.UTC))
	b.cal88.addEvent(nyBearer, time.Date(2027, 11, 7, 5, 0, 0, 0, time.UTC), time.Date(2027, 11, 7, 6, 0, 0, 0, time.UTC))

	links := []*storage.SchedulingLink{
		b.pac88Link("pac88-c1-paris", "Europe/Paris", paris, []int{1, 2, 3, 4, 5}, "09:00", "17:00", 60, paris),
		b.pac88Link("pac88-c1-ny", "America/New_York", ny, pac88EveryDay, "00:00", "23:59", 365, ny),
		b.pac88Link("pac88-c1-utc", "", utc, []int{0}, "05:00", "07:00", 365, utc),
		b.pac88Link("pac88-c1-london", "Europe/London", london, pac88EveryDay, "00:00", "03:00", 365, london),
		b.pac88Link("pac88-c1-oneday", "", paris, pac88EveryDay, "09:00", "17:00", 1, paris),
	}
	zones := []string{"", "UTC", "Pacific/Kiritimati", "Etc/GMT+12", "Asia/Kolkata", "Asia/Kathmandu", "America/New_York", "Pacific/Auckland"}
	days := []string{"2027-02-28", "2027-03-01", "2027-03-02", "2027-03-13", "2027-03-14", "2027-03-15", "2027-03-27", "2027-03-28",
		"2027-03-29", "2027-04-29", "2027-04-30", "2027-10-30", "2027-10-31", "2027-11-06", "2027-11-07", "2027-11-08", "2028-02-28", "2028-02-29", "2028-03-01"}
	ranges := [][2]string{{"", ""}, {"2027-03-10", "2027-04-20"}, {"2027-10-20", "2027-11-30"}, {"2028-02-01", "2028-03-13"}}

	for _, link := range links {
		ex := &pac88Explicit{b: b, slug: link.Slug, cache: map[string][]engine.AvailableSlot{}}
		_, info := b.info(link.Slug)
		for _, zname := range zones {
			z := mustZone(t, info.TimeZone)
			if zname != "" {
				z = mustZone(t, zname)
			}
			for _, d := range days {
				q := pac88Q("duration", "30", "date", d)
				if zname != "" {
					q.Set("display_time_zone", zname)
				}
				got := b.get(link.Slug, q)
				if got.code != http.StatusOK {
					t.Fatalf("%s %s %s = %d %s", link.Slug, zname, d, got.code, got.body)
				}
				want := ex.regroup(pac88Date(d), pac88Date(d), z)[d]
				var have []string
				for _, s := range got.slots {
					have = append(have, s.Start.UTC().Format(time.RFC3339)+"/"+s.End.UTC().Format(time.RFC3339))
				}
				if !sort.StringsAreSorted(have) {
					t.Errorf("%s %s %s: slots not ascending", link.Slug, zname, d)
				}
				if strings.Join(have, ",") != strings.Join(want, ",") {
					t.Errorf("%s in %q on %s:\n got %v\nwant %v", link.Slug, zname, d, have, want)
				}
			}
			for _, rg := range ranges {
				q := pac88Q("duration", "30")
				if rg[0] != "" {
					q.Set("from", rg[0])
					q.Set("to", rg[1])
				}
				if zname != "" {
					q.Set("display_time_zone", zname)
				}
				got := b.get(link.Slug, q)
				if got.code != http.StatusOK {
					t.Fatalf("%s %s overview %v = %d %s", link.Slug, zname, rg, got.code, got.body)
				}
				regrouped := ex.regroup(pac88Date(got.from), pac88Date(got.to), z)
				want := []string{}
				for k := range regrouped {
					want = append(want, k)
				}
				sort.Strings(want)
				if strings.Join(got.dates, ",") != strings.Join(want, ",") {
					t.Errorf("%s in %q overview %s..%s:\n got %v\nwant %v", link.Slug, zname, got.from, got.to, got.dates, want)
				}
			}
		}
	}
}

// --- fixed-date regrouping (AC-10, AC-17, AC-18, AC-18a, AC-29) -------------------

func TestPAC88_FixedDateRegrouping(t *testing.T) {
	b := newPAC88Bench(t, pac88Now)
	paris, _ := b.host("pac88-fix-paris@example.com")
	ny, _ := b.host("pac88-fix-ny@example.com")
	utc, _ := b.host("pac88-fix-utc@example.com")
	london, _ := b.host("pac88-fix-london@example.com")
	monday := b.pac88Link("pac88-fix-monday", "Europe/Paris", paris, []int{1}, "09:00", "17:00", 365, paris)
	nyLink := b.pac88Link("pac88-fix-ny", "America/New_York", ny, []int{0}, "01:00", "04:00", 365, ny)
	utcLink := b.pac88Link("pac88-fix-utc", "", utc, []int{0}, "05:00", "07:00", 365, utc)
	londonLink := b.pac88Link("pac88-fix-london", "Europe/London", london, []int{0}, "00:00", "03:00", 365, london)

	day := func(slug, date, zone string) []string {
		t.Helper()
		a := b.get(slug, pac88Q("duration", "30", "date", date, "display_time_zone", zone))
		if a.code != http.StatusOK {
			t.Fatalf("%s %s %s = %d %s", slug, date, zone, a.code, a.body)
		}
		return startsIn(a.slots, mustZone(t, zone))
	}
	wall := func(starts []string) string {
		var out []string
		for _, s := range starts {
			out = append(out, s[11:])
		}
		return strings.Join(out, ",")
	}

	t.Run("AC-10 Monday-only Paris shown in Pacific/Auckland", func(t *testing.T) {
		mon := day(monday.Slug, "2027-12-06", "Pacific/Auckland")
		tue := day(monday.Slug, "2027-12-07", "Pacific/Auckland")
		if len(mon) != 12 || mon[0][11:16] != "21:00" || mon[11][11:16] != "23:45" {
			t.Errorf("Monday (Auckland) = %v, want the 12 starts 21:00..23:45", mon)
		}
		if len(tue) != 19 || tue[0][11:16] != "00:00" || tue[18][11:16] != "04:30" {
			t.Errorf("Tuesday (Auckland) = %v, want the 19 starts 00:00..04:30", tue)
		}
		for _, other := range []string{"2027-12-05", "2027-12-08", "2027-12-12"} {
			if got := day(monday.Slug, other, "Pacific/Auckland"); len(got) != 0 {
				t.Errorf("%s (Auckland) lists %v, want nothing", other, got)
			}
		}
		ov := b.get(monday.Slug, pac88Q("duration", "30", "from", "2027-12-01", "to", "2027-12-31", "display_time_zone", "Pacific/Auckland"))
		if ov.code != http.StatusOK || strings.Join(ov.dates, ",") != "2027-12-06,2027-12-07,2027-12-13,2027-12-14,2027-12-20,2027-12-21,2027-12-27,2027-12-28" {
			t.Errorf("December overview (Auckland) = %d %v", ov.code, ov.dates)
		}
	})

	t.Run("AC-17 NY spring-forward Sunday", func(t *testing.T) {
		got := wall(day(nyLink.Slug, "2027-03-14", "America/New_York"))
		want := "01:00 -05:00,01:15 -05:00,01:30 -05:00,01:45 -05:00,03:00 -04:00,03:15 -04:00,03:30 -04:00"
		if got != want {
			t.Errorf("2027-03-14 (New York) = %s\nwant %s", got, want)
		}
	})

	t.Run("AC-18 UTC Sundays shown on the NY fall-back", func(t *testing.T) {
		got := wall(day(utcLink.Slug, "2027-11-07", "America/New_York"))
		want := "01:00 -04:00,01:15 -04:00,01:30 -04:00,01:45 -04:00,01:00 -05:00,01:15 -05:00,01:30 -05:00"
		if got != want {
			t.Errorf("2027-11-07 (New York) = %s\nwant %s", got, want)
		}
	})

	t.Run("AC-18a London fall-back shown in London", func(t *testing.T) {
		starts := day(londonLink.Slug, "2027-10-31", "Europe/London")
		seen := map[string]bool{}
		for _, s := range starts {
			hm := s[11:16]
			if seen[hm] {
				t.Errorf("wall time %s listed twice: %v", hm, starts)
			}
			seen[hm] = true
		}
		got := wall(starts)
		want := "00:00 +01:00,00:15 +01:00,00:30 +01:00,00:45 +01:00,01:00 +01:00,01:15 +01:00,01:30 +01:00,01:45 +01:00,02:00 +00:00,02:15 +00:00,02:30 +00:00"
		if got != want {
			t.Errorf("2027-10-31 (London) = %s\nwant %s", got, want)
		}
	})

	t.Run("AC-29 Paris Tuesday 16:15 and 16:30 under Wednesday in Tokyo", func(t *testing.T) {
		b2 := newPAC88Bench(t, time.Date(2027, 1, 12, 15, 5, 0, 0, time.UTC)) // Tuesday 16:05 Paris
		owner, _ := b2.host("pac88-ac29@example.com")
		link := b2.pac88Link("pac88-ac29", "Europe/Paris", owner, []int{2}, "09:00", "17:00", 60, owner)
		wed := b2.get(link.Slug, pac88Q("duration", "30", "date", "2027-01-13", "display_time_zone", "Asia/Tokyo"))
		if wed.code != http.StatusOK || wall(startsIn(wed.slots, mustZone(t, "Asia/Tokyo"))) != "00:15 +09:00,00:30 +09:00" {
			t.Errorf("Wednesday (Tokyo) = %d %v, want 00:15 and 00:30", wed.code, startsIn(wed.slots, mustZone(t, "Asia/Tokyo")))
		}
		ov := b2.get(link.Slug, pac88Q("duration", "30", "display_time_zone", "Asia/Tokyo"))
		if ov.code != http.StatusOK || len(ov.dates) == 0 || ov.dates[0] != "2027-01-13" || ov.from != "2027-01-13" {
			t.Errorf("overview (Tokyo) = %d %v from %s, want first day 2027-01-13 (today in Tokyo)", ov.code, ov.dates, ov.from)
		}
	})
}

// --- C-2: validation ----------------------------------------------------------

func TestPAC88_C2_ValidationMessagesAndOrder(t *testing.T) {
	b := newPAC88Bench(t, pac88Now)
	owner, _ := b.host("pac88-c2@example.com")
	link := b.pac88Link("pac88-c2", "Europe/Paris", owner, pac88EveryDay, "09:00", "17:00", 60, owner)
	const zoneMsg = `{"error":"invalid display_time_zone, use an IANA time zone name"}`
	bad := func(msg string) string { return fmt.Sprintf(`{"error":%q}`, msg) }

	for _, tc := range []struct {
		name string
		slug string
		kv   []string
		code int
		body string
	}{
		{"unknown slug before zone", "pac88-no-such-link", []string{"display_time_zone", "Not/AZone"}, 404, bad("not found")},
		{"duration before zone", "", []string{"duration", "45", "display_time_zone", "Not/AZone"}, 400, bad("invalid duration")},
		{"zone before combination", "", []string{"display_time_zone", "Not/AZone", "date", "2027-03-02", "from", "2027-03-02"}, 400, zoneMsg},
		{"zone before date", "", []string{"display_time_zone", "Not/AZone", "date", "bad"}, 400, zoneMsg},
		{"date with from", "", []string{"date", "2027-03-02", "from", "2027-03-02"}, 400, bad("date cannot be combined with from or to")},
		{"date with to", "", []string{"date", "2027-03-02", "to", "2027-03-02"}, 400, bad("date cannot be combined with from or to")},
		{"combination before date format", "", []string{"date", "bad", "to", "bad"}, 400, bad("date cannot be combined with from or to")},
		{"date format", "", []string{"date", "2027-3-2"}, 400, bad("invalid date format, use YYYY-MM-DD")},
		{"from before to", "", []string{"from", "x", "to", "y"}, 400, bad("invalid from, use YYYY-MM-DD")},
		{"from format before pairing", "", []string{"from", "x"}, 400, bad("invalid from, use YYYY-MM-DD")},
		{"to format", "", []string{"from", "2027-03-02", "to", "2027-03-32"}, 400, bad("invalid to, use YYYY-MM-DD")},
		{"to format before pairing", "", []string{"to", "y"}, 400, bad("invalid to, use YYYY-MM-DD")},
		{"from alone", "", []string{"from", "2027-03-02"}, 400, bad("from and to must be given together")},
		{"to alone", "", []string{"to", "2027-03-02"}, 400, bad("from and to must be given together")},
		{"pairing before order", "", []string{"to", "2027-03-01"}, 400, bad("from and to must be given together")},
		{"order", "", []string{"from", "2027-03-02", "to", "2027-03-01"}, 400, bad("to must not be before from")},
		{"order before span", "", []string{"from", "2027-06-01", "to", "2027-03-01"}, 400, bad("to must not be before from")},
		{"span 43 days", "", []string{"from", "2027-03-01", "to", "2027-04-12"}, 400, bad("range must not exceed 42 days")},
		{"span 42 days", "", []string{"from", "2027-03-01", "to", "2027-04-11"}, 200, ""},
		{"one day range", "", []string{"from", "2027-03-01", "to", "2027-03-01"}, 200, ""},
		{"empty values are absent", "", []string{"display_time_zone", "", "date", "", "from", "", "to", ""}, 200, ""},
	} {
		slug := tc.slug
		if slug == "" {
			slug = link.Slug
		}
		a := b.get(slug, pac88Q(tc.kv...))
		if a.code != tc.code || (tc.body != "" && a.body != tc.body) {
			t.Errorf("%s: %d %s, want %d %s", tc.name, a.code, a.body, tc.code, tc.body)
		}
		if a.code == http.StatusBadRequest && a.header.Get("Content-Type") != "application/json" {
			t.Errorf("%s: Content-Type %q", tc.name, a.header.Get("Content-Type"))
		}
	}

	refused := []string{"Not/AZone", "Local", "localtime", "posixrules", "right/Europe/Paris", "right/UTC", "europe/paris",
		"../etc/passwd", "/etc/localtime", "Europe/Paris/", "A/B/C/D", " UTC", "Europe/Paris ", "1Europe", "Europe//Paris",
		strings.Repeat("A", 65), "America/" + strings.Repeat("B", 57)}
	for _, z := range refused {
		if a := b.get(link.Slug, pac88Q("display_time_zone", z, "date", "2027-03-02")); a.code != http.StatusBadRequest || a.body != zoneMsg {
			t.Errorf("display_time_zone %q: %d %s, want the display-zone 400", z, a.code, a.body)
		}
	}
	for _, z := range []string{"UTC", "Etc/UTC", "Etc/GMT+12", "Etc/GMT-14", "Asia/Calcutta", "US/Eastern", "America/Argentina/Buenos_Aires"} {
		if a := b.get(link.Slug, pac88Q("display_time_zone", z, "date", "2027-03-02")); a.code != http.StatusOK || a.display != z {
			t.Errorf("display_time_zone %q: %d %s, want 200 echoing it", z, a.code, a.body)
		}
	}

	// The link's zone (500) comes before the display zone.
	b.h.ownerZone = func(storage.DBTX, uuid.UUID) (string, bool, error) { return "", false, errors.New("settings down") }
	if a := b.get(link.Slug, pac88Q("display_time_zone", "Not/AZone")); a.code != http.StatusInternalServerError {
		t.Errorf("link zone failure with a bad zone: %d %s, want 500", a.code, a.body)
	}
}

// --- C-3: one calendar read per host -------------------------------------------

func TestPAC88_C3_OneCalendarReadPerHost(t *testing.T) {
	b := newPAC88Bench(t, pac88Now)
	owner, ob := b.host("pac88-c3-owner@example.com")
	co, cb := b.host("pac88-c3-co@example.com")
	b.setZone(co, "America/Los_Angeles")
	link := b.pac88Link("pac88-c3", "Europe/Paris", owner, pac88EveryDay, "09:00", "17:00", 365, owner, co)
	adelaide, ab := b.host("pac88-c3-adelaide@example.com")
	adLink := b.pac88Link("pac88-c3-adelaide", "Australia/Adelaide", adelaide, pac88EveryDay, "00:00", "23:59", 365, adelaide)

	check := func(what string, q map[string]int, want int, bearers ...string) {
		t.Helper()
		for _, br := range bearers {
			if q[br] != want {
				t.Errorf("%s: %d reads of %s, want %d", what, q[br], br, want)
			}
		}
	}
	for _, tc := range []struct {
		name string
		kv   []string
	}{
		{"default 42-day overview", []string{"duration", "30"}},
		{"42-day overview in Auckland", []string{"duration", "30", "from", "2027-06-01", "to", "2027-07-12", "display_time_zone", "Pacific/Auckland"}},
		{"one day in Kiritimati", []string{"duration", "30", "date", "2027-03-10", "display_time_zone", "Pacific/Kiritimati"}},
	} {
		b.cal88.resetCounts()
		if a := b.get(link.Slug, pac88Q(tc.kv...)); a.code != http.StatusOK || (len(a.dates) == 0 && len(a.slots) == 0) {
			t.Fatalf("%s: %d %s", tc.name, a.code, a.body)
		}
		reads, lists := b.cal88.counts()
		check(tc.name, reads, 1, ob, cb)
		check(tc.name+" (pages)", lists, 1, ob, cb)
	}

	// A Brisbane day (UTC+10) on Adelaide's spring-forward day spans three
	// Adelaide link days (2027-10-02 tail, the 23-hour 10-03, 10-04 head).
	from, to := pac88Date("2027-10-03"), pac88Date("2027-10-03")
	if n := len(neededLinkDays(from, to, mustZone(t, "Australia/Brisbane"), mustZone(t, "Australia/Adelaide"))); n != 3 {
		t.Fatalf("needed link days = %d, want 3", n)
	}
	b.cal88.resetCounts()
	if a := b.get(adLink.Slug, pac88Q("duration", "30", "date", "2027-10-03", "display_time_zone", "Australia/Brisbane")); a.code != http.StatusOK || len(a.slots) == 0 {
		t.Fatalf("Brisbane day: %d %s", a.code, a.body)
	}
	reads, _ := b.cal88.counts()
	check("three link days", reads, 1, ab)

	// A range wholly outside the horizon reads no host.
	b.cal88.resetCounts()
	if a := b.get(link.Slug, pac88Q("duration", "30", "from", "2028-06-01", "to", "2028-07-01")); a.code != http.StatusOK || len(a.dates) != 0 {
		t.Fatalf("beyond the horizon: %d %s", a.code, a.body)
	}
	if reads, _ := b.cal88.counts(); len(reads) != 0 {
		t.Errorf("beyond the horizon: reads %v, want none", reads)
	}
	// So does a range before today.
	if a := b.get(link.Slug, pac88Q("duration", "30", "from", "2027-01-01", "to", "2027-01-31")); a.code != http.StatusOK || len(a.dates) != 0 {
		t.Fatalf("before today: %d %s", a.code, a.body)
	}
	if reads, _ := b.cal88.counts(); len(reads) != 0 {
		t.Errorf("before today: reads %v, want none", reads)
	}
}

// --- C-4: storage failures are the 500 ---------------------------------------

func TestPAC88_C4_StorageFailureIs500InBothModes(t *testing.T) {
	boom := errors.New("injected storage failure")
	target := "2027-03-30" // day 30 of the default overview
	for _, tc := range []struct {
		name  string
		reads engine.BookingReads
	}{
		{"accepted hosts", engine.BookingReads{AcceptedHosts: func(storage.DBTX, uuid.UUID) ([]*storage.LinkHost, error) { return nil, boom }}},
		{"focus blocks", engine.BookingReads{FocusBlocks: func(db storage.DBTX, u uuid.UUID, start time.Time) ([]storage.FocusBlock, error) {
			if start.Format(time.DateOnly) == target {
				return nil, boom
			}
			return storage.ListFocusBlocksForUserWeek(db, u, start)
		}}},
		{"host bookings", engine.BookingReads{HostBookings: func(db storage.DBTX, u uuid.UUID, s, e time.Time) ([]time.Time, error) {
			if s.Format(time.DateOnly) == target {
				return nil, boom
			}
			return storage.GetBusyBookingsForUser(db, u, s, e)
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newPAC88Bench(t, pac88Now)
			owner, _ := b.host("pac88-c4-" + strings.ReplaceAll(tc.name, " ", "-") + "@example.com")
			link := b.pac88Link("pac88-c4-"+strings.ReplaceAll(tc.name, " ", "-"), "Europe/Paris", owner, pac88EveryDay, "09:00", "17:00", 60, owner)
			if a := b.get(link.Slug, pac88Q("duration", "30")); a.code != http.StatusOK || len(a.dates) != 42 {
				t.Fatalf("before the failure: %d %v", a.code, a.dates)
			}
			b.flow.Reads = tc.reads
			for _, q := range []map[string]string{
				{"duration": "30"},
				{"duration": "30", "date": target},
				{"duration": "30", "date": target, "display_time_zone": "Asia/Tokyo"},
				{"duration": "30", "from": "2027-03-25", "to": "2027-04-05", "display_time_zone": "America/New_York"},
			} {
				var kv []string
				for k, v := range q {
					kv = append(kv, k, v)
				}
				if a := b.get(link.Slug, pac88Q(kv...)); a.code != http.StatusInternalServerError || a.body != `{"error":"internal error"}` {
					t.Errorf("%v: %d %s, want 500 internal error", q, a.code, a.body)
				}
			}
		})
	}
}

// --- C-5 / AC-26 / AC-31: the echo ---------------------------------------------

func TestPAC88_C5_ResponseEcho(t *testing.T) {
	b := newPAC88Bench(t, pac88Now)
	owner, _ := b.host("pac88-c5@example.com")
	link := b.pac88Link("pac88-c5", "Europe/Paris", owner, pac88EveryDay, "09:00", "17:00", 60, owner)

	a := b.get(link.Slug, pac88Q("duration", "30", "date", "2027-03-02"))
	_, info := b.info(link.Slug)
	if a.zone != "Europe/Paris" || a.zone != info.TimeZone || a.display != a.zone || a.from != "2027-03-02" || a.to != "2027-03-02" || len(a.dates) != 0 || len(a.slots) == 0 {
		t.Errorf("date mode echo: %+v", a)
	}
	a = b.get(link.Slug, pac88Q("duration", "30", "date", "2027-03-02", "display_time_zone", "Asia/Calcutta"))
	if a.display != "Asia/Calcutta" || a.zone != "Europe/Paris" {
		t.Errorf("link names are echoed as sent: display %q zone %q", a.display, a.zone)
	}
	for _, tc := range []struct{ zone, from, to string }{
		{"", "2027-03-01", "2027-04-11"},
		{"Pacific/Kiritimati", "2027-03-01", "2027-04-11"}, // 20:00 there
		{"Etc/GMT+12", "2027-02-28", "2027-04-10"},         // 18:00 the day before
	} {
		q := pac88Q("duration", "30")
		if tc.zone != "" {
			q.Set("display_time_zone", tc.zone)
		}
		a = b.get(link.Slug, q)
		if a.from != tc.from || a.to != tc.to || len(a.slots) != 0 {
			t.Errorf("default overview in %q: %s..%s (%d slots), want %s..%s", tc.zone, a.from, a.to, len(a.slots), tc.from, tc.to)
		}
	}
	// AC-26: the host changes zone between two requests.
	b.setZone(owner, "America/New_York")
	if a = b.get(link.Slug, pac88Q("duration", "30", "date", "2027-03-02")); a.zone != "America/New_York" || a.display != "America/New_York" {
		t.Errorf("after the host's zone change: zone %q display %q", a.zone, a.display)
	}
	if _, info := b.info(link.Slug); info.TimeZone != a.zone {
		t.Errorf("link info zone %q, listing zone %q", info.TimeZone, a.zone)
	}
	// A-4: no settings row is UTC.
	bare := createTestUser(t, "pac88-c5-bare@example.com")
	other := b.pac88Link("pac88-c5-bare", "", bare, pac88EveryDay, "09:00", "17:00", 60, owner)
	if a = b.get(other.Slug, pac88Q("duration", "30")); a.zone != "UTC" || a.display != "UTC" {
		t.Errorf("no settings row: zone %q display %q, want UTC", a.zone, a.display)
	}
}

// --- C-6: no caching headers ---------------------------------------------------

func TestPAC88_C6_NoCachingHeaders(t *testing.T) {
	b := newPAC88Bench(t, pac88Now)
	owner, _ := b.host("pac88-c6@example.com")
	link := b.pac88Link("pac88-c6", "Europe/Paris", owner, pac88EveryDay, "09:00", "17:00", 60, owner)
	for _, q := range [][]string{{"duration", "30"}, {"duration", "30", "date", "2027-03-02", "display_time_zone", "Asia/Tokyo"}} {
		a := b.get(link.Slug, pac88Q(q...))
		if a.code != http.StatusOK {
			t.Fatalf("%v: %d", q, a.code)
		}
		for _, h := range []string{"Cache-Control", "ETag", "Last-Modified", "Expires"} {
			if v := a.header.Get(h); v != "" {
				t.Errorf("%v: %s = %q, want none", q, h, v)
			}
		}
	}
}

// --- C-7: rate limit -------------------------------------------------------------

func TestPAC88_C7_RateLimitCountsEachListingOnceAndComesFirst(t *testing.T) {
	ownerID := createTestUser(t, "pac88-c7-owner@example.com")
	link := createPublicBookingLink(t, ownerID, "pac88-c7", "PAC-88 C-7", 0, "reusable", nil)
	r := setupPublicBookingRoutes(t, &countingBookingFlow{})
	useFakeClock(t)
	base := "/api/book/" + link.Slug + "/slots"
	variants := []string{
		"", "?from=2026-11-01&to=2026-12-12", "?display_time_zone=Asia/Tokyo",
		"?from=2026-11-01&to=2026-12-12&display_time_zone=Pacific/Auckland", "?date=2026-11-02&display_time_zone=UTC",
	}
	for i := 0; i < 120; i++ {
		if got := doPublic(t, r, http.MethodGet, base+variants[i%len(variants)], "", peerA, nil); got.code != http.StatusOK {
			t.Fatalf("request %d: %d %s", i+1, got.code, got.body)
		}
	}
	// The 121st is the 429 whatever its parameters, before any 400 or 404.
	assertRateLimited(t, doPublic(t, r, http.MethodGet, base+"?display_time_zone=Not/AZone", "", peerA, nil), 60)
	assertRateLimited(t, doPublic(t, r, http.MethodGet, base+"?from=2026-11-01", "", peerA, nil), 60)
	assertRateLimited(t, doPublic(t, r, http.MethodGet, "/api/book/pac88-no-such/slots?display_time_zone=x", "", peerA, nil), 60)
	// Another client is unaffected.
	if got := doPublic(t, r, http.MethodGet, base+variants[3], "", peerB, nil); got.code != http.StatusOK {
		t.Fatalf("other client: %d", got.code)
	}
}

// --- C-9 / AC-16b: a 365-day link's last month fits the budget ---------------

// Replaces TestPAC72_LongHorizonDatelessListingIs503: the overview of the
// month of the last bookable day, with a 20 ms calendar, answers 200 inside a
// 1 s budget, and so does every month on the way.
func TestPAC88_C9_LongHorizonOverviewFitsTheBudget(t *testing.T) {
	b := newPAC88Bench(t, pac88Now)
	owner, _ := b.host("pac88-c9@example.com")
	link := b.pac88Link("pac88-c9", "", owner, pac88EveryDay, "09:00", "17:00", 365, owner)
	b.cal88.mu.Lock()
	b.cal88.delay = 20 * time.Millisecond
	b.cal88.mu.Unlock()
	t.Cleanup(peritem.SetForTest(peritem.Settings{Workers: 4, Margin: 50 * time.Millisecond}))
	r := chi.NewRouter()
	r.Use(budget.Middleware(time.Second, nil))
	r.Get("/api/book/{slug}/slots", b.h.getSlots)
	last := pac88Now.AddDate(0, 0, 364).Format(time.DateOnly) // 2028-02-28
	// Month grids (six weeks from the Monday on or before the 1st).
	for _, grid := range [][2]string{{"2027-02-22", "2027-04-04"}, {"2027-08-30", "2027-10-10"}, {"2028-01-31", "2028-03-12"}} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/book/"+link.Slug+"/slots?duration=30&from="+grid[0]+"&to="+grid[1], nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("grid %v: %d %s, want 200", grid, rec.Code, rec.Body.String())
		}
		if grid[0] == "2028-01-31" && !strings.Contains(rec.Body.String(), `"`+last+`"],`) {
			t.Errorf("last month's overview does not end on the last bookable day %s: %s", last, rec.Body.String())
		}
	}
}

// --- C-10: the one read is complete; storage covers every needed day --------

func TestPAC88_C10_CompleteReadAndStorageCoverage(t *testing.T) {
	setup := func(t *testing.T, tag string) (*pac88Bench, *storage.SchedulingLink, uuid.UUID, string) {
		b := newPAC88Bench(t, pac88Now)
		owner, bearer := b.host("pac88-c10-" + tag + "@example.com")
		link := b.pac88Link("pac88-c10-"+tag, "Europe/Paris", owner, pac88EveryDay, "09:00", "17:00", 60, owner)
		return b, link, owner, bearer
	}
	paris := func(t *testing.T, date, hhmm string) time.Time {
		d, _ := time.ParseInLocation("2006-01-02 15:04", date+" "+hhmm, mustZone(t, "Europe/Paris"))
		return d
	}
	day40, last := "2027-04-09", "2027-04-11" // days 40 and 42 of the default overview
	has := func(dates []string, d string) bool {
		for _, x := range dates {
			if x == d {
				return true
			}
		}
		return false
	}

	t.Run("busy event on page 2", func(t *testing.T) {
		b, link, _, bearer := setup(t, "pages")
		b.cal88.pageSize = 2
		b.cal88.addEvent(bearer, paris(t, "2027-03-02", "10:00"), paris(t, "2027-03-02", "11:00"))
		b.cal88.addEvent(bearer, paris(t, "2027-03-03", "10:00"), paris(t, "2027-03-03", "11:00"))
		b.cal88.addEvent(bearer, paris(t, day40, "09:00"), paris(t, day40, "17:00")) // only on page 2
		a := b.get(link.Slug, pac88Q("duration", "30"))
		if a.code != http.StatusOK || has(a.dates, day40) || len(a.dates) != 41 {
			t.Errorf("overview = %d, %d dates (day 40 listed: %v), want 41 without %s", a.code, len(a.dates), has(a.dates, day40), day40)
		}
		reads, lists := b.cal88.counts()
		if reads[bearer] != 1 || lists[bearer] != 2 {
			t.Errorf("reads %d pages %d, want one read of two pages", reads[bearer], lists[bearer])
		}
		// The explicit day agrees (the booking re-reads that day).
		if d := b.get(link.Slug, pac88Q("duration", "30", "date", day40)); d.code != http.StatusOK || len(d.slots) != 0 {
			t.Errorf("%s = %d %v, want no slot", day40, d.code, d.slots)
		}
	})

	t.Run("page 2 fails", func(t *testing.T) {
		b, link, _, bearer := setup(t, "page2fail")
		b.cal88.pageSize = 1
		b.cal88.addEvent(bearer, paris(t, "2027-03-02", "10:00"), paris(t, "2027-03-02", "11:00"))
		b.cal88.addEvent(bearer, paris(t, day40, "10:00"), paris(t, day40, "11:00"))
		b.cal88.failPage[bearer] = 2
		a := b.get(link.Slug, pac88Q("duration", "30"))
		if a.code != http.StatusOK || len(a.dates) != 0 {
			t.Errorf("overview with page 2 failing = %d %v, want 200 with no day (D-6)", a.code, a.dates)
		}
	})

	t.Run("focus block on the last needed day", func(t *testing.T) {
		b, link, owner, _ := setup(t, "focus")
		if err := storage.SaveFocusBlock(b.db, owner, "pac88-c10-focus", last, paris(t, last, "09:00"), paris(t, last, "17:00")); err != nil {
			t.Fatal(err)
		}
		a := b.get(link.Slug, pac88Q("duration", "30"))
		if a.code != http.StatusOK || has(a.dates, last) || len(a.dates) != 41 {
			t.Errorf("overview = %d %v, want every day but %s", a.code, a.dates, last)
		}
	})

	t.Run("booking on the last needed day", func(t *testing.T) {
		b, link, _, _ := setup(t, "booking")
		b.booking(link, paris(t, last, "09:00"), paris(t, last, "17:00"), "confirmed")
		a := b.get(link.Slug, pac88Q("duration", "30"))
		if a.code != http.StatusOK || has(a.dates, last) || len(a.dates) != 41 {
			t.Errorf("overview = %d %v, want every day but %s", a.code, a.dates, last)
		}
	})
}

// --- helpers (pure) ------------------------------------------------------------

func TestPAC88_ZoneDayStartAndNeededLinkDays(t *testing.T) {
	for _, tc := range []struct {
		day, zone, want string
	}{
		{"2027-03-14", "America/New_York", "2027-03-14T05:00:00Z"},
		{"2027-03-15", "America/New_York", "2027-03-15T04:00:00Z"},
		{"2027-03-01", "Pacific/Kiritimati", "2027-02-28T10:00:00Z"},
		{"2027-03-01", "Etc/GMT+12", "2027-03-01T12:00:00Z"},
		{"2027-03-01", "Asia/Kathmandu", "2027-02-28T18:15:00Z"},
		// Santiago's midnight is skipped on its spring-forward day: the
		// day starts at 01:00 local.
		{"2027-09-05", "America/Santiago", "2027-09-05T04:00:00Z"},
		// Samoa skipped 2011-12-30 entirely: its "start" is 12-31's.
		{"2011-12-30", "Pacific/Apia", "2011-12-30T10:00:00Z"},
	} {
		got := zoneDayStart(pac88Date(tc.day), mustZone(t, tc.zone)).Format(time.RFC3339)
		if got != tc.want {
			t.Errorf("zoneDayStart(%s, %s) = %s, want %s", tc.day, tc.zone, got, tc.want)
		}
	}
	paris, ny := mustZone(t, "Europe/Paris"), mustZone(t, "America/New_York")
	days := neededLinkDays(pac88Date("2027-03-01"), pac88Date("2027-04-11"), mustZone(t, "Pacific/Kiritimati"), mustZone(t, "Etc/GMT+12"))
	// Kiritimati 03-01 starts 02-27 22:00 at -12; 04-12 starts 04-10 22:00.
	if len(days) != 43 || days[0].Format(time.DateOnly) != "2027-02-27" || days[42].Format(time.DateOnly) != "2027-04-10" {
		t.Errorf("42 days, +14 over -12: %d link days from %s, want 43 from 2027-02-27 (at most n + 2)", len(days), days[0].Format(time.DateOnly))
	}
	if d := neededLinkDays(pac88Date("2027-03-02"), pac88Date("2027-03-02"), paris, paris); len(d) != 1 || d[0].Format(time.DateOnly) != "2027-03-02" || d[0].Location() != paris {
		t.Errorf("same zone: %v, want the one link day", d)
	}
	if d := neededLinkDays(pac88Date("2027-03-02"), pac88Date("2027-03-02"), paris, ny); len(d) != 2 {
		t.Errorf("Paris day in New York link days: %v, want 2", d)
	}
}
