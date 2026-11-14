package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Enach/paceday/backend/internal/testdb"
)

// PAC-75 (contract contracts/features/PAC-75.md §3): denied admin step-up
// outcomes are recorded individually up to StepupDenialRecordBound per user
// and UTC-aligned StepupDenialWindow; the rest are counted in
// sso_stepup_denial_windows.left_out by one upsert in the record transaction.

// pac75T0 is 2 minutes into a window.
var pac75T0 = time.Date(2026, 11, 14, 10, 2, 0, 0, time.UTC)

func pac75Denied(user uuid.UUID, org *uuid.UUID, reason string, now time.Time) SSOStepupOutcome {
	return SSOStepupOutcome{UserID: user, OrgID: org, Provider: "google", Reason: reason, Now: now}
}

func pac75StepupEvents(t *testing.T, db *sql.DB, user uuid.UUID) (granted, denied int) {
	t.Helper()
	evs, err := ListSSOEventsByUser(context.Background(), db, user)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range evs {
		if ev.Kind != SSOEventStepup {
			continue
		}
		if strings.Contains(string(ev.Details), `"outcome": "granted"`) || strings.Contains(string(ev.Details), `"outcome":"granted"`) {
			granted++
		} else {
			denied++
		}
	}
	return granted, denied
}

func pac75Windows(t *testing.T, db *sql.DB, user uuid.UUID) []SSOStepupDenialWindow {
	t.Helper()
	ws, err := ListSSOStepupDenialWindows(context.Background(), db, user)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestPAC75_WindowStartIsUTCAligned(t *testing.T) {
	for in, want := range map[time.Time]time.Time{
		time.Date(2026, 11, 14, 10, 9, 59, 999, time.UTC):                     time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 14, 10, 10, 0, 0, time.UTC):                       time.Date(2026, 11, 14, 10, 10, 0, 0, time.UTC),
		time.Date(2026, 11, 14, 11, 5, 0, 0, time.FixedZone("+01", 3600)):     time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 14, 10, 37, 0, 0, time.FixedZone("+0530", 19800)): time.Date(2026, 11, 14, 5, 0, 0, 0, time.UTC),
	} {
		got := StepupDenialWindowStart(in)
		if !got.Equal(want) || got.Location() != time.UTC || got.Unix()%600 != 0 {
			t.Errorf("StepupDenialWindowStart(%v) = %v, want %v", in, got, want)
		}
	}
}

// AC-8 (storage), AC-9, AC-10, AC-11: the bound, the count, grants always
// recorded, per user, per window.
func TestPAC75_DenialBoundPerUserAndWindow(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	user, org, _ := stepupFixture(t, db, "pac75-bound")
	reasons := []string{"state_mismatch", "token_invalid", "not_admin", "hd_mismatch"}
	for i := 0; i < 50; i++ {
		rec, err := RecordSSOStepupOutcomeWindow(ctx, db, pac75Denied(user, &org, reasons[i%len(reasons)], pac75T0.Add(time.Duration(i)*time.Second)))
		if err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
		wantLeftOut := 0
		if i >= StepupDenialRecordBound {
			wantLeftOut = i - StepupDenialRecordBound + 1
		}
		if rec.Grant != nil || rec.LeftOut != wantLeftOut || !rec.WindowStart.Equal(time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)) {
			t.Fatalf("record %d = %+v, want left_out %d in the 10:00 window", i, rec, wantLeftOut)
		}
	}
	if g, d := pac75StepupEvents(t, db, user); g != 0 || d != 5 {
		t.Fatalf("events: %d granted, %d denied; want 0 and 5", g, d)
	}
	ws := pac75Windows(t, db, user)
	if len(ws) != 1 || ws[0].Recorded != 5 || ws[0].LeftOut != 45 || ws[0].LastLeftOutAt == nil ||
		!ws[0].LastLeftOutAt.Equal(pac75T0.Add(49*time.Second)) {
		t.Fatalf("windows = %+v, want one with recorded 5, left_out 45, last_left_out_at T0+49s", ws)
	}

	// AC-9: at the bound, a grant is issued and recorded; the window is untouched.
	rec, err := RecordSSOStepupOutcomeWindow(ctx, db, SSOStepupOutcome{UserID: user, OrgID: &org, Provider: "google", Granted: true, Now: pac75T0.Add(time.Minute)})
	if err != nil || rec.Grant == nil || rec.LeftOut != 0 {
		t.Fatalf("grant at the bound = %+v, %v", rec, err)
	}
	if g, d := pac75StepupEvents(t, db, user); g != 1 || d != 5 {
		t.Fatalf("events after the grant: %d granted, %d denied; want 1 and 5", g, d)
	}
	if ws := pac75Windows(t, db, user); len(ws) != 1 || ws[0].Recorded != 5 || ws[0].LeftOut != 45 {
		t.Fatalf("the grant changed the window: %+v", ws)
	}

	// AC-10: another user of the same org is recorded.
	other, err := UpsertUser(db, "other@"+strings.TrimPrefix(mustUserEmail(t, db, user), "admin@"), "Other", "", "google", "sub-other-"+user.String())
	if err != nil {
		t.Fatal(err)
	}
	if rec, err := RecordSSOStepupOutcomeWindow(ctx, db, pac75Denied(other.ID, &org, "not_admin", pac75T0)); err != nil || rec.LeftOut != 0 {
		t.Fatalf("other user = %+v, %v; want recorded", rec, err)
	}
	if _, d := pac75StepupEvents(t, db, other.ID); d != 1 {
		t.Fatalf("other user: %d denied events, want 1", d)
	}

	// AC-11: once the window has passed, the next denial is recorded again.
	next := StepupDenialWindowStart(pac75T0).Add(StepupDenialWindow)
	if rec, err := RecordSSOStepupOutcomeWindow(ctx, db, pac75Denied(user, &org, "not_admin", next)); err != nil || rec.LeftOut != 0 || !rec.WindowStart.Equal(next) {
		t.Fatalf("next window = %+v, %v; want recorded in %v", rec, err, next)
	}
	if _, d := pac75StepupEvents(t, db, user); d != 6 {
		t.Fatalf("%d denied events after the window passed, want 6", d)
	}
	if ws := pac75Windows(t, db, user); len(ws) != 2 || ws[1].Recorded != 1 || ws[1].LeftOut != 0 || ws[1].LastLeftOutAt != nil {
		t.Fatalf("windows = %+v, want a new one with recorded 1", ws)
	}
}

func mustUserEmail(t *testing.T, db *sql.DB, id uuid.UUID) string {
	t.Helper()
	u, err := GetUserByID(db, id)
	if err != nil || u == nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	return u.Email
}

// A granted outcome whose tenant pin fails is not recorded; the handler
// re-records it as tid_changed, a denial, which goes through the window.
func TestPAC75_TenantPinRefusalCountsNothing(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	user, org, _ := stepupFixture(t, db, "pac75-pin")
	if _, err := db.Exec(`UPDATE organizations SET ms_tenant_id = 'aaaa' WHERE id = $1`, org); err != nil {
		t.Fatal(err)
	}
	_, err := RecordSSOStepupOutcomeWindow(ctx, db, SSOStepupOutcome{UserID: user, OrgID: &org, Provider: "microsoft", Granted: true, PinTenant: "bbbb", Now: pac75T0})
	if !errors.Is(err, ErrTenantPinned) {
		t.Fatalf("err = %v, want ErrTenantPinned", err)
	}
	if ws := pac75Windows(t, db, user); len(ws) != 0 {
		t.Fatalf("windows = %+v, want none", ws)
	}
	if g, d := pac75StepupEvents(t, db, user); g+d != 0 {
		t.Fatalf("%d events, want none", g+d)
	}
}

// AC-12: 50 concurrent denied records for one user, over two pools (two api
// processes): exactly 5 recorded, 45 left out, no deadlock or lock timeout.
func TestPAC75_DenialBoundConcurrentAcrossPools(t *testing.T) {
	dsn, cleanup, err := testdb.Create()
	if err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() { _ = cleanup() })
	pools := make([]*sql.DB, 2)
	for i := range pools {
		if pools[i], err = Open(dsn); err != nil {
			t.Fatal(err)
		}
		pools[i].SetMaxOpenConns(25)
		defer pools[i].Close()
	}
	user, org, _ := stepupFixture(t, pools[0], "pac75-conc")
	// The second user has no session guard either way; one with, one without.
	gen, _, err := CurrentGeneration(pools[0], user)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 50)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := context.Background()
			if i%2 == 0 {
				ctx = WithSessionGuard(ctx, &SessionGuard{UserID: user, Gen: gen})
			}
			<-start
			_, err := RecordSSOStepupOutcomeWindow(ctx, pools[i%2], pac75Denied(user, &org, "state_mismatch", pac75T0))
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && (pg.Code == "40P01" || pg.Code == "55P03") {
			t.Fatalf("concurrent record: %s %v", pg.Code, err)
		}
		if err != nil {
			t.Fatalf("concurrent record: %v", err)
		}
	}
	if _, d := pac75StepupEvents(t, pools[0], user); d != 5 {
		t.Fatalf("%d denied events, want 5", d)
	}
	if ws := pac75Windows(t, pools[1], user); len(ws) != 1 || ws[0].Recorded != 5 || ws[0].LeftOut != 45 {
		t.Fatalf("windows = %+v, want recorded 5, left_out 45", ws)
	}
}

// AC-13 (storage): an orgless user's denials are bounded the same way.
func TestPAC75_OrglessDenialsAreBounded(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	u, err := UpsertUser(db, "pac75-"+uuid.NewString()[:8]+"@gmail.com", "Orgless", "", "google", "sub-orgless-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := RecordSSOStepupOutcomeWindow(ctx, db, pac75Denied(u.ID, nil, "state_mismatch", pac75T0)); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	var withOrg, total int
	if err := db.QueryRow(`SELECT count(*) FILTER (WHERE org_id IS NOT NULL), count(*) FROM sso_events WHERE user_id = $1`, u.ID).Scan(&withOrg, &total); err != nil {
		t.Fatal(err)
	}
	if total != 5 || withOrg != 0 {
		t.Fatalf("%d events (%d with an org), want 5 with none", total, withOrg)
	}
	if ws := pac75Windows(t, db, u.ID); len(ws) != 1 || ws[0].Recorded != 5 || ws[0].LeftOut != 3 {
		t.Fatalf("windows = %+v, want recorded 5, left_out 3", ws)
	}
}

// AC-32: a denied record held after its window upsert while the same user is
// deleted: no deadlock; the deletion completes after the record commits.
// With and without a session guard (contract §3.5: the explicit KEY SHARE is
// what keeps the order when no guard is present).
func TestPAC75_DenialRecordAndAccountDeletionDoNotDeadlock(t *testing.T) {
	for _, guarded := range []bool{false, true} {
		name := "no guard"
		if guarded {
			name = "guard"
		}
		t.Run(name, func(t *testing.T) {
			db := openTestDB(t)
			user, org, _ := stepupFixture(t, db, "pac75-del")
			ctx := context.Background()
			if guarded {
				gen, _, err := CurrentGeneration(db, user)
				if err != nil {
					t.Fatal(err)
				}
				ctx = WithSessionGuard(ctx, &SessionGuard{UserID: user, Gen: gen})
			}
			// The window row exists before the held record, so its upsert
			// takes the UPDATE path, which re-checks no foreign key: only the
			// explicit KEY SHARE on the actor (contract §3.3 step 1) puts the
			// users row first. Without it, the deletion locks users, waits on
			// the window row, and the record's event insert (FK KEY SHARE on
			// users) closes the cycle: 40P01 (review F1).
			if _, err := RecordSSOStepupOutcomeWindow(ctx, db, pac75Denied(user, &org, "not_admin", pac75T0)); err != nil {
				t.Fatalf("first record: %v", err)
			}
			held, release := make(chan struct{}), make(chan struct{})
			InstallAfterStepupDenialUpsert(t, func(id uuid.UUID) {
				if id == user {
					close(held)
					<-release
				}
			})
			recErr := make(chan error, 1)
			go func() {
				_, err := RecordSSOStepupOutcomeWindow(ctx, db, pac75Denied(user, &org, "not_admin", pac75T0))
				recErr <- err
			}()
			<-held
			delErr := make(chan error, 1)
			go func() {
				_, err := db.Exec(`DELETE FROM users WHERE id = $1`, user)
				delErr <- err
			}()
			select {
			case err := <-delErr:
				t.Fatalf("the deletion did not wait for the record: %v", err)
			case <-time.After(300 * time.Millisecond):
			}
			close(release)
			if err := <-recErr; err != nil {
				t.Fatalf("record: %v", err)
			}
			select {
			case err := <-delErr:
				if err != nil {
					t.Fatalf("delete: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the deletion did not complete")
			}
			var n int
			if err := db.QueryRow(`SELECT count(*) FROM sso_stepup_denial_windows WHERE user_id = $1`, user).Scan(&n); err != nil || n != 0 {
				t.Fatalf("%d window rows left after the deletion (%v)", n, err)
			}
		})
	}
}

// AC-33 (storage): a denied record waiting on the window row past the lock
// timeout fails with 55P03 and records or counts nothing.
func TestPAC75_WindowWaitIsBoundedByLockTimeout(t *testing.T) {
	db := openTestDB(t)
	user, org, _ := stepupFixture(t, db, "pac75-wait")
	prev := LinkLockTimeout
	LinkLockTimeout = 300 * time.Millisecond
	t.Cleanup(func() { LinkLockTimeout = prev })
	ws := StepupDenialWindowStart(pac75T0)
	if _, err := db.Exec(`INSERT INTO sso_stepup_denial_windows (user_id, window_start, recorded) VALUES ($1, $2, 1)`, user, ws); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`SELECT 1 FROM sso_stepup_denial_windows WHERE user_id = $1 FOR UPDATE`, user); err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	_, err = RecordSSOStepupOutcomeWindow(context.Background(), db, pac75Denied(user, &org, "not_admin", pac75T0))
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "55P03" {
		t.Fatalf("err = %v, want SQLSTATE 55P03", err)
	}
	if d := time.Since(began); d > 5*time.Second {
		t.Fatalf("waited %s", d)
	}
	_ = tx.Rollback()
	if _, d := pac75StepupEvents(t, db, user); d != 0 {
		t.Fatalf("%d events, want none", d)
	}
	if got := pac75Windows(t, db, user); len(got) != 1 || got[0].Recorded != 1 || got[0].LeftOut != 0 {
		t.Fatalf("window = %+v, want unchanged", got)
	}
	// The timeout was transaction-local: the pool's next user waits as usual.
	var lt string
	if err := db.QueryRow(`SHOW lock_timeout`).Scan(&lt); err != nil || lt != "0" {
		t.Fatalf("lock_timeout after the record = %q (%v), want the default 0", lt, err)
	}
}

// AC-34: migration 039 on a database with sso_events rows, down and up again:
// sso_events unchanged; an unaligned window_start is refused.
func TestPAC75_Migration039(t *testing.T) {
	db, to := migrationTestDB(t)
	to(38)
	u := seedMigrationUser(t, db, "m39@example.com", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	for i := 0; i < 3; i++ {
		if _, err := db.Exec(`INSERT INTO sso_events (user_id, kind, details) VALUES ($1, 'sso_stepup', '{"outcome":"denied","reason":"state_mismatch"}')`, u); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := func() string {
		t.Helper()
		var out string
		if err := db.QueryRow(`SELECT string_agg(id::text || user_id::text || kind || details::text || created_at::text, ',' ORDER BY id) FROM sso_events`).Scan(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := snapshot()
	to(39)
	if snapshot() != before {
		t.Fatal("migration 039 changed sso_events")
	}
	for _, bad := range []string{"2026-11-14T10:00:01Z", "2026-11-14T10:00:00.5Z", "2026-11-14T10:05:00Z"} {
		if _, err := db.Exec(`INSERT INTO sso_stepup_denial_windows (user_id, window_start, recorded) VALUES ($1, $2, 1)`, u, bad); err == nil {
			t.Errorf("unaligned window_start %s accepted", bad)
		}
	}
	if _, err := db.Exec(`INSERT INTO sso_stepup_denial_windows (user_id, window_start, recorded) VALUES ($1, '2026-11-14T11:00:00+01:00', 1)`, u); err != nil {
		t.Fatalf("aligned window_start refused: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO sso_stepup_denial_windows (user_id, window_start, recorded) VALUES ($1, '2026-11-14T10:00:00Z', 1)`, u); err == nil {
		t.Error("10:00Z and 11:00+01:00 are two keys")
	}
	for _, bad := range []string{
		`INSERT INTO sso_stepup_denial_windows (user_id, window_start, recorded) VALUES ($1, '2026-11-14T12:00:00Z', 0)`,
		`INSERT INTO sso_stepup_denial_windows (user_id, window_start, recorded, left_out) VALUES ($1, '2026-11-14T12:10:00Z', 5, 1)`,
		`INSERT INTO sso_stepup_denial_windows (user_id, window_start, recorded, last_left_out_at) VALUES ($1, '2026-11-14T12:20:00Z', 5, now())`,
	} {
		if _, err := db.Exec(bad, u); err == nil {
			t.Errorf("accepted: %s", bad)
		}
	}
	to(38)
	if snapshot() != before {
		t.Fatal("down 039 changed sso_events")
	}
	var exists bool
	if err := db.QueryRow(`SELECT to_regclass('public.sso_stepup_denial_windows') IS NOT NULL`).Scan(&exists); err != nil || exists {
		t.Fatalf("table still there after down (%v)", err)
	}
	to(39)
}
