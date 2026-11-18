package calendar

import "sync/atomic"

// PAC-133 §8: the calendar package's deterministic test seams, under the
// PAC-80 rules of storage/test_seams.go. Each is held in a synchronised
// holder, never in an exported or package-level func variable: production
// code cannot set one by assignment, and production installs none. A test in
// any package installs one through its Install function, which needs the
// test's TB and clears the seam when the test ends.

// TB is the part of testing.TB the Install functions use. *testing.T and
// *testing.B satisfy it; calendar does not import package testing.
type TB interface {
	Helper()
	Cleanup(func())
}

// seam holds one value of type T.
type seam[T any] struct{ p atomic.Pointer[T] }

func (s *seam[T]) get() (T, bool) {
	if p := s.p.Load(); p != nil {
		return *p, true
	}
	var zero T
	return zero, false
}

// install sets v and returns the function that clears it; tb's cleanup clears
// it too. Clearing is idempotent and never removes a value installed after
// this one.
func (s *seam[T]) install(tb TB, v T) (uninstall func()) {
	tb.Helper()
	p := &v
	s.p.Store(p)
	uninstall = func() { s.p.CompareAndSwap(p, nil) }
	tb.Cleanup(uninstall)
	return uninstall
}

// graphBaseURL replaces Microsoft Graph's address while installed.
var graphBaseURL seam[string]

// InstallGraphBaseURL makes every OutlookClient call use url (for example an
// httptest Graph stand-in's URL) instead of https://graph.microsoft.com/v1.0,
// until the returned function is called or the test ends (PAC-133 AC-4,
// AC-6). Nothing in production changes Graph's address.
func InstallGraphBaseURL(tb TB, url string) (uninstall func()) {
	tb.Helper()
	return graphBaseURL.install(tb, url)
}
