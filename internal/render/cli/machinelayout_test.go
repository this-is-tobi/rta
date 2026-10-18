package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// What pretty draws as layout is not data in the formats a program reads. A
// key indented under the one it belongs to (`profile show`) and the last line
// of a branch that has entries it does not list (`fs tree`) reached `-o json`
// as a key with two spaces in it and as a child named "…". All four machine
// formats leave them out and say a tree was cut in its warnings; pretty keeps
// what it draws.

func layoutKeys() view.KeyValue {
	return view.KeyValue{Pairs: []view.Pair{
		{Key: "profile", Value: "staging"},
		{Key: "  set:timeout", Value: "10"},
	}}
}

func cutDirectory() view.Tree {
	return view.Tree{Roots: []view.Node{{
		Label: "project/", Detail: "/work/project",
		Children: []view.Node{
			{Label: "a.md", Detail: "2 B"},
			{Label: view.CutLabel, Detail: "2 more"},
			{Label: view.CutLabel, Detail: "1 hidden (--all)"},
		},
	}}}
}

func rendered(t *testing.T, v view.View, f Format) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Render(&buf, v, Options{Format: f, NoColor: true, Width: 100}); err != nil {
		t.Fatalf("%s: %v", f, err)
	}
	return buf.String()
}

func TestNoMachineFormatCarriesAKeysLayout(t *testing.T) {
	for _, f := range []Format{JSON, YAML, CSV, Markdown} {
		out := rendered(t, layoutKeys(), f)
		if !strings.Contains(out, "set:timeout") {
			t.Errorf("%s lost the key:\n%s", f, out)
		}
		if strings.Contains(out, "  set:timeout") {
			t.Errorf("%s carries the indent pretty gave the key:\n%s", f, out)
		}
	}
	if out := rendered(t, layoutKeys(), Pretty); !strings.Contains(out, "  set:timeout") {
		t.Errorf("pretty lost the indent that shows which block a key belongs to:\n%s", out)
	}
}

func TestNoMachineFormatDrawsACutTreeAsAnEntry(t *testing.T) {
	for _, f := range []Format{JSON, YAML, CSV, Markdown} {
		out := rendered(t, cutDirectory(), f)
		if strings.Contains(out, view.CutLabel+" ") || strings.Contains(out, "\""+view.CutLabel+"\"") ||
			strings.Contains(out, "- "+view.CutLabel) {
			t.Errorf("%s draws the marker as an entry:\n%s", f, out)
		}
		for _, want := range []string{"core.tree.cut", "project/: 2 more", "project/: 1 hidden (--all)"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s does not say %q:\n%s", f, want, out)
			}
		}
		if !strings.Contains(out, "a.md") {
			t.Errorf("%s lost the entry the tree lists:\n%s", f, out)
		}
	}
	out := rendered(t, cutDirectory(), Pretty)
	for _, want := range []string{"… 2 more", "… 1 hidden (--all)"} {
		if !strings.Contains(out, want) {
			t.Errorf("pretty lost its last lines (%q):\n%s", want, out)
		}
	}
}

// Rendering twice is the same: a view is drawn again by the TUI after it was
// written to a file, and neither may edit the other's.
func TestEncodingACutTreeLeavesItForPretty(t *testing.T) {
	tree := cutDirectory()
	first := rendered(t, tree, Pretty)
	_ = rendered(t, tree, JSON)
	_ = rendered(t, tree, Markdown)
	if again := rendered(t, tree, Pretty); again != first {
		t.Errorf("pretty changed after the machine formats ran:\n%s\nthen\n%s", first, again)
	}
}
