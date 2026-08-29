package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	vaultapi "github.com/hashicorp/vault/api"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Finding a secret in somebody else's Vault is the job, and `vault kv list`
// answers one level at a time.
//
// A name ending in "/" is another path to list, so learning the shape of a
// mount means retyping the path input over and over — which is exactly how a
// person who does not already know where things are gives up and asks a
// colleague. The shape is the thing they are missing, and it is one walk away.

const (
	// maxTreeNodes bounds what comes back. A KV mount can hold a hundred
	// thousand paths, and a tree nobody can read is not a better answer than
	// no tree: this is a map somebody is orienting themselves on, and past a
	// few hundred entries the way to find something is a narrower --path.
	maxTreeNodes = 500
	// maxTreeRequests bounds what goes out, which is the half that costs
	// somebody else money. One LIST per folder, and a deep mount has more
	// folders than a person expects — audit devices record every one of them.
	maxTreeRequests = 200
)

// Read and ungated, like vault.kv.list, and the reason is worth stating
// rather than inherited: this discloses exactly what a caller could already
// have by walking kv.list level by level. Fewer round trips is not a wider
// permission — the safety model classifies by blast radius, and the blast radius of a
// list of names is the same list of names.
//
// What it does change is the record. Two hundred listings become one line in
// the ledger, so an operator reading what an agent did sees "mapped the
// mount" instead of a wall of individual calls, which is the more honest
// summary of what happened.
func kvTreeCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:         "vault.kv.tree",
		Summary:    "The whole shape of a KV mount in one call — names only",
		Safety:     plugin.Read,
		Idempotent: true,
		Description: "`vault kv list` answers one level at a time, and a name ending in \"/\" is " +
			"another path to list — so learning where anything lives in somebody else's Vault " +
			"means retyping the path over and over. This walks it once and draws the shape.\n\n" +
			"Names only, never values: the same Read/Write split vault.kv.list and vault.kv.get " +
			"already draw, and the reason a listing is ungated while a read is not.\n\n" +
			"Bounded in both directions, and it says when it stopped. A folder the token may not " +
			"list is marked and stepped over rather than ending the walk — a policy that grants " +
			"part of a mount is the normal case, and the part you can see is still the answer " +
			"you came for.",
		Run: runKVTree,
	}, mountField(),
		plugin.Field{Name: "path", Type: plugin.String, Positional: true, Default: "",
			Help: "start here; empty walks the whole mount",
			Live: true, Suggest: suggestPaths},
		plugin.Field{Name: "depth", Type: plugin.Int, Default: 4, Min: 1, Max: 20,
			Help: "how many levels to expand"})
}

func runKVTree(ctx context.Context, req plugin.Request) (view.View, error) {
	return withClient(req, func(client *vaultapi.Client) (view.View, error) {
		w := &treeWalk{ctx: ctx, client: client, req: req, mount: req.String("mount")}
		start := strings.Trim(req.String("path"), "/")

		names, err := w.list(start)
		if err != nil {
			// The root is different from every other level: a walk that cannot
			// start has nothing to show, so this is the one listing whose
			// failure is the answer.
			return nil, classify(err, req)
		}
		children := w.expand(start, names, req.Int("depth")-1)

		label := w.mount
		if start != "" {
			label += "/" + start
		}
		detail := fmt.Sprintf("%d secrets", w.secrets)
		if w.folders > 0 {
			detail += fmt.Sprintf(", %d folders", w.folders)
		}
		if w.stopped != "" {
			detail += " — " + w.stopped
		}
		return view.Tree{Roots: []view.Node{{
			Label: label, Detail: detail, Children: children,
		}}}, nil
	})
}

// treeWalk carries the budgets and the tally across a recursive walk, so the
// bound is one thing rather than a parameter every level has to remember to
// pass down.
type treeWalk struct {
	ctx    context.Context
	client *vaultapi.Client
	req    plugin.Request
	mount  string

	requests int
	nodes    int
	secrets  int
	folders  int
	// stopped names the bound that was reached, in the words the operator
	// needs to do something about it. Empty means the walk finished.
	stopped string
}

func (w *treeWalk) list(path string) ([]string, error) {
	w.requests++
	p := w.mount + "/metadata"
	if path != "" {
		p += "/" + path
	}
	secret, err := w.client.Logical().ListWithContext(w.ctx, p)
	if err != nil {
		return nil, err
	}
	if secret == nil {
		return nil, nil
	}
	raw, _ := secret.Data["keys"].([]interface{})
	names := make([]string, 0, len(raw))
	for _, k := range raw {
		if s, ok := k.(string); ok {
			names = append(names, s)
		}
	}
	sort.Strings(names)
	return names, nil
}

// expand turns one level's names into nodes, recursing into folders while
// there is depth and budget left.
func (w *treeWalk) expand(parent string, names []string, depth int) []view.Node {
	var out []view.Node
	for _, name := range names {
		if w.nodes >= maxTreeNodes {
			w.stopped = fmt.Sprintf("stopped at %d paths; narrow it with --path", maxTreeNodes)
			return append(out, view.Node{Label: "…", Detail: w.stopped})
		}
		w.nodes++
		full := name
		if parent != "" {
			full = parent + "/" + name
		}
		if !strings.HasSuffix(name, "/") {
			w.secrets++
			out = append(out, view.Node{Label: name})
			continue
		}

		w.folders++
		folder := strings.TrimSuffix(full, "/")
		node := view.Node{Label: name}
		switch {
		case depth <= 0:
			node.Detail = "not expanded — raise --depth"
		case w.requests >= maxTreeRequests:
			w.stopped = fmt.Sprintf("stopped after %d listings; narrow it with --path", maxTreeRequests)
			node.Detail = "not expanded — " + w.stopped
		default:
			children, err := w.list(folder)
			if err != nil {
				// **Marked and stepped over, not fatal.** A token whose policy
				// covers part of a mount is the ordinary case in a shared
				// Vault, and ending the walk at the first folder it may not
				// read would turn "here is the half you can see" into nothing
				// at all. The reason is the classified one, so a denial and an
				// engine that is not KV read differently.
				node.Detail = classify(err, w.req).Message
				break
			}
			node.Children = w.expand(folder, children, depth-1)
		}
		out = append(out, node)
	}
	return out
}
