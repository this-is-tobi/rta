package audit

import (
	"encoding/json"
	"strings"
)

// Reading the structure out of the same files manifest.go reads the list out
// of.
//
// What each format actually records, which is the whole design constraint:
//
//	go.mod            direct/indirect, marked per require. No edges — the
//	                  graph lives in the module cache, and `go mod graph`
//	                  is the command that has it.
//	package-lock.json direct (the root and each workspace entry) and every
//	                  edge, v2/v3. v1's nested shape is not read.
//	pnpm-lock.yaml    direct (`importers:`) and every edge (`snapshots:` in
//	                  v9, `packages:` in v5/v6).
//	yarn.lock         every edge, both dialects. Direct only for Berry,
//	                  which records the workspace entries v1 does not.
//	bun.lock          direct (`workspaces`) and every edge.
//	Cargo.lock        direct (the sourceless workspace members) and edges.
//	uv.lock           direct (the virtual/editable root) and edges.
//	poetry.lock       edges. Direct lives in pyproject.toml.
//	Gemfile.lock      direct (`DEPENDENCIES`) and edges.
//	composer.lock     edges. Direct lives in composer.json.
//	requirements.txt  both, but only when pip-compile wrote it — its `# via`
//	                  annotations are exact. A hand-written or pip-frozen
//	                  file says nothing and is read as saying nothing.
//	Pipfile.lock      nothing. The format has no edges to read.
//	CycloneDX SBOM    direct (what the metadata component depends on) and
//	                  every edge.
//	SPDX SBOM         nothing yet. Its relationships are expressive enough
//	                  to need care, and an SBOM's producer had the real
//	                  graph — see the native commands the report names.
//
// Every one of these is a line or token scanner over a file a program wrote.
// None of them pulls in a YAML or TOML dependency, for lockfiles.go's reason:
// a generated file has a rigid shape, and a scanner that reads the two things
// it needs cannot be wrong about the rest.

// parseGraph reads what a manifest records about its own structure. An
// unrecognised or unstructured file yields the zero graph, which states
// nothing — the honest answer, and the one every caller already handles.
func parseGraph(base string, data []byte) graph {
	switch base {
	case "go.mod":
		return goModGraph(string(data))
	case "package-lock.json":
		return npmLockGraph(data)
	case "pnpm-lock.yaml":
		return pnpmGraph(string(data))
	case "yarn.lock":
		return yarnGraph(string(data))
	case "bun.lock":
		return bunGraph(data)
	case "Cargo.lock":
		return tomlGraph(string(data), "crates.io", cargoLock)
	case "uv.lock":
		return tomlGraph(string(data), "PyPI", uvLock)
	case "poetry.lock":
		return tomlGraph(string(data), "PyPI", poetryLock)
	case "Gemfile.lock":
		return gemfileGraph(string(data))
	case "composer.lock":
		return composerGraph(data)
	case "requirements.txt":
		return requirementsGraph(string(data))
	}
	if strings.HasSuffix(base, ".json") {
		return sbomGraph(data)
	}
	return graph{}
}

// stateIndirect places every package the manifest listed, and did not name as
// direct, on the indirect side.
//
// Only called from a format that states the direct set *exhaustively*. That
// is the whole precondition: in a file that lists which dependencies the
// project asked for, absence from that list is a statement. In a file that
// does not — yarn v1, poetry.lock, composer.lock — absence means nothing, and
// calling this there would manufacture "indirect" out of silence.
func (g *graph) stateIndirect(all []string) {
	for _, r := range all {
		if !g.direct[r] {
			g.indirect[r] = true
		}
	}
}

// full reports whether the edge budget is spent.
func (g *graph) full() bool {
	if g.edges() >= maxEdges {
		g.truncated = true
		return true
	}
	return false
}

// require records one edge, ignoring the self-edges some formats emit for a
// workspace that depends on a sibling by its own name, and ignoring an edge
// this graph already has.
//
// Deduplicated because the same edge arrives by ordinary routes, not odd
// ones: an npm package naming a dependency in both `dependencies` and
// `optionalDependencies`, and — the case manifest.go calls "the normal case,
// not an odd one" — a CycloneDX SBOM merged with the lockfile it was
// generated from. Left in, `rta audit why` drew every route twice and each
// copy spent one of its three hundred nodes.
//
// Scanning the slice rather than keeping a set per package: a package's
// dependency list is tens of entries, and a map per node would cost more than
// it saves at every size this actually sees.
func (g *graph) require(from, to string) {
	if from == "" || to == "" || from == to || g.full() {
		return
	}
	for _, existing := range g.requires[from] {
		if existing == to {
			return
		}
	}
	g.requires[from] = append(g.requires[from], to)
	g.count++
}

// goModGraph reads the `// indirect` marker, which is the one thing go.mod
// records and every other Go manifest does not.
//
// No edges: go.mod lists the module graph's *requirements*, flattened by MVS,
// and which module pulled in which is knowledge the module cache holds. `go
// mod why` is the command that has it, and the report names it rather than
// guessing.
func goModGraph(text string) graph {
	g := newGraph()
	inBlock := false
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		comment := ""
		if i := strings.Index(line, "//"); i >= 0 {
			comment, line = line[i:], strings.TrimSpace(line[:i])
		}
		switch {
		case line == "require (":
			inBlock = true
			continue
		case inBlock && line == ")":
			inBlock = false
			continue
		case strings.HasPrefix(line, "require "):
			line = strings.TrimSpace(strings.TrimPrefix(line, "require "))
		case !inBlock:
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.HasPrefix(fields[1], "v") {
			continue
		}
		r := ref("Go", fields[0])
		if strings.Contains(comment, "indirect") {
			g.indirect[r] = true
		} else {
			g.direct[r] = true
		}
	}
	return g
}

// gemfileGraph reads both blocks a Gemfile.lock carries: the indentation
// under `specs:` is the edge list, and `DEPENDENCIES` at the bottom is what
// the Gemfile itself asked for.
//
// Four spaces is a gem, six is one of its requirements — the same grammar
// parseGemfileLock reads the versions out of.
func gemfileGraph(text string) graph {
	g := newGraph()
	const (
		none = iota
		specs
		dependencies
	)
	block, entry := none, ""
	var all []string

	for _, raw := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if indentOf(raw) == 0 {
			block = none
			if trimmed == "DEPENDENCIES" {
				block = dependencies
			}
			entry = ""
			continue
		}
		if indentOf(raw) <= 2 {
			if block != dependencies {
				block = none
				if trimmed == "specs:" {
					block = specs
				}
				entry = ""
				continue
			}
		}
		switch block {
		case dependencies:
			// `rails (~> 7.0)` or `rails` or `rails!` for a git/path gem.
			name, _, _ := strings.Cut(trimmed, " ")
			name = strings.TrimSuffix(name, "!")
			if name != "" {
				g.direct[ref("RubyGems", name)] = true
			}
		case specs:
			name, _, ok := strings.Cut(trimmed, " (")
			if !ok {
				name = trimmed
			}
			switch indentOf(raw) {
			case 4:
				entry = ref("RubyGems", name)
				all = append(all, entry)
			case 6:
				g.require(entry, ref("RubyGems", name))
			}
		}
	}
	if len(g.direct) > 0 {
		g.stateIndirect(all)
	}
	return g
}

// composerGraph reads the `require` map each package carries. The direct set
// is not here — it is in composer.json, which is not a lockfile and is not
// what this reads.
func composerGraph(data []byte) graph {
	var lock struct {
		Packages    []composerGraphPkg `json:"packages"`
		PackagesDev []composerGraphPkg `json:"packages-dev"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return graph{}
	}
	g := newGraph()
	for _, p := range append(append([]composerGraphPkg{}, lock.Packages...), lock.PackagesDev...) {
		if p.Name == "" {
			continue
		}
		from := ref("Packagist", p.Name)
		for dep := range p.Require {
			// php, ext-*, lib-* and composer-* are platform requirements, not
			// packages — Packagist has never heard of them and a chain running
			// through "php" explains nothing.
			if !strings.Contains(dep, "/") {
				continue
			}
			g.require(from, ref("Packagist", dep))
		}
	}
	return g
}

type composerGraphPkg struct {
	Name    string                     `json:"name"`
	Require map[string]json.RawMessage `json:"require"`
}

// requirementsGraph reads pip-compile's annotations, and nothing else.
//
// A requirements.txt is three different files wearing one name: something
// hand-written (every line direct), something `pip freeze` produced (no
// structure at all), and something pip-compile generated. Only the third
// records which is which, and it records it exactly:
//
//	flask==3.0.0
//	    # via -r requirements.in
//	click==8.1.7
//	    # via flask
//
// `# via -r <file>` means the project asked for it. `# via <package>` is an
// edge. A file with no annotations produces the zero graph, which is the
// truthful answer for the two shapes that record nothing.
func requirementsGraph(text string) graph {
	g := newGraph()
	pkg := ""
	var all []string
	// inVia gates the continuation lines. pip-compile writes the first source
	// after `# via` and any further ones on their own indented comment lines
	// beneath it, so a bare comment following a pin is only a source when a
	// `via` opened the run. Without the gate, `flask==3.0.0` followed by a
	// human's `# pinned until the 3.1 migration` becomes an edge from a
	// package called "pinned until the 3.1 migration".
	inVia := false
	annotated := false

	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			// Indented, always: pip-compile's annotations hang under the pin
			// they belong to, and a comment at column zero is the file's own
			// header or somebody's note.
			if pkg == "" || indentOf(raw) == 0 {
				continue
			}
			body := strings.TrimSpace(strings.TrimPrefix(line, "#"))
			// "via" as a word, not as a prefix. `# viable replacement pending`
			// under a pin became an edge from a package called "ble replacement
			// pending", and — worse than the nonsense node — set `annotated`,
			// which switches the whole file from "states nothing" to "states a
			// graph" and makes every other line's silence mean "indirect".
			switch rest, isVia := cutWord(body, "via"); {
			case isVia:
				inVia, annotated = true, true
				body = rest
				if body == "" {
					continue
				}
			case !inVia:
				continue
			}
			if projectSource(body) {
				// `-r requirements.in`, `-c constraints.txt`, or the project
				// itself — pip-tools writes `myproject (pyproject.toml)` when
				// the requirement came from the project's own metadata rather
				// than an input file. Read as an edge it invented a package
				// named after a filename and, in a file mixing both shapes,
				// left genuinely direct dependencies to be marked indirect.
				g.direct[ref("PyPI", pkg)] = true
				continue
			}
			for _, src := range strings.Split(body, ",") {
				if src = strings.TrimSpace(src); src != "" {
					g.require(ref("PyPI", src), ref("PyPI", pkg))
				}
			}
			continue
		}
		inVia = false
		if strings.HasPrefix(line, "-") {
			pkg = ""
			continue
		}
		name, _, ok := strings.Cut(line, "==")
		if !ok {
			pkg = ""
			continue
		}
		if i := strings.IndexAny(name, "[ \t"); i >= 0 {
			name = name[:i]
		}
		pkg = strings.TrimSpace(name)
		if pkg != "" {
			all = append(all, ref("PyPI", pkg))
		}
	}
	// A file with no annotations is a hand-written list or a `pip freeze`, and
	// neither records the distinction. Saying nothing is the truthful answer:
	// "every line is direct" is right for one of those shapes and badly wrong
	// for the other, and nothing in the file says which it is looking at.
	if !annotated {
		return graph{}
	}
	if len(g.direct) > 0 {
		g.stateIndirect(all)
	}
	return g
}

// sbomGraph reads CycloneDX's `dependencies` array, which is the one place in
// any of these formats where the graph is a first-class thing the producer
// wrote down on purpose.
//
// Refs are bom-refs — opaque identifiers, conventionally the purl — so they
// are resolved through the component list rather than parsed. A ref that
// names no component is dropped: a chain through an identifier nobody can
// look up is not an explanation.
func sbomGraph(data []byte) graph {
	var b struct {
		BOMFormat string `json:"bomFormat"`
		Metadata  struct {
			Component struct {
				BOMRef string `json:"bom-ref"`
			} `json:"component"`
		} `json:"metadata"`
		Components []struct {
			BOMRef  string `json:"bom-ref"`
			Name    string `json:"name"`
			Version string `json:"version"`
			PURL    string `json:"purl"`
		} `json:"components"`
		Dependencies []struct {
			Ref       string   `json:"ref"`
			DependsOn []string `json:"dependsOn"`
		} `json:"dependencies"`
	}
	if err := json.Unmarshal(data, &b); err != nil || b.BOMFormat == "" {
		return graph{}
	}
	byRef := map[string]string{}
	// Every component listed, not only those that turn up as a dependency
	// ref: a leaf has no entry of its own in the dependencies array, and it
	// is exactly the leaves that "indirect" most needs to be said about.
	var all []string
	for _, c := range b.Components {
		got, ok := fromPURL(c.PURL, "")
		if !ok {
			got = component{name: c.Name, version: c.Version}
		}
		if got.name == "" {
			continue
		}
		r := ref(got.ecosystem, got.name)
		all = append(all, r)
		if c.BOMRef != "" {
			byRef[c.BOMRef] = r
		}
		if c.PURL != "" {
			byRef[c.PURL] = r
		}
	}
	g := newGraph()
	root := b.Metadata.Component.BOMRef
	for _, d := range b.Dependencies {
		from, known := byRef[d.Ref]
		for _, on := range d.DependsOn {
			to, ok := byRef[on]
			if !ok {
				continue
			}
			if d.Ref == root && root != "" {
				g.direct[to] = true
				continue
			}
			if known {
				g.require(from, to)
			}
		}
	}
	if len(g.direct) > 0 {
		g.stateIndirect(all)
	}
	return g
}

// specSeparator finds the `@` that divides a yarn entry's name from its range.
//
// The *first* one after a leading scope, never the last. A Berry resolution
// embeds a second `name@range` inside itself —
//
//	"fsevents@patch:fsevents@npm%3A2.3.2#optional!builtin<compat/fsevents>":
//
// — so the last `@` sits inside the resolution and the name came out as
// `fsevents@patch:fsevents`: a package that does not exist, printed in a
// security report as the thing that pulled a vulnerable one in, while the real
// fsevents node got no edges at all. `patch:` entries are routine in Berry
// lockfiles, and `resolutions` overrides produce the same shape.
//
// Returns -1 when there is no separator. A leading `@` is a scope and is never
// it, which is why the search starts at one.
func specSeparator(spec string) int {
	if len(spec) < 2 {
		return -1
	}
	i := strings.IndexByte(spec[1:], '@')
	if i < 0 {
		return -1
	}
	return i + 1
}

// cutWord strips a leading word and the space after it, and reports whether
// the word was there as a word rather than as the start of a longer one.
func cutWord(s, word string) (string, bool) {
	if s == word {
		return "", true
	}
	if rest, ok := strings.CutPrefix(s, word+" "); ok {
		return strings.TrimSpace(rest), true
	}
	return s, false
}

// projectSource reports whether a pip-compile `# via` source is the project
// itself rather than another package.
//
// Two shapes: an input file (`-r requirements.in`, `-c constraints.txt`) and
// the project's own metadata, which pip-tools writes as the project name
// followed by the file it read — `myproject (pyproject.toml)`.
func projectSource(src string) bool {
	if strings.HasPrefix(src, "-r ") || strings.HasPrefix(src, "-c ") {
		return true
	}
	for _, f := range []string{"(pyproject.toml)", "(setup.py)", "(setup.cfg)"} {
		if strings.HasSuffix(src, f) {
			return true
		}
	}
	return false
}
