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
