package stdio

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// exempt names files that may still say os.Stdin, with the reason.
//
// Deliberately empty. Every reader in rta goes through Real, and the point of
// an empty allowlist is that adding to it is a decision somebody has to
// defend in writing rather than a line that slips through review.
var exempt = map[string]string{}

// os.Stdin is not a stream, it is a variable that go-plugin reads at spawn
// time (client.go:659, `cmd.Stdin = os.Stdin`, unconditional, no opt-out). So
// this package repoints it at /dev/null and keeps the real one — which means
// any other code naming os.Stdin gets /dev/null and silently reads nothing,
// or, if it runs before Claim, reopens the very hole Claim closed.
//
// This started as a courtesy each surface performed for itself: Claim for
// `mcp serve`, Silence for the human surfaces. Silence had zero callers and
// `mcp serve` called Claim from inside its RunE, which cobra runs long after
// main has loaded — and therefore spawned — every installed plugin. So every
// rta invocation handed fd 0 to every plugin on $PATH. Reproduced with a
// binary that does not even complete the handshake: `printf 'secret' | rta
// explain sys.status` gave it 36 bytes of the caller's input.
//
// A rule four surfaces have to remember is a rule the fifth breaks. This is
// what stops the fifth.
func TestOnlyStdioNamesOsStdin(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()

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
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		// This package owns fd 0, and its tests have to be able to install a
		// pipe as the real one to test anything at all.
		if strings.HasPrefix(rel, "internal/stdio/") {
			return nil
		}
		// Tests may read their own stdin: they are not what a plugin inherits.
		if strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		if _, ok := exempt[rel]; ok {
			return nil
		}
		// Parsed, not grepped: the fix's own comments explain at length why
		// os.Stdin must not be named, and a comment about the rule must not
		// trip it. That is not hypothetical — four of them say it.
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Stdin" {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "os" {
				return true
			}
			t.Errorf("%s:%d: os.Stdin is /dev/null after main claims fd 0, so this reads "+
				"nothing — and naming it before the claim is how a plugin came to inherit "+
				"the user's input. Use stdio.Real(), which is the true stream whether or "+
				"not Claim has run.",
				rel, fset.Position(sel.Pos()).Line)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Silence was the other half of the original design: Claim for a surface that
// wants the stream back, Silence for one that only wants children not to have
// it. It had no callers, which is what "the human surfaces were supposed to
// remember" turned out to mean in practice. main claims once for everyone
// now, so a second entry point is a second thing to forget.
func TestSilenceIsGone(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()
	pkg, err := parser.ParseDir(fset, filepath.Join(root, "internal", "stdio"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pkg {
		for name, file := range p.Files {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			for _, d := range file.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if ok && fn.Name.Name == "Silence" {
					t.Errorf("%s: Silence is back. Every surface calling it for itself is "+
						"the design that left fd 0 open; main claims once instead.", name)
				}
			}
		}
	}
}

// The one mutation the tests above cannot see: main not calling Claim at all.
// Deleting the call breaks the build only while the import is still there,
// and deleting both compiles cleanly and reopens the hole in full — every rta
// invocation handing fd 0 to every plugin on $PATH, with every other test in
// this package still green, because they all test Claim rather than the fact
// that somebody calls it.
//
// Position matters as much as presence. LoadPlugins spawns; a Claim after it
// is the exact bug this replaced, where `mcp serve` claimed correctly, from
// inside a RunE that runs after startup.
func TestMainClaimsStdinBeforeItSpawnsAnything(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()
	path := filepath.Join(root, "cmd", "rta", "main.go")
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	var mainFn *ast.FuncDecl
	for _, d := range file.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "main" && fn.Recv == nil {
			mainFn = fn
		}
	}
	if mainFn == nil {
		t.Fatal("cmd/rta/main.go has no func main")
	}

	// -1 means "never called", which sorts before everything and would let a
	// missing Claim pass an ordering check, so both are asserted separately.
	posOf := func(pkg, name string) int {
		found := -1
		ast.Inspect(mainFn, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != name {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == pkg && found == -1 {
				found = fset.Position(call.Pos()).Line
			}
			return true
		})
		return found
	}

	claim, spawn := posOf("stdio", "Claim"), posOf("app", "LoadPlugins")
	if claim == -1 {
		t.Fatal("main does not call stdio.Claim: every plugin it spawns inherits the " +
			"caller's stdin — an agent's JSON-RPC stream, or a passphrase being typed")
	}
	if spawn == -1 {
		t.Fatal("main no longer calls app.LoadPlugins; this test asserts an order between " +
			"two calls and one of them is gone, so it is now checking nothing")
	}
	if claim > spawn {
		t.Errorf("main claims stdin at line %d but spawns plugins at line %d: the spawn is "+
			"what copies the descriptor, so claiming afterwards closes nothing", claim, spawn)
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
