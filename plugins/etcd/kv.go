package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/this-is-tobi/rule-them-all/pkg/format"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The keyspace, and the line drawn through it.
//
// etcd.kv.list and etcd.kv.tree return key names. etcd.kv.get
// returns what a key holds, and it is the only capability here that does — so
// it is the only one that is a write, and the only one that needs a grant
// naming it.
//
// The line is worth more here than in most places. A Kubernetes cluster keeps
// every object it has in etcd, including its Secrets, and those are stored
// base64-encoded rather than encrypted unless somebody turned encryption at
// rest on. So "read the value of an arbitrary key" is, on a very common
// deployment, "read every secret in the cluster" — and it should cost the same
// consent as saying that out loud.

const (
	// maxKeys bounds a listing. etcd holds the whole state of some systems;
	// a keyspace with a hundred thousand keys is normal and a listing of them
	// is not an answer.
	maxKeys = 1000
	// maxTreeNodes bounds the drawing rather than the fetch. One request
	// returns the keys; past this many nodes the way to find something is a
	// narrower --prefix.
	maxTreeNodes = 500
)

func prefixField() plugin.Field {
	return plugin.Field{Name: "prefix", Type: plugin.String, Positional: true, Default: "",
		Help: "only keys starting with this; empty walks the whole keyspace"}
}

func kvListCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:         "etcd.kv.list",
		Summary:    "Key names under a prefix — never their contents",
		Safety:     plugin.Read,
		Idempotent: true,
		Description: "Names, versions and lease bindings for every key under a prefix.\n\n" +
			"Never a value: this is the read tier, and etcd.kv.get is where contents live. No " +
			"size column either, and that is the same line rather than an omission — etcd " +
			"carries no length field, so the only way to report a size would be to fetch every " +
			"value and decline to print it, which has already read the thing.\n\n" +
			"Bounded, and it says when it stopped. A listing that quietly ended at a thousand " +
			"reads exactly like a keyspace with a thousand keys in it.",
		Run: func(ctx context.Context, req plugin.Request) (view.View, error) {
			return withClient(ctx, req, func(ctx context.Context, c *clientv3.Client) (view.View, error) {
				return kvListView(ctx, c, req)
			})
		},
	}, prefixField(),
		plugin.Field{Name: "limit", Type: plugin.Int, Default: 200, Min: 1, Max: maxKeys,
			Help: "how many keys to return"})
}

// fetchKeys is the one request both the listing and the tree are built from.
//
// WithKeysOnly is not decoration: without it etcd sends every value over the
// wire, so a "names only" capability would pull the entire keyspace's contents
// into this process and then discard them. The values would never be shown,
// and they would still have been read, logged by the cluster, and held in
// memory here.
func fetchKeys(ctx context.Context, c *clientv3.Client, req plugin.Request, limit int) (*clientv3.GetResponse, *view.Error) {
	key, opts := keyFetchOptions(req.String("prefix"), limit)
	resp, err := c.Get(ctx, key, opts...)
	if err != nil {
		return nil, classify(err, req)
	}
	return resp, nil
}

// keyFetchOptions is split out so what this asks the cluster for is assertable
// without a cluster. WithKeysOnly is the one that has to be right, and its
// absence is invisible from the output: the values would arrive, be discarded,
// and the answer would look identical.
func keyFetchOptions(prefix string, limit int) (string, []clientv3.OpOption) {
	opts := []clientv3.OpOption{
		clientv3.WithKeysOnly(),
		clientv3.WithSort(clientv3.SortByKey, clientv3.SortAscend),
		// One past the limit, which is what tells a full page from a page that
		// happens to end on the boundary. The difference between "here is the
		// rest" and "there is more" cannot be guessed from a count.
		clientv3.WithLimit(int64(limit) + 1),
	}
	if prefix == "" {
		// etcd has no "everything" prefix. The convention is to range from the
		// zero byte, which sorts before every printable key. WithFromKey and
		// WithPrefix are mutually exclusive, which is why this picks one here
		// rather than adding to a list that already holds the other.
		return "\x00", append(opts, clientv3.WithFromKey())
	}
	return prefix, append(opts, clientv3.WithPrefix())
}

func kvListView(ctx context.Context, c *clientv3.Client, req plugin.Request) (view.View, error) {
	limit := req.Int("limit")
	resp, verr := fetchKeys(ctx, c, req, limit)
	if verr != nil {
		return nil, verr
	}

	// No size column, and that is a limit rather than an omission. etcd's
	// KeyValue carries no length field, so the only way to report a size is to
	// fetch the value — which is precisely what this capability exists not to
	// do. A "names only" listing that pulled every value to measure it would
	// have read the whole keyspace's contents and merely declined to print
	// them, which is not the same thing at all. etcd.kv.get reports the size
	// of the one key it was asked for.
	t := view.Table{Columns: []view.Column{
		{Name: "Key"},
		{Name: "Version", Kind: view.KindNumber},
		{Name: "Lease"},
	}}
	for i, kv := range resp.Kvs {
		if i == limit {
			t.Page = &view.Cursor{Next: string(resp.Kvs[i-1].Key)}
			break
		}
		lease := "-"
		if kv.Lease != 0 {
			lease = hexID(uint64(kv.Lease))
		}
		t.Rows = append(t.Rows, []string{
			string(kv.Key),
			strconv.FormatInt(kv.Version, 10),
			lease,
		})
	}
	t.Total = len(t.Rows)
	return t, nil
}

func kvTreeCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:         "etcd.kv.tree",
		Summary:    "The shape of the keyspace in one call — names only",
		Safety:     plugin.Read,
		Idempotent: true,
		Description: "etcd keys are flat, and everything treats them as paths anyway: " +
			"/registry/pods/default/... is a hierarchy that only the \"/\" makes visible. This " +
			"reads a prefix once and draws it.\n\n" +
			"One request however deep the result goes: the levels are built here from the keys " +
			"rather than fetched a level at a time, which is what makes this cheaper than the " +
			"repeated listing it replaces.\n\n" +
			"Names and counts only, never a value. Same read tier as etcd.kv.list, and the " +
			"reason a listing is ungated while a read is not.",
		Run: func(ctx context.Context, req plugin.Request) (view.View, error) {
			return withClient(ctx, req, func(ctx context.Context, c *clientv3.Client) (view.View, error) {
				return kvTreeView(ctx, c, req)
			})
		},
	}, prefixField(),
		plugin.Field{Name: "depth", Type: plugin.Int, Default: 4, Min: 1, Max: 20,
			Help: "how many levels to expand"},
		plugin.Field{Name: "limit", Type: plugin.Int, Default: maxKeys, Min: 1, Max: 100000,
			Help: "how many keys to read before stopping"})
}

func kvTreeView(ctx context.Context, c *clientv3.Client, req plugin.Request) (view.View, error) {
	limit := req.Int("limit")
	resp, verr := fetchKeys(ctx, c, req, limit)
	if verr != nil {
		return nil, verr
	}

	prefix := req.String("prefix")
	root := &treeNode{}
	truncated := false
	for i, kv := range resp.Kvs {
		if i == limit {
			truncated = true
			break
		}
		rel := strings.TrimPrefix(string(kv.Key), prefix)
		root.insert(splitKey(rel))
	}

	label := prefix
	if label == "" {
		label = "/"
	}
	w := &treeRender{maxDepth: req.Int("depth")}
	children := w.expand(root, 1)

	detail := fmt.Sprintf("%d keys", root.keys)
	switch {
	case truncated:
		detail += fmt.Sprintf(" — stopped at %d; narrow it with --prefix or raise --limit", limit)
	case w.stopped != "":
		detail += " — " + w.stopped
	}
	return view.Tree{Roots: []view.Node{{Label: label, Detail: detail, Children: children}}}, nil
}

// splitKey drops empty segments, so a key written with a leading or doubled
// separator — which etcd stores verbatim — does not produce an unnamed level
// nothing can navigate to.
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

// treeNode is one level. keys is the recursive count, accumulated on the way
// down: the total is wanted at every level, and adding to each node as a key
// passes through it costs one pass instead of two.
type treeNode struct {
	children map[string]*treeNode
	keys     int
	leaf     bool
}

func (n *treeNode) insert(parts []string) {
	n.keys++
	if len(parts) == 0 {
		return
	}
	if n.children == nil {
		n.children = map[string]*treeNode{}
	}
	c, ok := n.children[parts[0]]
	if !ok {
		c = &treeNode{}
		n.children[parts[0]] = c
	}
	if len(parts) == 1 {
		c.leaf = true
	}
	c.insert(parts[1:])
}

type treeRender struct {
	maxDepth int
	nodes    int
	stopped  string
}

func (w *treeRender) expand(n *treeNode, depth int) []view.Node {
	names := make([]string, 0, len(n.children))
	for name := range n.children {
		names = append(names, name)
	}
	// Levels before leaves, each alphabetically. Go randomizes map iteration
	// on purpose, so without this the same keyspace renders differently on
	// every call — undiffable, and untestable.
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
		if c.leaf && len(c.children) == 0 {
			out = append(out, view.Node{Label: name})
			continue
		}
		node := view.Node{Label: name + "/", Detail: fmt.Sprintf("%d keys", c.keys)}
		if depth >= w.maxDepth {
			// Collapsed, not dropped. The count was accumulated on the way in,
			// so a level past the depth still reports how much is under it —
			// usually the answer somebody wanted — and says which flag gets
			// them the rest.
			node.Detail += " — not expanded, raise --depth"
		} else {
			node.Children = w.expand(c, depth+1)
		}
		out = append(out, node)
	}
	return out
}

func kvGetCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:      "etcd.kv.get",
		Summary: "What one key holds",
		// **Write, and it needs a grant naming it.** Nothing here mutates.
		//
		// The classification is about what it discloses. A Kubernetes cluster
		// keeps every object it has in etcd, and its Secrets are stored
		// base64-encoded rather than encrypted unless somebody turned
		// encryption at rest on. So on a very common deployment "read the
		// value of an arbitrary key" is "read every secret in the cluster",
		// and it should cost the same consent as saying that out loud.
		//
		// NeedsGrant on top of the write tier, because unlike a row from a
		// table this names exactly one thing — which means a grant can name it
		// too, and the narrow consent is actually available here.
		Safety:     plugin.Write,
		NeedsGrant: true,
		Idempotent: true,
		Description: "The value stored at one key, with its version and lease.\n\n" +
			"**Classified write for what it discloses, not what it changes.** A Kubernetes " +
			"cluster keeps its Secrets in etcd base64-encoded rather than encrypted, unless " +
			"encryption at rest was turned on — so reading an arbitrary key here can be reading " +
			"every secret in the cluster.\n\n" +
			"It also needs a grant naming it. That is available because this names one key: " +
			"`rta grant allow etcd.kv.get /registry/services/endpoints/default/api` is a consent " +
			"somebody can actually read, which a whole-namespace grant would not be.\n\n" +
			"The read tier — etcd.kv.list and etcd.kv.tree — shows names and sizes, which is " +
			"usually the question and costs none of this.",
		Run: func(ctx context.Context, req plugin.Request) (view.View, error) {
			return withClient(ctx, req, func(ctx context.Context, c *clientv3.Client) (view.View, error) {
				return kvGetView(ctx, c, req)
			})
		},
	}, plugin.Field{Name: "key", Type: plugin.String, Positional: true, Required: true,
		Help: "the exact key to read"})
}

func kvGetView(ctx context.Context, c *clientv3.Client, req plugin.Request) (view.View, error) {
	key := req.String("key")
	resp, err := c.Get(ctx, key)
	if err != nil {
		return nil, classify(err, req)
	}
	if len(resp.Kvs) == 0 {
		return nil, view.Errorf("etcd.key.notfound", "no key %q", key).
			WithHint("`rta etcd kv list " + key + "` shows what is there — this is an exact match, not a prefix")
	}
	kv := resp.Kvs[0]
	return kvGetResult(string(kv.Key), kv.Value, kv.Version, kv.CreateRevision, kv.ModRevision, kv.Lease), nil
}

// kvGetResult is split from the fetch so the shape of the answer — and in
// particular what it declares redacted — is assertable without a cluster.
// Redaction is a claim worth testing, and one that would otherwise only be
// checked by somebody reading it.
func kvGetResult(key string, value []byte, version, created, modified, lease int64) view.View {
	leaseText := "-"
	if lease != 0 {
		leaseText = hexID(uint64(lease))
	}
	return view.KeyValue{
		Pairs: []view.Pair{
			{Key: "key", Value: key},
			{Key: "value", Value: string(value)},
			{Key: "size", Value: format.Bytes(uint64(len(value)))},
			{Key: "version", Value: strconv.FormatInt(version, 10)},
			{Key: "created revision", Value: strconv.FormatInt(created, 10)},
			{Key: "modified revision", Value: strconv.FormatInt(modified, 10)},
			{Key: "lease", Value: leaseText},
		},
		// The value is the whole point of the capability and still must not
		// land in a log or a terminal scrollback by accident. Redacted is what
		// makes every renderer mask it unless somebody asked for it.
		Redacted: []string{"value"},
	}
}
