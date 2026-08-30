package main

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// keyServer answers ListObjects v2 with exactly the keys it is given, so a
// shape can be stated in the test and checked against what comes out. Sizes
// are derived from the key's position rather than passed in, because every
// assertion here is about structure and totals rather than about any one
// object's bytes.
func keyServer(t *testing.T, keys ...string) *httptest.Server {
	t.Helper()
	type object struct {
		Key          string `xml:"Key"`
		LastModified string `xml:"LastModified"`
		Size         int64  `xml:"Size"`
		ETag         string `xml:"ETag"`
	}
	type result struct {
		XMLName     xml.Name `xml:"ListBucketResult"`
		Name        string   `xml:"Name"`
		IsTruncated bool     `xml:"IsTruncated"`
		Contents    []object `xml:"Contents"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res := result{Name: "test-bucket"}
		prefix := r.URL.Query().Get("prefix")
		for i, k := range keys {
			if !strings.HasPrefix(k, prefix) {
				continue
			}
			size := int64(0)
			if !strings.HasSuffix(k, "/") {
				size = int64(i+1) * 1000
			}
			res.Contents = append(res.Contents, object{
				Key: k, Size: size, ETag: `"e"`,
				LastModified: "2026-08-01T10:00:00.000Z",
			})
		}
		w.Header().Set("Content-Type", "application/xml")
		_ = xml.NewEncoder(w).Encode(res)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func treeOf(t *testing.T, srv *httptest.Server, values map[string]any) view.Tree {
	t.Helper()
	values["bucket"] = "test-bucket"
	v, err := runObjectTree(context.Background(),
		reqFor(t, "s3.object.tree", endpointOf(t, srv), values))
	if err != nil {
		t.Fatal(err)
	}
	tree, ok := v.(view.Tree)
	if !ok {
		t.Fatalf("want Tree, got %s", view.TypeOf(v))
	}
	if len(tree.Roots) != 1 {
		t.Fatalf("want one root, got %d", len(tree.Roots))
	}
	return tree
}

// find walks to a node by label path, so an assertion names the place it is
// talking about instead of indexing into slices.
func find(t *testing.T, nodes []view.Node, path ...string) view.Node {
	t.Helper()
	for _, want := range path {
		found := false
		for _, n := range nodes {
			if n.Label == want {
				nodes, found = n.Children, true
				if want == path[len(path)-1] {
					return n
				}
				break
			}
		}
		if !found {
			labels := make([]string, len(nodes))
			for i, n := range nodes {
				labels[i] = n.Label
			}
			t.Fatalf("no node %q among %v", want, labels)
		}
	}
	t.Fatalf("empty path")
	return view.Node{}
}

// The whole point of the capability: one call produces the nesting that
// s3.object.list only reaches one --prefix at a time.
func TestTreeBuildsNestingFromFlatKeys(t *testing.T) {
	srv := keyServer(t,
		"releases/v1/rta_linux",
		"releases/v1/rta_darwin",
		"releases/v2/rta_linux",
		"index.json",
	)
	tree := treeOf(t, srv, map[string]any{})
	root := tree.Roots[0]

	if root.Label != "test-bucket" {
		t.Errorf("root label = %q, want the bucket", root.Label)
	}
	find(t, root.Children, "releases/", "v1/", "rta_linux")
	find(t, root.Children, "releases/", "v2/", "rta_linux")
	find(t, root.Children, "index.json")
}

// The question a flat listing cannot answer. Sizes accumulate up every level,
// so a prefix reports what is under it rather than only what is directly in it.
func TestTreeAggregatesCountAndBytesUpEveryLevel(t *testing.T) {
	// Sizes are 1000, 2000, 3000 by position, so releases/ holds 6000 total
	// and v1/ holds 3000 of it.
	srv := keyServer(t,
		"releases/v1/a", // 1000
		"releases/v1/b", // 2000
		"releases/v2/c", // 3000
	)
	tree := treeOf(t, srv, map[string]any{})
	root := tree.Roots[0]

	if !strings.Contains(root.Detail, "3 objects") {
		t.Errorf("root detail = %q, want 3 objects", root.Detail)
	}
	releases := find(t, root.Children, "releases/")
	if !strings.Contains(releases.Detail, "3 objects") {
		t.Errorf("releases/ detail = %q, want 3 objects", releases.Detail)
	}
	v1 := find(t, root.Children, "releases/", "v1/")
	if !strings.Contains(v1.Detail, "2 objects") {
		t.Errorf("v1/ detail = %q, want 2 objects", v1.Detail)
	}
	// 1000+2000 = 3000 bytes under v1/, which format.Bytes renders in binary
	// units. The assertion is on the sum rather than on either object's size:
	// getting 1000 here would mean the walk overwrote instead of accumulating.
	if !strings.Contains(v1.Detail, "2.9 KiB") {
		t.Errorf("v1/ detail = %q, want the summed size (3000 bytes)", v1.Detail)
	}
}

// A prefix past --depth keeps its totals. The counts were accumulated on the
// way in, so collapsing a level costs nothing and still answers "how much is
// under here" — which is usually why somebody was looking.
func TestTreeCollapsesPastDepthButKeepsTotals(t *testing.T) {
	srv := keyServer(t, "a/b/c/d/deep")
	tree := treeOf(t, srv, map[string]any{"depth": 2})

	b := find(t, tree.Roots[0].Children, "a/", "b/")
	if len(b.Children) != 0 {
		t.Errorf("b/ expanded past --depth 2: %d children", len(b.Children))
	}
	if !strings.Contains(b.Detail, "1 objects") {
		t.Errorf("collapsed node lost its total: %q", b.Detail)
	}
	if !strings.Contains(b.Detail, "raise --depth") {
		t.Errorf("collapsed node does not say how to see more: %q", b.Detail)
	}
}

// A listing that quietly stopped reads exactly like a smaller bucket, which
// is the bug the bound would introduce if it were silent.
func TestTreeSaysWhenItStoppedReading(t *testing.T) {
	keys := make([]string, 0, 50)
	for i := range 50 {
		keys = append(keys, fmt.Sprintf("obj-%02d", i))
	}
	srv := keyServer(t, keys...)

	full := treeOf(t, srv, map[string]any{})
	if strings.Contains(full.Roots[0].Detail, "stopped") {
		t.Errorf("unbounded walk claimed it stopped: %q", full.Roots[0].Detail)
	}

	cut := treeOf(t, srv, map[string]any{"limit": 10})
	if !strings.Contains(cut.Roots[0].Detail, "stopped at 10 keys") {
		t.Errorf("bounded walk did not say so: %q", cut.Roots[0].Detail)
	}
	if !strings.Contains(cut.Roots[0].Detail, "--limit") {
		t.Errorf("bounded walk did not name the flag: %q", cut.Roots[0].Detail)
	}
}

// A folder created from a console leaves a zero-byte marker object. It has to
// show — it is the only trace an empty prefix leaves — but counting it as an
// object would inflate every total by one per folder.
func TestTreeShowsDirectoryMarkersWithoutCountingThem(t *testing.T) {
	srv := keyServer(t, "empty/", "real/file.txt")
	tree := treeOf(t, srv, map[string]any{})
	root := tree.Roots[0]

	find(t, root.Children, "empty/")
	if !strings.Contains(root.Detail, "1 objects") {
		t.Errorf("root detail = %q — the marker was counted as an object", root.Detail)
	}
}

// S3 stores a doubled separator verbatim. Splitting naively would create a
// level with an empty name, which nothing can navigate to and nothing can
// render.
func TestTreeDropsEmptySegmentsFromDoubledSeparators(t *testing.T) {
	srv := keyServer(t, "logs//app.txt")
	tree := treeOf(t, srv, map[string]any{})

	find(t, tree.Roots[0].Children, "logs/", "app.txt")
	for _, n := range tree.Roots[0].Children {
		if n.Label == "/" || n.Label == "" {
			t.Errorf("empty segment became a node: %q", n.Label)
		}
	}
}

// Go randomizes map iteration on purpose, so without an explicit sort the same
// bucket renders differently on every call — undiffable, and untestable.
func TestTreeOrderIsStableAndFoldersComeFirst(t *testing.T) {
	srv := keyServer(t, "zeta.txt", "alpha.txt", "b-dir/x", "a-dir/y")
	first := treeOf(t, srv, map[string]any{})

	got := make([]string, 0, 4)
	for _, n := range first.Roots[0].Children {
		got = append(got, n.Label)
	}
	want := []string{"a-dir/", "b-dir/", "alpha.txt", "zeta.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", got, want)
	}

	for range 5 {
		again := treeOf(t, srv, map[string]any{})
		var repeat []string
		for _, n := range again.Roots[0].Children {
			repeat = append(repeat, n.Label)
		}
		if strings.Join(repeat, ",") != strings.Join(got, ",") {
			t.Fatalf("order changed between calls: %v then %v", got, repeat)
		}
	}
}

// Rooting the tree at the bucket and drawing empty levels down to the prefix
// somebody asked about would be answering a question they did not ask.
func TestTreeRootsAtThePrefixAsked(t *testing.T) {
	srv := keyServer(t, "releases/v1/a", "releases/v1/b", "other/c")
	tree := treeOf(t, srv, map[string]any{"prefix": "releases/"})
	root := tree.Roots[0]

	if root.Label != "test-bucket/releases" {
		t.Errorf("root label = %q, want the bucket and the prefix", root.Label)
	}
	find(t, root.Children, "v1/", "a")
	for _, n := range root.Children {
		if n.Label == "releases/" {
			t.Error("prefix redrawn inside its own tree")
		}
	}
}

// The rendered bound is a different question from the read bound, and needs
// its own answer: a bucket whose keys all sit at one level produces one node
// per key however few keys were read.
func TestTreeBoundsRenderedNodesSeparatelyFromKeysRead(t *testing.T) {
	keys := make([]string, 0, maxTreeNodes+20)
	for i := range maxTreeNodes + 20 {
		keys = append(keys, fmt.Sprintf("k-%04d", i))
	}
	srv := keyServer(t, keys...)
	// Read far more keys than the node budget allows to render.
	tree := treeOf(t, srv, map[string]any{"limit": maxTreeNodes + 20})

	root := tree.Roots[0]
	if len(root.Children) > maxTreeNodes+1 {
		t.Errorf("rendered %d nodes, budget is %d", len(root.Children), maxTreeNodes)
	}
	if !strings.Contains(root.Detail, "stopped") {
		t.Errorf("node budget was hit silently: %q", root.Detail)
	}
	last := root.Children[len(root.Children)-1]
	if last.Label != "…" {
		t.Errorf("truncation is not marked in the tree: last label %q", last.Label)
	}
}

// Registration is covered by main_test.go's safety table, which fails both
// ways. What is only asserted here is the safety class: a walk that returns
// names and sizes is a read however many keys it touched, and classifying it
// any harder would make the cheap way to learn a bucket's shape cost more
// consent than the expensive way it replaces.
func TestTreeIsAReadCapability(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if c.ID == "s3.object.tree" {
			if c.Run == nil {
				t.Error("s3.object.tree has no Run")
			}
			if c.Safety != plugin.Read {
				t.Errorf("s3.object.tree is %s — a listing of names and sizes is a read", c.Safety)
			}
			return
		}
	}
	t.Fatal("s3.object.tree is not registered in Plugin()")
}
