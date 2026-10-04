package atomicfile

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// stateReaders are the packages whose files live under paths.Data() and are
// read before anything verifies them.
//
// Named rather than derived, and the list is the point: os.ReadFile is
// perfectly correct almost everywhere else in rta — builtin/kv reads the
// operator's own key files, internal/config reads a config the operator owns
// in a directory nothing else writes — so a tree-wide ban would be a rule
// nobody could keep. What these seven share is the property that makes the
// unbounded read a weapon: the file sits in a directory internal/consent's
// own comment describes as one "whose whole threat model is that somebody
// else can write there", and every one of them is read *before* its seal,
// its MAC or its shape is checked, because the check is inside the bytes.
//
// An eighth package joining this list is a real decision — it means rta keeps
// state somewhere a lower-trust process can reach — so it is made here, in
// one line, rather than discovered later as a finding.
var stateReaders = []string{
	"internal/consent",
	"internal/grant",
	"internal/seal",
	"internal/agentlog",
	"internal/profile",
	"internal/plugintrust",
	// The declaration cache and the key that seals it: read on every run
	// that has a plugin, shell completion's included, and every entry is
	// read before its MAC is checked because the MAC is inside it. The
	// entries were capped first and the key was missed — one package, two
	// reads, and only a rule over the whole package would have caught the
	// second.
	"internal/pluginhost",
	// atomicfile and filelock define this rule rather than merely follow
	// it — Publish's own fallback read and filelock's lock-sentinel reads
	// were exactly the gap (grants.lock, read once
	// per retry, unbounded) — so they belong on the list they police, not
	// only exempted from it as the packages that happen to implement
	// ReadCapped.
	"internal/atomicfile",
	"internal/filelock",
}

// waitingOpeners are where an os.Open of a path would wait on a named pipe
// planted at it: the files the operator owns and rta reads before anything
// checks them, in directories something else can write to. Whole packages
// where every read is of such a file, one file where the package also opens
// paths the operator typed or rta staged itself, which are not this rule's
// business.
var waitingOpeners = []string{
	"internal/config",
	"internal/operator",
	"internal/mcp",
	"internal/plugindist/index.go",
}

// The read half of TestPersistentStateIsNotWrittenWithOsWriteFile, and it
// exists because the write half alone was not the whole discipline.
//
// A security scan found two of these — the consent decision file and
// grants.json — and reported them as separate findings. They were one class:
// six call sites across five packages, each an os.ReadFile on a fixed path a
// same-uid process can replace, each running before the seal that would have
// caught a forgery. Bounding two of them would have left the shape intact and
// the next scan free to find the rest.
//
// Parsed rather than grepped, for the same reason the write side is: the
// comments around these calls discuss os.ReadFile by name, and a comment
// naming the rule must not trip it.
func TestRtasOwnStateIsNotReadUnbounded(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()

	for _, pkg := range stateReaders {
		dir := filepath.Join(root, pkg)
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		if len(files) == 0 {
			t.Errorf("%s: no Go files — did the package move? A renamed package "+
				"silently stops being checked, which is the one way this test lies.", pkg)
			continue
		}
		for _, path := range files {
			if filepath.Ext(path) == ".go" && len(path) > 8 && path[len(path)-8:] == "_test.go" {
				continue
			}
			file, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				t.Fatal(perr)
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				t.Fatal(rerr)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "ReadFile" {
					return true
				}
				id, ok := sel.X.(*ast.Ident)
				if !ok || id.Name != "os" {
					return true
				}
				t.Errorf("%s:%d: os.ReadFile on a file under paths.Data() loads whatever "+
					"a same-uid process put there, before any seal or MAC is checked — "+
					"one large write is enough to take out the process that reads it. "+
					"Use atomicfile.ReadCapped with a cap sized to what rta writes.",
					rel, fset.Position(call.Pos()).Line)
				return true
			})
		}
	}
}

// os.Open of a file the operator owns waits for a writer if a named pipe has
// been put in its place, which Lstat before it cannot prevent: the check and
// the open are two moments. atomicfile.Open opens without waiting and refuses
// anything that is not a regular file.
func TestOperatorOwnedFilesAreNotOpenedWithOsOpen(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()
	for _, target := range waitingOpeners {
		full := filepath.Join(root, target)
		files := []string{full}
		if filepath.Ext(full) != ".go" {
			var err error
			if files, err = filepath.Glob(filepath.Join(full, "*.go")); err != nil {
				t.Fatal(err)
			}
		}
		if len(files) == 0 {
			t.Errorf("%s: no Go files — did it move? A renamed target silently stops being checked.", target)
		}
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			file, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				t.Fatal(perr)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || (sel.Sel.Name != "Open" && sel.Sel.Name != "ReadFile") {
					return true
				}
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "os" {
					rel, _ := filepath.Rel(root, path)
					t.Errorf("%s:%d: os.%s waits for a writer on a named pipe put in the file's place; "+
						"use atomicfile.Open, ReadFile or ReadCapped",
						rel, fset.Position(call.Pos()).Line, sel.Sel.Name)
				}
				return true
			})
		}
	}
}
