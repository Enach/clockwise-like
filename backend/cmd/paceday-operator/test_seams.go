package main

import (
	"context"
	"database/sql"
	"sync/atomic"

	"github.com/Enach/paceday/backend/storage"
)

// The operator's deterministic test seams, on the PAC-80 pattern of
// storage/test_seams.go: each is held in a synchronised holder, not an
// exported variable, so production code cannot set one by assignment, and a
// test installs one through its Install function, which clears it when the
// test ends. Production installs none: every seam is a no-op until a test
// installs it.

// TB is the part of testing.TB the Install functions use.
type TB interface {
	Helper()
	Cleanup(func())
}

// seam holds one hook of function type F.
type seam[F any] struct{ p atomic.Pointer[F] }

func (s *seam[F]) get() (F, bool) {
	if p := s.p.Load(); p != nil {
		return *p, true
	}
	var zero F
	return zero, false
}

func (s *seam[F]) install(tb TB, f F) (uninstall func()) {
	tb.Helper()
	p := &f
	s.p.Store(p)
	uninstall = func() { s.p.CompareAndSwap(p, nil) }
	tb.Cleanup(uninstall)
	return uninstall
}

// PAC-140 (docs/factory/silent-failures.md SF-108): the encryption
// commands' sealed_count flush and the count of values left under a retired
// data key. For the flush, a non-nil error from the hook replaces the
// flush's error after the real flush ran. For the count, the hook runs
// before the real count (so a test can change what it reads), and a non-nil
// error is returned in place of the count. Either way the command takes its
// production error path.
var (
	flushSealCountsHook  seam[func(sub string) error]
	countValuesUnderHook seam[func(dek int32) error]
	auditTableProbeHook  seam[func() error]
)

// InstallFlushSealCounts installs the sealed_count flush seam; sub is the
// subcommand ("convert" or "rotate-data-key").
func InstallFlushSealCounts(tb TB, fn func(sub string) error) (uninstall func()) {
	tb.Helper()
	return flushSealCountsHook.install(tb, fn)
}

// InstallCountValuesUnder installs the seam of rotate-data-key's count of
// the values left under the retired data key.
func InstallCountValuesUnder(tb TB, fn func(dek int32) error) (uninstall func()) {
	tb.Helper()
	return countValuesUnderHook.install(tb, fn)
}

// flushSealCounts is storage.FlushSealCounts behind its seam.
func flushSealCounts(ctx context.Context, db *sql.DB, sub string, counts map[int32]int64) error {
	err := storage.FlushSealCounts(ctx, db, counts)
	if hook, ok := flushSealCountsHook.get(); ok {
		if hookErr := hook(sub); hookErr != nil {
			return hookErr
		}
	}
	return err
}

// countValuesUnder is storage.CountValuesUnder behind its seam.
func countValuesUnder(ctx context.Context, db *sql.DB, dek int32) (int64, error) {
	if hook, ok := countValuesUnderHook.get(); ok {
		if err := hook(dek); err != nil {
			return 0, err
		}
	}
	return storage.CountValuesUnder(ctx, db, dek)
}

// InstallAuditTableProbe installs the seam of the probe for operator_audit
// on a database below the encryption migration. A non-nil error from the
// hook replaces the probe's error after the real probe ran.
func InstallAuditTableProbe(tb TB, fn func() error) (uninstall func()) {
	tb.Helper()
	return auditTableProbeHook.install(tb, fn)
}

// auditTableExists reports whether operator_audit exists, behind its seam.
func auditTableExists(ctx context.Context, db *sql.DB) (bool, error) {
	var ok bool
	err := db.QueryRowContext(ctx, `SELECT to_regclass('public.operator_audit') IS NOT NULL`).Scan(&ok)
	if hook, hooked := auditTableProbeHook.get(); hooked {
		if hookErr := hook(); hookErr != nil {
			return false, hookErr
		}
	}
	return ok, err
}
