package main

import (
	"fmt"
	"go/ast"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// notSeams are interfaces the seam audit matches that were read and judged
// not to be storage or calendar seams: their methods are whole operations
// (an engine run, a handler's business step) whose implementation happens to
// call storage, not a stand-in for the store or the calendar client. A blank
// error from one of them is out of this guard's scope. Keyed like seams; the
// value is the reason, printed by the audit test.
var notSeams = map[string]string{
	backendModule + ".teamDetector":                  "engine operation (weekly manager detection), not a stand-in for the store",
	backendModule + "/api.BookingFlow":               "engine operation (slots, booking confirmation), not a stand-in for the store",
	backendModule + "/api.Compressor":                "engine operation (compression), not a stand-in for the calendar client",
	backendModule + "/api.ConferenceProviderFactory": "builds a conference provider from settings; an operation, not a store",
	backendModule + "/api.FocusEngine":               "engine operation (focus run, clear week), not a stand-in for the store",
	backendModule + "/api.ManagerWorkflow":           "engine operation (manager detection and stats), not a stand-in for the store",
	backendModule + "/api.NLPParser":                 "NLP operation; its storage call is the audit write",
	backendModule + "/api.PersonalCalendarBlocker":   "engine operation (personal-calendar preview and sync), not a stand-in for the store",
	backendModule + "/api.Scheduler":                 "engine operation (suggest, create meeting), not a stand-in for the calendar client",
	backendModule + "/api.TeamAvailability":          "engine operation (team slots and analytics), not a stand-in for the store",
}

// auditSeams fails the run when the seam list has a hole: a backend interface
// declared outside storage and calendar, with an error-returning method that
// some backend type implements by calling a targeted function (storage,
// auth.Load*, calendar, database/sql, or a listed seam) or by a method that
// is itself declared in storage or calendar. Such an interface is a seam: a
// blank error through it hides the same failure the direct call would. It
// must be listed in seams (guarded) or notSeams (with a reason).
//
// The evidence is one level deep (the implementing method's own body), which
// is how every seam in the backend is written today.
func auditSeams(pkgs []*packages.Package) (found map[string]string, problems []string) {
	var all []*packages.Package
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if p.PkgPath == backendModule || strings.HasPrefix(p.PkgPath, backendModule+"/") {
			all = append(all, p)
		}
	})

	decls := map[*types.Func]*ast.FuncDecl{}
	infoOf := map[*types.Func]*types.Info{}
	generated := map[string]bool{} // file name -> generated
	for _, p := range all {
		for _, f := range p.Syntax {
			generated[p.Fset.File(f.Pos()).Name()] = isGenerated(f)
			for _, d := range f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Body == nil {
					continue
				}
				if fn, ok := p.TypesInfo.Defs[fd.Name].(*types.Func); ok {
					decls[fn], infoOf[fn] = fd, p.TypesInfo
				}
			}
		}
	}

	type iface struct {
		key string
		t   *types.Interface
		pkg *types.Package
	}
	var ifaces []iface
	var concrete []*types.Named
	for _, p := range all {
		inScope := p.PkgPath != backendModule+"/storage" && p.PkgPath != backendModule+"/calendar"
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			tn, ok := scope.Lookup(name).(*types.TypeName)
			if !ok || tn.IsAlias() {
				continue
			}
			named, ok := tn.Type().(*types.Named)
			if !ok {
				continue
			}
			if it, ok := named.Underlying().(*types.Interface); ok {
				if inScope && it.NumMethods() > 0 && !generated[p.Fset.Position(tn.Pos()).Filename] {
					ifaces = append(ifaces, iface{p.PkgPath + "." + name, it, p.Types})
				}
				continue
			}
			if named.TypeParams().Len() == 0 {
				concrete = append(concrete, named)
			}
		}
	}

	found = map[string]string{}
	for _, in := range ifaces {
		for _, t := range concrete {
			var impl types.Type = t
			if !types.Implements(impl, in.t) {
				impl = types.NewPointer(t)
				if !types.Implements(impl, in.t) {
					continue
				}
			}
			if ev := seamEvidence(in.t, in.pkg, impl, decls, infoOf); ev != "" {
				if _, seen := found[in.key]; !seen {
					found[in.key] = fmt.Sprintf("%s.%s", t.Obj().Pkg().Name(), t.Obj().Name()) + ev
				}
			}
		}
	}
	keys := make([]string, 0, len(found))
	for k := range found {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if seams[k] == "" && notSeams[k] == "" {
			problems = append(problems, fmt.Sprintf("%s: implemented by %s — list it in seams (analyzer.go) or, with a reason, in notSeams (seams.go)", short(k), found[k]))
		}
	}
	return found, problems
}

// seamEvidence returns why impl's implementation of it is a storage or
// calendar seam, or "".
func seamEvidence(it *types.Interface, pkg *types.Package, impl types.Type, decls map[*types.Func]*ast.FuncDecl, infoOf map[*types.Func]*types.Info) string {
	for i := 0; i < it.NumMethods(); i++ {
		m := it.Method(i)
		sig := m.Type().(*types.Signature)
		if !returnsError(sig) {
			continue
		}
		obj, _, _ := types.LookupFieldOrMethod(impl, true, pkg, m.Name())
		fn, ok := obj.(*types.Func)
		if !ok || fn.Pkg() == nil {
			continue
		}
		if p := fn.Pkg().Path(); p == backendModule+"/storage" || p == backendModule+"/calendar" {
			return fmt.Sprintf(".%s is declared in %s", m.Name(), fn.Pkg().Name())
		}
		fd := decls[fn.Origin()]
		if fd == nil {
			continue
		}
		info := infoOf[fn.Origin()]
		var hit string
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || hit != "" {
				return hit == ""
			}
			if callee := calleeFunc(info, call); callee != nil {
				if _, ok := target(callee); ok {
					hit = calleeLabel(callee)
				}
			}
			return true
		})
		if hit != "" {
			return fmt.Sprintf(".%s calls %s", m.Name(), hit)
		}
	}
	return ""
}

func returnsError(sig *types.Signature) bool {
	r := sig.Results()
	for i := 0; i < r.Len(); i++ {
		if types.Identical(r.At(i).Type(), errorType) {
			return true
		}
	}
	return false
}

// short drops the module path: "api.ssoStore", or ".teamDetector" for the
// backend's main package.
func short(key string) string {
	if s, ok := strings.CutPrefix(key, backendModule+"/"); ok {
		return s
	}
	return strings.TrimPrefix(key, backendModule)
}
