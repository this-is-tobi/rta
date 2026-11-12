package kv

import (
	"context"
	"sort"
	"strings"

	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
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
// disk and its whole listing is what `kv list` already prints. Narrowed by
// folder as `kv list` is: a store of a few thousand keys drew a tree past what
// an MCP answer holds, and the refusal told the agent to narrow a call that
// took no input to narrow it with.
func treeCapability() plugin.Capability {
	return plugin.Capability{
		ID: "kv.tree", Summary: "The store as a tree of the folders its keys share",
		Safety: plugin.Read, Idempotent: true,
		Description: "Keys named with slashes — `staging/db/password`, `prod/deploy-key` — are " +
			"drawn as the folders they share, each leaf labelled with its kind. Names only, " +
			"never values, the same line `kv.list` draws. A grant for `kv.get` scoped to staging/ is " +
			"how one of those folders becomes a grant's scope, so this is also the map of what " +
			"such a grant covers.",
		Inputs: unlockFields(folderField),
		Run:    runTree,
	}
}

func runTree(ctx context.Context, req plugin.Request) (view.View, error) {
	s, verr := load(ctx, req)
	if verr != nil {
		return nil, verr
	}
	// As kv.list reads it: "db" and "db/" both name the folder.
	folder := strings.TrimSuffix(strings.TrimSpace(req.String("folder")), "/")
	names := make([]string, 0, len(s.Entries))
	for k := range s.Entries {
		if folder == "" || strings.HasPrefix(k, folder+"/") {
			names = append(names, k)
		}
	}
	// An empty store is a tree with no roots — an empty array, not a
	// missing one, so `jq '.roots[]'` yields nothing and exits 0 — with the
	// sentence beside it for a person: see view.Tree.Empty. It answered with
	// that sentence as a Text view, which a parser met in place of the tree.
	if len(names) == 0 {
		empty := emptyList(req.Surface(), len(s.Entries), folder, "", "")
		// A key named where a folder goes holds no keys, and said so as an
		// empty folder: `kv tree prod/db/password` answered "No key in
		// prod/db/password/" about the key it had just been given.
		if _, isKey := s.Entries[folder]; isKey && folder != "" {
			empty = textclean.Record(folder) + " is a key, not a folder — " +
				req.Surface().Call("kv.show", plugin.Arg{Name: "key", Value: folder, Positional: true}) + " describes it"
		}
		return view.Tree{Roots: []view.Node{}, Empty: empty}, nil
	}
	sort.Strings(names)

	// Narrowed to a folder, the tree is rooted at it: `kv tree prod/db` drew
	// "prod/ 2 keys" above db/, a count of prod/ that left out every key of
	// prod/ outside db/, beside a `kv tree` that counts three there.
	root := &treeNode{}
	top := root
	if folder != "" {
		top = root.child(folder, true)
	}
	for _, k := range names {
		parts := strings.Split(strings.TrimPrefix(k, folderPrefix(folder)), "/")
		cur := top
		for _, p := range parts[:len(parts)-1] {
			cur = cur.child(p, true)
		}
		leaf := cur.child(parts[len(parts)-1], false)
		leaf.kind = s.Entries[k].Kind
	}
	return view.Tree{Roots: root.render()}, nil
}

// folderPrefix is folder as the start of a key under it, "" for the whole store.
func folderPrefix(folder string) string {
	if folder == "" {
		return ""
	}
	return folder + "/"
}

// treeNode is a folder or a leaf while the tree is being built; the order
// of insertion is the sorted order of the keys, which is the order drawn.
//
// A folder and a leaf of the same label are two nodes: a key `a` beside `a/top`
// is an entry and a folder, the store holds both, and `kv.list` and the count
// say so. One node per label drew the folder alone, and the entry was on every
// page but the map.
type treeNode struct {
	label    string
	folder   bool
	kind     string
	children []*treeNode
}

func (n *treeNode) child(label string, folder bool) *treeNode {
	for _, c := range n.children {
		if c.label == label && c.folder == folder {
			return c
		}
	}
	c := &treeNode{label: label, folder: folder}
	n.children = append(n.children, c)
	return c
}

// render draws each label as a listed name (plugin.ListedName): a key's name is
// whatever its writer typed, and cleaned of its control characters two names
// that differ drew alike, while a newline in one started a line of the map a
// reader took for another entry.
func (n *treeNode) render() []view.Node {
	out := make([]view.Node, 0, len(n.children))
	for _, c := range n.children {
		node := view.Node{Label: plugin.ListedName(c.label), Detail: c.kind}
		if c.folder {
			node.Label += "/"
			node.Detail = format.CountOf(c.leaves(), "key")
			node.Children = c.render()
		}
		out = append(out, node)
	}
	return out
}

func (n *treeNode) leaves() int {
	if !n.folder {
		return 1
	}
	total := 0
	for _, c := range n.children {
		total += c.leaves()
	}
	return total
}
