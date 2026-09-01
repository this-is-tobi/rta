package atomicfile

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

// stateReaders are the packages whose files live under paths.Data() and are
// read before anything verifies them.
//
// Named rather than derived, and the list is the point: os.ReadFile is
// perfectly correct almost everywhere else in rta — builtin/kv reads the
// operator's own key files, internal/config reads a config the operator owns
// in a directory nothing else writes — so a tree-wide ban would be a rule
// nobody could keep. What these six share is the property that makes the
// unbounded read a weapon: the file sits in a directory internal/consent's
// own comment describes as one "whose whole threat model is that somebody
// else can write there", and every one of them is read *before* its seal,
// its MAC or its shape is checked, because the check is inside the bytes.
//
// A seventh package joining this list is a real decision — it means rta keeps
// state somewhere a lower-trust process can reach — so it is made here, in
// one line, rather than discovered later as a finding.
var stateReaders = []string{
	"internal/consent",
	"internal/grant",
	"internal/seal",
	"internal/agentlog",
	"internal/profile",
	"internal/plugintrust",
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
		dir := filepath.Join(root, filepath.FromSlash(pkg))
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
					filepath.ToSlash(rel), fset.Position(call.Pos()).Line)
				return true
			})
		}
	}
}
