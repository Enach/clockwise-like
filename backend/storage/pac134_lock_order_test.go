package storage

// PAC-134 AC-11b (spec §3.5, contract contracts/features/PAC-134.md §6.3):
// one case per row W1-W29 of the lock-order table, each in both start
// orders, with real contention on rows the erase-address tool locks.
//
//   - Order A (the tool first): the tool runs to the end of its lock phase
//     (after erase_t8) and is held there, every lock taken; then the row's
//     live writer runs. It must be seen WAITING on a lock (pg_stat_activity,
//     wait_event_type = 'Lock'), except the writers the table marks N, which
//     must complete while the tool is still held. Then the tool is released;
//     both complete with their normal answers, never 40P01 (deadlock),
//     55P03 (lock wait timeout) or 57014.
//   - Order B (the writer first): the real writer is held after its locks by
//     an existing seam, or run inside an outer transaction kept open (the
//     Querier-taking writers), or, for the two DBTX-only sweeps (W9, W27),
//     its own statement runs inside an outer transaction (emulated: the same
//     statement, same lock order). Then the tool runs and must be seen
//     waiting (or, N rows, complete without waiting); then the writer
//     commits and the tool completes.
//
// Every case runs on its own database cloned from one migrated template.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/internal/testdb"
	"github.com/google/uuid"
)

const loX = "pat@ext.example"

// loTemplate is a migrated database the cases clone (CREATE DATABASE …
// TEMPLATE), created once per top-level test.
type loTemplate struct {
	name  string
	admin *sql.DB
	base  *url.URL
}

func newLoTemplate(t *testing.T) *loTemplate {
	t.Helper()
	dsn, cleanup, err := testdb.Create()
	if err != nil {
		t.Fatalf("PostgreSQL unavailable: %v", err)
	}
	db, err := Open(dsn)
	if err != nil {
		_ = cleanup()
		t.Fatal(err)
	}
	db.Close()
	if err := testdb.WaitNoSessions(dsn, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(dsn)
	admin, err := sql.Open("pgx", testdb.AdminDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close(); _ = cleanup() })
	return &loTemplate{name: strings.TrimPrefix(u.Path, "/"), admin: admin, base: u}
}

// loFix is one case's database and fixture.
type loFix struct {
	t        *testing.T
	dsn, dat string
	db       *sql.DB // fixture and observation pool
	writer   *sql.DB // the live writer's pool ("lo-writer")
	tool     *sql.DB // the tool's pool (application_name = EraseAddressApp)

	owner, xu    uuid.UUID // O: owner of TA/TB/TC, manager, owner of L1/L2; XU: an account holding X (only with withAccount)
	ta, tb, tc   uuid.UUID
	l1, l2       uuid.UUID
	bookingL1    uuid.UUID
	bookingKey   string
	createKey    string
	createDigest []byte
	otherOnL2    uuid.UUID
	inviteTA     uuid.UUID
}

func (tpl *loTemplate) fix(t *testing.T) *loFix {
	t.Helper()
	name := fmt.Sprintf("lo_%d_%s", time.Now().UnixNano(), strings.ReplaceAll(uuid.NewString()[:8], "-", ""))
	if _, err := tpl.admin.Exec(`CREATE DATABASE "` + name + `" TEMPLATE "` + tpl.name + `"`); err != nil {
		t.Fatal(err)
	}
	u := *tpl.base
	u.Path = "/" + name
	dsn := u.String()
	f := &loFix{t: t, dsn: dsn, dat: name}
	var err error
	if f.db, err = OpenNoMigrate(dsn, "lo-fixture"); err != nil {
		t.Fatal(err)
	}
	if f.writer, err = OpenNoMigrate(dsn, "lo-writer"); err != nil {
		t.Fatal(err)
	}
	if f.tool, err = OpenNoMigrateDescribeExec(dsn, EraseAddressApp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		f.db.Close()
		f.writer.Close()
		f.tool.Close()
		_, _ = tpl.admin.Exec(`DROP DATABASE IF EXISTS "` + name + `" WITH (FORCE)`)
	})
	f.base()
	return f
}

func (f *loFix) exec(q string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Exec(q, args...); err != nil {
		f.t.Fatalf("%s: %v", q, err)
	}
}

func (f *loFix) id(q string, args ...any) uuid.UUID {
	f.t.Helper()
	var id uuid.UUID
	if err := f.db.QueryRow(q, args...).Scan(&id); err != nil {
		f.t.Fatalf("%s: %v", q, err)
	}
	return id
}

func (f *loFix) count(q string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.db.QueryRow(q, args...).Scan(&n); err != nil {
		f.t.Fatalf("%s: %v", q, err)
	}
	return n
}

func (f *loFix) team() uuid.UUID {
	id := f.id(`INSERT INTO teams (name, created_by) VALUES ('Team', $1) RETURNING id`, f.owner)
	f.exec(`INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $2, 'owner')`, id, f.owner)
	return id
}

// base is X in every place: T1, T2 (pending) on TA; the roster entry of O
// (as manager) with its TA assignment, a 1:1 and a cached figure; a pending
// co-host invitation and a keyed booking on live L1; a booking on deleted
// L2 (with another booker's, due for PAC-40's job); a keyed creation whose
// outcomes hold X.
func (f *loFix) base() {
	f.t.Helper()
	f.owner = f.id(`INSERT INTO users (email, name) VALUES ('owner@lo.example', 'Owner') RETURNING id`)
	f.ta, f.tb, f.tc = f.team(), f.team(), f.team()
	f.exec(`INSERT INTO team_added_members (team_id, email, display_name, added_by) VALUES ($1, $2, 'Pat', $3)`, f.ta, loX, f.owner)
	f.inviteTA = f.id(`INSERT INTO team_invites (team_id, invitee_email, invited_by, token, expires_at) VALUES ($1, $2, $3, $4, now() + interval '7 days') RETURNING id`,
		f.ta, loX, f.owner, "lo-"+uuid.NewString())
	f.exec(`INSERT INTO manager_team_members (manager_user_id, member_email, display_name, source) VALUES ($1, $2, 'Pat', 'manual')`, f.owner, loX)
	f.exec(`INSERT INTO manager_team_member_assignments (manager_user_id, team_id, member_email) VALUES ($1, $2, $3)`, f.owner, f.ta, loX)
	f.exec(`INSERT INTO one_on_one_occurrences (manager_user_id, member_email, calendar_event_id, occurred_at) VALUES ($1, $2, 'ev-x', now())`, f.owner, loX)
	f.exec(`INSERT INTO team_analytics_cache (manager_user_id, member_email, week_start) VALUES ($1, $2, '2026-11-02')`, f.owner, loX)
	l1, err := CreateSchedulingLink(f.db, &SchedulingLink{OwnerUserID: f.owner, Slug: "lo-l1-" + uuid.NewString()[:8], Title: "L1",
		DurationOptions: []int{30}, DaysOfWeek: []int{1, 2, 3, 4, 5, 6, 0}, WindowStart: "00:00", WindowEnd: "23:59", UsageType: "reusable", Active: true})
	if err != nil {
		f.t.Fatal(err)
	}
	f.l1 = l1.ID
	if _, err := AddLinkHost(f.db, f.l1, f.owner, "accepted"); err != nil {
		f.t.Fatal(err)
	}
	if _, err := AddLinkInvitation(f.db, f.l1, loX); err != nil {
		f.t.Fatal(err)
	}
	f.bookingKey = "lo-booking-key-000001"
	f.bookingL1 = f.id(`INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time, notes, idempotency_key)
		VALUES ($1, 'Pat', $2, date_trunc('hour', now()) + interval '2 days', date_trunc('hour', now()) + interval '2 days 30 minutes', 'n', $3) RETURNING id`,
		f.l1, loX, f.bookingKey)
	f.l2 = f.id(`INSERT INTO scheduling_links (owner_user_id, slug, title, active, deleted_at) VALUES ($1, $2, 'L2', false, now()) RETURNING id`, f.owner, "lo-l2-"+uuid.NewString()[:8])
	f.exec(`INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time) VALUES ($1, 'Pat', $2, now() - interval '200 days', now() - interval '200 days' + interval '30 minutes')`, f.l2, loX)
	f.otherOnL2 = f.id(`INSERT INTO bookings (link_id, booker_name, booker_email, start_time, end_time) VALUES ($1, 'Other', 'other@lo.example', now() - interval '201 days', now() - interval '201 days' + interval '30 minutes') RETURNING id`, f.l2)
	l4 := f.id(`INSERT INTO scheduling_links (owner_user_id, slug, title) VALUES ($1, $2, 'L4') RETURNING id`, f.owner, "lo-l4-"+uuid.NewString()[:8])
	f.createKey = "lo-create-key-000001"
	d := sha256.Sum256([]byte("lo create"))
	f.createDigest = d[:]
	f.exec(`INSERT INTO scheduling_link_create_keys (owner_user_id, idempotency_key, link_id, request_sha256, co_host_changes)
		VALUES ($1, $2, $3, $4, $5::jsonb)`, f.owner, f.createKey, l4, f.createDigest,
		`{"invited_count":1,"not_added_count":0,"addresses":[{"email":"`+loX+`","outcome":"invited"}],"removed":[]}`)
}

// withAccount adds XU, an account holding X, domain-confirmed: the holder of
// X's co-host invitation (W21-W23).
func (f *loFix) withAccount() {
	f.xu = f.id(`INSERT INTO users (email, name, address_standing) VALUES ($1, 'Pat', 'domain_confirmed') RETURNING id`, loX)
}

func (f *loFix) opts() EraseAddressOptions {
	return EraseAddressOptions{Address: loX, AccountHeldAcknowledged: true, MaxDuration: 20 * time.Second,
		Run: OperatorRun{Operator: "lo", Reason: "PAC-134 AC-11b"}}
}

// waiting reports whether a session of app on this case's database waits on
// a lock.
func (f *loFix) waiting(app string) bool {
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE datname = $1 AND application_name = $2 AND wait_event_type = 'Lock'`,
		f.dat, app).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n > 0
}

// waitFor polls until app is seen waiting on a lock, or done closes (a
// failure: it finished without waiting).
func (f *loFix) waitFor(app string, done <-chan struct{}, what string) {
	f.t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if f.waiting(app) {
			return
		}
		select {
		case <-done:
			f.t.Fatalf("%s finished without waiting on a lock", what)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.t.Fatalf("%s was never seen waiting on a lock", what)
}

// loHold installs the after-statement seam that holds the first run that
// passes stmt until release is closed.
func loHold(t *testing.T, stmt string) (held <-chan struct{}, release func()) {
	t.Helper()
	h := make(chan struct{})
	r := make(chan struct{})
	var fired atomic.Bool
	InstallEraseAfterStatement(t, func(ctx context.Context, name string) error {
		if name != stmt || !fired.CompareAndSwap(false, true) {
			return nil
		}
		close(h)
		select {
		case <-r:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	var once sync.Once
	rel := func() { once.Do(func() { close(r) }) }
	t.Cleanup(rel)
	return h, rel
}

// loRun runs fn in a goroutine; done closes when it returned.
type loRun struct {
	done chan struct{}
	err  error
}

func loGo(fn func() error) *loRun {
	r := &loRun{done: make(chan struct{})}
	go func() { defer close(r.done); r.err = fn() }()
	return r
}

func (r *loRun) wait(t *testing.T, what string) error {
	t.Helper()
	select {
	case <-r.done:
		return r.err
	case <-time.After(25 * time.Second):
		t.Fatalf("%s did not finish", what)
	}
	return nil
}

// tool runs the erasure on its own pool.
func (f *loFix) startTool() (*loRun, *EraseAddressResult) {
	res := &EraseAddressResult{}
	r := loGo(func() error {
		got, err := EraseAddress(context.Background(), f.tool, f.opts())
		if got != nil {
			*res = *got
		}
		return err
	})
	return r, res
}

// loLockError reports an error that must never be a live path's answer
// under contention with the tool.
func loLockError(err error) bool {
	switch pgCode(err) {
	case "40P01", "55P03", "57014":
		return true
	}
	return false
}

// loCase is one row of the table.
type loCase struct {
	name string
	// setup adds what the row needs on top of the base fixture.
	setup func(f *loFix)
	// writerA is the real live writer, run while the tool is held.
	writerA func(ctx context.Context, f *loFix) error
	// waitsA: the writer must wait on the tool (false: N rows, it must
	// complete while the tool is held).
	waitsA bool
	// okA checks the writer's answer once the tool committed.
	okA func(f *loFix, err error) error
	// holdB starts the writer so that it holds its locks; release lets it
	// finish and returns its answer.
	holdB func(ctx context.Context, f *loFix) (release func() error)
	// waitsB: the tool must wait on the writer.
	waitsB bool
}

func loNil(_ *loFix, err error) error { return err }

func loIs(target error) func(*loFix, error) error {
	return func(_ *loFix, err error) error {
		if errors.Is(err, target) {
			return nil
		}
		return fmt.Errorf("answer %v, want %v", err, target)
	}
}

// loTx runs fn inside an outer transaction on the writer's pool and keeps
// it open: release commits it.
func loTx(ctx context.Context, f *loFix, fn func(tx *sql.Tx) error) func() error {
	f.t.Helper()
	tx, err := f.writer.BeginTx(ctx, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		f.t.Fatalf("writer in an outer transaction: %v", err)
	}
	return tx.Commit
}

// loSeamHold runs writer in a goroutine and returns once hold reports that
// the writer reached its seam; release lets it go and returns its answer.
func loSeamHold(f *loFix, install func(reached chan<- struct{}, gate <-chan struct{}), writer func() error) func() error {
	f.t.Helper()
	reached := make(chan struct{})
	gate := make(chan struct{})
	install(reached, gate)
	r := loGo(writer)
	select {
	case <-reached:
	case <-r.done:
		f.t.Fatalf("the writer finished before its seam: %v", r.err)
	case <-time.After(10 * time.Second):
		f.t.Fatal("the writer never reached its seam")
	}
	var once sync.Once
	f.t.Cleanup(func() { once.Do(func() { close(gate) }) })
	return func() error {
		once.Do(func() { close(gate) })
		return r.wait(f.t, "the writer")
	}
}

// loOnce wraps a seam body so it fires once.
func loOnce(reached chan<- struct{}, gate <-chan struct{}) func() {
	var fired atomic.Bool
	return func() {
		if fired.CompareAndSwap(false, true) {
			close(reached)
			<-gate
		}
	}
}

func (f *loFix) teamSeam(ctx context.Context, writer func() error) func() error {
	return loSeamHold(f, func(reached chan<- struct{}, gate <-chan struct{}) {
		once := loOnce(reached, gate)
		InstallAfterTeamLock(f.t, func(string, uuid.UUID) error { once(); return nil })
	}, writer)
}

func (f *loFix) member(email string) *ManagerTeamMember {
	return &ManagerTeamMember{ManagerUserID: f.owner, MemberEmail: email, DisplayName: "Pat", Source: "manual", Cadence: "none"}
}

func (f *loFix) link1() *SchedulingLink {
	f.t.Helper()
	l, err := GetSchedulingLinkByID(f.db, f.l1)
	if err != nil || l == nil {
		f.t.Fatal(l, err)
	}
	return l
}

func (f *loFix) saveBooking(ctx context.Context, b *Booking) (*Booking, error) {
	conn, err := f.writer.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return SaveBookingOnLiveLink(ctx, conn, b, nil)
}

func (f *loFix) freshBooking() *Booking {
	start := time.Now().Truncate(time.Hour).Add(5 * 24 * time.Hour)
	return &Booking{LinkID: f.l1, BookerName: "Pat", BookerEmail: loX, StartTime: start, EndTime: start.Add(30 * time.Minute),
		IdempotencyKey: "lo-fresh-key-" + strings.ReplaceAll(uuid.NewString()[:8], "-", "x")}
}

// storedBooking is X's keyed booking on L1 as a retry would send it.
func (f *loFix) storedBooking() *Booking {
	f.t.Helper()
	b := &Booking{LinkID: f.l1, IdempotencyKey: f.bookingKey}
	if err := f.db.QueryRow(`SELECT booker_name, booker_email, start_time, end_time, notes FROM bookings WHERE id = $1`, f.bookingL1).
		Scan(&b.BookerName, &b.BookerEmail, &b.StartTime, &b.EndTime, &b.Notes); err != nil {
		f.t.Fatal(err)
	}
	return b
}

func (f *loFix) newLink(title string) *SchedulingLink {
	return &SchedulingLink{OwnerUserID: f.owner, Slug: "lo-new-" + uuid.NewString()[:8], Title: title, DurationOptions: []int{30},
		DaysOfWeek: []int{1, 2, 3, 4, 5}, WindowStart: "09:00", WindowEnd: "17:00", UsageType: "reusable", Active: true}
}

// expiredInvites stores X's expired pending invitations on TB and TC (W9).
func (f *loFix) expiredInvites() {
	for _, team := range []uuid.UUID{f.tb, f.tc} {
		f.exec(`INSERT INTO team_invites (team_id, invitee_email, invited_by, token, expires_at) VALUES ($1, $2, $3, $4, now() - interval '1 day')`,
			team, loX, f.owner, "lo-exp-"+uuid.NewString())
	}
}

// expiredKeys stores two keyed creations 25 h old whose outcomes hold X (W27).
func (f *loFix) expiredKeys() {
	for _, k := range []string{"lo-old-key-aaaaaaaa", "lo-old-key-BBBBBBBB"} {
		l := f.id(`INSERT INTO scheduling_links (owner_user_id, slug, title) VALUES ($1, $2, 'old') RETURNING id`, f.owner, "lo-old-"+uuid.NewString()[:8])
		f.exec(`INSERT INTO scheduling_link_create_keys (owner_user_id, idempotency_key, link_id, created_at, request_sha256, co_host_changes)
			VALUES ($1, $2, $3, now() - interval '25 hours', $4, $5::jsonb)`, f.owner, k, l, f.createDigest,
			`{"addresses":[],"removed":[{"email":"PAT@ext.example","status":"pending"}]}`)
	}
}

const (
	loExpireSQL = `UPDATE team_invites SET status = 'expired'
		WHERE id IN (SELECT id FROM team_invites WHERE status = 'pending' AND expires_at < NOW() ORDER BY id FOR UPDATE)`
	loCleanupSQL = `UPDATE scheduling_link_create_keys k SET request_sha256 = NULL, co_host_changes = NULL
		FROM (SELECT owner_user_id, idempotency_key FROM scheduling_link_create_keys
		      WHERE request_sha256 IS NOT NULL AND created_at <= now() - make_interval(secs => $1)
		      ORDER BY owner_user_id, idempotency_key COLLATE "C" FOR UPDATE) m
		WHERE k.owner_user_id = m.owner_user_id AND k.idempotency_key = m.idempotency_key`
)

func loCases() []loCase {
	return []loCase{
		{
			name: "W1 team add",
			writerA: func(ctx context.Context, f *loFix) error {
				_, created, err := AddTeamPersonCtx(ctx, f.writer, f.ta, f.owner, loX, "Pat")
				if err == nil && !created {
					return errors.New("the add did not create a new entry after the erasure")
				}
				return err
			},
			waitsA: true, okA: loNil, waitsB: true,
			holdB: func(ctx context.Context, f *loFix) func() error {
				return f.teamSeam(ctx, func() error { _, _, err := AddTeamPersonCtx(ctx, f.writer, f.ta, f.owner, loX, "Pat"); return err })
			},
		},
		{
			name: "W2 team invite",
			writerA: func(ctx context.Context, f *loFix) error {
				_, err := InviteTeamPersonCtx(ctx, f.writer, f.ta, f.owner, loX, "Pat")
				return err
			},
			waitsA: true, okA: loNil, waitsB: true,
			holdB: func(ctx context.Context, f *loFix) func() error {
				return f.teamSeam(ctx, func() error { _, err := InviteTeamPersonCtx(ctx, f.writer, f.ta, f.owner, loX, "Pat"); return err })
			},
		},
		{
			name: "W3 accept, already member",
			setup: func(f *loFix) {
				u := f.id(`INSERT INTO users (email) VALUES ('member@lo.example') RETURNING id`)
				f.exec(`INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $2, 'member')`, f.ta, u)
				f.xu = u
			},
			writerA: func(ctx context.Context, f *loFix) error {
				return AcceptTeamInviteForUserCtx(ctx, f.writer, f.inviteTA, f.ta, f.xu)
			},
			waitsA: true, okA: loIs(ErrInviteGone), waitsB: true,
			holdB: func(ctx context.Context, f *loFix) func() error {
				return f.teamSeam(ctx, func() error { return AcceptTeamInviteForUserCtx(ctx, f.writer, f.inviteTA, f.ta, f.xu) })
			},
		},
		{
			name: "W4 accept, upgrade",
			setup: func(f *loFix) {
				f.xu = f.id(`INSERT INTO users (email) VALUES ('upgrade@lo.example') RETURNING id`)
			},
			writerA: func(ctx context.Context, f *loFix) error {
				return AcceptTeamInviteForUserCtx(ctx, f.writer, f.inviteTA, f.ta, f.xu)
			},
			waitsA: true, okA: loIs(ErrInviteGone), waitsB: true,
			holdB: func(ctx context.Context, f *loFix) func() error {
				return f.teamSeam(ctx, func() error { return AcceptTeamInviteForUserCtx(ctx, f.writer, f.inviteTA, f.ta, f.xu) })
			},
		},
		{
			name: "W5 accept, legacy create",
			setup: func(f *loFix) {
				f.xu = f.id(`INSERT INTO users (email) VALUES ('legacy@lo.example') RETURNING id`)
				f.exec(`DELETE FROM team_added_members WHERE team_id = $1`, f.ta)
			},
			writerA: func(ctx context.Context, f *loFix) error {
				return AcceptTeamInviteForUserCtx(ctx, f.writer, f.inviteTA, f.ta, f.xu)
			},
			waitsA: true, okA: loIs(ErrInviteGone), waitsB: true,
			holdB: func(ctx context.Context, f *loFix) func() error {
				return f.teamSeam(ctx, func() error { return AcceptTeamInviteForUserCtx(ctx, f.writer, f.inviteTA, f.ta, f.xu) })
			},
		},
		{
			name: "W6 team removal",
			writerA: func(ctx context.Context, f *loFix) error {
				return RemoveTeamPersonCtx(ctx, f.writer, f.ta, f.owner, loX)
			},
			waitsA: true, okA: loIs(ErrTeamPersonNotFound), waitsB: true,
			holdB: func(ctx context.Context, f *loFix) func() error {
				return f.teamSeam(ctx, func() error { return RemoveTeamPersonCtx(ctx, f.writer, f.ta, f.owner, loX) })
			},
		},
		{
			name: "W7 Manager-path team removal",
			writerA: func(ctx context.Context, f *loFix) error {
				return RemoveManagerTeamPersonCtx(ctx, f.writer, f.owner, f.ta, loX)
			},
			waitsA: true, okA: loNil, waitsB: true,
			holdB: func(ctx context.Context, f *loFix) func() error {
				return f.teamSeam(ctx, func() error { return RemoveManagerTeamPersonCtx(ctx, f.writer, f.owner, f.ta, loX) })
			},
		},
		{
			name: "W8 team delete",
			writerA: func(ctx context.Context, f *loFix) error {
				return DeleteTeamCtx(ctx, f.writer, f.ta)
			},
			waitsA: true, okA: loNil, waitsB: true,
			holdB: func(ctx context.Context, f *loFix) func() error {
				return loTx(ctx, f, func(tx *sql.Tx) error { return DeleteTeam(tx, f.ta) })
			},
		},
		{
			// Order B emulated: ExpireOldInvites takes a DBTX and opens no
			// transaction of its own, so its (L-1) statement runs in an outer
			// transaction kept open.
			name:  "W9 pending-invitation expiry sweep",
			setup: func(f *loFix) { f.expiredInvites() },
			writerA: func(ctx context.Context, f *loFix) error {
				return ExpireOldInvites(f.writer)
			},
			waitsA: true, okA: loNil, waitsB: true,
			holdB: func(ctx context.Context, f *loFix) func() error {
				return loTx(ctx, f, func(tx *sql.Tx) error { _, err := tx.Exec(loExpireSQL); return err })
			},
		},
		{
			name: "W10 Manager add",
			writerA: func(ctx context.Context, f *loFix) error {
				return UpsertManagerTeamMemberCtx(ctx, f.writer, f.member(loX))
			},
			waitsA: true, okA: loNil, waitsB: true,
			holdB: func(ctx context.Context, f *loFix) func() error {
				return loTx(ctx, f, func(tx *sql.Tx) error { return UpsertManagerTeamMember(tx, f.member(loX)) })
			},
		},
		{
			name: "W11 Manager add for a team",
			writerA: func(ctx context.Context, f *loFix) error {
				return UpsertManagerTeamMemberForTeamCtx(ctx, f.writer, f.ta, f.member(loX))
			},
			waitsA: true, okA: loNil, waitsB: true,
			holdB: func(ctx context.Context, f *loFix) func() error {
				return loTx(ctx, f, func(tx *sql.Tx) error { return UpsertManagerTeamMemberForTeam(tx, f.ta, f.member(loX)) })
			},
		},
		{
			name: "W12 Manager detection",
			writerA: func(ctx context.Context, f *loFix) error {
				m := f.member(loX)
				m.Source = "auto"
				return UpsertManagerTeamMemberCtx(ctx, f.writer, m)
			},
			waitsA: true, okA: loNil, waitsB: true,
			holdB: func(ctx context.Context, f *loFix) func() error {
				m := f.member(loX)
				m.Source = "auto"
				return loTx(ctx, f, func(tx *sql.Tx) error { return UpsertManagerTeamMember(tx, m) })
			},
		},
		{
			name: "W13 Manager confirm",
			writerA: func(ctx context.Context, f *loFix) error {
				a, s, _, err := ConfirmManagerTeamMembersCtx(ctx, f.writer, f.owner, f.tb, []string{loX, "PAT@EXT.EXAMPLE"})
				if err == nil && (a != 0 || s != 2) {
					return fmt.Errorf("confirm assigned %d skipped %d, want 0 and 2", a, s)
				}
				return err
			},
			waitsA: true, okA: loNil, waitsB: true,
			holdB: func(ctx context.Context, f *loFix) func() error {
				return loTx(ctx, f, func(tx *sql.Tx) error {
					_, _, _, err := ConfirmManagerTeamMembers(tx, f.owner, f.tb, []string{loX})
					return err
				})
			},
		},
		{
			name: "W14 Manager patch",
			writerA: func(ctx context.Context, f *loFix) error {
				return PatchManagerTeamMemberCtx(ctx, f.writer, f.owner, loX, "New", "none", nil)
			},
			waitsA: true, okA: loNil, waitsB: true,
			holdB: func(ctx context.Context, f *loFix) func() error {
				return loTx(ctx, f, func(tx *sql.Tx) error { return PatchManagerTeamMember(tx, f.owner, loX, "New", "none", nil) })
			},
		},
		{
			name: "W15 Manager patch for a team",
			writerA: func(ctx context.Context, f *loFix) error {
				return PatchManagerTeamMemberForTeamCtx(ctx, f.writer, f.owner, f.ta, loX, "New", "none", nil)
			},
			waitsA: true, okA: loIs(sql.ErrNoRows), waitsB: true,
			holdB: func(ctx context.Context, f *loFix) func() error {
				return loTx(ctx, f, func(tx *sql.Tx) error {
					return PatchManagerTeamMemberForTeam(tx, f.owner, f.ta, loX, "New", "none", nil)
				})
			},
		},
		{
			name: "W16 Manager delete",
			writerA: func(ctx context.Context, f *loFix) error {
				return DeleteManagerTeamMemberByEmailCtx(ctx, f.writer, f.owner, loX)
			},
			waitsA: true, okA: loNil, waitsB: true,
			holdB: func(ctx context.Context, f *loFix) func() error {
				return loTx(ctx, f, func(tx *sql.Tx) error { return DeleteManagerTeamMemberByEmail(tx, f.owner, loX) })
			},
		},
		{
			// Order B: ON CONFLICT DO NOTHING locks no existing row, so a
			// recording that holds its new row first leaves the tool
			// nothing to wait for (N in that order).
			name: "W17 1:1 recording",
			writerA: func(ctx context.Context, f *loFix) error {
				return UpsertOneOnOneOccurrence(f.writer, f.owner, loX, "ev-x", time.Now())
			},
			waitsA: true,
			okA: func(f *loFix, err error) error {
				if err == nil && f.count(`SELECT count(*) FROM one_on_one_occurrences WHERE calendar_event_id = 'ev-x'`) != 1 {
					return errors.New("the recording did not insert after the erasure")
				}
				return err
			},
			waitsB: false,
			holdB: func(ctx context.Context, f *loFix) func() error {
				return loTx(ctx, f, func(tx *sql.Tx) error { return UpsertOneOnOneOccurrence(tx, f.owner, loX, "ev-new", time.Now()) })
			},
		},
		{
			name: "W18 analytics-cache write",
			writerA: func(ctx context.Context, f *loFix) error {
				return UpsertTeamAnalytics(f.writer, &TeamAnalyticsRow{ManagerUserID: f.owner, MemberEmail: loX,
					WeekStart: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), DataAvailable: true})
			},
			waitsA: true, okA: loNil, waitsB: true,
			holdB: func(ctx context.Context, f *loFix) func() error {
				return loTx(ctx, f, func(tx *sql.Tx) error {
					return UpsertTeamAnalytics(tx, &TeamAnalyticsRow{ManagerUserID: f.owner, MemberEmail: loX,
						WeekStart: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), DataAvailable: true})
				})
			},
		},
		{
			name: "W19 booking save, fresh key",
			writerA: func(ctx context.Context, f *loFix) error {
				_, err := f.saveBooking(ctx, f.freshBooking())
				return err
			},
			waitsA: false, okA: loNil, waitsB: false,
			holdB: func(ctx context.Context, f *loFix) func() error {
				return loSeamHold(f, func(reached chan<- struct{}, gate <-chan struct{}) {
					once := loOnce(reached, gate)
					InstallAfterBookingInsert(f.t, func(*Booking) error { once(); return nil })
				}, func() error { _, err := f.saveBooking(ctx, f.freshBooking()); return err })
			},
		},
		{
			name: "W20 co-host single invite",
			writerA: func(ctx context.Context, f *loFix) error {
				_, created, err := InviteLinkAddressCtx(ctx, f.writer, f.l1, loX)
				if err == nil && !created {
					return errors.New("the invite did not create a new invitation after the erasure")
				}
				return err
			},
			waitsA: true, okA: loNil, waitsB: true,
			holdB: func(ctx context.Context, f *loFix) func() error {
				return loSeamHold(f, func(reached chan<- struct{}, gate <-chan struct{}) {
					once := loOnce(reached, gate)
					InstallAfterLinkInviteLock(f.t, func(context.Context, uuid.UUID) error { once(); return nil })
				}, func() error { _, _, err := InviteLinkAddressCtx(ctx, f.writer, f.l1, loX); return err })
			},
		},
		{
			name:  "W21 co-host accept",
			setup: func(f *loFix) { f.withAccount() },
			writerA: func(ctx context.Context, f *loFix) error {
				matched, err := RespondToHostInviteCtx(ctx, f.writer, f.l1, f.xu, "accepted")
				if err == nil && matched {
					return errors.New("accepted an invitation the erasure deleted")
				}
				return err
			},
			waitsA: true, okA: loNil, waitsB: true,
			holdB: func(ctx context.Context, f *loFix) func() error {
				return loTx(ctx, f, func(tx *sql.Tx) error {
					if err := setLinkLockTimeout(tx); err != nil {
						return err
					}
					_, err := respondToHostInviteLocked(tx, f.l1, f.xu, "accepted")
					return err
				})
			},
		},
		{
			name:  "W22 co-host decline",
			setup: func(f *loFix) { f.withAccount() },
			writerA: func(ctx context.Context, f *loFix) error {
				matched, err := RespondToHostInviteCtx(ctx, f.writer, f.l1, f.xu, "declined")
				if err == nil && matched {
					return errors.New("declined an invitation the erasure deleted")
				}
				return err
			},
			waitsA: true, okA: loNil, waitsB: true,
			holdB: func(ctx context.Context, f *loFix) func() error {
				return loTx(ctx, f, func(tx *sql.Tx) error {
					if err := setLinkLockTimeout(tx); err != nil {
						return err
					}
					_, err := respondToHostInviteLocked(tx, f.l1, f.xu, "declined")
					return err
				})
			},
		},
		{
			name:  "W23 co-host leave",
			setup: func(f *loFix) { f.withAccount() },
			writerA: func(ctx context.Context, f *loFix) error {
				_, err := RemoveLinkHostCtx(ctx, f.writer, f.l1, f.xu)
				return err
			},
			waitsA: true, okA: loNil, waitsB: true,
			holdB: func(ctx context.Context, f *loFix) func() error {
				return loTx(ctx, f, func(tx *sql.Tx) error {
					if err := setLinkLockTimeout(tx); err != nil {
						return err
					}
					_, err := removeLinkHostLocked(tx, f.l1, f.xu)
					return err
				})
			},
		},
		{
			name: "W24 co-host PATCH",
			writerA: func(ctx context.Context, f *loFix) error {
				_, _, _, err := UpdateSchedulingLinkReportingCoHostsCtx(ctx, f.writer, f.l1, f.link1(), f.owner, []string{loX})
				return err
			},
			waitsA: true, okA: loNil, waitsB: true,
			holdB: func(ctx context.Context, f *loFix) func() error {
				l := f.link1()
				return loSeamHold(f, func(reached chan<- struct{}, gate <-chan struct{}) {
					once := loOnce(reached, gate)
					InstallAfterLinkPatchUpdate(f.t, func(uuid.UUID) error { once(); return nil })
				}, func() error {
					_, _, _, err := UpdateSchedulingLinkReportingCoHostsCtx(ctx, f.writer, f.l1, l, f.owner, []string{loX})
					return err
				})
			},
		},
		{
			name: "W25 link delete",
			writerA: func(ctx context.Context, f *loFix) error {
				ok, err := DeleteSchedulingLinkCtx(ctx, f.writer, f.l1)
				if err == nil && !ok {
					return errors.New("the delete did not delete")
				}
				return err
			},
			waitsA: true, okA: loNil, waitsB: true,
			holdB: func(ctx context.Context, f *loFix) func() error {
				return loTx(ctx, f, func(tx *sql.Tx) error {
					if err := setLinkLockTimeout(tx); err != nil {
						return err
					}
					_, err := DeleteSchedulingLink(tx, f.l1)
					return err
				})
			},
		},
		{
			name: "W26 link create with co-hosts, fresh key",
			writerA: func(ctx context.Context, f *loFix) error {
				d := sha256.Sum256([]byte("fresh"))
				_, _, _, err := CreateSchedulingLinkWithCoHostsKeyedCtx(ctx, f.writer, f.newLink("fresh"), []string{loX}, "lo-fresh-create-0001", d[:])
				return err
			},
			waitsA: false, okA: loNil, waitsB: false,
			holdB: func(ctx context.Context, f *loFix) func() error {
				d := sha256.Sum256([]byte("fresh"))
				return loSeamHold(f, func(reached chan<- struct{}, gate <-chan struct{}) {
					once := loOnce(reached, gate)
					InstallBeforeLinkCreateCommit(f.t, func(uuid.UUID) error { once(); return nil })
				}, func() error {
					_, _, _, err := CreateSchedulingLinkWithCoHostsKeyedCtx(ctx, f.writer, f.newLink("fresh"), []string{loX}, "lo-fresh-create-0001", d[:])
					return err
				})
			},
		},
		{
			// Order B emulated: ClearExpiredLinkCreateKeyData opens its own
			// transaction on a DBTX; its (L-2) statement runs in an outer
			// transaction kept open.
			name:  "W27 keyed-create 24 h cleanup",
			setup: func(f *loFix) { f.expiredKeys() },
			writerA: func(ctx context.Context, f *loFix) error {
				_, err := ClearExpiredLinkCreateKeyData(ctx, f.writer)
				return err
			},
			waitsA: true, okA: loNil, waitsB: true,
			holdB: func(ctx context.Context, f *loFix) func() error {
				return loTx(ctx, f, func(tx *sql.Tx) error { _, err := tx.Exec(loCleanupSQL, LinkCreateKeyWindow.Seconds()); return err })
			},
		},
		{
			name: "W28 PAC-40 booker erasure",
			writerA: func(ctx context.Context, f *loFix) error {
				n, err := EraseLinkBookers(ctx, f.writer, f.l2, time.Now())
				if err == nil && n != 1 {
					return fmt.Errorf("the job erased %d bookings, want 1 (X's was already erased)", n)
				}
				return err
			},
			waitsA: true, okA: loNil, waitsB: true,
			holdB: func(ctx context.Context, f *loFix) func() error {
				return loSeamHold(f, func(reached chan<- struct{}, gate <-chan struct{}) {
					once := loOnce(reached, gate)
					InstallAfterBookerErasureRecheck(f.t, func(*sql.Tx, uuid.UUID) error { once(); return nil })
				}, func() error { _, err := EraseLinkBookers(ctx, f.writer, f.l2, time.Now()); return err })
			},
		},
	}
}

// TestPAC134_AC11b_LockOrder runs every row in both orders.
func TestPAC134_AC11b_LockOrder(t *testing.T) {
	tpl := newLoTemplate(t)
	for _, c := range loCases() {
		c := c
		t.Run(c.name+"/tool first", func(t *testing.T) {
			f := tpl.fix(t)
			if c.setup != nil {
				c.setup(f)
			}
			ctx := context.Background()
			held, release := loHold(t, "erase_t8")
			tool, res := f.startTool()
			select {
			case <-held:
			case <-tool.done:
				t.Fatalf("the tool ended before its hold: %v", tool.err)
			case <-time.After(15 * time.Second):
				t.Fatal("the tool never reached its hold")
			}
			w := loGo(func() error { return c.writerA(ctx, f) })
			if c.waitsA {
				f.waitFor("lo-writer", w.done, "the writer")
			} else {
				select {
				case <-w.done:
				case <-time.After(8 * time.Second):
					t.Fatal("the writer did not complete while the tool was held")
				}
				if f.waiting("lo-writer") {
					t.Error("the writer waits")
				}
			}
			release()
			if err := tool.wait(t, "the tool"); err != nil {
				t.Fatalf("the tool: %v", err)
			}
			werr := w.wait(t, "the writer")
			if loLockError(werr) {
				t.Fatalf("the writer: %v", werr)
			}
			if err := c.okA(f, werr); err != nil {
				t.Fatalf("the writer: %v", err)
			}
			if res.AuditID == uuid.Nil {
				t.Error("the tool committed no record")
			}
		})
		t.Run(c.name+"/writer first", func(t *testing.T) {
			f := tpl.fix(t)
			if c.setup != nil {
				c.setup(f)
			}
			ctx := context.Background()
			finish := c.holdB(ctx, f)
			tool, res := f.startTool()
			if c.waitsB {
				f.waitFor(EraseAddressApp, tool.done, "the tool")
			} else {
				select {
				case <-tool.done:
				case <-time.After(10 * time.Second):
					t.Fatal("the tool did not complete while the writer held its locks")
				}
			}
			werr := finish()
			if werr != nil {
				t.Fatalf("the writer: %v", werr)
			}
			if err := tool.wait(t, "the tool"); err != nil {
				t.Fatalf("the tool: %v", err)
			}
			if res.AuditID == uuid.Nil {
				t.Error("the tool committed no record")
			}
		})
	}
}

// W19 and W26, keyed retries of what the tool is erasing (contract md §6.3,
// revision 2 note 1). Observed: the retry decides its key on its MVCC
// snapshot, so while the tool is held it does NOT wait and replays the
// committed (not yet erased) row; once the tool committed, the same retry
// is key reused. The create transaction itself, given the key being cleared
// (the R3 race path), does wait on the key's primary-key slot and ends in a
// key conflict.
func TestPAC134_AC11b_KeyedRetries(t *testing.T) {
	tpl := newLoTemplate(t)
	t.Run("W19 keyed booking retry", func(t *testing.T) {
		f := tpl.fix(t)
		ctx := context.Background()
		retry := f.storedBooking()
		held, release := loHold(t, "erase_t8")
		tool, _ := f.startTool()
		<-held
		w := loGo(func() error {
			b, err := f.saveBooking(ctx, retry)
			if err == nil && !b.Replayed {
				return errors.New("the retry wrote a booking")
			}
			return err
		})
		select {
		case <-w.done:
		case <-time.After(8 * time.Second):
			t.Fatal("the keyed retry waited on the held tool")
		}
		if err := w.err; err != nil {
			t.Fatalf("retry while held: %v", err)
		}
		release()
		if err := tool.wait(t, "the tool"); err != nil {
			t.Fatal(err)
		}
		// After the erasure the stored row no longer has the attempt's
		// details: key reused, nothing written.
		if _, err := f.saveBooking(ctx, retry); !errors.Is(err, ErrIdempotencyKeyReused) {
			t.Fatalf("retry after the erasure: %v", err)
		}
		if n := f.count(`SELECT count(*) FROM bookings WHERE link_id = $1`, f.l1); n != 1 {
			t.Fatalf("%d bookings on L1", n)
		}
	})
	t.Run("W26 keyed create retry", func(t *testing.T) {
		f := tpl.fix(t)
		ctx := context.Background()
		held, release := loHold(t, "erase_t8")
		tool, _ := f.startTool()
		<-held
		res, err := ResolveLinkCreateKeyCtx(ctx, f.writer, f.owner, &LinkCreateAttempt{Key: f.createKey, Digest: f.createDigest})
		if err != nil || res.Outcome != LinkCreateKeyReplay {
			t.Fatalf("resolution while held: %v %v", res, err)
		}
		// The create transaction with the same key (the R3 race path) waits
		// on the key slot the tool holds, then conflicts.
		w := loGo(func() error {
			_, _, _, err := CreateSchedulingLinkWithCoHostsKeyedCtx(ctx, f.writer, f.newLink("retry"), []string{loX}, f.createKey, f.createDigest)
			return err
		})
		f.waitFor("lo-writer", w.done, "the keyed create")
		release()
		if err := tool.wait(t, "the tool"); err != nil {
			t.Fatal(err)
		}
		if err := w.wait(t, "the keyed create"); !errors.Is(err, ErrLinkCreateConflict) {
			t.Fatalf("keyed create: %v, want a key conflict", err)
		}
		res, err = ResolveLinkCreateKeyCtx(ctx, f.writer, f.owner, &LinkCreateAttempt{Key: f.createKey, Digest: f.createDigest})
		if err != nil || res.Outcome != LinkCreateKeyReused {
			t.Fatalf("resolution after the erasure: %v %v", res, err)
		}
	})
}

// W29: two erasures of X concurrently, both start orders are the same row:
// the second waits on the first, both succeed, the erased counts add up to
// what a dry run counted, and nothing matches afterwards.
func TestPAC134_AC11b_W29_TwoRuns(t *testing.T) {
	tpl := newLoTemplate(t)
	f := tpl.fix(t)
	dry := f.opts()
	dry.DryRun = true
	want, err := EraseAddress(context.Background(), f.tool, dry)
	if err != nil {
		t.Fatal(err)
	}
	held, release := loHold(t, "erase_t8")
	first, res1 := f.startTool()
	<-held
	second, res2 := f.startTool()
	f.waitFor(EraseAddressApp, second.done, "the second run")
	release()
	if err := first.wait(t, "the first run"); err != nil {
		t.Fatal(err)
	}
	if err := second.wait(t, "the second run"); err != nil {
		t.Fatal(err)
	}
	for i, p := range want.Places {
		if got := res1.Places[i].Erased + res2.Places[i].Erased; got != p.Erased {
			t.Errorf("%s: erased %d + %d, want %d", p.Name, res1.Places[i].Erased, res2.Places[i].Erased, p.Erased)
		}
	}
	again, err := EraseAddress(context.Background(), f.tool, dry)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range again.Places {
		if p.Erased != 0 {
			t.Errorf("%s: %d left", p.Name, p.Erased)
		}
	}
}

// L-3 (contract md §6.2): confirm's answers are unchanged for empty,
// duplicate, unknown and known entries; a confirm racing an erasure of the
// entry waits for it and answers skipped, never a foreign-key 500.
func TestPAC134_L3_ConfirmOrderAndAnswers(t *testing.T) {
	tpl := newLoTemplate(t)
	t.Run("answers", func(t *testing.T) {
		f := tpl.fix(t)
		f.exec(`INSERT INTO manager_team_members (manager_user_id, member_email) VALUES ($1, 'b@x.example'), ($1, 'a@x.example')`, f.owner)
		a, s, total, err := ConfirmManagerTeamMembers(f.db, f.owner, f.tb,
			[]string{"", "  B@X.example ", "a@x.example", "a@x.example", "unknown@x.example", "   "})
		if err != nil || a != 2 || s != 4 || total != 2 {
			t.Fatalf("assigned %d skipped %d total %d err %v; want 2, 4, 2", a, s, total, err)
		}
		a, s, total, err = ConfirmManagerTeamMembers(f.db, f.owner, f.tb, []string{"a@x.example", "b@x.example"})
		if err != nil || a != 0 || s != 2 || total != 2 {
			t.Fatalf("repeat: assigned %d skipped %d total %d err %v", a, s, total, err)
		}
	})
	t.Run("racing an erasure", func(t *testing.T) {
		f := tpl.fix(t)
		held, release := loHold(t, "erase_t3")
		tool, _ := f.startTool()
		<-held
		var a, s int
		w := loGo(func() error {
			var err error
			a, s, _, err = ConfirmManagerTeamMembersCtx(context.Background(), f.writer, f.owner, f.tb, []string{loX})
			return err
		})
		f.waitFor("lo-writer", w.done, "the confirm")
		release()
		if err := tool.wait(t, "the tool"); err != nil {
			t.Fatal(err)
		}
		if err := w.wait(t, "the confirm"); err != nil || a != 0 || s != 1 {
			t.Fatalf("confirm: assigned %d skipped %d err %v; want skipped, no error", a, s, err)
		}
		if n := f.count(`SELECT count(*) FROM manager_team_member_assignments WHERE team_id = $1`, f.tb); n != 0 {
			t.Fatalf("%d assignments on TB", n)
		}
	})
}
