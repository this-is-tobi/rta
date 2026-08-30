package main

import (
	"fmt"
	"strings"
	"testing"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The tree is built here from the keys rather than fetched a level at a time,
// so all of its behaviour is a pure function of the key list. That makes it
// testable without a cluster, which is the whole reason the walk was written
// this way round.

// treeFrom builds the tree the capability would render for a set of keys.
func treeFrom(t *testing.T, depth int, keys ...string) view.Node {
	t.Helper()
	root := &treeNode{}
	for _, k := range keys {
		root.insert(splitKey(k))
	}
	w := &treeRender{maxDepth: depth}
	return view.Node{
		Label:    "/",
		Detail:   fmt.Sprintf("%d keys", root.keys),
		Children: w.expand(root, 1),
	}
}

func child(t *testing.T, nodes []view.Node, path ...string) view.Node {
	t.Helper()
	for i, want := range path {
		found := false
		for _, n := range nodes {
			if n.Label == want {
				if i == len(path)-1 {
					return n
				}
				nodes, found = n.Children, true
				break
			}
		}
		if !found {
			labels := make([]string, len(nodes))
			for j, n := range nodes {
				labels[j] = n.Label
			}
			t.Fatalf("no node %q among %v", want, labels)
		}
	}
	t.Fatalf("empty path")
	return view.Node{}
}

// etcd keys are flat and everything treats them as paths anyway. This is the
// capability's whole reason to exist.
func TestFlatKeysBecomeTheHierarchyEverybodyAlreadyReadsThemAs(t *testing.T) {
	root := treeFrom(t, 6,
		"/registry/pods/default/api-1",
		"/registry/pods/default/api-2",
		"/registry/pods/kube-system/coredns",
		"/registry/services/endpoints/default/api",
	)
	child(t, root.Children, "registry/", "pods/", "default/", "api-1")
	child(t, root.Children, "registry/", "pods/", "kube-system/", "coredns")
	child(t, root.Children, "registry/", "services/", "endpoints/", "default/", "api")
}

// Counts accumulate up every level, so a prefix reports what is under it and
// not only what is directly in it.
func TestCountsAccumulateUpEveryLevel(t *testing.T) {
	root := treeFrom(t, 6,
		"/registry/pods/default/api-1",
		"/registry/pods/default/api-2",
		"/registry/pods/kube-system/coredns",
	)
	if !strings.Contains(root.Detail, "3 keys") {
		t.Errorf("root detail = %q, want 3 keys", root.Detail)
	}
	pods := child(t, root.Children, "registry/", "pods/")
	if !strings.Contains(pods.Detail, "3 keys") {
		t.Errorf("pods/ detail = %q, want 3 keys", pods.Detail)
	}
	def := child(t, root.Children, "registry/", "pods/", "default/")
	if !strings.Contains(def.Detail, "2 keys") {
		t.Errorf("default/ detail = %q, want 2 keys", def.Detail)
	}
}

// A level past --depth keeps its count. It was accumulated on the way in, so
// collapsing costs nothing and still answers "how much is under here" — which
// is usually why somebody was looking.
func TestCollapsedLevelsKeepTheirCounts(t *testing.T) {
	root := treeFrom(t, 2,
		"/registry/pods/default/api-1",
		"/registry/pods/default/api-2",
	)
	pods := child(t, root.Children, "registry/", "pods/")
	if len(pods.Children) != 0 {
		t.Errorf("expanded past --depth 2: %d children", len(pods.Children))
	}
	if !strings.Contains(pods.Detail, "2 keys") {
		t.Errorf("collapsed node lost its count: %q", pods.Detail)
	}
	if !strings.Contains(pods.Detail, "raise --depth") {
		t.Errorf("collapsed node does not say how to see more: %q", pods.Detail)
	}
}

// etcd stores a leading or doubled separator verbatim. Splitting naively
// creates a level with an empty name, which nothing can navigate to.
func TestEmptySegmentsNeverBecomeLevels(t *testing.T) {
	root := treeFrom(t, 6, "/registry//pods/x", "//double/leading")
	var walk func([]view.Node)
	walk = func(nodes []view.Node) {
		for _, n := range nodes {
			if n.Label == "" || n.Label == "/" {
				t.Errorf("empty segment became a node: %q", n.Label)
			}
			walk(n.Children)
		}
	}
	walk(root.Children)
	child(t, root.Children, "registry/", "pods/", "x")
	child(t, root.Children, "double/", "leading")
}

// Go randomizes map iteration on purpose, so without an explicit sort the same
// keyspace renders differently on every call — undiffable, and untestable.
func TestOrderIsStableAndLevelsComeBeforeLeaves(t *testing.T) {
	keys := []string{"/zeta", "/alpha", "/b-dir/x", "/a-dir/y"}
	first := treeFrom(t, 6, keys...)
	got := make([]string, 0, 4)
	for _, n := range first.Children {
		got = append(got, n.Label)
	}
	want := []string{"a-dir/", "b-dir/", "alpha", "zeta"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", got, want)
	}
	for range 5 {
		again := treeFrom(t, 6, keys...)
		var repeat []string
		for _, n := range again.Children {
			repeat = append(repeat, n.Label)
		}
		if strings.Join(repeat, ",") != strings.Join(got, ",") {
			t.Fatalf("order changed between calls: %v then %v", got, repeat)
		}
	}
}

// The node budget is a different question from the key budget, and needs its
// own answer: a keyspace whose keys all sit at one level produces one node per
// key however few keys were read.
func TestTheNodeBudgetIsMarkedRatherThanSilent(t *testing.T) {
	keys := make([]string, 0, maxTreeNodes+20)
	for i := range maxTreeNodes + 20 {
		keys = append(keys, fmt.Sprintf("/k-%04d", i))
	}
	root := treeFrom(t, 6, keys...)
	if len(root.Children) > maxTreeNodes+1 {
		t.Errorf("rendered %d nodes, budget is %d", len(root.Children), maxTreeNodes)
	}
	last := root.Children[len(root.Children)-1]
	if last.Label != "…" {
		t.Errorf("truncation is not marked in the tree: last label %q", last.Label)
	}
	if !strings.Contains(last.Detail, "--prefix") {
		t.Errorf("truncation does not say how to narrow it: %q", last.Detail)
	}
}

// A key that is both a level and a leaf is normal in etcd — nothing stops
// /a/b existing alongside /a/b/c — and dropping either would lose data.
func TestAKeyThatIsAlsoAPrefixKeepsItsChildren(t *testing.T) {
	root := treeFrom(t, 6, "/a/b", "/a/b/c")
	b := child(t, root.Children, "a/", "b/")
	if len(b.Children) == 0 {
		t.Fatal("a key that is also a prefix lost its children")
	}
	child(t, root.Children, "a/", "b/", "c")
	if !strings.Contains(b.Detail, "2 keys") {
		t.Errorf("b/ detail = %q, want both keys counted", b.Detail)
	}
}

// **WithKeysOnly is the read tier.**
//
// Without it etcd sends every value over the wire, so etcd.kv.list and
// etcd.kv.tree would read the entire keyspace's contents, discard them, and
// return exactly the same answer. The output is identical either way, which is
// precisely why this needs a test rather than a reviewer: probing its removal
// broke nothing at all.
func TestListingsNeverAskTheClusterForValues(t *testing.T) {
	for _, prefix := range []string{"", "/registry/"} {
		key, opts := keyFetchOptions(prefix, 200)
		op := clientv3.OpGet(key, opts...)
		if !op.IsKeysOnly() {
			t.Errorf("prefix %q: the request would pull every value over the wire", prefix)
		}
		if op.Limit() != 201 {
			t.Errorf("prefix %q: limit = %d, want one past the asked-for bound so a full "+
				"page can be told from one that ends on the boundary", prefix, op.Limit())
		}
	}
}

// The two range forms are mutually exclusive, and picking the wrong one is not
// a smaller answer — it is a different range. An empty prefix must range from
// the start of the keyspace; a named one must be bounded to that prefix.
func TestTheRangeFormMatchesWhatWasAsked(t *testing.T) {
	key, opts := keyFetchOptions("", 10)
	op := clientv3.OpGet(key, opts...)
	if !op.IsOptsWithFromKey() {
		t.Error("an empty prefix did not range from the start of the keyspace")
	}
	if op.IsOptsWithPrefix() {
		t.Error("an empty prefix was sent as a prefix range — etcd has no `everything` prefix")
	}

	key, opts = keyFetchOptions("/registry/", 10)
	op = clientv3.OpGet(key, opts...)
	if !op.IsOptsWithPrefix() {
		t.Error("a named prefix was not sent as a prefix range")
	}
	if op.IsOptsWithFromKey() {
		t.Error("a named prefix ranged from it to the end of the keyspace — far more than asked for")
	}
	if string(op.KeyBytes()) != "/registry/" {
		t.Errorf("key = %q, want the prefix asked for", op.KeyBytes())
	}
}
