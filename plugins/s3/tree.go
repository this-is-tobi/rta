package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/minio/minio-go/v7"

	"github.com/this-is-tobi/rule-them-all/pkg/format"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Learning the shape of somebody else's bucket, and where its space went.
//
// s3.object.list answers one level at a time: it groups on "/" and returns
// the prefixes at that level, so finding out what a bucket actually holds
// means retyping --prefix a level deeper over and over. That is the same
// problem vault.kv.list has, and it has the same fix — walk it once, draw the
// shape — but the walk itself is a different animal, and the difference is
// worth stating because it decides the bounds below.
//
// A KV mount has to be mapped one LIST per folder, so its tree is bounded on
// requests: every folder costs a round trip somebody else pays for and an
// audit device somewhere records. A bucket does not work that way. One
// ListObjects with Recursive:true streams every key under the prefix, and the
// folders are a fiction the "/" delimiter creates on the client side — so
// there is exactly one request here however deep the result goes, and the
// only thing worth bounding is how much of the stream is read.
//
// Which also means this can answer a question s3.object.list cannot. Every
// key arrives with its size, so aggregating on the way up is free, and the
// tree reports objects and bytes per prefix. "Where did four terabytes go" is
// the question people actually bring to a bucket, and it is unanswerable from
// a paginated flat listing.

const (
	// defaultTreeKeys bounds what is read off the stream. This is deliberately
	// far above s3.object.list's 200: that limit sizes a table somebody reads
	// row by row, and this one sizes an aggregate, where ten thousand keys
	// collapse into the twenty prefixes that matter. The cost of raising it is
	// a longer read of one already-open stream, not more requests.
	defaultTreeKeys = 2000
	// maxTreeNodes bounds what is rendered, which is a different question from
	// what was read and needs its own answer. A bucket with 500 distinct
	// prefixes at one level is not a tree anybody reads — past that the way to
	// find something is a narrower --prefix, and saying so is more useful than
	// printing a wall.
	maxTreeNodes = 500
)

func s3ObjectTreeCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:         "s3.object.tree",
		Summary:    "The shape of a bucket in one call, with objects and bytes per prefix",
		Safety:     plugin.Read,
		Idempotent: true,
		Description: "`s3 object list` groups on \"/\" and answers one level at a time, so " +
			"learning what is in somebody else's bucket means retyping --prefix a level " +
			"deeper over and over. This reads the prefix once and draws the whole shape.\n\n" +
			"Every prefix carries its recursive object count and total size, which is the " +
			"question a flat paginated listing cannot answer: where the space went.\n\n" +
			"Names and sizes only, never content — the same Read/Write split s3.object.list " +
			"and s3.object.get already draw.\n\n" +
			"One request, however deep the result: the folders are built here from the keys, " +
			"not fetched a level at a time. Bounded on how much of the stream is read, and it " +
			"says when it stopped rather than looking like a smaller bucket.",
		Run: runObjectTree,
	}, bucketField("bucket to walk"),
		plugin.Field{Name: "prefix", Type: plugin.String, Help: "start here rather than at the root",
			Live: true, Suggest: suggestKeys("prefix")},
		plugin.Field{Name: "depth", Type: plugin.Int, Default: 4, Min: 1, Max: 20,
			Help: "how many levels to expand"},
		plugin.Field{Name: "limit", Type: plugin.Int, Default: defaultTreeKeys, Min: 1, Max: 100000,
			Help: "how many keys to read before stopping"})
}

func runObjectTree(ctx context.Context, req plugin.Request) (view.View, error) {
	return withClient(req, func(ctx context.Context, client *minio.Client) (view.View, error) {
		bucket := req.String("bucket")
		prefix := req.String("prefix")
		limit := req.Int("limit")

		root := &treeNode{}
		read := 0
		truncated := false
		for obj := range client.ListObjects(ctx, bucket, minio.ListObjectsOptions{
			Prefix:    prefix,
			Recursive: true,
		}) {
			if obj.Err != nil {
				return nil, classify(obj.Err, req)
			}
			if read == limit {
				// Stopping mid-stream leaves the range unfinished, which
				// minio-go handles through the context rather than through a
				// close: the goroutine feeding the channel selects on
				// ctx.Done(). Nothing here can leak it, because withClient's
				// context is the call's own and ends with the call.
				truncated = true
				break
			}
			read++
			// Relative to the prefix somebody asked about. Rooting the tree at
			// the bucket and then drawing four empty levels down to their
			// prefix would be answering a question they did not ask.
			rel := strings.TrimPrefix(obj.Key, prefix)
			if rel == "" {
				continue
			}
			if strings.HasSuffix(rel, "/") {
				// A zero-byte directory marker, not an object anybody stored.
				// The folder still has to appear — a marker with nothing under
				// it is the only trace an empty prefix leaves — but counting it
				// as an object would make every console-created folder inflate
				// the totals by one.
				root.ensure(splitKey(strings.TrimSuffix(rel, "/")))
				continue
			}
			root.insert(splitKey(rel), uint64(obj.Size))
		}

		label := bucket
		if prefix != "" {
			label += "/" + strings.TrimSuffix(prefix, "/")
		}
		w := &treeRender{maxDepth: req.Int("depth")}
		children := w.expand(root, 1)

		detail := fmt.Sprintf("%d objects, %s", root.objects, format.Bytes(root.bytes))
		switch {
		case truncated:
			detail += fmt.Sprintf(" — stopped at %d keys; narrow it with --prefix or raise --limit", limit)
		case w.stopped != "":
			detail += " — " + w.stopped
		}
		return view.Tree{Roots: []view.Node{{
			Label: label, Detail: detail, Children: children,
		}}}, nil
	})
}

// splitKey turns a key into its path segments, dropping empty ones so that a
// key written with a doubled separator ("logs//app.txt", which S3 accepts and
// stores verbatim) does not produce an unnamed level nobody can navigate to.
func splitKey(key string) []string {
	parts := strings.Split(key, "/")
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// treeNode is one prefix or one object. objects and bytes are recursive
// totals for a prefix and the object's own for a leaf, accumulated on the way
// down rather than summed on the way back up: the aggregate is wanted at
// every level, and adding to each node as a key passes through it costs one
// pass instead of two.
type treeNode struct {
	children map[string]*treeNode
	objects  int
	bytes    uint64
	leaf     bool
}

func (n *treeNode) child(name string) *treeNode {
	if n.children == nil {
		n.children = map[string]*treeNode{}
	}
	c, ok := n.children[name]
	if !ok {
		c = &treeNode{}
		n.children[name] = c
	}
	return c
}

func (n *treeNode) insert(parts []string, size uint64) {
	n.objects++
	n.bytes += size
	if len(parts) == 0 {
		return
	}
	c := n.child(parts[0])
	if len(parts) == 1 {
		c.leaf = true
	}
	c.insert(parts[1:], size)
}

// ensure creates the path without counting anything, which is what a
// directory marker warrants: it proves the prefix exists and says nothing
// about what is in it.
func (n *treeNode) ensure(parts []string) {
	if len(parts) == 0 {
		return
	}
	n.child(parts[0]).ensure(parts[1:])
}

// treeRender carries the node budget across the walk, so the bound is one
// thing rather than a parameter every level has to remember to pass down.
type treeRender struct {
	maxDepth int
	nodes    int
	// stopped names the bound that was reached, in the words somebody needs to
	// do something about it. Empty means the whole tree was drawn.
	stopped string
}

func (w *treeRender) expand(n *treeNode, depth int) []view.Node {
	names := make([]string, 0, len(n.children))
	for name := range n.children {
		names = append(names, name)
	}
	// Prefixes before objects, each alphabetically. A map's iteration order is
	// deliberately random in Go, so without this the same bucket renders
	// differently on every call — which would make the output useless to diff
	// and impossible to test.
	sort.Slice(names, func(i, j int) bool {
		a, b := n.children[names[i]], n.children[names[j]]
		if a.leaf != b.leaf {
			return b.leaf
		}
		return names[i] < names[j]
	})

	var out []view.Node
	for _, name := range names {
		if w.nodes >= maxTreeNodes {
			w.stopped = fmt.Sprintf("stopped at %d nodes; narrow it with --prefix", maxTreeNodes)
			return append(out, view.Node{Label: "…", Detail: w.stopped})
		}
		w.nodes++
		c := n.children[name]
		if c.leaf {
			out = append(out, view.Node{Label: name, Detail: format.Bytes(c.bytes)})
			continue
		}

		node := view.Node{
			Label:  name + "/",
			Detail: fmt.Sprintf("%d objects, %s", c.objects, format.Bytes(c.bytes)),
		}
		if depth >= w.maxDepth {
			// Collapsed, not dropped. The count and the size are already known
			// — they were accumulated on the way in — so a prefix past the
			// depth still reports how much is under it. That is usually the
			// answer somebody wanted, and when it is not, it says which flag
			// gets them the rest.
			node.Detail += " — not expanded, raise --depth"
		} else {
			node.Children = w.expand(c, depth+1)
		}
		out = append(out, node)
	}
	return out
}
