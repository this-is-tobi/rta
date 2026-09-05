package audit

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// "Why is this here?" — asked about one package, answered from the lockfile.
//
// `audit deps` answers it in a sentence for the packages that turned out to be
// vulnerable, because a sentence is what fits in a finding. This is the same
// question asked deliberately, about any package, with room for the whole
// answer: every route from something the project asked for down to the package
// in hand, as a tree rather than a list, so shared prefixes collapse and the
// shape of the problem is visible.
//
// It exists as its own capability rather than a flag on `deps` because it is a
// different question with a different subject. `deps` is about a project; this
// is about a package. A flag that changed the subject of a command would also
// change the shape of its output, which is the kind of thing that makes a
// command impossible to script against.

// How much of a graph is worth drawing. Larger than the finding's bounds by a
// long way — this is the view somebody opened *to see the whole thing* — and
// still bounded, because a widely-shared utility in a large monorepo is
// reached from hundreds of places and a tree with hundreds of leaves has not
// explained anything.
const (
	maxTreeDepth = 12
	maxTreeNodes = 300
)

func runWhy(ctx context.Context, req plugin.Request) (view.View, error) {
	name := strings.TrimSpace(req.String("package"))
	if name == "" {
		return nil, view.Errorf("audit.why.nopackage", "no package named").
			WithHint("`rta audit why lodash` — the name as the lockfile spells it")
	}
	path := strings.TrimSpace(req.String("path"))
	if path == "" {
		path = "."
	}
	proj, verr := openProject(ctx, req, path)
	if verr != nil {
		return nil, verr
	}
	names, shown, truncated, err := proj.manifests(req.Bool("recursive"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, view.Errorf("audit.why.nopath", "no such path: %s", path).
				WithHint("pass the directory holding the lockfile or SBOM, the file itself, " +
					"or a repository URL")
		}
		return nil, view.Errorf("audit.why.path", "reading %s: %v", path, err)
	}
	if len(names) == 0 {
		return nil, view.Errorf("audit.why.nomanifest", "no lockfile or SBOM in %s", remoteLabel(path)).
			WithHint("reads what a project already declares, so one of these has to exist: " +
				strings.Join(ecosystems, "; "))
	}

	inv := read(proj.fsys, names, shown)
	found := matching(inv.all, name)
	if len(found) == 0 {
		return nil, notInstalled(name, inv)
	}

	p := plugin.NewPage(ctx, req)
	p.PutAs("summary", "summary", whySummary(found, inv, truncated || inv.truncated))
	tree, cut := whyTree(inv.structure, found)
	p.PutAs("reached from", "reached from", tree)
	if cut {
		p.Warn(view.Errorf("audit.why.truncated",
			"the graph is larger than this draws, so some routes are not shown").
			WithHint("`" + whyCommand(found[0]) + "` prints all of them"))
	}
	return p.View(), nil
}

// matching finds every component the name could mean.
//
// Every version, because two copies of one package at two versions is the
// ordinary state of a JavaScript tree and is often the entire answer: one of
// them is the vulnerable one and only one thing pulled it in. And every
// ecosystem, because a polyglot repository really does hold `redis` on npm and
// `redis` on PyPI, and refusing to guess which was meant is better than
// picking.
//
// Matched exactly first, then by the ecosystem's own equivalence rule, so
// `Jinja2` finds `jinja2` without `express` ever finding `Express`.
func matching(all []component, name string) []component {
	var exact, loose []component
	for _, c := range all {
		switch {
		case c.name == name:
			exact = append(exact, c)
		case c.ecosystem == "PyPI" && normalizePyPI(c.name) == normalizePyPI(name):
			loose = append(loose, c)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return loose
}

// notInstalled is the error for a package this project does not have, with the
// near misses that are usually what was meant.
func notInstalled(name string, inv inventory) *view.Error {
	near := nearby(name, inv.all)
	err := view.Errorf("audit.why.absent", "nothing in this project declares %q", name)
	if len(near) > 0 {
		return err.WithHint("did you mean: " + strings.Join(near, ", "))
	}
	return err.WithHint(strconv.Itoa(len(inv.all)) + " dependencies were read from " +
		manifestSummary(".", inv.manifests) + " — `rta audit deps --offline` lists them")
}

// nearby is the handful of installed packages whose names contain, or are
// contained by, what was asked for.
//
// Substring rather than an edit distance, because the mistake this catches is
// not a typo: it is a scope left off (`core` for `@babel/core`), a module path
// shortened (`cobra` for `github.com/spf13/cobra`), or a package remembered by
// the half of its name that is the interesting half.
func nearby(name string, all []component) []string {
	lower := strings.ToLower(name)
	var out []string
	seen := map[string]bool{}
	for _, c := range all {
		l := strings.ToLower(c.name)
		if !strings.Contains(l, lower) && !strings.Contains(lower, l) {
			continue
		}
		if seen[c.name] {
			continue
		}
		seen[c.name] = true
		out = append(out, c.name)
		if len(out) == 5 {
			break
		}
	}
	sort.Strings(out)
	return out
}

// whySummary is the answer in the four lines somebody reads before the tree.
func whySummary(found []component, inv inventory, truncated bool) view.KeyValue {
	c := found[0]
	versions := make([]string, 0, len(found))
	ecos := map[string]bool{}
	for _, f := range found {
		versions = append(versions, f.version)
		ecos[f.ecosystem] = true
	}
	sort.Strings(versions)

	pairs := []view.Pair{
		{Key: "package", Value: c.name},
		{Key: "version", Value: strings.Join(versions, ", ")},
		{Key: "ecosystem", Value: strings.Join(sortedSet(ecos), ", ")},
		{Key: "declared in", Value: filepath.Base(c.source)},
	}

	// The relation first among the things that vary, because it is the one that
	// decides whether the tree below is worth reading at all: a direct
	// dependency needs no explanation, it needs a version bump.
	switch inv.relation(c) {
	case relDirect:
		pairs = append(pairs, view.Pair{Key: "relation",
			Value: "direct — this project asks for it by name"})
	case relIndirect:
		pairs = append(pairs, view.Pair{Key: "relation",
			Value: "indirect — something else pulled it in"})
	default:
		pairs = append(pairs, view.Pair{Key: "relation",
			Value: "not stated — " + filepath.Base(c.source) + " does not record which dependencies are direct"})
	}
	if cmd := whyCommand(c); cmd != "" {
		// Always offered, not only when this could not answer. What rta reads
		// is a committed file; what the command reads is the resolver's own
		// state, and where those disagree the command is right and the
		// disagreement is worth knowing about.
		pairs = append(pairs, view.Pair{Key: "or run", Value: cmd})
	}
	if truncated {
		pairs = append(pairs, view.Pair{Key: "scan",
			Value: "stopped at " + strconv.Itoa(maxManifests) + " manifests — narrow the path to cover the rest"})
	}
	return view.KeyValue{Pairs: pairs}
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// whyTree draws the package at the root and what requires it beneath, upwards
// until every branch reaches something the project asked for.
//
// Inverted on purpose. The forward direction — a direct dependency at the top,
// the package at the bottom — is how the tree was built, and it is the wrong
// way round for this question: the subject would be a leaf repeated once per
// route, and the thing somebody is looking at would be the thing hardest to
// find. Inverted, the subject is the root, and the routes share their near
// ends, which is where the actionable answer usually is — "four things need
// it, and all four go through webpack".
//
// The bool reports that a bound was hit, so the caller can say so rather than
// let a cut branch read like a complete one.
func whyTree(g graph, found []component) (view.Tree, bool) {
	requiredBy := map[string][]string{}
	for from, to := range g.requires {
		for _, t := range to {
			requiredBy[t] = append(requiredBy[t], from)
		}
	}

	nodes := 0
	cut := false
	var build func(r string, seen map[string]bool, depth int) []view.Node
	build = func(r string, seen map[string]bool, depth int) []view.Node {
		parents := append([]string(nil), requiredBy[r]...)
		sort.Strings(parents)
		var out []view.Node
		for _, p := range parents {
			// A cycle is ordinary in an npm or Cargo graph; a branch that
			// revisits what it came from is not a route, it is a loop.
			if seen[p] {
				continue
			}
			if nodes >= maxTreeNodes || depth >= maxTreeDepth {
				cut = true
				return append(out, view.Node{Label: deeper, Detail: "more routes not shown"})
			}
			nodes++
			branch := make(map[string]bool, len(seen)+1)
			for k := range seen {
				branch[k] = true
			}
			branch[p] = true
			node := view.Node{Label: refName(p), Children: build(p, branch, depth+1)}
			if g.direct[p] {
				// Where a branch ends is the actionable part: this is the one
				// the project asked for, so this is the one to change.
				node.Detail = "← asked for by this project"
			}
			out = append(out, node)
		}
		return out
	}

	roots := make([]view.Node, 0, len(found))
	for _, c := range found {
		r := ref(c.ecosystem, c.name)
		label := c.name + " " + c.version
		root := view.Node{Label: label, Children: build(r, map[string]bool{r: true}, 0)}
		switch {
		case g.direct[r]:
			root.Detail = "asked for by this project"
		case len(root.Children) == 0:
			root.Detail = "nothing read records what requires it"
		}
		roots = append(roots, root)
	}
	return view.Tree{Roots: roots}, cut
}
