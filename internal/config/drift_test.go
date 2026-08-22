package config_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// LoadFile's own doc states the rule: "anything that reads the config in
// order to write it back must start here: Load would fold this session's
// RTA_* into the value, and saving that would bake one shell's environment
// into the file for every future run."
//
// `rta init` was the one writer that did not. `RTA_OUTPUT=json rta init`
// offered json as the current setting and then wrote it, permanently, from a
// variable exported for one command — and nothing anywhere would have said
// so, because the file it produced is a perfectly valid file.
//
// There are two writers today and there will be more: a plugin-config editor
// is the obvious next one, and it is the one whose mistake would be the
// operator's credentials rather than an output format. A rule two callers
// have to remember is a rule the third breaks.
func TestEveryConfigWriterReadsThroughLoadFile(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()
	var offenders []string

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil // not this test's business
		}
		rel, _ := filepath.Rel(root, path)
		// The config package itself defines both, and is where the rule is
		// stated rather than followed.
		if filepath.Dir(rel) == filepath.Join("internal", "config") {
			return nil
		}

		var writes, loads bool
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "config" {
				return true
			}
			switch sel.Sel.Name {
			case "Write":
				writes = true
			case "Load":
				loads = true
			}
			return true
		})
		if writes && loads {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Errorf("these files write the config and read it with config.Load: %v\n"+
			"Load folds this shell's RTA_* into the value, so writing it back turns "+
			"one `export` into a permanent line in the operator's file — read with "+
			"config.LoadFile instead", offenders)
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
