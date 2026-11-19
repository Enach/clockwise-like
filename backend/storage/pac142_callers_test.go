package storage

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// PAC-142: GetUserByID reads the organisation and fails when that read
// fails, so only a caller that answers with User.Org may use it. Today that
// is GET /api/auth/me alone; every other reader uses GetUserByIDNoOrg. This
// pins the list, so moving a caller back (or adding one) is a reviewed change
// here, not a silent new failure mode. Syntactic: any call written
// GetUserByID(...) or <pkg>.GetUserByID(...) in non-test backend code.
func TestPAC142_GetUserByIDCallersArePinned(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := ""
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				name = fn.Name
			case *ast.SelectorExpr:
				name = fn.Sel.Name
			}
			if name == "GetUserByID" {
				got = append(got, filepath.ToSlash(rel))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if len(got) != 1 || got[0] != "api/handlers_me.go" {
		t.Fatalf("GetUserByID callers = %q, want only api/handlers_me.go (GET /api/auth/me); a caller that never reads User.Org uses GetUserByIDNoOrg", got)
	}
}
