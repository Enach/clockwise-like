package storage

import (
	"context"
	"database/sql"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

// PAC-80 item 1 (spec docs/specs/PAC-80.md AC-1..AC-3): the storage package's
// test seams are not exported assignable globals, are race-free, and never
// outlive the test that installed them.

// exportedFuncVars returns the package-level, exported variables of function
// type declared in the Go source files of fset/files: declared with a func
// type (literal or a func type named in the same files), or initialised with
// a function literal or a function of the same files.
func exportedFuncVars(files []*ast.File) []string {
	funcTypes := map[string]bool{}
	funcs := map[string]bool{}
	for _, f := range files {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					funcs[d.Name.Name] = true
				}
			case *ast.GenDecl:
				if d.Tok != token.TYPE {
					continue
				}
				for _, s := range d.Specs {
					ts := s.(*ast.TypeSpec)
					if _, ok := ts.Type.(*ast.FuncType); ok {
						funcTypes[ts.Name.Name] = true
					}
				}
			}
		}
	}
	isFuncType := func(e ast.Expr) bool {
		switch e := e.(type) {
		case *ast.FuncType:
			return true
		case *ast.Ident:
			return funcTypes[e.Name]
		}
		return false
	}
	isFuncValue := func(e ast.Expr) bool {
		switch e := e.(type) {
		case *ast.FuncLit:
			return true
		case *ast.Ident:
			return funcs[e.Name]
		}
		return false
	}
	var out []string
	for _, f := range files {
		for _, d := range f.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok || g.Tok != token.VAR {
				continue
			}
			for _, s := range g.Specs {
				vs := s.(*ast.ValueSpec)
				for i, n := range vs.Names {
					if !n.IsExported() {
						continue
					}
					fn := vs.Type != nil && isFuncType(vs.Type)
					if vs.Type == nil && i < len(vs.Values) && isFuncValue(vs.Values[i]) {
						fn = true
					}
					if fn {
						out = append(out, n.Name)
					}
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// AC-1: no exported, assignable package-level variable of function type in
// the storage package's production source (on d520775 GuardedCallHook,
// AfterLinkPatchUpdate and the two erasure seams were).
func TestPAC80_NoExportedFuncVariableInStorage(t *testing.T) {
	// The scan finds what it is meant to find (the pre-PAC-80 forms).
	fset := token.NewFileSet()
	probe, err := parser.ParseFile(fset, "probe.go", `package storage
import "context"
type Hook func()
func helper() {}
var GuardedCallHook func(ctx context.Context)
var Named Hook
var Lit = func() {}
var Ref = helper
var notExported func()
var ErrX = errorsNew("x")
var Struct struct{ F func() }`, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(exportedFuncVars([]*ast.File{probe}), ","); got != "GuardedCallHook,Lit,Named,Ref" {
		t.Fatalf("probe scan = %s", got)
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	if len(files) < 10 {
		t.Fatalf("scanned %d files: not the storage package", len(files))
	}
	if got := exportedFuncVars(files); len(got) != 0 {
		t.Fatalf("exported assignable function variables in storage: %v (install test seams through test_seams.go)", got)
	}
}

// AC-2: installing, clearing and firing the mid-request seam concurrently,
// through guarded calls with and without a scheduling-link write
// transaction, 1000 times. Run with -race (spec D-2: by the implementer, the
// gate's container has no C toolchain); without -race it still proves the
// calls work and the seam ends cleared.
func TestPAC80_GuardedCallHookIsRaceFree(t *testing.T) {
	db := openTestDB(t)
	u := pac71User(t, db, "pac80-race@example.com", "")
	ctx := WithSessionGuard(context.Background(), &SessionGuard{UserID: u, Gen: 0})
	var fired atomic.Int64
	const workers, rounds = 4, 250 // 1000 guarded passes
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(2)
		go func() { // installers and clearers
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				un := InstallGuardedCallHook(t, func(context.Context) { fired.Add(1) })
				un()
			}
		}()
		go func(w int) { // guarded calls, half through the link-write transaction
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				var err error
				if (i+w)%2 == 0 {
					err = WithSessionGeneration(ctx, db, func(q Querier) error { return nil })
				} else {
					err = withLinkWriteTx(ctx, db, func(q Querier) error { return nil })
				}
				if err != nil {
					t.Errorf("guarded call: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	if _, ok := guardedCallHook.get(); ok {
		t.Fatal("the seam is still installed after every installer cleared it")
	}
	t.Logf("hook fired %d times over %d guarded passes", fired.Load(), workers*rounds)
}

// AC-3: a seam is cleared when the test that installed it ends, and an
// earlier test's clear never removes a seam installed after it.
func TestPAC80_SeamsNeverOutliveTheirTest(t *testing.T) {
	t.Run("installs and leaves it", func(t *testing.T) {
		InstallGuardedCallHook(t, func(context.Context) {})
		InstallAfterLinkPatchUpdate(t, func(uuid.UUID) error { return nil })
		InstallAfterBookerErasureUpdate(t, func(uuid.UUID) error { return nil })
		InstallAfterBookerErasureRecheck(t, func(*sql.Tx, uuid.UUID) error { return nil })
	})
	if _, ok := guardedCallHook.get(); ok {
		t.Error("guarded-call seam outlived its test")
	}
	if _, ok := afterLinkPatchUpdate.get(); ok {
		t.Error("link PATCH seam outlived its test")
	}
	if _, ok := afterBookerErasureUpdate.get(); ok {
		t.Error("erasure update seam outlived its test")
	}
	if _, ok := afterBookerErasureRecheck.get(); ok {
		t.Error("erasure re-check seam outlived its test")
	}
	// A guarded call after that test observes no hook.
	fireGuardedCallHook(context.Background())

	first := InstallGuardedCallHook(t, func(context.Context) {})
	var second atomic.Bool
	InstallGuardedCallHook(t, func(context.Context) { second.Store(true) })
	first() // the earlier install's clear is stale: the later seam stays
	fireGuardedCallHook(context.Background())
	if !second.Load() {
		t.Fatal("clearing an earlier install removed a later one")
	}
}

// AC-6 (storage level) and spec §6: the profile get-or-create refuses a stale
// session and creates no row; without a session it creates the default row.
func TestPAC80_ProfileGetOrCreateUnderTheReCheck(t *testing.T) {
	db := openTestDB(t)
	u := pac71User(t, db, "pac80-profile@example.com", "")
	mustExec(t, db, `UPDATE users SET session_generation = 1 WHERE id = $1`, u)
	g := &SessionGuard{UserID: u, Gen: 0}
	stale := WithSessionGuard(context.Background(), g)
	if p, err := GetOrCreateUserProfileCtx(stale, db, u); !errors.Is(err, ErrStaleSession) || p != nil || !g.Stale() {
		t.Fatalf("stale get-or-create: %v %v", p, err)
	}
	if p, err := GetUserProfileCtx(stale, db, u); !errors.Is(err, ErrStaleSession) || p != nil {
		t.Fatalf("stale get: %v %v", p, err)
	}
	var n int
	_ = db.QueryRow(`SELECT count(*) FROM user_profiles WHERE user_id = $1`, u).Scan(&n)
	if n != 0 {
		t.Fatalf("a stale session created %d profile rows", n)
	}

	current := WithSessionGuard(context.Background(), &SessionGuard{UserID: u, Gen: 1})
	if _, err := GetUserProfileCtx(current, db, u); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("get without a row: %v", err)
	}
	p, err := GetOrCreateUserProfileCtx(current, db, u)
	if err != nil || p.IsManager || p.DetectedAt != nil || !p.AnalyticsSharedWithManager {
		t.Fatalf("current get-or-create: %+v %v", p, err)
	}
	other := pac71User(t, db, "pac80-profile-job@example.com", "")
	if p, err := GetOrCreateUserProfileCtx(context.Background(), db, other); err != nil || p.UserID != other {
		t.Fatalf("no session: %+v %v", p, err)
	}
}
