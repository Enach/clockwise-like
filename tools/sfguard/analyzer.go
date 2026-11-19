package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
)

// backendModule is the import path the targeted packages live under. The
// self-test fixture (testdata/fixture) declares the same module path, so the
// guard runs on it exactly as it runs on backend/.
const backendModule = "github.com/Enach/paceday/backend"

// seams are the types declared outside backend/storage and backend/calendar
// that a storage or calendar call goes through: interfaces that exist so a
// test can substitute the store or the client, and their production
// implementations. A call through one is a storage read or write, or a
// calendar operation, whatever the concrete type behind it. Keyed by package
// path and type name; the value is the target set reported.
//
// The list is enforced, not trusted: auditSeams (seams.go) fails the run when a
// backend interface outside storage and calendar has an error-returning method
// that an implementation answers with a targeted call, and it is in neither
// this map nor notSeams; and when a concrete type behind a listed seam
// interface is not listed itself (a call on the concrete type, `t, _ :=
// g.load(ctx)`, hides the same failure).
var seams = map[string]string{
	backendModule + "/engine.calendarOps":        "calendar-ops",
	backendModule + "/engine.realOps":            "calendar-ops",
	backendModule + "/engine.outlookFreeBusy":    "calendar-ops",
	backendModule + "/engine.googleFreeBusy":     "calendar-ops",
	backendModule + "/api.ConferenceEventClient": "calendar-ops",
	backendModule + "/api.orgAIStore":            "storage-seam",
	backendModule + "/api.dbOrgAIStore":          "storage-seam",
	backendModule + "/api.ssoStore":              "storage-seam",
	backendModule + "/api.dbSSOStore":            "storage-seam",
	backendModule + "/nlp.AISource":              "storage-seam",
	backendModule + "/nlp.DBSource":              "storage-seam",
	backendModule + "/auth.credentialStore":      "storage-seam",
	backendModule + "/auth.googleStore":          "storage-seam",
	backendModule + "/auth.microsoftStore":       "storage-seam",
	backendModule + "/auth.zoomStore":            "storage-seam",
}

// Shape is which form of dropped error a finding is.
type Shape int

const (
	// Blank: the error result is assigned to `_` (PAC-122).
	Blank Shape = iota
	// ErrNil: `if v, err := target(...); err == nil { ... }` with no else
	// and no later use of err: the failure takes the same path as "absent"
	// (PAC-142).
	ErrNil
)

// errNilPrefix marks an ErrNil finding's callee in the baseline, so the two
// shapes of one call are counted apart.
const errNilPrefix = "errnil:"

// Finding is one dropped error from a targeted call.
type Finding struct {
	File   string // slash path relative to the repo root
	Line   int    // for the message only; never part of the fingerprint
	Func   string // enclosing top-level declaration, e.g. (*Handler).Create
	Callee string // e.g. storage.GetUser, storage.DBTX.ExecContext, sql.DB.Exec
	Why    string // which target set matched
	Shape  Shape
}

// calleeField is the third baseline field: the callee, prefixed with
// errnil: for the ErrNil shape.
func (f Finding) calleeField() string {
	if f.Shape == ErrNil {
		return errNilPrefix + f.Callee
	}
	return f.Callee
}

// Key is the line-insensitive fingerprint the baseline records.
func (f Finding) Key() string { return f.File + " " + f.Func + " " + f.calleeField() }

// target reports whether a call to fn is in the guarded set, and why.
//
//   - anything declared in backend/storage (functions, and methods of its
//     types and interfaces: Querier, DBTX, the stores);
//   - auth.Load* (the stored-credential loads);
//   - methods declared in backend/calendar (Provider, CalendarClient,
//     OutlookClient, the webcal client): the calendar operations themselves;
//   - methods of the seams above (engine calendar-ops, api and auth store
//     interfaces and their production implementations);
//   - methods of database/sql's handles (*sql.DB, *sql.Tx, *sql.Conn,
//     *sql.Stmt, *sql.Row, *sql.Rows): the `_ = h.db.Exec(...)` and
//     `_ = h.db.QueryRow(...).Scan(...)` shapes.
//
// Methods named Rollback or Close are never targeted: `_ = tx.Rollback()` on
// a path that already failed, or after a commit, is the idiom, and its error
// carries no value anyone reads as a default. sql.Result is not a handle, so
// `n, _ := res.RowsAffected()` is out of scope by design (the drivers in use
// never fail it once Exec succeeded).
func target(fn *types.Func) (string, bool) {
	pkg := fn.Pkg()
	if pkg == nil {
		return "", false
	}
	path := pkg.Path()
	sig, _ := fn.Type().(*types.Signature)
	recv := ""
	if sig != nil && sig.Recv() != nil {
		recv = recvTypeName(sig.Recv().Type())
	}
	if recv != "" && (fn.Name() == "Rollback" || fn.Name() == "Close") {
		return "", false
	}
	switch {
	case path == backendModule+"/storage":
		return "storage", true
	case path == backendModule+"/auth" && recv == "" && strings.HasPrefix(fn.Name(), "Load"):
		return "auth.Load*", true
	case path == backendModule+"/calendar" && recv != "":
		return "calendar", true
	case recv != "" && seams[path+"."+recv] != "":
		return seams[path+"."+recv], true
	case path == "database/sql" && recv != "":
		switch recv {
		case "DB", "Tx", "Conn", "Stmt", "Row", "Rows":
			return "database/sql", true
		}
	}
	return "", false
}

func recvTypeName(t types.Type) string {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	switch n := t.(type) {
	case *types.Named:
		return n.Obj().Name()
	case *types.Alias:
		return n.Obj().Name()
	}
	return ""
}

func calleeLabel(fn *types.Func) string {
	label := fn.Pkg().Name() + "."
	if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
		if r := recvTypeName(sig.Recv().Type()); r != "" {
			label += r + "."
		}
	}
	return label + fn.Name()
}

var errorType = types.Universe.Lookup("error").Type()

// calleeFunc resolves the called function or method, static or through an
// interface. Calls of function values (closures, fields) resolve to nothing.
func calleeFunc(info *types.Info, call *ast.CallExpr) *types.Func {
	fun := ast.Unparen(call.Fun)
	switch f := fun.(type) {
	case *ast.IndexExpr: // generic instantiation f[T](...)
		fun = f.X
	case *ast.IndexListExpr:
		fun = f.X
	}
	var id *ast.Ident
	switch f := fun.(type) {
	case *ast.Ident:
		id = f
	case *ast.SelectorExpr:
		id = f.Sel
	default:
		return nil
	}
	fn, _ := info.Uses[id].(*types.Func)
	return fn
}

func isBlank(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "_"
}

// isGenerated is the Go convention (go.dev/s/generatedcode): the marker line
// before the package clause. The phrase anywhere else exempts nothing.
func isGenerated(f *ast.File) bool { return ast.IsGenerated(f) }

// funcName names a top-level declaration the way the baseline records it.
func funcName(d *ast.FuncDecl) string {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return d.Name.Name
	}
	t := d.Recv.List[0].Type
	star := ""
	if s, ok := t.(*ast.StarExpr); ok {
		star, t = "*", s.X
	}
	switch g := t.(type) { // drop type parameters: T[K] -> T
	case *ast.IndexExpr:
		t = g.X
	case *ast.IndexListExpr:
		t = g.X
	}
	name := "?"
	if id, ok := t.(*ast.Ident); ok {
		name = id.Name
	}
	if star != "" {
		return "(*" + name + ")." + d.Name.Name
	}
	return name + "." + d.Name.Name
}

// checkPackage returns every blank-assigned error from a targeted call, and
// every `err == nil` swallow of one (errNilSwallow), in the package's
// non-test, non-generated files.
func checkPackage(pkg *packages.Package, root string) []Finding {
	var out []Finding
	writes := plainAssignTargets(pkg)
	for _, file := range pkg.Syntax {
		name := pkg.Fset.File(file.Pos()).Name()
		if strings.HasSuffix(name, "_test.go") || isGenerated(file) {
			continue
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			rel = name
		}
		rel = filepath.ToSlash(rel)
		for _, decl := range file.Decls {
			encl := "<package>"
			if fd, ok := decl.(*ast.FuncDecl); ok {
				encl = funcName(fd)
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				if is, ok := n.(*ast.IfStmt); ok {
					if f, ok := errNilSwallow(pkg, is, writes); ok {
						f.File, f.Func = rel, encl
						out = append(out, f)
					}
					return true
				}
				var lhs, rhs []ast.Expr
				switch s := n.(type) {
				case *ast.AssignStmt:
					if s.Tok != token.ASSIGN && s.Tok != token.DEFINE {
						return true
					}
					lhs, rhs = s.Lhs, s.Rhs
				case *ast.ValueSpec:
					for _, id := range s.Names {
						lhs = append(lhs, id)
					}
					rhs = s.Values
				default:
					return true
				}
				for _, f := range blankErrors(pkg, lhs, rhs) {
					f.File, f.Func = rel, encl
					out = append(out, f)
				}
				return true
			})
		}
	}
	return out
}

// blankErrors pairs left-hand sides with call results. `a, _ := f()` maps
// positions onto f's result tuple; `_, _ = f(), g()` pairs one to one.
func blankErrors(pkg *packages.Package, lhs, rhs []ast.Expr) []Finding {
	var out []Finding
	check := func(call *ast.CallExpr, results *types.Tuple, blanks []ast.Expr) {
		fn := calleeFunc(pkg.TypesInfo, call)
		if fn == nil {
			return
		}
		why, ok := target(fn)
		if !ok {
			return
		}
		for i := 0; i < results.Len() && i < len(blanks); i++ {
			if isBlank(blanks[i]) && types.Identical(results.At(i).Type(), errorType) {
				out = append(out, Finding{
					Line:   pkg.Fset.Position(call.Pos()).Line,
					Callee: calleeLabel(fn),
					Why:    why,
				})
				return
			}
		}
	}
	sigResults := func(call *ast.CallExpr) *types.Tuple {
		tv, ok := pkg.TypesInfo.Types[call.Fun]
		if !ok {
			return nil
		}
		sig, ok := tv.Type.Underlying().(*types.Signature)
		if !ok {
			return nil
		}
		return sig.Results()
	}
	if len(rhs) == 1 && len(lhs) > 1 {
		if call, ok := ast.Unparen(rhs[0]).(*ast.CallExpr); ok {
			if res := sigResults(call); res != nil {
				check(call, res, lhs)
			}
		}
		return out
	}
	for i := 0; i < len(rhs) && i < len(lhs); i++ {
		call, ok := ast.Unparen(rhs[i]).(*ast.CallExpr)
		if !ok {
			continue
		}
		if res := sigResults(call); res != nil && res.Len() == 1 {
			check(call, res, lhs[i:i+1])
		}
	}
	return out
}

// errNilSwallow matches the PAC-142 shape:
//
//	if v, err := storage.Get(...); err == nil { use(v) }
//
// an if statement whose init assigns one targeted call's results (:= or =),
// whose error result lands in a named variable, whose condition is
// `err == nil` (or `nil == err`), alone or as the first operand of an &&
// chain (`err == nil && v != nil`: a failure still takes the false branch),
// whose else branch (if any) never reads that variable, and with no read of
// it after the if statement. A failed call then takes the path of "absent":
// nothing is said. An else, or an else-if chain, that does not read the
// error (`} else if other {`) does not handle it, so it exempts nothing; one
// that reads it (`} else if !errors.Is(err, sql.ErrNoRows) {`) does. A later
// write (`err = storage.Save(...)`) is not a read. Not matched: a separate
// `v, err := f()` line followed by `if err == nil` (the error outlives the
// if, and errcheck-style review sees it), `||` conditions, `err == nil` that
// is not the leftmost operand of &&, and a switch init.
func errNilSwallow(pkg *packages.Package, is *ast.IfStmt, writes map[*ast.Ident]bool) (Finding, bool) {
	if is.Init == nil {
		return Finding{}, false
	}
	as, ok := is.Init.(*ast.AssignStmt)
	if !ok || (as.Tok != token.DEFINE && as.Tok != token.ASSIGN) || len(as.Rhs) != 1 {
		return Finding{}, false
	}
	call, ok := ast.Unparen(as.Rhs[0]).(*ast.CallExpr)
	if !ok {
		return Finding{}, false
	}
	fn := calleeFunc(pkg.TypesInfo, call)
	if fn == nil {
		return Finding{}, false
	}
	why, ok := target(fn)
	if !ok {
		return Finding{}, false
	}
	errVar := nilComparedIdent(is.Cond)
	if errVar == nil {
		return Finding{}, false
	}
	obj := pkg.TypesInfo.ObjectOf(errVar)
	if obj == nil || !types.Identical(obj.Type(), errorType) {
		return Finding{}, false
	}
	// The compared variable must be one the init assigned from the call.
	assigned := false
	for _, l := range as.Lhs {
		if id, ok := l.(*ast.Ident); ok && id.Name != "_" && pkg.TypesInfo.ObjectOf(id) == obj {
			assigned = true
		}
	}
	if !assigned {
		return Finding{}, false
	}
	// An else (or else-if chain) that reads the variable sees the failure.
	if is.Else != nil && readsVar(pkg, is.Else, obj, writes) {
		return Finding{}, false
	}
	// A later read of the variable (only possible for `=`, where it outlives
	// the if) means the failure is looked at after all. A write is not a read.
	for id, o := range pkg.TypesInfo.Uses {
		if o == obj && id.Pos() > is.End() && !writes[id] {
			return Finding{}, false
		}
	}
	return Finding{
		Line:   pkg.Fset.Position(call.Pos()).Line,
		Callee: calleeLabel(fn),
		Why:    why,
		Shape:  ErrNil,
	}, true
}

// readsVar reports whether n reads obj (a use that is not the target of a
// plain `=` assignment).
func readsVar(pkg *packages.Package, n ast.Node, obj types.Object, writes map[*ast.Ident]bool) bool {
	found := false
	ast.Inspect(n, func(c ast.Node) bool {
		if id, ok := c.(*ast.Ident); ok && !writes[id] && pkg.TypesInfo.Uses[id] == obj {
			found = true
		}
		return !found
	})
	return found
}

// plainAssignTargets is every identifier written by a plain `=` assignment
// (`err = f()`, `v, err = f()`) in the package: a use that is a write, not a
// read.
func plainAssignTargets(pkg *packages.Package) map[*ast.Ident]bool {
	out := map[*ast.Ident]bool{}
	for _, f := range pkg.Syntax {
		ast.Inspect(f, func(n ast.Node) bool {
			if as, ok := n.(*ast.AssignStmt); ok && as.Tok == token.ASSIGN {
				for _, l := range as.Lhs {
					if id, ok := ast.Unparen(l).(*ast.Ident); ok {
						out[id] = true
					}
				}
			}
			return true
		})
	}
	return out
}

// nilComparedIdent returns x for a condition `x == nil` or `nil == x`, or
// such a comparison as the leftmost operand of an && chain.
func nilComparedIdent(cond ast.Expr) *ast.Ident {
	b, ok := ast.Unparen(cond).(*ast.BinaryExpr)
	if !ok {
		return nil
	}
	if b.Op == token.LAND {
		return nilComparedIdent(b.X)
	}
	if b.Op != token.EQL {
		return nil
	}
	isNil := func(e ast.Expr) bool {
		id, ok := ast.Unparen(e).(*ast.Ident)
		return ok && id.Name == "nil"
	}
	x, y := ast.Unparen(b.X), ast.Unparen(b.Y)
	if isNil(y) {
		id, _ := x.(*ast.Ident)
		return id
	}
	if isNil(x) {
		id, _ := y.(*ast.Ident)
		return id
	}
	return nil
}

func (f Finding) String() string {
	if f.Shape == ErrNil {
		return fmt.Sprintf("%s:%d: %s acts only when %s succeeds (`if ...; err == nil`, no else that reads err): its error is dropped (%s, %s)", f.File, f.Line, f.Func, f.Callee, f.Why, errNilPrefix+f.Callee)
	}
	return fmt.Sprintf("%s:%d: %s discards the error from %s (%s)", f.File, f.Line, f.Func, f.Callee, f.Why)
}
