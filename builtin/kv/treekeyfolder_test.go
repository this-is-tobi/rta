package kv

import (
	"context"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// A key that is also the prefix of others is two things: an entry and a
// folder. The tree built one node per label, so `a` beside `a/top` drew the
// folder alone and the entry was in the store, in the listing and in the count
// but not on the map.
func TestKVTreeDrawsAKeyThatIsAlsoAFolder(t *testing.T) {
	setup(t)
	setKeys(t, "a", "a/top", "a/b/c/deep")

	out, err := runTree(context.Background(), req(map[string]any{}, false))
	if err != nil {
		t.Fatal(err)
	}
	roots := out.(view.Tree).Roots
	var leaf, folder *view.Node
	for i := range roots {
		switch roots[i].Label {
		case "a":
			leaf = &roots[i]
		case "a/":
			folder = &roots[i]
		}
	}
	if leaf == nil || folder == nil {
		t.Fatalf("the entry and the folder must both be drawn, got %+v", roots)
	}
	if len(leaf.Children) != 0 {
		t.Errorf("the entry has children: %+v", leaf)
	}
	if folder.Detail != "2 keys" {
		t.Errorf("the folder counts %q, want the two keys under it", folder.Detail)
	}
}
