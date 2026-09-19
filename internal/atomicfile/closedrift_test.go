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

// closeAllowed names the files opened for writing whose Close may go
// unread, with the reason each is fine. Same shape and same rule as
// `allowed` above it: short, specific, and checked by a test of its own so a
// stale entry cannot sit here waving the next one through.
var closeAllowed = map[string]string{
	"internal/app/mcp.go": "a writability probe — the file is opened, closed and removed " +
		"without a byte being written, so there is no buffered write for Close to fail on",
	"internal/filelock/filelock.go": "CreateTemp reserves a name for Link and nothing else; " +
		"the handle is closed and the file removed before anything is written to it",
	"internal/stdio/stdio.go": "/dev/null, installed as os.Stdin for the life of the process " +
		"and deliberately never closed — see the Real doc on why closing fd 0 is the bug",
}

// A file opened for writing has its Close read, because a Close that fails
// is a write that did not land.
//
// **Write, then Close, is two operations and only the first one is obvious.**
// The bytes a program hands to Write sit in the kernel's page cache; the
// error that says they never reached the disk — a full filesystem, a quota,
// a network mount that went away — is delivered at Close, and on a file
// where nothing checks it, that error is the whole failure. What the caller
// sees instead is a success message about a file that is short.
//
// rta already holds this rule almost everywhere, and the shape of the misses
// is why this exists rather than a rule in a comment: the ledger's own
// Append checks its Close, and writeAnchor — the same file, 190 lines up,
// writing the record that tells rta's retention apart from a deleted
// segment — did not. Two of the plugins had the same split, each with a
// correct sibling in the same module. Nobody writes this wrong on purpose;
// it is written wrong by copying the read-side spelling, where discarding
// Close is right.
//
// No linter catches it: `_ = f.Close()` is an explicit discard, which is
// exactly what errcheck asks for, and .golangci.yml excludes
// `(io.Closer).Close` besides.
//
// The check is per-variable rather than per-function — the handle that a
// write-mode open returned is the one whose Close has to be read — and it
// asks only that *some* Close of that variable is read, since a function
// that closes on the error path with `_ =` and on the success path with a
// check is correct.
func TestAFileOpenedForWritingHasItsCloseRead(t *testing.T) {
	forEachSource(t, func(rel string, file *ast.File, fset *token.FileSet) {
		if _, ok := closeAllowed[rel]; ok {
			return
		}
		for _, fn := range funcsIn(file) {
			for name, pos := range writeHandles(fn) {
				if closeIsRead(fn, name) {
					continue
				}
				t.Errorf("%s: %s opens %q for writing and never reads its Close — "+
					"a Close that fails is a write that did not land",
					fset.Position(pos), funcName(fn), name)
			}
		}
	})
}

// Every exemption must still open a file for writing. One that moved or was
// rewritten is an exemption nobody is watching.
func TestEveryCloseExemptionIsStillNeeded(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()
	for rel := range closeAllowed {
		path := filepath.Join(root, filepath.FromSlash(rel))
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Errorf("%s is exempted but cannot be read: %v", rel, err)
			continue
		}
		found := false
		for _, fn := range funcsIn(file) {
			if len(writeHandles(fn)) > 0 {
				found = true
			}
		}
		if !found {
			t.Errorf("%s is exempted but no longer opens a file for writing — drop the exemption", rel)
		}
	}
}

// forEachSource walks rta's own non-test Go files.
func forEachSource(t *testing.T, fn func(rel string, file *ast.File, fset *token.FileSet)) {
	t.Helper()
	root := repoRoot(t)
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == "testdata" || name == "examples" || name == "proto" {
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
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		fn(filepath.ToSlash(rel), file, fset)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// funcsIn is every function and method body in a file, plus the function
// literals inside them — a closure that opens a file for writing is as much
// a writer as a named function is.
func funcsIn(file *ast.File) []ast.Node {
	var out []ast.Node
	ast.Inspect(file, func(n ast.Node) bool {
		switch fn := n.(type) {
		case *ast.FuncDecl:
			if fn.Body != nil {
				out = append(out, n)
			}
		case *ast.FuncLit:
			out = append(out, n)
		}
		return true
	})
	return out
}

func funcName(fn ast.Node) string {
	if d, ok := fn.(*ast.FuncDecl); ok {
		return d.Name.Name
	}
	return "a function literal"
}

func bodyOf(fn ast.Node) *ast.BlockStmt {
	switch f := fn.(type) {
	case *ast.FuncDecl:
		return f.Body
	case *ast.FuncLit:
		return f.Body
	}
	return nil
}

// writeHandles maps each variable in fn that holds a file opened for
// writing to where it was opened.
//
// os.Create and os.CreateTemp are writes by definition. os.OpenFile is one
// when its flags say so — O_CREATE alone is not enough, since a read of a
// file that may not exist is spelled that way too.
func writeHandles(fn ast.Node) map[string]token.Pos {
	body := bodyOf(fn)
	if body == nil {
		return nil
	}
	out := map[string]token.Pos{}
	for _, stmt := range body.List {
		assign, ok := stmt.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 || len(assign.Lhs) == 0 {
			continue
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok {
			continue
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "os" {
			continue
		}
		switch sel.Sel.Name {
		case "Create", "CreateTemp":
		case "OpenFile":
			if len(call.Args) < 2 || !writeFlags(call.Args[1]) {
				continue
			}
		default:
			continue
		}
		if name, ok := assign.Lhs[0].(*ast.Ident); ok && name.Name != "_" {
			out[name.Name] = call.Pos()
		}
	}
	return out
}

// writeFlags reports whether an os.OpenFile flag expression asks for writing.
func writeFlags(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch sel.Sel.Name {
		case "O_WRONLY", "O_RDWR", "O_APPEND":
			found = true
		}
		return true
	})
	return found
}

// closeIsRead reports whether any `name.Close()` in fn has its result read —
// assigned to something other than the blank identifier, returned, or used
// as a condition. A bare call, a `defer name.Close()` and an `_ =` are all
// the same thing here: the error is gone.
func closeIsRead(fn ast.Node, name string) bool {
	body := bodyOf(fn)
	if body == nil {
		return false
	}
	read := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			for i, rhs := range node.Rhs {
				if !isCloseOf(rhs, name) {
					continue
				}
				if i < len(node.Lhs) {
					if id, ok := node.Lhs[i].(*ast.Ident); !ok || id.Name != "_" {
						read = true
					}
				}
			}
		case *ast.ReturnStmt:
			for _, res := range node.Results {
				if isCloseOf(res, name) {
					read = true
				}
			}
		case *ast.IfStmt:
			if isCloseOf(node.Cond, name) {
				read = true
			}
		case *ast.BinaryExpr:
			if isCloseOf(node.X, name) || isCloseOf(node.Y, name) {
				read = true
			}
		}
		return true
	})
	return read
}

func isCloseOf(expr ast.Expr, name string) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Close" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == name
}
