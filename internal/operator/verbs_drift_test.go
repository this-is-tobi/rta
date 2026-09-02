package operator

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// TestVerbsCarriesEveryVerbConstant parses the Verb* constants out of
// wire.go and asserts Verbs() returns exactly their values.
//
// Verbs() is a hand-maintained list beside the constants, and a
// hand-written list catches drift only one way: a new VerbX constant
// added without extending Verbs() would dodge both the closed vocabulary
// and the ledger's recording-decision test that walks it — which is
// precisely the ship-unclassified path Verbs() exists to close. So the
// constants are read from the source rather than restated here, the same
// reasoning pkg/view's own drift test states for its view union.
func TestVerbsCarriesEveryVerbConstant(t *testing.T) {
	declared := map[string]bool{}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "wire.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 ||
				!strings.HasPrefix(vs.Names[0].Name, "Verb") {
				continue
			}
			lit, ok := vs.Values[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			val, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatal(err)
			}
			declared[val] = true
		}
	}
	if len(declared) == 0 {
		t.Fatal("no Verb* constants parsed out of wire.go — the parser drifted, not the vocabulary")
	}
	listed := map[string]bool{}
	for _, v := range Verbs() {
		listed[v] = true
	}
	for v := range declared {
		if !listed[v] {
			t.Errorf("wire.go declares %q and Verbs() does not list it — decide whether the ledger "+
				"records it (internal/mcp's recording test walks this list) and add it", v)
		}
	}
	for v := range listed {
		if !declared[v] {
			t.Errorf("Verbs() lists %q and wire.go declares no such constant", v)
		}
	}
}
