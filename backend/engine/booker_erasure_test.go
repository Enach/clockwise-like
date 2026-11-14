package engine

// PAC-40 part B: the booker-erasure job against a real database
// (contracts/features/PAC-40.md §5; spec §3.5, AC-7, AC-8, AC-26..AC-36).
// Every run takes an injected clock, an injected logger and an injected
// error reporter, so each test asserts on the exact lines and reports.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/secretbox"
	"github.com/Enach/paceday/backend/internal/secretbox/secretboxtest"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lib/pq"
)

const day = 24 * time.Hour

// erasureEnv is one isolated database, one owner and a recorder for every
// line and report the job produces.
type erasureEnv struct {
	t       *testing.T
	db      *sql.DB
	owner   uuid.UUID
	mu      sync.Mutex
	lines   []string
	reports []string
}

func newErasureEnv(t *testing.T) *erasureEnv {
	t.Helper()
	db := openTestDB(t)
	u, err := storage.UpsertUser(db, "pac40b-owner@example.com", "PAC-40B Owner", "", "google", "pac40b-owner")
	if err != nil {
		t.Fatal(err)
	}
	return &erasureEnv{t: t, db: db, owner: u.ID}
}

// job is the erasure job at the fixed instant now.
func (e *erasureEnv) job(now time.Time) *BookerErasure {
	return &BookerErasure{
		DB:  e.db,
		Now: func() time.Time { return now },
		Logf: func(f string, a ...any) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.lines = append(e.lines, fmt.Sprintf(f, a...))
		},
		Report: func(err error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.reports = append(e.reports, err.Error())
		},
	}
}

func (e *erasureEnv) run(now time.Time) BookerErasureSummary {
	return e.job(now).Run(context.Background())
}

// summaries returns the run lines written so far.
func (e *erasureEnv) summaries() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, l := range e.lines {
		if strings.HasPrefix(l, "booker-erasure: run ") {
			out = append(out, l)
		}
	}
	return out
}

// link creates an owned link (owner as accepted host) with a booking per
// end instant; status "cancelled" for the indexes in cancelled.
func (e *erasureEnv) link(slug string, ends []time.Time, cancelled ...int) (uuid.UUID, []uuid.UUID) {
	e.t.Helper()
	l, err := storage.CreateSchedulingLink(e.db, &storage.SchedulingLink{
		OwnerUserID: e.owner, Slug: slug, Title: "Title " + slug, DurationOptions: []int{30},
		DaysOfWeek: []int{1, 2, 3, 4, 5}, WindowStart: "09:00", WindowEnd: "17:00", UsageType: "reusable", Active: true,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := storage.AddLinkHost(e.db, l.ID, e.owner, "accepted"); err != nil {
		e.t.Fatal(err)
	}
	ids := make([]uuid.UUID, 0, len(ends))
	for i, end := range ends {
		ids = append(ids, e.booking(l.ID, fmt.Sprintf("%s-b%d", slug, i), end))
	}
	for _, i := range cancelled {
		if _, err := e.db.Exec(`UPDATE bookings SET status = 'cancelled' WHERE id = $1`, ids[i]); err != nil {
			e.t.Fatal(err)
		}
	}
	return l.ID, ids
}

// booking inserts one booking ending at end, booked by "Booker <tag>" with
// the address "<tag>@booker.test".
func (e *erasureEnv) booking(linkID uuid.UUID, tag string, end time.Time) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	if err := e.db.QueryRow(`
		INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time, notes)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		linkID, "Booker "+tag, tag+"@booker.test", end.Add(-30*time.Minute), end, "notes "+tag).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *erasureEnv) del(linkID uuid.UUID) {
	e.t.Helper()
	if ok, err := storage.DeleteSchedulingLink(e.db, linkID); err != nil || !ok {
		e.t.Fatalf("delete = %v, %v", ok, err)
	}
}

// row is everything stored about one booking, as compared before and after.
type bookingRow struct {
	Name, Email, Status, Notes string
	Start, End, Created        time.Time
	ErasedAt                   sql.NullTime
}

func (e *erasureEnv) row(id uuid.UUID) bookingRow {
	e.t.Helper()
	var r bookingRow
	if err := e.db.QueryRow(`
		SELECT booker_name, booker_email, status, notes, start_time, end_time, created_at, booker_erased_at
		FROM bookings WHERE id = $1`, id).Scan(&r.Name, &r.Email, &r.Status, &r.Notes, &r.Start, &r.End, &r.Created, &r.ErasedAt); err != nil {
		e.t.Fatal(err)
	}
	return r
}

// audits returns the details of the owner's erasure entries, oldest first.
func (e *erasureEnv) audits() []storage.BookersErasedDetails {
	e.t.Helper()
	rows, err := e.db.Query(`SELECT details FROM audit_log WHERE user_id = $1 AND action = $2 ORDER BY id`,
		e.owner, storage.AuditBookersErased)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []storage.BookersErasedDetails
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			e.t.Fatal(err)
		}
		var d storage.BookersErasedDetails
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&d); err != nil {
			e.t.Fatalf("audit details %q: %v", raw, err)
		}
		out = append(out, d)
	}
	return out
}

// noPII fails when a booker's name or address appears in any line, report
// or erasure audit entry the job produced.
func (e *erasureEnv) noPII(tags ...string) {
	e.t.Helper()
	var raw []string
	rows, err := e.db.Query(`SELECT details FROM audit_log WHERE action = $1`, storage.AuditBookersErased)
	if err != nil {
		e.t.Fatal(err)
	}
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		raw = append(raw, s)
	}
	rows.Close()
	e.mu.Lock()
	all := append(append(append([]string{}, e.lines...), e.reports...), raw...)
	e.mu.Unlock()
	for _, s := range all {
		for _, tag := range tags {
			if strings.Contains(s, "Booker "+tag) || strings.Contains(s, tag+"@booker.test") || strings.Contains(s, "@booker.test") {
				e.t.Errorf("personal data of %s in %q", tag, s)
			}
		}
	}
}

func wantSummary(t *testing.T, line string, at time.Time, examined, erasedLinks, bookings, failures int) {
	t.Helper()
	want := fmt.Sprintf("booker-erasure: run at=%s links_examined=%d links_erased=%d bookings_erased=%d failures=%d",
		at.UTC().Format(time.RFC3339), examined, erasedLinks, bookings, failures)
	if line != want {
		t.Errorf("summary = %q\nwant      %q", line, want)
	}
}

// The scenario of AC-26/AC-27: T1 < T2, the T2 booking cancelled.
var (
	t1 = time.Date(2025, 3, 3, 10, 30, 0, 0, time.UTC)
	t2 = time.Date(2025, 3, 10, 15, 0, 0, 0, time.UTC)
)

// AC-26: one second before T2 + 90 x 24 h nothing changes, no entry, one
// summary line with zeros; T1 + 90 days (the earlier booking) never makes
// the link due.
func TestPAC40B_NotDueBeforeTheLatestEndPlus90Days(t *testing.T) {
	e := newErasureEnv(t)
	link, ids := e.link("ac26", []time.Time{t1, t2}, 1)
	e.del(link)
	before := []bookingRow{e.row(ids[0]), e.row(ids[1])}

	for i, at := range []time.Time{t1.Add(90 * day), t2.Add(90*day - time.Second)} {
		s := e.run(at)
		if s != (BookerErasureSummary{}) {
			t.Errorf("run at %s = %+v; want nothing", at, s)
		}
		if got := e.summaries(); len(got) != i+1 {
			t.Fatalf("summary lines = %v; want exactly one per run", got)
		} else {
			wantSummary(t, got[i], at, 0, 0, 0, 0)
		}
	}
	for i, id := range ids {
		if got := e.row(id); got != before[i] {
			t.Errorf("booking %d changed: %+v -> %+v", i, before[i], got)
		}
	}
	if a := e.audits(); len(a) != 0 {
		t.Errorf("audit entries %v; want none", a)
	}
	if len(e.lines) != 2 || len(e.reports) != 0 {
		t.Errorf("lines %v reports %v; want only the two summaries", e.lines, e.reports)
	}
}

// AC-27 (+ AC-8 and AC-7 after erasure): at T2 + 90 x 24 h both bookings are
// erased; everything else about them, the link, its hosts, its slug, its
// uses and its busy time is unchanged; no name or email anywhere.
func TestPAC40B_ErasesBothAtTheDueInstantAndKeepsTheRest(t *testing.T) {
	e := newErasureEnv(t)
	cohost, err := storage.UpsertUser(e.db, "pac40b-cohost@example.com", "Co", "", "google", "pac40b-co")
	if err != nil {
		t.Fatal(err)
	}
	link, ids := e.link("ac27", []time.Time{t1, t2}, 1)
	if _, err := storage.AddLinkHost(e.db, link, cohost.ID, "accepted"); err != nil {
		t.Fatal(err)
	}
	if err := storage.SaveBookingEvent(e.db, ids[0], e.owner, "evt-ac27"); err != nil {
		t.Fatal(err)
	}
	e.del(link)
	before := []bookingRow{e.row(ids[0]), e.row(ids[1])}
	type linkState struct {
		Deleted          time.Time
		Active           bool
		Title, Slug      string
		Hosts, Uses, Evs int
	}
	state := func() linkState {
		var s linkState
		if err := e.db.QueryRow(`
			SELECT deleted_at, active, title, slug,
			       (SELECT count(*) FROM scheduling_link_hosts WHERE link_id = l.id),
			       (SELECT count(*) FROM bookings WHERE link_id = l.id),
			       (SELECT count(*) FROM booking_events WHERE booking_id = ANY($2))
			FROM scheduling_links l WHERE id = $1`, link, []uuid.UUID{ids[0], ids[1]}).
			Scan(&s.Deleted, &s.Active, &s.Title, &s.Slug, &s.Hosts, &s.Uses, &s.Evs); err != nil {
			t.Fatal(err)
		}
		return s
	}
	linkBefore := state()
	busyBefore, err := storage.GetBusyBookingsForUser(e.db, e.owner, t1.Add(-day), t1.Add(day))
	if err != nil || len(busyBefore) != 2 {
		t.Fatalf("AC-7: busy before = %v, %v; want the confirmed booking", busyBefore, err)
	}

	at := t2.Add(90 * day)
	if s := e.run(at); s != (BookerErasureSummary{LinksExamined: 1, LinksErased: 1, BookingsErased: 2}) {
		t.Fatalf("run = %+v", s)
	}
	for i, id := range ids {
		got, want := e.row(id), before[i]
		if got.Name != "" || got.Email != "" || !got.ErasedAt.Valid || !got.ErasedAt.Time.Equal(at) {
			t.Errorf("booking %d = %+v; want erased at %s", i, got, at)
		}
		got.Name, got.Email, got.ErasedAt = want.Name, want.Email, want.ErasedAt
		if got != want {
			t.Errorf("booking %d: start/end/status/notes/created_at changed: %+v -> %+v", i, want, got)
		}
	}
	if after := state(); after != linkBefore {
		t.Errorf("link changed: %+v -> %+v", linkBefore, after)
	}
	// AC-8 (after-erasure half): the slug stays reserved.
	if exists, err := storage.SlugExists(e.db, "ac27"); err != nil || !exists {
		t.Errorf("slug after erasure: exists=%v err=%v", exists, err)
	}
	// AC-7 (second half): still busy for its period.
	if busy, err := storage.GetBusyBookingsForUser(e.db, e.owner, t1.Add(-day), t1.Add(day)); err != nil || !reflect.DeepEqual(busy, busyBefore) {
		t.Errorf("AC-7: busy after erasure = %v, %v; want %v", busy, err, busyBefore)
	}
	wantSummary(t, e.summaries()[0], at, 1, 1, 2, 0)
	e.noPII("ac27-b0", "ac27-b1")
	if len(e.reports) != 0 {
		t.Errorf("reports %v", e.reports)
	}
}

// AC-28: re-runs change nothing and write no entry; two concurrent runs
// erase each booking once and write exactly one entry.
func TestPAC40B_IdempotentAndConcurrentRunsWriteOneEntry(t *testing.T) {
	e := newErasureEnv(t)
	link, ids := e.link("ac28", []time.Time{t1, t2})
	e.del(link)
	at := t2.Add(90 * day)
	e.run(at)
	erased := e.row(ids[0])
	for _, later := range []time.Time{at, t2.Add(400 * day)} {
		if s := e.run(later); s != (BookerErasureSummary{}) {
			t.Errorf("re-run at %s = %+v; want nothing", later, s)
		}
	}
	if got := e.row(ids[0]); got != erased {
		t.Errorf("re-run changed an erased booking: %+v -> %+v", erased, got)
	}
	if a := e.audits(); len(a) != 1 {
		t.Fatalf("entries after re-runs = %d; want 1", len(a))
	}

	link2, _ := e.link("ac28-race", []time.Time{t1, t2})
	e.del(link2)
	var wg sync.WaitGroup
	sums := make([]BookerErasureSummary, 4)
	for i := range sums {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sums[i] = e.run(at)
		}(i)
	}
	wg.Wait()
	total := 0
	for _, s := range sums {
		total += s.BookingsErased
		if s.Failures != 0 {
			t.Errorf("concurrent run failed: %+v (lines %v)", s, e.lines)
		}
	}
	if total != 2 {
		t.Errorf("concurrent runs erased %d bookings in total; want 2 (each once)", total)
	}
	if a := e.audits(); len(a) != 2 || a[1].LinkID != link2 {
		t.Errorf("entries = %+v; want exactly one more, for the raced link", a)
	}
	var awaiting int
	_ = e.db.QueryRow(`SELECT count(*) FROM bookings WHERE link_id = $1 AND booker_erased_at IS NULL`, link2).Scan(&awaiting)
	if awaiting != 0 {
		t.Errorf("%d bookings left after concurrent runs", awaiting)
	}
}

// AC-29: live, paused and pre-037 inactive links are never examined.
func TestPAC40B_LiveAndPausedLinksAreNeverErased(t *testing.T) {
	e := newErasureEnv(t)
	old := time.Now().UTC().Add(-400 * day)
	_, live := e.link("ac29-live", []time.Time{old})
	paused, pb := e.link("ac29-paused", []time.Time{old})
	if _, err := e.db.Exec(`UPDATE scheduling_links SET active = false WHERE id = $1`, paused); err != nil {
		t.Fatal(err)
	}
	// "Pre-037 inactive": active false, deleted_at null, as every link
	// inactive before the migration is.
	legacy, lb := e.link("ac29-legacy", []time.Time{old.Add(-day)})
	if _, err := e.db.Exec(`UPDATE scheduling_links SET active = false, deleted_at = NULL WHERE id = $1`, legacy); err != nil {
		t.Fatal(err)
	}
	s := e.run(time.Now().UTC())
	if s != (BookerErasureSummary{}) {
		t.Errorf("run = %+v; want nothing examined", s)
	}
	for _, id := range []uuid.UUID{live[0], pb[0], lb[0]} {
		if r := e.row(id); r.Name == "" || r.ErasedAt.Valid {
			t.Errorf("booking %s erased on a link that is not deleted: %+v", id, r)
		}
	}
	if a := e.audits(); len(a) != 0 {
		t.Errorf("entries %v", a)
	}
}

// AC-30: a link deleted after its only booking was already 90 days past is
// erased by the first run after the delete.
func TestPAC40B_DeletedLongAfterTheLastBookingIsErasedAtOnce(t *testing.T) {
	e := newErasureEnv(t)
	end := time.Now().UTC().Add(-100 * day).Truncate(time.Second)
	link, ids := e.link("ac30", []time.Time{end})
	e.del(link)
	var deletedAt time.Time
	if err := e.db.QueryRow(`SELECT deleted_at FROM scheduling_links WHERE id = $1`, link).Scan(&deletedAt); err != nil {
		t.Fatal(err)
	}
	if !end.Add(90 * day).Before(deletedAt) {
		t.Fatalf("fixture: E+90d %s is not before D %s", end.Add(90*day), deletedAt)
	}
	if s := e.run(deletedAt.Add(time.Second)); s.LinksErased != 1 || s.BookingsErased != 1 {
		t.Fatalf("run at D+1s = %+v", s)
	}
	if r := e.row(ids[0]); r.Name != "" || !r.ErasedAt.Valid {
		t.Errorf("booking = %+v; want erased", r)
	}
}

// AC-31: one entry for three bookings, naming the title, id, count, last
// booking date and run time, and nothing else.
func TestPAC40B_OneAuditEntryWithoutPersonalData(t *testing.T) {
	e := newErasureEnv(t)
	link, _ := e.link("ac31", []time.Time{t1, t2, t2.Add(-time.Hour)})
	e.del(link)
	at := t2.Add(90*day + 5*time.Minute)
	e.run(at)
	got := e.audits()
	want := storage.BookersErasedDetails{
		LinkID: link, LinkTitle: "Title ac31", BookingsErased: 3,
		LastBookingEndAt: t2.Format(time.RFC3339), RanAt: at.Format(time.RFC3339),
	}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("entries = %+v; want exactly %+v", got, want)
	}
	var raw string
	if err := e.db.QueryRow(`SELECT details FROM audit_log WHERE action = $1`, storage.AuditBookersErased).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var members map[string]any
	if err := json.Unmarshal([]byte(raw), &members); err != nil || len(members) != 5 {
		t.Errorf("details %s: %d members, %v; want exactly the five", raw, len(members), err)
	}
	e.noPII("ac31-b0", "ac31-b1", "ac31-b2")
}

// piiFault is the AC-33 fault: a *pgconn.PgError whose Message and Detail
// both hold the booker's name and email (the shapes K4-4 names).
func piiFault(tag string) error {
	return &pgconn.PgError{
		Severity: "ERROR", Code: "23514",
		Message: fmt.Sprintf(`new row for relation "bookings" violates check constraint, value "Booker %s"`, tag),
		Detail:  fmt.Sprintf("Failing row contains (Booker %s, %s@booker.test).", tag, tag),
	}
}

func setErasureFault(t *testing.T, f func(uuid.UUID) error) {
	t.Helper()
	if f == nil { // no fault: a seam that never fails
		f = func(uuid.UUID) error { return nil }
	}
	storage.InstallAfterBookerErasureUpdate(t, f)
}

// AC-33: a failure on link A rolls A back entirely (no change, no entry),
// is logged and reported by class without personal data, and does not stop
// B; the next run erases A.
func TestPAC40B_AFailedLinkRollsBackAloneAndIsRetried(t *testing.T) {
	e := newErasureEnv(t)
	a, aIDs := e.link("ac33-a", []time.Time{t1})
	b, bIDs := e.link("ac33-b", []time.Time{t1})
	e.del(a)
	e.del(b)
	before := e.row(aIDs[0])
	setErasureFault(t, func(id uuid.UUID) error {
		if id == a {
			return fmt.Errorf("wrapped: %w", piiFault("ac33-a-b0"))
		}
		return nil
	})
	at := t1.Add(90 * day)
	s := e.run(at)
	if s != (BookerErasureSummary{LinksExamined: 2, LinksErased: 1, BookingsErased: 1, Failures: 1}) {
		t.Fatalf("run = %+v", s)
	}
	if got := e.row(aIDs[0]); got != before {
		t.Errorf("A changed although its erasure failed: %+v -> %+v", before, got)
	}
	if got := e.row(bIDs[0]); got.Name != "" || !got.ErasedAt.Valid {
		t.Errorf("B not erased: %+v", got)
	}
	if got := e.audits(); len(got) != 1 || got[0].LinkID != b {
		t.Errorf("entries = %+v; want B's only", got)
	}
	wantLine := fmt.Sprintf("booker-erasure: link %s failed: sqlstate 23514", a)
	if e.lines[0] != wantLine {
		t.Errorf("failure line = %q; want %q", e.lines[0], wantLine)
	}
	wantSummary(t, e.summaries()[0], at, 2, 1, 1, 1)
	if len(e.reports) != 1 || e.reports[0] != wantLine {
		t.Errorf("reports = %v; want the sanitised line only", e.reports)
	}
	e.noPII("ac33-a-b0", "ac33-b-b0")

	setErasureFault(t, nil)
	if s := e.run(at.Add(day)); s.LinksErased != 1 || s.Failures != 0 {
		t.Fatalf("next run = %+v; want A erased", s)
	}
	if got := e.row(aIDs[0]); got.Name != "" || !got.ErasedAt.Valid {
		t.Errorf("A not erased by the next run: %+v", got)
	}
	if got := e.audits(); len(got) != 2 || got[1].LinkID != a {
		t.Errorf("entries = %+v", got)
	}
}

// AC-33 (drain half): a ctx cancelled while one link is being erased stops
// the run before the next link; the link in progress completes as a unit.
func TestPAC40B_CancelStopsBetweenLinksEachAllOrNothing(t *testing.T) {
	e := newErasureEnv(t)
	first, fIDs := e.link("ac33-c1", []time.Time{t1, t1.Add(-time.Hour)})
	second, sIDs := e.link("ac33-c2", []time.Time{t1})
	e.del(first)
	e.del(second)
	if first.String() > second.String() { // the scan is in id order
		first = second
		fIDs, sIDs = sIDs, fIDs
	}
	ctx, cancel := context.WithCancel(context.Background())
	setErasureFault(t, func(id uuid.UUID) error {
		cancel() // the drain cancels in the middle of the first link
		return nil
	})
	at := t1.Add(90 * day)
	s := e.job(at).Run(ctx)
	if s.LinksErased != 1 || s.LinksExamined != 1 {
		t.Fatalf("run = %+v; want the first link only", s)
	}
	for _, id := range fIDs {
		if r := e.row(id); r.Name != "" || !r.ErasedAt.Valid {
			t.Errorf("first link half-erased: %+v", r)
		}
	}
	for _, id := range sIDs {
		if r := e.row(id); r.Name == "" || r.ErasedAt.Valid {
			t.Errorf("second link touched after the cancel: %+v", r)
		}
	}
	if got := e.audits(); len(got) != 1 || got[0].LinkID != first {
		t.Errorf("entries = %+v", got)
	}
	if len(e.summaries()) != 1 {
		t.Errorf("summary lines %v; want one", e.summaries())
	}
}

// AC-34: a booking landing after an erasure moves the due instant to its
// own end + 90 days; it is then erased with a second entry, and the earlier
// bookings keep their first erasure.
func TestPAC40B_ALateBookingIsErasedOnItsOwnSchedule(t *testing.T) {
	e := newErasureEnv(t)
	link, ids := e.link("ac34", []time.Time{t1, t2})
	e.del(link)
	r1 := t2.Add(90 * day)
	e.run(r1)
	first := e.row(ids[0])
	t3 := r1.Add(10 * day)
	late := e.booking(link, "ac34-late", t3)
	if s := e.run(t3.Add(90*day - time.Second)); s != (BookerErasureSummary{}) {
		t.Errorf("run before T3+90d = %+v", s)
	}
	if r := e.row(late); r.Name == "" || r.ErasedAt.Valid {
		t.Errorf("late booking erased early: %+v", r)
	}
	at := t3.Add(90 * day)
	if s := e.run(at); s.BookingsErased != 1 {
		t.Fatalf("run at T3+90d = %+v", s)
	}
	if r := e.row(late); r.Name != "" || !r.ErasedAt.Time.Equal(at) {
		t.Errorf("late booking = %+v", r)
	}
	if got := e.row(ids[0]); got != first {
		t.Errorf("earlier booking re-erased: %+v -> %+v", first, got)
	}
	got := e.audits()
	if len(got) != 2 || got[1].BookingsErased != 1 || got[1].LastBookingEndAt != t3.Format(time.RFC3339) {
		t.Errorf("entries = %+v; want a second entry for the late booking", got)
	}
}

// AC-36: the same result with encryption configured, not configured, and
// configured with an unrelated secret that cannot be decrypted.
func TestPAC40B_ErasureNeedsNoKey(t *testing.T) {
	// A well-formed envelope under a data key no keyring holds (as PAC-84's tests).
	undecryptable := "pdenc:1:2147483000:" + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 48))
	for _, mode := range []string{"configured", "not configured", "undecryptable secret"} {
		t.Run(mode, func(t *testing.T) {
			e := newErasureEnv(t)
			prev := secretbox.Current()
			t.Cleanup(func() { secretbox.Install(prev) })
			switch mode {
			case "configured":
				secretboxtest.Install(t)
			case "not configured":
				secretbox.Install(nil)
			default:
				secretboxtest.Install(t)
				if _, err := e.db.Exec(`
					INSERT INTO oauth_tokens (user_id, calendar_id, access_token, refresh_token, expiry)
					VALUES ($1, 'primary', '', $2, now())`, e.owner, undecryptable); err != nil {
					t.Fatalf("seed undecryptable: %v", err)
				}
			}
			link, ids := e.link("ac36", []time.Time{t1, t2}, 1)
			e.del(link)
			if s := e.run(t2.Add(90*day - time.Second)); s != (BookerErasureSummary{}) {
				t.Errorf("AC-26 under %s: %+v", mode, s)
			}
			if s := e.run(t2.Add(90 * day)); s != (BookerErasureSummary{LinksExamined: 1, LinksErased: 1, BookingsErased: 2}) {
				t.Errorf("AC-27 under %s: %+v (lines %v)", mode, s, e.lines)
			}
			for _, id := range ids {
				if r := e.row(id); r.Name != "" || r.Email != "" || !r.ErasedAt.Valid {
					t.Errorf("under %s: %+v", mode, r)
				}
			}
		})
	}
}

// K4-3: while a link's erasure transaction is open, a booking insert on that
// link waits (FOR UPDATE conflicts with the foreign key's KEY SHARE) and is
// never erased by it.
func TestPAC40B_BookingInsertWaitsForTheErasureLock(t *testing.T) {
	e := newErasureEnv(t)
	link, _ := e.link("k43", []time.Time{t1})
	e.del(link)
	inside, release := make(chan struct{}), make(chan struct{})
	setErasureFault(t, func(uuid.UUID) error {
		close(inside)
		<-release
		return nil
	})
	at := t1.Add(90 * day)
	done := make(chan BookerErasureSummary, 1)
	go func() { done <- e.run(at) }()
	<-inside
	inserted := make(chan uuid.UUID, 1)
	go func() { inserted <- e.booking(link, "k43-new", at.Add(day)) }()
	waitForLockWaiter(t, e.db)
	select {
	case <-inserted:
		t.Fatal("the booking insert did not wait for the erasure's FOR UPDATE")
	default:
	}
	close(release)
	if s := <-done; s.BookingsErased != 1 {
		t.Fatalf("run = %+v", s)
	}
	id := <-inserted
	if r := e.row(id); r.Name == "" || r.ErasedAt.Valid {
		t.Errorf("the booking inserted after the lock was erased: %+v", r)
	}
}

// PAC-101 §4.2: the link lock wait is bounded; a held lock fails the link by
// class 55P03, nothing changes, and the run still writes its summary.
func TestPAC40B_LinkLockWaitIsBounded(t *testing.T) {
	prev := storage.LinkLockTimeout
	storage.LinkLockTimeout = 300 * time.Millisecond
	t.Cleanup(func() { storage.LinkLockTimeout = prev })
	e := newErasureEnv(t)
	link, ids := e.link("bound", []time.Time{t1})
	e.del(link)
	holder, err := e.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback() }()
	if _, err := holder.Exec(`SELECT 1 FROM scheduling_links WHERE id = $1 FOR UPDATE`, link); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	s := e.run(t1.Add(90 * day))
	if s.Failures != 1 || s.LinksErased != 0 || time.Since(start) > 5*time.Second {
		t.Fatalf("run = %+v after %s; want one bounded failure", s, time.Since(start))
	}
	if want := fmt.Sprintf("booker-erasure: link %s failed: sqlstate 55P03", link); e.lines[0] != want {
		t.Errorf("line %q; want %q", e.lines[0], want)
	}
	if r := e.row(ids[0]); r.Name == "" {
		t.Errorf("erased although the lock was not taken: %+v", r)
	}
}

func waitForLockWaiter(t *testing.T, db *sql.DB) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no backend waited on a lock")
}

// A scan failure is one failure and still one summary line.
func TestPAC40B_ScanFailureStillWritesTheSummary(t *testing.T) {
	e := newErasureEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	at := t2
	s := e.job(at).Run(ctx)
	if s.Failures != 1 {
		t.Fatalf("run = %+v", s)
	}
	if e.lines[0] != "booker-erasure: scan failed: context canceled" || len(e.summaries()) != 1 {
		t.Errorf("lines %v", e.lines)
	}
	wantSummary(t, e.summaries()[0], at, 0, 0, 0, 1)
}

// K4-4: the class is the SQLSTATE or a fixed word, never Error() text.
func TestPAC40B_ErrorClassCarriesNoRowData(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{piiFault("x"), "sqlstate 23514"},
		{fmt.Errorf("tx: %w", piiFault("x")), "sqlstate 23514"},
		{&pq.Error{Code: "22P02", Message: `invalid input syntax "Booker x"`, Detail: "x@booker.test"}, "sqlstate 22P02"},
		{&pgconn.PgError{Code: "Booker x@booker.test"}, "sqlstate invalid"},
		{&pgconn.PgError{Code: "2350x"}, "sqlstate invalid"},
		{context.Canceled, "context canceled"},
		{fmt.Errorf("q: %w", context.DeadlineExceeded), "context deadline exceeded"},
		{sql.ErrConnDone, "connection closed"},
		{sql.ErrTxDone, "transaction done"},
		{storage.ErrAuditNoSubject, "audit entry has no subject"},
		{errors.New("Booker x x@booker.test"), "unclassified"},
	}
	for _, c := range cases {
		if got := ErasureErrorClass(c.err); got != c.want {
			t.Errorf("class(%T) = %q; want %q", c.err, got, c.want)
		}
	}
}

// Defaults: a job with no clock, logger or reporter still runs and logs
// through the standard logger.
func TestPAC40B_Defaults(t *testing.T) {
	e := newErasureEnv(t)
	j := &BookerErasure{DB: e.db}
	if s := j.Run(context.Background()); s != (BookerErasureSummary{}) {
		t.Errorf("run = %+v", s)
	}
	if d := j.now(); time.Since(d) > time.Minute {
		t.Errorf("default clock %v", d)
	}
	j.report(errors.New("ignored")) // no reporter: nothing happens
}
