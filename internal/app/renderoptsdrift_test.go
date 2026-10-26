package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"strings"
	"testing"
)

// Every render path in this package takes its options from renderOptions.
//
// They were spelled out at each path, and the copies drifted: Screen reached
// five of them and not the other nine, and Notes one. A new command copies
// whichever path is nearest, so the only rule that holds is that there is
// nothing to copy — a cli.Options literal anywhere but renderOptions fails
// here, with the function to call instead.
func TestRenderOptionsAreBuiltInOnePlace(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		// Parsed, not grepped: the helper's own comment names the type.
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Recv == nil && fn.Name.Name == "renderOptions" {
				continue
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok {
					return true
				}
				sel, ok := lit.Type.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Options" {
					return true
				}
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "cli" {
					t.Errorf("%s:%d: cli.Options built by hand — call renderOptions, so this path "+
						"draws an empty result's sentence on a terminal and puts csv's notes on "+
						"stderr the way every other one does",
						name, fset.Position(lit.Pos()).Line)
				}
				return true
			})
		}
	}
}

// A command group renders with viewRender. The profile, dashboard, policy and
// config groups each wrote the same render-or-refuse closure out, and the
// config one had gained a fallback the other three had not; a function literal
// of renderFn's shape anywhere but viewRender is the next copy, and fails here.
func TestTheRenderOrRefuseClosureIsWrittenOnce(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	shape := "func(*cobra.Command, view.View, *view.Error) error"
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "viewRender" {
				continue
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				lit, ok := n.(*ast.FuncLit)
				if !ok {
					return true
				}
				if unnamed(lit.Type) == shape {
					t.Errorf("%s:%d: a render-or-refuse closure written out — use viewRender",
						name, fset.Position(lit.Pos()).Line)
				}
				return true
			})
		}
	}
}

// unnamed is a function type as its signature reads with the parameter names
// left out.
func unnamed(ft *ast.FuncType) string {
	var params []string
	for _, f := range ft.Params.List {
		for range max(len(f.Names), 1) {
			params = append(params, types.ExprString(f.Type))
		}
	}
	var results []string
	if ft.Results != nil {
		for _, f := range ft.Results.List {
			results = append(results, types.ExprString(f.Type))
		}
	}
	return "func(" + strings.Join(params, ", ") + ") " + strings.Join(results, ", ")
}
