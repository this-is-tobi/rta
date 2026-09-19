package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// newTestModel is the only place allowed to call teatest.NewTestModel.
//
// The wrapper is worth nothing if the next test file to want a program
// reaches past it — and reaching past it is the default, since
// teatest.NewTestModel is what every example and every other file in this
// package used to say. The cost of the omission is not paid where it is
// made: an abandoned program shows up as a data race in whatever test runs
// after it, which for this package is the theme one
// (TestAProgramIsStoppedEvenWhenItsTestNeverQuitsIt has the mechanism).
//
// Parsed rather than grepped, like the other drift tests here: three
// comments in this package argue about teatest.NewTestModel by name, and a
// comment explaining the rule must not trip it.
func TestEveryProgramInThisPackageIsStartedThroughNewTestModel(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var checked int
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "NewTestModel" {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != "teatest" {
					return true
				}
				checked++
				if fn.Name.Name != "newTestModel" {
					t.Errorf("%s: %s calls teatest.NewTestModel directly — use newTestModel, "+
						"which stops the program when the test ends",
						fset.Position(call.Pos()), fn.Name.Name)
				}
				return true
			})
		}
	}
	// A rule nothing exercises is a rule that has quietly stopped being
	// checked: if the wrapper itself is ever renamed or inlined away, this
	// must fail rather than pass over an empty walk.
	if checked != 1 {
		t.Errorf("found %d calls to teatest.NewTestModel, want exactly 1 (newTestModel's own)", checked)
	}
}
