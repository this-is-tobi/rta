package audit

import (
	"sort"
	"strings"
)

// Where a vulnerable dependency came from.
//
// The advisory list answers "are we affected". This answers the question that
// arrives half a second later and decides what the rest of the day looks
// like: **did we ask for this, or did something else drag it in** — and if
// something else, what. A package the project names itself is one somebody
// bumps in a minute. A package four levels down is one where the upgrade
// belongs to whoever owns the thing above it, and the choice is between
// waiting, overriding a resolution, or dropping the top-level dependency
// altogether. Those are three different afternoons, and today the report gave
// no way to tell which one you were in.
//
// Everything here is read from what the project already committed. Nothing is
// resolved, no package manager is run, no network is touched — the same rule
// manifest.go states for components, for the same reason.
//
// Three rules keep it honest, and all three are about refusing to invent:
//
//   - **A silent format says nothing, not "no".** Most lockfiles record which
//     dependencies the project asked for; several do not, because the answer
//     lives in the file beside them (package.json, pyproject.toml,
//     composer.json). Where the manifest does not say, this says it does not
//     say. "indirect" printed from a file that never mentioned the
//     distinction is a fact invented in a security report.
//   - **Edges are between names, not versions.** A lockfile edge records the
//     *range* the dependant asked for; which version satisfied it is a
//     resolution this deliberately does not reproduce. So a chain says "qs is
//     required by express", which is what the file states, and it does not
//     claim to know which copy of qs in a tree that holds two of them. Where
//     that matters the report shows every chain it found rather than picking
//     one.
//   - **Shallow, and separate from the component parsers.** parseComponents
//     and parseGraph read the same bytes and share nothing. A mistake here
//     costs a missing explanation; a mistake there would report the wrong
//     version as vulnerable. Keeping them apart is what makes the second
//     impossible to cause from the first.

// graph is what a project's manifests record about their own structure.
type graph struct {
	// direct and indirect are the packages a manifest placed on one side or
	// the other, by ref. Two sets rather than one boolean map, because a
	// package in neither is a third answer — the format did not say — and the
	// report has to be able to give it.
	direct   map[string]bool
	indirect map[string]bool
	// directAt is the direct set again for the formats that say which
	// *version* the project asked for, keyed "eco/name@version"; pinned names
	// the refs it covers.
	//
	// **Because a name is not enough when a tree holds two copies of it.** A
	// real project asks for `commander: ^15.0.0` and carries 15.0.0, 7.2.0 and
	// 8.3.0 — the last two dragged in by other packages. Keyed on the name,
	// all three were reported as "a direct dependency", so an advisory against
	// 7.2.0 sent somebody to bump a version in a package.json that has nothing
	// to do with that copy. Measured across five real lockfiles: 9 such
	// components in one, 12 in another.
	//
	// The four npm-family formats all state the resolved version of a direct
	// dependency, so this is exactness rather than a workaround. Where a
	// format does not, pinned is empty for that ref and the name-keyed answer
	// stands — with relation refusing to claim anything when the name is
	// ambiguous.
	directAt map[string]bool
	pinned   map[string]bool
	// requires maps a package to the packages it requires, by ref.
	requires map[string][]string
	// count is how many edges requires holds.
	//
	// Kept rather than computed, and that is a correctness matter and not a
	// tidiness one: the bound below is checked on every insert, and computing
	// it by walking the whole map made construction quadratic in the number of
	// edges. Measured at 0.4 s for 25,000 edges, 1.6 s for 50,000 and 6.1 s
	// for 100,000 — a clean 4× per doubling — so the *bound itself* permitted
	// around twenty-five seconds of pure map-walking on the happy path of a
	// large pnpm monorepo, before any network call.
	count int
	// truncated records that a manifest was larger than maxEdges and the rest
	// of its structure was not read. Reported, never silent: a chain that
	// stops early because a bound was hit reads exactly like a chain that
	// stops early because it reached the top.
	truncated bool
}

// maxEdges bounds one run's structure. A pnpm monorepo lockfile can carry
// tens of thousands of packages; the edges are the largest thing this plugin
// holds in memory and the only one that grows with somebody else's tree
// rather than with their project. Twenty times the largest real lockfile
// measured, and a bound that is announced when it fires.
const maxEdges = 200000

// ref names a package inside its ecosystem. Versionless, per the second rule
// above, and ecosystem-qualified because a monorepo holds `redis` on npm and
// `redis` on PyPI and they are not the same package.
func ref(ecosystem, name string) string {
	if ecosystem == "PyPI" {
		name = normalizePyPI(name)
	}
	return ecosystem + "/" + name
}

// normalizePyPI applies PEP 503's rule: names compare case-insensitively with
// runs of `-`, `_` and `.` all equal to a single `-`.
//
// Needed because the formats spell the same package two ways in one file.
// poetry.lock writes `name = "jinja2"` in the record and `Jinja2 = ">=3.1.2"`
// in the [package.dependencies] table three lines down; pip-compile writes
// `# via Flask`. Without this an edge points at a package that is right there
// under a different spelling, and the chain silently stops one step short.
func normalizePyPI(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	dash := false
	for _, r := range strings.ToLower(name) {
		if r == '-' || r == '_' || r == '.' {
			dash = true
			continue
		}
		if dash && b.Len() > 0 {
			b.WriteByte('-')
		}
		dash = false
		b.WriteRune(r)
	}
	return b.String()
}

// refName is ref's inverse for display: the part a person recognises.
func refName(r string) string {
	if _, name, ok := strings.Cut(r, "/"); ok {
		return name
	}
	return r
}

func newGraph() graph {
	return graph{
		direct:   map[string]bool{},
		indirect: map[string]bool{},
		directAt: map[string]bool{},
		pinned:   map[string]bool{},
		requires: map[string][]string{},
	}
}

// at keys a package's ref together with the version installed.
func at(r, version string) string { return r + "@" + version }

// pin records that the project asked for this exact version of this package.
func (g *graph) pin(r, version string) {
	g.direct[r] = true
	g.pinned[r] = true
	if version != "" {
		g.directAt[at(r, version)] = true
	}
}

// asked collects, per package, the versions this project's own manifests
// resolved their declarations to — and whether any declaration could not be
// located at all.
//
// **A pin is a claim about every copy of a name, so it may only be made from a
// complete answer.** relation reads `pinned[r]` as "this file said which copy
// the project asked for", and answers relIndirect for every other copy on the
// strength of it. That is only true if every declaration of the name was
// found: in a workspace repository two packages declare `commander` at
// incompatible ranges, one copy is hoisted to the top and the other is nested
// beside the workspace that asked for it, and pinning only the hoisted one
// made the report say "indirect" about a dependency named in the package.json
// two directories down. Where a declaration cannot be attributed, the name
// stays unpinned and falls back to the name-keyed answer, which relation
// already withholds when the inventory holds more than one copy.
type asked struct {
	versions map[string][]string
	partial  map[string]bool
}

func newAsked() asked {
	return asked{versions: map[string][]string{}, partial: map[string]bool{}}
}

// found records that a declaration of r resolved to this version.
func (a asked) found(r, version string) {
	a.versions[r] = append(a.versions[r], version)
}

// missing records a declaration of r that no installed entry answers, which
// disqualifies the name from being pinned at all.
func (a asked) missing(r string) { a.partial[r] = true }

// apply promotes the complete answers to pins and leaves the rest name-keyed.
func (a asked) apply(g *graph) {
	for r, versions := range a.versions {
		if a.partial[r] {
			continue
		}
		for _, v := range versions {
			g.pin(r, v)
		}
	}
}

// edges counts what this graph holds, for the bound.
func (g graph) edges() int { return g.count }

// merge folds another manifest's structure in.
//
// Union, deliberately, and the direct set wins a disagreement: in a monorepo
// a package can be a workspace's own dependency and another workspace's
// transitive one, and "somebody here asked for this by name" is the answer
// that changes what you do about it.
func (g *graph) merge(o graph) {
	if g.requires == nil {
		*g = newGraph()
	}
	// **A pin only speaks for a package when every manifest that called it
	// direct said which version.** Two projects under one scan, one with a
	// package-lock.json (which states the resolved version) and one with a
	// composer.json or a yarn v1 lockfile (which does not), both depending on
	// the same package: unioning the pins let the first project's pin answer
	// relIndirect about the second project's copy — a version-exact statement
	// neither file made. Computed from each side's own pre-merge state, so it
	// does not matter which manifest was read first.
	loose := map[string]bool{}
	for _, side := range []graph{*g, o} {
		for r := range side.direct {
			if !side.pinned[r] {
				loose[r] = true
			}
		}
	}
	for r := range loose {
		delete(g.pinned, r)
	}
	for r := range o.direct {
		g.direct[r] = true
	}
	for r := range o.indirect {
		g.indirect[r] = true
	}
	for r := range o.directAt {
		g.directAt[r] = true
	}
	for r := range o.pinned {
		if !loose[r] {
			g.pinned[r] = true
		}
	}
	for from, to := range o.requires {
		for _, t := range to {
			g.require(from, t)
		}
	}
	g.truncated = g.truncated || o.truncated
}

// Relations a component can stand in to the project.
const (
	relDirect   = "direct"
	relIndirect = "indirect"
	// relUnstated is the empty string so that "the format did not say" is the
	// zero value, and a caller that forgets to handle it renders nothing
	// rather than rendering a guess.
	relUnstated = ""
)

// relation reports whether the project asked for this package.
//
// ambiguous names the refs this project holds more than one version of, which
// the graph cannot know on its own — it is a fact about the inventory. For
// those, a name-keyed direct set says nothing this function may repeat: the
// project asked for *a* commander and this is one of three, and which one is
// not something the file records. A format that pinned the version answers
// exactly and never reaches that case.
func (g graph) relation(c component, ambiguous map[string]bool) string {
	r := ref(c.ecosystem, c.name)
	if g.pinned[r] && c.version != "" {
		if g.directAt[at(r, c.version)] {
			return relDirect
		}
		// Pinned to a different version: this copy is something else's, and
		// the file said so. Indirect is a statement here, not an inference.
		return relIndirect
	}
	switch {
	case g.direct[r]:
		if ambiguous[r] {
			return relUnstated
		}
		return relDirect
	case g.indirect[r]:
		return relIndirect
	}
	return relUnstated
}

// via returns the chains that lead to target, shortest first: each chain runs
// from a package the project asked for down to target, named at every step.
//
// Breadth-first upwards from the target, so the shortest explanation — the
// one somebody can act on soonest — is the one reported. A chain never
// revisits a package it already contains: dependency cycles are ordinary in
// npm and Cargo graphs, and a walk that did not say so would not terminate.
//
// maxChains bounds the answer because a widely-used utility is reached from
// forty places and a report that lists all forty has said nothing. maxDepth
// bounds the walk for the same reason a chain of eleven packages explains
// less than a chain of three.
func (g graph) via(target string, maxChains, maxDepth int) [][]string {
	if len(g.requires) == 0 {
		return nil
	}
	// Built on demand rather than kept alongside: the reverse index is only
	// ever wanted for the handful of packages that turned out to be
	// vulnerable, and building it for a clean project would be the largest
	// thing this plugin did on the happy path.
	requiredBy := map[string][]string{}
	for from, to := range g.requires {
		for _, t := range to {
			requiredBy[t] = append(requiredBy[t], from)
		}
	}

	type step struct {
		chain []string
		seen  map[string]bool
	}
	start := step{chain: []string{target}, seen: map[string]bool{target: true}}
	queue := []step{start}
	var out [][]string
	// Deduplicated by their rendered form: two workspaces of a monorepo
	// reaching the same package the same way is one explanation, not two.
	shown := map[string]bool{}

	emit := func(chain []string) {
		if len(chain) < 2 {
			return // "it is reached from itself" explains nothing
		}
		key := strings.Join(chain, ">")
		if !shown[key] {
			shown[key] = true
			out = append(out, chain)
		}
	}

	// A step budget as well as the two the caller sets. maxChains only stops
	// the walk once three chains have been *emitted*, and a package required
	// from a thousand places whose requirers are themselves required from a
	// thousand places emits nothing for a long time while the queue widens by
	// a factor of a thousand a level. The two visible bounds shape the answer;
	// this one bounds the work, and it is the only one a hostile lockfile
	// could otherwise walk past.
	for steps := 0; len(queue) > 0 && len(out) < maxChains && steps < maxWalk; steps++ {
		cur := queue[0]
		queue = queue[1:]
		head := cur.chain[0]
		// The top of a chain: the project asked for this by name, or nothing
		// in any manifest requires it. Both are explanations worth printing,
		// and the caller can tell them apart because it has the relation.
		if g.direct[head] || len(requiredBy[head]) == 0 {
			emit(cur.chain)
			continue
		}
		// Deep enough. The ellipsis is the point: a chain cut by a bound and a
		// chain that reached the top render identically otherwise, and one of
		// them is a claim about where the dependency came from.
		if len(cur.chain) >= maxDepth {
			emit(append([]string{deeper}, cur.chain...))
			continue
		}
		// Sorted, so the same lockfile explains itself the same way twice.
		parents := append([]string(nil), requiredBy[head]...)
		sort.Strings(parents)
		grew := false
		for _, p := range parents {
			if cur.seen[p] {
				continue
			}
			grew = true
			seen := make(map[string]bool, len(cur.seen)+1)
			for k := range cur.seen {
				seen[k] = true
			}
			seen[p] = true
			queue = append(queue, step{chain: append([]string{p}, cur.chain...), seen: seen})
		}
		// Every way up leads back through something already on this chain: a
		// cycle, which is ordinary. Without this the chain is dropped and a
		// package inside a cycle gets no explanation at all.
		if !grew {
			emit(append([]string{deeper}, cur.chain...))
		}
	}
	return out
}

// deeper marks a chain this walk stopped short of the top of.
const deeper = "…"

// maxWalk bounds one via() call regardless of the graph's shape.
//
// It is not tuned against a measurement and does not need to be: the search
// stops as soon as it has three chains, so on any tree where an explanation
// exists it ends in tens of steps, and the only inputs that reach this
// ceiling are the ones where the walk would otherwise widen without
// terminating. What it buys is that the cost of "why is lodash here" depends
// on rta rather than on how tangled somebody else's lockfile is — the one
// property worth having when the file is untrusted input.
const maxWalk = 5000
