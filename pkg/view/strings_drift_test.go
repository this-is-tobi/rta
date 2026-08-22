package view

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"testing"
)

// TestMapStringsHandlesEveryViewType parses the view union out of the source
// and asserts mapStrings has a case for each member.
//
// A hand-written list of the types is what this test would naturally be, and
// it only catches drift one way. Adding an eighth view type and forgetting to
// sanitize it does not fail anything: the new type falls through sanitize's
// default, renders unfiltered, and the suite stays green — which is the exact
// shape of the bug this whole file exists for, one view type over. So the
// union is read from pkg/view rather than restated here.
func TestMapStringsHandlesEveryViewType(t *testing.T) {
	union := map[string]bool{}
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Name.Name != "isView" || fn.Recv == nil || len(fn.Recv.List) != 1 {
					continue
				}
				switch rt := fn.Recv.List[0].Type.(type) {
				case *ast.Ident:
					union[rt.Name] = true
				case *ast.StarExpr:
					if id, ok := rt.X.(*ast.Ident); ok {
						union[id.Name] = true
					}
				}
			}
		}
	}
	if len(union) < 7 {
		t.Fatalf("found only %v — the parse is wrong, not the code", union)
	}

	handled := map[string]bool{}
	f, err := parser.ParseFile(fset, "strings.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		cc, ok := n.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, e := range cc.List {
			if star, ok := e.(*ast.StarExpr); ok {
				e = star.X
			}
			if id, ok := e.(*ast.Ident); ok {
				handled[id.Name] = true
			}
		}
		return true
	})
	for name := range union {
		if !handled[name] {
			t.Errorf("%s is a member of the view union with no case in mapStrings, so every "+
				"consumer of MapStrings silently skips it", name)
		}
	}
}

// exemptFromMapping names string fields MapStrings deliberately leaves alone,
// with the reason. Anything not listed here must be visited.
//
// It is empty, and that is the strongest form this can take. It held
// Error.Code and Section.ID, both exempted as identifiers constrained where
// they are produced — true while every one of them was a literal in rta's own
// source, false the moment a plugin could send one over the wire.
//
// The map stays because the next field that genuinely should not be mapped
// needs somewhere to say so, and a sentence explaining why.
var exemptFromMapping = map[string]string{}

// Every plain-string field in the view union must be visited by MapStrings.
//
// TestMapStringsHandlesEveryViewType checks each view *type* has a case, which
// is one of the two ways this drifts. The other is a case that exists and
// skips a field, and it had happened twice: Table.Page.Next reached a model
// uncleaned, and Table.Redacted/KeyValue.Redacted were left behind while the
// names they are matched against were rewritten — which silently took the
// mask off a secret.
//
// Reflection rather than a list of fields, for the same reason the sibling
// test parses the union rather than restating it: a hand-written list only
// catches drift in the direction somebody remembered.
func TestMapStringsVisitsEveryStringField(t *testing.T) {
	for _, proto := range []View{Text{}, KeyValue{}, Table{}, Tree{}, Chart{}, Sections{}} {
		rt := reflect.TypeOf(proto)
		pv := reflect.New(rt)
		want := map[string]string{}
		fillStrings(pv.Elem(), rt.Name(), want, 0)

		seen := map[string]bool{}
		MapStrings(pv.Elem().Interface().(View), func(s string) string {
			seen[s] = true
			return s
		})

		for sentinel, path := range want {
			if seen[sentinel] {
				continue
			}
			if why, ok := exemptFromMapping[path]; ok {
				t.Logf("%s is deliberately not mapped: %s", path, why)
				continue
			}
			t.Errorf("MapStrings never visits %s, so every consumer of it — the terminal "+
				"renderers and the MCP bridge — passes that string through unfiltered", path)
		}
	}
}

// fillStrings writes a unique sentinel into every settable plain-string field
// reachable from v, recording where each one went.
//
// depth bounds it because the union is recursive: Node holds []Node and
// Section holds a View. Two levels is enough to reach every distinct field
// while terminating.
func fillStrings(v reflect.Value, path string, out map[string]string, depth int) {
	if depth > 3 {
		return
	}
	switch v.Kind() {
	case reflect.String:
		// Exactly `string`, not named string types: ColumnKind and ChartKind
		// are closed enums matched by value, and rewriting one would not
		// sanitise anything, it would invalidate it.
		if v.Type() == reflect.TypeOf("") && v.CanSet() {
			s := fmt.Sprintf("SENTINEL%d", len(out))
			out[s] = path
			v.SetString(s)
		}
	case reflect.Pointer:
		if v.IsNil() && v.CanSet() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		if !v.IsNil() {
			fillStrings(v.Elem(), path, out, depth+1)
		}
	case reflect.Slice:
		if v.CanSet() {
			v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		}
		if v.Len() > 0 {
			fillStrings(v.Index(0), path+"[0]", out, depth+1)
		}
	case reflect.Interface:
		if v.CanSet() && v.Type().Name() == "View" {
			inner := reflect.New(reflect.TypeOf(Text{})).Elem()
			fillStrings(inner, path+".(Text)", out, depth+1)
			v.Set(inner)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if f.PkgPath != "" {
				continue // unexported
			}
			fillStrings(v.Field(i), path+"."+f.Name, out, depth+1)
		}
	}
}
