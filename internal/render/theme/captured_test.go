package theme

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The palette changes while rta runs — when the environment in force changes —
// so a colour, or a style made from one, must never be computed once and kept.
// The way that happens is a package-level var: its initializer runs before main,
// before the configuration is read, and nothing runs it again. That is how a
// theme override came to reach nine of thirteen shared styles and miss one, and
// it is exactly what a switch of environment would do to every screen that
// kept one.
//
// So this reads every package of the program and refuses a package-level
// variable whose initializer touches the palette (theme.Primary, theme.Key,
// lipgloss.NewStyle, lipgloss.Color — anything that makes a colour). A variable
// that merely holds a border shape or a glyph is fine and is not matched.
// What is built when something is drawn (inside a function) is the rule and not
// the exception, and is not looked at.
func TestNoPackageKeepsAColourItComputedOnce(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		// The palette itself is where the vars are, and where they are rebuilt.
		if filepath.Dir(rel) == filepath.Join("internal", "render", "theme") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil //nolint:nilerr // a file that does not parse is the compiler's finding, not this test's
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, value := range vs.Values {
					if makesAColour(value) {
						name := "_"
						if i < len(vs.Names) {
							name = vs.Names[i].Name
						}
						offenders = append(offenders, rel+": var "+name)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Errorf("a package-level variable is built from the palette once, at startup, and will keep the colours "+
			"of the environment it started in:\n  %s\nbuild it where it is drawn, or from theme.Key and "+
			"the other shared styles at the point of use",
			strings.Join(offenders, "\n  "))
	}
}

// makesAColour reports whether an initializer reaches for the palette: any
// member of the theme package, or lipgloss making a style or a colour.
func makesAColour(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		switch {
		case pkg.Name == "theme":
			found = true
		case pkg.Name == "lipgloss" && (sel.Sel.Name == "NewStyle" || sel.Sel.Name == "Color"):
			found = true
		}
		return true
	})
	return found
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found above the test's working directory")
		}
		dir = parent
	}
}
