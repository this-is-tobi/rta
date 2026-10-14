package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// secretsWiredIn is the reader an mcp.Options literal in the named source is
// given as its Secrets, spelled as written ("kv.RevealForServer"); src is the
// source, or nil to read the file.
func secretsWiredIn(t *testing.T, name string, src any) string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), name, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var wired string
	ast.Inspect(file, func(n ast.Node) bool {
		field, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		if key, ok := field.Key.(*ast.Ident); !ok || key.Name != "Secrets" {
			return true
		}
		if sel, ok := field.Value.(*ast.SelectorExpr); ok {
			if pkg, ok := sel.X.(*ast.Ident); ok {
				wired = pkg.Name + "." + sel.Sel.Name
			}
		}
		return true
	})
	return wired
}

// The server opens the store with the reader that words a refusal for the
// operator (kv.RevealForServer), because a refused credential is answered to
// the agent in a sentence of its own and the real one is read on the record by
// the operator. The wiring is one line in a function that needs a running
// server to reach, and the reader it replaced said "ask the operator to run"
// to the person who is the operator, so the line is read here rather than run.
func TestTheServerReadsItsSecretsWithTheReaderWordedForTheOperator(t *testing.T) {
	if wired := secretsWiredIn(t, "mcp.go", nil); wired != "kv.RevealForServer" {
		t.Errorf("mcp.Options.Secrets is %q, want kv.RevealForServer: the record's note is read by the "+
			"operator, and kv.Reveal words every hint as a step to ask them for", wired)
	}
	// What the reading above would see if the line went back: it has to tell the
	// reader that was there from the one that is.
	const before = "package app\nvar _ = mcp.Options{\n\tSecrets: kv.Reveal,\n}\n"
	if wired := secretsWiredIn(t, "before.go", before); wired != "kv.Reveal" {
		t.Errorf("the reading finds %q in the wiring that was replaced, want kv.Reveal", wired)
	}
}
