package kv

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// A key's name is whatever its writer typed, and an agent with a grant to
// write can be that writer. Listing it cleaned of its control characters made
// two names that differ read as one (an escape sequence inside `password`
// vanished, and both rows said `prod/db/password`), and a newline in it started
// a second line of the listing that a reader took for a second entry. A name is
// listed as every other listed name is: as itself when it reads as itself, and
// otherwise in quotes with each character a person would not see written out.
const (
	nameWithEscape   = "prod/db/pass\x1b[0mword"
	nameWithNewline  = "nl-key\nlocked to     yes - forged line"
	nameWithOverride = "\u202eevilfolder/x"
)

func TestKVListShowsEveryNameAsItIs(t *testing.T) {
	setup(t)
	setKeys(t, "prod/db/password", nameWithEscape, nameWithNewline, nameWithOverride)

	out, err := runList(context.Background(), req(map[string]any{}, false))
	if err != nil {
		t.Fatal(err)
	}
	tbl := out.(view.Table)
	if len(tbl.Rows) != 4 {
		t.Fatalf("%d rows for 4 keys", len(tbl.Rows))
	}
	seen := map[string]bool{}
	for _, row := range tbl.Rows {
		name := row[0]
		if strings.ContainsAny(name, "\x1b\n\u202e") {
			t.Errorf("a name reached the listing with a character a reader cannot see: %q", name)
		}
		if seen[name] {
			t.Errorf("two different keys are listed as %q", name)
		}
		seen[name] = true
	}
	if !seen["prod/db/password"] {
		t.Errorf("a plain name was changed: %v", seen)
	}
}

func TestKVTreeShowsEveryNameAsItIs(t *testing.T) {
	setup(t)
	setKeys(t, "prod/db/password", nameWithEscape, nameWithNewline, nameWithOverride)

	out, err := runTree(context.Background(), req(map[string]any{}, false))
	if err != nil {
		t.Fatal(err)
	}
	var walk func(nodes []view.Node)
	labels := map[string]int{}
	walk = func(nodes []view.Node) {
		for _, n := range nodes {
			if strings.ContainsAny(n.Label, "\x1b\n\u202e") {
				t.Errorf("a label reached the tree with a character a reader cannot see: %q", n.Label)
			}
			labels[n.Label]++
			walk(n.Children)
		}
	}
	walk(out.(view.Tree).Roots)
	for label, n := range labels {
		if n > 1 {
			t.Errorf("two different nodes are both drawn as %q", label)
		}
	}
}

func TestKVRemovedListShowsEveryNameAsItIs(t *testing.T) {
	setup(t)
	setKeys(t, nameWithEscape, nameWithNewline)
	for _, k := range []string{nameWithEscape, nameWithNewline} {
		text(t, runRemove, map[string]any{"key": k}, false)
	}
	out, err := runList(context.Background(), req(map[string]any{"removed": true}, false))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range out.(view.Table).Rows {
		if strings.ContainsAny(row[0], "\x1b\n") {
			t.Errorf("a removed name reached the listing with a character a reader cannot see: %q", row[0])
		}
	}
}
