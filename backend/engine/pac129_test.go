package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/calendar"
	"github.com/Enach/paceday/backend/internal/budget"
	"github.com/Enach/paceday/backend/internal/peritem"
	"github.com/Enach/paceday/backend/storage"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// PAC-129 (contract contracts/features/PAC-129.md §6): the team path's
// batched free/busy entry point.

// batchReader is a provider double for one owner: it refuses more than max
// addresses per request (as the provider's documented limit), answers from
// table, records each call's ids and access token, the peak concurrency, and
// can delay or fail chosen calls.
type batchReader struct {
	max   int
	table map[string][]calendar.TimeSlot
	delay time.Duration
	// fail reports whether the call with these ids fails as a whole.
	fail func(ids []string) bool

	mu     sync.Mutex
	calls  [][]string
	tokens []string
	live   atomic.Int32
	peak   atomic.Int32
}

func (b *batchReader) answer(ctx context.Context, ts oauth2.TokenSource, ids []string, start, end time.Time) (map[string]calendar.FreeBusyEntry, error) {
	tok, _ := ts.Token()
	b.mu.Lock()
	b.calls = append(b.calls, append([]string(nil), ids...))
	if tok != nil {
		b.tokens = append(b.tokens, tok.AccessToken)
	}
	b.mu.Unlock()
	n := b.live.Add(1)
	defer b.live.Add(-1)
	for {
		p := b.peak.Load()
		if n <= p || b.peak.CompareAndSwap(p, n) {
			break
		}
	}
	if b.delay > 0 {
		select {
		case <-time.After(b.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if len(ids) > b.max {
		return nil, fmt.Errorf("tooManyCalendarsRequested: %d > %d", len(ids), b.max)
	}
	if b.fail != nil && b.fail(ids) {
		return nil, errors.New("backend error")
	}
	return calendarsAnswer(b.table)(ctx, ids, start, end)
}

func (b *batchReader) nCalls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.calls)
}

type googleFunc func(ctx context.Context, ids []string, start, end time.Time) (map[string]calendar.FreeBusyEntry, error)

func (f googleFunc) GetFreeBusy(ctx context.Context, ids []string, start, end time.Time) (map[string]calendar.FreeBusyEntry, error) {
	return f(ctx, ids, start, end)
}

// teamService is a FreeBusyService whose Google and Outlook readers are b.
func teamService(db *sql.DB, b *batchReader) *FreeBusyService {
	s := NewFreeBusyService(db, &oauth2.Config{})
	s.Microsoft = &auth.MicrosoftConfig{ClientID: "m", ClientSecret: "s"}
	s.Clock = &stepClock{t: time.Date(2030, 1, 1, 8, 0, 0, 0, time.UTC)}
	s.newOutlook = func(ts oauth2.TokenSource) outlookFreeBusy {
		return outlookFunc(func(ctx context.Context, ids []string, start, end time.Time) (map[string]calendar.FreeBusyEntry, error) {
			return b.answer(ctx, ts, ids, start, end)
		})
	}
	s.team.newGoogle = func(_ context.Context, ts oauth2.TokenSource) (googleFreeBusy, error) {
		return googleFunc(func(ctx context.Context, ids []string, start, end time.Time) (map[string]calendar.FreeBusyEntry, error) {
			return b.answer(ctx, ts, ids, start, end)
		}), nil
	}
	return s
}

func googleOwner(t *testing.T, db *sql.DB, access string) uuid.UUID {
	t.Helper()
	u := pac67User(t, db, "owner-"+uuid.NewString()[:8]+"@corp.example")
	if err := auth.UpsertUserToken(db, u, &oauth2.Token{AccessToken: access, RefreshToken: "r", Expiry: time.Now().Add(24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	return u
}

func outlookOwner(t *testing.T, db *sql.DB, access string) uuid.UUID {
	t.Helper()
	u := pac67User(t, db, "owner-"+uuid.NewString()[:8]+"@corp.example")
	storeOutlookCredential(t, db, u, access, "r", time.Now().Add(24*time.Hour))
	return u
}

func addresses(n int) ([]string, map[string][]calendar.TimeSlot) {
	emails := make([]string, n)
	table := map[string][]calendar.TimeSlot{}
	for i := range emails {
		emails[i] = fmt.Sprintf("p%02d@corp.example", i)
		table[emails[i]] = nil
	}
	return emails, table
}

func coverageOf(res []ParticipantBusy) map[string]string {
	out := map[string]string{}
	for _, r := range res {
		out[r.Email] = r.Coverage
	}
	return out
}

// AC-14: 49 added people on Google and on Outlook: every request within the
// provider's limit, everyone known; one failed Microsoft batch makes only its
// addresses unknown.
func TestPAC129_AC14_BatchesStayWithinProviderLimits(t *testing.T) {
	db := openTestDB(t)
	emails, table := addresses(49)
	day := monday.Add(24 * time.Hour)

	g := &batchReader{max: 50, table: table}
	gs := teamService(db, g)
	res, err := gs.QueryForTeam(context.Background(), googleOwner(t, db, "g-owner"), emails, day, day.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for e, c := range coverageOf(res) {
		if c != "known" {
			t.Errorf("Google: %s is %s", e, c)
		}
	}
	if len(g.calls) != 1 || len(g.calls[0]) != 49 {
		t.Errorf("Google calls = %d (first %d), want one of 49", len(g.calls), len(g.calls[0]))
	}

	m := &batchReader{max: 20, table: table}
	ms := teamService(db, m)
	res, err = ms.QueryForTeam(context.Background(), outlookOwner(t, db, "m-owner"), emails, day, day.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 49 {
		t.Fatalf("%d answers", len(res))
	}
	for e, c := range coverageOf(res) {
		if c != "known" {
			t.Errorf("Outlook: %s is %s", e, c)
		}
	}
	sizes := map[int]int{}
	for _, c := range m.calls {
		sizes[len(c)]++
	}
	if len(m.calls) != 3 || sizes[20] != 2 || sizes[9] != 1 {
		t.Errorf("Outlook batches = %v, want 20+20+9", sizes)
	}

	// One failed Microsoft batch: only its addresses are unknown.
	failing := map[string]bool{}
	f := &batchReader{max: 20, table: table, fail: func(ids []string) bool {
		for _, id := range ids {
			if id == "p25@corp.example" {
				return true
			}
		}
		return false
	}}
	fs := teamService(db, f)
	res, err = fs.QueryForTeam(context.Background(), outlookOwner(t, db, "m-owner-2"), emails, day, day.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range f.calls {
		for _, id := range c {
			if id == "p25@corp.example" {
				for _, x := range c {
					failing[x] = true
				}
			}
		}
	}
	for e, c := range coverageOf(res) {
		if want := map[bool]string{true: "unknown", false: "known"}[failing[e]]; c != want {
			t.Errorf("one failed batch: %s is %s, want %s", e, c, want)
		}
	}
	if len(failing) != 20 {
		t.Errorf("the failed batch held %d addresses, want 20", len(failing))
	}
}

// AC-14: with each Microsoft batch delayed by its limit minus a step, the three
// batches overlap (never more than 3 at once) and the answer comes within one
// batch limit plus a margin, not three.
func TestPAC129_AC14_BatchesRunConcurrentlyWithinOneLimit(t *testing.T) {
	db := openTestDB(t)
	emails, table := addresses(49)
	limit := 900 * time.Millisecond
	m := &batchReader{max: 20, table: table, delay: limit - 300*time.Millisecond}
	s := teamService(db, m)
	s.team.limit = limit
	start := time.Now()
	res, err := s.QueryForTeam(context.Background(), outlookOwner(t, db, "m-owner"), emails, monday, monday.Add(24*time.Hour))
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	for e, c := range coverageOf(res) {
		if c != "known" {
			t.Errorf("%s is %s", e, c)
		}
	}
	if p := m.peak.Load(); p != 3 {
		t.Errorf("peak concurrency %d, want the 3 batches overlapping", p)
	}
	if took > limit+400*time.Millisecond {
		t.Errorf("took %v, want within one batch limit (%v) plus a margin", took, limit)
	}

	// A fourth batch waits for a slot: at most 3 at once.
	s.team.outlookMax = 10
	m2 := &batchReader{max: 20, table: table, delay: 100 * time.Millisecond}
	s2 := teamService(db, m2)
	s2.team.outlookMax = 10
	if _, err := s2.QueryForTeam(context.Background(), outlookOwner(t, db, "m-owner-2"), emails, monday, monday.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if p := m2.peak.Load(); p > 3 || m2.nCalls() != 5 {
		t.Errorf("5 batches: peak %d calls %d, want at most 3 at once", p, m2.nCalls())
	}
}

// Contract §6 caching: a batch the budget cuts is the 503 and writes nothing
// to the cache; one not started at the stop margin makes no call; a batch
// that fails on its own limit is cached unknown for 15 minutes.
func TestPAC129_BudgetCutBatchIsTheBudgetErrorAndIsNotCached(t *testing.T) {
	db := openTestDB(t)
	defer peritem.SetForTest(peritem.Settings{Workers: 4, Margin: 100 * time.Millisecond})()
	emails, table := addresses(3)
	owner := outlookOwner(t, db, "m-owner")

	slow := &batchReader{max: 20, table: table, delay: time.Hour}
	s := teamService(db, slow)
	s.team.limit = 2 * time.Second
	// remaining (400 ms) - margin (100 ms) < limit: the deadline is the budget's.
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	_, err := s.QueryForTeam(ctx, owner, emails, monday, monday.Add(24*time.Hour))
	cancel()
	if !errors.Is(err, budget.ErrExpired) {
		t.Fatalf("budget-cut batch: %v, want budget.ErrExpired", err)
	}
	fast := &batchReader{max: 20, table: table}
	s.newOutlook = teamService(db, fast).newOutlook
	res, err := s.QueryForTeam(context.Background(), owner, emails, monday, monday.Add(24*time.Hour))
	if err != nil || fast.nCalls() != 1 {
		t.Fatalf("retry: %v with %d calls, want the provider asked again (nothing cached)", err, fast.nCalls())
	}
	for e, c := range coverageOf(res) {
		if c != "known" {
			t.Errorf("retry: %s is %s", e, c)
		}
	}

	// Not started: the budget is already at the margin.
	none := &batchReader{max: 20, table: table}
	s3 := teamService(db, none)
	ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
	_, err = s3.QueryForTeam(ctx, owner, []string{"other@corp.example"}, monday, monday.Add(24*time.Hour))
	cancel()
	if !errors.Is(err, budget.ErrExpired) || none.nCalls() != 0 {
		t.Errorf("at the margin: %v with %d calls, want the budget error and no call", err, none.nCalls())
	}

	// Own limit: unknown, and cached (PAC-67, unchanged).
	own := &batchReader{max: 20, table: table, delay: time.Hour}
	s4 := teamService(db, own)
	s4.team.limit = 100 * time.Millisecond
	res, err = s4.QueryForTeam(context.Background(), owner, []string{"slow@corp.example"}, monday, monday.Add(24*time.Hour))
	if err != nil || res[0].Coverage != "unknown" {
		t.Fatalf("own-limit timeout: %+v %v, want unknown", res, err)
	}
	if _, err := s4.QueryForTeam(context.Background(), owner, []string{"slow@corp.example"}, monday, monday.Add(24*time.Hour)); err != nil || own.nCalls() != 1 {
		t.Errorf("own-limit result not cached: %d calls (%v)", own.nCalls(), err)
	}
}

// AC-29 (engine half) and AC-13: the read uses the owner's credential; an
// owner with no calendar makes everyone unknown without any other credential;
// personal domains are unknown without a call.
func TestPAC129_ReadsThroughTheOwnersCredentialOnly(t *testing.T) {
	db := openTestDB(t)
	_, table := addresses(1)
	b := &batchReader{max: 50, table: table}
	s := teamService(db, b)
	owner := googleOwner(t, db, "owner-access-token")
	res, err := s.QueryForTeam(context.Background(), owner, []string{"p00@corp.example", "z@gmail.com"}, monday, monday.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got := coverageOf(res); got["p00@corp.example"] != "known" || got["z@gmail.com"] != "unknown" {
		t.Errorf("coverage = %v", got)
	}
	if len(b.tokens) != 1 || b.tokens[0] != "owner-access-token" || len(b.calls[0]) != 1 {
		t.Errorf("calls %v tokens %v, want one call with the owner's token and no personal address", b.calls, b.tokens)
	}

	// AC-13: no credential for the owner: unknown, no call at all.
	none := &batchReader{max: 50, table: table}
	noCal := pac67User(t, db, "nocal-"+uuid.NewString()[:8]+"@corp.example")
	res, err = teamService(db, none).QueryForTeam(context.Background(), noCal, []string{"p00@corp.example"}, monday, monday.Add(24*time.Hour))
	if err != nil || res[0].Coverage != "unknown" || none.nCalls() != 0 {
		t.Errorf("owner disconnected: %+v %v calls %d", res, err, none.nCalls())
	}
}

// AC-12 (regression guard on PAC-133 AC-8): a one-day read, then a one-week
// read from the same Monday within 15 minutes, through one credential: the
// week read reports Tuesday's busy time.
func TestPAC129_AC12_WeekReadAfterADayReadSeesTuesday(t *testing.T) {
	db := openTestDB(t)
	tuesday := slot(monday.Add(24*time.Hour+10*time.Hour), time.Hour)
	b := &batchReader{max: 20, table: map[string][]calendar.TimeSlot{"x@corp.example": {tuesday}}}
	s := teamService(db, b)
	owner := outlookOwner(t, db, "m")
	if _, err := s.QueryForTeam(context.Background(), owner, []string{"x@corp.example"}, monday, monday.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	res, err := s.QueryForTeam(context.Background(), owner, []string{"x@corp.example"}, monday, monday.Add(7*24*time.Hour))
	if err != nil || len(res[0].Busy) != 1 || !res[0].Busy[0].Start.Equal(tuesday.Start) {
		t.Fatalf("week read = %+v (%v), want Tuesday's window", res, err)
	}
	if busyMinutes(res[0].Busy, monday, monday.Add(7*24*time.Hour)) != 60 {
		t.Error("busy minutes over the week are not 60")
	}
}

// busyMinutes merges and clips to the week.
func TestPAC129_BusyMinutesMergesAndClips(t *testing.T) {
	week := monday.Add(7 * 24 * time.Hour)
	got := busyMinutes([]calendar.TimeSlot{
		slot(monday.Add(-30*time.Minute), time.Hour), // 30 inside
		slot(monday.Add(2*time.Hour), time.Hour),
		slot(monday.Add(2*time.Hour+30*time.Minute), time.Hour), // overlaps: +30
		slot(week.Add(-15*time.Minute), time.Hour),              // 15 inside
	}, monday, week)
	if got != 30+90+15 {
		t.Errorf("busyMinutes = %d, want 135", got)
	}
}

// AC-34 (engine half): an added person whose adder is no longer an owner is
// unknown in Find a time, with no provider call.
func TestPAC129_AC34_NoAddingOwnerIsUnknown(t *testing.T) {
	db := openTestDB(t)
	owner := pac67User(t, db, "o-"+uuid.NewString()[:8]+"@corp.example") // no calendar: never a network call
	team, err := storage.CreateTeam(db, "T", owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.AddTeamMember(db, team.ID, owner, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO team_added_members (team_id, email, added_by) VALUES ($1, 'x@corp.example', NULL)`, team.ID); err != nil {
		t.Fatal(err)
	}
	b := &batchReader{max: 50, table: map[string][]calendar.TimeSlot{"x@corp.example": nil}}
	e := &TeamAvailabilityEngine{DB: db, FreeBusy: teamService(db, b)}
	slots, err := e.FindSlots(context.Background(), team.ID, monday, 60)
	if err != nil || len(slots) == 0 {
		t.Fatalf("slots %d, %v", len(slots), err)
	}
	// The owner has no calendar connected: unknown too.
	if s := slots[0]; s.TotalCount != 2 || s.UnknownCount != 2 || s.FreeCount != 0 || b.nCalls() != 0 {
		t.Errorf("slot %+v, calls %d; want X unknown with no call", s, b.nCalls())
	}
}
