package atomicfile

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// allowed names the os.WriteFile calls that are not persistent state, with
// the reason each is fine. Everything else must go through this package.
//
// Kept deliberately short and specific. An allowlist that grows is a rule
// nobody believes in — if a third entry ever wants to be here, the question
// to ask is whether the file it writes is really scratch.
var allowed = map[string]string{
	"builtin/kv/edit.go": "plaintext into a private temp directory that is RemoveAll'd " +
		"on the way out — scratch by construction, and atomicity would protect nothing",
	"internal/app/scaffold.go": "brand-new files in a brand-new plugin directory: " +
		"nothing exists to be replaced and no reader is watching",
}

// Six places in rta grew their own copy of temp-file-plus-rename, and the
// seventh — the config file, the one written on a keystroke — grew a plain
// os.WriteFile instead. Two of the six were the *same function* in two
// packages, and only one of them had been got right; the other truncated a
// seal key that a lockless reader could catch mid-write, at which point rta
// told the operator their grant file "was not written by rta" and suggested
// deleting it.
//
// Both defects are the same shape: a discipline held everywhere but one
// place, where nothing made the omission visible. This is what makes it
// visible.
func TestPersistentStateIsNotWrittenWithOsWriteFile(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Nothing under these is rta's own state handling.
			if name := d.Name(); name == ".git" || name == "testdata" || name == "examples" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		if _, ok := allowed[filepath.ToSlash(rel)]; ok {
			return nil
		}
		// Parsed rather than grepped: this package's own doc comments argue
		// about os.WriteFile at length, and so do three of the call sites it
		// replaced. A comment mentioning the rule must not trip it.
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "WriteFile" {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "os" {
				return true
			}
			t.Errorf("%s:%d: os.WriteFile truncates before it writes, so a reader can "+
				"catch the file empty and a crash can leave it that way — use "+
				"atomicfile.Write, or atomicfile.Publish for a file that must be "+
				"created once and never replaced. If this really is scratch, say so "+
				"in atomicfile's allowed map.",
				filepath.ToSlash(rel), fset.Position(call.Pos()).Line)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Every exemption must still exist. An allowlist entry for a file that moved
// or a call that was already fixed is an exemption nobody is watching, which
// is how the next one gets waved through.
func TestEveryExemptionIsStillNeeded(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()
	for rel := range allowed {
		path := filepath.Join(root, filepath.FromSlash(rel))
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Errorf("%s is exempted but cannot be read: %v", rel, err)
			continue
		}
		found := false
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "WriteFile" {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "os" {
				found = true
			}
			return true
		})
		if !found {
			t.Errorf("%s is exempted but no longer calls os.WriteFile — drop the exemption", rel)
		}
	}
}

// repoRoot walks up to the directory holding go.mod.
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
