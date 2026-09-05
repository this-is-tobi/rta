package audit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// npmProject is a small tree with the shape the question is about: a package
// reached two ways, one of them through two levels.
const npmProject = `{
  "lockfileVersion": 3,
  "packages": {
    "": { "dependencies": { "express": "^4.17.1" }, "devDependencies": { "jest": "^26.0.0" } },
    "node_modules/express": { "version": "4.17.1", "dependencies": { "qs": "6.7.0", "lodash": "4.17.20" } },
    "node_modules/jest": { "version": "26.0.0", "dependencies": { "babel": "7.0.0" } },
    "node_modules/babel": { "version": "7.0.0", "dependencies": { "lodash": "4.17.20" } },
    "node_modules/lodash": { "version": "4.17.20" },
    "node_modules/qs": { "version": "6.7.0" }
  }
}`

func whyIn(t *testing.T, body string, values map[string]any) (view.View, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package-lock.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	full := map[string]any{"path": dir}
	for k, v := range values {
		full[k] = v
	}
	return runWhy(context.Background(), plugin.NewRequest(full, false, true))
}

// sectionOf pulls one section out of the page by its stable id.
func sectionOf(t *testing.T, v view.View, id string) view.View {
	t.Helper()
	s, ok := v.(view.Sections)
	if !ok {
		t.Fatalf("want a page, got %T", v)
	}
	for _, item := range s.Items {
		if item.Key() == id {
			return item.View
		}
	}
	t.Fatalf("no %q section in %v", id, s.Items)
	return nil
}

func TestWhyDrawsEveryRouteToAPackage(t *testing.T) {
	v, err := whyIn(t, npmProject, map[string]any{"package": "lodash"})
	if err != nil {
		t.Fatal(err)
	}
	tree, ok := sectionOf(t, v, "reached from").(view.Tree)
	if !ok {
		t.Fatalf("the routes section is not a tree")
	}
	if len(tree.Roots) != 1 {
		t.Fatalf("want one root, got %d", len(tree.Roots))
	}
	root := tree.Roots[0]
	if !strings.Contains(root.Label, "lodash 4.17.20") {
		t.Errorf("the root is not the package asked about: %q", root.Label)
	}
	// Two routes: express asks for it directly, and jest reaches it through
	// babel. Both are true and both are shown.
	var top []string
	for _, c := range root.Children {
		top = append(top, c.Label)
	}
	if len(top) != 2 || top[0] != "babel" || top[1] != "express" {
		t.Fatalf("routes = %v, want [babel express]", top)
	}
	// The end of a branch is the actionable part, so it says which one the
	// project itself asked for.
	if !strings.Contains(root.Children[1].Detail, "asked for by this project") {
		t.Errorf("express is a direct dependency and does not say so: %+v", root.Children[1])
	}
	if len(root.Children[0].Children) != 1 || root.Children[0].Children[0].Label != "jest" {
		t.Errorf("the two-step route through babel is missing: %+v", root.Children[0])
	}
}

// A direct dependency needs no tree; it needs a version bump, and the answer
// has to say so first.
func TestWhyNamesADirectDependencyAsOne(t *testing.T) {
	v, err := whyIn(t, npmProject, map[string]any{"package": "express"})
	if err != nil {
		t.Fatal(err)
	}
	kv, ok := sectionOf(t, v, "summary").(view.KeyValue)
	if !ok {
		t.Fatal("the summary is not a key/value view")
	}
	if got := pairValue(kv, "relation"); !strings.HasPrefix(got, "direct") {
		t.Errorf("relation = %q, want it to lead with direct", got)
	}
	// And the command that has the resolver's own answer, always offered:
	// what rta reads is a committed file, and where the two disagree the
	// command is right.
	if got := pairValue(kv, "or run"); got != "npm why express" {
		t.Errorf("or run = %q", got)
	}
}

// A format that records no edges has to say so rather than draw an empty tree
// that reads as "nothing requires this".
func TestWhySaysWhenTheManifestDoesNotRecordEdges(t *testing.T) {
	dir := t.TempDir()
	body := "module example.com/x\n\ngo 1.24\n\nrequire (\n\tgithub.com/a/b v1.0.0 // indirect\n)\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := runWhy(context.Background(), plugin.NewRequest(
		map[string]any{"path": dir, "package": "github.com/a/b"}, false, true))
	if err != nil {
		t.Fatal(err)
	}
	kv := sectionOf(t, v, "summary").(view.KeyValue)
	if got := pairValue(kv, "relation"); !strings.HasPrefix(got, "indirect") {
		t.Errorf("go.mod marks this indirect and the summary says %q", got)
	}
	if got := pairValue(kv, "or run"); got != "go mod why -m github.com/a/b" {
		t.Errorf("or run = %q — the half go.mod cannot answer has to be handed over", got)
	}
	root := sectionOf(t, v, "reached from").(view.Tree).Roots[0]
	if len(root.Children) != 0 {
		t.Fatalf("go.mod records no edges and a tree appeared: %+v", root)
	}
	if !strings.Contains(root.Detail, "nothing read records what requires it") {
		t.Errorf("an edgeless root reads as having no dependants: %q", root.Detail)
	}
}

// The mistake this catches is not a typo — it is a scope left off or a module
// path shortened — so the error offers what is actually installed.
func TestWhyOffersNearMissesForAPackageThatIsNotThere(t *testing.T) {
	_, err := whyIn(t, npmProject, map[string]any{"package": "dash"})
	if err == nil {
		t.Fatal("a package nothing declares was accepted")
	}
	verr, ok := err.(*view.Error)
	if !ok {
		t.Fatalf("want a view error, got %T", err)
	}
	if !strings.Contains(verr.Hint, "lodash") {
		t.Errorf("hint does not offer the obvious candidate: %q", verr.Hint)
	}
}

// A cycle is ordinary in an npm graph, and a walk that did not say so would
// not terminate.
func TestWhyTerminatesOnACycle(t *testing.T) {
	cyclic := `{
	  "lockfileVersion": 3,
	  "packages": {
	    "": { "dependencies": { "a": "1.0.0" } },
	    "node_modules/a": { "version": "1.0.0", "dependencies": { "b": "1.0.0" } },
	    "node_modules/b": { "version": "1.0.0", "dependencies": { "c": "1.0.0" } },
	    "node_modules/c": { "version": "1.0.0", "dependencies": { "b": "1.0.0" } }
	  }
	}`
	done := make(chan view.View, 1)
	go func() {
		v, err := whyIn(t, cyclic, map[string]any{"package": "b"})
		if err != nil {
			close(done)
			return
		}
		done <- v
	}()
	v, ok := <-done
	if !ok {
		t.Fatal("why failed on a cyclic graph")
	}
	root := sectionOf(t, v, "reached from").(view.Tree).Roots[0]
	if len(root.Children) == 0 {
		t.Error("a package inside a cycle got no route at all")
	}
}

// Two copies of one package at two versions is the ordinary state of a
// JavaScript tree, and is often the whole answer: one of them is the
// vulnerable one and only one thing pulled it in.
func TestWhyShowsEveryVersionItFound(t *testing.T) {
	twoCopies := `{
	  "lockfileVersion": 3,
	  "packages": {
	    "": { "dependencies": { "old": "1.0.0", "new": "2.0.0" } },
	    "node_modules/old": { "version": "1.0.0", "dependencies": { "shared": "1.0.0" } },
	    "node_modules/new": { "version": "2.0.0", "dependencies": { "shared": "2.0.0" } },
	    "node_modules/shared": { "version": "2.0.0" },
	    "node_modules/old/node_modules/shared": { "version": "1.0.0" }
	  }
	}`
	v, err := whyIn(t, twoCopies, map[string]any{"package": "shared"})
	if err != nil {
		t.Fatal(err)
	}
	if got := pairValue(sectionOf(t, v, "summary").(view.KeyValue), "version"); got != "1.0.0, 2.0.0" {
		t.Errorf("version = %q, want both copies named", got)
	}
	if roots := sectionOf(t, v, "reached from").(view.Tree).Roots; len(roots) != 2 {
		t.Errorf("want a root per version, got %d", len(roots))
	}
}

func pairValue(kv view.KeyValue, key string) string {
	for _, p := range kv.Pairs {
		if p.Key == key {
			return p.Value
		}
	}
	return ""
}
