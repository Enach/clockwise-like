package storage

import (
	"context"
	"database/sql"
	"sync/atomic"

	"github.com/google/uuid"
)

// PAC-80 item 1 (spec docs/specs/PAC-80.md AC-1..AC-3, D-1): the storage
// package's deterministic test seams. Each is held in a synchronised holder,
// not in an exported variable: production code cannot set one by plain
// assignment, and installing, clearing and firing a seam from different
// goroutines (a test's cleanup racing background work it spawned) is free of
// data races. Tests in any package install one through its Install function,
// which needs the test's testing.TB and clears the seam when the test ends,
// so no test leaves a seam installed for the next one. Production installs
// none: every seam is a no-op until a test installs it.

// TB is the part of testing.TB the Install functions use. *testing.T and
// *testing.B satisfy it; storage does not import package testing.
type TB interface {
	Helper()
	Cleanup(func())
}

// seam holds one hook of function type F.
type seam[F any] struct{ p atomic.Pointer[F] }

// get returns the installed hook, if any.
func (s *seam[F]) get() (F, bool) {
	if p := s.p.Load(); p != nil {
		return *p, true
	}
	var zero F
	return zero, false
}

// install sets f and returns the function that clears it; tb's cleanup
// clears it too. Clearing is idempotent and never removes a hook installed
// after this one.
func (s *seam[F]) install(tb TB, f F) (uninstall func()) {
	tb.Helper()
	p := &f
	s.p.Store(p)
	uninstall = func() { s.p.CompareAndSwap(p, nil) }
	tb.Cleanup(uninstall)
	return uninstall
}

var (
	guardedCallHook           seam[func(ctx context.Context)]
	afterBookerErasureRecheck seam[func(tx *sql.Tx, linkID uuid.UUID) error]
	afterBookerErasureUpdate  seam[func(linkID uuid.UUID) error]
	afterLinkPatchUpdate      seam[func(linkID uuid.UUID) error]
	afterStepupDenialUpsert   seam[func(userID uuid.UUID)]
	afterBookingLocks         seam[func(b *Booking)]
	afterBookingInsert        seam[func(b *Booking) error]
)

// InstallGuardedCallHook installs the mid-request seam (PAC-71 review of
// #242): fn runs before every in-request re-check of a request with a session
// (WithSessionGeneration and the scheduling-link write transaction), so a
// test can commit a binding between a handler's first and second storage
// call, or after it spawned background work. It stays installed until the
// returned function is called or the test ends.
func InstallGuardedCallHook(tb TB, fn func(ctx context.Context)) (uninstall func()) {
	tb.Helper()
	return guardedCallHook.install(tb, fn)
}

// fireGuardedCallHook runs the mid-request seam, if a test installed one.
func fireGuardedCallHook(ctx context.Context) {
	if fn, ok := guardedCallHook.get(); ok {
		fn(ctx)
	}
}

// InstallAfterBookerErasureRecheck installs the seam that runs inside a link's
// erasure transaction after the step-2 re-check passed and before the erasing
// UPDATE, on that transaction (review of #269, K4-3), so a test can make a
// not-yet-due booking visible to the UPDATE and prove that the UPDATE's own
// due rule leaves it alone.
func InstallAfterBookerErasureRecheck(tb TB, fn func(tx *sql.Tx, linkID uuid.UUID) error) (uninstall func()) {
	tb.Helper()
	return afterBookerErasureRecheck.install(tb, fn)
}

// InstallAfterBookerErasureUpdate installs the seam that runs inside a link's
// erasure transaction after the erasing UPDATE and before the audit insert
// (PAC-40 AC-33). A non-nil error rolls the whole link back.
func InstallAfterBookerErasureUpdate(tb TB, fn func(linkID uuid.UUID) error) (uninstall func()) {
	tb.Helper()
	return afterBookerErasureUpdate.install(tb, fn)
}

// InstallAfterLinkPatchUpdate installs the seam that runs inside a PATCH's
// transaction after the link UPDATE matched and before the co-host steps
// (PAC-101 contract §5). A non-nil error rolls the whole PATCH back.
func InstallAfterLinkPatchUpdate(tb TB, fn func(linkID uuid.UUID) error) (uninstall func()) {
	tb.Helper()
	return afterLinkPatchUpdate.install(tb, fn)
}

// InstallAfterStepupDenialUpsert installs the seam that runs inside a denied
// step-up record's transaction after the window upsert and before the event
// insert (PAC-75 AC-32), so a test can hold the window row while the same
// user is deleted and prove the lock order (contract §3.5).
func InstallAfterStepupDenialUpsert(tb TB, fn func(userID uuid.UUID)) (uninstall func()) {
	tb.Helper()
	return afterStepupDenialUpsert.install(tb, fn)
}

// fireAfterStepupDenialUpsert runs the PAC-75 seam, if a test installed one.
func fireAfterStepupDenialUpsert(userID uuid.UUID) {
	if fn, ok := afterStepupDenialUpsert.get(); ok {
		fn(userID)
	}
}

// InstallAfterBookingLocks installs the seam that runs inside the booking
// save after every advisory key is held and before the re-checks (PAC-82
// contract §6), so a test can hold one save under its keys and observe a
// concurrent one.
func InstallAfterBookingLocks(tb TB, fn func(b *Booking)) (uninstall func()) {
	tb.Helper()
	return afterBookingLocks.install(tb, fn)
}

// InstallAfterBookingInsert installs the seam that runs inside the booking
// save after the insert and before COMMIT (PAC-82 contract §6). A non-nil
// error rolls the save back and is returned.
func InstallAfterBookingInsert(tb TB, fn func(b *Booking) error) (uninstall func()) {
	tb.Helper()
	return afterBookingInsert.install(tb, fn)
}

// PAC-116 seams (contract 6: test-only, unexported, installed through an
// Install function that takes the test's testing.TB; no flag, environment
// variable or configuration reaches them).
var (
	afterMigrateStateRead   seam[func(caller string)]
	afterMigrateCheck       seam[func(caller string)]
	insideMigrationFile     seam[func(version uint)]
	afterHeartbeatWrite     seam[func(inst Instance)]
	afterMigrateLockRefused seam[func(caller string)]
	afterProbeDirtyRead     seam[func()]
)

// InstallAfterMigrateLockRefused installs the seam that runs inside
// MigrateWhenStopped when pg_try_advisory_lock failed, before the run
// returns and closes its paceday-migrate: connection, so a test can keep
// the loser's connection open across the winner's serving check (PAC-116
// challenge S4, AC-21).
func InstallAfterMigrateLockRefused(tb TB, fn func(caller string)) (uninstall func()) {
	tb.Helper()
	return afterMigrateLockRefused.install(tb, fn)
}

// InstallAfterProbeDirtyRead installs the seam that runs inside
// ProbeVersionState after a first lock-free read found the version dirty
// with no migrating holder, before its one re-read (PAC-116 contract 2
// "Read order"), so a test can finish the migration in between.
func InstallAfterProbeDirtyRead(tb TB, fn func()) (uninstall func()) {
	tb.Helper()
	return afterProbeDirtyRead.install(tb, fn)
}

// InstallAfterMigrateStateRead installs the seam that runs inside
// MigrateWhenStopped after step 1 found something to decide (pending, or
// dirty with no migrating holder) and before it tries the maintenance lock,
// so a test can hold two migrators at a barrier there (PAC-116 AC-10, AC-21).
func InstallAfterMigrateStateRead(tb TB, fn func(caller string)) (uninstall func()) {
	tb.Helper()
	return afterMigrateStateRead.install(tb, fn)
}

// InstallAfterMigrateCheck installs the seam that runs inside
// MigrateWhenStopped after the serving check passed (step 5), with the
// maintenance lock held, and before any migration is applied (PAC-116 AC-8,
// AC-11, AC-23).
func InstallAfterMigrateCheck(tb TB, fn func(caller string)) (uninstall func()) {
	tb.Helper()
	return afterMigrateCheck.install(tb, fn)
}

// InstallInsideMigrationFile installs the seam that runs when golang-migrate
// reads an up file's body during MigrateWhenStopped's step 6: the version is
// then marked dirty and the file has not run yet (PAC-116 AC-19).
func InstallInsideMigrationFile(tb TB, fn func(version uint)) (uninstall func()) {
	tb.Helper()
	return insideMigrationFile.install(tb, fn)
}

// InstallAfterHeartbeatWrite installs the seam that runs in LoadKeyring after
// the backend wrote its first heartbeat and before it reads the maintenance
// lock's holder (PAC-116 AC-9).
func InstallAfterHeartbeatWrite(tb TB, fn func(inst Instance)) (uninstall func()) {
	tb.Helper()
	return afterHeartbeatWrite.install(tb, fn)
}

func fireMigrateHook(s *seam[func(caller string)], caller string) {
	if fn, ok := s.get(); ok {
		fn(caller)
	}
}

// PAC-112 seams (contract md §11): the scheduling-link co-host list on create
// and PATCH. Each fires inside the write's transaction; a non-nil error fails
// that step, and the caller rolls the whole create or PATCH back.
var (
	coHostLookup    seam[func(email string) error]
	coHostWrite     seam[func(linkID, userID uuid.UUID) error]
	createOwnerHost seam[func(linkID uuid.UUID) error]
)

// InstallCoHostLookup installs the lookup seam: fn runs once per lookup of
// the co-host list (one read of an address's carriers, contract md §3.3),
// before the read, with the address being looked up (trimmed of ECMA-262
// \s). Recording the addresses shows which were read and in which order; a
// non-nil error fails that lookup (AC-10, AC-21).
func InstallCoHostLookup(tb TB, fn func(email string) error) (uninstall func()) {
	tb.Helper()
	return coHostLookup.install(tb, fn)
}

// InstallCoHostWrite installs the seam that runs before each co-host
// invitation insert of the co-host list, with the link and the invitee; a
// non-nil error fails that write (AC-20).
func InstallCoHostWrite(tb TB, fn func(linkID, userID uuid.UUID) error) (uninstall func()) {
	tb.Helper()
	return coHostWrite.install(tb, fn)
}

// InstallCreateOwnerHost installs the seam that runs inside a link create
// before the owner's host-row insert; a non-nil error fails that write
// (AC-22).
func InstallCreateOwnerHost(tb TB, fn func(linkID uuid.UUID) error) (uninstall func()) {
	tb.Helper()
	return createOwnerHost.install(tb, fn)
}

// fireSeamErr runs an error-returning seam taking one argument, if installed.
func fireSeamErr[A any](s *seam[func(A) error], a A) error {
	if fn, ok := s.get(); ok {
		return fn(a)
	}
	return nil
}

// fireSeamErr2 runs an error-returning seam taking two arguments, if installed.
func fireSeamErr2[A, B any](s *seam[func(A, B) error], a A, b B) error {
	if fn, ok := s.get(); ok {
		return fn(a, b)
	}
	return nil
}

// PAC-119 seams (contract md §6): the keyed link create. The two resolution
// seams receive the request context, so a test can tell two same-key
// requests apart by a value it put on one request's context (challenge m-5).
var (
	afterLinkCreateKeyRead     seam[func(ctx context.Context, key string)]
	afterLinkCreateKeyResolved seam[func(ctx context.Context, key string)]
	beforeLinkCreateCommit     seam[func(linkID uuid.UUID) error]
)

// InstallAfterLinkCreateKeyRead installs the seam that runs inside the
// resolution transaction after the key read and before the second session
// re-check (spec AC-10's second position).
func InstallAfterLinkCreateKeyRead(tb TB, fn func(ctx context.Context, key string)) (uninstall func()) {
	tb.Helper()
	return afterLinkCreateKeyRead.install(tb, fn)
}

// InstallAfterLinkCreateKeyResolved installs the seam that runs after a
// resolution that found nothing stored has committed, before it returns: for
// the first resolution of a request, after key resolution and before the slug
// check (spec AC-30).
func InstallAfterLinkCreateKeyResolved(tb TB, fn func(ctx context.Context, key string)) (uninstall func()) {
	tb.Helper()
	return afterLinkCreateKeyResolved.install(tb, fn)
}

// InstallBeforeLinkCreateCommit installs the seam that runs inside a link
// create's transaction after its last write and before COMMIT (spec AC-7,
// AC-29): the link, its slug and, for a keyed create, its key are held
// uncommitted while fn runs. A non-nil error rolls the whole create back.
func InstallBeforeLinkCreateCommit(tb TB, fn func(linkID uuid.UUID) error) (uninstall func()) {
	tb.Helper()
	return beforeLinkCreateCommit.install(tb, fn)
}

// PAC-120 seams (contract md §5): the single co-host invitation.
// confirmedHolderLookup and linkReadByID fire for every caller of
// ConfirmedHolder and GetSchedulingLinkByID; a test filters on the address or
// link id it owns.
var (
	afterLinkInviteLock   seam[func(ctx context.Context, linkID uuid.UUID) error]
	confirmedHolderLookup seam[func(email string) error]
	linkReadByID          seam[func(id uuid.UUID) error]
)

// InstallAfterLinkInviteLock installs the seam that runs inside the single
// invitation's transaction right after the link is locked FOR NO KEY UPDATE
// and its owner read, before the existing-row read. It receives the request
// context, so a test can hold one of two concurrent invitations. A non-nil
// error rolls the invitation back and is returned.
func InstallAfterLinkInviteLock(tb TB, fn func(ctx context.Context, linkID uuid.UUID) error) (uninstall func()) {
	tb.Helper()
	return afterLinkInviteLock.install(tb, fn)
}

// InstallConfirmedHolderLookup installs the seam that runs at the start of
// ConfirmedHolder with the address as given; a non-nil error is returned as
// the lookup's error without querying.
func InstallConfirmedHolderLookup(tb TB, fn func(email string) error) (uninstall func()) {
	tb.Helper()
	return confirmedHolderLookup.install(tb, fn)
}

// InstallLinkReadByID installs the seam that runs at the start of
// GetSchedulingLinkByID; a non-nil error is returned without querying.
func InstallLinkReadByID(tb TB, fn func(id uuid.UUID) error) (uninstall func()) {
	tb.Helper()
	return linkReadByID.install(tb, fn)
}

// PAC-122 batch 0 seams (docs/factory/silent-failures.md SF-001, SF-002).
// teamMemberRead fires at the start of GetTeamMember for every caller, and
// inviteSweep at the start of ExpireOldInvites; a test filters on the ids it
// owns where it can.
var (
	teamMemberRead seam[func(teamID, userID uuid.UUID) error]
	inviteSweep    seam[func() error]
)

// InstallTeamMemberRead installs the seam that runs at the start of
// GetTeamMember; a non-nil error is returned as the read's error without
// querying.
func InstallTeamMemberRead(tb TB, fn func(teamID, userID uuid.UUID) error) (uninstall func()) {
	tb.Helper()
	return teamMemberRead.install(tb, fn)
}

// InstallInviteSweep installs the seam that runs at the start of
// ExpireOldInvites; a non-nil error is returned as the sweep's error without
// writing, so the sweep fails while reads still work.
func InstallInviteSweep(tb TB, fn func() error) (uninstall func()) {
	tb.Helper()
	return inviteSweep.install(tb, fn)
}

// PAC-129 (contract §11): the team-write seams.
var (
	afterTeamLock             seam[func(op string, teamID uuid.UUID) error]
	afterAcceptMembershipStep seam[func(inviteID uuid.UUID) error]
	teamMembersListRead       seam[func(teamID uuid.UUID) error]
)

// InstallAfterTeamLock installs the seam that runs inside every PAC-129
// membership write right after the team row is locked (op is "add",
// "remove", "invite", "accept" or "manager-remove"), so a test can force the
// order of two racing writes (AC-23, AC-24, AC-30). A non-nil error rolls the
// write back.
func InstallAfterTeamLock(tb TB, fn func(op string, teamID uuid.UUID) error) (uninstall func()) {
	tb.Helper()
	return afterTeamLock.install(tb, fn)
}

// InstallAfterAcceptMembershipStep installs the seam that runs inside the
// acceptance transaction after the membership insert and before the status
// update (AC-19). A non-nil error rolls the acceptance back.
func InstallAfterAcceptMembershipStep(tb TB, fn func(inviteID uuid.UUID) error) (uninstall func()) {
	tb.Helper()
	return afterAcceptMembershipStep.install(tb, fn)
}

// InstallTeamMembersListRead installs the seam that runs before
// ListTeamMembers and ListAddedMembers read; a non-nil error is the read's
// error (AC-6).
func InstallTeamMembersListRead(tb TB, fn func(teamID uuid.UUID) error) (uninstall func()) {
	tb.Helper()
	return teamMembersListRead.install(tb, fn)
}

// PAC-132 seams (docs/factory/silent-failures.md SF-029): the strict roster
// read inside a link create's or PATCH's write transaction. linkRosterHosts
// fires before the host-rows read and linkRosterUser before each host's user
// record read; a non-nil error is that read's error, without querying, and
// rolls the write back. A test filters on the link or user it owns.
var (
	linkRosterHosts seam[func(linkID uuid.UUID) error]
	linkRosterUser  seam[func(linkID, userID uuid.UUID) error]
)

// InstallLinkRosterHosts installs the seam that runs before the in-write
// read of a link's host rows; a non-nil error fails that read.
func InstallLinkRosterHosts(tb TB, fn func(linkID uuid.UUID) error) (uninstall func()) {
	tb.Helper()
	return linkRosterHosts.install(tb, fn)
}

// InstallLinkRosterUser installs the seam that runs before the in-write read
// of one host's user record; a non-nil error fails that read.
func InstallLinkRosterUser(tb TB, fn func(linkID, userID uuid.UUID) error) (uninstall func()) {
	tb.Helper()
	return linkRosterUser.install(tb, fn)
}

// PAC-122 batch 4 seams (docs/factory/silent-failures.md SF-009, SF-076,
// SF-077). Each fires at the start of its read or write for every caller; a
// non-nil error is that call's error, without querying. A test filters on
// the ids it owns.
var (
	teamRead           seam[func(teamID uuid.UUID) error]
	userRead           seam[func(userID uuid.UUID) error]
	userProfileUpsert  seam[func(userID uuid.UUID) error]
	managerRosterCount seam[func(managerID uuid.UUID) error]
)

// InstallTeamRead installs the seam that runs at the start of GetTeam.
func InstallTeamRead(tb TB, fn func(teamID uuid.UUID) error) (uninstall func()) {
	tb.Helper()
	return teamRead.install(tb, fn)
}

// InstallUserRead installs the seam that runs at the start of GetUserByID.
func InstallUserRead(tb TB, fn func(userID uuid.UUID) error) (uninstall func()) {
	tb.Helper()
	return userRead.install(tb, fn)
}

// InstallUserProfileUpsert installs the seam that runs at the start of
// UpsertUserProfile.
func InstallUserProfileUpsert(tb TB, fn func(userID uuid.UUID) error) (uninstall func()) {
	tb.Helper()
	return userProfileUpsert.install(tb, fn)
}

// InstallManagerRosterCount installs the seam that runs at the start of
// CountManagerTeamMembers.
func InstallManagerRosterCount(tb TB, fn func(managerID uuid.UUID) error) (uninstall func()) {
	tb.Helper()
	return managerRosterCount.install(tb, fn)
}

// callerRoleRead fires at the start of the in-transaction owner/membership
// read of every PAC-129 team write (callerRole); a non-nil error is that
// read's error, without querying (PAC-122 SF-074).
var callerRoleRead seam[func(teamID, userID uuid.UUID) error]

// InstallCallerRoleRead installs the callerRole seam.
func InstallCallerRoleRead(tb TB, fn func(teamID, userID uuid.UUID) error) (uninstall func()) {
	tb.Helper()
	return callerRoleRead.install(tb, fn)
}

// focusBlocksRead fires at the start of ListFocusBlocksForUserWeek(Ctx) for every
// caller; a non-nil error is that read's error, without querying (PAC-141:
// the smart scheduler's and team availability's focus-block reads). A test
// filters on the user ids it owns.
var focusBlocksRead seam[func(userID uuid.UUID) error]

// InstallFocusBlocksRead installs the focus-block read seam.
func InstallFocusBlocksRead(tb TB, fn func(userID uuid.UUID) error) (uninstall func()) {
	tb.Helper()
	return focusBlocksRead.install(tb, fn)
}
