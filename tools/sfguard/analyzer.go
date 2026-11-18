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

// calendarOpsTypes are the engine's calendar-operation seams: the interface the
// engine calls Google through (calendarOps, its production realOps), and the
// free/busy readers of the team and Outlook paths. A call through them is a
// calendar read or write, whatever the concrete client behind it.
var calendarOpsTypes = map[string]bool{
	"calendarOps":     true,
	"realOps":         true,
	"outlookFreeBusy": true,
	"googleFreeBusy":  true,
}

// Finding is one blank-assigned error from a targeted call.
type Finding struct {
	File   string // slash path relative to the repo root
	Line   int    // for the message only; never part of the fingerprint
	Func   string // enclosing top-level declaration, e.g. (*Handler).Create
	Callee string // e.g. storage.GetUser, storage.DBTX.ExecContext, sql.DB.Exec
	Why    string // which target set matched
}

// Key is the line-insensitive fingerprint the baseline records.
func (f Finding) Key() string { return f.File + " " + f.Func + " " + f.Callee }

// target reports whether a call to fn is in the guarded set, and why.
//
//   - anything declared in backend/storage (functions, and methods of its
//     types and interfaces: Querier, DBTX, the stores);
//   - auth.Load* (the stored-credential loads);
//   - methods declared in backend/calendar (Provider, CalendarClient,
//     OutlookClient, the webcal client): the calendar operations themselves;
//   - the engine's calendar-ops seams (calendarOpsTypes);
//   - methods of database/sql's handles (*sql.DB, *sql.Tx, *sql.Conn,
//     *sql.Stmt, *sql.Row, *sql.Rows) — the `_ = h.db.Exec(...)` shape —
//     except Rollback and Close, whose blank-assigned error is the idiom
//     after a failure or a completed read and carries no default.
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
	case path == backendModule+"/engine" && calendarOpsTypes[recv]:
		return "calendar-ops", true
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

func isGenerated(f *ast.File) bool {
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if strings.Contains(c.Text, "Code generated") && strings.Contains(c.Text, "DO NOT EDIT") {
				return true
			}
		}
	}
	return false
}

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

// checkPackage returns every blank-assigned error from a targeted call in the
// package's non-test, non-generated files.
func checkPackage(pkg *packages.Package, root string) []Finding {
	var out []Finding
	for i, file := range pkg.Syntax {
		name := pkg.CompiledGoFiles[i]
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

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: %s discards the error from %s (%s)", f.File, f.Line, f.Func, f.Callee, f.Why)
}
