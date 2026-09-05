package kv

import (
	"context"
	"sort"
	"strings"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// kv.tree draws the store by the folders its names share.
//
// A key may contain slashes, and the store already treats what comes before
// one as a folder — `rta grant allow kv.get staging/` covers everything under
// it, and a name ending in a slash is refused for naming a folder rather than
// an entry. This is that convention made visible, the way `vault kv tree`
// shows a mount: names and kinds, never values, which is what keeps it a
// Read beside `kv list`. A store with no slashes in it is one folder of
// leaves, and says so by looking like one.
//
// Not bounded, unlike the other trees, because this store is on the local
// disk and its whole listing is what `kv list` already prints.
func treeCapability() plugin.Capability {
	return plugin.Capability{
		ID: "kv.tree", Summary: "The store as a tree of the folders its keys share",
		Safety: plugin.Read, Idempotent: true,
		Description: "Keys named with slashes — `staging/db/password`, `prod/deploy-key` — are " +
			"drawn as the folders they share, each leaf labelled with its kind. Names only, " +
			"never values, the same line `kv list` draws. `rta grant allow kv.get staging/` is " +
			"how one of those folders becomes a grant's scope, so this is also the map of what " +
			"such a grant covers.",
		Inputs: unlockFields(),
		Run:    runTree,
	}
}

func runTree(_ context.Context, req plugin.Request) (view.View, error) {
	s, verr := load(req)
	if verr != nil {
		return nil, verr
	}
	if len(s.Entries) == 0 {
		return view.Text{Body: emptyList(0, "", "")}, nil
	}
	names := make([]string, 0, len(s.Entries))
	for k := range s.Entries {
		names = append(names, k)
	}
	sort.Strings(names)

	root := &treeNode{}
	for _, k := range names {
		parts := strings.Split(k, "/")
		cur := root
		for _, p := range parts[:len(parts)-1] {
			cur = cur.child(p)
		}
		leaf := cur.child(parts[len(parts)-1])
		leaf.kind = s.Entries[k].Kind
	}
	return view.Tree{Roots: root.render()}, nil
}

// treeNode is a folder or a leaf while the tree is being built; the order
// of insertion is the sorted order of the keys, which is the order drawn.
type treeNode struct {
	label    string
	kind     string
	children []*treeNode
}

func (n *treeNode) child(label string) *treeNode {
	for _, c := range n.children {
		if c.label == label {
			return c
		}
	}
	c := &treeNode{label: label}
	n.children = append(n.children, c)
	return c
}

func (n *treeNode) render() []view.Node {
	out := make([]view.Node, 0, len(n.children))
	for _, c := range n.children {
		node := view.Node{Label: c.label, Detail: c.kind}
		if len(c.children) > 0 {
			node.Label += "/"
			node.Detail = plural(c.leaves(), "key")
			node.Children = c.render()
		}
		out = append(out, node)
	}
	return out
}

func (n *treeNode) leaves() int {
	if len(n.children) == 0 {
		return 1
	}
	total := 0
	for _, c := range n.children {
		total += c.leaves()
	}
	return total
}
