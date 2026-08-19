package plugin

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func noop(context.Context, Request) (view.View, error) { return view.Text{Body: "ok"}, nil }

func validPlugin() Plugin {
	return Plugin{
		Name:    "demo",
		Summary: "demo plugin",
		Capabilities: []Capability{
			{ID: "demo.item.list", Summary: "list items", Safety: Read, Run: noop},
			{ID: "demo.item.rm", Summary: "remove item", Safety: Destructive, Run: noop},
		},
	}
}

func TestValidateOK(t *testing.T) {
	if err := validPlugin().Validate(); err != nil {
		t.Fatalf("valid plugin rejected: %v", err)
	}
}

func TestValidateFailures(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Plugin)
		wantSub string
	}{
		{"bad name", func(p *Plugin) { p.Name = "Demo!" }, "lowercase"},
		{"no capabilities", func(p *Plugin) { p.Capabilities = nil }, "no capabilities"},
		{"bad id", func(p *Plugin) { p.Capabilities[0].ID = "demo" }, "segments"},
		{"too many segments", func(p *Plugin) { p.Capabilities[0].ID = "demo.a.b.c" }, "segments"},
		{"wrong namespace", func(p *Plugin) { p.Capabilities[0].ID = "other.item.list" }, "namespace"},
		{"missing summary", func(p *Plugin) { p.Capabilities[0].Summary = "" }, "summary"},
		{"bad safety", func(p *Plugin) { p.Capabilities[0].Safety = "yolo" }, "safety"},
		{"nil handler", func(p *Plugin) { p.Capabilities[0].Run = nil }, "handler"},
		{"dup id", func(p *Plugin) { p.Capabilities[1].ID = p.Capabilities[0].ID; p.Capabilities[1].Safety = Read }, "duplicate"},
		{"bad field", func(p *Plugin) {
			p.Capabilities[0].Inputs = []Field{{Name: "BadName", Type: String}}
		}, "field name"},
		{"unknown field type", func(p *Plugin) {
			p.Capabilities[0].Inputs = []Field{{Name: "port", Type: "integer"}}
		}, "unknown type"},
		{"scope names no input", func(p *Plugin) { p.Capabilities[0].Scope = "nope" }, "names no input"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validPlugin()
			tt.mutate(&p)
			err := p.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("want error containing %q, got %v", tt.wantSub, err)
			}
		})
	}
}

// Scope is what lets a grant narrow to one record instead of the whole
// capability, and it narrows by naming an input the caller filled in. A Scope
// naming nothing — "keys" where the field is "key" — leaves the gate with no
// value to match against, so every grant issued on that capability silently
// covers every record it can reach: exactly the "read the staging token" that
// turns out to mean "read every secret I own" (PROJECT.md §4.7.11). Nothing
// else in the system would notice, because the grant still looks scoped in
// `rta grant list`.
func TestScopeMustNameADeclaredInput(t *testing.T) {
	p := validPlugin()
	p.Capabilities[0].Scope = "key"
	p.Capabilities[0].Inputs = []Field{{Name: "keys", Type: StringSlice, Help: "keys to read"}}

	err := p.Validate()
	if err == nil {
		t.Fatal(`a scope naming no declared input was accepted`)
	}
	if !strings.Contains(err.Error(), "names no input") {
		t.Errorf("the error should say why: %v", err)
	}

	// Spelled to match a declared input, the same capability is fine — the
	// rule rejects the dangling reference, not the act of scoping.
	p.Capabilities[0].Inputs = []Field{{Name: "key", Type: String, Help: "key to read"}}
	if err := p.Validate(); err != nil {
		t.Errorf("a scope naming a declared input was rejected: %v", err)
	}
}

// Field.Type was validated nowhere, and every surface switches on it with a
// default branch meaning "string". So a capability declaring Type: "integer" —
// JSON Schema's spelling, and the obvious thing to reach for — got a string
// --port flag, {"type": "string"} in its published MCP schema, and a handler
// whose req.Int("port") returned 0, with nothing anywhere reporting a problem.
func TestAnUnrecognisedFieldTypeIsRejected(t *testing.T) {
	declared := []FieldType{String, Int, Bool, Float, StringSlice, Text, Path, Secret}

	p := validPlugin()
	p.Capabilities[0].Inputs = []Field{{Name: "port", Type: "integer", Help: "port to probe"}}

	err := p.Validate()
	if err == nil {
		t.Fatal(`an input declaring Type "integer" was accepted`)
	}
	// An author who reached for the wrong spelling needs the right one, not a
	// verdict, so the rejection carries the whole accepted set. Only the half
	// after the offer is searched, since "integer" itself contains "int".
	_, accepted, ok := strings.Cut(err.Error(), "want one of ")
	if !ok {
		t.Fatalf("the rejection should offer the accepted types: %v", err)
	}
	for _, ft := range declared {
		if !strings.Contains(accepted, string(ft)) {
			t.Errorf("the rejection should name %q as an option: %v", ft, err)
		}
	}

	// Every declared type is accepted, so the rule catches misspellings rather
	// than narrowing the contract. The built-in catalogue uses all of these but
	// Float; it stays accepted because removing a declared constant from the
	// set is the same breaking change one direction over.
	for _, ft := range declared {
		p := validPlugin()
		p.Capabilities[0].Inputs = []Field{{Name: "input", Type: ft}}
		if err := p.Validate(); err != nil {
			t.Errorf("declared type %q was rejected: %v", ft, err)
		}
	}
}

func TestRequestAccessors(t *testing.T) {
	r := NewRequest(map[string]any{
		"s": "str", "i": 42, "i64": int64(7), "f": 3.5, "fi": 2,
		"b": true, "ss": []string{"a", "b"}, "ssa": []any{"x", 1},
	}, true, false)
	if r.String("s") != "str" || r.String("missing") != "" {
		t.Error("String accessor")
	}
	if r.Int("i") != 42 || r.Int("i64") != 7 || r.Int("missing") != 0 {
		t.Error("Int accessor")
	}
	if r.Float("f") != 3.5 || r.Float("fi") != 2 {
		t.Error("Float accessor")
	}
	if !r.Bool("b") || r.Bool("missing") {
		t.Error("Bool accessor")
	}
	if got := r.StringSlice("ss"); len(got) != 2 || got[0] != "a" {
		t.Error("StringSlice accessor")
	}
	if got := r.StringSlice("ssa"); len(got) != 2 || got[1] != "1" {
		t.Error("StringSlice from []any")
	}
	if !r.DryRun || r.Yes {
		t.Error("flags not carried")
	}
}

// A bare string in a StringSlice slot means one value, not none. An MCP
// client is not schema-checked before its arguments reach here (the SDK's
// own contract makes that the caller's responsibility), so {"key": "x"}
// instead of {"key": ["x"]} is a call this has to interpret the same way
// internal/grant does when it reads the identical raw value to decide what a
// grant covers — two readers disagreeing here is how a per-key grant on
// kv.env ends up exporting the whole store (see internal/grant/grant_test.go
// TestScalarAndSliceFormsAgreeOnScope for the sharp case).
func TestStringSliceAcceptsABareString(t *testing.T) {
	r := NewRequest(map[string]any{"tag": "solo", "empty": ""}, false, false)
	if got := r.StringSlice("tag"); len(got) != 1 || got[0] != "solo" {
		t.Errorf("StringSlice(scalar) = %v, want one value", got)
	}
	// An empty string is "nothing said", the same as an absent key — not a
	// list holding one empty entry.
	if got := r.StringSlice("empty"); got != nil {
		t.Errorf("StringSlice(\"\") = %v, want nil", got)
	}
	if got := r.StringSlice("missing"); got != nil {
		t.Errorf("StringSlice(missing) = %v, want nil", got)
	}
}

func TestWords(t *testing.T) {
	c := Capability{ID: "pg.table.list"}
	w := c.Words()
	if len(w) != 3 || w[0] != "pg" || w[2] != "list" {
		t.Errorf("Words() = %v", w)
	}
}

// TestFieldTypesCoversEveryDeclaredConstant walks the FieldType const block in
// the source and asserts fieldTypes lists all of it.
//
// A hand-written list of the constants — which is what the other test does,
// and what any test of a closed set naturally reaches for — only catches drift
// in one direction. Deleting a type from fieldTypes fails loudly. Adding a
// ninth FieldType constant and forgetting to add it to fieldTypes does not:
// the new type becomes a value Validate rejects, every surface grows a case
// for it that can never run, and the suite stays green. That is the specific
// failure a closed set invites, so the source is the only honest oracle.
func TestFieldTypesCoversEveryDeclaredConstant(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "plugin.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var declared []string
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			id, ok := vs.Type.(*ast.Ident)
			if !ok || id.Name != "FieldType" {
				continue
			}
			for _, name := range vs.Names {
				declared = append(declared, name.Name)
			}
		}
	}
	if len(declared) == 0 {
		t.Fatal("found no FieldType constants — the parse is wrong, not the code")
	}

	listed := map[FieldType]bool{}
	for _, ft := range fieldTypes {
		listed[ft] = true
	}
	byName := map[string]FieldType{
		"String": String, "Int": Int, "Bool": Bool, "Float": Float,
		"StringSlice": StringSlice, "Text": Text, "Path": Path, "Secret": Secret,
	}
	for _, name := range declared {
		ft, known := byName[name]
		if !known {
			t.Fatalf("FieldType %s was added to plugin.go but not to this test's map, "+
				"so nothing checks whether fieldTypes lists it", name)
		}
		if !listed[ft] {
			t.Errorf("FieldType %s (%q) is declared but missing from fieldTypes, so Validate "+
				"rejects every plugin that uses it", name, ft)
		}
	}
	if len(declared) != len(fieldTypes) {
		t.Errorf("%d FieldType constants but %d in fieldTypes", len(declared), len(fieldTypes))
	}
}

// An input with no Type at all validated before, and behaved as a string on
// every surface. Rejecting it is the freeze-relevant half of the closed-set
// decision and the one most likely to be quietly reverted by the first
// "be lenient, default empty to String" patch — so it is pinned separately,
// and the message is checked for saying which mistake it was.
func TestAnInputMustDeclareItsType(t *testing.T) {
	p := validPlugin()
	p.Capabilities[0].Inputs = []Field{{Name: "host", Help: "host to reach"}}

	err := p.Validate()
	if err == nil {
		t.Fatal("an input declaring no type was accepted; Secret and Path are what make a " +
			"value masked and completable, and neither can be inferred from a name")
	}
	if !strings.Contains(err.Error(), "declares no type") {
		t.Errorf("omission and misspelling are different mistakes and need different "+
			"messages: %v", err)
	}
	if !strings.Contains(err.Error(), string(Secret)) {
		t.Errorf("the rejection should name the accepted types: %v", err)
	}
}
