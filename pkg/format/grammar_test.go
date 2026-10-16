package format

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseDurationReadsDaysAndWeeksAndGoSpellings(t *testing.T) {
	for raw, want := range map[string]time.Duration{
		"30s":   30 * time.Second,
		"5m":    5 * time.Minute,
		"2h":    2 * time.Hour,
		"1d":    24 * time.Hour,
		"1w":    7 * 24 * time.Hour,
		"1.5d":  36 * time.Hour,
		"1d12h": 36 * time.Hour,
		"1h30m": 90 * time.Minute,
		"0":     0,
	} {
		got, err := ParseDuration(raw)
		if err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %v, %v; want %v", raw, got, err, want)
		}
	}
	for _, raw := range []string{"", "30", "soon", "-5m", "1 d", "5x", "d"} {
		if _, err := ParseDuration(raw); err == nil {
			t.Errorf("ParseDuration(%q) was accepted", raw)
		}
	}
}

// Every length of time a person writes is read by ParseDuration, so what is valid
// in one place is valid in all of them. A call to Go's own parser anywhere else is
// a place where `1d` is refused and the next place takes it, and it is how the
// profile's deadline, a role's window and a grant's lifetime came to disagree with
// a lock's. This holds the whole tree to it, tests aside.
//
// What is not a person's spelling, a duration a program printed and reads back, is
// not covered by this and would be written as `format.ParseDuration` all the same:
// the grammar contains Go's.
func TestNoPlaceReadsALengthOfTimeWithGosOwnParser(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", ".local", "node_modules", ".tools":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".pb.go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil //nolint:nilerr // a file that does not parse is the compiler's finding, not this test's
		}
		names := map[string]bool{}
		for _, imp := range file.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == "time" {
				name := "time"
				if imp.Name != nil {
					name = imp.Name.Name
				}
				names[name] = true
			}
		}
		if len(names) == 0 {
			return nil
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "ParseDuration" {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && names[pkg.Name] {
				rel, _ := filepath.Rel(root, path)
				offenders = append(offenders, rel+":"+strconv.Itoa(fset.Position(call.Pos()).Line))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Errorf("these read a length of time with time.ParseDuration, which has no day: use format.ParseDuration\n  %s",
			strings.Join(offenders, "\n  "))
	}
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
