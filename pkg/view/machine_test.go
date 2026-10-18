package view

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// cutTree is a directory listing as fs tree builds it: a branch cut at its
// limit and with hidden entries, a branch it could not read, and the top level
// itself with entries it left out.
func cutTree() Tree {
	return Tree{Roots: []Node{{
		Label: "project/", Detail: "/work/project",
		Children: []Node{
			{Label: "docs/", Children: []Node{
				{Label: "a.md", Detail: "2 B"},
				{Label: CutLabel, Detail: "2 more"},
				{Label: CutLabel, Detail: "1 hidden (--all)"},
			}},
			{Label: "vault/", Children: []Node{{Label: CutLabel, Detail: "unreadable: permission denied"}}},
			{Label: "main.go", Detail: "9 B"},
			{Label: CutLabel, Detail: "4 hidden (--all)"},
		},
	}}}
}

func encoded(t *testing.T, v View) map[string]any {
	t.Helper()
	data, err := Marshal(Envelope{View: v})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, data)
	}
	return m
}

// A key that pretty indents under the one it belongs to carries that layout in
// its text, and a program reads the text: `profile show -o json` had the key
// "  set:timeout", with two spaces a script has to know to trim. The key is its
// name in every encoding, the list of masked keys is spelled the same way, and
// the view itself is not edited, since pretty draws the same one.
func TestAKeyIsEncodedWithoutTheLayoutPrettyGaveIt(t *testing.T) {
	kv := KeyValue{
		Pairs:    []Pair{{Key: "profile", Value: "staging"}, {Key: "  set:timeout", Value: "10"}, {Key: "  credential:password", Value: "●●●"}},
		Redacted: []string{"  credential:password"},
	}
	m := encoded(t, kv)
	pairs := m["pairs"].([]any)
	var keys []string
	for _, p := range pairs {
		keys = append(keys, p.(map[string]any)["key"].(string))
	}
	if want := []string{"profile", "set:timeout", "credential:password"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %q, want %q", keys, want)
	}
	if got := m["redacted"].([]any); len(got) != 1 || got[0] != "credential:password" {
		t.Errorf("redacted = %v, want the masked key spelled as its pair is", got)
	}
	if kv.Pairs[1].Key != "  set:timeout" || kv.Redacted[0] != "  credential:password" {
		t.Errorf("the view was edited to encode it: %+v", kv)
	}
}

// A tree that did not list everything draws a last line saying so, `…  2 more`,
// and that line is a node: `jq '.roots[].children[]'` met an entry named "…"
// that is no entry of the directory. The encoding has no node for it. What it
// stood for is in `warnings`, as a table's is, one for each place a branch was
// cut, naming the branch and what was left out.
func TestATreeIsEncodedWithoutItsCutMarkersAndSaysWhereItWasCut(t *testing.T) {
	tree := cutTree()
	m := encoded(t, tree)

	var labels []string
	var walk func(nodes []any)
	walk = func(nodes []any) {
		for _, n := range nodes {
			n := n.(map[string]any)
			labels = append(labels, n["label"].(string))
			if kids, ok := n["children"].([]any); ok {
				walk(kids)
			}
		}
	}
	walk(m["roots"].([]any))
	if want := []string{"project/", "docs/", "a.md", "vault/", "main.go"}; !reflect.DeepEqual(labels, want) {
		t.Errorf("labels = %q, want %q: a cut marker is not an entry", labels, want)
	}

	var got []string
	for _, w := range m["warnings"].([]any) {
		w := w.(map[string]any)
		if w["code"] != "core.tree.cut" {
			t.Errorf("warning code = %v, want core.tree.cut", w["code"])
		}
		if _, advisory := w["advisory"]; advisory {
			t.Errorf("a branch cut short is a gap, not a note beside a whole answer: %v", w)
		}
		got = append(got, w["message"].(string))
	}
	want := []string{
		"project/docs/: 2 more",
		"project/docs/: 1 hidden (--all)",
		"project/vault/: unreadable: permission denied",
		"project/: 4 hidden (--all)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("warnings = %q, want %q", got, want)
	}

	if !reflect.DeepEqual(tree, cutTree()) {
		t.Error("the tree was edited to encode it: pretty and the TUI draw the same one")
	}
}

// A marker at the very top of a tree is a marker too, and says it without a
// branch in front of it.
func TestATopLevelCutMarkerIsSaidWithoutABranch(t *testing.T) {
	m := encoded(t, Tree{Roots: []Node{{Label: "a"}, {Label: CutLabel, Detail: "5 more"}}})
	if got := m["roots"].([]any); len(got) != 1 {
		t.Errorf("roots = %v, want the one entry", got)
	}
	ws := m["warnings"].([]any)
	if len(ws) != 1 || ws[0].(map[string]any)["message"] != "5 more" {
		t.Errorf("warnings = %v", ws)
	}
}

// Only a leaf is a marker: a node of that name that has children is a branch
// somebody labelled so, and is kept with what is under it. A tree that was
// not cut has no `warnings` at all, and is encoded as it always was.
func TestOnlyALeafLabelledWithTheEllipsisIsACutMarker(t *testing.T) {
	m := encoded(t, Tree{Roots: []Node{{Label: CutLabel, Children: []Node{{Label: "x"}}}}})
	if roots := m["roots"].([]any); len(roots) != 1 {
		t.Errorf("a branch labelled with the ellipsis was dropped: %v", roots)
	}
	if _, has := m["warnings"]; has {
		t.Errorf("a tree that lost nothing carries warnings: %v", m["warnings"])
	}
	plain := encoded(t, Tree{Roots: []Node{{Label: "a", Detail: "1 B"}}})
	if _, has := plain["warnings"]; has {
		t.Errorf("a whole tree carries warnings: %v", plain["warnings"])
	}
}

// Sections nest views, and each is encoded as its own envelope, so a tree or
// a key/value page inside one is held to the same rule.
func TestTheRuleHoldsInsideSections(t *testing.T) {
	page := Sections{Items: []Section{
		{ID: "files", Title: "files", View: Tree{Roots: []Node{{Label: "a"}, {Label: CutLabel, Detail: "1 more"}}}},
		{ID: "profile", Title: "profile", View: KeyValue{Pairs: []Pair{{Key: "  set:timeout", Value: "10"}}}},
	}}
	data, err := Marshal(Envelope{View: page})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`"  set:timeout"`, `"…"`} {
		if strings.Contains(string(data), bad) {
			t.Errorf("encoded page still holds %s: %s", bad, data)
		}
	}
	if !strings.Contains(string(data), `"core.tree.cut"`) {
		t.Errorf("the nested tree's cut is not said: %s", data)
	}
}

// yaml and csv are written from ToMap, so they hold the same tree and the same
// warnings as json does.
func TestToMapHoldsWhatJSONHolds(t *testing.T) {
	m, err := ToMap(cutTree())
	if err != nil {
		t.Fatal(err)
	}
	ws, ok := m["warnings"].([]any)
	if !ok || len(ws) != 4 {
		t.Errorf("warnings = %v, want the four cuts", m["warnings"])
	}
}
